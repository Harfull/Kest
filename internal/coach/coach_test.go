package coach

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Harfull/Kest/internal/clock"
	"github.com/Harfull/Kest/internal/plugin"
	"github.com/Harfull/Kest/internal/store"
	"github.com/Harfull/Kest/internal/voice"
)

// outbox collects what the coach says.
type outbox struct {
	mu   sync.Mutex
	msgs []Message
	at   []time.Time
	clk  *clock.Fake
	fail error
}

func (o *outbox) Send(_ context.Context, m Message) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.fail != nil {
		return o.fail
	}
	o.msgs = append(o.msgs, m)
	o.at = append(o.at, o.clk.Now())
	return nil
}

func (o *outbox) count() int { o.mu.Lock(); defer o.mu.Unlock(); return len(o.msgs) }

func (o *outbox) last() Message {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.msgs[len(o.msgs)-1]
}

type rig struct {
	t   *testing.T
	c   *Coach
	db  *store.DB
	out *outbox
	clk *clock.Fake
	loc *time.Location
}

// monday is Monday 12 October 2026, 00:00 UTC.
var monday = time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)

func newRig(t *testing.T, mutate func(*Config)) *rig {
	t.Helper()
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "kest.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	clk := clock.NewFake(monday)
	out := &outbox{clk: clk}
	cfg := Config{MaxPerDay: 3, QuietStart: "23:00", QuietEnd: "07:00"}
	if mutate != nil {
		mutate(&cfg)
	}
	c, err := New(Options{Store: db, Out: out, Clock: clk, Loc: time.UTC, Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	return &rig{t: t, c: c, db: db, out: out, clk: clk, loc: time.UTC}
}

// at moves the clock to hh:mm on the given day offset from Monday (0 = Monday)
// and runs a tick there.
func (r *rig) at(day, h, m int) {
	r.t.Helper()
	r.clk.Set(monday.AddDate(0, 0, day).Add(time.Duration(h)*time.Hour + time.Duration(m)*time.Minute))
	if err := r.c.Tick(context.Background()); err != nil {
		r.t.Fatalf("Tick() error = %v", err)
	}
}

// when returns an instant on the given day offset.
func when(day, h, m int) time.Time {
	return monday.AddDate(0, 0, day).Add(time.Duration(h)*time.Hour + time.Duration(m)*time.Minute)
}

// tool calls a coach tool by name.
func (r *rig) tool(name, args string) (plugin.Result, error) {
	r.t.Helper()
	for _, ref := range r.c.Tools() {
		if ref.Tool.Name == name {
			return ref.Tool.Handler(context.Background(), plugin.Call{Args: json.RawMessage(args)})
		}
	}
	r.t.Fatalf("no tool %s", name)
	return plugin.Result{}, nil
}

// habit creates a habit through the tool and returns its id.
func (r *rig) habit(name, extra string) string {
	r.t.Helper()
	args := `{"name":"` + name + `","behavior":"Do the thing","tiny_version":"Do the tiny thing","anchor":"after lunch"` + extra + `}`
	if _, err := r.tool("create_habit", args); err != nil {
		r.t.Fatalf("create_habit: %v", err)
	}
	habits, _ := r.db.ListHabits(context.Background(), false)
	return habits[len(habits)-1].ID
}

func (r *rig) logs(id string) []store.HabitLog {
	l, _ := r.db.HabitLogs(context.Background(), id, "2026-01-01", "2027-01-01")
	return l
}

func TestNewValidatesItsInputs(t *testing.T) {
	if _, err := New(Options{}); err == nil {
		t.Error("New() without a store succeeded")
	}
	db, _ := store.Open(context.Background(), filepath.Join(t.TempDir(), "k.db"))
	defer db.Close() //nolint:errcheck // test cleanup
	if _, err := New(Options{Store: db, Out: &outbox{}, Config: Config{QuietStart: "late"}}); err == nil {
		t.Error("an invalid quiet_start was accepted")
	}
}

func TestMorningCheckInComesOnceAtTheRightTime(t *testing.T) {
	r := newRig(t, nil)

	r.at(0, 6, 0) // quiet hours, and before the check-in
	r.at(0, 7, 10)
	if r.out.count() != 0 {
		t.Fatalf("a message went out before 07:15: %q", r.out.last().Text)
	}

	r.at(0, 7, 15)
	if r.out.count() != 1 || !strings.HasPrefix(r.out.last().Text, "Morning") && !strings.HasPrefix(r.out.last().Text, "Good to see") {
		t.Fatalf("morning check-in missing or odd: %+v", r.out.msgs)
	}
	r.at(0, 7, 20)
	r.at(0, 9, 0)
	if r.out.count() != 1 {
		t.Errorf("%d messages; the morning check-in must not repeat", r.out.count())
	}

	r.at(1, 7, 15) // next day
	if r.out.count() != 2 {
		t.Errorf("no morning check-in the next day (%d messages)", r.out.count())
	}
}

func TestMorningCheckInIsNotSentStale(t *testing.T) {
	r := newRig(t, nil)
	// The server was down all morning and comes back at 12:30.
	r.at(0, 12, 30)
	if r.out.count() != 0 {
		t.Errorf("a stale 'good morning' went out at lunchtime: %q", r.out.last().Text)
	}
}

func TestMorningIncludesTheSleepQuestionWhenANightWasInferred(t *testing.T) {
	r := newRig(t, nil)
	ctx := context.Background()
	_ = r.db.AddSleepEvent(ctx, store.SleepEvent{TS: when(-1, 22, 30), Kind: store.SleepActivity})
	_ = r.db.AddSleepEvent(ctx, store.SleepEvent{TS: when(0, 6, 20), Kind: store.SleepActivity})

	r.at(0, 7, 15)

	if r.out.count() != 1 {
		t.Fatalf("messages = %d", r.out.count())
	}
	m := r.out.last()
	if !strings.Contains(m.Text, "Close?") && !strings.Contains(m.Text, "Sound right?") {
		t.Errorf("no sleep question in %q", m.Text)
	}
	if !strings.Contains(m.Text, "hours") {
		t.Errorf("no hours in %q", m.Text)
	}
	if len(m.Buttons) != 1 || len(m.Buttons[0]) != 3 || m.Buttons[0][0].Data != "s:ok:2026-10-12" {
		t.Errorf("buttons = %+v", m.Buttons)
	}
	if n := strings.Count(m.Text, "?"); n != 1 {
		t.Errorf("%d questions in one message: %q", n, m.Text)
	}
}

func TestSayingGoodMorningCancelsTheCheckIn(t *testing.T) {
	r := newRig(t, nil)
	if kind, ok := r.c.NoteMessage(context.Background(), "good morning", when(0, 7, 5)); !ok || kind != "wake" {
		t.Fatalf("NoteMessage = %q, %v", kind, ok)
	}
	r.at(0, 7, 15)
	r.at(0, 8, 0)
	if r.out.count() != 0 {
		t.Errorf("a check-in followed the owner's own good morning: %q", r.out.last().Text)
	}
}

func TestMorningWaitsWhileTheOwnerIsMidConversation(t *testing.T) {
	r := newRig(t, nil)
	r.c.RecordActivity(context.Background(), when(0, 7, 10))

	r.at(0, 7, 15)
	if r.out.count() != 0 {
		t.Fatal("interrupted a conversation in progress")
	}
	r.at(0, 7, 35) // twenty-five minutes of quiet
	if r.out.count() != 1 {
		t.Errorf("the check-in never came once the conversation paused (%d)", r.out.count())
	}
}

func TestHabitPromptAppearsInsideItsWindowOnce(t *testing.T) {
	r := newRig(t, nil)
	id := r.habit("Walk after lunch", `,"window_start":"12:30","window_end":"15:00"`)

	r.at(0, 12, 0) // before the window
	if r.out.count() != 0 {
		t.Fatal("prompted before the window opened")
	}
	r.at(0, 12, 30)
	if r.out.count() != 1 {
		t.Fatalf("no prompt in the window (%d)", r.out.count())
	}
	m := r.out.last()
	if !strings.Contains(strings.ToLower(m.Text), "do the thing") && !strings.Contains(m.Text, "Do the thing") {
		t.Errorf("prompt does not state the plan: %q", m.Text)
	}
	if !strings.Contains(strings.ToLower(m.Text), "tiny") {
		t.Errorf("prompt does not offer the worst-day version: %q", m.Text)
	}
	if got := []string{m.Buttons[0][0].Data, m.Buttons[0][1].Data, m.Buttons[0][2].Data}; got[0] != "h:d:"+id || got[1] != "h:t:"+id || got[2] != "h:s:"+id {
		t.Errorf("buttons = %v", got)
	}

	r.at(0, 13, 0)
	r.at(0, 14, 30)
	if r.out.count() != 1 {
		t.Errorf("%d prompts; once a day is the rule", r.out.count())
	}
}

func TestHabitPromptSkippedWhenLoggedPausedOrOutOfWindow(t *testing.T) {
	r := newRig(t, nil)
	id := r.habit("Walk after lunch", `,"window_start":"12:30","window_end":"15:00"`)

	// Already logged before the window opens.
	_ = r.db.SetHabitLog(context.Background(), store.HabitLog{HabitID: id, Day: "2026-10-12", Status: store.LogDone, TS: when(0, 9, 0)})
	r.at(0, 12, 30)
	if r.out.count() != 0 {
		t.Error("prompted for a habit that was already done today")
	}

	// Tuesday: paused.
	if _, err := r.tool("update_habit", `{"habit":"`+id+`","state":"paused"}`); err != nil {
		t.Fatal(err)
	}
	r.at(1, 12, 30)
	if r.out.count() != 0 {
		t.Error("prompted for a paused habit")
	}

	// Wednesday: active again, but the window has closed.
	_, _ = r.tool("update_habit", `{"habit":"`+id+`","state":"active"}`)
	r.at(2, 15, 30)
	if r.out.count() != 0 {
		t.Error("prompted after the window closed")
	}
}

func TestCadenceDecidesWhichDaysAreDue(t *testing.T) {
	r := newRig(t, nil)
	r.habit("Gym", `,"cadence":"days:wed","window_start":"12:30","window_end":"15:00"`)

	r.at(0, 12, 30) // Monday
	r.at(1, 12, 30) // Tuesday
	if r.out.count() != 0 {
		t.Fatalf("prompted on a day the habit is not for: %q", r.out.last().Text)
	}
	r.at(2, 12, 30) // Wednesday
	if r.out.count() != 1 {
		t.Errorf("no prompt on Wednesday (%d)", r.out.count())
	}
}

func TestLapseProtocolEscalatesGently(t *testing.T) {
	r := newRig(t, nil)
	id := r.habit("Walk after lunch", `,"window_start":"12:30","window_end":"15:00"`)
	ctx := context.Background()

	// Monday done; Tuesday forgotten.
	_ = r.db.SetHabitLog(ctx, store.HabitLog{HabitID: id, Day: "2026-10-12", Status: store.LogDone, TS: when(0, 13, 0)})

	r.at(2, 12, 30) // Wednesday: one miss behind it
	if r.out.count() != 1 {
		t.Fatalf("no prompt on Wednesday")
	}
	first := r.out.last()
	if !strings.Contains(strings.ToLower(first.Text), "yesterday") && !strings.Contains(strings.ToLower(first.Text), "slipped") {
		t.Errorf("first lapse message does not acknowledge the miss: %q", first.Text)
	}
	if labels := labelsOf(first); labels != "Full version|Small version|Not today" {
		t.Errorf("first lapse offers %q; it should offer the small version", labels)
	}

	// Wednesday ignored too: two misses in a row.
	r.at(3, 12, 30)
	second := r.out.last()
	if labels := labelsOf(second); labels != "Make it smaller|Pause it|Keep going" {
		t.Errorf("repeat lapse offers %q; it should offer to resize, not push harder", labels)
	}
	for _, m := range []Message{first, second} {
		if v := voice.Check(m.Text, 50); len(v) > 0 {
			t.Errorf("lapse message breaks the voice guide: %v\n%q", v, m.Text)
		}
		low := strings.ToLower(m.Text)
		for _, banned := range []string{"streak", "failed", "missed", "lazy", "disappoint"} {
			if strings.Contains(low, banned) {
				t.Errorf("lapse message uses %q: %q", banned, m.Text)
			}
		}
	}
}

func labelsOf(m Message) string {
	var out []string
	for _, row := range m.Buttons {
		for _, b := range row {
			out = append(out, b.Label)
		}
	}
	return strings.Join(out, "|")
}

func TestEveningListsWhatIsLeftAndOffersARoughDay(t *testing.T) {
	r := newRig(t, nil)
	id := r.habit("Stretching", `,"window_start":"08:00","window_end":"10:00"`)

	r.at(0, 20, 30)
	if r.out.count() > 0 && strings.Contains(r.out.last().Text, "How did today go") {
		t.Fatal("evening check-in came before 21:00")
	}
	before := r.out.count()

	// Not logged: it appears on the evening buttons.
	r.at(0, 21, 0)
	if r.out.count() != before+1 {
		t.Fatalf("no evening check-in")
	}
	m := r.out.last()
	labels := labelsOf(m)
	if !strings.Contains(labels, "Stretching: done") || !strings.HasSuffix(labels, "Rough day") {
		t.Errorf("evening buttons = %q", labels)
	}
	if m.Buttons[0][0].Data != "h:d:"+id || m.Buttons[len(m.Buttons)-1][0].Data != "e:r" {
		t.Errorf("evening data = %+v", m.Buttons)
	}

	// Next day with nothing due: the quiet variant with a good-day button.
	_, _ = r.tool("update_habit", `{"habit":"`+id+`","state":"paused"}`)
	n := r.out.count()
	r.at(1, 21, 0)
	if r.out.count() != n+1 || !strings.Contains(labelsOf(r.out.last()), "Good day") {
		t.Errorf("empty-evening message = %+v", r.out.last())
	}
}

func TestWeeklyReviewOnSundayEveningOnly(t *testing.T) {
	r := newRig(t, nil)
	id := r.habit("Walk after lunch", `,"window_start":"12:30","window_end":"15:00"`)
	for d := range 5 {
		_ = r.db.SetHabitLog(context.Background(), store.HabitLog{HabitID: id, Day: monday.AddDate(0, 0, d).Format("2006-01-02"), Status: store.LogDone, TS: when(d, 13, 0)})
	}

	r.at(5, 21, 0) // Saturday: the usual evening check-in
	if hasWeekly(r.out) {
		t.Fatal("weekly review on a Saturday")
	}
	r.at(6, 20, 45) // Sunday, before the evening slot
	if hasWeekly(r.out) {
		t.Fatal("weekly review before the evening")
	}
	r.at(6, 21, 0)
	if !hasWeekly(r.out) {
		t.Fatalf("no weekly review on Sunday evening: %+v", r.out.msgs)
	}
	m := r.out.last()
	if !strings.Contains(m.Text, "Walk after lunch: 5 of 6 days") {
		t.Errorf("weekly review lacks the habit rate: %q", m.Text)
	}
	if strings.Count(m.Text, "?") != 1 {
		t.Errorf("weekly review should end with one question: %q", m.Text)
	}

	// It stands in for the evening check-in: no second message that night.
	n := r.out.count()
	r.at(6, 22, 45)
	if r.out.count() != n {
		t.Errorf("a second message followed the weekly review: %q", r.out.last().Text)
	}
}

func hasWeekly(o *outbox) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, m := range o.msgs {
		if strings.Contains(m.Text, "your week") || strings.Contains(m.Text, "Weekly look back") {
			return true
		}
	}
	return false
}

func TestWinddownIsOptIn(t *testing.T) {
	r := newRig(t, nil)

	r.at(0, 22, 15)
	for _, m := range r.out.msgs {
		if strings.Contains(m.Text, "Wind-down") || strings.Contains(m.Text, "bedtime") {
			t.Fatal("wind-down sent without being asked for")
		}
	}

	if _, err := r.tool("set_preferences", `{"winddown":true}`); err != nil {
		t.Fatal(err)
	}
	r.at(1, 21, 0) // the evening check-in goes first
	before := r.out.count()
	r.at(1, 22, 15) // too soon after it: the 90 minute gap holds
	if r.out.count() != before {
		t.Fatal("wind-down ignored the minimum gap")
	}
	r.at(1, 22, 35)
	if r.out.count() != before+1 {
		t.Fatalf("no wind-down after opting in")
	}
	if text := r.out.last().Text; !strings.Contains(text, "Wind-down") && !strings.Contains(text, "bedtime") {
		t.Errorf("wind-down text = %q", text)
	}
}
