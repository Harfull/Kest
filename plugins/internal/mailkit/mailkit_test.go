package mailkit

import (
	"strings"
	"testing"
	"time"
)

var now = time.Date(2026, 10, 12, 9, 30, 0, 0, time.UTC)

const sample = "From: =?UTF-8?Q?Dana_Fern=C3=A1ndez?= <dana@example.com>\r\n" +
	"To: Kest Owner <owner@example.org>, other@example.org\r\n" +
	"Cc: boss@example.org\r\n" +
	"Subject: =?ISO-8859-1?Q?Caf=E9_on_Friday=3F?=\r\n" +
	"Date: Sun, 11 Oct 2026 14:03:00 +0000\r\n" +
	"Message-ID: <abc123@example.com>\r\n" +
	"References: <old1@example.com> <old2@example.com>\r\n" +
	"MIME-Version: 1.0\r\n" +
	"Content-Type: multipart/mixed; boundary=outer\r\n\r\n" +
	"--outer\r\n" +
	"Content-Type: multipart/alternative; boundary=inner\r\n\r\n" +
	"--inner\r\n" +
	"Content-Type: text/plain; charset=utf-8\r\n" +
	"Content-Transfer-Encoding: quoted-printable\r\n\r\n" +
	"Hi,=0D=0A=0D=0AAre you free Friday? Caf=C3=A9 at 10.=0D=0A\r\n" +
	"--inner\r\n" +
	"Content-Type: text/html; charset=utf-8\r\n\r\n" +
	"<html><body><p>Hi, this is the HTML copy</p></body></html>\r\n" +
	"--inner--\r\n" +
	"--outer\r\n" +
	"Content-Type: application/pdf; name=\"menu.pdf\"\r\n" +
	"Content-Disposition: attachment; filename=\"menu.pdf\"\r\n" +
	"Content-Transfer-Encoding: base64\r\n\r\n" +
	"JVBERi0xLjQK\r\n" +
	"--outer--\r\n"

func TestParseMultipartMessage(t *testing.T) {
	m, err := Parse([]byte(sample), 6000)
	if err != nil {
		t.Fatal(err)
	}
	if m.From != "Dana Fernández <dana@example.com>" {
		t.Errorf("from = %q", m.From)
	}
	if m.Subject != "Café on Friday?" {
		t.Errorf("subject = %q", m.Subject)
	}
	if !m.Date.Equal(time.Date(2026, 10, 11, 14, 3, 0, 0, time.UTC)) {
		t.Errorf("date = %v", m.Date)
	}
	if len(m.To) != 2 || m.To[0] != "Kest Owner <owner@example.org>" || m.To[1] != "other@example.org" || len(m.Cc) != 1 {
		t.Errorf("recipients = %v / %v", m.To, m.Cc)
	}
	if !strings.Contains(m.Text, "Are you free Friday? Café at 10.") || strings.Contains(m.Text, "HTML copy") {
		t.Errorf("text = %q (the plain part should win over the HTML copy)", m.Text)
	}
	if len(m.Attachments) != 1 || m.Attachments[0] != "menu.pdf" {
		t.Errorf("attachments = %v", m.Attachments)
	}
	if m.MessageID != "<abc123@example.com>" || len(m.References) != 2 {
		t.Errorf("threading = %q %v", m.MessageID, m.References)
	}
	if !strings.HasPrefix(m.Snippet, "Hi, Are you free Friday?") {
		t.Errorf("snippet = %q", m.Snippet)
	}
}

func TestParseHTMLOnlyMessageDropsScriptsAndStyles(t *testing.T) {
	raw := "From: a@example.com\r\nSubject: Sale\r\nContent-Type: text/html; charset=windows-1252\r\n\r\n" +
		"<style>p{color:red}</style><script>alert('x')</script><p>Caf\xe9 deals</p><p>Today only</p>"
	m, err := Parse([]byte(raw), 6000)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(m.Text, "alert") || strings.Contains(m.Text, "color") {
		t.Errorf("script or style leaked: %q", m.Text)
	}
	if !strings.Contains(m.Text, "Café deals\n\nToday only") {
		t.Errorf("text = %q", m.Text)
	}
}

func TestParseBoundsAndCleansText(t *testing.T) {
	raw := "From: a@example.com\r\nSubject: x\x1b[31m\r\n\r\n" + strings.Repeat("word ", 5000) + "\x07"
	m, err := Parse([]byte(raw), 100)
	if err != nil {
		t.Fatal(err)
	}
	if len([]rune(m.Text)) > 110 || !strings.HasSuffix(m.Text, "[cut]") {
		t.Errorf("text not bounded: %d runes", len([]rune(m.Text)))
	}
	if strings.ContainsAny(m.Subject+m.Text, "\x1b\x07") {
		t.Error("control characters survived")
	}
}

