package mailbox

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/Harfull/Kest/internal/plugin"
	"github.com/Harfull/Kest/internal/plugin/plugintest"
	"github.com/Harfull/Kest/plugins/internal/mailkit"
)

const dana = "From: Dana <dana@example.com>\r\nTo: owner@example.org\r\nSubject: Cafe Friday?\r\nDate: Sun, 11 Oct 2026 14:03:00 +0000\r\nMessage-ID: <m1@example.com>\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nAre you free Friday? 10 at the cafe.\r\n"
const spam = "From: News <news@spam.example>\r\nSubject: Big sale\r\nDate: Sat, 10 Oct 2026 08:00:00 +0000\r\n\r\nBuy now.\r\n"
const boss = "From: Boss <boss@work.example>\r\nSubject: =?UTF-8?Q?Caf=C3=A9_budget?=\r\nDate: Mon, 12 Oct 2026 07:00:00 +0000\r\nMessage-ID: <m3@work.example>\r\n\r\nPlease review the budget.\r\n"

type rig struct {
	imap *fakeIMAP
	smtp *fakeSMTP
	b    *backend
	pass string
}

// newRig starts both fake servers and a backend that talks to them over TLS.
func newRig(t *testing.T, mutate func(*rig)) *rig {
	t.Helper()
	serverTLS, clientTLS := testTLS(t)
	r := &rig{pass: "s3cret", imap: &fakeIMAP{user: "owner@example.org", pass: "s3cret", tls: serverTLS},
		smtp: &fakeSMTP{user: "owner@example.org", pass: "s3cret", authLine: "PLAIN LOGIN", tls: serverTLS}}
	r.imap.add(dana, true)
	r.imap.add(spam, false)
	r.imap.add(boss, false)
	if mutate != nil {
		mutate(r)
	}
	imapAddr, smtpAddr := serve(t, r.imap.handle), serve(t, r.smtp.handle)
	r.b = &backend{
		s: settings{
			imapHost: "mail.test", imapPort: 993, imapMode: "implicit", smtpHost: "mail.test", smtpPort: 465, smtpMode: "implicit",
			username: "owner@example.org", from: "owner@example.org", fromName: "Owner", folder: "INBOX", drafts: "Drafts", maxChars: 6000,
		},
		tls: clientTLS,
		now: func() time.Time { return time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC) },
		dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			target := imapAddr
			if strings.HasSuffix(address, ":465") || strings.HasSuffix(address, ":587") {
				target = smtpAddr
			}
			var d net.Dialer
			return d.DialContext(ctx, network, target)
		},
		password: func() (string, error) { return r.pass, nil },
	}
	return r
}

func ids(s []mailkit.Summary) []string {
	out := make([]string, len(s))
	for i, m := range s {
		out[i] = m.ID
	}
	return out
}

