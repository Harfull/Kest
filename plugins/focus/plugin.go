// Package focus helps the owner pick one to three priorities for the day and
// work in timed stretches: it keeps the day's list, runs a focus timer that
// tells the owner when time is up, and logs finished sessions.
//
// A timer is something the owner asked for at a specific time, so its end
// message is urgent like a reminder. Everything else the plugin says comes from
// the owner's own words.
package focus

import (
	"context"
	_ "embed" // manifest.toml
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/Harfull/Kest/internal/plugin"
	"github.com/Harfull/Kest/plugins/internal/logbook"
)

//go:embed manifest.toml
var manifestSrc string

var manifest = plugin.MustParseManifest(manifestSrc)

const (
	jobEnd        = "focus_end"
	activeKey     = "active"
	maxPriorities = 3
	minMinutes    = 5
	maxMinutes    = 120
	defaultMins   = 25
	// staleAfter is how long past its end a timer is kept; it matches the
	// misfire window of the end job.
	staleAfter = 35 * time.Minute
)

type priority struct {
	Text string `json:"text"`
	Done bool   `json:"done,omitempty"`
}

// active is the running timer.
type active struct {
	Task    string    `json:"task"`
	Started time.Time `json:"started"`
	Minutes int       `json:"minutes"`
	JobID   string    `json:"job_id"`
}

// block is one finished stretch of focus.
type block struct {
	Task    string    `json:"task"`
	Minutes int       `json:"minutes"` // time actually spent
	Planned int       `json:"planned"`
	Full    bool      `json:"full"` // ran to the end
	At      time.Time `json:"at"`
}

// Plugin implements plugin.Plugin, plugin.JobProvider, plugin.ContextProvider
// and plugin.Digester.
type Plugin struct {
	host   plugin.Host
	prios  logbook.Book[priority]
	blocks logbook.Book[block]
}

// New returns a fresh plugin instance.
func New() plugin.Plugin { return &Plugin{} }

// Manifest implements plugin.Plugin.
func (p *Plugin) Manifest() plugin.Manifest { return manifest }

// Init implements plugin.Plugin.
func (p *Plugin) Init(_ context.Context, h plugin.Host) error {
	p.host = h
	p.prios = logbook.Book[priority]{KV: h.KV(), Prefix: "prio/"}
	p.blocks = logbook.Book[block]{KV: h.KV(), Prefix: "blocks/"}
	return nil
}

// Close implements plugin.Plugin.
func (p *Plugin) Close(context.Context) error { return nil }

// Tools implements plugin.Plugin.
func (p *Plugin) Tools() []plugin.Tool {
	schema := func(props, required string) json.RawMessage {
		return json.RawMessage(`{"type":"object","properties":{` + props + `},"required":[` + required + `],"additionalProperties":false}`)
	}
	return []plugin.Tool{
		{
			Name: "set_priorities", Risk: plugin.RiskWrite, Handler: p.setPriorities,
			Description: "Set today's one to three priorities, in the owner's words, replacing the list. Keep them few: the point is to choose.",
			Schema:      schema(`"items":{"type":"array","items":{"type":"string","maxLength":120},"minItems":1,"maxItems":3}`, `"items"`),
		},
		{
			Name: "list_priorities", Risk: plugin.RiskRead, Handler: p.listPriorities,
			Description: "Show today's priorities and which are done.",
			Schema:      schema("", ""),
		},
		{
			Name: "complete_priority", Risk: plugin.RiskWrite, Handler: p.complete,
			Description: "Mark one of today's priorities done, by its number from list_priorities.",
			Schema:      schema(`"number":{"type":"integer","minimum":1,"maximum":3}`, `"number"`),
		},
		{
			Name: "start_focus", Risk: plugin.RiskWrite, Handler: p.start,
			Description: "Start a focus timer for a task (5 to 120 minutes, default 25). The owner is told when time is up. Use when they say they are starting work or ask for a timer.",
			Schema:      schema(`"task":{"type":"string","maxLength":120},"minutes":{"type":"integer","minimum":5,"maximum":120}`, `"task"`),
		},
		{
			Name: "stop_focus", Risk: plugin.RiskWrite, Handler: p.stop,
			Description: "End the running focus timer early and log the time spent.",
			Schema:      schema("", ""),
		},
		{
			Name: "focus_summary", Risk: plugin.RiskRead, Handler: p.summary,
			Description: "Report focus time today and this week: stretches and minutes. Describe it plainly; never compare or grade.",
			Schema:      schema("", ""),
		},
	}
}

func decode(raw json.RawMessage, v any) error {
	d := json.NewDecoder(strings.NewReader(string(raw)))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return fmt.Errorf("invalid arguments: %w", err)
	}
	return nil
}

func tidy(s string, n int) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		s = string(r[:n])
	}
	return s
}

func (p *Plugin) today() string { return logbook.Day(p.host.Clock().Now(), p.host.Location()) }

// --- priorities ----------------------------------------------------------------------

