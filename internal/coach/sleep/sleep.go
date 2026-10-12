// Package sleep estimates when the owner sleeps without a wearable, and turns
// that into quiet hours and check-in times.
//
// Signals, strongest first: explicit messages ("heading to bed", "I'm up"),
// confirmations of an estimate, and the longest gap in chat activity at night.
// Phone use leaks into sleep time, so a raw gap is biased; learned offsets
// correct it from the owner's confirmations. Every estimate carries a
// confidence and nothing here claims precision it does not have. See
// docs/research/sleep/README.md.
//
// The functions are pure: events and nights go in, estimates come out.
package sleep

import (
	"math"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/Harfull/Kest/internal/store"
)

const (
	dayLayout = "2006-01-02"
	// MinGap is the shortest silence that can count as a night's sleep.
	MinGap   = 3 * time.Hour
	minSleep = time.Hour
)

// Params are the corrections applied to a raw chat gap. They start at the
// defaults and are learned from the owner's confirmations.
type Params struct {
	// Latency is the time from saying "heading to bed" to falling asleep.
	Latency time.Duration
	// OnsetOffset is added to the last message before the gap.
	OnsetOffset time.Duration
	// WakeOffset is subtracted from the first message after the gap.
	WakeOffset time.Duration
}

// DefaultParams returns the starting corrections.
func DefaultParams() Params {
	return Params{Latency: 15 * time.Minute, OnsetOffset: 20 * time.Minute, WakeOffset: 20 * time.Minute}
}

// maxOffset bounds learned corrections so one odd night cannot run away.
const maxOffset = 3 * time.Hour

func clampOffset(d time.Duration) time.Duration {
	return min(max(d, 0), maxOffset)
}

// Learn moves the corrections toward what the owner said was true. estimate is
// what the model produced with p; truth is what the owner confirmed or stated.
// alpha is the learning rate (0.3 is gentle).
func (p Params) Learn(estimate, truth store.SleepNight, alpha float64) Params {
	onsetErr := truth.Onset.Sub(estimate.Onset) // positive: fell asleep later than estimated
	wakeErr := estimate.Wake.Sub(truth.Wake)    // positive: woke earlier than estimated
	p.OnsetOffset = clampOffset(p.OnsetOffset + time.Duration(alpha*float64(onsetErr)))
	p.WakeOffset = clampOffset(p.WakeOffset + time.Duration(alpha*float64(wakeErr)))
	return p
}

func at(day time.Time, loc *time.Location, h, m int) time.Time {
	d := day.In(loc)
	return time.Date(d.Year(), d.Month(), d.Day(), h, m, 0, 0, loc)
}

// EstimateNight works out the night that ended on the local date of wakeDay,
// from events around it. It reports false when there is not enough to say.
//
// The sleep day runs from 15:00 the day before to 15:00 on wakeDay. An explicit
// "bedtime" followed by a "wake" gives an explicit night. Otherwise the longest
// silence in chat activity that begins in the evening and ends in the morning is
// used, with an explicit bedtime or wake overriding its edge. An open silence
// (no message since) is never treated as a night.
func EstimateNight(events []store.SleepEvent, wakeDay time.Time, loc *time.Location, p Params) (store.SleepNight, bool) {
	prevEve := at(wakeDay.AddDate(0, 0, -1), loc, 15, 0)
	end := at(wakeDay, loc, 15, 0)
	night := store.SleepNight{Day: wakeDay.In(loc).Format(dayLayout)}

	// Explicit signals.
	var bed, wake *time.Time
	for i := range events {
		e := events[i]
		if e.Kind == store.SleepBedtime && !e.TS.Before(prevEve) && e.TS.Before(at(wakeDay, loc, 6, 0)) {
			bed = &events[i].TS // the last bedtime wins
		}
	}
	for i := range events {
		e := events[i]
		if e.Kind != store.SleepWake || !e.TS.Before(end) {
			continue
		}
		if bed != nil && e.TS.After(*bed) {
			wake = &events[i].TS
			break
		}
		if bed == nil && !e.TS.Before(at(wakeDay, loc, 3, 0)) {
			wake = &events[i].TS
			break
		}
	}
	if bed != nil && wake != nil && wake.Sub(*bed) >= minSleep {
		night.Onset, night.Wake, night.Confidence = bed.Add(p.Latency), *wake, store.ConfExplicit
		return night, true
	}

	// Chat-gap inference.
	var acts []time.Time
	for _, e := range events {
		if (e.Kind == store.SleepActivity || e.Kind == store.SleepImport) && !e.TS.Before(prevEve) && e.TS.Before(at(wakeDay, loc, 23, 59)) {
			acts = append(acts, e.TS)
		}
	}
	sort.Slice(acts, func(i, j int) bool { return acts[i].Before(acts[j]) })

	var bestStart, bestEnd time.Time
	for i := 0; i+1 < len(acts); i++ {
		gs, ge := acts[i], acts[i+1]
		if ge.Sub(gs) < MinGap {
			continue
		}
		// The silence must begin in the evening or small hours and end in the morning.
		if gs.Before(at(wakeDay.AddDate(0, 0, -1), loc, 19, 0)) || gs.After(at(wakeDay, loc, 6, 0)) {
			continue
		}
		if ge.Before(at(wakeDay, loc, 3, 0)) || ge.After(end) {
			continue
		}
		if bestStart.IsZero() || ge.Sub(gs) > bestEnd.Sub(bestStart) {
			bestStart, bestEnd = gs, ge
		}
	}
	if bestStart.IsZero() {
		return store.SleepNight{}, false
	}

	night.Onset, night.Wake, night.Confidence = bestStart.Add(p.OnsetOffset), bestEnd.Add(-p.WakeOffset), store.ConfInferred
	if bed != nil {
		night.Onset = bed.Add(p.Latency)
	}
	if wake != nil {
		night.Wake = *wake
	}
	if night.Wake.Sub(night.Onset) < minSleep {
		return store.SleepNight{}, false
	}
	return night, true
}

