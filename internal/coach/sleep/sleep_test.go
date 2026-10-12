package sleep

import (
	"testing"
	"time"

	"github.com/Harfull/Kest/internal/store"
)

var utc = time.UTC

// wakeDay is Sunday 11 October 2026; the night in question is Saturday night.
var wakeDay = time.Date(2026, 10, 11, 0, 0, 0, 0, utc)

func tm(day, h, m int) time.Time { return time.Date(2026, 10, day, h, m, 0, 0, utc) }

func act(ts time.Time) store.SleepEvent  { return store.SleepEvent{TS: ts, Kind: store.SleepActivity} }
func bed(ts time.Time) store.SleepEvent  { return store.SleepEvent{TS: ts, Kind: store.SleepBedtime} }
func woke(ts time.Time) store.SleepEvent { return store.SleepEvent{TS: ts, Kind: store.SleepWake} }

func TestExplicitNight(t *testing.T) {
	evs := []store.SleepEvent{act(tm(10, 20, 0)), bed(tm(10, 23, 10)), woke(tm(11, 7, 5)), act(tm(11, 7, 6))}

	n, ok := EstimateNight(evs, wakeDay, utc, DefaultParams())
	if !ok || n.Confidence != store.ConfExplicit {
		t.Fatalf("night = %+v, %v", n, ok)
	}
	if want := tm(10, 23, 25); !n.Onset.Equal(want) { // bedtime + 15 minute latency
		t.Errorf("onset = %v, want %v", n.Onset, want)
	}
	if !n.Wake.Equal(tm(11, 7, 5)) || n.Day != "2026-10-11" {
		t.Errorf("night = %+v", n)
	}
	if d := Duration(n); d != 7*time.Hour+40*time.Minute {
		t.Errorf("duration = %v", d)
	}
}

func TestInferredNightUsesTheLongestOvernightGap(t *testing.T) {
	evs := []store.SleepEvent{
		act(tm(10, 14, 0)), act(tm(10, 17, 30)), // a long afternoon gap that must be ignored
		act(tm(10, 22, 40)), // last message of the evening
		act(tm(11, 7, 50)),  // first message of the morning
		act(tm(11, 8, 5)),
	}
	n, ok := EstimateNight(evs, wakeDay, utc, DefaultParams())
	if !ok || n.Confidence != store.ConfInferred {
		t.Fatalf("night = %+v, %v", n, ok)
	}
	if !n.Onset.Equal(tm(10, 23, 0)) || !n.Wake.Equal(tm(11, 7, 30)) {
		t.Errorf("night = %v - %v; want 23:00 - 07:30 (gap edges corrected by 20 minutes)", n.Onset, n.Wake)
	}
}

func TestExplicitEdgesOverrideAnInferredGap(t *testing.T) {
	base := []store.SleepEvent{act(tm(10, 22, 40)), act(tm(11, 7, 50))}

	withBed := append([]store.SleepEvent{bed(tm(10, 23, 30))}, base...)
	n, ok := EstimateNight(withBed, wakeDay, utc, DefaultParams())
	if !ok || n.Confidence != store.ConfInferred || !n.Onset.Equal(tm(10, 23, 45)) || !n.Wake.Equal(tm(11, 7, 30)) {
		t.Errorf("explicit bedtime + inferred wake = %+v, %v", n, ok)
	}

	withWake := append([]store.SleepEvent{woke(tm(11, 6, 55))}, base...)
	n, ok = EstimateNight(withWake, wakeDay, utc, DefaultParams())
	if !ok || !n.Onset.Equal(tm(10, 23, 0)) || !n.Wake.Equal(tm(11, 6, 55)) {
		t.Errorf("inferred onset + explicit wake = %+v, %v", n, ok)
	}
}

