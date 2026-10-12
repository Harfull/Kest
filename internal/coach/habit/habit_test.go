package habit

import (
	"strings"
	"testing"
	"time"

	"github.com/Harfull/Kest/internal/store"
)

// today is Wednesday 14 October 2026.
var today = time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)

func good() store.Habit {
	return store.Habit{
		ID: "h", Name: "Walk after lunch", Anchor: "After I finish lunch", Behavior: "Walk for 10 minutes",
		TinyVersion: "Put on shoes and step outside", Cadence: "daily", WindowStart: "12:30", WindowEnd: "15:00",
		State: store.HabitActive, CreatedAt: today.AddDate(0, 0, -40),
	}
}

// logsFor builds logs for the given offsets (0 = today, 1 = yesterday, ...).
func logsFor(status string, offsets ...int) []store.HabitLog {
	var out []store.HabitLog
	for _, n := range offsets {
		out = append(out, store.HabitLog{HabitID: "h", Day: today.AddDate(0, 0, -n).Format(dayLayout), Status: status})
	}
	return out
}

func TestCadenceParseAndRoundTrip(t *testing.T) {
	for in, want := range map[string]string{
		"": "daily", "daily": "daily", "WEEKDAYS": "weekdays", "times:3": "times:3",
		"days:wed,mon": "days:mon,wed", "days:SUN,sat": "days:sat,sun",
	} {
		c, err := ParseCadence(in)
		if err != nil || c.String() != want {
			t.Errorf("ParseCadence(%q) = %q, %v; want %q", in, c.String(), err, want)
		}
		if again, err := ParseCadence(c.String()); err != nil || again.String() != want {
			t.Errorf("round trip of %q failed: %v", want, err)
		}
	}
	for _, bad := range []string{"hourly", "days:", "days:funday", "times:0", "times:7", "times:x", "weekly"} {
		if _, err := ParseCadence(bad); err == nil {
			t.Errorf("ParseCadence(%q) succeeded", bad)
		}
	}
}

func TestCadenceExpectedAndPerWeek(t *testing.T) {
	mwf, _ := ParseCadence("days:mon,wed,fri")
	wd, _ := ParseCadence("weekdays")
	tt, _ := ParseCadence("times:3")
	daily, _ := ParseCadence("daily")

	if !mwf.ExpectedOn(time.Wednesday) || mwf.ExpectedOn(time.Tuesday) {
		t.Error("days: cadence wrong")
	}
	if wd.ExpectedOn(time.Sunday) || !wd.ExpectedOn(time.Friday) {
		t.Error("weekdays cadence wrong")
	}
	if tt.ExpectedOn(time.Monday) || !tt.Flexible() {
		t.Error("times: cadence must not be tied to a day")
	}
	for c, want := range map[Cadence]int{daily: 7, wd: 5, mwf: 3, tt: 3} {
		if got := c.PerWeek(); got != want {
			t.Errorf("%s PerWeek = %d, want %d", c, got, want)
		}
	}
}