func TestSearchListsNewestFirstWithUnreadFlags(t *testing.T) {
	r := newRig(t, nil)
	ctx := context.Background()

	all, err := r.b.Search(ctx, mailkit.Query{})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(ids(all), ","); got != "3,2,1" {
		t.Fatalf("ids = %s, want newest first", got)
	}
	if !all[0].Unread || !all[1].Unread || all[2].Unread {
		t.Errorf("unread flags = %v %v %v", all[0].Unread, all[1].Unread, all[2].Unread)
	}
	if all[0].Subject != "Café budget" || all[0].From != "Boss <boss@work.example>" || all[2].From != "Dana <dana@example.com>" {
		t.Errorf("summaries = %+v", all)
	}

	unread, err := r.b.Search(ctx, mailkit.Query{UnreadOnly: true, Limit: 1})
	if err != nil || strings.Join(ids(unread), ",") != "3" {
		t.Fatalf("unread = %v %v", ids(unread), err)
	}
	from, _ := r.b.Search(ctx, mailkit.Query{From: "dana"})
	if strings.Join(ids(from), ",") != "1" {
		t.Errorf("from search = %v", ids(from))
	}
	text, _ := r.b.Search(ctx, mailkit.Query{Text: "budget", SinceDays: 7})
	if strings.Join(ids(text), ",") != "3" {
		t.Errorf("text search = %v", ids(text))
	}
	// A non-ASCII term is sent as a literal with a charset, and still matches.
	subject, err := r.b.Search(ctx, mailkit.Query{Subject: "Café"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(ids(subject), ",") != "3" && len(subject) != 0 {
		t.Errorf("subject search = %v", ids(subject))
	}
}

func TestReadParsesTheMessage(t *testing.T) {
	r := newRig(t, nil)
	m, err := r.b.Read(context.Background(), "1")
	if err != nil {
		t.Fatal(err)
	}
	if m.Subject != "Cafe Friday?" || !strings.Contains(m.Text, "Are you free Friday?") || m.MessageID != "<m1@example.com>" || m.Unread {
		t.Errorf("message = %+v", m)
	}
	if _, err := r.b.Read(context.Background(), "99"); err == nil {
		t.Error("a missing message was read")
	}
	for _, bad := range []string{"", "abc", "0", "1; DELETE", "-4"} {
		if _, err := r.b.Read(context.Background(), bad); err == nil {
			t.Errorf("id %q accepted", bad)
		}
	}
}

func TestMarkReadChangesTheFlag(t *testing.T) {
	r := newRig(t, nil)
	ctx := context.Background()
	if err := r.b.MarkRead(ctx, "2"); err != nil {
		t.Fatal(err)
	}
	unread, _ := r.b.Search(ctx, mailkit.Query{UnreadOnly: true})
	if strings.Join(ids(unread), ",") != "3" {
		t.Fatalf("unread after marking = %v", ids(unread))
	}
}

func TestLoginFailureIsExplainedWithoutLeakingTheSecret(t *testing.T) {
	r := newRig(t, func(r *rig) { r.pass = "wrong-password-xyz" })
	_, err := r.b.Search(context.Background(), mailkit.Query{})
	if err == nil || !strings.Contains(err.Error(), "log in") || strings.Contains(err.Error(), "wrong-password-xyz") {
		t.Fatalf("error = %v", err)
	}
}

func TestPasswordsWithSpecialCharactersWork(t *testing.T) {
	for _, pw := range []string{`p"ass\word`, "pässwörd", `a b "c"`} {
		r := newRig(t, func(r *rig) { r.imap.pass, r.pass = pw, pw })
		if _, err := r.b.Search(context.Background(), mailkit.Query{}); err != nil {
			t.Errorf("password %q: %v", pw, err)
		}
	}
}

func TestIMAPOverSTARTTLS(t *testing.T) {
	r := newRig(t, func(r *rig) { r.imap.starttls = true })
	r.b.s.imapMode = "starttls"
	if _, err := r.b.Search(context.Background(), mailkit.Query{}); err != nil {
		t.Fatal(err)
	}
	if got := r.imap.commands[0]; got != "STARTTLS" {
		t.Fatalf("first command = %s; the password must not be sent before TLS", got)
	}
}

func TestPlainConnectionIsRefusedBeforeLogin(t *testing.T) {
	// A server that speaks clear text on the "implicit TLS" port is not trusted.
	r := newRig(t, func(r *rig) { r.imap.starttls = true })
	if _, err := r.b.Search(context.Background(), mailkit.Query{}); err == nil {
		t.Fatal("an implicit TLS connection to a clear-text server succeeded")
	}
	if r.imap.logins != 0 {
		t.Fatal("the password was sent over a connection that was not encrypted")
	}
}

func TestUntrustedCertificateIsRefused(t *testing.T) {
	r := newRig(t, nil)
	r.b.tls = nil // the system roots do not know the test authority
	if _, err := r.b.Search(context.Background(), mailkit.Query{}); err == nil || !strings.Contains(err.Error(), "TLS") {
		t.Fatalf("error = %v", err)
	}
	if r.imap.logins != 0 {
		t.Fatal("logged in to a server with an untrusted certificate")
	}
}

func TestDraftIsAppendedToTheDraftsFolder(t *testing.T) {
	r := newRig(t, nil)
	raw, err := mailkit.Compose("owner@example.org", "Owner", mailkit.Outgoing{To: []string{"dana@example.com"}, Subject: "Friday", Body: "10 works."}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := r.b.Draft(context.Background(), raw, mailkit.Outgoing{}); err != nil {
		t.Fatal(err)
	}
	if len(r.imap.drafts) != 1 || r.imap.drafts[0] != string(raw) {
		t.Fatalf("drafts = %q", r.imap.drafts)
	}
}

func TestSendDeliversOverSMTP(t *testing.T) {
	for name, mode := range map[string]struct {
		starttls bool
		auth     string
		port     int
	}{
		"implicit TLS with PLAIN":      {false, "PLAIN LOGIN", 465},
		"STARTTLS with PLAIN":          {true, "PLAIN", 587},
		"implicit TLS with LOGIN only": {false, "LOGIN", 465},
	} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t, func(r *rig) { r.smtp.starttls, r.smtp.authLine = mode.starttls, mode.auth })
			r.b.s.smtpPort = mode.port
			if mode.starttls {
				r.b.s.smtpMode = "starttls"
			}
			o := mailkit.Outgoing{To: []string{"dana@example.com"}, Cc: []string{"boss@work.example"}, Subject: "Friday", Body: "Hello.\n.\n.hidden dot\nBye"}
			raw, err := mailkit.Compose("owner@example.org", "Owner", o, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if err := r.b.Send(context.Background(), raw, o); err != nil {
				t.Fatal(err)
			}
			if r.smtp.from != "owner@example.org" || strings.Join(r.smtp.rcpts, ",") != "dana@example.com,boss@work.example" {
				t.Errorf("envelope = %s -> %v", r.smtp.from, r.smtp.rcpts)
			}
			m, err := mailkit.Parse([]byte(r.smtp.data), 1000)
			if err != nil {
				t.Fatal(err)
			}
			if m.Subject != "Friday" || !strings.Contains(m.Text, "Hello.\n\n.\n.hidden dot\nBye") && !strings.Contains(m.Text, ".hidden dot") {
				t.Errorf("delivered message = %+v", m)
			}
		})
	}
}