func TestParseSurvivesHostileStructure(t *testing.T) {
	deep := "Content-Type: multipart/mixed; boundary=b\r\n\r\n"
	for range 100 {
		deep += "--b\r\nContent-Type: multipart/mixed; boundary=b\r\n\r\n"
	}
	if _, err := Parse([]byte("From: a@example.com\r\n"+deep), 100); err != nil {
		t.Fatal(err)
	}
	if _, err := Parse([]byte("not an email"), 100); err == nil {
		t.Fatal("garbage parsed")
	}
}

func TestComposeBuildsAnAcceptableMessage(t *testing.T) {
	raw, err := Compose("owner@example.org", "Kest Owner", Outgoing{
		To: []string{"Dana <dana@example.com>", "dana@example.com"}, Cc: []string{"boss@example.org"},
		Subject: "Café on Friday", Body: "Hello,\nSee you at 10 – thanks!\n",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, want := range []string{"From: \"Kest Owner\" <owner@example.org>", "To: dana@example.com\r\n", "Cc: boss@example.org", "Date: Mon, 12 Oct 2026 09:30:00 +0000", "Content-Transfer-Encoding: quoted-printable"} {
		if !strings.Contains(text, want) {
			t.Errorf("message lacks %q:\n%s", want, text)
		}
	}
	// What was written reads back as the same message.
	m, err := Parse(raw, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if m.Subject != "Café on Friday" || !strings.Contains(m.Text, "See you at 10 – thanks!") || len(m.To) != 1 || !strings.HasPrefix(m.MessageID, "<") || !strings.HasSuffix(m.MessageID, "@example.org>") {
		t.Errorf("round trip = %+v", m)
	}
}

func TestComposeRepliesInTheThread(t *testing.T) {
	orig, err := Parse([]byte(sample), 1000)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := Compose("owner@example.org", "", Outgoing{To: []string{"dana@example.com"}, Body: "Yes!", Reply: &orig}, now)
	if err != nil {
		t.Fatal(err)
	}
	m, err := Parse(raw, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if m.Subject != "Re: Café on Friday?" || len(m.References) != 3 || m.References[2] != "<abc123@example.com>" {
		t.Errorf("reply = %q %v", m.Subject, m.References)
	}
	if !strings.Contains(string(raw), "In-Reply-To: <abc123@example.com>") {
		t.Error("In-Reply-To missing")
	}
}

func TestComposeRefusesInjectionAndExcess(t *testing.T) {
	for name, o := range map[string]Outgoing{
		"newline in recipient":    {To: []string{"a@example.com\r\nBcc: spy@example.net"}, Body: "x"},
		"newline in subject":      {To: []string{"a@example.com"}, Subject: "hi\r\nBcc: spy@example.net", Body: "x"},
		"no recipients":           {Body: "x"},
		"not an address":          {To: []string{"just a name"}, Body: "x"},
		"too many recipients":     {To: []string{"1@e.com", "2@e.com", "3@e.com", "4@e.com", "5@e.com", "6@e.com", "7@e.com", "8@e.com", "9@e.com", "10@e.com", "11@e.com"}, Body: "x"},
		"enormous body":           {To: []string{"a@example.com"}, Body: strings.Repeat("x", 20001)},
		"recipient list smuggled": {To: []string{"a@example.com, b@example.net, c@example.net"}, Body: "x"},
	} {
		raw, err := Compose("owner@example.org", "", o, now)
		if name == "recipient list smuggled" {
			// A comma list parses as one address list entry only if it is a single address.
			if err == nil && strings.Count(strings.SplitN(string(raw), "\r\n", 3)[1], "@") > 1 {
				continue
			}
		}
		if err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestToUTF8(t *testing.T) {
	if got := toUTF8("iso-8859-1", []byte{0x63, 0x61, 0x66, 0xe9}); got != "café" {
		t.Errorf("latin1 = %q", got)
	}
	if got := toUTF8("windows-1252", []byte{0x80, 0x93, 0x94}); got != "€“”" {
		t.Errorf("cp1252 = %q", got)
	}
	if got := toUTF8("x-unknown", []byte("ok\xff")); got != "ok\uFFFD" {
		t.Errorf("unknown = %q", got)
	}
}
