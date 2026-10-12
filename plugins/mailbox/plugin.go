// Package mailbox gives Kest an email account over IMAP and SMTP, so any
// provider works with an app password: read, search, draft, send and mark
// read, plus optional nudges for mail the owner is waiting for.
//
// Everything read from a mailbox is untrusted: an email can say anything,
// including instructions aimed at the model. Sending always needs the owner's
// approval of the exact recipients and text. Credentials come from the
// MAILBOX_PASSWORD secret and are never logged.
package mailbox

import (
	"context"
	_ "embed" // manifest.toml
	"errors"
	"fmt"
	"net/mail"
	"strings"

	"github.com/Harfull/Kest/internal/plugin"
	"github.com/Harfull/Kest/plugins/internal/mailkit"
)

//go:embed manifest.toml
var manifestSrc string

var manifest = plugin.MustParseManifest(manifestSrc)

// Plugin implements plugin.Plugin and plugin.JobProvider.
type Plugin struct {
	host    plugin.Host
	backend *backend
	nudger  *mailkit.Nudger
}

// New returns a fresh plugin instance.
func New() plugin.Plugin { return &Plugin{} }

// Manifest implements plugin.Plugin.
func (p *Plugin) Manifest() plugin.Manifest { return manifest }

// Init implements plugin.Plugin.
func (p *Plugin) Init(ctx context.Context, h plugin.Host) error {
	p.host = h
	cfg := h.Config()
	s := settings{
		imapHost: strings.TrimSpace(cfg.String("imap_host")), imapPort: cfg.Int("imap_port"), imapMode: cfg.String("imap_tls"),
		smtpHost: strings.TrimSpace(cfg.String("smtp_host")), smtpPort: cfg.Int("smtp_port"), smtpMode: cfg.String("smtp_tls"),
		username: cfg.String("username"), fromName: cfg.String("from_name"),
		folder: cfg.String("folder"), drafts: cfg.String("drafts_folder"), maxChars: cfg.Int("max_chars"),
	}
	for name, mode := range map[string]string{"imap_tls": s.imapMode, "smtp_tls": s.smtpMode} {
		if mode != "implicit" && mode != "starttls" {
			return fmt.Errorf("%s must be implicit or starttls; mail is never sent without encryption", name)
		}
	}
	for name, port := range map[string]int{"imap_port": s.imapPort, "smtp_port": s.smtpPort} {
		if port < 1 || port > 65535 {
			return fmt.Errorf("%s must be a port number", name)
		}
	}
	if s.imapHost == "" || s.smtpHost == "" || strings.TrimSpace(s.username) == "" {
		return errors.New("imap_host, smtp_host and username are required")
	}
	if s.maxChars < 200 || s.maxChars > 50000 {
		return errors.New("max_chars must be between 200 and 50000")
	}
	from := strings.TrimSpace(cfg.String("from_address"))
	if from == "" {
		from = s.username
	}
	a, err := mail.ParseAddress(from)
	if err != nil {
		return errors.New("from_address (or the username, when it is not set) must be an email address")
	}
	s.from = a.Address

	p.backend = &backend{s: s, dial: h.DialContext, now: h.Clock().Now, password: func() (string, error) {
		pw, err := h.Secret("MAILBOX_PASSWORD")
		if err != nil {
			return "", errors.New("the mailbox password is not set; add KEST_SECRET_MAILBOX_PASSWORD")
		}
		return pw, nil
	}}

	nudges := cfg.Bool("nudges")
	if cfg.Int("nudge_minutes") < 5 || cfg.Int("nudge_minutes") > 1440 {
		return errors.New("nudge_minutes must be between 5 and 1440")
	}
	p.nudger = &mailkit.Nudger{Host: h, Backend: p.backend, Rules: mailkit.Rules{
		From: mailkit.ParseList(cfg.String("nudge_from")), Words: mailkit.ParseList(cfg.String("nudge_subject_words")),
	}}
	if nudges && len(p.nudger.Rules.From)+len(p.nudger.Rules.Words) == 0 {
		return errors.New("nudges needs nudge_from or nudge_subject_words, so it knows which mail matters")
	}
	if err := mailkit.EnsureJob(ctx, h, nudges, cfg.Int("nudge_minutes")); err != nil {
		return err
	}
	return nil
}

// Close implements plugin.Plugin.
func (p *Plugin) Close(context.Context) error { return nil }

// Tools implements plugin.Plugin.
func (p *Plugin) Tools() []plugin.Tool {
	return mailkit.Tools(mailkit.Options{
		Host: p.host, Backend: p.backend, From: p.backend.s.from, FromName: p.backend.s.fromName, MaxChars: p.backend.s.maxChars,
	})
}

// JobNames implements plugin.JobProvider.
func (p *Plugin) JobNames() []string { return []string{mailkit.JobCheckInbox} }

// RunJob implements plugin.JobProvider.
func (p *Plugin) RunJob(ctx context.Context, name, _ string) error {
	if name != mailkit.JobCheckInbox {
		return fmt.Errorf("unknown job %q", name)
	}
	return p.nudger.Check(ctx)
}
