package scheduler

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// cronRule is a standard 5-field cron expression evaluated in the job's zone:
//
//	minute hour day-of-month month day-of-week
//
// Fields accept *, numbers, lists (1,3), ranges (1-5) and steps (*/15, 1-10/2).
// Day-of-week is 0-6 with 0 = Sunday (7 is also accepted as Sunday). When both
// day-of-month and day-of-week are restricted, either may match, as in Vixie cron.
type cronRule struct {
	minute, hour, dom, month, dow uint64
	domStar, dowStar              bool
}

type cronBounds struct{ lo, hi int }

var cronFieldBounds = [5]cronBounds{{0, 59}, {0, 23}, {1, 31}, {1, 12}, {0, 7}}

func parseCron(expr string) (Rule, error) {
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return nil, fmt.Errorf("cron %q: want 5 fields (minute hour day-of-month month day-of-week)", expr)
	}

	var sets [5]uint64
	for i, f := range fields {
		set, err := parseCronField(f, cronFieldBounds[i])
		if err != nil {
			return nil, fmt.Errorf("cron %q: field %d: %w", expr, i+1, err)
		}
		sets[i] = set
	}

	// Fold 7 into 0 so both spell Sunday.
	if sets[4]&(1<<7) != 0 {
		sets[4] |= 1
		sets[4] &^= 1 << 7
	}

	return cronRule{
		minute: sets[0], hour: sets[1], dom: sets[2], month: sets[3], dow: sets[4],
		domStar: fields[2] == "*", dowStar: fields[4] == "*",
	}, nil
}

func parseCronField(f string, b cronBounds) (uint64, error) {
	var set uint64
	for _, part := range strings.Split(f, ",") {
		rangePart, stepPart, hasStep := strings.Cut(part, "/")
		step := 1
		if hasStep {
			n, err := strconv.Atoi(stepPart)
			if err != nil || n < 1 {
				return 0, fmt.Errorf("bad step %q", stepPart)
			}
			step = n
		}

		lo, hi := b.lo, b.hi
		switch {
		case rangePart == "*":
			if b.hi == 7 {
				hi = 6 // "*" in day-of-week means 0-6
			}
		case strings.Contains(rangePart, "-"):
			a, z, _ := strings.Cut(rangePart, "-")
			var err1, err2 error
			lo, err1 = strconv.Atoi(a)
			hi, err2 = strconv.Atoi(z)
			if err := errors.Join(err1, err2); err != nil {
				return 0, fmt.Errorf("bad range %q", rangePart)
			}
		default:
			n, err := strconv.Atoi(rangePart)
			if err != nil {
				return 0, fmt.Errorf("bad value %q", rangePart)
			}
			lo, hi = n, n
			if hasStep {
				hi = b.hi // "5/10" means 5 to max stepping by 10
			}
		}

		if lo < b.lo || hi > b.hi || lo > hi {
			return 0, fmt.Errorf("%q out of range %d-%d", part, b.lo, b.hi)
		}
		for v := lo; v <= hi; v += step {
			set |= 1 << uint(v)
		}
	}
	return set, nil
}

func (r cronRule) dayMatches(t time.Time) bool {
	if r.month&(1<<uint(t.Month())) == 0 {
		return false
	}
	domOK := r.dom&(1<<uint(t.Day())) != 0
	dowOK := r.dow&(1<<uint(t.Weekday())) != 0
	switch {
	case r.domStar && r.dowStar:
		return true
	case r.domStar:
		return dowOK
	case r.dowStar:
		return domOK
	default:
		return domOK || dowOK
	}
}

// Next scans forward day by day, trying each matching hour and minute through
// wallClock so daylight saving rules apply. Four years covers leap-day rules.
func (r cronRule) Next(after time.Time, loc *time.Location) (time.Time, error) {
	local := after.In(loc)
	for i := 0; i < 366*4; i++ {
		day := local.AddDate(0, 0, i)
		if !r.dayMatches(day) {
			continue
		}
		var best time.Time
		for h := 0; h < 24; h++ {
			if r.hour&(1<<uint(h)) == 0 {
				continue
			}
			for m := 0; m < 60; m++ {
				if r.minute&(1<<uint(m)) == 0 {
					continue
				}
				t := wallClock(day.Year(), day.Month(), day.Day(), h, m, loc)
				if t.After(after) && (best.IsZero() || t.Before(best)) {
					best = t
				}
			}
		}
		if !best.IsZero() {
			return best, nil
		}
	}
	return time.Time{}, errors.New("cron expression never matches")
}
