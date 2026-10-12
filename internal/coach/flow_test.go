package coach

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Harfull/Kest/internal/store"
)

func TestHabitButtonsLogTheDay(t *testing.T) {
	cases := []struct {
		data, status string
	}{
		{"h:d:", store.LogDone},
		{"h:t:", store.LogTiny},
		{"h:s:", store.LogSkipped},
	}
	for _, tc := range cases {
		t.Run(tc.status, func(t *testing.T) {
			r := newRig(t, nil)
			id := r.habit("Walk", "")
			r.clk.Set(when(0, 13, 0))

			reply, handled := r.c.HandleCallback(context.Background(), tc.data+id)
			if !handled || reply.Text == "" {
				t.Fatalf("HandleCallback() = %q, %v", reply.Text, handled)
			}
			logs := r.logs(id)
			if len(logs) != 1 || logs[0].Status != tc.status {
				t.Fatalf("logs = %+v, want one %s", logs, tc.status)
			}
		})
	}
}

func TestHabitPauseAndUnknownButtons(t *testing.T) {
	r := newRig(t, nil)
	id := r.habit("Walk", "")
	ctx := context.Background()

	if _, handled := r.c.HandleCallback(ctx, "h:p:"+id); !handled {
		t.Fatal("pause not handled")
	}
	h, err := r.db.GetHabit(ctx, id)
	if err != nil || h.State != store.HabitPaused {
		t.Fatalf("habit state = %q, err %v; want paused", h.State, err)
	}

	if reply, _ := r.c.HandleCallback(ctx, "h:d:nope"); !strings.Contains(reply.Text, "not active") {
		t.Errorf("unknown habit reply = %q", reply.Text)
	}
	if reply, _ := r.c.HandleCallback(ctx, "h:zz:"+id); !strings.Contains(reply.Text, "did not understand") {
		t.Errorf("unknown action reply = %q", reply.Text)
	}
	for _, data := range []string{"", "nothing", "x:y", "approve:1"} {
		if _, handled := r.c.HandleCallback(ctx, data); handled {
			t.Errorf("HandleCallback(%q) claimed foreign data", data)
		}
	}
}

func TestEveningAndWeeklyButtonsAnswerGently(t *testing.T) {
	r := newRig(t, nil)
	for _, data := range []string{"e:g", "e:r", "w:adj", "b:keep"} {
		reply, handled := r.c.HandleCallback(context.Background(), data)
		if !handled || reply.Text == "" {
			t.Errorf("HandleCallback(%q) = %q, %v", data, reply.Text, handled)
		}
	}
}