func TestValidate(t *testing.T) {
	if err := Validate(good()); err != nil {
		t.Fatalf("a good habit was rejected: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*store.Habit)
	}{
		{"no name", func(h *store.Habit) { h.Name = "  " }},
		{"name too long", func(h *store.Habit) { h.Name = strings.Repeat("a", 61) }},
		{"no behavior", func(h *store.Habit) { h.Behavior = "" }},
		{"no tiny version", func(h *store.Habit) { h.TinyVersion = "" }},
		{"bad cadence", func(h *store.Habit) { h.Cadence = "sometimes" }},
		{"bad window start", func(h *store.Habit) { h.WindowStart = "noon" }},
		{"window ends before it starts", func(h *store.Habit) { h.WindowStart, h.WindowEnd = "15:00", "12:30" }},
		{"window too short", func(h *store.Habit) { h.WindowStart, h.WindowEnd = "12:00", "12:10" }},
		{"anchor too long", func(h *store.Habit) { h.Anchor = strings.Repeat("a", 121) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := good()
			tt.mutate(&h)
			if Validate(h) == nil {
				t.Error("Validate() = nil, want an error")
			}
		})
	}
}

func TestWindowInLocalTime(t *testing.T) {
	ny, _ := time.LoadLocation("America/New_York")
	s, e := Window(good(), today, ny)
	if s.Format("15:04") != "12:30" || e.Format("15:04") != "15:00" || s.Location() != ny {
		t.Errorf("window = %v - %v", s, e)
	}
	// 12:30 in New York on 14 Oct (EDT, UTC-4) is 16:30 UTC.
	if s.UTC().Hour() != 16 || s.UTC().Minute() != 30 {
		t.Errorf("window start in UTC = %v", s.UTC())
	}
}

func TestComputeRollingRates(t *testing.T) {
	h := good()

	// Done on 5 of the last 6 completed days and today not yet logged.
	logs := logsFor(store.LogDone, 2, 3, 4, 5, 6)
	c := Compute(h, logs, 30, today, time.UTC)

	if c.Done7 != 5 || c.Expected7 != 6 {
		t.Errorf("7-day counts = %d/%d, want 5/6 (today is not over, so it is not expected yet)", c.Done7, c.Expected7)
	}
	if c.Rate7 < 0.83 || c.Rate7 > 0.84 {
		t.Errorf("Rate7 = %v", c.Rate7)
	}
	if c.MissedInARow != 1 {
		t.Errorf("MissedInARow = %d, want 1 (yesterday)", c.MissedInARow)
	}
	if c.ShowingUpDays != 30 {
		t.Errorf("ShowingUpDays = %d, want it passed through", c.ShowingUpDays)
	}
}

func TestTodayCountsOnceLogged(t *testing.T) {
	h := good()
	c := Compute(h, logsFor(store.LogDone, 0, 1, 2), 3, today, time.UTC)
	if c.Done7 != 3 || c.Expected7 != 7 {
		t.Errorf("counts = %d/%d, want 3/7", c.Done7, c.Expected7)
	}
	if c.MissedInARow != 0 {
		t.Errorf("MissedInARow = %d, want 0 after a completed yesterday", c.MissedInARow)
	}
}

func TestTinyCountsAndSkipDoesNot(t *testing.T) {
	h := good()
	logs := append(logsFor(store.LogTiny, 1, 2), logsFor(store.LogSkipped, 3)...)
	c := Compute(h, logs, 2, today, time.UTC)

	if c.Done7 != 2 {
		t.Errorf("Done7 = %d; the tiny version must count, a skip must not", c.Done7)
	}
	if c.MissedInARow != 0 {
		t.Errorf("MissedInARow = %d; yesterday's tiny version was a completion", c.MissedInARow)
	}

	skipped := Compute(h, logsFor(store.LogSkipped, 1, 2), 0, today, time.UTC)
	if skipped.MissedInARow != 2+38 { // 2 skipped days, then the unlogged days before them
		t.Errorf("MissedInARow = %d", skipped.MissedInARow)
	}
}

func TestOneMissNeverResetsToZero(t *testing.T) {
	h := good()
	// 27 completed days in a row, then yesterday missed.
	var offsets []int
	for n := 2; n <= 28; n++ {
		offsets = append(offsets, n)
	}
	c := Compute(h, logsFor(store.LogDone, offsets...), 27, today, time.UTC)

	if c.Rate7 < 0.8 || c.Rate28 < 0.9 {
		t.Errorf("rates after one miss = %.2f / %.2f; one miss should only dent them", c.Rate7, c.Rate28)
	}
	if c.ShowingUpDays != 27 {
		t.Errorf("ShowingUpDays = %d; it never resets", c.ShowingUpDays)
	}
	if c.MissedInARow != 1 {
		t.Errorf("MissedInARow = %d", c.MissedInARow)
	}
}

func TestNewHabitsAreNotJudgedForTheBeforeTimes(t *testing.T) {
	h := good()

	h.CreatedAt = today // created today
	c := Compute(h, nil, 0, today, time.UTC)
	if c.Rate7 != -1 || c.Rate28 != -1 || c.MissedInARow != 0 {
		t.Errorf("brand new habit = %+v; nothing is expected yet", c)
	}

	h.CreatedAt = today.AddDate(0, 0, -2) // two earlier days existed
	c = Compute(h, logsFor(store.LogDone, 2), 1, today, time.UTC)
	if c.Expected7 != 2 || c.Done7 != 1 || c.MissedInARow != 1 {
		t.Errorf("young habit = %+v; want 1 of 2 expected, 1 missed", c)
	}
}

func TestWeekdaysCadenceIgnoresWeekends(t *testing.T) {
	h := good()
	h.Cadence = "weekdays"
	friday := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC).AddDate(0, 0, -3) // Friday 9 Oct is 3 days before Monday 12
	monday := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)

	// Done on Friday; the weekend passes; today is Monday and not yet logged.
	logs := []store.HabitLog{{HabitID: "h", Day: friday.Format(dayLayout), Status: store.LogDone}}
	c := Compute(h, logs, 1, monday, time.UTC)
	if c.MissedInARow != 0 {
		t.Errorf("MissedInARow = %d; a weekend is not a miss", c.MissedInARow)
	}

	// Nothing since Thursday: Friday missed, then the weekend, so the count is 1 + earlier days.
	c = Compute(h, nil, 0, monday, time.UTC)
	if c.MissedInARow < 1 {
		t.Errorf("MissedInARow = %d, want >= 1", c.MissedInARow)
	}
	if c.Expected7 != 4 { // the 7 days ending today hold Tue-Fri; weekends and unlogged today are excluded
		t.Errorf("Expected7 = %d, want 4", c.Expected7)
	}
}