func (p *Plugin) setPriorities(ctx context.Context, call plugin.Call) (plugin.Result, error) {
	var a struct {
		Items []string `json:"items"`
	}
	if err := decode(call.Args, &a); err != nil {
		return plugin.Result{}, err
	}
	var items []priority
	for _, s := range a.Items {
		if s = tidy(s, 120); s != "" {
			items = append(items, priority{Text: s})
		}
	}
	if len(items) == 0 || len(items) > maxPriorities {
		return plugin.Result{}, fmt.Errorf("give one to %d priorities", maxPriorities)
	}
	if err := p.prios.Save(ctx, p.today(), items); err != nil {
		return plugin.Result{}, err
	}
	return plugin.Result{Content: fmt.Sprintf("Saved %d priorit(ies) for today.", len(items)), Summary: "set priorities"}, nil
}

func formatPriorities(items []priority) string {
	var b strings.Builder
	for i, it := range items {
		mark := ""
		if it.Done {
			mark = " (done)"
		}
		fmt.Fprintf(&b, "%d. %s%s\n", i+1, it.Text, mark)
	}
	return strings.TrimSpace(b.String())
}

func (p *Plugin) listPriorities(ctx context.Context, _ plugin.Call) (plugin.Result, error) {
	items, err := p.prios.Load(ctx, p.today())
	if err != nil {
		return plugin.Result{}, err
	}
	if len(items) == 0 {
		return plugin.Result{Content: "No priorities set for today.", Summary: "read priorities"}, nil
	}
	return plugin.Result{Content: formatPriorities(items), Summary: "read priorities"}, nil
}

func (p *Plugin) complete(ctx context.Context, call plugin.Call) (plugin.Result, error) {
	var a struct {
		Number int `json:"number"`
	}
	if err := decode(call.Args, &a); err != nil {
		return plugin.Result{}, err
	}
	day := p.today()
	items, err := p.prios.Load(ctx, day)
	if err != nil {
		return plugin.Result{}, err
	}
	if a.Number < 1 || a.Number > len(items) {
		return plugin.Result{}, errors.New("there is no priority with that number today")
	}
	items[a.Number-1].Done = true
	if err := p.prios.Save(ctx, day, items); err != nil {
		return plugin.Result{}, err
	}
	return plugin.Result{Content: "Marked done: " + items[a.Number-1].Text, Summary: "completed a priority"}, nil
}

// --- timer ---------------------------------------------------------------------------

func (p *Plugin) loadActive(ctx context.Context) (active, bool) {
	raw, err := p.host.KV().Get(ctx, activeKey)
	if err != nil {
		return active{}, false
	}
	var a active
	if json.Unmarshal([]byte(raw), &a) != nil {
		return active{}, false
	}
	// The end job is dropped if it was missed by more than staleAfter (the
	// restart took too long). A timer long past its end with no job to finish it
	// is cleared, so it cannot block the next one.
	if p.host.Clock().Now().After(a.Started.Add(time.Duration(a.Minutes) * time.Minute).Add(staleAfter)) {
		_ = p.host.KV().Delete(ctx, activeKey)
		return active{}, false
	}
	return a, true
}

func (p *Plugin) start(ctx context.Context, call plugin.Call) (plugin.Result, error) {
	var a struct {
		Task    string `json:"task"`
		Minutes int    `json:"minutes"`
	}
	if err := decode(call.Args, &a); err != nil {
		return plugin.Result{}, err
	}
	task := tidy(a.Task, 120)
	if task == "" {
		return plugin.Result{}, errors.New("task is required")
	}
	if a.Minutes == 0 {
		a.Minutes = defaultMins
	}
	if a.Minutes < minMinutes || a.Minutes > maxMinutes {
		return plugin.Result{}, fmt.Errorf("minutes must be between %d and %d", minMinutes, maxMinutes)
	}
	if cur, ok := p.loadActive(ctx); ok {
		return plugin.Result{}, fmt.Errorf("a focus timer for %q is already running; stop it first", cur.Task)
	}
	now := p.host.Clock().Now()
	id, err := p.host.Jobs().Schedule(ctx, plugin.JobSpec{Name: jobEnd, Payload: "", At: now.Add(time.Duration(a.Minutes) * time.Minute), Misfire: "expire_after:30m"})
	if err != nil {
		return plugin.Result{}, fmt.Errorf("schedule the timer: %w", err)
	}
	raw, _ := json.Marshal(active{Task: task, Started: now, Minutes: a.Minutes, JobID: id})
	if err := p.host.KV().Set(ctx, activeKey, string(raw)); err != nil {
		_ = p.host.Jobs().Cancel(ctx, id)
		return plugin.Result{}, err
	}
	end := now.Add(time.Duration(a.Minutes) * time.Minute).In(p.host.Location()).Format("15:04")
	return plugin.Result{Content: fmt.Sprintf("Focus timer started: %s, %d minutes, until %s. The owner will be told when it ends.", task, a.Minutes, end), Summary: "started a focus timer"}, nil
}

