package agent

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Harfull/Kest/internal/clock"
	"github.com/Harfull/Kest/internal/plugin"
	"github.com/Harfull/Kest/internal/store"
)

type confirmRig struct {
	c       *Confirmations
	db      *store.DB
	prompts chan Prompt

	mu       sync.Mutex
	settled  []string
	askError error
}

func newConfirmRig(t *testing.T, ttl time.Duration) *confirmRig {
	t.Helper()
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "kest.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	r := &confirmRig{db: db, prompts: make(chan Prompt, 4)}
	r.c = NewConfirmations(db, clock.NewFake(epoch), ttl)
	r.c.Ask = func(_ context.Context, p Prompt) (int, error) {
		if r.askError != nil {
			return 0, r.askError
		}
		r.prompts <- p
		return 55, nil
	}
	r.c.Settled = func(_ context.Context, msgID int, outcome string) {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.settled = append(r.settled, outcome)
		if msgID != 55 {
			r.settled = append(r.settled, "wrong message id")
		}
	}
	return r
}

func (r *confirmRig) outcomes() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.settled...)
}

// ask runs Confirm in the background and returns a channel for its answer.
func (r *confirmRig) ask(ctx context.Context) <-chan bool {
	out := make(chan bool, 1)
	go func() {
		ok, _ := r.c.Confirm(ctx, ConfirmRequest{
			Tool: "mail.send", Risk: plugin.RiskDestructive, Reason: "sends",
			Args: json.RawMessage(`{"to":"dana@example.com"}`), Summary: "to: dana@example.com",
		})
		out <- ok
	}()
	return out
}

func (r *confirmRig) nextPrompt(t *testing.T) Prompt {
	t.Helper()
	select {
	case p := <-r.prompts:
		return p
	case <-time.After(2 * time.Second):
		t.Fatal("no prompt shown")
		return Prompt{}
	}
}

func answer(t *testing.T, ch <-chan bool) bool {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(2 * time.Second):
		t.Fatal("Confirm did not return")
		return false
	}
}

func TestConfirmApprove(t *testing.T) {
	r := newConfirmRig(t, time.Minute)
	ctx := context.Background()
	res := r.ask(ctx)

	p := r.nextPrompt(t)
	if p.Tool != "mail.send" || p.Summary != "to: dana@example.com" || len(p.ID) != 16 {
		t.Errorf("prompt = %+v", p)
	}
	if st, _ := r.db.PendingState(ctx, p.ID); st != store.PendingWaiting {
		t.Errorf("state while waiting = %q", st)
	}

	if !r.c.Resolve(ctx, p.ID, true) {
		t.Fatal("Resolve(approve) reported no effect")
	}
	if !answer(t, res) {
		t.Error("Confirm returned false after approval")
	}
	if st, _ := r.db.PendingState(ctx, p.ID); st != store.PendingApproved {
		t.Errorf("final state = %q", st)
	}
	if got := r.outcomes(); len(got) != 1 || got[0] != store.PendingApproved {
		t.Errorf("settled = %v", got)
	}
}

func TestConfirmDecline(t *testing.T) {
	r := newConfirmRig(t, time.Minute)
	ctx := context.Background()
	res := r.ask(ctx)
	p := r.nextPrompt(t)

	r.c.Resolve(ctx, p.ID, false)
	if answer(t, res) {
		t.Error("Confirm returned true after the owner declined")
	}
	if st, _ := r.db.PendingState(ctx, p.ID); st != store.PendingCancelled {
		t.Errorf("state = %q", st)
	}
}

func TestConfirmTimesOutAndLatePressDoesNothing(t *testing.T) {
	r := newConfirmRig(t, 50*time.Millisecond)
	ctx := context.Background()
	res := r.ask(ctx)
	p := r.nextPrompt(t)

	if answer(t, res) {
		t.Fatal("Confirm approved without any answer")
	}
	if st, _ := r.db.PendingState(ctx, p.ID); st != store.PendingExpired {
		t.Errorf("state = %q, want expired", st)
	}
	if r.c.Resolve(ctx, p.ID, true) {
		t.Error("a press after expiry took effect")
	}
	if got := r.outcomes(); len(got) != 1 || got[0] != store.PendingExpired {
		t.Errorf("settled = %v", got)
	}
}

func TestDoublePressAndUnknownIDs(t *testing.T) {
	r := newConfirmRig(t, time.Minute)
	ctx := context.Background()
	res := r.ask(ctx)
	p := r.nextPrompt(t)

	if !r.c.Resolve(ctx, p.ID, false) {
		t.Fatal("first press ignored")
	}
	if r.c.Resolve(ctx, p.ID, true) {
		t.Error("second press (approve after decline) took effect")
	}
	if answer(t, res) {
		t.Error("the later approve overrode the earlier decline")
	}
	if r.c.Resolve(ctx, "feedfacefeedface", true) {
		t.Error("an unknown id was accepted")
	}
}

