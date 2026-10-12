// Package habit holds the rules for habits: how often one is expected, whether
// a plan is well formed, and how consistently it is being kept.
//
// Consistency is a rolling completion rate, not a streak: a missed day lowers
// the rate a little and never resets anything to zero. See
// docs/research/behavior-science/README.md and ADR 0006.
//
// Everything here is a pure function of its inputs; storage and messaging live
// elsewhere.
package habit

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Harfull/Kest/internal/store"
)

const dayLayout = "2006-01-02"

// Day formats a time as a local calendar day, YYYY-MM-DD.
func Day(t time.Time, loc *time.Location) string { return t.In(loc).Format(dayLayout) }

// Cadence says on which days a habit is expected.
type Cadence struct {
	kind  string // daily, weekdays, days, times
	days  uint   // bit per time.Weekday, for kind "days"
	times int    // per week, for kind "times"
}

var dayNames = map[string]time.Weekday{
	"sun": time.Sunday, "mon": time.Monday, "tue": time.Tuesday, "wed": time.Wednesday,
	"thu": time.Thursday, "fri": time.Friday, "sat": time.Saturday,
}

// ParseCadence parses "daily", "weekdays", "days:mon,wed,fri" or "times:3"
// (any three days in a week).
func ParseCadence(s string) (Cadence, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	switch {
	case s == "" || s == "daily":
		return Cadence{kind: "daily"}, nil
	case s == "weekdays":
		return Cadence{kind: "weekdays"}, nil
	case strings.HasPrefix(s, "days:"):
		var mask uint
		for _, p := range strings.Split(strings.TrimPrefix(s, "days:"), ",") {
			d, ok := dayNames[strings.TrimSpace(p)]
			if !ok {
				return Cadence{}, fmt.Errorf("cadence %q: unknown day %q (use mon, tue, wed, thu, fri, sat, sun)", s, p)
			}
			mask |= 1 << uint(d)
		}
		if mask == 0 {
			return Cadence{}, fmt.Errorf("cadence %q: no days given", s)
		}
		return Cadence{kind: "days", days: mask}, nil
	case strings.HasPrefix(s, "times:"):
		n, err := strconv.Atoi(strings.TrimPrefix(s, "times:"))
		if err != nil || n < 1 || n > 6 {
			return Cadence{}, fmt.Errorf("cadence %q: times must be a number from 1 to 6 (use daily for every day)", s)
		}
		return Cadence{kind: "times", times: n}, nil
	}
	return Cadence{}, fmt.Errorf("unknown cadence %q (use daily, weekdays, days:mon,wed or times:3)", s)
}

// String returns the stored spelling.
func (c Cadence) String() string {
	switch c.kind {
	case "days":
		var parts []string
		for _, d := range []time.Weekday{time.Monday, time.Tuesday, time.Wednesday, time.Thursday, time.Friday, time.Saturday, time.Sunday} {
			if c.days&(1<<uint(d)) != 0 {
				parts = append(parts, strings.ToLower(d.String()[:3]))
			}
		}
		return "days:" + strings.Join(parts, ",")
	case "times":
		return "times:" + strconv.Itoa(c.times)
	case "":
		return "daily"
	}
	return c.kind
}

// Flexible reports whether the habit is "N times a week" on days of the
// owner's choosing, rather than fixed days.
func (c Cadence) Flexible() bool { return c.kind == "times" }

// PerWeek returns how many completions a week the cadence asks for.
func (c Cadence) PerWeek() int {
	switch c.kind {
	case "weekdays":
		return 5
	case "days":
		n := 0
		for d := time.Sunday; d <= time.Saturday; d++ {
			if c.days&(1<<uint(d)) != 0 {
				n++
			}
		}
		return n
	case "times":
		return c.times
	}
	return 7
}