func TestNoNightWhenEvidenceIsMissingOrImplausible(t *testing.T) {
	tests := []struct {
		name string
		evs  []store.SleepEvent
	}{
		{"no events", nil},
		{"still asleep: no message since the gap began", []store.SleepEvent{act(tm(10, 22, 40))}},
		{"gap shorter than three hours", []store.SleepEvent{act(tm(11, 1, 0)), act(tm(11, 3, 30))}},
		{"daytime nap is not a night", []store.SleepEvent{act(tm(10, 13, 0)), act(tm(10, 18, 30))}},
		{"bedtime without a wake or a gap", []store.SleepEvent{bed(tm(10, 23, 0))}},
		{"wake before bedtime does not pair", []store.SleepEvent{woke(tm(10, 16, 0)), bed(tm(10, 23, 0))}},
		{"explicit night under an hour", []store.SleepEvent{bed(tm(11, 6, 0)), woke(tm(11, 6, 30))}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if n, ok := EstimateNight(tt.evs, wakeDay, utc, DefaultParams()); ok {
				t.Errorf("got a night: %+v", n)
			}
		})
	}
}

func TestLongestGapWins(t *testing.T) {
	evs := []store.SleepEvent{
		act(tm(10, 21, 0)), act(tm(11, 0, 30)), // 3.5h gap, then awake for a bit
		act(tm(11, 0, 45)), act(tm(11, 8, 15)), // 7.5h gap: the real night
	}
	n, ok := EstimateNight(evs, wakeDay, utc, DefaultParams())
	if !ok || !n.Onset.Equal(tm(11, 1, 5)) || !n.Wake.Equal(tm(11, 7, 55)) {
		t.Errorf("night = %+v, %v", n, ok)
	}
}

func TestNightAcrossDaylightSavingEnd(t *testing.T) {
	ny, _ := time.LoadLocation("America/New_York")
	day := time.Date(2026, 11, 1, 0, 0, 0, 0, ny) // clocks fall back at 02:00
	evs := []store.SleepEvent{
		bed(time.Date(2026, 10, 31, 23, 0, 0, 0, ny)),
		woke(time.Date(2026, 11, 1, 7, 0, 0, 0, ny)),
	}
	n, ok := EstimateNight(evs, day, ny, Params{})
	if !ok {
		t.Fatal("no night found")
	}
	if d := Duration(n); d != 9*time.Hour { // eight clock hours plus the repeated hour
		t.Errorf("duration = %v, want 9h of real time", d)
	}
}

func TestLearnMovesTowardTheTruthAndIsClamped(t *testing.T) {
	est := store.SleepNight{Onset: tm(10, 23, 0), Wake: tm(11, 7, 30)}
	truth := store.SleepNight{Onset: tm(10, 23, 40), Wake: tm(11, 7, 0)} // slept later, woke earlier

	p := DefaultParams().Learn(est, truth, 0.5)
	if p.OnsetOffset != 40*time.Minute { // 20m + 0.5*40m
		t.Errorf("OnsetOffset = %v", p.OnsetOffset)
	}
	if p.WakeOffset != 35*time.Minute { // 20m + 0.5*30m
		t.Errorf("WakeOffset = %v", p.WakeOffset)
	}

	wild := store.SleepNight{Onset: tm(10, 12, 0), Wake: tm(11, 22, 0)}
	p = DefaultParams().Learn(est, wild, 1)
	if p.OnsetOffset != 0 || p.WakeOffset != 0 {
		t.Errorf("offsets %v / %v; negative corrections must clamp to zero", p.OnsetOffset, p.WakeOffset)
	}
	far := store.SleepNight{Onset: tm(11, 20, 0), Wake: tm(11, 1, 0)}
	p = DefaultParams().Learn(est, far, 1)
	if p.OnsetOffset != maxOffset || p.WakeOffset != maxOffset {
		t.Errorf("offsets %v / %v; large corrections must clamp to %v", p.OnsetOffset, p.WakeOffset, maxOffset)
	}
}

func night(onsetH, onsetM, wakeH, wakeM, day int) store.SleepNight {
	onsetDay := day
	if onsetH >= 12 {
		onsetDay = day - 1
	}
	return store.SleepNight{Onset: tm(onsetDay, onsetH, onsetM), Wake: tm(day, wakeH, wakeM), Confidence: store.ConfInferred}
}

