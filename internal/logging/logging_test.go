package logging

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

func TestNew(t *testing.T) {
	tests := []struct {
		name    string
		opts    Options
		wantErr bool
	}{
		{name: "text info", opts: Options{Level: "info", Format: "text"}},
		{name: "json debug", opts: Options{Level: "debug", Format: "json"}},
		{name: "case insensitive", opts: Options{Level: "WARN", Format: "JSON"}},
		{name: "bad level", opts: Options{Level: "loud", Format: "text"}, wantErr: true},
		{name: "bad format", opts: Options{Level: "info", Format: "xml"}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := New(&bytes.Buffer{}, tt.opts)
			if (err != nil) != tt.wantErr {
				t.Fatalf("New() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestLevelFiltering(t *testing.T) {
	var buf bytes.Buffer
	log, err := New(&buf, Options{Level: "warn", Format: "text"})
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

func newBuf(t *testing.T, opts Options) (*slog.Logger, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	opts.Level, opts.Format = "debug", "text"
	log, err := New(&buf, opts)
	if err != nil {
		t.Fatal(err)
	}
	return log, &buf
}

func TestRedactsConfiguredSecretsAnywhere(t *testing.T) {
	const secret = "hunter2-very-secret"
	log, buf := newBuf(t, Options{Secrets: []string{secret}})

	log.Info("connecting with "+secret,
		"url", "https://x.test/?key="+secret,
		"err", errors.New("rejected "+secret),
		slog.Group("req", "auth", secret),
	)
	log.With("token", secret).Info("with attrs")

	if strings.Contains(buf.String(), secret) {
		t.Fatalf("secret leaked:\n%s", buf.String())
	}
}

func TestRedactsCredentialShapes(t *testing.T) {
	log, buf := newBuf(t, Options{})

	log.Info("upstream said no",
		"a", "123456789:ABCdefGHIjklMNOpqrSTUvwxYZ0123456789",
		"b", "key sk-or-v1-abcdef1234567890abcdef",
		"c", "Authorization: Bearer abcdef0123456789abcdef",
		"d", "contact jane.doe@example.com please",
	)

	out := buf.String()
	for _, leak := range []string{"ABCdefGHI", "sk-or-v1", "abcdef0123456789", "jane.doe@example.com"} {
		if strings.Contains(out, leak) {
			t.Errorf("%q leaked:\n%s", leak, out)
		}
	}
}

func TestHidesContentUnlessSensitive(t *testing.T) {
	tests := []struct {
		name      string
		sensitive bool
		wantText  bool
	}{
		{"default hides", false, false},
		{"sensitive shows", true, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			log, buf := newBuf(t, Options{Sensitive: tt.sensitive})
			log.Info("turn", "text", "I skipped my workout", "chat_id", 42)

			out := buf.String()
			if got := strings.Contains(out, "skipped my workout"); got != tt.wantText {
				t.Errorf("content visible = %v, want %v:\n%s", got, tt.wantText, out)
			}
			if !strings.Contains(out, "chat_id=42") {
				t.Errorf("non-sensitive attribute lost:\n%s", out)
			}
		})
	}
}

func TestShortSecretsAreNotMasked(t *testing.T) {
	log, buf := newBuf(t, Options{Secrets: []string{"a", "ab"}})
	log.Info("a banana")

	if !strings.Contains(buf.String(), "a banana") {
		t.Errorf("short secret shredded ordinary text: %s", buf.String())
	}
}