// ExpectedOn reports whether the habit is expected on that weekday. Flexible
// cadences are never tied to a particular day, so they return false.
func (c Cadence) ExpectedOn(d time.Weekday) bool {
	switch c.kind {
	case "", "daily":
		return true
	case "weekdays":
		return d != time.Saturday && d != time.Sunday
	case "days":
		return c.days&(1<<uint(d)) != 0
	}
	return false
}

// ParseHM parses a local HH:MM time into minutes since midnight.
func ParseHM(s string) (int, error) {
	t, err := time.Parse("15:04", strings.TrimSpace(s))
	if err != nil {
		return 0, fmt.Errorf("time %q must be HH:MM in 24-hour time", s)
	}
	return t.Hour()*60 + t.Minute(), nil
}

// Validate checks that a habit's plan is complete and sensible. A habit needs a
// behavior and a worst-day version, which is what makes it tiny enough to keep.
func Validate(h store.Habit) error {
	var errs []error
	add := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }

	if n := len([]rune(strings.TrimSpace(h.Name))); n == 0 || n > 60 {
		add("name must be 1 to 60 characters")
	}
	if n := len([]rune(strings.TrimSpace(h.Behavior))); n == 0 || n > 200 {
		add("behavior must be 1 to 200 characters")
	}
	if n := len([]rune(strings.TrimSpace(h.TinyVersion))); n == 0 || n > 200 {
		add("tiny_version is required (the version you could do on your worst day), up to 200 characters")
	}
	if len([]rune(h.Anchor)) > 120 || len([]rune(h.Why)) > 300 {
		add("anchor is limited to 120 characters and why to 300")
	}
	if _, err := ParseCadence(h.Cadence); err != nil {
		errs = append(errs, err)
	}
	start, err1 := ParseHM(h.WindowStart)
	end, err2 := ParseHM(h.WindowEnd)
	switch {
	case err1 != nil:
		errs = append(errs, err1)
	case err2 != nil:
		errs = append(errs, err2)
	case end <= start:
		add("window_end (%s) must be after window_start (%s) on the same day", h.WindowEnd, h.WindowStart)
	case end-start < 30:
		add("the window must be at least 30 minutes long")
	}
	return errors.Join(errs...)
}

// Window returns the prompt window for a habit on a local day.
func Window(h store.Habit, day time.Time, loc *time.Location) (start, end time.Time) {
	s, _ := ParseHM(h.WindowStart)
	e, _ := ParseHM(h.WindowEnd)
	d := day.In(loc)
	at := func(m int) time.Time { return time.Date(d.Year(), d.Month(), d.Day(), m/60, m%60, 0, 0, loc) }
	return at(s), at(e)
}

// Consistency summarizes how well a habit is being kept.
type Consistency struct {
	// Rate7 and Rate28 are the share of expected occurrences completed over the
	// last 7 and 28 days, from 0 to 1. They are -1 when nothing was expected yet.
	Rate7, Rate28 float64
	// Done7 / Expected7 and Done28 / Expected28 are the counts behind the rates.
	Done7, Expected7   int
	Done28, Expected28 int
	// ShowingUpDays counts every day the habit was done, ever. It never resets.
	ShowingUpDays int
	// MissedInARow counts expected occurrences missed in a row, ending yesterday.
	// It exists to trigger the lapse protocol and is never shown as a countdown.
	MissedInARow int
}

// completed reports whether a log counts as keeping the habit. The tiny version
// counts: showing up small is still showing up.
func completed(l store.HabitLog) bool { return l.Status == store.LogDone || l.Status == store.LogTiny }

