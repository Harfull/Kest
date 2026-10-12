package scheduler

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// Rule computes when a job runs next.
type Rule interface {
	// Next returns the first run strictly after the given instant, evaluating
	// wall-clock rules in loc.
	Next(after time.Time, loc *time.Location) (time.Time, error)
}

// ParseRule parses a recurrence rule:
//
//	daily@07:30
//	weekdays@06:45
//	weekly:mon,wed,fri@18:00
//	every:90m
//	cron:30 7 * * 1-5
//
// Wall-clock rules are evaluated in the job's time zone; "every" is elapsed
// time and ignores zones.
func ParseRule(s string) (Rule, error) {
	s = strings.TrimSpace(s)
	switch {
	case strings.HasPrefix(s, "daily@"):
		h, m, err := parseClock(strings.TrimPrefix(s, "daily@"))
		if err != nil {
			return nil, err
		}
		return dayRule{days: allDays, hour: h, min: m}, nil

	case strings.HasPrefix(s, "weekdays@"):
		h, m, err := parseClock(strings.TrimPrefix(s, "weekdays@"))
		if err != nil {
			return nil, err
		}
		return dayRule{days: weekdayMask, hour: h, min: m}, nil

	case strings.HasPrefix(s, "weekly:"):
		spec, clk, ok := strings.Cut(strings.TrimPrefix(s, "weekly:"), "@")
		if !ok {
			return nil, fmt.Errorf("rule %q: want weekly:mon,wed@HH:MM", s)
		}
		days, err := parseDays(spec)
		if err != nil {
			return nil, fmt.Errorf("rule %q: %w", s, err)
		}
		h, m, err := parseClock(clk)
		if err != nil {
			return nil, fmt.Errorf("rule %q: %w", s, err)
		}
		return dayRule{days: days, hour: h, min: m}, nil

	case strings.HasPrefix(s, "every:"):
		d, err := time.ParseDuration(strings.TrimPrefix(s, "every:"))
		if err != nil {
			return nil, fmt.Errorf("rule %q: %w", s, err)
		}
		if d < time.Minute {
			return nil, fmt.Errorf("rule %q: interval must be at least 1m", s)
		}
		return everyRule{d: d}, nil

	case strings.HasPrefix(s, "cron:"):
		return parseCron(strings.TrimPrefix(s, "cron:"))
	}
	return nil, fmt.Errorf("unknown rule %q (want daily@, weekdays@, weekly:, every: or cron:)", s)
}

// Day masks are indexed by time.Weekday (Sunday = bit 0).
const (
	allDays     = 0b1111111
	weekdayMask = 0b0111110 // Monday to Friday
)

var dayNames = map[string]time.Weekday{
	"sun": time.Sunday, "mon": time.Monday, "tue": time.Tuesday, "wed": time.Wednesday,
	"thu": time.Thursday, "fri": time.Friday, "sat": time.Saturday,
}

func parseDays(spec string) (uint, error) {
	var mask uint
	for _, p := range strings.Split(spec, ",") {
		d, ok := dayNames[strings.ToLower(strings.TrimSpace(p))]
		if !ok {
			return 0, fmt.Errorf("unknown day %q (use sun, mon, tue, wed, thu, fri, sat)", p)
		}
		mask |= 1 << uint(d)
	}
	if mask == 0 {
		return 0, errors.New("no days given")
	}
	return mask, nil
}

func parseClock(s string) (hour, minute int, err error) {
	t, err := time.Parse("15:04", strings.TrimSpace(s))
	if err != nil {
		return 0, 0, fmt.Errorf("time %q must be HH:MM in 24-hour time", s)
	}
	return t.Hour(), t.Minute(), nil
}

// dayRule fires at a fixed wall-clock time on selected weekdays.
type dayRule struct {
	days      uint
	hour, min int
}

func (r dayRule) Next(after time.Time, loc *time.Location) (time.Time, error) {
	local := after.In(loc)
	// A week plus slack always contains a matching day; the extra days cover
	// the case where today's slot has passed.
	for i := 0; i < 9; i++ {
		day := local.AddDate(0, 0, i)
		if r.days&(1<<uint(day.Weekday())) == 0 {
			continue
		}
		t := wallClock(day.Year(), day.Month(), day.Day(), r.hour, r.min, loc)
		if t.After(after) {
			return t, nil
		}
	}
	return time.Time{}, errors.New("no matching day found")
}

// everyRule fires at a fixed elapsed interval.
type everyRule struct{ d time.Duration }

func (r everyRule) Next(after time.Time, _ *time.Location) (time.Time, error) {
	return after.Add(r.d), nil
}