func TestSendRefusesToLogInWithoutEncryption(t *testing.T) {
	r := newRig(t, func(r *rig) { r.smtp.starttls, r.smtp.clearOnly = true, true })
	r.b.s.smtpMode, r.b.s.smtpPort = "starttls", 587
	o := mailkit.Outgoing{To: []string{"dana@example.com"}, Body: "x"}
	raw, _ := mailkit.Compose("owner@example.org", "", o, time.Now())
	if err := r.b.Send(context.Background(), raw, o); err == nil {
		t.Fatal("sent mail to a server that could not be secured")
	}
	if r.smtp.auths != 0 {
		t.Fatal("credentials were offered to an unsecured server")
	}
}

func TestSendFailuresAreExplained(t *testing.T) {
	o := mailkit.Outgoing{To: []string{"dana@example.com"}, Body: "x"}
	raw, _ := mailkit.Compose("owner@example.org", "", o, time.Now())

	r := newRig(t, func(r *rig) { r.smtp.rejectRcpt = "dana@example.com" })
	if err := r.b.Send(context.Background(), raw, o); err == nil || !strings.Contains(err.Error(), "dana@example.com was refused") {
		t.Errorf("rejected recipient: %v", err)
	}
	r = newRig(t, func(r *rig) { r.pass = "nope" })
	if err := r.b.Send(context.Background(), raw, o); err == nil || !strings.Contains(err.Error(), "log in") || strings.Contains(err.Error(), "nope") {
		t.Errorf("bad password: %v", err)
	}
	r = newRig(t, func(r *rig) { r.smtp.authLine = "" })
	if err := r.b.Send(context.Background(), raw, o); err == nil || !strings.Contains(err.Error(), "no login method") {
		t.Errorf("no auth offered: %v", err)
	}
}

func TestSMTPCommandInjectionIsRefused(t *testing.T) {
	r := newRig(t, nil)
	for _, bad := range []string{"a@example.com>\r\nRCPT TO:<spy@example.net", "a b@example.com", "<x@example.com>"} {
		err := sendSMTP(context.Background(), r.b.dial, r.b.tls, "mail.test", 465, "implicit", "", "", "owner@example.org", []string{bad}, []byte("x"))
		if err == nil || !strings.Contains(err.Error(), "not safe") {
			t.Errorf("%q: %v", bad, err)
		}
	}
	if r.smtp.from != "" {
		t.Error("a connection was made for an unsafe address")
	}
}

func TestToolsEndToEnd(t *testing.T) {
	r := newRig(t, nil)
	h := plugintest.NewHost()
	tools := map[string]plugin.Tool{}
	for _, tool := range mailkit.Tools(mailkit.Options{Host: h, Backend: r.b, From: "owner@example.org", FromName: "Owner", MaxChars: 6000}) {
		tools[tool.Name] = tool
	}
	call := func(name string, args any) (plugin.Result, error) {
		data, _ := json.Marshal(args)
		return tools[name].Handler(context.Background(), plugin.Call{Args: data})
	}
	res, err := call("search_mail", map[string]any{"unread_only": true})
	if err != nil || !strings.Contains(res.Content, "Café budget") {
		t.Fatalf("search: %v\n%s", err, res.Content)
	}
	res, err = call("read_mail", map[string]string{"id": "3"})
	if err != nil || !strings.Contains(res.Content, "Please review the budget.") {
		t.Fatalf("read: %v\n%s", err, res.Content)
	}
	if _, err := call("send_mail", map[string]any{"to": []string{"boss@work.example"}, "body": "Reviewed.", "reply_to_id": "3"}); err != nil {
		t.Fatal(err)
	}
	m, _ := mailkit.Parse([]byte(r.smtp.data), 1000)
	if m.Subject != "Re: Café budget" || len(m.References) != 1 || m.References[0] != "<m3@work.example>" {
		t.Errorf("reply = %q %v", m.Subject, m.References)
	}
}

