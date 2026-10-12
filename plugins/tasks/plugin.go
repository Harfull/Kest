// Package tasks keeps the owner's to-do list.
//
// Tasks live in the plugin's key/value store as one JSON document per task, so
// they are separate from conversation history and survive /forget.
package tasks

import (
	"context"
	_ "embed" // manifest.toml
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Harfull/Kest/internal/plugin"
)

//go:embed manifest.toml
var manifestSrc string

var manifest = plugin.MustParseManifest(manifestSrc)

const (
	keyPrefix = "task/"
	keySeq    = "seq"
	maxTitle  = 200
)

// Plugin implements plugin.Plugin.
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

// task is the stored form.
type task struct {
	ID      int       `json:"id"`
	Title   string    `json:"title"`
	Due     string    `json:"due,omitempty"` // YYYY-MM-DD in the owner's zone
	Done    bool      `json:"done"`
	Created time.Time `json:"created"`
	DoneAt  time.Time `json:"done_at,omitzero"`
}

// Tools implements plugin.Plugin.
func (p *Plugin) Tools() []plugin.Tool {
	return []plugin.Tool{
		{
			Name:        "add_task",
			Description: "Add a task to the owner's to-do list. Optionally give a due date as YYYY-MM-DD.",
			Schema: json.RawMessage(`{"type":"object","properties":{
				"title":{"type":"string","description":"The task, in a few words."},
				"due":{"type":"string","description":"Due date, YYYY-MM-DD in the owner's time zone."}
			},"required":["title"],"additionalProperties":false}`),
			Risk:    plugin.RiskWrite,
			Handler: p.add,
		},
		{
			Name:        "list_tasks",
			Description: "List the owner's tasks with their ids, soonest due first. Open tasks only unless include_done is true.",
			Schema:      json.RawMessage(`{"type":"object","properties":{"include_done":{"type":"boolean"}},"additionalProperties":false}`),
			Risk:        plugin.RiskRead,
			Handler:     p.list,
		},
		{
			Name:        "complete_task",
			Description: "Mark a task as done, by its id.",
			Schema:      json.RawMessage(`{"type":"object","properties":{"id":{"type":"integer"}},"required":["id"],"additionalProperties":false}`),
			Risk:        plugin.RiskWrite,
			Handler:     p.complete,
		},
		{
			Name:        "delete_task",
			Description: "Delete a task by its id. Prefer complete_task unless the owner wants it gone.",
			Schema:      json.RawMessage(`{"type":"object","properties":{"id":{"type":"integer"}},"required":["id"],"additionalProperties":false}`),
			Risk:        plugin.RiskWrite,
			Handler:     p.remove,
		},
	}
}

func (p *Plugin) all(ctx context.Context) ([]task, error) {
	items, err := p.host.KV().List(ctx, keyPrefix)
	if err != nil {
		return nil, err
	}
	out := make([]task, 0, len(items))
	for _, it := range items {
		var t task
		if err := json.Unmarshal([]byte(it.Value), &t); err != nil {
			p.host.Logger().WarnContext(ctx, "skipping unreadable task", "key", it.Key)
			continue
		}
		out = append(out, t)
	}
	return out, nil
}

func (p *Plugin) get(ctx context.Context, id int) (task, error) {
	raw, err := p.host.KV().Get(ctx, keyPrefix+strconv.Itoa(id))
	if err != nil {
		if errors.Is(err, plugin.ErrNotFound) {
			return task{}, fmt.Errorf("no task with id %d", id)
		}
		return task{}, err
	}
	var t task
	if err := json.Unmarshal([]byte(raw), &t); err != nil {
		return task{}, fmt.Errorf("task %d is unreadable", id)
	}
	return t, nil
}

func (p *Plugin) put(ctx context.Context, t task) error {
	raw, err := json.Marshal(t)
	if err != nil {
		return err
	}
	return p.host.KV().Set(ctx, keyPrefix+strconv.Itoa(t.ID), string(raw))
}

// nextID returns an increasing task id. The plugin is called one turn at a
// time, so a read-modify-write on the counter is safe.
func (p *Plugin) nextID(ctx context.Context) (int, error) {
	n := 0
	if v, err := p.host.KV().Get(ctx, keySeq); err == nil {
		n, _ = strconv.Atoi(v)
	} else if !errors.Is(err, plugin.ErrNotFound) {
		return 0, err
	}
	n++
	return n, p.host.KV().Set(ctx, keySeq, strconv.Itoa(n))
}

