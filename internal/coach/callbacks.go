package coach

import (
	"context"
	"strings"
	"time"

	"github.com/Harfull/Kest/internal/coach/habit"
	"github.com/Harfull/Kest/internal/coach/sleep"
	"github.com/Harfull/Kest/internal/store"
)

// Callback data is "<area>:<action>[:<argument>]", at most 64 bytes:
//
//	h:d|t|s|p|r|k:<habit id>   done, small version, not today, pause, resize, keep going
//	s:ok|sh|lg:<day>           sleep estimate: about right, shorter, longer
//	e:g|r                      evening: good day, rough day
//	b:less|keep                message less, or keep things as they are
//	w:adj                      weekly review: adjust something

// HandleCallback acts on a button press from a coach message and returns the
// reply to show. handled is false when the data does not belong to the coach.
func (c *Coach) HandleCallback(ctx context.Context, data string) (reply Message, handled bool) {
	area, rest, ok := strings.Cut(data, ":")
	if !ok {
		return Message{}, false
	}
	action, arg, _ := strings.Cut(rest, ":")

	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.clock.Now()

	switch area {
	case "h":
		return c.habitCallback(ctx, now, action, arg), true
	case "s":
		return c.sleepCallback(ctx, now, action, arg), true
	case "e":
		kind := "rough_day"
		if action == "g" {
			kind = "good_day"
		}
		return c.say(kind, nil, now), true
	case "b":
		return c.backoffCallback(ctx, now, action), true
	case "w":
		return c.say("habit_resize", nil, now), true
	}
	return Message{}, false
}

// say renders a small reply. Failures fall back to a plain acknowledgement so a
// button press is never met with silence.
func (c *Coach) say(kind string, data map[string]any, now time.Time) Message {
	if data == nil {
		data = map[string]any{}
	}
	text, _, err := c.render(kind, data, "", int(now.Unix()/60))
	if err != nil {
		c.log.Error("render reply", "kind", kind, "err", err)
		text = "Noted."
	}
	return Message{Text: text}
}

func (c *Coach) habitCallback(ctx context.Context, now time.Time, action, id string) Message {
	h, err := c.db.GetHabit(ctx, id)
	if err != nil || h.State == store.HabitRetired {
		return Message{Text: "That habit is not active any more."}
	}

	switch action {
	case "p":
		h.State = store.HabitPaused
		if err := c.db.UpdateHabit(ctx, h); err != nil {
			c.log.ErrorContext(ctx, "pause habit", "err", err)
			return Message{Text: "I could not pause that just now. Please try again."}
		}
		return c.say("habit_paused", map[string]any{"Habit": h.Name}, now)
	case "r":
		return c.say("habit_resize", nil, now)
	case "k":
		return c.say("backoff_keep", nil, now)
	}

	status := map[string]string{"d": store.LogDone, "t": store.LogTiny, "s": store.LogSkipped}[action]
	if status == "" {
		return Message{Text: "I did not understand that button."}
	}
	if err := c.db.SetHabitLog(ctx, store.HabitLog{HabitID: h.ID, Day: habit.Day(now, c.loc), Status: status, TS: now}); err != nil {
		c.log.ErrorContext(ctx, "log habit", "err", err)
		return Message{Text: "I could not save that just now. Please try again."}
	}

	cons := c.consistency(ctx, h, now)
	switch status {
	case store.LogDone:
		return c.say("habit_done", map[string]any{"Habit": h.Name, "Done7": cons.Done7, "Expected7": cons.Expected7}, now)
	case store.LogTiny:
		return c.say("habit_tiny", map[string]any{"Habit": h.Name}, now)
	}
	return c.say("habit_skipped", map[string]any{"Habit": h.Name}, now)
}

// sleepCallback applies the owner's verdict on an estimate. "About right"
// confirms it. "Shorter" or "Longer" moves the onset by 45 minutes, confirms
// the night, and teaches the model so the next estimate starts closer.
func (c *Coach) sleepCallback(ctx context.Context, now time.Time, action, day string) Message {
	night, err := c.db.GetSleepNight(ctx, day)
	if err != nil {
		return c.say("sleep_ack", nil, now)
	}
	if night.UserConfirmed {
		return c.say("sleep_ack", nil, now)
	}

	truth := night
	switch action {
	case "sh":
		truth.Onset = night.Onset.Add(45 * time.Minute)
	case "lg":
		truth.Onset = night.Onset.Add(-45 * time.Minute)
	}
	if action != "ok" {
		c.saveSleepParamsFrom(ctx, night, truth)
	}
	truth.Confidence, truth.UserConfirmed = store.ConfConfirmed, true
	if err := c.db.SetSleepNight(ctx, truth); err != nil {
		c.log.ErrorContext(ctx, "confirm sleep night", "err", err)
	}
	return c.say("sleep_ack", nil, now)
}

func (c *Coach) saveSleepParamsFrom(ctx context.Context, estimate, truth store.SleepNight) {
	if err := c.saveSleepParams(ctx, c.sleepParams(ctx).Learn(estimate, truth, 0.3)); err != nil {
		c.log.WarnContext(ctx, "save sleep corrections", "err", err)
	}
}

func (c *Coach) backoffCallback(ctx context.Context, now time.Time, action string) Message {
	if action != "less" {
		return c.say("backoff_keep", nil, now)
	}
	p := c.prefs(ctx)
	current := c.dailyCap(p)
	p.Silent = false
	p.MaxPerDay = max(1, current-1)
	if err := c.savePrefs(ctx, p); err != nil {
		c.log.ErrorContext(ctx, "save preferences", "err", err)
	}
	return c.say("backoff_less", nil, now)
}

// Summary describes last night for the agent's prompt, in safe words.
func (c *Coach) lastNightLine(ctx context.Context, now time.Time) string {
	n, err := c.db.GetSleepNight(ctx, habit.Day(now, c.loc))
	if err != nil {
		return ""
	}
	how := "estimated from chat activity"
	switch n.Confidence {
	case store.ConfExplicit:
		how = "from the owner's own times"
	case store.ConfConfirmed:
		how = "confirmed by the owner"
	}
	return "Last night: " + hoursText(sleep.Duration(n)) + " (" + how + ")."
}
