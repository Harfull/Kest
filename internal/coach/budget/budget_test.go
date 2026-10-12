package budget

import (
	"testing"
	"time"

	"github.com/Harfull/Kest/internal/store"
)

var now = time.Date(2026, 10, 12, 18, 0, 0, 0, time.UTC)

// msg builds a notification sent `ago` before now; answered says whether the
// owner reacted.
func msg(kind string, ago time.Duration, answered bool) store.Notification {
	n := store.Notification{TS: now.Add(-ago), Kind: kind}
	if answered {
		n.RespondedAt = n.TS.Add(10 * time.Minute)
	}
	return n
}

func TestAllowBasics(t *testing.T) {
	p := New(3, time.UTC)

	tests := []struct {
		name    string
		history []store.Notification
		quiet   bool
		prio    Priority
		want    bool
	}{
		{"empty history", nil, false, Normal, true},
		{"quiet hours always win", nil, true, Normal, false},
		{"below the daily cap", []store.Notification{msg("morning", 8*time.Hour, true)}, false, Normal, true},
		{"daily cap reached", []store.Notification{
			msg("morning", 9*time.Hour, true), msg("habit:a", 5*time.Hour, true), msg("habit:b", 2*time.Hour, true)}, false, Normal, false},
		{"yesterday's messages do not count today", []store.Notification{
			msg("morning", 26*time.Hour, true), msg("habit:a", 25*time.Hour, true), msg("evening", 24*time.Hour+30*time.Minute, true)}, false, Normal, true},
		{"too soon after the last", []store.Notification{msg("morning", 30*time.Minute, true)}, false, Normal, false},
		{"gap satisfied", []store.Notification{msg("morning", 91*time.Minute, true)}, false, Normal, true},
		{"low priority behaves like normal while the owner responds", []store.Notification{msg("morning", 5*time.Hour, true)}, false, Low, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := p.Allow(now, "habit:x", tt.prio, tt.history, tt.quiet)
			if d.OK != tt.want {
				t.Errorf("Allow = %+v, want OK=%v", d, tt.want)
			}
			if !d.OK && d.Reason == "" {
				t.Error("a refusal needs a reason for the logs")
			}
		})
	}
}

func TestZeroMaxTurnsProactiveMessagesOff(t *testing.T) {
	if d := New(0, time.UTC).Allow(now, "morning", Normal, nil, false); d.OK {
		t.Error("MaxPerDay 0 still allowed a message")
	}
}

func TestLocalDayDecidesTheCap(t *testing.T) {
	tokyo, _ := time.LoadLocation("Asia/Tokyo") // UTC+9
	p := New(1, tokyo)
	// 2026-10-12 18:00 UTC is 03:00 on the 13th in Tokyo. A message at 12:00 UTC
	// is 21:00 on the 12th there: yesterday, so today's cap is still free.
	hist := []store.Notification{{TS: time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC), Kind: "evening", RespondedAt: now}}
	if d := p.Allow(now, "morning", Normal, hist, false); !d.OK {
		t.Errorf("Allow = %+v; the earlier message belongs to the previous local day", d)
	}
}

func TestIgnoredCountsFromNewestAndStopsAtAResponse(t *testing.T) {
	p := New(3, time.UTC)
	h := []store.Notification{
		msg("a", 40*time.Hour, false),
		msg("b", 30*time.Hour, true), // answered: older ones do not count
		msg("c", 20*time.Hour, false),
		msg("d", 10*time.Hour, false),
		msg("e", 30*time.Minute, false), // inside the response window: undecided
	}
	if got := p.Ignored(h, now); got != 2 {
		t.Errorf("Ignored = %d, want 2 (c and d; e is undecided, b ends the run)", got)
	}
	if got := p.Ignored(nil, now); got != 0 {
		t.Errorf("Ignored(nil) = %d", got)
	}
}

// previous returns n unanswered messages on earlier days, the newest first
// sent newestAgo before now and the rest a day apart.
func previous(n int, newestAgo time.Duration) []store.Notification {
	var h []store.Notification
	for i := n - 1; i >= 0; i-- {
		h = append(h, msg("habit:x", newestAgo+time.Duration(i)*24*time.Hour, false))
	}
	return h
}

