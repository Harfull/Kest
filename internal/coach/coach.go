// Package coach is Kest's coaching engine: it decides whether, when and how to
// speak to the owner about their habits and sleep.
//
// A scheduler job calls Tick every few minutes. Tick works out which check-in
// is due (morning, habit prompts, evening, weekly, wind-down), asks the
// notification budget whether it may speak, and sends at most one message.
// Because everything is derived from stored history and the sleep model, a
// restart or a changed schedule never duplicates or loses a check-in.
//
// Design: docs/architecture/coach-engine.md. Evidence:
// docs/research/behavior-science/README.md and docs/research/sleep/README.md.
package coach

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/Harfull/Kest/internal/clock"
	"github.com/Harfull/Kest/internal/coach/budget"
	"github.com/Harfull/Kest/internal/coach/habit"
	"github.com/Harfull/Kest/internal/coach/messages"
	"github.com/Harfull/Kest/internal/coach/sleep"
	"github.com/Harfull/Kest/internal/store"
)

// Button is an inline button on an outgoing message.
type Button struct{ Label, Data string }

// Message is something the coach wants to say.
type Message struct {
	Text    string
	Buttons [][]Button
}

// Outbox delivers coach messages to the owner. The app connects it to Telegram.
type Outbox interface {
	Send(ctx context.Context, m Message) error
}

// Store is the persistence the coach needs. *store.DB satisfies it.
type Store interface {
	AddHabit(ctx context.Context, h store.Habit) error
	UpdateHabit(ctx context.Context, h store.Habit) error
	GetHabit(ctx context.Context, id string) (store.Habit, error)
	ListHabits(ctx context.Context, includeRetired bool) ([]store.Habit, error)
	SetHabitLog(ctx context.Context, l store.HabitLog) error
	HabitLogs(ctx context.Context, habitID, from, to string) ([]store.HabitLog, error)
	ShowingUpDays(ctx context.Context, habitID string) (int, error)

	AddSleepEvent(ctx context.Context, e store.SleepEvent) error
	SleepEvents(ctx context.Context, from, to time.Time) ([]store.SleepEvent, error)
	LastActivity(ctx context.Context, t time.Time) (time.Time, bool, error)
	SetSleepNight(ctx context.Context, n store.SleepNight) error
	SleepNights(ctx context.Context, day string, n int) ([]store.SleepNight, error)
	GetSleepNight(ctx context.Context, day string) (store.SleepNight, error)

	AddNotification(ctx context.Context, n store.Notification) (int64, error)
	Notifications(ctx context.Context, since time.Time) ([]store.Notification, error)
	MarkResponded(ctx context.Context, t time.Time, window time.Duration) (bool, error)

	GetProfile(ctx context.Context, key string) (string, error)
	SetProfile(ctx context.Context, key, value string, now time.Time) error
}

// Options configures a Coach.
type Options struct {
	Store  Store
	Out    Outbox
	Clock  clock.Clock
	Loc    *time.Location
	Log    *slog.Logger
	Config Config
	// Digests supplies extra lines for the weekly review, such as a plugin's
	// counts of workouts. Optional.
	Digests func(ctx context.Context) []string
}

// Config holds the coach's defaults. The owner's preferences override them.
type Config struct {
	// MaxPerDay caps proactive messages per local day.
	MaxPerDay int
	// QuietStart and QuietEnd (HH:MM) are the declared quiet hours, used until
	// enough nights have been observed.
	QuietStart, QuietEnd string
	// SleepTarget is the nightly sleep the owner is aiming for. Defaults to 8h.
	SleepTarget time.Duration
}

// Coach is the coaching engine. It is safe for concurrent use.
type Coach struct {
	db    Store
	out   Outbox
	clock clock.Clock
	loc   *time.Location
	log   *slog.Logger
	cfg   Config
	msgs  *messages.Set
	prior sleep.Prior
	extra func(ctx context.Context) []string

	mu sync.Mutex // serializes state changes: ticks, callbacks, tools
}

