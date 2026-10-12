package mailkit

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"strings"

	"golang.org/x/net/html"
)

// maxParts bounds how many MIME parts are looked at, so a hostile message
// cannot make parsing expensive. maxDepth bounds nesting.
const (
	maxParts = 40
	maxDepth = 6
	// maxPartBytes bounds the decoded size of one text part.
	maxPartBytes = 1 << 20
)

// windows1252 maps the bytes 0x80 to 0x9F, where it differs from Latin-1.
var windows1252 = [32]rune{
	0x20AC, 0x81, 0x201A, 0x0192, 0x201E, 0x2026, 0x2020, 0x2021, 0x02C6, 0x2030, 0x0160, 0x2039, 0x0152, 0x8D, 0x017D, 0x8F,
	0x90, 0x2018, 0x2019, 0x201C, 0x201D, 0x2022, 0x2013, 0x2014, 0x02DC, 0x2122, 0x0161, 0x203A, 0x0153, 0x9D, 0x017E, 0x0178,
}

// toUTF8 converts text in a common charset to UTF-8. Unknown charsets are
// treated as UTF-8 with invalid bytes replaced, which is right far more often
// than it is wrong and never fails.
func toUTF8(charset string, data []byte) string {
	switch strings.ToLower(strings.TrimSpace(charset)) {
	case "iso-8859-1", "latin1", "iso8859-1", "us-ascii-8bit":
		return latin1(data, false)
	case "windows-1252", "cp1252", "iso-8859-15":
		return latin1(data, true)
	}
	return strings.ToValidUTF8(string(data), "�")
}

func latin1(data []byte, cp1252 bool) string {
	var b strings.Builder
	for _, c := range data {
		if cp1252 && c >= 0x80 && c <= 0x9F {
			b.WriteRune(windows1252[c-0x80])
			continue
		}
		b.WriteRune(rune(c))
	}
	return b.String()
}

var wordDecoder = &mime.WordDecoder{CharsetReader: func(charset string, r io.Reader) (io.Reader, error) {
	data, err := io.ReadAll(io.LimitReader(r, 1<<16))
	if err != nil {
		return nil, err
	}
	return strings.NewReader(toUTF8(charset, data)), nil
}}

// decodeHeader decodes RFC 2047 words, falling back to the raw text.
func decodeHeader(s string) string {
	if d, err := wordDecoder.DecodeHeader(s); err == nil {
		return oneLine(d)
	}
	return oneLine(s)
}

// formatAddress renders an address as "Name <addr>" with the name decoded.
func formatAddress(a *mail.Address) string {
	if a == nil {
		return ""
	}
	name := decodeHeader(a.Name)
	if name == "" {
		return a.Address
	}
	return name + " <" + a.Address + ">"
}

func addressList(h mail.Header, key string) []string {
	raw := h.Get(key)
	if raw == "" {
		return nil
	}
	list, err := mail.ParseAddressList(raw)
	if err != nil {
		return []string{oneLine(raw)}
	}
	out := make([]string, 0, len(list))
	for _, a := range list {
		out = append(out, formatAddress(a))
	}
	return out
}

// ParseHeader parses the header lines of a message, with no body, into the
// fields a search result needs.
func ParseHeader(raw []byte) (Summary, error) {
	msg, err := mail.ReadMessage(bytes.NewReader(append(bytes.TrimRight(raw, "\r\n"), "\r\n\r\n"...)))
	if err != nil {
		return Summary{}, fmt.Errorf("parse message header: %w", err)
	}
	return summaryOf(msg.Header), nil
}

func summaryOf(h mail.Header) Summary {
	s := Summary{Subject: decodeHeader(h.Get("Subject"))}
	if from := h.Get("From"); from != "" {
		if a, err := mail.ParseAddress(from); err == nil {
			s.From = formatAddress(a)
		} else {
			s.From = oneLine(from)
		}
	}
	if t, err := h.Date(); err == nil {
		s.Date = t
	}
	return s
}

// Parse parses a whole message. Text is at most maxText runes.
func Parse(raw []byte, maxText int) (Message, error) {
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return Message{}, fmt.Errorf("parse message: %w", err)
	}
	out := Message{Summary: summaryOf(msg.Header)}
	out.To = addressList(msg.Header, "To")
	out.Cc = addressList(msg.Header, "Cc")
	out.MessageID = strings.TrimSpace(msg.Header.Get("Message-Id"))
	out.References = strings.Fields(msg.Header.Get("References"))

	w := &walker{}
	w.walk(msg.Header.Get("Content-Type"), msg.Header.Get("Content-Transfer-Encoding"), msg.Header.Get("Content-Disposition"), msg.Body, 0)
	text := w.plain
	if strings.TrimSpace(text) == "" {
		text = w.html
	}
	text = tidy(text)
	if r := []rune(text); maxText > 0 && len(r) > maxText {
		text = string(r[:maxText]) + "…[cut]"
	}
	out.Text = text
	out.Attachments = w.attachments
	out.Snippet = clip(oneLine(text), 160)
	return out, nil
}

