// Package audit records what Kest did on the owner's behalf: which tool ran,
// at what risk, whether the owner approved it and how it ended.
//
// It records actions, never content. Summaries are scrubbed of control
// characters, token-shaped strings and email addresses, and cut short, so the
// log is safe to show the owner and to keep. It is not a log of conversations
// (see the store's messages) and not the application log (see package logging).
package audit

import (
	"context"
	"log/slog"
	"strings"
	"time"
	"unicode"

	"github.com/Harfull/Kest/internal/clock"
	"github.com/Harfull/Kest/internal/logging"
	"github.com/Harfull/Kest/internal/store"
)

// Actors and statuses used across Kest, so entries read the same everywhere.
const (
	ActorOwner  = "owner"
	ActorSystem = "system"

	StatusOK       = "ok"
	StatusError    = "error"
	StatusDeclined = "declined"
)

// maxSummary bounds a summary. Longer text is almost certainly content.
const maxSummary = 200

// Store is the persistence the log needs. *store.DB satisfies it.
type Store interface {
	AddAudit(ctx context.Context, e store.AuditEntry) error
	RecentAudit(ctx context.Context, n int) ([]store.AuditEntry, error)
}

// Entry is one thing Kest did. The zero Actor means the owner.
type Entry struct {
	Actor     string
	Action    string // plugin.tool, or a command such as "forget"
	Risk      string // read, write or destructive
	Confirmed bool
	Status    string
	Summary   string
}

// Log writes and reads audit entries.
type Log struct {
	db    Store
	clock clock.Clock
	log   *slog.Logger
	scrub *logging.Redactor
}

// New returns a Log. Secrets are extra strings to mask in summaries, on top of
// the token and email shapes that are always masked.
func New(db Store, clk clock.Clock, log *slog.Logger, secrets ...string) *Log {
	if clk == nil {
		clk = clock.Real{}
	}
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Log{db: db, clock: clk, log: log, scrub: logging.NewRedactor(secrets...)}
}

// Record stores an entry. A failure to write is logged once and otherwise
// ignored: losing an audit line must not undo or block the action it describes.
func (l *Log) Record(ctx context.Context, e Entry) {
	if e.Actor == "" {
		e.Actor = ActorOwner
	}
	if e.Status == "" {
		e.Status = StatusOK
	}
	err := l.db.AddAudit(ctx, store.AuditEntry{
		TS: l.clock.Now(), Actor: e.Actor, Action: e.Action, Risk: e.Risk,
		Confirmed: e.Confirmed, Status: e.Status, Summary: l.Summarize(e.Summary),
	})
	if err != nil {
		l.log.WarnContext(ctx, "write audit entry", "action", e.Action, "err", err)
	}
}

// Recent returns up to n entries, newest first.
func (l *Log) Recent(ctx context.Context, n int) ([]store.AuditEntry, error) {
	return l.db.RecentAudit(ctx, n)
}

// Summarize scrubs and shortens text for the log.
func (l *Log) Summarize(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return ' '
		}
		return r
	}, s)
	s = strings.Join(strings.Fields(l.scrub.String(s)), " ")
	if r := []rune(s); len(r) > maxSummary {
		s = string(r[:maxSummary-1]) + "…"
	}
	return s
}

// Format renders an entry as one line for the owner.
func Format(e store.AuditEntry, loc *time.Location) string {
	line := e.TS.In(loc).Format("2 Jan 15:04") + "  " + e.Action + " (" + e.Risk + ")"
	if e.Confirmed {
		line += ", approved"
	}
	line += ": " + e.Status
	if e.Summary != "" {
		line += ". " + e.Summary
	}
	return line
}
