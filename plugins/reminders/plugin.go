// Package reminders lets the owner ask Kest to remind them of something at a
// specific time, once or on a schedule.
//
// Reminders are persistent scheduler jobs, so they survive restarts. A reminder
// the owner asked for is delivered even during quiet hours, because they chose
// the time.
package reminders

import (
	"context"
	_ "embed" // manifest.toml
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Harfull/Kest/internal/plugin"
)

//go:embed manifest.toml
var manifestSrc string

var manifest = plugin.MustParseManifest(manifestSrc)

const (
	jobFire = "fire"
	// maxText bounds a reminder's text.
	maxText = 300
	// repeatGrace is how late a repeating reminder may fire before it is
	// skipped; a stale "take your medication" is worse than none.
	repeatGrace = "expire_after:2h"
)

// Plugin implements plugin.Plugin and plugin.JobProvider.
type Plugin struct {
	host plugin.Host
}

// New returns a fresh plugin instance.
func New() plugin.Plugin { return &Plugin{} }

// Manifest implements plugin.Plugin.
func (p *Plugin) Manifest() plugin.Manifest { return manifest }

// Init implements plugin.Plugin.
func (p *Plugin) Init(_ context.Context, h plugin.Host) error {
	p.host = h
	return nil
}

// Close implements plugin.Plugin.
func (p *Plugin) Close(context.Context) error { return nil }

// JobNames implements plugin.JobProvider.
func (p *Plugin) JobNames() []string { return []string{jobFire} }

// payload is what a reminder job carries.
type payload struct {
	Text string `json:"text"`
}

// RunJob implements plugin.JobProvider: it delivers a reminder.
func (p *Plugin) RunJob(ctx context.Context, name, raw string) error {
	if name != jobFire {
		return fmt.Errorf("unknown job %q", name)
	}
	var pl payload
	if err := json.Unmarshal([]byte(raw), &pl); err != nil || strings.TrimSpace(pl.Text) == "" {
		return errors.New("reminder has no text")
	}
	return p.host.Notify(ctx, plugin.Notification{Text: "Reminder: " + pl.Text, Urgent: true})
}

// Tools implements plugin.Plugin.
func (p *Plugin) Tools() []plugin.Tool {
	return []plugin.Tool{
		{
			Name: "create_reminder",
			Description: "Set a reminder. Give the text and either a local date and time in `at` (like 2026-10-12T09:00) for a one-off reminder, " +
				"or a `repeat` rule: daily@07:30, weekdays@08:00, weekly:mon,thu@18:00 or every:2h. Work out `at` from the current local time.",
			Schema: json.RawMessage(`{"type":"object","properties":{
				"text":{"type":"string","description":"What to remind the owner of, in a few words."},
				"at":{"type":"string","description":"Local date and time, e.g. 2026-10-12T09:00."},
				"repeat":{"type":"string","description":"Repeat rule such as daily@07:30. Omit for a one-off reminder."}
			},"required":["text"],"additionalProperties":false}`),
			Risk:    plugin.RiskWrite,
			Handler: p.create,
		},
		{
			Name:        "list_reminders",
			Description: "List the owner's upcoming reminders with their ids.",
			Schema:      json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
			Risk:        plugin.RiskRead,
			Handler:     p.list,
		},
		{
			Name:        "cancel_reminder",
			Description: "Cancel a reminder by its id, as shown by list_reminders.",
			Schema:      json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"}},"required":["id"],"additionalProperties":false}`),
			Risk:        plugin.RiskWrite,
			Handler:     p.cancel,
		},
	}
}