// finish logs the running block and clears the timer. It reports what ran.
func (p *Plugin) finish(ctx context.Context, full bool) (active, int, bool, error) {
	cur, ok := p.loadActive(ctx)
	if !ok {
		return active{}, 0, false, nil
	}
	now := p.host.Clock().Now()
	spent := int(now.Sub(cur.Started).Round(time.Minute) / time.Minute)
	if full {
		spent = cur.Minutes
	}
	spent = max(0, min(spent, cur.Minutes))
	if spent > 0 {
		if err := p.blocks.Add(ctx, logbook.Day(cur.Started, p.host.Location()), block{Task: cur.Task, Minutes: spent, Planned: cur.Minutes, Full: full, At: cur.Started}); err != nil {
			return cur, spent, true, err
		}
	}
	if err := p.host.KV().Delete(ctx, activeKey); err != nil {
		return cur, spent, true, err
	}
	return cur, spent, true, nil
}

func (p *Plugin) stop(ctx context.Context, _ plugin.Call) (plugin.Result, error) {
	cur, spent, ok, err := p.finish(ctx, false)
	if err != nil {
		return plugin.Result{}, err
	}
	if !ok {
		return plugin.Result{Content: "No focus timer is running.", Summary: "stopped nothing"}, nil
	}
	if err := p.host.Jobs().Cancel(ctx, cur.JobID); err != nil && !errors.Is(err, plugin.ErrNotFound) {
		return plugin.Result{}, err
	}
	return plugin.Result{Content: fmt.Sprintf("Stopped. About %d minute(s) on %s were logged.", spent, cur.Task), Summary: "stopped the focus timer"}, nil
}

// JobNames implements plugin.JobProvider.
func (p *Plugin) JobNames() []string { return []string{jobEnd} }

// RunJob implements plugin.JobProvider: it ends the timer and tells the owner.
func (p *Plugin) RunJob(ctx context.Context, name, _ string) error {
	if name != jobEnd {
		return fmt.Errorf("unknown job %q", name)
	}
	cur, spent, ok, err := p.finish(ctx, true)
	if err != nil {
		return err
	}
	if !ok {
		return nil // stopped early; nothing to announce
	}
	text := fmt.Sprintf("Your %d minutes on %s are up. Take a minute to stretch, then decide the next small step.", spent, cur.Task)
	return p.host.Notify(ctx, plugin.Notification{Text: text, Urgent: true})
}

func (p *Plugin) summary(ctx context.Context, _ plugin.Call) (plugin.Result, error) {
	now, loc := p.host.Clock().Now(), p.host.Location()
	week, err := p.blocks.Range(ctx, logbook.Day(logbook.WeekStart(now, loc), loc), logbook.Day(now, loc))
	if err != nil {
		return plugin.Result{}, err
	}
	count := func(days ...string) (n, mins int) {
		for _, d := range days {
			for _, b := range week[d] {
				n++
				mins += b.Minutes
			}
		}
		return n, mins
	}
	tn, tm := count(logbook.Day(now, loc))
	var all []string
	for d := range week {
		all = append(all, d)
	}
	wn, wm := count(all...)
	var b strings.Builder
	fmt.Fprintf(&b, "Today: %d stretch(es), %d minutes. This week: %d stretch(es), %d minutes.", tn, tm, wn, wm)
	if cur, ok := p.loadActive(ctx); ok {
		left := max(0, cur.Minutes-int(now.Sub(cur.Started)/time.Minute))
		fmt.Fprintf(&b, "\nRunning now: %s, about %d minute(s) left.", cur.Task, left)
	}
	return plugin.Result{Content: b.String(), Summary: "read focus summary"}, nil
}

// PromptContext implements plugin.ContextProvider.
func (p *Plugin) PromptContext(ctx context.Context) string {
	var b strings.Builder
	if items, err := p.prios.Load(ctx, p.today()); err == nil && len(items) > 0 {
		b.WriteString("- Today's priorities: " + strings.ReplaceAll(formatPriorities(items), "\n", " ") + "\n")
	}
	if cur, ok := p.loadActive(ctx); ok {
		end := cur.Started.Add(time.Duration(cur.Minutes) * time.Minute).In(p.host.Location()).Format("15:04")
		fmt.Fprintf(&b, "- A focus timer is running: %s until %s.\n", cur.Task, end)
	}
	b.WriteString("- Focus: when the owner is planning their day, help them pick one to three priorities (focus__set_priorities); when they start work, offer a timer (focus__start_focus). Keep it light.\n")
	return b.String()
}

// WeeklyDigest implements plugin.Digester.
func (p *Plugin) WeeklyDigest(ctx context.Context) string {
	now, loc := p.host.Clock().Now(), p.host.Location()
	week, err := p.blocks.Range(ctx, logbook.Day(logbook.WeekStart(now, loc), loc), logbook.Day(now, loc))
	if err != nil {
		return ""
	}
	n, mins := 0, 0
	for _, blocks := range week {
		for _, b := range blocks {
			n++
			mins += b.Minutes
		}
	}
	if n == 0 {
		return ""
	}
	return fmt.Sprintf("Focus time this week: %d stretch(es), %d minutes.", n, mins)
}
