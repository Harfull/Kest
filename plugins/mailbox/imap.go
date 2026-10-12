package mailbox

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"
)

// This file is a small IMAP4rev1 client (RFC 3501): just what the plugin
// needs, over a connection that is always encrypted. It is written against the
// standard library on purpose; see docs/decisions/0009-mail-on-the-standard-library.md.

// dialFunc opens a connection under the plugin's network policy.
type dialFunc func(ctx context.Context, network, address string) (net.Conn, error)

// opTimeout bounds one whole mail operation: connect, log in, work, log out.
const opTimeout = 40 * time.Second

// maxLiteral bounds one literal a server sends, so a hostile or broken server
// cannot make the process allocate without limit.
const maxLiteral = 8 << 20

// imapClient is one connection. It is not safe for concurrent use.
type imapClient struct {
	conn net.Conn
	r    *bufio.Reader
	n    int
}

// resp is one parsed response line. Tokens are strings (atoms and quoted
// strings), []byte (literals) or []any (parenthesized lists).
type resp struct {
	tag    string // "*" untagged, "+" continuation, otherwise the command tag
	tokens []any
	text   string // for status responses, the human text
}

func (r resp) word(i int) string {
	if i < len(r.tokens) {
		if s, ok := r.tokens[i].(string); ok {
			return s
		}
	}
	return ""
}

func dialIMAP(ctx context.Context, dial dialFunc, host string, port int, mode string, base *tls.Config) (*imapClient, error) {
	conn, err := dial(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return nil, fmt.Errorf("connect to the IMAP server: %w", err)
	}
	deadline(ctx, conn)
	if mode == "implicit" {
		conn, err = startTLS(ctx, conn, host, base)
		if err != nil {
			return nil, err
		}
	}
	c := &imapClient{conn: conn, r: bufio.NewReaderSize(conn, 64<<10)}
	greeting, err := c.readResponse()
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("read the IMAP greeting: %w", err)
	}
	if greeting.tag != "*" || (greeting.word(0) != "OK" && greeting.word(0) != "PREAUTH") {
		_ = conn.Close()
		return nil, errors.New("the IMAP server refused the connection")
	}
	if mode == "starttls" {
		if _, err := c.do(ctx, []any{"STARTTLS"}); err != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("start TLS: %w", err)
		}
		tc, err := startTLS(ctx, conn, host, base)
		if err != nil {
			return nil, err
		}
		c.conn, c.r = tc, bufio.NewReaderSize(tc, 64<<10)
	}
	return c, nil
}

// startTLS wraps conn, verifying the server's certificate against its name.
// There is no option to skip verification; base only lets tests trust a test
// certificate authority.
func startTLS(ctx context.Context, conn net.Conn, host string, base *tls.Config) (net.Conn, error) {
	cfg := &tls.Config{}
	if base != nil {
		cfg = base.Clone()
	}
	cfg.ServerName, cfg.MinVersion = host, tls.VersionTLS12
	tc := tls.Client(conn, cfg)
	if err := tc.HandshakeContext(ctx); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("secure the connection (TLS): %w", err)
	}
	return tc, nil
}

func deadline(ctx context.Context, conn net.Conn) {
	d := time.Now().Add(opTimeout)
	if cd, ok := ctx.Deadline(); ok && cd.Before(d) {
		d = cd
	}
	_ = conn.SetDeadline(d)
}

// close says goodbye and drops the connection. It must not wait on a cancelled
// context, so it keeps its own short deadline.
func (c *imapClient) close(ctx context.Context) {
	deadline(context.WithoutCancel(ctx), c.conn)
	_, _ = c.conn.Write([]byte(c.nextTag() + " LOGOUT\r\n"))
	_ = c.conn.Close()
}

func (c *imapClient) nextTag() string {
	c.n++
	return "k" + strconv.Itoa(c.n)
}

// literal is a part of a command sent as an IMAP literal.
type literal []byte