func TestTypicalHandlesMidnightWraparound(t *testing.T) {
	nights := []store.SleepNight{
		night(23, 50, 7, 0, 10), night(0, 10, 7, 5, 11), night(23, 40, 6, 55, 9), night(0, 20, 7, 10, 8), night(23, 55, 7, 0, 7),
	}
	onset, wake, ok := Typical(nights, utc)
	if !ok {
		t.Fatal("not enough nights")
	}
	// A naive average of these would land near noon; the circular median stays near midnight.
	if circDist(onset, 0) > 25 {
		t.Errorf("onset = %02d:%02d, want within 25 minutes of midnight", onset/60, onset%60)
	}
	if circDist(wake, 7*60) > 10 {
		t.Errorf("wake = %02d:%02d", wake/60, wake%60)
	}

	if _, _, ok := Typical(nights[:2], utc); ok {
		t.Error("two nights were enough")
	}
	prior := []store.SleepNight{{Confidence: store.ConfPrior}, {Confidence: store.ConfPrior}, {Confidence: store.ConfPrior}}
	if _, _, ok := Typical(prior, utc); ok {
		t.Error("prior nights counted as observations")
	}
}

func TestModelUsesThePriorThenLearns(t *testing.T) {
	prior := Prior{QuietStart: 23 * 60, QuietEnd: 7 * 60}

	m := NewModel(prior, nil, utc)
	if m.Learned() || m.Onset() != 23*60+30 || m.Wake() != 7*60 {
		t.Errorf("prior model = learned %v, onset %d, wake %d", m.Learned(), m.Onset(), m.Wake())
	}
	// Quiet hours are the declared ones.
	for h, want := range map[int]bool{22: false, 23: true, 3: true, 6: true, 7: false, 12: false} {
		if got := m.InQuiet(tm(12, h, 0)); got != want {
			t.Errorf("prior InQuiet(%02d:00) = %v, want %v", h, got, want)
		}
	}

	// An owner who actually sleeps 01:00 to 09:00.
	late := []store.SleepNight{night(1, 0, 9, 0, 10), night(1, 10, 9, 5, 9), night(0, 50, 8, 55, 8), night(1, 5, 9, 0, 7)}
	m = NewModel(prior, late, utc)
	if !m.Learned() || m.Nights() != 4 {
		t.Fatalf("learned = %v, nights = %d", m.Learned(), m.Nights())
	}
	if m.InQuiet(tm(12, 8, 0)) != true || m.InQuiet(tm(12, 10, 0)) != false || m.InQuiet(tm(12, 0, 45)) != true {
		t.Error("learned quiet hours do not follow the observed schedule")
	}
	if m.InQuiet(tm(12, 23, 0)) != false {
		t.Error("23:00 should not be quiet for someone who sleeps at 01:00")
	}
}

func TestQuietWindowThatDoesNotCrossMidnight(t *testing.T) {
	// A day sleeper: asleep 08:00 to 15:00.
	day := []store.SleepNight{
		{Onset: tm(10, 8, 0), Wake: tm(10, 15, 0), Confidence: store.ConfInferred},
		{Onset: tm(11, 8, 10), Wake: tm(11, 15, 5), Confidence: store.ConfInferred},
		{Onset: tm(9, 7, 55), Wake: tm(9, 14, 55), Confidence: store.ConfInferred},
	}
	m := NewModel(Prior{QuietStart: 23 * 60, QuietEnd: 7 * 60}, day, utc)
	if !m.InQuiet(tm(12, 10, 0)) || m.InQuiet(tm(12, 20, 0)) || m.InQuiet(tm(12, 3, 0)) {
		t.Error("daytime sleeper's quiet window is wrong")
	}
}

