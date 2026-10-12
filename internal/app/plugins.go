package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Harfull/Kest/internal/agent"
	"github.com/Harfull/Kest/internal/plugin"
	"github.com/Harfull/Kest/internal/scheduler"
	"github.com/Harfull/Kest/internal/store"
	"github.com/Harfull/Kest/internal/telegram"
)

// confirmTimeout is how long a turn waits for the owner to answer an approval
// prompt. The conversation queue is blocked meanwhile, so it is short.
const confirmTimeout = 2 * time.Minute

// secretPrefix is prepended to a plugin secret's name to find its environment
// variable: a manifest secret NOTION_TOKEN is read from KEST_SECRET_NOTION_TOKEN.
const secretPrefix = "KEST_SECRET_"

// kvAdapter exposes a store.KV as a plugin.KV.
type kvAdapter struct{ kv *store.KV }

func (a kvAdapter) Get(ctx context.Context, key string) (string, error) {
	v, err := a.kv.Get(ctx, key)
	if errors.Is(err, store.ErrNotFound) {
		return "", plugin.ErrNotFound
	}
	return v, err
}

func (a kvAdapter) Set(ctx context.Context, key, value string) error {
	return a.kv.Set(ctx, key, value)
}

func (a kvAdapter) Delete(ctx context.Context, key string) error { return a.kv.Delete(ctx, key) }

func (a kvAdapter) List(ctx context.Context, prefix string) ([]plugin.KVItem, error) {
	items, err := a.kv.List(ctx, prefix)
	if err != nil {
		return nil, err
	}
	out := make([]plugin.KVItem, len(items))
	for i, it := range items {
		out[i] = plugin.KVItem{Key: it.Key, Value: it.Value}
	}
	return out, nil
}

// jobsAdapter exposes the scheduler to one plugin. Jobs are owned by
// "plugin:<id>" and named "<id>/<job>", and a plugin can only list or cancel
// its own.
type jobsAdapter struct {
	s    *scheduler.Scheduler
	id   string
	zone string
}

func (a jobsAdapter) owner() string { return "plugin:" + a.id }

func (a jobsAdapter) Schedule(ctx context.Context, spec plugin.JobSpec) (string, error) {
	misfire, err := scheduler.ParseMisfire(spec.Misfire)
	if err != nil {
		return "", err
	}
	return a.s.Schedule(ctx, scheduler.Spec{
		Owner:   a.owner(),
		Name:    a.id + "/" + spec.Name,
		Payload: spec.Payload,
		Rule:    spec.Rule,
		Zone:    a.zone,
		At:      spec.At,
		Misfire: misfire,
	})
}

func (a jobsAdapter) Cancel(ctx context.Context, id string) error {
	j, err := a.s.Get(ctx, id)
	if err != nil || j.Owner != a.owner() {
		// A job that belongs to someone else looks the same as a missing one.
		return plugin.ErrNotFound
	}
	if err := a.s.Cancel(ctx, id); errors.Is(err, store.ErrNotFound) {
		return plugin.ErrNotFound
	} else if err != nil {
		return err
	}
	return nil
}

func (a jobsAdapter) List(ctx context.Context) ([]plugin.JobInfo, error) {
	jobs, err := a.s.ListOwned(ctx, a.owner())
	if err != nil {
		return nil, err
	}
	out := make([]plugin.JobInfo, len(jobs))
	for i, j := range jobs {
		out[i] = plugin.JobInfo{
			ID:      j.ID,
			Name:    strings.TrimPrefix(j.Name, a.id+"/"),
			Payload: j.Payload,
			Rule:    j.Rule,
			NextRun: j.NextRunAt,
		}
	}
	return out, nil
}

// approvalPrompt renders a confirmation request. The text is built from the
// structured call, never from model prose.
func approvalPrompt(p agent.Prompt) string {
	var b strings.Builder
	fmt.Fprintf(&b, "I'd like to run **%s**:\n\n%s\n\n", p.Tool, p.Summary)
	if p.Reason != "" {
		fmt.Fprintf(&b, "I'm asking because %s.\n", p.Reason)
	}
	b.WriteString("Go ahead?")
	return b.String()
}

// askOwner shows an approval prompt with Go ahead and Skip buttons.
func (c *chat) askOwner(ctx context.Context, p agent.Prompt) (int, error) {
	return c.tr.Send(ctx, telegram.Reply{
		ChatID: c.ownerChat,
		Text:   approvalPrompt(p),
		Buttons: [][]telegram.Button{{
			{Label: "Go ahead", Data: agent.ApproveData + p.ID},
			{Label: "Skip", Data: agent.DeclineData + p.ID},
		}},
	})
}

// settled removes the buttons once a prompt is decided, and says so when it
// timed out, because the owner may not have seen it.
func (c *chat) settled(ctx context.Context, msgID int, outcome string) {
	c.clearButtons(ctx, c.ownerChat, msgID)
	if outcome == store.PendingExpired {
		c.say(ctx, c.ownerChat, msgApprovalTimedOut)
	}
}

// notify sends a plugin notification to the owner.
func (c *chat) notify(ctx context.Context, n plugin.Notification) error {
	_, err := c.tr.Send(ctx, telegram.Reply{ChatID: c.ownerChat, Text: n.Text})
	return err
}