func TestSleepButtonsConfirmAndTeach(t *testing.T) {
	estimate := func(ctx context.Context, r *rig) store.SleepNight {
		n := store.SleepNight{
			Day: "2026-10-12", Onset: when(-1, 23, 0), Wake: when(0, 7, 0), Confidence: store.ConfInferred,
		}
		if err := r.db.SetSleepNight(ctx, n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	t.Run("about right confirms without teaching", func(t *testing.T) {
		r := newRig(t, nil)
		ctx := context.Background()
		n := estimate(ctx, r)
		r.c.HandleCallback(ctx, "s:ok:"+n.Day)
		got, _ := r.db.GetSleepNight(ctx, n.Day)
		if !got.UserConfirmed || got.Confidence != store.ConfConfirmed || !got.Onset.Equal(n.Onset) {
			t.Errorf("night = %+v", got)
		}
		if _, err := r.db.GetProfile(ctx, sleepParamsKey); err == nil {
			t.Error("a correct estimate changed the learned corrections")
		}
	})

	t.Run("shorter moves the onset later and teaches", func(t *testing.T) {
		r := newRig(t, nil)
		ctx := context.Background()
		n := estimate(ctx, r)
		r.c.HandleCallback(ctx, "s:sh:"+n.Day)
		got, _ := r.db.GetSleepNight(ctx, n.Day)
		if want := n.Onset.Add(45 * time.Minute); !got.Onset.Equal(want) || !got.UserConfirmed {
			t.Errorf("onset = %v confirmed = %v, want %v", got.Onset, got.UserConfirmed, want)
		}
		if _, err := r.db.GetProfile(ctx, sleepParamsKey); err != nil {
			t.Errorf("corrections not saved: %v", err)
		}
	})

	t.Run("longer moves the onset earlier", func(t *testing.T) {
		r := newRig(t, nil)
		ctx := context.Background()
		n := estimate(ctx, r)
		r.c.HandleCallback(ctx, "s:lg:"+n.Day)
		got, _ := r.db.GetSleepNight(ctx, n.Day)
		if want := n.Onset.Add(-45 * time.Minute); !got.Onset.Equal(want) {
			t.Errorf("onset = %v, want %v", got.Onset, want)
		}
	})

	t.Run("a second press does nothing", func(t *testing.T) {
		r := newRig(t, nil)
		ctx := context.Background()
		n := estimate(ctx, r)
		r.c.HandleCallback(ctx, "s:ok:"+n.Day)
		r.c.HandleCallback(ctx, "s:sh:"+n.Day)
		got, _ := r.db.GetSleepNight(ctx, n.Day)
		if !got.Onset.Equal(n.Onset) {
			t.Errorf("a confirmed night was changed to %v", got.Onset)
		}
	})

	t.Run("a missing night is acknowledged", func(t *testing.T) {
		r := newRig(t, nil)
		ctx := context.Background()
		if reply, handled := r.c.HandleCallback(ctx, "s:ok:2020-01-01"); !handled || reply.Text == "" {
			t.Errorf("reply = %q, %v", reply.Text, handled)
		}
	})
}

func TestStatingBedtimeAndWakeBuildsAnExplicitNight(t *testing.T) {
	r := newRig(t, nil)
	ctx := context.Background()

	r.clk.Set(when(0, 23, 10))
	if kind, found := r.c.NoteMessage(ctx, "heading to bed now", r.clk.Now()); !found || kind != "bedtime" {
		t.Fatalf("NoteMessage(bed) = %q, %v", kind, found)
	}
	if _, found := r.c.NoteMessage(ctx, "what is the weather", r.clk.Now()); found {
		t.Error("an ordinary message was taken for a sleep signal")
	}

	r.clk.Set(when(1, 6, 50))
	if kind, found := r.c.NoteMessage(ctx, "good morning", r.clk.Now()); !found || kind != "wake" {
		t.Fatalf("NoteMessage(wake) = %q, %v", kind, found)
	}
	night, err := r.db.GetSleepNight(ctx, "2026-10-13")
	if err != nil {
		t.Fatalf("no night stored: %v", err)
	}
	if night.Confidence != store.ConfExplicit {
		t.Errorf("confidence = %q, want explicit", night.Confidence)
	}
	if got := night.Wake.Sub(night.Onset); got < 7*time.Hour || got > 8*time.Hour {
		t.Errorf("night length = %v, want about 7h40m", got)
	}
}

func TestActivityCreditsTheMessageItAnswers(t *testing.T) {
	r := newRig(t, nil)
	ctx := context.Background()
	r.at(0, 8, 0)
	if r.out.count() != 1 {
		t.Fatalf("no morning message to answer")
	}

	r.c.RecordActivity(ctx, when(0, 8, 10))
	history, _ := r.db.Notifications(ctx, when(-1, 0, 0))
	if len(history) != 1 || history[0].RespondedAt.IsZero() {
		t.Errorf("history = %+v, want the morning message marked as answered", history)
	}
}

func TestPreferencesChangeWhatIsSent(t *testing.T) {
	t.Run("silent turns everything off", func(t *testing.T) {
		r := newRig(t, nil)
		if _, err := r.tool("set_preferences", `{"silent":true}`); err != nil {
			t.Fatal(err)
		}
		r.at(0, 8, 0)
		r.at(0, 21, 0)
		if r.out.count() != 0 {
			t.Errorf("%d messages sent while silent", r.out.count())
		}
		if _, err := r.tool("set_preferences", `{"silent":false}`); err != nil {
			t.Fatal(err)
		}
		r.at(0, 8, 30)
		if r.out.count() != 1 {
			t.Errorf("nothing sent after turning messages back on")
		}
	})

	t.Run("a kind switched off stays off", func(t *testing.T) {
		r := newRig(t, nil)
		if _, err := r.tool("set_preferences", `{"morning":false}`); err != nil {
			t.Fatal(err)
		}
		r.at(0, 8, 0)
		if r.out.count() != 0 {
			t.Error("morning check-in sent after it was turned off")
		}
		r.at(0, 21, 0)
		if r.out.count() != 1 {
			t.Error("the evening check-in should be unaffected")
		}
	})

	t.Run("a lower daily cap holds", func(t *testing.T) {
		r := newRig(t, nil)
		r.habit("Walk", `,"window_start":"09:00","window_end":"20:00"`)
		if _, err := r.tool("set_preferences", `{"max_per_day":1}`); err != nil {
			t.Fatal(err)
		}
		for _, hm := range [][2]int{{8, 0}, {9, 30}, {12, 0}, {21, 0}} {
			r.at(0, hm[0], hm[1])
		}
		if r.out.count() != 1 {
			t.Errorf("%d messages with a cap of 1", r.out.count())
		}
	})

	t.Run("the prompt context reflects them", func(t *testing.T) {
		r := newRig(t, nil)
		if _, err := r.tool("set_preferences", `{"evening":false}`); err != nil {
			t.Fatal(err)
		}
		got := r.c.PromptContext(context.Background())
		if !strings.Contains(strings.ToLower(got), "evening") {
			t.Errorf("PromptContext() = %q, want a mention of the evening setting", got)
		}
	})
}

func TestIgnoredMessagesLeadToTheBackOffQuestion(t *testing.T) {
	r := newRig(t, func(c *Config) { c.MaxPerDay = 4 })
	ctx := context.Background()
	r.habit("Walk", `,"window_start":"09:00","window_end":"20:00"`)

	asked := -1
	for day := 0; day < 12 && asked < 0; day++ {
		for _, hm := range [][2]int{{8, 0}, {9, 30}, {13, 0}, {21, 0}} {
			r.at(day, hm[0], hm[1])
		}
		for i := 0; i < r.out.count(); i++ {
			if hasButton(r.out.msgs[i], "b:less") {
				asked = day
			}
		}
	}
	if asked < 0 {
		t.Fatal("the owner ignored everything for 12 days and was never asked about it")
	}

	before := r.c.dailyCap(r.c.prefs(ctx))
	reply, handled := r.c.HandleCallback(ctx, "b:less")
	if !handled || reply.Text == "" {
		t.Fatal("b:less not handled")
	}
	if after := r.c.dailyCap(r.c.prefs(ctx)); after != before-1 {
		t.Errorf("cap after \"less\" = %d, want %d", after, before-1)
	}
}

func hasButton(m Message, data string) bool {
	for _, row := range m.Buttons {
		for _, b := range row {
			if b.Data == data {
				return true
			}
		}
	}
	return false
}

func TestToolsHandleBadInput(t *testing.T) {
	r := newRig(t, nil)
	cases := []struct{ name, tool, args string }{
		{"habit with no name", "create_habit", `{"behavior":"x","tiny_version":"y"}`},
		{"habit with a bad cadence", "create_habit", `{"name":"A","behavior":"x","tiny_version":"y","cadence":"whenever"}`},
		{"habit with a bad time", "create_habit", `{"name":"A","behavior":"x","tiny_version":"y","window_start":"25:99"}`},
		{"log of an unknown habit", "log_habit", `{"habit":"ghost","status":"done"}`},
		{"log with a bad status", "log_habit", `{"habit":"ghost","status":"maybe"}`},
		{"update of an unknown habit", "update_habit", `{"habit":"ghost","name":"B"}`},
		{"sleep with a bad kind", "log_sleep", `{"kind":"nap"}`},
		{"sleep with a bad time", "log_sleep", `{"kind":"wake","time":"soonish"}`},
		{"unparseable arguments", "set_preferences", `{"silent":"yes"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := r.tool(tc.tool, tc.args); err == nil {
				t.Errorf("%s(%s) succeeded, want an error", tc.tool, tc.args)
			}
		})
	}
}

func TestTooManyHabitsAreRefused(t *testing.T) {
	r := newRig(t, nil)
	for i := range maxActiveHabits {
		r.habit("Habit "+string(rune('A'+i)), "")
	}
	_, err := r.tool("create_habit", `{"name":"One more","behavior":"x","tiny_version":"y"}`)
	if err == nil {
		t.Errorf("an eighth active habit was accepted; the limit is %d", maxActiveHabits)
	}
}

func TestLogHabitBackdatesWithinTwoWeeksOnly(t *testing.T) {
	r := newRig(t, nil)
	r.clk.Set(when(20, 12, 0))
	id := r.habit("Walk", "")
	if _, err := r.tool("log_habit", `{"habit":"Walk","status":"done","date":"2026-10-30"}`); err != nil {
		t.Errorf("a log from 3 days ago failed: %v", err)
	}
	if _, err := r.tool("log_habit", `{"habit":"`+id+`","status":"done","date":"2026-09-01"}`); err == nil {
		t.Error("a log from two months ago was accepted")
	}
}

// asleep reports whether the simulated owner is asleep at ts. A responsive owner
// is up from 07:00 and gone by 22:00, with half an hour of slack either side
// because the coach infers sleep from chat gaps; the silent owner is held to the
// declared quiet hours.
func asleep(ts time.Time, responsive bool) bool {
	min := ts.Hour()*60 + ts.Minute()
	if responsive {
		return min >= 22*60+30 || min < 6*60+30
	}
	return min >= 23*60 || min < 7*60
}

// TestTwoMonthSimulation is the Phase 3 exit check: over 60 days with the
// coach ticking every five minutes, nothing is ever sent in the declared quiet
// hours or above the daily cap, and an owner who stops answering is messaged
// far less than one who keeps answering.
func TestTwoMonthSimulation(t *testing.T) {
	const days = 60
	type result struct {
		perDay map[string]int
		quiet  []string
		total  int
	}

	// The owner is active 07:00-22:00 when responsive. The declared quiet hours
	// (23:00-07:00) apply until the coach has seen enough days to learn better.
	run := func(t *testing.T, responsive bool) result {
		t.Helper()
		r := newRig(t, nil)
		r.habit("Walk", `,"window_start":"12:00","window_end":"20:00"`)
		r.habit("Stretch", `,"window_start":"07:30","window_end":"10:00"`)

		res := result{perDay: map[string]int{}}
		seen := 0
		ctx := context.Background()
		for d := range days {
			for step := 0; step < 24*12; step++ {
				ts := when(d, 0, 0).Add(time.Duration(step) * 5 * time.Minute)
				r.clk.Set(ts)

				// A responsive owner chats during the day and answers the buttons.
				if responsive && ts.Hour() >= 7 && ts.Hour() < 22 && step%6 == 0 {
					r.c.RecordActivity(ctx, ts)
				}
				if err := r.c.Tick(ctx); err != nil {
					t.Fatalf("day %d %s: Tick() = %v", d, ts.Format("15:04"), err)
				}
				for ; seen < r.out.count(); seen++ {
					msg := r.out.msgs[seen]
					res.total++
					res.perDay[ts.Format("2006-01-02")]++
					if asleep(ts, responsive) {
						res.quiet = append(res.quiet, ts.Format("2006-01-02 15:04"))
					}
					for _, row := range msg.Buttons {
						for _, b := range row {
							if len(b.Data) > 64 {
								t.Errorf("callback data %q is %d bytes; Telegram allows 64", b.Data, len(b.Data))
							}
						}
					}
				}
			}
		}
		return res
	}

	responsive := run(t, true)
	silent := run(t, false)

	for name, res := range map[string]result{"responsive": responsive, "silent": silent} {
		if len(res.quiet) > 0 {
			t.Errorf("%s owner: %d messages in quiet hours, first at %s", name, len(res.quiet), res.quiet[0])
		}
		for day, n := range res.perDay {
			if n > 3 {
				t.Errorf("%s owner: %d messages on %s, cap is 3", name, n, day)
			}
		}
	}
	if responsive.total < days { // at least about one a day
		t.Errorf("responsive owner got only %d messages in %d days", responsive.total, days)
	}
	if silent.total*2 > responsive.total {
		t.Errorf("an owner who never answers got %d messages against %d for one who does; want under half", silent.total, responsive.total)
	}
	t.Logf("messages over %d days: responsive %d, silent %d", days, responsive.total, silent.total)
}
