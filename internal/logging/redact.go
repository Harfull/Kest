package logging

import (
	"context"
	"log/slog"
	"regexp"
	"strings"
)

const redacted = "[redacted]"

// tokenPatterns match credential shapes even when the exact value is unknown,
// for example a token echoed back inside an upstream error message.
var tokenPatterns = []*regexp.Regexp{
	regexp.MustCompile(`\b\d{6,}:[A-Za-z0-9_-]{30,}\b`),                  // Telegram bot token
	regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{16,}\b`),                      // OpenRouter and similar API keys
	regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._~+/=-]{16,}`),         // Authorization header values
	regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}`), // email addresses
}

// sensitiveKeys are attribute names that carry user content. Their values are
// hidden unless sensitive logging is enabled.
var sensitiveKeys = map[string]bool{
	"content": true, "text": true, "body": true, "message": true,
	"prompt": true, "completion": true, "transcript": true, "subject": true,
}

// Redactor removes secrets and personal data from log output.
type Redactor struct {
	exact []string
}

// NewRedactor returns a Redactor that masks the given secret values wherever
// they appear, in addition to common credential shapes. Empty and very short
// values are ignored because masking them would shred ordinary text.
func NewRedactor(secrets ...string) *Redactor {
	r := &Redactor{}
	for _, s := range secrets {
		if len(s) >= 6 {
			r.exact = append(r.exact, s)
		}
	}
	return r
}

// String masks secrets in s.
func (r *Redactor) String(s string) string {
	for _, v := range r.exact {
		s = strings.ReplaceAll(s, v, redacted)
	}
	for _, p := range tokenPatterns {
		s = p.ReplaceAllString(s, redacted)
	}
	return s
}

// redactingHandler applies a Redactor to every record before delegating.
type redactingHandler struct {
	next      slog.Handler
	r         *Redactor
	sensitive bool
}

func (h *redactingHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return h.next.Enabled(ctx, l)
}

func (h *redactingHandler) Handle(ctx context.Context, rec slog.Record) error {
	out := slog.NewRecord(rec.Time, rec.Level, h.r.String(rec.Message), rec.PC)
	rec.Attrs(func(a slog.Attr) bool {
		out.AddAttrs(h.attr(a))
		return true
	})
	return h.next.Handle(ctx, out)
}

func (h *redactingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	clean := make([]slog.Attr, len(attrs))
	for i, a := range attrs {
		clean[i] = h.attr(a)
	}
	return &redactingHandler{next: h.next.WithAttrs(clean), r: h.r, sensitive: h.sensitive}
}

func (h *redactingHandler) WithGroup(name string) slog.Handler {
	return &redactingHandler{next: h.next.WithGroup(name), r: h.r, sensitive: h.sensitive}
}

// attr redacts one attribute, recursing into groups.
func (h *redactingHandler) attr(a slog.Attr) slog.Attr {
	a.Value = a.Value.Resolve()

	if a.Value.Kind() == slog.KindGroup {
		group := a.Value.Group()
		clean := make([]slog.Attr, len(group))
		for i, g := range group {
			clean[i] = h.attr(g)
		}
		return slog.Attr{Key: a.Key, Value: slog.GroupValue(clean...)}
	}

	if !h.sensitive && sensitiveKeys[strings.ToLower(a.Key)] {
		return slog.String(a.Key, redacted)
	}

	switch a.Value.Kind() {
	case slog.KindString:
		return slog.String(a.Key, h.r.String(a.Value.String()))
	case slog.KindAny:
		if err, ok := a.Value.Any().(error); ok {
			return slog.String(a.Key, h.r.String(err.Error()))
		}
		if s, ok := a.Value.Any().(interface{ String() string }); ok {
			return slog.String(a.Key, h.r.String(s.String()))
		}
	default:
	}
	return a
}
