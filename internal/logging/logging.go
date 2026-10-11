// Package logging builds the process-wide structured logger.
//
// Every record passes through a redactor that masks configured secrets,
// credential-shaped strings and email addresses, and hides user content
// attributes unless sensitive logging is explicitly enabled.
package logging

import (
	"fmt"
	"io"
	"log/slog"
	"strings"
)

// Options configures New.
type Options struct {
	// Level is debug, info, warn or error.
	Level string
	// Format is "text" or "json".
	Format string
	// Secrets are values that must never appear in output.
	Secrets []string
	// Sensitive allows user content attributes (message text and the like) to
	// be logged. It should only be on while debugging.
	Sensitive bool
}

// New returns a redacting slog.Logger that writes to w.
func New(w io.Writer, opts Options) (*slog.Logger, error) {
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(strings.ToLower(opts.Level))); err != nil {
		return nil, fmt.Errorf("parse log level %q: %w", opts.Level, err)
	}

	ho := &slog.HandlerOptions{Level: lvl}

	var base slog.Handler
	switch strings.ToLower(opts.Format) {
	case "text":
		base = slog.NewTextHandler(w, ho)
	case "json":
		base = slog.NewJSONHandler(w, ho)
	default:
		return nil, fmt.Errorf("unknown log format %q (want text or json)", opts.Format)
	}

	return slog.New(&redactingHandler{
		next:      base,
		r:         NewRedactor(opts.Secrets...),
		sensitive: opts.Sensitive,
	}), nil
}
