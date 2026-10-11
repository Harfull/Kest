package logging

import (
	"bytes"
	"strings"
	"testing"
)

func TestNew(t *testing.T) {
	tests := []struct {
		name    string
		level   string
		format  string
		wantErr bool
	}{
		{name: "text info", level: "info", format: "text"},
		{name: "json debug", level: "debug", format: "json"},
		{name: "case insensitive", level: "WARN", format: "JSON"},
		{name: "bad level", level: "loud", format: "text", wantErr: true},
		{name: "bad format", level: "info", format: "xml", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := New(&bytes.Buffer{}, tt.level, tt.format)
			if (err != nil) != tt.wantErr {
				t.Fatalf("New() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestLevelFiltering(t *testing.T) {
	var buf bytes.Buffer
	log, err := New(&buf, "warn", "text")
	if err != nil {
		t.Fatal(err)
	}

	log.Info("hidden")
	log.Warn("shown")

	out := buf.String()
	if strings.Contains(out, "hidden") {
		t.Errorf("info message leaked at warn level: %q", out)
	}
	if !strings.Contains(out, "shown") {
		t.Errorf("warn message missing: %q", out)
	}
}
