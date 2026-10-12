package app

import (
	"context"
	"fmt"

	"github.com/Harfull/Kest/internal/coach"
	"github.com/Harfull/Kest/internal/scheduler"
	"github.com/Harfull/Kest/internal/telegram"
)

const (
	// coachJob is the scheduler job that lets the coach decide whether a
	// check-in is due.
	coachJob = "coach.tick"
	// coachTickRule is how often it looks. Five minutes keeps check-ins within
	// a few minutes of their time without waking the process needlessly.
	coachTickRule = "every:5m"
)

// coachOutbox delivers the coach's messages to the owner's Telegram chat.
type coachOutbox struct {
	tr   telegram.Transport
	chat int64
}

func (o coachOutbox) Send(ctx context.Context, m coach.Message) error {
	reply := telegram.Reply{ChatID: o.chat, Text: m.Text}
	for _, row := range m.Buttons {
		var out []telegram.Button
		for _, b := range row {
			out = append(out, telegram.Button{Label: b.Label, Data: b.Data})
		}
		reply.Buttons = append(reply.Buttons, out)
	}
	if _, err := o.tr.Send(ctx, reply); err != nil {
		return fmt.Errorf("send coach message: %w", err)
	}
	return nil
}

// coachConfig maps the application config to the coach's.
func (c *chat) coachConfig() coach.Config {
	return coach.Config{
		MaxPerDay:  c.cfg.Coach.MaxProactivePerDay,
		QuietStart: c.cfg.Coach.QuietStart,
		QuietEnd:   c.cfg.Coach.QuietEnd,
	}
}

// coachTick is the scheduler handler. Lockdown silences it.
func (c *chat) coachTick(ctx context.Context, _ scheduler.Job) error {
	if c.agent.Locked() {
		return nil
	}
	return c.coach.Tick(ctx)
}

// ensureCoachJob makes sure the tick job exists. It skips missed runs: a check-in
// that is hours late is worse than none, and the coach does its own staleness checks.
func ensureCoachJob(ctx context.Context, sch *scheduler.Scheduler, zone string) error {
	_, err := sch.Ensure(ctx, scheduler.Spec{
		Owner:   "system",
		Name:    coachJob,
		Rule:    coachTickRule,
		Zone:    zone,
		Misfire: scheduler.MisfireSkip,
	})
	if err != nil {
		return fmt.Errorf("schedule coach: %w", err)
	}
	return nil
}

// noteOwner tells the coach the owner just did something. It runs for every
// update before anything else, so even a slow conversation turn is credited
// with the time the owner actually spoke.
func (c *chat) noteOwner(ctx context.Context) {
	c.coach.RecordActivity(ctx, c.clock.Now())
}

// coachCallback handles a coach button press. It reports whether the data was
// the coach's.
func (c *chat) coachCallback(ctx context.Context, u telegram.Update, ack func(string)) bool {
	reply, ok := c.coach.HandleCallback(ctx, u.Callback.Data)
	if !ok {
		return false
	}
	ack("")
	c.clearButtons(ctx, u.ChatID, u.Callback.MessageID)
	c.say(ctx, u.ChatID, reply.Text)
	return true
}

// settings shows the check-in settings. Changing them is done by asking Kest.
func (c *chat) settings(ctx context.Context, chatID int64) {
	c.say(ctx, chatID, c.coach.Settings(ctx)+"\n\nTo change any of this, just tell me, for example \"fewer messages\" or \"no evening check-in\".")
}

// lockdown stops everything that writes, sends or messages unprompted until the
// process restarts.
func (c *chat) lockdown(ctx context.Context, chatID int64) {
	c.agent.SetLockdown(true)
	c.log.WarnContext(ctx, "lockdown turned on by the owner")
	c.say(ctx, chatID, msgLockdown)
}
