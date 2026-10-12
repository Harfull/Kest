package mailbox

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"fmt"
	"io"
	"math/big"
	"net"

	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// testTLS makes a certificate for mail.test that is its own authority, and the
// client config that trusts it.
func testTLS(t *testing.T) (server, client *tls.Config) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "mail.test"}, DNSNames: []string{"mail.test"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true, IsCA: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	return &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}, MinVersion: tls.VersionTLS12},
		&tls.Config{RootCAs: pool}
}

// serve accepts connections until the test ends and runs handle on each.
func serve(t *testing.T, handle func(net.Conn)) string {
	t.Helper()
	l, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close() //nolint:errcheck // test server
				_ = c.SetDeadline(time.Now().Add(10 * time.Second))
				handle(c)
			}()
		}
	}()
	return l.Addr().String()
}

// --- fake IMAP server ------------------------------------------------------------------

type storedMail struct {
	uid  int
	raw  string
	seen bool
}

type fakeIMAP struct {
	mu       sync.Mutex
	user     string
	pass     string
	mail     []*storedMail
	drafts   []string
	selected string
	logins   int
	// starttls makes the server start in clear text and upgrade, as port 143 does.
	starttls bool
	tls      *tls.Config
	// commands records every command line the server received, with literals elided.
	commands []string
}

func (f *fakeIMAP) add(raw string, seen bool) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	uid := len(f.mail) + 1
	f.mail = append(f.mail, &storedMail{uid: uid, raw: raw, seen: seen})
	return uid
}

func (f *fakeIMAP) record(cmd string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.commands = append(f.commands, cmd)
}

func (f *fakeIMAP) handle(c net.Conn) {
	if !f.starttls {
		c = tls.Server(c, f.tls)
	}
	r := bufio.NewReader(c)
	write := func(s string) { _, _ = io.WriteString(c, s) }
	write("* OK [CAPABILITY IMAP4rev1] ready\r\n")
	authed := false
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		tag, rest, _ := strings.Cut(line, " ")
		// Pull in literals: "... {n}" asks for n more bytes after a continuation.
		var literals []string
		for strings.HasSuffix(rest, "}") {
			i := strings.LastIndex(rest, "{")
			n, err := strconv.Atoi(rest[i+1 : len(rest)-1])
			if i < 0 || err != nil {
				break
			}
			write("+ go ahead\r\n")
			buf := make([]byte, n)
			if _, err := io.ReadFull(r, buf); err != nil {
				return
			}
			literals = append(literals, string(buf))
			more, _ := r.ReadString('\n')
			rest = rest[:i] + "\x00" + strings.TrimRight(more, "\r\n")
		}
		cmd, args, _ := strings.Cut(rest, " ")
		f.record(strings.ToUpper(cmd))

		switch strings.ToUpper(cmd) {
		case "STARTTLS":
			write(tag + " OK begin TLS now\r\n")
			c = tls.Server(c, f.tls)
			r = bufio.NewReader(c)
		case "LOGIN":
			f.mu.Lock()
			f.logins++
			f.mu.Unlock()
			user, pass := f.credentials(args, literals)
			if user != f.user || pass != f.pass {
				write(tag + " NO [AUTHENTICATIONFAILED] invalid credentials\r\n")
				continue
			}
			authed = true
			write(tag + " OK logged in\r\n")
		case "SELECT", "EXAMINE":
			if !authed {
				write(tag + " BAD log in first\r\n")
				continue
			}
			f.mu.Lock()
			f.selected = strings.Trim(args, `"`)
			n := len(f.mail)
			f.mu.Unlock()
			write(fmt.Sprintf("* %d EXISTS\r\n%s OK [READ-WRITE] done\r\n", n, tag))
		case "UID":
			f.uid(write, tag, args, literals)
		case "APPEND":
			f.mu.Lock()
			if len(literals) > 0 {
				f.drafts = append(f.drafts, literals[len(literals)-1])
			}
			f.mu.Unlock()
			write(tag + " OK appended\r\n")
		case "LOGOUT":
			write("* BYE bye\r\n" + tag + " OK done\r\n")
			return
		default:
			write(tag + " BAD unknown command\r\n")
		}
	}
}

func (f *fakeIMAP) uid(write func(string), tag, args string, literals []string) {
	sub, rest, _ := strings.Cut(args, " ")
	f.mu.Lock()
	defer f.mu.Unlock()
	switch strings.ToUpper(sub) {
	case "SEARCH":
		rest = strings.TrimPrefix(rest, "CHARSET UTF-8 ")
		var hits []string
		for _, m := range f.mail {
			if f.matches(m, rest, literals) {
				hits = append(hits, strconv.Itoa(m.uid))
			}
		}
		write("* SEARCH " + strings.Join(hits, " ") + "\r\n" + tag + " OK done\r\n")
	case "FETCH":
		set, items, _ := strings.Cut(rest, " ")
		for _, idText := range strings.Split(set, ",") {
			id, _ := strconv.Atoi(idText)
			for _, m := range f.mail {
				if m.uid != id {
					continue
				}
				flags := ""
				if m.seen {
					flags = `\Seen`
				}
				var b strings.Builder
				fmt.Fprintf(&b, "* %d FETCH (UID %d FLAGS (%s)", m.uid, m.uid, flags)
				head, _, _ := strings.Cut(m.raw, "\r\n\r\n")
				if strings.Contains(items, "HEADER.FIELDS") {
					fmt.Fprintf(&b, " BODY[HEADER.FIELDS (FROM SUBJECT DATE MESSAGE-ID)] {%d}\r\n%s\r\n\r\n", len(head)+4, head)
				}
				if strings.Contains(items, "BODY.PEEK[]") {
					fmt.Fprintf(&b, " BODY[]<0> {%d}\r\n%s", len(m.raw), m.raw)
				}
				b.WriteString(")\r\n")
				write(b.String())
			}
		}
		write(tag + " OK done\r\n")
	case "STORE":
		idText, _, _ := strings.Cut(rest, " ")
		id, _ := strconv.Atoi(idText)
		for _, m := range f.mail {
			if m.uid == id {
				m.seen = true
			}
		}
		write(tag + " OK done\r\n")
	default:
		write(tag + " BAD unknown\r\n")
	}
}

