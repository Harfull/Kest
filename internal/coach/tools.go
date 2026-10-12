package coach

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Harfull/Kest/internal/coach/habit"
	"github.com/Harfull/Kest/internal/coach/sleep"
	"github.com/Harfull/Kest/internal/plugin"
	"github.com/Harfull/Kest/internal/store"
)

const (
	maxActiveHabits = 7
	maxBackdate     = 14 // days a habit log may be entered late
	toolPlugin      = "coach"
)

// Tools returns the agent's coaching tools. They obey the same risk policy as
// plugin tools: writes need approval once untrusted content has been read.
func (c *Coach) Tools() []plugin.ToolRef {
	t := func(name, desc string, risk plugin.Risk, schema string, h func(context.Context, plugin.Call) (plugin.Result, error)) plugin.ToolRef {
		return plugin.ToolRef{Plugin: toolPlugin, Tool: plugin.Tool{
			Name: name, Description: desc, Risk: risk, Schema: json.RawMessage(schema), Handler: h,
		}}
	}
	return []plugin.ToolRef{
		t("create_habit",
			"Create a habit with an if-then plan. First ask the owner what the habit is, where it fits in their day (the cue), and the smallest "+
				"version they could still do on their worst day. Keep the habit small. Cadence is daily, weekdays, days:mon,wed,fri or times:3. "+
				"Times are local 24-hour HH:MM and set when Kest nudges.",
			plugin.RiskWrite, `{"type":"object","properties":{
				"name":{"type":"string","description":"Short name, like Walk after lunch."},
				"behavior":{"type":"string","description":"The habit itself, like Walk for 10 minutes."},
				"tiny_version":{"type":"string","description":"The worst-day version, like Put on shoes and step outside."},
				"anchor":{"type":"string","description":"The routine it follows, like after lunch. Empty if it is tied to a time."},
				"why":{"type":"string","description":"Why it matters, in the owner's words."},
				"cadence":{"type":"string"},
				"window_start":{"type":"string","description":"When to nudge, HH:MM."},
				"window_end":{"type":"string","description":"When the nudge stops being useful, HH:MM."}
			},"required":["name","behavior","tiny_version"],"additionalProperties":false}`, c.toolCreateHabit),
		t("list_habits", "List the owner's habits with their ids, plans and how consistently each is being kept.",
			plugin.RiskRead, `{"type":"object","properties":{},"additionalProperties":false}`, c.toolListHabits),
		t("log_habit",
			"Record how a habit went: done, tiny (the small version) or skipped. Defaults to today; date may be up to two weeks back (YYYY-MM-DD).",
			plugin.RiskWrite, `{"type":"object","properties":{
				"habit":{"type":"string","description":"The habit's id or name."},
				"status":{"type":"string","enum":["done","tiny","skipped"]},
				"date":{"type":"string"}
			},"required":["habit","status"],"additionalProperties":false}`, c.toolLogHabit),
		t("update_habit",
			"Change a habit's plan, or pause, resume or retire it (state: active, paused, retired). Use this when the owner wants it smaller, at a new time, or put on hold.",
			plugin.RiskWrite, `{"type":"object","properties":{
				"habit":{"type":"string","description":"The habit's id or name."},
				"name":{"type":"string"},"behavior":{"type":"string"},"tiny_version":{"type":"string"},
				"anchor":{"type":"string"},"why":{"type":"string"},"cadence":{"type":"string"},
				"window_start":{"type":"string"},"window_end":{"type":"string"},
				"state":{"type":"string","enum":["active","paused","retired"]}
			},"required":["habit"],"additionalProperties":false}`, c.toolUpdateHabit),
		t("log_sleep",
			"Record when the owner went to bed or woke up, if they tell you. time is optional: HH:MM (the most recent such time) or YYYY-MM-DDTHH:MM local; default now.",
			plugin.RiskWrite, `{"type":"object","properties":{
				"kind":{"type":"string","enum":["bedtime","wake"]},
				"time":{"type":"string"}
			},"required":["kind"],"additionalProperties":false}`, c.toolLogSleep),
		t("sleep_report",
			"Summarize the owner's recent sleep: typical times, average length, regularity and how sure Kest is. Estimates are approximate and never a diagnosis.",
			plugin.RiskRead, `{"type":"object","properties":{},"additionalProperties":false}`, c.toolSleepReport),
		t("set_preferences",
			"Change how often and when Kest checks in. Use when the owner asks for fewer or more messages, or to stop or start a kind of check-in. "+
				"max_per_day 0 means keep the configured limit; silent true turns every unprompted message off.",
			plugin.RiskWrite, `{"type":"object","properties":{
				"morning":{"type":"boolean"},"habits":{"type":"boolean"},"evening":{"type":"boolean"},
				"weekly":{"type":"boolean"},"winddown":{"type":"boolean"},
				"max_per_day":{"type":"integer","minimum":0,"maximum":10},
				"silent":{"type":"boolean"}
			},"additionalProperties":false}`, c.toolSetPreferences),
	}
}