// New builds a Coach, loading and validating the message templates.
func New(o Options) (*Coach, error) {
	if o.Store == nil || o.Out == nil {
		return nil, errors.New("coach: a store and an outbox are required")
	}
	if o.Clock == nil {
		o.Clock = clock.Real{}
	}
	if o.Loc == nil {
		o.Loc = time.UTC
	}
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
	if o.Config.SleepTarget == 0 {
		o.Config.SleepTarget = 8 * time.Hour
	}
	qs, err := habit.ParseHM(orDefault(o.Config.QuietStart, "23:00"))
	if err != nil {
		return nil, fmt.Errorf("coach: quiet_start: %w", err)
	}
	qe, err := habit.ParseHM(orDefault(o.Config.QuietEnd, "07:00"))
	if err != nil {
		return nil, fmt.Errorf("coach: quiet_end: %w", err)
	}
	set, err := messages.Load()
	if err != nil {
		return nil, err
	}
	return &Coach{
		db: o.Store, out: o.Out, clock: o.Clock, loc: o.Loc, log: o.Log, cfg: o.Config, msgs: set,
		prior: sleep.Prior{QuietStart: qs, QuietEnd: qe}, extra: o.Digests,
	}, nil
}

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

// --- preferences ---------------------------------------------------------------------

const (
	prefsKey       = "coach_prefs"
	sleepParamsKey = "sleep_params"
)

// Prefs are the owner's choices about the daily rhythm. Anything the owner
// turns off stays off.
type Prefs struct {
	Morning  bool `json:"morning"`
	Habits   bool `json:"habits"`
	Evening  bool `json:"evening"`
	Weekly   bool `json:"weekly"`
	Winddown bool `json:"winddown"`
	// MaxPerDay overrides the configured cap when positive. Zero means use the
	// configured cap; Silent turns every proactive message off.
	MaxPerDay int  `json:"max_per_day"`
	Silent    bool `json:"silent"`
}

func defaultPrefs() Prefs { return Prefs{Morning: true, Habits: true, Evening: true, Weekly: true} }

func (c *Coach) prefs(ctx context.Context) Prefs {
	p := defaultPrefs()
	raw, err := c.db.GetProfile(ctx, prefsKey)
	if err != nil {
		return p
	}
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		c.log.WarnContext(ctx, "unreadable coach preferences; using defaults", "err", err)
		return defaultPrefs()
	}
	return p
}

func (c *Coach) savePrefs(ctx context.Context, p Prefs) error {
	raw, _ := json.Marshal(p)
	return c.db.SetProfile(ctx, prefsKey, string(raw), c.clock.Now())
}

// dailyCap returns the effective cap on proactive messages per day.
func (c *Coach) dailyCap(p Prefs) int {
	if p.Silent {
		return 0
	}
	if p.MaxPerDay > 0 {
		return p.MaxPerDay
	}
	return c.cfg.MaxPerDay
}

// --- sleep: signals, nights, model ---------------------------------------------------

type storedParams struct {
	Latency int `json:"latency_s"`
	Onset   int `json:"onset_s"`
	Wake    int `json:"wake_s"`
}

func (c *Coach) sleepParams(ctx context.Context) sleep.Params {
	p := sleep.DefaultParams()
	raw, err := c.db.GetProfile(ctx, sleepParamsKey)
	if err != nil {
		return p
	}
	var sp storedParams
	if json.Unmarshal([]byte(raw), &sp) != nil {
		return p
	}
	return sleep.Params{
		Latency:     time.Duration(sp.Latency) * time.Second,
		OnsetOffset: time.Duration(sp.Onset) * time.Second,
		WakeOffset:  time.Duration(sp.Wake) * time.Second,
	}
}

func (c *Coach) saveSleepParams(ctx context.Context, p sleep.Params) error {
	raw, _ := json.Marshal(storedParams{
		Latency: int(p.Latency / time.Second), Onset: int(p.OnsetOffset / time.Second), Wake: int(p.WakeOffset / time.Second),
	})
	return c.db.SetProfile(ctx, sleepParamsKey, string(raw), c.clock.Now())
}

// nights returns up to 28 recent nights, newest first, as of now.
func (c *Coach) nights(ctx context.Context, now time.Time) []store.SleepNight {
	n, err := c.db.SleepNights(ctx, habit.Day(now, c.loc), 28)
	if err != nil {
		c.log.WarnContext(ctx, "load sleep nights", "err", err)
	}
	return n
}