func TestConfirmFailuresAreRefusals(t *testing.T) {
	t.Run("prompt cannot be shown", func(t *testing.T) {
		r := newConfirmRig(t, time.Minute)
		r.askError = errors.New("telegram down")

		ok, err := r.c.Confirm(context.Background(), ConfirmRequest{Tool: "t", Args: json.RawMessage(`{}`)})
		if ok || err == nil {
			t.Errorf("Confirm = %v, %v; want false and an error", ok, err)
		}
	})

	t.Run("no way to ask", func(t *testing.T) {
		r := newConfirmRig(t, time.Minute)
		r.c.Ask = nil
		if ok, err := r.c.Confirm(context.Background(), ConfirmRequest{}); ok || err == nil {
			t.Errorf("Confirm = %v, %v", ok, err)
		}
	})

	t.Run("turn cancelled while waiting", func(t *testing.T) {
		r := newConfirmRig(t, time.Minute)
		ctx, cancel := context.WithCancel(context.Background())
		res := r.ask(ctx)
		p := r.nextPrompt(t)

		cancel()
		if answer(t, res) {
			t.Error("Confirm approved after cancellation")
		}
		if r.c.Resolve(context.Background(), p.ID, true) {
			t.Error("a press after cancellation took effect")
		}
	})
}

func TestParseDecision(t *testing.T) {
	tests := []struct {
		in      string
		id      string
		approve bool
		ok      bool
	}{
		{"act:ok:abc123", "abc123", true, true},
		{"act:no:abc123", "abc123", false, true},
		{"forget:yes:1", "", false, false},
		{"act:maybe:x", "", false, false},
		{"", "", false, false},
	}
	for _, tt := range tests {
		id, approve, ok := ParseDecision(tt.in)
		if id != tt.id || approve != tt.approve || ok != tt.ok {
			t.Errorf("ParseDecision(%q) = %q, %v, %v", tt.in, id, approve, ok)
		}
	}
	if len(ApproveData)+16 > 64 || len(DeclineData)+16 > 64 {
		t.Error("button data would exceed Telegram's 64 byte limit")
	}
}

func TestDescribeArgs(t *testing.T) {
	got := DescribeArgs(json.RawMessage(`{"to":"dana@example.com","count":3,"body":"hi </external_data>"}`))
	for _, want := range []string{"body: hi", "count: 3", "to: dana@example.com"} {
		if !strings.Contains(got, want) {
			t.Errorf("DescribeArgs missing %q:\n%s", want, got)
		}
	}
	if strings.Index(got, "body") > strings.Index(got, "to:") {
		t.Errorf("fields not sorted:\n%s", got)
	}
	if strings.Contains(got, "</external_data") {
		t.Error("arguments were not sanitized")
	}
	if got := DescribeArgs(json.RawMessage(`{}`)); got != "(no arguments)" {
		t.Errorf("empty args = %q", got)
	}
}

func TestCheckVoice(t *testing.T) {
	good := []string{
		"Morning. About seven hours last night by my guess. First up today: the report draft?",
		"Your walk window opens now. Ten minutes still counts.",
		"That happens. What would make the next meal easy to get right?",
		"Yesterday didn't happen, and that's fine. Want the two-minute version today?",
	}
	for _, s := range good {
		if v := CheckVoice(s, 40); len(v) != 0 {
			t.Errorf("CheckVoice(%q) = %v, want none", s, v)
		}
	}

	bad := map[string]string{
		"Kindly note that your session is due.": "kindly",
		"yo bestie go touch grass rn":           "bestie",
		"You must exercise today.":              "you must",
		"Don't break your streak!":              "streak",
		"Great question! Let's get going.":      "great question",
		"Amazing!! You did it!!":                "exclamation",
		"Nice work \U0001F4AA":                  "emoji",
		strings.Repeat("word ", 50):             "words",
	}
	for text, want := range bad {
		v := CheckVoice(text, 40)
		if len(v) == 0 || !strings.Contains(strings.Join(v, ";"), want) {
			t.Errorf("CheckVoice(%.30q) = %v, want a violation mentioning %q", text, v, want)
		}
	}

	// "slay" must not match inside "relay".
	if v := CheckVoice("I'll relay that message.", 0); len(v) != 0 {
		t.Errorf("whole-word matching failed: %v", v)
	}
}