// astring renders s as a quoted string when it is plain printable ASCII and as
// a literal otherwise, so no value can break out of its argument.
func astring(s string) any {
	for _, r := range s {
		if r < 0x20 || r > 0x7e {
			return literal(s)
		}
	}
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

// do sends a command and returns the untagged responses that came before its
// tagged completion. parts are raw text (strings) and literals.
func (c *imapClient) do(ctx context.Context, parts []any) ([]resp, error) {
	deadline(ctx, c.conn)
	tag := c.nextTag()
	var line strings.Builder
	line.WriteString(tag)
	flush := func() error {
		_, err := io.WriteString(c.conn, line.String())
		line.Reset()
		return err
	}
	for _, p := range parts {
		switch v := p.(type) {
		case string:
			line.WriteString(" " + v)
		case literal:
			fmt.Fprintf(&line, " {%d}\r\n", len(v))
			if err := flush(); err != nil {
				return nil, fmt.Errorf("write command: %w", err)
			}
			cont, err := c.readResponse()
			if err != nil || cont.tag != "+" {
				return nil, errors.New("the IMAP server did not accept the command")
			}
			if _, err := c.conn.Write(v); err != nil {
				return nil, fmt.Errorf("write command: %w", err)
			}
		}
	}
	line.WriteString("\r\n")
	if err := flush(); err != nil {
		return nil, fmt.Errorf("write command: %w", err)
	}

	var untagged []resp
	for {
		r, err := c.readResponse()
		if err != nil {
			return nil, fmt.Errorf("read the IMAP response: %w", err)
		}
		switch r.tag {
		case "*":
			untagged = append(untagged, r)
		case tag:
			switch r.word(0) {
			case "OK":
				return untagged, nil
			case "NO":
				return nil, fmt.Errorf("the IMAP server said no: %s", clipText(r.text))
			}
			return nil, fmt.Errorf("the IMAP server rejected the command: %s", clipText(r.text))
		}
	}
}

func clipText(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 120 {
		return s[:120] + "…"
	}
	return s
}

// --- response parsing --------------------------------------------------------------

// readResponse reads one complete response, including any literals.
func (c *imapClient) readResponse() (resp, error) {
	first, err := c.readAtom()
	if err != nil {
		return resp{}, err
	}
	r := resp{tag: first}
	if first == "+" {
		_, err := c.readLine()
		return r, err
	}
	// Status responses carry free text, which must not be parsed as tokens.
	word, err := c.readAtom()
	if err != nil {
		return resp{}, err
	}
	switch strings.ToUpper(word) {
	case "OK", "NO", "BAD", "BYE", "PREAUTH":
		rest, err := c.readLine()
		r.tokens, r.text = []any{strings.ToUpper(word)}, rest
		return r, err
	}
	// "* 12 FETCH (...)", "* SEARCH 1 2 3", "* CAPABILITY ...", "* LIST ...".
	r.tokens = append(r.tokens, strings.ToUpper(word))
	if _, err := strconv.Atoi(word); err == nil {
		kind, err := c.readAtom()
		if err != nil {
			return resp{}, err
		}
		r.tokens = []any{strings.ToUpper(kind), word}
	}
	for {
		tok, done, err := c.readToken()
		if err != nil {
			return resp{}, err
		}
		if done {
			return r, nil
		}
		r.tokens = append(r.tokens, tok)
	}
}

// readLine reads up to and including CRLF and returns the text without it.
func (c *imapClient) readLine() (string, error) {
	line, err := c.r.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

// readAtom reads one space-delimited word.
func (c *imapClient) readAtom() (string, error) {
	if err := c.skipSpaces(); err != nil {
		return "", err
	}
	var b strings.Builder
	for {
		ch, err := c.r.ReadByte()
		if err != nil {
			return "", err
		}
		if ch == ' ' || ch == '\r' || ch == '\n' {
			_ = c.r.UnreadByte()
			return b.String(), nil
		}
		b.WriteByte(ch)
		if b.Len() > 4096 {
			return "", errors.New("response word too long")
		}
	}
}

func (c *imapClient) skipSpaces() error {
	for {
		ch, err := c.r.ReadByte()
		if err != nil {
			return err
		}
		if ch != ' ' {
			return c.r.UnreadByte()
		}
	}
}

// readToken reads the next token of a response. done is true at the end of the
// line, which it consumes.
func (c *imapClient) readToken() (tok any, done bool, err error) {
	if err := c.skipSpaces(); err != nil {
		return nil, false, err
	}
	ch, err := c.r.ReadByte()
	if err != nil {
		return nil, false, err
	}
	switch ch {
	case '\r':
		if next, err := c.r.ReadByte(); err != nil || next != '\n' {
			return nil, false, errors.New("malformed line ending")
		}
		return nil, true, nil
	case '\n':
		return nil, true, nil
	case '(':
		list := []any{}
		for {
			if err := c.skipSpaces(); err != nil {
				return nil, false, err
			}
			next, err := c.r.ReadByte()
			if err != nil {
				return nil, false, err
			}
			if next == ')' {
				return list, false, nil
			}
			_ = c.r.UnreadByte()
			item, end, err := c.readToken()
			if err != nil {
				return nil, false, err
			}
			if end {
				return nil, false, errors.New("unterminated list")
			}
			list = append(list, item)
		}
	case '"':
		var b strings.Builder
		for {
			q, err := c.r.ReadByte()
			if err != nil {
				return nil, false, err
			}
			switch q {
			case '"':
				return b.String(), false, nil
			case '\\':
				if q, err = c.r.ReadByte(); err != nil {
					return nil, false, err
				}
			}
			b.WriteByte(q)
			if b.Len() > 1<<20 {
				return nil, false, errors.New("quoted string too long")
			}
		}
	case '{':
		numText, err := c.r.ReadString('}')
		if err != nil {
			return nil, false, err
		}
		n, err := strconv.Atoi(strings.TrimRight(strings.TrimSuffix(numText, "}"), "+"))
		if err != nil || n < 0 || n > maxLiteral {
			return nil, false, errors.New("unacceptable literal size")
		}
		if _, err := c.readLine(); err != nil { // the CRLF after {n}
			return nil, false, err
		}
		data := make([]byte, n)
		if _, err := io.ReadFull(c.r, data); err != nil {
			return nil, false, err
		}
		return data, false, nil
	}
	// An atom. A [section] may contain spaces and parentheses, as in
	// BODY[HEADER.FIELDS (FROM TO)], and belongs to the same atom.
	var b strings.Builder
	b.WriteByte(ch)
	bracket := ch == '['
	for {
		next, err := c.r.ReadByte()
		if err != nil {
			return nil, false, err
		}
		switch {
		case next == '[':
			bracket = true
		case next == ']':
			bracket = false
		case !bracket && (next == ' ' || next == ')' || next == '(' || next == '\r' || next == '\n'):
			_ = c.r.UnreadByte()
			return b.String(), false, nil
		}
		b.WriteByte(next)
		if b.Len() > 4096 {
			return nil, false, errors.New("response atom too long")
		}
	}
}

// --- commands ----------------------------------------------------------------------

func (c *imapClient) login(ctx context.Context, user, pass string) error {
	if _, err := c.do(ctx, []any{"LOGIN", astring(user), astring(pass)}); err != nil {
		return fmt.Errorf("log in: %w", err)
	}
	return nil
}

func (c *imapClient) selectFolder(ctx context.Context, folder string, readOnly bool) error {
	cmd := "SELECT"
	if readOnly {
		cmd = "EXAMINE"
	}
	if _, err := c.do(ctx, []any{cmd, astring(folder)}); err != nil {
		return fmt.Errorf("open the folder: %w", err)
	}
	return nil
}

// uidSearch returns the matching UIDs, ascending. charset is added when the
// criteria contain non-ASCII text.
func (c *imapClient) uidSearch(ctx context.Context, criteria []any) ([]uint32, error) {
	parts := []any{"UID", "SEARCH"}
	for _, p := range criteria {
		if l, ok := p.(literal); ok && !isASCII(string(l)) {
			parts = append(parts, "CHARSET", "UTF-8")
			break
		}
	}
	rs, err := c.do(ctx, append(parts, criteria...))
	if err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}
	var out []uint32
	for _, r := range rs {
		if r.word(0) != "SEARCH" {
			continue
		}
		for _, t := range r.tokens[1:] {
			if s, ok := t.(string); ok {
				if n, err := strconv.ParseUint(s, 10, 32); err == nil {
					out = append(out, uint32(n))
				}
			}
		}
	}
	return out, nil
}

func isASCII(s string) bool {
	for _, r := range s {
		if r > 0x7e {
			return false
		}
	}
	return true
}

// fetched is what a FETCH returned for one message.
type fetched struct {
	uid    uint32
	seen   bool
	header []byte
	body   []byte
}

// uidFetch fetches the given items for a set of UIDs. items is the text inside
// the parentheses, for example "UID FLAGS BODY.PEEK[HEADER.FIELDS (FROM)]".
func (c *imapClient) uidFetch(ctx context.Context, uids []uint32, items string) ([]fetched, error) {
	if len(uids) == 0 {
		return nil, nil
	}
	set := make([]string, len(uids))
	for i, u := range uids {
		set[i] = strconv.FormatUint(uint64(u), 10)
	}
	rs, err := c.do(ctx, []any{"UID", "FETCH", strings.Join(set, ","), "(" + items + ")"})
	if err != nil {
		return nil, fmt.Errorf("fetch: %w", err)
	}
	var out []fetched
	for _, r := range rs {
		if r.word(0) != "FETCH" || len(r.tokens) < 3 {
			continue
		}
		list, ok := r.tokens[2].([]any)
		if !ok {
			continue
		}
		var f fetched
		for i := 0; i+1 < len(list); i += 2 {
			key, _ := list[i].(string)
			key = strings.ToUpper(key)
			switch {
			case key == "UID":
				if s, ok := list[i+1].(string); ok {
					if n, err := strconv.ParseUint(s, 10, 32); err == nil {
						f.uid = uint32(n)
					}
				}
			case key == "FLAGS":
				if flags, ok := list[i+1].([]any); ok {
					for _, fl := range flags {
						if s, _ := fl.(string); strings.EqualFold(s, `\Seen`) {
							f.seen = true
						}
					}
				}
			case strings.HasPrefix(key, "BODY[HEADER"):
				f.header = asBytes(list[i+1])
			case strings.HasPrefix(key, "BODY[]"):
				f.body = asBytes(list[i+1])
			}
		}
		if f.uid != 0 {
			out = append(out, f)
		}
	}
	return out, nil
}

func asBytes(v any) []byte {
	switch t := v.(type) {
	case []byte:
		return t
	case string:
		if t == "NIL" {
			return nil
		}
		return []byte(t)
	}
	return nil
}

func (c *imapClient) markSeen(ctx context.Context, uid uint32) error {
	_, err := c.do(ctx, []any{"UID", "STORE", strconv.FormatUint(uint64(uid), 10), "+FLAGS.SILENT", `(\Seen)`})
	if err != nil {
		return fmt.Errorf("mark as read: %w", err)
	}
	return nil
}

func (c *imapClient) appendMessage(ctx context.Context, folder, flags string, raw []byte) error {
	if _, err := c.do(ctx, []any{"APPEND", astring(folder), "(" + flags + ")", literal(raw)}); err != nil {
		return fmt.Errorf("save to %s: %w", folder, err)
	}
	return nil
}