// Duration returns how long the night was.
func Duration(n store.SleepNight) time.Duration { return n.Wake.Sub(n.Onset) }

// --- typical times ---------------------------------------------------------------

// minuteOfDay returns minutes since local midnight.
func minuteOfDay(t time.Time, loc *time.Location) int {
	l := t.In(loc)
	return l.Hour()*60 + l.Minute()
}

// circDist is the distance between two minutes-of-day around the clock.
func circDist(a, b int) int {
	d := a - b
	if d < 0 {
		d = -d
	}
	return min(d, 1440-d)
}

// circMedian returns the sample that minimizes the total circular distance to
// all samples: a median that handles 23:50 and 00:10 being close together.
func circMedian(xs []int) int {
	best, bestSum := xs[0], math.MaxInt
	for _, c := range xs {
		sum := 0
		for _, x := range xs {
			sum += circDist(c, x)
		}
		if sum < bestSum {
			best, bestSum = c, sum
		}
	}
	return best
}

// minNights is how many nights are needed before habits override the prior.
const minNights = 3

// Typical returns the usual onset and wake, as minutes since local midnight,
// from nights (use the most recent 28). ok is false with fewer than three.
func Typical(nights []store.SleepNight, loc *time.Location) (onset, wake int, ok bool) {
	var os, ws []int
	for _, n := range nights {
		if n.Confidence == store.ConfPrior {
			continue
		}
		os = append(os, minuteOfDay(n.Onset, loc))
		ws = append(ws, minuteOfDay(n.Wake, loc))
	}
	if len(os) < minNights {
		return 0, 0, false
	}
	return circMedian(os), circMedian(ws), true
}

// Prior is the declared schedule used until enough nights are known, as
// minutes since midnight.
type Prior struct{ QuietStart, QuietEnd int }

// Model answers schedule questions from the usual sleep window.
type Model struct {
	loc       *time.Location
	onset     int
	wake      int
	learned   bool
	nightsFor int
}

// NewModel builds a Model from recent nights, falling back to the prior. The
// prior's quiet hours begin 30 minutes before the owner is assumed asleep.
func NewModel(prior Prior, nights []store.SleepNight, loc *time.Location) Model {
	m := Model{loc: loc, onset: (prior.QuietStart + 30) % 1440, wake: prior.QuietEnd}
	for _, n := range nights {
		if n.Confidence != store.ConfPrior {
			m.nightsFor++
		}
	}
	if o, w, ok := Typical(nights, loc); ok {
		m.onset, m.wake, m.learned = o, w, true
	}
	return m
}

// Learned reports whether the window comes from observed nights.
func (m Model) Learned() bool { return m.learned }

// Nights returns how many observed nights the model has seen.
func (m Model) Nights() int { return m.nightsFor }

// Onset and Wake return the usual times as minutes since local midnight.
func (m Model) Onset() int { return m.onset }

// Wake returns the usual wake time as minutes since local midnight.
func (m Model) Wake() int { return m.wake }

// quietBefore is how long before the usual sleep time quiet hours begin.
const quietBefore = 30

// InQuiet reports whether t falls in quiet hours: from half an hour before the
// usual sleep time until the usual wake time.
func (m Model) InQuiet(t time.Time) bool {
	start := (m.onset - quietBefore + 1440) % 1440
	end := m.wake
	mod := minuteOfDay(t, m.loc)
	if start <= end {
		return mod >= start && mod < end
	}
	return mod >= start || mod < end
}

