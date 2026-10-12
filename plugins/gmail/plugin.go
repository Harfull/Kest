// Package gmail gives Kest a Gmail account through the Gmail API: read,
// search, draft, send and mark read, plus optional nudges for mail the owner is
// waiting for.
//
// It authorizes with an OAuth refresh token the owner creates once; Kest never
// runs a consent flow itself. Everything read from Gmail is untrusted, and
// sending always needs the owner's approval of the exact recipients and text.
// The three secrets come from the environment or the secrets file and are never
// logged. Setup: docs/guides/gmail-setup.md.
package gmail

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
	host     plugin.Host
	backend  *backend
	nudger   *mailkit.Nudger
	from     string
	fromName string
}

// New returns a fresh plugin instance.
func New() plugin.Plugin { return &Plugin{} }

// Manifest implements plugin.Plugin.
func (p *Plugin) Manifest() plugin.Manifest { return manifest }

// Init implements plugin.Plugin.
func (p *Plugin) Init(ctx context.Context, h plugin.Host) error {
	p.host = h
	cfg := h.Config()
	if cfg.Int("max_chars") < 200 || cfg.Int("max_chars") > 50000 {
		return errors.New("max_chars must be between 200 and 50000")
	}
	if from := strings.TrimSpace(cfg.String("from_address")); from != "" {
		a, err := mail.ParseAddress(from)
		if err != nil {
			return errors.New("from_address must be an email address")
		}
		p.from = a.Address
	}
	p.fromName = cfg.String("from_name")

	p.backend = &backend{http: h.HTTP(), now: h.Clock().Now, maxChars: cfg.Int("max_chars"), creds: func() (credentials, error) {
		var c credentials
		var err error
		for name, dst := range map[string]*string{"GMAIL_CLIENT_ID": &c.clientID, "GMAIL_CLIENT_SECRET": &c.clientSecret, "GMAIL_REFRESH_TOKEN": &c.refreshToken} {
			if *dst, err = h.Secret(name); err != nil {
				return credentials{}, fmt.Errorf("the Gmail secret %s is not set; add KEST_SECRET_%s (see docs/guides/gmail-setup.md)", name, name)
			}
		}
		return c, nil
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
	return mailkit.EnsureJob(ctx, h, nudges, cfg.Int("nudge_minutes"))
}

// Close implements plugin.Plugin.
func (p *Plugin) Close(context.Context) error { return nil }

// Tools implements plugin.Plugin.
func (p *Plugin) Tools() []plugin.Tool {
	return mailkit.Tools(mailkit.Options{
		Host: p.host, Backend: p.backend, From: p.from, FromFunc: p.backend.ownAddress, FromName: p.fromName, MaxChars: p.backend.maxChars,
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
