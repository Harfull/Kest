package coach

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestNudgeRespectsQuietHoursAndTheBudget(t *testing.T) {
	r := newRig(t, nil)
	ctx := context.Background()

	r.clk.Set(when(0, 3, 0)) // quiet hours
	if sent, err := r.c.Nudge(ctx, "mailbox", "New email from Dana."); err != nil || sent {
		t.Fatalf("a nudge went out in quiet hours: sent %v, err %v", sent, err)
	}

	r.clk.Set(when(0, 14, 0))
	if sent, err := r.c.Nudge(ctx, "mailbox", "New email from Dana."); err != nil || !sent {
		t.Fatalf("a nudge in the daytime was held back: sent %v, err %v", sent, err)
	}
	if got := r.out.last().Text; got != "New email from Dana." {
		t.Fatalf("message = %q", got)
	}

	// The minimum gap between proactive messages applies to nudges too.
	r.clk.Set(when(0, 14, 30))
	if sent, _ := r.c.Nudge(ctx, "mailbox", "Another."); sent {
		t.Fatal("two nudges inside the minimum gap")
	}
	r.clk.Set(when(0, 17, 0))
	if sent, _ := r.c.Nudge(ctx, "mailbox", "Later."); !sent {
		t.Fatal("a nudge after the gap was held back")
	}
}

func TestNudgeIsSilencedByThePreferences(t *testing.T) {
	r := newRig(t, nil)
	if _, err := r.tool("set_preferences", `{"silent":true}`); err != nil {
		t.Fatal(err)
	}
	r.clk.Set(when(0, 14, 0))
	if sent, _ := r.c.Nudge(context.Background(), "mailbox", "New email."); sent {
		t.Fatal("a nudge ignored the owner's request for silence")
	}
}

func TestNudgeCountsTowardTheDailyRhythm(t *testing.T) {
	r := newRig(t, nil)
	ctx := context.Background()
	r.clk.Set(when(1, 9, 0))
	if sent, _ := r.c.Nudge(ctx, "mailbox", "New email."); !sent {
		t.Fatal("nudge held back")
	}
	history, err := r.db.Notifications(ctx, when(0, 0, 0))
	if err != nil || len(history) != 1 || !strings.HasPrefix(history[0].Kind, NudgeKind+":mailbox") {
		t.Fatalf("history = %+v, %v", history, err)
	}
	if empty, _ := r.c.Nudge(ctx, "mailbox", "   "); empty {
		t.Fatal("an empty nudge was sent")
	}
}

func TestWeeklyReviewIncludesPluginDigests(t *testing.T) {
	r := newRig(t, nil)
	r.c.extra = func(context.Context) []string { return []string{"Workouts: 3 sessions, 95 minutes."} }
	summary, _ := r.c.weeklyFacts(context.Background(), when(6, 19, 0).Add(time.Minute))
	if !strings.Contains(summary, "Workouts: 3 sessions, 95 minutes.") || strings.Contains(summary, "not much to look at") {
		t.Fatalf("summary = %q", summary)
	}
}