func TestFlexibleCadence(t *testing.T) {
	h := good()
	h.Cadence = "times:3"

	c := Compute(h, logsFor(store.LogDone, 1, 3, 5), 3, today, time.UTC)
	if c.Done7 != 3 || c.Expected7 != 3 || c.Rate7 != 1 {
		t.Errorf("met quota = %+v", c)
	}
	if c.MissedInARow != 0 {
		t.Errorf("MissedInARow = %d", c.MissedInARow)
	}

	over := Compute(h, logsFor(store.LogDone, 0, 1, 2, 3, 4), 5, today, time.UTC)
	if over.Done7 != 3 {
		t.Errorf("Done7 = %d; extra sessions beyond the quota should not inflate the rate", over.Done7)
	}

	// Nothing for 12 days with a spacing of 3 days: several gaps missed.
	lapsed := Compute(h, logsFor(store.LogDone, 13), 1, today, time.UTC)
	if lapsed.MissedInARow < 2 {
		t.Errorf("lapsed MissedInARow = %d", lapsed.MissedInARow)
	}
}

func TestDueToday(t *testing.T) {
	h := good()
	logged := store.HabitLog{Status: store.LogDone}

	if !DueToday(h, nil, 0, today, time.UTC) {
		t.Error("daily habit with no log should be due")
	}
	if DueToday(h, &logged, 0, today, time.UTC) {
		t.Error("a logged habit is not due")
	}
	h.State = store.HabitPaused
	if DueToday(h, nil, 0, today, time.UTC) {
		t.Error("a paused habit is not due")
	}

	h = good()
	h.Cadence = "days:mon"
	if DueToday(h, nil, 0, today, time.UTC) { // Wednesday
		t.Error("habit for Mondays is due on Wednesday")
	}

	h.Cadence = "times:2"
	if !DueToday(h, nil, 1, today, time.UTC) || DueToday(h, nil, 2, today, time.UTC) {
		t.Error("flexible habit should be due until the weekly quota is met")
	}
}