func TestBackoffLimits(t *testing.T) {
	p := New(3, time.UTC)
	tests := []struct {
		ignored int
		cap     int
		gap     time.Duration
	}{
		{0, 3, 90 * time.Minute},
		{1, 3, 90 * time.Minute},
		{2, 2, 3 * time.Hour},
		{3, 1, 12 * time.Hour},
		{5, 1, 12 * time.Hour},
		{6, 1, 72 * time.Hour},
		{40, 1, 72 * time.Hour},
	}
	for _, tt := range tests {
		cap, gap := p.limits(tt.ignored)
		if cap != tt.cap || gap != tt.gap {
			t.Errorf("limits(%d) = %d / %v, want %d / %v", tt.ignored, cap, gap, tt.cap, tt.gap)
		}
	}
	if cap, _ := New(1, time.UTC).limits(2); cap != 1 {
		t.Errorf("a cap of 1 must not drop to 0, got %d", cap)
	}
}

func TestBackoffLadderInPractice(t *testing.T) {
	p := New(3, time.UTC)

	tests := []struct {
		name    string
		history []store.Notification
		want    bool
	}{
		{"nothing sent", nil, true},
		{"two ignored days ago: still allowed", previous(2, 25*time.Hour), true},
		{"three ignored, last one a day ago: one a day is allowed", previous(3, 25*time.Hour), true},
		{"three ignored and one already today: the day is used up", append(previous(3, 30*time.Hour), msg("habit:x", 6*time.Hour, false)), false},
		{"six ignored, last a day ago: wait 72 hours", previous(6, 25*time.Hour), false},
		{"six ignored, last two days ago: still waiting", previous(6, 49*time.Hour), false},
		{"six ignored, last three days ago: may try again", previous(6, 73*time.Hour), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := p.Allow(now, "habit:x", Normal, tt.history, false)
			if d.OK != tt.want {
				t.Errorf("Allow = %+v, want OK=%v", d, tt.want)
			}
		})
	}
}

func TestTwoIgnoredDoublesTheGap(t *testing.T) {
	p := New(3, time.UTC)
	p.ResponseWindow = 6 * time.Hour // so the newest message below is still undecided
	yesterday := []store.Notification{msg("morning", 30*time.Hour, false), msg("habit:x", 26*time.Hour, false)}

	// A message 2 hours ago is still inside the response window, so it is
	// undecided and the owner counts as having ignored just the two from yesterday.
	recent := append(append([]store.Notification(nil), yesterday...), msg("evening", 2*time.Hour, false))
	if got := p.Ignored(recent, now); got != 2 {
		t.Fatalf("Ignored = %d, want 2", got)
	}
	if d := p.Allow(now, "weekly", Normal, recent, false); d.OK {
		t.Errorf("Allow = %+v; 2 hours is past the normal 90 minutes but short of the doubled 3 hours", d)
	}

	later := append(append([]store.Notification(nil), yesterday...), msg("evening", 3*time.Hour+time.Minute, false))
	if d := p.Allow(now, "weekly", Normal, later, false); !d.OK {
		t.Errorf("Allow = %+v; 3 hours have passed", d)
	}
}

func TestLowPriorityGivesWayFirst(t *testing.T) {
	p := New(3, time.UTC)
	var h []store.Notification
	for i := 4; i >= 1; i-- {
		h = append(h, msg("habit:x", time.Duration(14+i*13)*time.Hour, false))
	}
	if d := p.Allow(now, "winddown", Low, h, false); d.OK {
		t.Errorf("a low-priority nudge went out while the owner is not responding: %+v", d)
	}
	if d := p.Allow(now, "morning", Normal, h, false); !d.OK {
		t.Errorf("the normal rhythm should still ration through, got %+v", d)
	}
	// The question about messaging less is itself allowed under back-off.
	if d := p.Allow(now, "backoff", Low, h, false); !d.OK {
		t.Errorf("the back-off question was blocked: %+v", d)
	}
}

func TestShouldAskToBackOff(t *testing.T) {
	p := New(3, time.UTC)
	ignored := []store.Notification{msg("a", 50*time.Hour, false), msg("b", 40*time.Hour, false), msg("c", 30*time.Hour, false)}

	if !p.ShouldAskToBackOff(ignored, now) {
		t.Error("three unanswered messages should prompt the question")
	}
	if p.ShouldAskToBackOff(ignored[:2], now) {
		t.Error("two unanswered messages are not enough")
	}
	asked := append([]store.Notification{msg("backoff", 3*24*time.Hour, true)}, ignored...)
	if p.ShouldAskToBackOff(asked, now) {
		t.Error("asked twice within a week")
	}
	old := append([]store.Notification{msg("backoff", 8*24*time.Hour, true)}, ignored...)
	if !p.ShouldAskToBackOff(old, now) {
		t.Error("a week-old question should not block asking again")
	}
}

func TestKindOf(t *testing.T) {
	for in, want := range map[string]string{"habit:abc": "habit", "morning": "morning", "": ""} {
		if got := kindOf(in); got != want {
			t.Errorf("kindOf(%q) = %q", in, got)
		}
	}
}
