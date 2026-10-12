package audit

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Harfull/Kest/internal/clock"
	"github.com/Harfull/Kest/internal/store"
)

func newLog(t *testing.T, secrets ...string) (*Log, *store.DB) {
	t.Helper()
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "kest.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	clk := clock.NewFake(time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC))
	return New(db, clk, nil, secrets...), db
}

func TestRecordFillsDefaultsAndReadsBackNewestFirst(t *testing.T) {
	l, _ := newLog(t)
	ctx := context.Background()

	l.Record(ctx, Entry{Action: "reminders.create_reminder", Risk: "write", Summary: "set for 9:00"})
	l.Record(ctx, Entry{Action: "webfetch.fetch", Risk: "read", Status: StatusError, Summary: "tool failed"})

	got, err := l.Recent(ctx, 10)
	if err != nil || len(got) != 2 {
		t.Fatalf("Recent() = %v, %v", got, err)
	}
	if got[0].Action != "webfetch.fetch" || got[1].Action != "reminders.create_reminder" {
		t.Errorf("order = %s, %s; want newest first", got[0].Action, got[1].Action)
	}
	if got[1].Actor != ActorOwner || got[1].Status != StatusOK || got[1].TS.IsZero() {
		t.Errorf("defaults not filled: %+v", got[1])
	}
	if one, _ := l.Recent(ctx, 1); len(one) != 1 {
		t.Errorf("Recent(1) returned %d entries", len(one))
	}
}

func TestSummariesAreScrubbedAndShort(t *testing.T) {
	l, _ := newLog(t, "hunter2-secret")
	tests := []struct {
		name, in string
		check    func(string) bool
	}{
		{"email", "sent to alice@example.com", func(s string) bool { return !strings.Contains(s, "alice@example.com") }},
		{"configured secret", "key was hunter2-secret ok", func(s string) bool { return !strings.Contains(s, "hunter2-secret") }},
		{"token shape", "used sk-or-v1-abcdef0123456789abcdef0123456789", func(s string) bool { return !strings.Contains(s, "abcdef0123456789") }},
		{"control characters", "line one\nline two\x00\ttabbed", func(s string) bool { return !strings.ContainsAny(s, "\n\x00\t") }},
		{"length", strings.Repeat("word ", 200), func(s string) bool { return len([]rune(s)) <= maxSummary }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := l.Summarize(tt.in); !tt.check(got) {
				t.Errorf("Summarize(%q) = %q", tt.in, got)
			}
		})
	}
}

type failingStore struct{ Store }

func (failingStore) AddAudit(context.Context, store.AuditEntry) error { return errors.New("disk full") }

func TestAFailedWriteDoesNotPanicOrBlock(t *testing.T) {
	l := New(failingStore{}, nil, nil)
	l.Record(context.Background(), Entry{Action: "x"}) // must simply return
}

func TestFormat(t *testing.T) {
	e := store.AuditEntry{
		TS: time.Date(2026, 10, 12, 13, 5, 0, 0, time.UTC), Action: "gmail.send", Risk: "destructive",
		Confirmed: true, Status: "ok", Summary: "sent",
	}
	got := Format(e, time.UTC)
	for _, want := range []string{"12 Oct 13:05", "gmail.send (destructive)", "approved", "ok", "sent"} {
		if !strings.Contains(got, want) {
			t.Errorf("Format() = %q, missing %q", got, want)
		}
	}
}