// Compute works out consistency as of the local day `today`.
//
// Today only counts once it is logged, because the day is not over; earlier
// days count whether or not anything was logged. Days before the habit existed
// are never expected.
func Compute(h store.Habit, logs []store.HabitLog, showingUp int, today time.Time, loc *time.Location) Consistency {
	cad, err := ParseCadence(h.Cadence)
	if err != nil {
		cad = Cadence{kind: "daily"}
	}
	byDay := make(map[string]store.HabitLog, len(logs))
	for _, l := range logs {
		byDay[l.Day] = l
	}

	todayDay := time.Date(today.In(loc).Year(), today.In(loc).Month(), today.In(loc).Day(), 12, 0, 0, 0, loc)
	created := h.CreatedAt.In(loc)
	createdDay := time.Date(created.Year(), created.Month(), created.Day(), 12, 0, 0, 0, loc)

	// at returns the local day n days before today, at noon to dodge DST edges.
	at := func(n int) time.Time { return todayDay.AddDate(0, 0, -n) }
	age := int(todayDay.Sub(createdDay).Hours()/24+0.5) + 1 // days the habit has existed, including today

	c := Consistency{ShowingUpDays: showingUp, Rate7: -1, Rate28: -1}

	if cad.Flexible() {
		c.Done7, c.Expected7 = flexible(cad, byDay, at, 7, age)
		c.Done28, c.Expected28 = flexible(cad, byDay, at, 28, age)
		c.MissedInARow = flexibleMisses(cad, byDay, at, age)
	} else {
		for n := 0; n < 28 && n < age; n++ {
			day := at(n)
			if !cad.ExpectedOn(day.Weekday()) {
				continue
			}
			l, logged := byDay[day.Format(dayLayout)]
			if n == 0 && !logged {
				continue // today is not over
			}
			done := logged && completed(l)
			if n < 7 {
				c.Expected7++
				if done {
					c.Done7++
				}
			}
			c.Expected28++
			if done {
				c.Done28++
			}
		}
		for n := 1; n < age; n++ { // consecutive expected misses, ending yesterday
			day := at(n)
			if !cad.ExpectedOn(day.Weekday()) {
				continue
			}
			if l, ok := byDay[day.Format(dayLayout)]; ok && completed(l) {
				break
			}
			c.MissedInARow++
		}
	}

	if c.Expected7 > 0 {
		c.Rate7 = float64(c.Done7) / float64(c.Expected7)
	}
	if c.Expected28 > 0 {
		c.Rate28 = float64(c.Done28) / float64(c.Expected28)
	}
	return c
}

// flexible counts completions against "N a week" over the last span days,
// scaled for a habit younger than the span.
func flexible(cad Cadence, byDay map[string]store.HabitLog, at func(int) time.Time, span, age int) (done, expected int) {
	days := min(span, age)
	for n := 0; n < days; n++ {
		if l, ok := byDay[at(n).Format(dayLayout)]; ok && completed(l) {
			done++
		}
	}
	expected = (cad.times*days + 6) / 7 // round up
	if expected == 0 {
		return 0, 0
	}
	return min(done, expected), expected
}

// flexibleMisses estimates missed occurrences for "N a week": how many expected
// gaps have passed since the last completion, ending yesterday.
func flexibleMisses(cad Cadence, byDay map[string]store.HabitLog, at func(int) time.Time, age int) int {
	spacing := (7 + cad.times - 1) / cad.times // days between occurrences
	since := 0
	for n := 1; n < age; n++ {
		if l, ok := byDay[at(n).Format(dayLayout)]; ok && completed(l) {
			break
		}
		since++
	}
	if since < spacing {
		return 0
	}
	return since / spacing
}

// DueToday reports whether the habit is expected today and nothing has been
// logged for it yet. Paused and retired habits are never due. Flexible habits
// are due until the week's quota is met.
func DueToday(h store.Habit, todayLog *store.HabitLog, doneThisWeek int, today time.Time, loc *time.Location) bool {
	if h.State != store.HabitActive || todayLog != nil {
		return false
	}
	cad, err := ParseCadence(h.Cadence)
	if err != nil {
		return false
	}
	if cad.Flexible() {
		return doneThisWeek < cad.times
	}
	return cad.ExpectedOn(today.In(loc).Weekday())
}