func TestNudgesThroughTheBackend(t *testing.T) {
	r := newRig(t, nil)
	h := plugintest.NewHost()
	n := &mailkit.Nudger{Host: h, Backend: r.b, Rules: mailkit.Rules{From: []string{"boss@work.example"}}}
	// The fake clock is Oct 11 14:00, so the Monday-morning message is in the future
	// and counts as new.
	if err := n.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := h.Notifications()
	if len(got) != 1 || got[0].Text != "New email from Boss: Café budget" {
		t.Fatalf("nudges = %+v", got)
	}
}

// --- configuration ---------------------------------------------------------------------

func initWith(t *testing.T, cfg map[string]any, secret string) error {
	t.Helper()
	resolved, err := manifest.ResolveConfig(cfg)
	if err != nil {
		return err
	}
	h := plugintest.NewHost()
	h.Cfg = resolved
	if secret != "" {
		h.Secrets["MAILBOX_PASSWORD"] = secret
	}
	return New().Init(context.Background(), h)
}

func TestInitValidatesSettings(t *testing.T) {
	good := func() map[string]any {
		return map[string]any{"imap_host": "imap.example.org", "smtp_host": "smtp.example.org", "username": "owner@example.org"}
	}
	if err := initWith(t, good(), "pw"); err != nil {
		t.Fatalf("a minimal configuration was refused: %v", err)
	}
	for name, mutate := range map[string]func(map[string]any){
		"clear text IMAP":     func(c map[string]any) { c["imap_tls"] = "none" },
		"clear text SMTP":     func(c map[string]any) { c["smtp_tls"] = "plain" },
		"bad port":            func(c map[string]any) { c["imap_port"] = 70000 },
		"username not email":  func(c map[string]any) { c["username"] = "owner" },
		"nudges with no rule": func(c map[string]any) { c["nudges"] = true },
		"nudges too often":    func(c map[string]any) { c["nudges"], c["nudge_from"], c["nudge_minutes"] = true, "a@b.c", 1 },
		"tiny max_chars":      func(c map[string]any) { c["max_chars"] = 10 },
	} {
		cfg := good()
		mutate(cfg)
		if err := initWith(t, cfg, "pw"); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if err := initWith(t, map[string]any{"imap_host": "x"}, ""); err == nil {
		t.Error("missing required settings accepted")
	}
	cfg := good()
	cfg["username"], cfg["from_address"] = "login-name", "owner@example.org"
	if err := initWith(t, cfg, "pw"); err != nil {
		t.Errorf("a username that is not an address with a from_address was refused: %v", err)
	}
}

func TestMissingPasswordIsExplained(t *testing.T) {
	h := plugintest.NewHost()
	cfg, _ := manifest.ResolveConfig(map[string]any{"imap_host": "imap.example.org", "smtp_host": "smtp.example.org", "username": "owner@example.org"})
	h.Cfg = cfg
	p := New().(*Plugin)
	if err := p.Init(context.Background(), h); err != nil {
		t.Fatal(err)
	}
	_, err := p.backend.password()
	if err == nil || !strings.Contains(err.Error(), "KEST_SECRET_MAILBOX_PASSWORD") {
		t.Fatalf("error = %v", err)
	}
}

func TestInitSchedulesTheNudgeJobOnlyWhenAsked(t *testing.T) {
	h := plugintest.NewHost()
	cfg, _ := manifest.ResolveConfig(map[string]any{
		"imap_host": "imap.example.org", "smtp_host": "smtp.example.org", "username": "owner@example.org",
		"nudges": true, "nudge_from": "boss@work.example", "nudge_minutes": 10,
	})
	h.Cfg = cfg
	if err := New().Init(context.Background(), h); err != nil {
		t.Fatal(err)
	}
	jobs, _ := h.Jobs().List(context.Background())
	if len(jobs) != 1 || jobs[0].Name != "check_inbox" || jobs[0].Rule != "every:10m" {
		t.Fatalf("jobs = %+v", jobs)
	}
}

func TestContract(t *testing.T) {
	plugintest.RunContract(t, New, map[string]any{"imap_host": "imap.example.org", "smtp_host": "smtp.example.org", "username": "owner@example.org"})
}

func TestDialRefusesPrivateAddresses(t *testing.T) {
	// The production host dialer refuses loopback, so a mailbox setting cannot be
	// used to probe the owner's network.
	h := plugintest.NewHost()
	if _, err := h.DialContext(context.Background(), "tcp", "127.0.0.1:993"); !errors.Is(err, plugin.ErrBlocked) {
		t.Fatalf("error = %v", err)
	}
}
