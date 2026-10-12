package mailkit

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"mime/quotedprintable"
	"net/mail"
	"strings"
	"time"
)

// Limits on what a model may ask to send.
const (
	MaxRecipients = 10
	maxSubject    = 200
	maxBody       = 20000
)

// ValidateAddresses parses recipients and returns bare addresses. A display
// name is dropped: the owner approves the address, not a label that could say
// anything. Header-injection attempts (line breaks) are refused outright.
func ValidateAddresses(list []string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	for _, raw := range list {
		if strings.ContainsAny(raw, "\r\n") {
			return nil, errors.New("an address contains a line break")
		}
		a, err := mail.ParseAddress(strings.TrimSpace(raw))
		if err != nil {
			return nil, fmt.Errorf("%q is not a valid email address", clip(plain(raw), 60))
		}
		key := strings.ToLower(a.Address)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, a.Address)
	}
	return out, nil
}

// Compose builds a plain-text email in RFC 5322 form. from is the account's own
// address; name is optional. Nothing here can add a Bcc or an attachment.
func Compose(from, name string, o Outgoing, now time.Time) ([]byte, error) {
	to, err := ValidateAddresses(o.To)
	if err != nil {
		return nil, err
	}
	cc, err := ValidateAddresses(o.Cc)
	if err != nil {
		return nil, err
	}
	if len(to) == 0 {
		return nil, ErrNoRecipients
	}
	if len(to)+len(cc) > MaxRecipients {
		return nil, fmt.Errorf("at most %d recipients are allowed", MaxRecipients)
	}
	if strings.ContainsAny(o.Subject, "\r\n") || len([]rune(o.Subject)) > maxSubject {
		return nil, fmt.Errorf("the subject must be one line of at most %d characters", maxSubject)
	}
	if len([]rune(o.Body)) > maxBody {
		return nil, fmt.Errorf("the body must be at most %d characters", maxBody)
	}
	sender, err := mail.ParseAddress(from)
	if err != nil {
		return nil, errors.New("the account's from address is not valid")
	}
	if name = oneLine(name); name != "" {
		sender.Name = name
	}

	subject := o.Subject
	if o.Reply != nil && strings.TrimSpace(subject) == "" {
		subject = "Re: " + strings.TrimPrefix(strings.TrimPrefix(o.Reply.Subject, "Re: "), "RE: ")
	}

	id := make([]byte, 12)
	if _, err := rand.Read(id); err != nil {
		return nil, fmt.Errorf("generate message id: %w", err)
	}
	domain := "localhost"
	if _, d, ok := strings.Cut(sender.Address, "@"); ok && d != "" {
		domain = d
	}

	var b bytes.Buffer
	h := func(k, v string) { b.WriteString(k + ": " + v + "\r\n") }
	h("From", sender.String())
	h("To", strings.Join(to, ", "))
	if len(cc) > 0 {
		h("Cc", strings.Join(cc, ", "))
	}
	h("Subject", mime.QEncoding.Encode("utf-8", subject))
	h("Date", now.Format(time.RFC1123Z))
	h("Message-ID", "<"+hex.EncodeToString(id)+"@"+domain+">")
	if o.Reply != nil && o.Reply.MessageID != "" {
		h("In-Reply-To", o.Reply.MessageID)
		refs := append(append([]string(nil), o.Reply.References...), o.Reply.MessageID)
		if len(refs) > 10 {
			refs = refs[len(refs)-10:]
		}
		h("References", strings.Join(refs, " "))
	}
	h("MIME-Version", "1.0")
	h("Content-Type", `text/plain; charset="utf-8"`)
	h("Content-Transfer-Encoding", "quoted-printable")
	b.WriteString("\r\n")

	qp := quotedprintable.NewWriter(&b)
	body := strings.ReplaceAll(strings.ReplaceAll(o.Body, "\r\n", "\n"), "\n", "\r\n")
	if _, err := qp.Write([]byte(body)); err != nil {
		return nil, fmt.Errorf("encode body: %w", err)
	}
	if err := qp.Close(); err != nil {
		return nil, fmt.Errorf("encode body: %w", err)
	}
	return b.Bytes(), nil
}