func (m Model) clampMinutes(v, lo, hi int) int { return min(max(v, lo), hi) }

// MorningAt is when the morning check-in is due on a local day: 15 minutes
// after the usual wake time, never before 05:30 or after 11:00.
func (m Model) MorningAt(day time.Time) time.Time {
	v := m.clampMinutes(m.wake+15, 5*60+30, 11*60)
	return at(day, m.loc, v/60, v%60)
}

// EveningAt is when the evening log-in is due: two and a half hours before the
// usual sleep time, between 19:00 and 22:30.
func (m Model) EveningAt(day time.Time) time.Time {
	v := (m.onset - 150 + 1440) % 1440
	if v < 12*60 { // sleep time after midnight: the evening is the day before's tail
		v += 1440
	}
	v = m.clampMinutes(v, 19*60, 22*60+30)
	return at(day, m.loc, v/60, v%60)
}

// WindDownAt is when the optional wind-down message is due: 75 minutes before
// the usual sleep time.
func (m Model) WindDownAt(day time.Time) time.Time {
	v := (m.onset - 75 + 1440) % 1440
	if v < 12*60 {
		v += 1440
	}
	v = m.clampMinutes(v, 19*60, 23*60+30)
	return at(day, m.loc, v/60, v%60)
}

// --- reports -----------------------------------------------------------------------

// Summary describes recent sleep in terms safe to say out loud.
type Summary struct {
	Nights      int
	AvgDuration time.Duration
	// Regularity is how much the middle of sleep moves from night to night.
	Regularity time.Duration
	// Debt is how far below the target the last seven nights were, in total.
	Debt time.Duration
	// LowConfidence is true when most nights are inferred rather than stated.
	LowConfidence bool
}

// Summarize looks at nights (newest first or any order) against a target length.
func Summarize(nights []store.SleepNight, target time.Duration, loc *time.Location) Summary {
	s := Summary{Nights: len(nights)}
	if len(nights) == 0 {
		return s
	}

	var total time.Duration
	var mids []int
	sure := 0
	for _, n := range nights {
		total += Duration(n)
		mid := n.Onset.Add(Duration(n) / 2)
		mids = append(mids, minuteOfDay(mid, loc))
		if n.Confidence == store.ConfExplicit || n.Confidence == store.ConfConfirmed {
			sure++
		}
	}
	s.AvgDuration = total / time.Duration(len(nights))
	s.LowConfidence = sure*2 < len(nights)

	center := circMedian(mids)
	var sum float64
	for _, m := range mids {
		d := float64(circDist(m, center))
		sum += d * d
	}
	s.Regularity = time.Duration(math.Sqrt(sum/float64(len(mids)))) * time.Minute

	recent := nights
	if len(recent) > 7 {
		sorted := append([]store.SleepNight(nil), nights...)
		sort.Slice(sorted, func(i, j int) bool { return sorted[i].Day > sorted[j].Day })
		recent = sorted[:7]
	}
	for _, n := range recent {
		if short := target - Duration(n); short > 0 {
			s.Debt += short
		}
	}
	return s
}

// --- explicit signals in chat --------------------------------------------------------

var (
	// Both patterns are anchored: a sleep word in the middle of a sentence ("remind
	// me to say good night to mum") is not the owner going to bed.
	bedtimeRe = regexp.MustCompile(`(?i)^((good ?night|night night|gn)\b|((i'?m|im|i am|ok,?|okay,?) )?(going to|heading to|off to) (bed|sleep)\b|turning in\b|hitting the hay\b)`)
	wakeRe    = regexp.MustCompile(`(?i)^((good morning|morning|gm)( kest| there)?|(i'?m|im|i am) (up|awake)( now| already)?|(just )?(woke|got) up( now| early| late| already)?( today)?|up now)$`)
)

// Signal kinds returned by DetectSignal.
const (
	SignalBedtime = store.SleepBedtime
	SignalWake    = store.SleepWake
)

// DetectSignal recognizes a plain statement that the owner is going to bed or
// has woken up. It is deliberately conservative: only short messages count, so
// "I should go to bed earlier, any tips?" is not a bedtime.
func DetectSignal(text string) (kind string, ok bool) {
	t := strings.TrimSpace(text)
	t = strings.Trim(t, " .!?,…")
	if t == "" {
		return "", false
	}
	n := len([]rune(t))
	switch {
	case n <= 40 && bedtimeRe.MatchString(t) && !strings.Contains(t, "?"):
		return SignalBedtime, true
	case n <= 30 && wakeRe.MatchString(t):
		return SignalWake, true
	}
	return "", false
}
