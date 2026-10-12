package coach

import (
	"context"
	"fmt"
	"hash/fnv"
	"sort"
	"strings"
	"time"

	"github.com/Harfull/Kest/internal/coach/budget"
	"github.com/Harfull/Kest/internal/coach/habit"
	"github.com/Harfull/Kest/internal/coach/sleep"
	"github.com/Harfull/Kest/internal/store"
)

// Kinds of proactive message, as recorded in the notification history.
const (
	kindMorning  = "morning"
	kindEvening  = "evening"
	kindWeekly   = "weekly"
	kindWinddown = "winddown"
	kindBackoff  = "backoff"
	kindHabit    = "habit:" // followed by the habit id
)

// candidate is a message that is due, before the budget has had its say.
type candidate struct {
	kind string
	prio budget.Priority
	// build composes the message. It runs only if the budget allows the send.
	build func(ctx context.Context, avoid string, seed int) (Message, string, error)
}

// Tick sends the check-in that is due, if any and if the budget allows. It
// sends at most one message per call. Call it every few minutes.
func (c *Coach) Tick(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := c.clock.Now()
	prefs := c.prefs(ctx)
	if c.dailyCap(prefs) == 0 {
		return nil
	}

	c.refreshNight(ctx, now)
	model := c.model(ctx, now)

	history, err := c.db.Notifications(ctx, now.AddDate(0, 0, -8))
	if err != nil {
		return fmt.Errorf("load history: %w", err)
	}
	policy := budget.New(c.dailyCap(prefs), c.loc)

	cands := c.candidates(ctx, now, prefs, model, history, policy)
	quiet := model.InQuiet(now)

	for _, cand := range cands {
		d := policy.Allow(now, cand.kind, cand.prio, history, quiet)
		if !d.OK {
			c.log.DebugContext(ctx, "coach message held back", "kind", cand.kind, "reason", d.Reason)
			continue
		}
		seed := seedFor(now, cand.kind)
		msg, variant, err := cand.build(ctx, lastVariant(history, cand.kind), seed)
		if err != nil {
			c.log.ErrorContext(ctx, "compose coach message", "kind", cand.kind, "err", err)
			continue
		}
		if err := c.out.Send(ctx, msg); err != nil {
			return fmt.Errorf("send %s: %w", cand.kind, err)
		}
		if _, err := c.db.AddNotification(ctx, store.Notification{TS: now, Kind: cand.kind, Variant: variant}); err != nil {
			return fmt.Errorf("record %s: %w", cand.kind, err)
		}
		return nil // one message per tick
	}
	return nil
}

// seedFor gives a stable number per day and kind, so wording rotates day to day.
func seedFor(now time.Time, kind string) int {
	h := fnv.New32a()
	_, _ = h.Write([]byte(kind))
	return int(now.Unix()/86400) + int(h.Sum32()%1000)
}

// lastVariant returns the wording used the last time a kind was sent.
func lastVariant(history []store.Notification, kind string) string {
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Kind == kind {
			return history[i].Variant
		}
	}
	return ""
}

// sentSince reports whether a message of this kind went out at or after t.
func sentSince(history []store.Notification, kind string, t time.Time) bool {
	for _, n := range history {
		if n.Kind == kind && !n.TS.Before(t) {
			return true
		}
	}
	return false
}

// startOfDay returns local midnight for the day containing t.
func (c *Coach) startOfDay(t time.Time) time.Time {
	l := t.In(c.loc)
	return time.Date(l.Year(), l.Month(), l.Day(), 0, 0, 0, 0, c.loc)
}