func (p *Plugin) create(ctx context.Context, call plugin.Call) (plugin.Result, error) {
	var a struct {
		Text   string `json:"text"`
		At     string `json:"at"`
		Repeat string `json:"repeat"`
	}
	if err := json.Unmarshal(call.Args, &a); err != nil {
		return plugin.Result{}, errors.New("arguments must be an object with text and at or repeat")
	}
	text := strings.TrimSpace(a.Text)
	if text == "" {
		return plugin.Result{}, errors.New("text is required")
	}
	if len([]rune(text)) > maxText {
		return plugin.Result{}, fmt.Errorf("text is too long (limit %d characters)", maxText)
	}
	if strings.TrimSpace(a.At) == "" && strings.TrimSpace(a.Repeat) == "" {
		return plugin.Result{}, errors.New("give either at (a date and time) or repeat (a rule)")
	}

	existing, err := p.host.Jobs().List(ctx)
	if err != nil {
		return plugin.Result{}, err
	}
	if limit := p.host.Config().Int("max_active"); limit > 0 && len(existing) >= limit {
		return plugin.Result{}, fmt.Errorf("there are already %d active reminders; cancel some first", len(existing))
	}

	body, _ := json.Marshal(payload{Text: text})
	spec := plugin.JobSpec{Name: jobFire, Payload: string(body)}

	loc, now := p.host.Location(), p.host.Clock().Now()
	if rule := strings.TrimSpace(a.Repeat); rule != "" {
		spec.Rule = rule
		spec.Misfire = repeatGrace
	}
	if at := strings.TrimSpace(a.At); at != "" {
		t, err := parseLocal(at, loc)
		if err != nil {
			return plugin.Result{}, err
		}
		if !t.After(now) {
			return plugin.Result{}, fmt.Errorf("%s has already passed (it is %s now)", t.In(loc).Format("Mon 2 Jan, 15:04"), now.In(loc).Format("Mon 2 Jan, 15:04"))
		}
		spec.At = t
	}

	id, err := p.host.Jobs().Schedule(ctx, spec)
	if err != nil {
		return plugin.Result{}, fmt.Errorf("could not schedule: %w", err)
	}

	when := "per the rule " + spec.Rule
	if !spec.At.IsZero() {
		when = spec.At.In(loc).Format("Mon 2 Jan, 15:04")
		if spec.Rule != "" {
			when += ", then " + spec.Rule
		}
	} else if list, err := p.host.Jobs().List(ctx); err == nil {
		for _, j := range list {
			if j.ID == id {
				when = j.NextRun.In(loc).Format("Mon 2 Jan, 15:04") + ", then " + spec.Rule
			}
		}
	}
	return plugin.Result{
		Content: fmt.Sprintf("Reminder %s set for %s: %s", id, when, text),
		Summary: "created reminder " + id,
	}, nil
}

// parseLocal reads a date and time in the owner's zone. A value that carries
// its own offset (RFC 3339) is honored.
func parseLocal(s string, loc *time.Location) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC(), nil
	}
	for _, layout := range []string{"2006-01-02T15:04", "2006-01-02 15:04", "2006-01-02T15:04:05"} {
		if t, err := time.ParseInLocation(layout, s, loc); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("could not read the time %q; use a local date and time like 2026-10-12T09:00", s)
}

func (p *Plugin) list(ctx context.Context, _ plugin.Call) (plugin.Result, error) {
	jobs, err := p.host.Jobs().List(ctx)
	if err != nil {
		return plugin.Result{}, err
	}
	if len(jobs) == 0 {
		return plugin.Result{Content: "No reminders are set.", Summary: "listed 0 reminders"}, nil
	}
	loc := p.host.Location()
	var b strings.Builder
	for _, j := range jobs {
		var pl payload
		_ = json.Unmarshal([]byte(j.Payload), &pl)
		line := fmt.Sprintf("- %s: %s at %s", j.ID, pl.Text, j.NextRun.In(loc).Format("Mon 2 Jan, 15:04"))
		if j.Rule != "" {
			line += " (repeats " + j.Rule + ")"
		}
		b.WriteString(line + "\n")
	}
	return plugin.Result{Content: strings.TrimSpace(b.String()), Summary: fmt.Sprintf("listed %d reminders", len(jobs))}, nil
}

func (p *Plugin) cancel(ctx context.Context, call plugin.Call) (plugin.Result, error) {
	var a struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(call.Args, &a); err != nil || strings.TrimSpace(a.ID) == "" {
		return plugin.Result{}, errors.New("id is required")
	}
	if err := p.host.Jobs().Cancel(ctx, strings.TrimSpace(a.ID)); err != nil {
		if errors.Is(err, plugin.ErrNotFound) {
			return plugin.Result{}, fmt.Errorf("no reminder with id %s", a.ID)
		}
		return plugin.Result{}, err
	}
	return plugin.Result{Content: "Reminder cancelled.", Summary: "cancelled reminder " + a.ID}, nil
}
