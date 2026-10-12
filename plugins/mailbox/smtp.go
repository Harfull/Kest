package mailbox

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/textproto"
	"strconv"
	"strings"
)

// sendSMTP delivers one message over an encrypted SMTP connection, logging in
// first if a username is set. It never sends credentials in clear text: the
// connection is TLS from the start or upgraded with STARTTLS before AUTH, and
// a server that offers no STARTTLS is refused.
func sendSMTP(ctx context.Context, dial dialFunc, base *tls.Config, host string, port int, mode, user, pass, from string, rcpts []string, raw []byte) error {
	for _, addr := range append([]string{from}, rcpts...) {
		if addr == "" || strings.ContainsFunc(addr, func(r rune) bool { return r <= 0x20 || r == 0x7f || r == '<' || r == '>' }) {
			return errors.New("an address is not safe to put in an SMTP command")
		}
	}
	conn, err := dial(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return fmt.Errorf("connect to the SMTP server: %w", err)
	}
	deadline(ctx, conn)
	if mode == "implicit" {
		if conn, err = startTLS(ctx, conn, host, base); err != nil {
			return err
		}
	}
	tp := textproto.NewConn(conn)
	defer func() { _ = tp.Close() }() // the exchange has finished or failed already

	expect := func(code int) (string, error) {
		_, msg, err := tp.ReadResponse(code)
		if err != nil {
			return "", fmt.Errorf("the SMTP server answered: %s", clipText(err.Error()))
		}
		return msg, nil
	}
	cmd := func(code int, format string, args ...any) (string, error) {
		if err := tp.PrintfLine(format, args...); err != nil {
			return "", fmt.Errorf("write to the SMTP server: %w", err)
		}
		return expect(code)
	}

	if _, err := expect(220); err != nil {
		return err
	}
	ext, err := cmd(250, "EHLO kest.local")
	if err != nil {
		return err
	}
	if mode == "starttls" {
		if !strings.Contains(strings.ToUpper(ext), "STARTTLS") {
			return errors.New("the SMTP server does not offer STARTTLS, so it was not used with a password")
		}
		if _, err := cmd(220, "STARTTLS"); err != nil {
			return err
		}
		tc, err := startTLS(ctx, conn, host, base)
		if err != nil {
			return err
		}
		tp = textproto.NewConn(tc)
		if ext, err = cmd(250, "EHLO kest.local"); err != nil {
			return err
		}
	}

	if user != "" {
		if err := authenticate(cmd, ext, user, pass); err != nil {
			return err
		}
	}

	if _, err := cmd(250, "MAIL FROM:<%s>", from); err != nil {
		return err
	}
	for _, r := range rcpts {
		if _, err := cmd(250, "RCPT TO:<%s>", r); err != nil {
			return fmt.Errorf("recipient %s was refused: %w", r, err)
		}
	}
	if _, err := cmd(354, "DATA"); err != nil {
		return err
	}
	w := tp.DotWriter()
	if _, err := w.Write(raw); err != nil {
		return fmt.Errorf("write the message: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("finish the message: %w", err)
	}
	if _, err := expect(250); err != nil {
		return err
	}
	_ = tp.PrintfLine("QUIT")
	return nil
}

// authenticate logs in with PLAIN or, failing that, LOGIN.
func authenticate(cmd func(int, string, ...any) (string, error), ext, user, pass string) error {
	mechs := ""
	for _, line := range strings.Split(strings.ToUpper(ext), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "AUTH") {
			mechs += " " + strings.TrimSpace(line)
		}
	}
	b64 := func(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }
	switch {
	case strings.Contains(mechs, " PLAIN"):
		if _, err := cmd(235, "AUTH PLAIN %s", b64("\x00"+user+"\x00"+pass)); err != nil {
			return fmt.Errorf("log in: %w", err)
		}
	case strings.Contains(mechs, " LOGIN"):
		if _, err := cmd(334, "AUTH LOGIN"); err != nil {
			return fmt.Errorf("log in: %w", err)
		}
		if _, err := cmd(334, "%s", b64(user)); err != nil {
			return fmt.Errorf("log in: %w", err)
		}
		if _, err := cmd(235, "%s", b64(pass)); err != nil {
			return fmt.Errorf("log in: %w", err)
		}
	default:
		return errors.New("the SMTP server offers no login method this plugin supports (PLAIN or LOGIN)")
	}
	return nil
}