func (p *Plugin) add(ctx context.Context, call plugin.Call) (plugin.Result, error) {
	var a struct {
		Title string `json:"title"`
		Due   string `json:"due"`
	}
	if err := json.Unmarshal(call.Args, &a); err != nil {
		return plugin.Result{}, errors.New("arguments must be an object with a title")
	}
	title := strings.TrimSpace(a.Title)
	if title == "" {
		return plugin.Result{}, errors.New("title is required")
	}
	if len([]rune(title)) > maxTitle {
		return plugin.Result{}, fmt.Errorf("title is too long (limit %d characters)", maxTitle)
	}
	due := strings.TrimSpace(a.Due)
	if due != "" {
		if _, err := time.Parse("2006-01-02", due); err != nil {
			return plugin.Result{}, fmt.Errorf("due date %q must look like 2026-10-12", due)
		}
	}

	existing, err := p.all(ctx)
	if err != nil {
		return plugin.Result{}, err
	}
	open := 0
	for _, t := range existing {
		if !t.Done {
			open++
		}
	}
	if limit := p.host.Config().Int("max_open"); limit > 0 && open >= limit {
		return plugin.Result{}, fmt.Errorf("there are already %d open tasks; finish some first", open)
	}

	id, err := p.nextID(ctx)
	if err != nil {
		return plugin.Result{}, err
	}
	t := task{ID: id, Title: title, Due: due, Created: p.host.Clock().Now()}
	if err := p.put(ctx, t); err != nil {
		return plugin.Result{}, err
	}
	msg := fmt.Sprintf("Added task %d: %s", id, title)
	if due != "" {
		msg += " (due " + due + ")"
	}
	return plugin.Result{Content: msg, Summary: "added task " + strconv.Itoa(id)}, nil
}

func (p *Plugin) list(ctx context.Context, call plugin.Call) (plugin.Result, error) {
	var a struct {
		IncludeDone bool `json:"include_done"`
	}
	_ = json.Unmarshal(call.Args, &a)

	tasks, err := p.all(ctx)
	if err != nil {
		return plugin.Result{}, err
	}
	var shown []task
	for _, t := range tasks {
		if a.IncludeDone || !t.Done {
			shown = append(shown, t)
		}
	}
	if len(shown) == 0 {
		return plugin.Result{Content: "No tasks.", Summary: "listed 0 tasks"}, nil
	}

	// Due soonest first; undated tasks last; ties by id.
	sort.Slice(shown, func(i, j int) bool {
		a, b := shown[i], shown[j]
		switch {
		case a.Due != b.Due && a.Due == "":
			return false
		case a.Due != b.Due && b.Due == "":
			return true
		case a.Due != b.Due:
			return a.Due < b.Due
		}
		return a.ID < b.ID
	})

	var b strings.Builder
	for _, t := range shown {
		mark := "[ ]"
		if t.Done {
			mark = "[x]"
		}
		line := fmt.Sprintf("%s %d: %s", mark, t.ID, t.Title)
		if t.Due != "" {
			line += " (due " + t.Due + ")"
		}
		b.WriteString(line + "\n")
	}
	return plugin.Result{Content: strings.TrimSpace(b.String()), Summary: fmt.Sprintf("listed %d tasks", len(shown))}, nil
}

func idArg(raw json.RawMessage) (int, error) {
	var a struct {
		ID int `json:"id"`
	}
	if err := json.Unmarshal(raw, &a); err != nil || a.ID <= 0 {
		return 0, errors.New("a positive integer id is required")
	}
	return a.ID, nil
}

func (p *Plugin) complete(ctx context.Context, call plugin.Call) (plugin.Result, error) {
	id, err := idArg(call.Args)
	if err != nil {
		return plugin.Result{}, err
	}
	t, err := p.get(ctx, id)
	if err != nil {
		return plugin.Result{}, err
	}
	if t.Done {
		return plugin.Result{Content: fmt.Sprintf("Task %d was already done.", id), Summary: "task already done"}, nil
	}
	t.Done, t.DoneAt = true, p.host.Clock().Now()
	if err := p.put(ctx, t); err != nil {
		return plugin.Result{}, err
	}
	return plugin.Result{Content: fmt.Sprintf("Marked task %d done: %s", id, t.Title), Summary: "completed task " + strconv.Itoa(id)}, nil
}

func (p *Plugin) remove(ctx context.Context, call plugin.Call) (plugin.Result, error) {
	id, err := idArg(call.Args)
	if err != nil {
		return plugin.Result{}, err
	}
	t, err := p.get(ctx, id)
	if err != nil {
		return plugin.Result{}, err
	}
	if err := p.host.KV().Delete(ctx, keyPrefix+strconv.Itoa(id)); err != nil {
		return plugin.Result{}, err
	}
	return plugin.Result{Content: fmt.Sprintf("Deleted task %d: %s", id, t.Title), Summary: "deleted task " + strconv.Itoa(id)}, nil
}