func TestCheckInTimes(t *testing.T) {
	m := NewModel(Prior{QuietStart: 23 * 60, QuietEnd: 7 * 60}, nil, utc)
	day := tm(12, 0, 0)

	if got := m.MorningAt(day); got.Hour() != 7 || got.Minute() != 15 {
		t.Errorf("MorningAt = %v, want 07:15", got)
	}
	if got := m.EveningAt(day); got.Hour() != 21 || got.Minute() != 0 { // onset 23:30 - 2.5h
		t.Errorf("EveningAt = %v, want 21:00", got)
	}
	if got := m.WindDownAt(day); got.Hour() != 22 || got.Minute() != 15 { // onset 23:30 - 75m
		t.Errorf("WindDownAt = %v, want 22:15", got)
	}

	// An early riser's morning check-in is held to 05:30; a late sleeper's to 11:00.
	early := NewModel(Prior{QuietStart: 21 * 60, QuietEnd: 4 * 60}, nil, utc)
	if got := early.MorningAt(day); got.Hour() != 5 || got.Minute() != 30 {
		t.Errorf("early MorningAt = %v", got)
	}
	late := NewModel(Prior{QuietStart: 4 * 60, QuietEnd: 13 * 60}, nil, utc)
	if got := late.MorningAt(day); got.Hour() != 11 {
		t.Errorf("late MorningAt = %v", got)
	}
	// Someone who sleeps at 02:00 still gets an evening slot on the evening side of the day.
	owl := NewModel(Prior{QuietStart: 90, QuietEnd: 9 * 60}, nil, utc)
	if got := owl.EveningAt(day); got.Hour() < 19 || got.Hour() > 22 {
		t.Errorf("owl EveningAt = %v, want 19:00-22:30", got)
	}
}

func TestSummarize(t *testing.T) {
	var nights []store.SleepNight
	for i := range 9 { // nine nights of 7h, the earliest two outside the debt window
		d := 20 - i
		n := night(23, 30, 6, 30, d)
		n.Day = time.Date(2026, 10, d, 0, 0, 0, 0, utc).Format("2006-01-02")
		nights = append(nights, n)
	}
	s := Summarize(nights, 8*time.Hour, utc)

	if s.Nights != 9 || s.AvgDuration != 7*time.Hour {
		t.Errorf("summary = %+v", s)
	}
	if s.Debt != 7*time.Hour { // last seven nights, one hour short each
		t.Errorf("Debt = %v, want 7h", s.Debt)
	}
	if s.Regularity != 0 {
		t.Errorf("Regularity = %v, want 0 for identical nights", s.Regularity)
	}
	if !s.LowConfidence {
		t.Error("all-inferred nights should be flagged low confidence")
	}

	nights[0].Confidence, nights[1].Confidence, nights[2].Confidence = store.ConfExplicit, store.ConfConfirmed, store.ConfExplicit
	nights[3].Confidence, nights[4].Confidence = store.ConfConfirmed, store.ConfExplicit
	if Summarize(nights, 8*time.Hour, utc).LowConfidence {
		t.Error("mostly stated nights flagged low confidence")
	}
	if got := Summarize(nil, 8*time.Hour, utc); got.Nights != 0 || got.AvgDuration != 0 {
		t.Errorf("empty summary = %+v", got)
	}
}

func TestDetectSignal(t *testing.T) {
	bedtimes := []string{"good night", "Goodnight!", "gn... actually good night", "heading to bed", "going to sleep now", "off to bed, talk tomorrow", "Night night."}
	wakes := []string{"morning", "Good morning!", "I'm up", "im up", "just woke up", "woke up early today", "i am up now"}
	neither := []string{
		"I should go to bed earlier, any tips?",
		"what time should I go to bed?",
		"I'm going to bed earlier this week so I can train in the morning, what do you think",
		"morning workout was great, thanks for the nudge",
		"remind me to say good night to mum",
		"", "   ", "hello",
	}
	for _, s := range bedtimes {
		if k, ok := DetectSignal(s); !ok || k != SignalBedtime {
			t.Errorf("DetectSignal(%q) = %q, %v; want bedtime", s, k, ok)
		}
	}
	for _, s := range wakes {
		if k, ok := DetectSignal(s); !ok || k != SignalWake {
			t.Errorf("DetectSignal(%q) = %q, %v; want wake", s, k, ok)
		}
	}
	for _, s := range neither {
		if k, ok := DetectSignal(s); ok {
			t.Errorf("DetectSignal(%q) = %q; not a sleep signal", s, k)
		}
	}
}