// matches evaluates the few search keys the plugin uses.
func (f *fakeIMAP) matches(m *storedMail, criteria string, literals []string) bool {
	toks := strings.Fields(criteria)
	lit := 0
	arg := func(i int) string {
		if toks[i] == "\x00" {
			v := literals[lit]
			lit++
			return v
		}
		return strings.Trim(toks[i], `"`)
	}
	low := strings.ToLower(m.raw)
	head := strings.ToLower(strings.SplitN(m.raw, "\r\n\r\n", 2)[0])
	for i := 0; i < len(toks); i++ {
		switch strings.ToUpper(toks[i]) {
		case "UNSEEN":
			if m.seen {
				return false
			}
		case "FROM":
			i++
			if !strings.Contains(head, "from:") || !strings.Contains(headerLine(head, "from"), strings.ToLower(arg(i))) {
				return false
			}
		case "SUBJECT":
			i++
			if !strings.Contains(headerLine(head, "subject"), strings.ToLower(arg(i))) {
				return false
			}
		case "TEXT":
			i++
			if !strings.Contains(low, strings.ToLower(arg(i))) {
				return false
			}
		case "SINCE":
			i++
		}
	}
	return true
}

func headerLine(head, name string) string {
	for _, l := range strings.Split(head, "\r\n") {
		if strings.HasPrefix(l, name+":") {
			return l
		}
	}
	return ""
}

// --- fake SMTP server ------------------------------------------------------------------

type fakeSMTP struct {
	mu       sync.Mutex
	user     string
	pass     string
	starttls bool
	authLine string // "PLAIN LOGIN", or "" to offer no login method
	tls      *tls.Config

	from  string
	rcpts []string
	data  string
	auths int
	// rejectRcpt makes RCPT TO fail for this address.
	rejectRcpt string
	// clearOnly makes a clear-text server that never offers STARTTLS.
	clearOnly bool
}

func (f *fakeSMTP) handle(c net.Conn) {
	secure := !f.starttls
	if secure {
		c = tls.Server(c, f.tls)
	}
	r := bufio.NewReader(c)
	write := func(s string) { _, _ = io.WriteString(c, s) }
	write("220 mail.test ESMTP\r\n")
	authed := false
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		up := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(up, "EHLO"):
			ext := "250-mail.test\r\n"
			if secure && f.authLine != "" {
				ext += "250-AUTH " + f.authLine + "\r\n"
			}
			if !secure && !f.clearOnly {
				ext += "250-STARTTLS\r\n"
			}
			write(ext + "250 8BITMIME\r\n")
		case up == "STARTTLS":
			write("220 go ahead\r\n")
			c = tls.Server(c, f.tls)
			r = bufio.NewReader(c)
			secure = true
		case strings.HasPrefix(up, "AUTH PLAIN "):
			raw, _ := base64.StdEncoding.DecodeString(line[len("AUTH PLAIN "):])
			parts := strings.Split(string(raw), "\x00")
			f.mu.Lock()
			f.auths++
			f.mu.Unlock()
			if len(parts) == 3 && parts[1] == f.user && parts[2] == f.pass {
				authed = true
				write("235 ok\r\n")
			} else {
				write("535 bad credentials\r\n")
			}
		case up == "AUTH LOGIN":
			write("334 VXNlcm5hbWU6\r\n")
			u, _ := r.ReadString('\n')
			write("334 UGFzc3dvcmQ6\r\n")
			p, _ := r.ReadString('\n')
			du, _ := base64.StdEncoding.DecodeString(strings.TrimSpace(u))
			dp, _ := base64.StdEncoding.DecodeString(strings.TrimSpace(p))
			f.mu.Lock()
			f.auths++
			f.mu.Unlock()
			if string(du) == f.user && string(dp) == f.pass {
				authed = true
				write("235 ok\r\n")
			} else {
				write("535 bad credentials\r\n")
			}
		case strings.HasPrefix(up, "MAIL FROM:"):
			if !authed && f.user != "" {
				write("530 authentication required\r\n")
				continue
			}
			f.mu.Lock()
			f.from = strings.Trim(line[len("MAIL FROM:"):], "<>")
			f.rcpts = nil
			f.mu.Unlock()
			write("250 ok\r\n")
		case strings.HasPrefix(up, "RCPT TO:"):
			addr := strings.Trim(line[len("RCPT TO:"):], "<>")
			if addr == f.rejectRcpt {
				write("550 no such user\r\n")
				continue
			}
			f.mu.Lock()
			f.rcpts = append(f.rcpts, addr)
			f.mu.Unlock()
			write("250 ok\r\n")
		case up == "DATA":
			write("354 end with <CRLF>.<CRLF>\r\n")
			var b strings.Builder
			for {
				l, err := r.ReadString('\n')
				if err != nil {
					return
				}
				if l == ".\r\n" {
					break
				}
				b.WriteString(strings.TrimPrefix(l, "."))
			}
			f.mu.Lock()
			f.data = b.String()
			f.mu.Unlock()
			write("250 queued\r\n")
		case up == "QUIT":
			write("221 bye\r\n")
			return
		default:
			write("500 what\r\n")
		}
	}
}
