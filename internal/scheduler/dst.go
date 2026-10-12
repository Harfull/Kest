package scheduler

import (
	"time"
)

// wallClock returns the instant at which the wall clock in loc reads
// y-mo-d h:mi, following the scheduler's daylight saving rules
// (docs/architecture/scheduler.md):
//
//   - A time that falls in a skipped hour (spring forward) resolves to the
//     first valid instant after the gap.
//   - A time that occurs twice (fall back) resolves to its first occurrence.
//
// The standard library leaves both cases unspecified, so the two possible
// offsets around the date are tried explicitly and each candidate is verified.
func wallClock(y int, mo time.Month, d, h, mi int, loc *time.Location) time.Time {
	// Local wall time expressed as if it were UTC; subtracting a zone offset
	// turns it into a real instant.
	lu := time.Date(y, mo, d, h, mi, 0, 0, time.UTC)

	// Sample offsets well before and after the date. 48 hours clears the
	// maximum offset (about 14h) plus a full day, so a transition on this date
	// always falls between the samples.
	offBefore := offset(lu.Add(-48*time.Hour), loc)
	offAfter := offset(lu.Add(48*time.Hour), loc)

	var best time.Time
	found := false
	for _, off := range []int{offBefore, offAfter} {
		c := lu.Add(-time.Duration(off) * time.Second)
		l := c.In(loc)
		if l.Year() == y && l.Month() == mo && l.Day() == d && l.Hour() == h && l.Minute() == mi {
			if !found || c.Before(best) {
				best, found = c, true
			}
		}
	}
	if found {
		return best
	}

	// The wall time does not exist: it fell in a gap. Find the transition.
	// With the pre-transition offset the instant lies after the gap, so the
	// transition is somewhere before it.
	// Search in whole seconds so the result is exactly the transition instant.
	hi := lu.Unix() - int64(offBefore)
	lo := hi - int64(6*time.Hour/time.Second)
	for hi-lo > 1 {
		mid := lo + (hi-lo)/2
		if offset(time.Unix(mid, 0), loc) == offBefore {
			lo = mid
		} else {
			hi = mid
		}
	}
	return time.Unix(hi, 0).UTC()
}

// offset returns loc's UTC offset in seconds at instant t.
func offset(t time.Time, loc *time.Location) int {
	_, off := t.In(loc).Zone()
	return off
}