// candidates lists what is due now, in the order it should be tried.
func (c *Coach) candidates(ctx context.Context, now time.Time, p Prefs, model sleep.Model, history []store.Notification, policy budget.Policy) []candidate {
	var out []candidate
	dayStart := c.startOfDay(now)

	// The question about messaging less comes first: it is the polite thing to
	// do when the owner has gone quiet.
	if policy.ShouldAskToBackOff(history, now) {
		out = append(out, candidate{kind: kindBackoff, prio: budget.Low, build: c.buildBackoff})
	}

	// Morning: from 15 minutes after the usual wake time until noon, unless the
	// owner has already said good morning or is mid-conversation (morningHeld).
	if p.Morning && !sentSince(history, kindMorning, dayStart) {
		at := model.MorningAt(now)
		if !now.Before(at) && now.Before(dayStart.Add(12*time.Hour)) && !c.morningHeld(ctx, now) {
			out = append(out, candidate{kind: kindMorning, prio: budget.Normal, build: c.buildMorning})
		}
	}

	// Habit prompts: inside each habit's window, once a day, if it is due.
	if p.Habits {
		habits, err := c.db.ListHabits(ctx, false)
		if err != nil {
			c.log.WarnContext(ctx, "list habits", "err", err)
		}
		type due struct {
			h   store.Habit
			end time.Time
		}
		var dues []due
		for _, h := range habits {
			start, end := habit.Window(h, now, c.loc)
			if now.Before(start) || !now.Before(end) || sentSince(history, kindHabit+h.ID, dayStart) {
				continue
			}
			if c.habitDue(ctx, h, now) {
				dues = append(dues, due{h, end})
			}
		}
		sort.Slice(dues, func(i, j int) bool { return dues[i].end.Before(dues[j].end) })
		for _, d := range dues {
			out = append(out, candidate{
				kind: kindHabit + d.h.ID, prio: budget.Normal,
				build: func(ctx context.Context, avoid string, seed int) (Message, string, error) {
					return c.buildHabit(ctx, d.h, avoid, seed)
				},
			})
		}
	}

	// On Sunday the weekly review takes the evening slot, so there is one message,
	// not two back to back.
	sundayReview := p.Weekly && now.In(c.loc).Weekday() == time.Sunday

	if p.Evening && !sundayReview && !sentSince(history, kindEvening, dayStart) {
		at := model.EveningAt(now)
		if !now.Before(at) && now.Before(at.Add(3*time.Hour)) {
			out = append(out, candidate{kind: kindEvening, prio: budget.Normal, build: c.buildEvening})
		}
	}

	if p.Winddown && !sentSince(history, kindWinddown, dayStart) {
		at := model.WindDownAt(now)
		if !now.Before(at) && now.Before(at.Add(90*time.Minute)) {
			out = append(out, candidate{kind: kindWinddown, prio: budget.Low, build: c.buildWinddown})
		}
	}

	// Weekly review: Sunday at the usual evening time, once.
	if sundayReview && !sentSince(history, kindWeekly, dayStart) {
		at := model.EveningAt(now)
		if !now.Before(at) && now.Before(at.Add(3*time.Hour)) {
			out = append(out, candidate{kind: kindWeekly, prio: budget.Normal, build: c.buildWeekly})
		}
	}
	return out
}

// morningHeld reports whether the morning check-in should wait or be dropped:
//
//   - the owner already said good morning (an explicit wake signal) today: the
//     conversation has done the job, so the check-in is dropped;
//   - the owner was active in the last 20 minutes: they are mid-conversation, so
//     the check-in waits for a quieter moment.
func (c *Coach) morningHeld(ctx context.Context, now time.Time) bool {
	events, err := c.db.SleepEvents(ctx, c.startOfDay(now), now.Add(time.Minute))
	if err != nil {
		return false
	}
	for _, e := range events {
		if e.Kind == store.SleepWake {
			return true
		}
	}
	last, ok, err := c.db.LastActivity(ctx, now)
	return err == nil && ok && now.Sub(last) < 20*time.Minute
}

// habitDue reports whether a habit is expected today and not yet logged.
func (c *Coach) habitDue(ctx context.Context, h store.Habit, now time.Time) bool {
	today := habit.Day(now, c.loc)
	logs, err := c.db.HabitLogs(ctx, h.ID, today, today)
	if err != nil {
		return false
	}
	var todayLog *store.HabitLog
	if len(logs) > 0 {
		todayLog = &logs[0]
	}
	return habit.DueToday(h, todayLog, c.doneThisWeek(ctx, h, now), now, c.loc)
}

