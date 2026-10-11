// Package logging builds the process-wide structured logger.
//
// Redaction of secrets and message content is added in Phase 1; until then
// callers must not log sensitive values.
package logging

import (
	"fmt"
	"io"
	"log/slog"
	"strings"
)

// New returns a slog.Logger that writes to w.
//
// level is one of debug, info, warn or error. format is "text" or "json".
func New(w io.Writer, level, format string) (*slog.Logger, error) {
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(strings.ToLower(level))); err != nil {
		return nil, fmt.Errorf("parse log level %q: %w", level, err)
	}

	opts := &slog.HandlerOptions{Level: lvl}

	switch strings.ToLower(format) {
	case "text":
		return slog.New(slog.NewTextHandler(w, opts)), nil
	case "json":
		return slog.New(slog.NewJSONHandler(w, opts)), nil
	default:
		return nil, fmt.Errorf("unknown log format %q (want text or json)", format)
	}
}