type walker struct {
	plain, html string
	attachments []string
	parts       int
}

func (w *walker) walk(contentType, encoding, disposition string, body io.Reader, depth int) {
	if w.parts++; w.parts > maxParts || depth > maxDepth {
		return
	}
	mt, params, err := mime.ParseMediaType(contentType)
	if err != nil || mt == "" {
		mt, params = "text/plain", map[string]string{}
	}
	disp, dparams, _ := mime.ParseMediaType(disposition)

	if strings.HasPrefix(mt, "multipart/") {
		mr := multipart.NewReader(body, params["boundary"])
		for {
			p, err := mr.NextPart()
			if err != nil {
				return
			}
			w.walk(p.Header.Get("Content-Type"), p.Header.Get("Content-Transfer-Encoding"), p.Header.Get("Content-Disposition"), p, depth+1)
		}
	}

	name := dparams["filename"]
	if name == "" {
		name = params["name"]
	}
	if disp == "attachment" || (name != "" && !strings.HasPrefix(mt, "text/")) {
		if name == "" {
			name = mt
		}
		if len(w.attachments) < 20 {
			w.attachments = append(w.attachments, clip(oneLine(decodeHeader(name)), 100))
		}
		return
	}
	if mt != "text/plain" && mt != "text/html" {
		return
	}
	data, err := io.ReadAll(io.LimitReader(decoder(encoding, body), maxPartBytes))
	if err != nil && len(data) == 0 {
		return
	}
	text := toUTF8(params["charset"], data)
	if mt == "text/html" {
		if w.html == "" {
			w.html = htmlText(text)
		}
		return
	}
	if w.plain != "" {
		w.plain += "\n"
	}
	w.plain += text
}

func decoder(encoding string, r io.Reader) io.Reader {
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "base64":
		return base64.NewDecoder(base64.StdEncoding, &stripNewlines{r})
	case "quoted-printable":
		return quotedprintable.NewReader(r)
	}
	return r
}

// stripNewlines drops line breaks and padding spaces inside base64 text, which
// the standard decoder does not tolerate everywhere.
type stripNewlines struct{ r io.Reader }

func (s *stripNewlines) Read(p []byte) (int, error) {
	n, err := s.r.Read(p)
	out := p[:0]
	for _, c := range p[:n] {
		if c != '\r' && c != '\n' && c != ' ' && c != '\t' {
			out = append(out, c)
		}
	}
	return len(out), err
}

// tidy trims the text and collapses long runs of blank lines.
func tidy(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = plainKeepNewlines(s)
	var b strings.Builder
	blank := 0
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimRight(line, " \t")
		if line == "" {
			if blank++; blank > 1 {
				continue
			}
		} else {
			blank = 0
		}
		b.WriteString(line + "\n")
	}
	return strings.TrimSpace(b.String())
}

func plainKeepNewlines(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, s)
}

var blockTags = map[string]bool{
	"p": true, "div": true, "br": true, "li": true, "tr": true, "h1": true, "h2": true, "h3": true,
	"h4": true, "h5": true, "h6": true, "blockquote": true, "table": true, "ul": true, "ol": true, "hr": true,
}

// htmlText turns an HTML body into readable text: no scripts, styles or tags,
// block elements on their own lines.
func htmlText(src string) string {
	z := html.NewTokenizer(strings.NewReader(src))
	var b strings.Builder
	skip := 0
	for {
		switch z.Next() {
		case html.ErrorToken:
			return b.String()
		case html.TextToken:
			if skip == 0 {
				b.WriteString(string(z.Text()))
			}
		case html.StartTagToken, html.SelfClosingTagToken:
			name, _ := z.TagName()
			tag := string(name)
			if tag == "script" || tag == "style" || tag == "head" {
				skip++
			}
			if blockTags[tag] {
				b.WriteString("\n")
			}
		case html.EndTagToken:
			name, _ := z.TagName()
			tag := string(name)
			if (tag == "script" || tag == "style" || tag == "head") && skip > 0 {
				skip--
			}
			if blockTags[tag] {
				b.WriteString("\n")
			}
		}
		if b.Len() > 4*maxPartBytes {
			return b.String()
		}
	}
}

// ErrNoRecipients is returned by Compose when a message has nobody to go to.
var ErrNoRecipients = errors.New("at least one recipient is required")