// doneThisWeek counts completions in the current ISO week (Monday start).
func (c *Coach) doneThisWeek(ctx context.Context, h store.Habit, now time.Time) int {
	l := now.In(c.loc)
	offset := (int(l.Weekday()) + 6) % 7 // days since Monday
	from := l.AddDate(0, 0, -offset).Format("2006-01-02")
	logs, err := c.db.HabitLogs(ctx, h.ID, from, l.Format("2006-01-02"))
	if err != nil {
		return 0
	}
	n := 0
	for _, lg := range logs {
		if lg.Status == store.LogDone || lg.Status == store.LogTiny {
			n++
		}
	}
	return n
}

// consistency computes a habit's consistency as of now.
func (c *Coach) consistency(ctx context.Context, h store.Habit, now time.Time) habit.Consistency {
	today := now.In(c.loc)
	from := today.AddDate(0, 0, -40).Format("2006-01-02")
	logs, err := c.db.HabitLogs(ctx, h.ID, from, today.Format("2006-01-02"))
	if err != nil {
		c.log.WarnContext(ctx, "load habit logs", "habit", h.ID, "err", err)
	}
	up, err := c.db.ShowingUpDays(ctx, h.ID)
	if err != nil {
		c.log.WarnContext(ctx, "count showing-up days", "habit", h.ID, "err", err)
	}
	return habit.Compute(h, logs, up, now, c.loc)
}

// --- composing -------------------------------------------------------------------------

func (c *Coach) render(kind string, data map[string]any, avoid string, seed int) (string, string, error) {
	return c.msgs.Render(kind, data, avoid, seed)
}

func (c *Coach) buildMorning(ctx context.Context, avoid string, seed int) (Message, string, error) {
	now := c.clock.Now()
	data := map[string]any{"Sleep": "", "Habit": ""}
	var buttons [][]Button

	// Last night, if there is an estimate the owner has not already confirmed.
	day := habit.Day(now, c.loc)
	if n, err := c.db.GetSleepNight(ctx, day); err == nil && !n.UserConfirmed && n.Confidence != store.ConfExplicit {
		line, _, err := c.render("sleep_line", map[string]any{"Hours": hoursText(sleep.Duration(n))}, "", seed)
		if err != nil {
			return Message{}, "", err
		}
		data["Sleep"] = line
		buttons = [][]Button{{
			{Label: "About right", Data: "s:ok:" + day},
			{Label: "Shorter", Data: "s:sh:" + day},
			{Label: "Longer", Data: "s:lg:" + day},
		}}
	}

	// One habit to keep warm: the one that has been missed most, else the first.
	if h, ok := c.habitToKeepWarm(ctx, now); ok {
		data["Habit"] = h.Name
	}

	text, variant, err := c.render(kindMorning, data, avoid, seed)
	return Message{Text: text, Buttons: buttons}, variant, err
}

func (c *Coach) habitToKeepWarm(ctx context.Context, now time.Time) (store.Habit, bool) {
	habits, err := c.db.ListHabits(ctx, false)
	if err != nil {
		return store.Habit{}, false
	}
	var best store.Habit
	bestMiss, found := -1, false
	for _, h := range habits {
		if !c.habitDue(ctx, h, now) {
			continue
		}
		if miss := c.consistency(ctx, h, now).MissedInARow; miss > bestMiss {
			best, bestMiss, found = h, miss, true
		}
	}
	return best, found
}

func (c *Coach) buildHabit(ctx context.Context, h store.Habit, avoid string, seed int) (Message, string, error) {
	avoidFor := func(kind string) string {
		if k, v, ok := strings.Cut(avoid, "/"); ok {
			if k == kind {
				return v
			}
			return ""
		}
		if kind == "habit_prompt" {
			return avoid
		}
		return ""
	}
	cons := c.consistency(ctx, h, c.clock.Now())
	data := map[string]any{"Habit": h.Name, "Anchor": h.Anchor, "Behavior": h.Behavior, "Tiny": h.TinyVersion}

	switch {
	case cons.MissedInARow >= 2:
		text, variant, err := c.render("lapse_repeat", data, avoidFor("lapse_repeat"), seed)
		return Message{Text: text, Buttons: [][]Button{{
			{Label: "Make it smaller", Data: "h:r:" + h.ID},
			{Label: "Pause it", Data: "h:p:" + h.ID},
			{Label: "Keep going", Data: "h:k:" + h.ID},
		}}}, "lapse_repeat/" + variant, err

	case cons.MissedInARow == 1:
		text, variant, err := c.render("lapse_first", data, avoidFor("lapse_first"), seed)
		return Message{Text: text, Buttons: [][]Button{{
			{Label: "Full version", Data: "h:d:" + h.ID},
			{Label: "Small version", Data: "h:t:" + h.ID},
			{Label: "Not today", Data: "h:s:" + h.ID},
		}}}, "lapse_first/" + variant, err
	}

	text, variant, err := c.render("habit_prompt", data, avoidFor("habit_prompt"), seed)
	return Message{Text: text, Buttons: [][]Button{{
		{Label: "Done", Data: "h:d:" + h.ID},
		{Label: "Small version", Data: "h:t:" + h.ID},
		{Label: "Not today", Data: "h:s:" + h.ID},
	}}}, variant, err
}