func (c *Coach) model(ctx context.Context, now time.Time) sleep.Model {
	return sleep.NewModel(c.prior, c.nights(ctx, now), c.loc)
}

// RecordActivity notes that the owner just did something (sent a message or
// pressed a button). It feeds the sleep model and credits the proactive message
// the owner is most likely reacting to.
func (c *Coach) RecordActivity(ctx context.Context, ts time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.db.AddSleepEvent(ctx, store.SleepEvent{TS: ts, Kind: store.SleepActivity}); err != nil {
		c.log.WarnContext(ctx, "record activity", "err", err)
	}
	if _, err := c.db.MarkResponded(ctx, ts, budget.DefaultResponseWindow); err != nil {
		c.log.WarnContext(ctx, "mark response", "err", err)
	}
}

// NoteMessage looks for a plain statement that the owner is going to bed or
// has woken up, and records it as an explicit sleep signal. It reports what it
// found, if anything.
func (c *Coach) NoteMessage(ctx context.Context, text string, ts time.Time) (kind string, found bool) {
	kind, found = sleep.DetectSignal(text)
	if !found {
		return "", false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.db.AddSleepEvent(ctx, store.SleepEvent{TS: ts, Kind: kind}); err != nil {
		c.log.WarnContext(ctx, "record sleep signal", "err", err)
		return "", false
	}
	if kind == sleep.SignalWake {
		c.refreshNight(ctx, ts)
	}
	return kind, true
}

// refreshNight (re)computes the night that ends on the local day of `now` and
// the one before it, storing the results. Confirmed nights are never replaced.
// When a night turns out to be explicit and a chat-gap estimate exists for the
// same night, the difference teaches the corrections.
func (c *Coach) refreshNight(ctx context.Context, now time.Time) {
	for back := 0; back <= 1; back++ {
		day := now.In(c.loc).AddDate(0, 0, -back)
		c.estimateAndStore(ctx, day)
	}
}

func (c *Coach) estimateAndStore(ctx context.Context, day time.Time) {
	dayStr := habit.Day(day, c.loc)
	existing, err := c.db.GetSleepNight(ctx, dayStr)
	haveExisting := err == nil
	if haveExisting && existing.UserConfirmed {
		return
	}

	from := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, c.loc).AddDate(0, 0, -1)
	events, err := c.db.SleepEvents(ctx, from, from.AddDate(0, 0, 3))
	if err != nil {
		c.log.WarnContext(ctx, "load sleep events", "err", err)
		return
	}
	params := c.sleepParams(ctx)
	night, ok := sleep.EstimateNight(events, day, c.loc, params)
	if !ok {
		return
	}

	if night.Confidence == store.ConfExplicit && (!haveExisting || existing.Confidence != store.ConfExplicit) {
		// Compare with what chat gaps alone would have said, and learn from it.
		var activityOnly []store.SleepEvent
		for _, e := range events {
			if e.Kind == store.SleepActivity {
				activityOnly = append(activityOnly, e)
			}
		}
		if guess, ok := sleep.EstimateNight(activityOnly, day, c.loc, params); ok {
			if err := c.saveSleepParams(ctx, params.Learn(guess, night, 0.3)); err != nil {
				c.log.WarnContext(ctx, "save sleep corrections", "err", err)
			}
		}
	}
	if haveExisting && existing.Confidence == night.Confidence && existing.Onset.Equal(night.Onset) && existing.Wake.Equal(night.Wake) {
		return // unchanged
	}
	if err := c.db.SetSleepNight(ctx, night); err != nil {
		c.log.WarnContext(ctx, "store sleep night", "err", err)
	}
}

// hoursText turns a duration into something safe to say: rounded to the nearest
// half hour, with "about" in front. Sleep estimates are never precise.
func hoursText(d time.Duration) string {
	half := float64(int(d.Hours()*2+0.5)) / 2
	whole := int(half)
	words := []string{"zero", "one", "two", "three", "four", "five", "six", "seven", "eight", "nine", "ten", "eleven", "twelve"}
	num := fmt.Sprint(whole)
	if whole < len(words) {
		num = words[whole]
	}
	switch {
	case half < 1:
		return "under an hour"
	case half == 1:
		return "about an hour"
	case half == float64(whole):
		return "about " + num + " hours"
	}
	return "about " + num + " and a half hours"
}
