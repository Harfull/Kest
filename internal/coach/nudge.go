package coach

import (
	"context"
	"fmt"
	"strings"

	"github.com/Harfull/Kest/internal/coach/budget"
	"github.com/Harfull/Kest/internal/store"
)

// NudgeKind is the notification kind for a message a plugin wants to send on
// its own, such as "an email you were waiting for arrived". The text after the
// colon names the plugin.
const NudgeKind = "nudge"

// Nudge sends an unprompted message from a plugin, but only if the notification
// budget allows it: not in quiet hours, not over the daily cap, not too soon
// after the last message and not while the owner is ignoring messages. It
// reports whether the message went out. A held-back nudge is dropped, not
// queued: a late "new email" is worse than none.
func (c *Coach) Nudge(ctx context.Context, source, text string) (bool, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return false, nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	now := c.clock.Now()
	prefs := c.prefs(ctx)
	policy := budget.New(c.dailyCap(prefs), c.loc)
	history, err := c.db.Notifications(ctx, now.AddDate(0, 0, -8))
	if err != nil {
		return false, fmt.Errorf("load history: %w", err)
	}
	kind := NudgeKind + ":" + source
	if d := policy.Allow(now, kind, budget.Low, history, c.model(ctx, now).InQuiet(now)); !d.OK {
		c.log.DebugContext(ctx, "nudge held back", "source", source, "reason", d.Reason)
		return false, nil
	}
	if err := c.out.Send(ctx, Message{Text: text}); err != nil {
		return false, fmt.Errorf("send nudge: %w", err)
	}
	if _, err := c.db.AddNotification(ctx, store.Notification{TS: now, Kind: kind}); err != nil {
		return true, fmt.Errorf("record nudge: %w", err)
	}
	return true, nil
}