func (c *Coach) buildEvening(ctx context.Context, avoid string, seed int) (Message, string, error) {
	now := c.clock.Now()
	habits, _ := c.db.ListHabits(ctx, false)

	var rows [][]Button
	for _, h := range habits {
		if len(rows) >= 3 {
			break
		}
		if c.habitDue(ctx, h, now) {
			rows = append(rows, []Button{{Label: h.Name + ": done", Data: "h:d:" + h.ID}, {Label: "Small", Data: "h:t:" + h.ID}})
		}
	}
	kind := "evening"
	if len(rows) == 0 {
		kind = "evening_none"
		rows = append(rows, []Button{{Label: "Good day", Data: "e:g"}})
	}
	rows = append(rows, []Button{{Label: "Rough day", Data: "e:r"}})

	text, variant, err := c.render(kind, map[string]any{}, avoid, seed)
	return Message{Text: text, Buttons: rows}, variant, err
}

func (c *Coach) buildWinddown(_ context.Context, avoid string, seed int) (Message, string, error) {
	text, variant, err := c.render("winddown", map[string]any{}, avoid, seed)
	return Message{Text: text}, variant, err
}

func (c *Coach) buildBackoff(_ context.Context, avoid string, seed int) (Message, string, error) {
	text, variant, err := c.render("backoff", map[string]any{}, avoid, seed)
	return Message{Text: text, Buttons: [][]Button{{
		{Label: "Yes, less", Data: "b:less"},
		{Label: "Keep as is", Data: "b:keep"},
	}}}, variant, err
}

func (c *Coach) buildWeekly(ctx context.Context, avoid string, seed int) (Message, string, error) {
	now := c.clock.Now()
	summary, suggestion := c.weeklyFacts(ctx, now)
	text, variant, err := c.render("weekly", map[string]any{"Summary": summary, "Suggestion": suggestion}, avoid, seed)
	return Message{Text: text, Buttons: [][]Button{{{Label: "Adjust something", Data: "w:adj"}}}}, variant, err
}

// weeklyFacts describes the week in plain, kind words and picks at most one idea.
func (c *Coach) weeklyFacts(ctx context.Context, now time.Time) (summary, suggestion string) {
	habits, _ := c.db.ListHabits(ctx, false)

	var parts []string
	lowest, lowestRate := "", 2.0
	for _, h := range habits {
		if h.State != store.HabitActive {
			continue
		}
		cons := c.consistency(ctx, h, now)
		if cons.Expected7 == 0 {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s: %d of %d days.", h.Name, cons.Done7, cons.Expected7))
		if cons.Rate7 < lowestRate {
			lowest, lowestRate = h.Name, cons.Rate7
		}
	}

	nights := c.nights(ctx, now)
	if len(nights) > 7 {
		nights = nights[:7]
	}
	sleepSentence := ""
	if len(nights) >= 3 {
		s := sleep.Summarize(nights, c.cfg.SleepTarget, c.loc)
		sleepSentence = "Sleep averaged " + hoursText(s.AvgDuration) + "."
	}

	switch {
	case len(parts) > 0:
		summary = strings.Join(parts, " ")
	case sleepSentence == "":
		summary = "There is not much to look at yet, and that is fine."
	}
	if sleepSentence != "" {
		if summary != "" {
			summary += " "
		}
		summary += sleepSentence
	}

	if lowestRate < 0.5 {
		suggestion = lowest + " has been the hardest to fit. A smaller version or a different time might help."
	}
	return summary, suggestion
}