func newHabitID() (string, error) {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate habit id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// findHabit resolves an id or a (partial, case-insensitive) name.
func (c *Coach) findHabit(ctx context.Context, ref string) (store.Habit, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return store.Habit{}, errors.New("say which habit (its id or name)")
	}
	habits, err := c.db.ListHabits(ctx, false)
	if err != nil {
		return store.Habit{}, err
	}
	var matches []store.Habit
	for _, h := range habits {
		switch {
		case h.ID == ref:
			return h, nil
		case strings.EqualFold(h.Name, ref):
			return h, nil
		case strings.Contains(strings.ToLower(h.Name), strings.ToLower(ref)):
			matches = append(matches, h)
		}
	}
	switch len(matches) {
	case 0:
		return store.Habit{}, fmt.Errorf("no habit matches %q", ref)
	case 1:
		return matches[0], nil
	}
	var names []string
	for _, m := range matches {
		names = append(names, m.Name+" ("+m.ID+")")
	}
	return store.Habit{}, fmt.Errorf("%q matches more than one habit: %s", ref, strings.Join(names, ", "))
}

func (c *Coach) toolCreateHabit(ctx context.Context, call plugin.Call) (plugin.Result, error) {
	var a struct {
		Name, Behavior, Anchor, Why, Cadence string
		TinyVersion                          string `json:"tiny_version"`
		WindowStart                          string `json:"window_start"`
		WindowEnd                            string `json:"window_end"`
	}
	if err := json.Unmarshal(call.Args, &a); err != nil {
		return plugin.Result{}, errors.New("arguments must be an object with name, behavior and tiny_version")
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	h := store.Habit{
		Name: strings.TrimSpace(a.Name), Why: strings.TrimSpace(a.Why), Anchor: strings.TrimSpace(a.Anchor),
		Behavior: strings.TrimSpace(a.Behavior), TinyVersion: strings.TrimSpace(a.TinyVersion),
		Cadence:     strings.ToLower(strings.TrimSpace(orDefault(a.Cadence, "daily"))),
		WindowStart: strings.TrimSpace(orDefault(a.WindowStart, "12:00")),
		State:       store.HabitActive, CreatedAt: c.clock.Now(),
	}
	if a.WindowEnd != "" {
		h.WindowEnd = strings.TrimSpace(a.WindowEnd)
	} else if start, err := habit.ParseHM(h.WindowStart); err == nil {
		end := min(start+180, 23*60+30)
		h.WindowEnd = fmt.Sprintf("%02d:%02d", end/60, end%60)
	}
	if err := habit.Validate(h); err != nil {
		return plugin.Result{}, err
	}

	existing, err := c.db.ListHabits(ctx, false)
	if err != nil {
		return plugin.Result{}, err
	}
	if len(existing) >= maxActiveHabits {
		return plugin.Result{}, fmt.Errorf("there are already %d habits; retire or pause one before adding another. Fewer habits kept well beats many dropped", len(existing))
	}
	for _, e := range existing {
		if strings.EqualFold(e.Name, h.Name) {
			return plugin.Result{}, fmt.Errorf("a habit called %q already exists (id %s); update it instead", e.Name, e.ID)
		}
	}

	if h.ID, err = newHabitID(); err != nil {
		return plugin.Result{}, err
	}
	if err := c.db.AddHabit(ctx, h); err != nil {
		return plugin.Result{}, err
	}

	cue := "a time"
	if h.Anchor != "" {
		cue = strings.ToLower(h.Anchor)
	}
	return plugin.Result{
		Content: fmt.Sprintf("Habit %s created: %s. Cue: %s. Plan: %s. Worst-day version: %s. Cadence: %s. Kest will nudge between %s and %s.",
			h.ID, h.Name, cue, h.Behavior, h.TinyVersion, h.Cadence, h.WindowStart, h.WindowEnd),
		Summary: "created habit " + h.ID,
	}, nil
}

func (c *Coach) toolListHabits(ctx context.Context, _ plugin.Call) (plugin.Result, error) {
	habits, err := c.db.ListHabits(ctx, false)
	if err != nil {
		return plugin.Result{}, err
	}
	if len(habits) == 0 {
		return plugin.Result{Content: "No habits yet.", Summary: "listed 0 habits"}, nil
	}
	now := c.clock.Now()
	var b strings.Builder
	for _, h := range habits {
		cons := c.consistency(ctx, h, now)
		line := fmt.Sprintf("- %s (id %s, %s, %s): %s; nudge %s-%s", h.Name, h.ID, h.State, h.Cadence, rateText(cons), h.WindowStart, h.WindowEnd)
		if h.Anchor != "" {
			line += "; cue: " + h.Anchor
		}
		line += "; small version: " + h.TinyVersion
		b.WriteString(line + "\n")
	}
	return plugin.Result{Content: strings.TrimSpace(b.String()), Summary: fmt.Sprintf("listed %d habits", len(habits))}, nil
}

// rateText states consistency in plain counts, never as a streak.
func rateText(c habit.Consistency) string {
	if c.Expected7 == 0 {
		return "just started"
	}
	s := fmt.Sprintf("%d of the last %d days", c.Done7, c.Expected7)
	if c.Expected28 > c.Expected7 {
		s += fmt.Sprintf(", %d of %d over four weeks", c.Done28, c.Expected28)
	}
	return s + fmt.Sprintf(", %d days showing up in total", c.ShowingUpDays)
}

func (c *Coach) toolLogHabit(ctx context.Context, call plugin.Call) (plugin.Result, error) {
	var a struct{ Habit, Status, Date string }
	if err := json.Unmarshal(call.Args, &a); err != nil {
		return plugin.Result{}, errors.New("arguments must be an object with habit and status")
	}
	if a.Status != store.LogDone && a.Status != store.LogTiny && a.Status != store.LogSkipped {
		return plugin.Result{}, errors.New("status must be done, tiny or skipped")
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	h, err := c.findHabit(ctx, a.Habit)
	if err != nil {
		return plugin.Result{}, err
	}
	now := c.clock.Now()
	day := habit.Day(now, c.loc)
	if d := strings.TrimSpace(a.Date); d != "" {
		t, err := time.ParseInLocation("2006-01-02", d, c.loc)
		if err != nil {
			return plugin.Result{}, fmt.Errorf("date %q must look like 2026-10-12", d)
		}
		today := c.startOfDay(now)
		switch {
		case t.After(today):
			return plugin.Result{}, errors.New("that date is in the future")
		case today.Sub(t) > maxBackdate*24*time.Hour:
			return plugin.Result{}, fmt.Errorf("logs can go back %d days at most", maxBackdate)
		}
		day = d
	}
	if err := c.db.SetHabitLog(ctx, store.HabitLog{HabitID: h.ID, Day: day, Status: a.Status, TS: now}); err != nil {
		return plugin.Result{}, err
	}

	cons := c.consistency(ctx, h, now)
	return plugin.Result{
		Content: fmt.Sprintf("Logged %s as %s for %s. Now %s.", h.Name, a.Status, day, rateText(cons)),
		Summary: "logged habit " + h.ID,
	}, nil
}

func (c *Coach) toolUpdateHabit(ctx context.Context, call plugin.Call) (plugin.Result, error) {
	var a struct {
		Habit, Name, Behavior, Anchor, Why, Cadence, State string
		TinyVersion                                        string `json:"tiny_version"`
		WindowStart                                        string `json:"window_start"`
		WindowEnd                                          string `json:"window_end"`
	}
	if err := json.Unmarshal(call.Args, &a); err != nil {
		return plugin.Result{}, errors.New("arguments must be an object with habit")
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	h, err := c.findHabit(ctx, a.Habit)
	if err != nil {
		return plugin.Result{}, err
	}
	set := func(dst *string, v string) {
		if v = strings.TrimSpace(v); v != "" {
			*dst = v
		}
	}
	set(&h.Name, a.Name)
	set(&h.Behavior, a.Behavior)
	set(&h.TinyVersion, a.TinyVersion)
	set(&h.Anchor, a.Anchor)
	set(&h.Why, a.Why)
	set(&h.Cadence, strings.ToLower(a.Cadence))
	set(&h.WindowStart, a.WindowStart)
	set(&h.WindowEnd, a.WindowEnd)
	if st := strings.TrimSpace(a.State); st != "" {
		if st != store.HabitActive && st != store.HabitPaused && st != store.HabitRetired {
			return plugin.Result{}, errors.New("state must be active, paused or retired")
		}
		h.State = st
	}
	// Moving the start later without an end keeps the window valid.
	if a.WindowStart != "" && a.WindowEnd == "" {
		if s, err := habit.ParseHM(h.WindowStart); err == nil {
			if e, err := habit.ParseHM(h.WindowEnd); err == nil && e <= s+29 {
				end := min(s+180, 23*60+30)
				h.WindowEnd = fmt.Sprintf("%02d:%02d", end/60, end%60)
			}
		}
	}
	if err := habit.Validate(h); err != nil {
		return plugin.Result{}, err
	}
	if err := c.db.UpdateHabit(ctx, h); err != nil {
		return plugin.Result{}, err
	}
	return plugin.Result{
		Content: fmt.Sprintf("Updated %s (%s): %s, small version: %s, nudge %s-%s, %s.", h.Name, h.State, h.Behavior, h.TinyVersion, h.WindowStart, h.WindowEnd, h.Cadence),
		Summary: "updated habit " + h.ID,
	}, nil
}

// parseSleepTime reads HH:MM (the most recent such time not in the future) or
// a full local date and time.
func (c *Coach) parseSleepTime(s string, now time.Time) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return now, nil
	}
	for _, layout := range []string{"2006-01-02T15:04", "2006-01-02 15:04"} {
		if t, err := time.ParseInLocation(layout, s, c.loc); err == nil {
			return t, nil
		}
	}
	m, err := habit.ParseHM(s)
	if err != nil {
		return time.Time{}, fmt.Errorf("time %q must be HH:MM or YYYY-MM-DDTHH:MM", s)
	}
	l := now.In(c.loc)
	t := time.Date(l.Year(), l.Month(), l.Day(), m/60, m%60, 0, 0, c.loc)
	if t.After(now) {
		t = t.AddDate(0, 0, -1)
	}
	return t, nil
}

func (c *Coach) toolLogSleep(ctx context.Context, call plugin.Call) (plugin.Result, error) {
	var a struct{ Kind, Time string }
	if err := json.Unmarshal(call.Args, &a); err != nil || (a.Kind != "bedtime" && a.Kind != "wake") {
		return plugin.Result{}, errors.New("kind must be bedtime or wake")
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	now := c.clock.Now()
	t, err := c.parseSleepTime(a.Time, now)
	if err != nil {
		return plugin.Result{}, err
	}
	if t.After(now.Add(5 * time.Minute)) {
		return plugin.Result{}, errors.New("that time is in the future")
	}
	if now.Sub(t) > 36*time.Hour {
		return plugin.Result{}, errors.New("that is too long ago to be useful")
	}
	if err := c.db.AddSleepEvent(ctx, store.SleepEvent{TS: t, Kind: a.Kind, Source: "tool"}); err != nil {
		return plugin.Result{}, err
	}
	c.refreshNight(ctx, now)
	return plugin.Result{
		Content: fmt.Sprintf("Recorded %s at %s.", a.Kind, t.In(c.loc).Format("Mon 2 Jan, 15:04")),
		Summary: "logged " + a.Kind,
	}, nil
}

func clockText(minutes int) string { return fmt.Sprintf("%02d:%02d", minutes/60, minutes%60) }

func (c *Coach) toolSleepReport(ctx context.Context, _ plugin.Call) (plugin.Result, error) {
	now := c.clock.Now()
	nights := c.nights(ctx, now)
	model := c.model(ctx, now)

	var b strings.Builder
	if !model.Learned() {
		fmt.Fprintf(&b, "Only %d observed night(s) so far, so the schedule in use is the declared one: asleep about %s, up about %s.",
			model.Nights(), clockText(model.Onset()), clockText(model.Wake()))
		if line := c.lastNightLine(ctx, now); line != "" {
			b.WriteString("\n" + line)
		}
		return plugin.Result{Content: b.String(), Summary: "sleep report (not enough nights)"}, nil
	}

	if len(nights) > 14 {
		nights = nights[:14]
	}
	s := sleep.Summarize(nights, c.cfg.SleepTarget, c.loc)
	fmt.Fprintf(&b, "Across %d recent nights: usually asleep around %s and up around %s; average %s.",
		s.Nights, clockText(model.Onset()), clockText(model.Wake()), hoursText(s.AvgDuration))
	fmt.Fprintf(&b, "\nRegularity: the middle of sleep moves by about %d minutes from night to night.", int(s.Regularity/time.Minute))
	if s.Debt > 30*time.Minute {
		fmt.Fprintf(&b, "\nOver the last week that is %s short of a %s target.", hoursText(s.Debt), hoursText(c.cfg.SleepTarget))
	}
	if s.LowConfidence {
		b.WriteString("\nMost nights are estimated from chat activity, so treat the numbers as rough. Telling Kest when you go to bed and wake up sharpens them.")
	}
	if line := c.lastNightLine(ctx, now); line != "" {
		b.WriteString("\n" + line)
	}
	return plugin.Result{Content: b.String(), Summary: "sleep report"}, nil
}

func (c *Coach) toolSetPreferences(ctx context.Context, call plugin.Call) (plugin.Result, error) {
	var a struct {
		Morning, Habits, Evening, Weekly, Winddown, Silent *bool
		MaxPerDay                                          *int `json:"max_per_day"`
	}
	if err := json.Unmarshal(call.Args, &a); err != nil {
		return plugin.Result{}, errors.New("arguments must be an object of preferences")
	}
	if a.MaxPerDay != nil && (*a.MaxPerDay < 0 || *a.MaxPerDay > 10) {
		return plugin.Result{}, errors.New("max_per_day must be between 0 and 10")
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	p := c.prefs(ctx)
	for dst, src := range map[*bool]*bool{&p.Morning: a.Morning, &p.Habits: a.Habits, &p.Evening: a.Evening, &p.Weekly: a.Weekly, &p.Winddown: a.Winddown, &p.Silent: a.Silent} {
		if src != nil {
			*dst = *src
		}
	}
	if a.MaxPerDay != nil {
		p.MaxPerDay = *a.MaxPerDay
	}
	if err := c.savePrefs(ctx, p); err != nil {
		return plugin.Result{}, err
	}
	return plugin.Result{Content: c.prefsText(p), Summary: "updated check-in preferences"}, nil
}

func (c *Coach) prefsText(p Prefs) string {
	onoff := func(b bool) string {
		if b {
			return "on"
		}
		return "off"
	}
	s := fmt.Sprintf("Check-ins: morning %s, habit prompts %s, evening %s, weekly review %s, wind-down %s. Up to %d a day.",
		onoff(p.Morning), onoff(p.Habits), onoff(p.Evening), onoff(p.Weekly), onoff(p.Winddown), c.dailyCap(p))
	if p.Silent {
		s = "All unprompted messages are off. " + s
	}
	return s
}

// Settings describes the current check-in settings and quiet hours, for /settings.
func (c *Coach) Settings(ctx context.Context) string {
	c.mu.Lock()
	defer c.mu.Unlock()

	m := c.model(ctx, c.clock.Now())
	source := "from your configured hours"
	if m.Learned() {
		source = fmt.Sprintf("learned from your last %d nights", m.Nights())
	}
	return c.prefsText(c.prefs(ctx)) + fmt.Sprintf("\nQuiet hours: %s to %s (%s).", clockText(m.QuietStart()), clockText(m.Wake()), source)
}

// PromptContext returns coaching facts and guidance for the agent's system
// prompt: the habits being kept, last night's sleep, and how to set up a habit.
func (c *Coach) PromptContext(ctx context.Context) string {
	now := c.clock.Now()
	var b strings.Builder

	habits, _ := c.db.ListHabits(ctx, false)
	if len(habits) > 0 {
		b.WriteString("- The owner's habits:\n")
		for _, h := range habits {
			fmt.Fprintf(&b, "  - %s (id %s, %s): %s\n", h.Name, h.ID, h.State, rateText(c.consistency(ctx, h, now)))
		}
	}
	if line := c.lastNightLine(ctx, now); line != "" {
		b.WriteString("- " + line + "\n")
	}
	b.WriteString("- " + c.prefsText(c.prefs(ctx)) + "\n")
	b.WriteString("- Habit coaching: to start a habit, ask what it is, where it fits in the day, and the smallest version they could do on a worst day, then call coach__create_habit. " +
		"Keep habits tiny and few. Talk about consistency (\"4 of the last 6 days\"), never streaks, and never treat a miss as failure. " +
		"Sleep numbers are approximate; say \"about\".\n")
	return b.String()
}
