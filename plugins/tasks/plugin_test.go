package tasks_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Harfull/Kest/internal/plugin"
	"github.com/Harfull/Kest/internal/plugin/plugintest"
	"github.com/Harfull/Kest/plugins/tasks"
)

func setup(t *testing.T, cfg plugin.Config) (*tasks.Plugin, *plugintest.Host) {
	t.Helper()
	h := plugintest.NewHost()
	for k, v := range cfg {
		h.Cfg[k] = v
	}
	p := tasks.New().(*tasks.Plugin)
	if err := p.Init(context.Background(), h); err != nil {
		t.Fatal(err)
	}
	return p, h
}

func run(t *testing.T, p *tasks.Plugin, tool, args string) (plugin.Result, error) {
	t.Helper()
	for _, tl := range p.Tools() {
		if tl.Name == tool {
			return tl.Handler(context.Background(), plugin.Call{Args: json.RawMessage(args)})
		}
	}
	t.Fatalf("no tool %s", tool)
	return plugin.Result{}, nil
}

func TestContract(t *testing.T) {
	plugintest.RunContract(t, tasks.New, nil)
}

func TestAddListCompleteDelete(t *testing.T) {
	p, _ := setup(t, nil)

	res, _ := run(t, p, "list_tasks", `{}`)
	if res.Content != "No tasks." {
		t.Errorf("empty = %q", res.Content)
	}

	for _, a := range []string{
		`{"title":"file taxes","due":"2026-10-31"}`,
		`{"title":"buy running shoes"}`,
		`{"title":"book dentist","due":"2026-10-15"}`,
	} {
		if _, err := run(t, p, "add_task", a); err != nil {
			t.Fatalf("add %s: %v", a, err)
		}
	}

	res, _ = run(t, p, "list_tasks", `{}`)
	want := []string{"[ ] 3: book dentist (due 2026-10-15)", "[ ] 1: file taxes (due 2026-10-31)", "[ ] 2: buy running shoes"}
	if got := strings.Split(res.Content, "\n"); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("list order = %q\nwant        %q", got, want)
	}

	if res, err := run(t, p, "complete_task", `{"id":3}`); err != nil || !strings.Contains(res.Content, "book dentist") {
		t.Errorf("complete = %q, %v", res.Content, err)
	}
	if res, _ := run(t, p, "complete_task", `{"id":3}`); !strings.Contains(res.Content, "already done") {
		t.Errorf("completing twice = %q", res.Content)
	}

	res, _ = run(t, p, "list_tasks", `{}`)
	if strings.Contains(res.Content, "dentist") {
		t.Errorf("done task listed by default: %q", res.Content)
	}
	res, _ = run(t, p, "list_tasks", `{"include_done":true}`)
	if !strings.Contains(res.Content, "[x] 3: book dentist") {
		t.Errorf("include_done list = %q", res.Content)
	}

	if _, err := run(t, p, "delete_task", `{"id":1}`); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, p, "delete_task", `{"id":1}`); err == nil {
		t.Error("deleting a missing task succeeded")
	}
	if res, _ := run(t, p, "list_tasks", `{}`); strings.Contains(res.Content, "taxes") {
		t.Errorf("deleted task still listed: %q", res.Content)
	}
}

func TestIDsNeverRepeat(t *testing.T) {
	p, _ := setup(t, nil)
	_, _ = run(t, p, "add_task", `{"title":"a"}`)
	_, _ = run(t, p, "delete_task", `{"id":1}`)
	res, _ := run(t, p, "add_task", `{"title":"b"}`)
	if !strings.Contains(res.Content, "task 2") {
		t.Errorf("id reused after delete: %q", res.Content)
	}
}

func TestValidation(t *testing.T) {
	p, _ := setup(t, plugin.Config{"max_open": 2})

	bad := map[string][2]string{
		"no title":         {"add_task", `{}`},
		"blank title":      {"add_task", `{"title":"  "}`},
		"title too long":   {"add_task", `{"title":"` + strings.Repeat("a", 201) + `"}`},
		"bad due":          {"add_task", `{"title":"x","due":"next friday"}`},
		"complete no id":   {"complete_task", `{}`},
		"complete zero":    {"complete_task", `{"id":0}`},
		"complete unknown": {"complete_task", `{"id":42}`},
		"delete negative":  {"delete_task", `{"id":-1}`},
	}
	for name, c := range bad {
		if _, err := run(t, p, c[0], c[1]); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}

	_, _ = run(t, p, "add_task", `{"title":"one"}`)
	_, _ = run(t, p, "add_task", `{"title":"two"}`)
	if _, err := run(t, p, "add_task", `{"title":"three"}`); err == nil {
		t.Error("max_open not enforced")
	}
	// Finishing a task frees a slot.
	_, _ = run(t, p, "complete_task", `{"id":1}`)
	if _, err := run(t, p, "add_task", `{"title":"three"}`); err != nil {
		t.Errorf("slot not freed after completing a task: %v", err)
	}
}

func TestCorruptEntriesAreSkipped(t *testing.T) {
	p, h := setup(t, nil)
	_, _ = run(t, p, "add_task", `{"title":"good"}`)
	_ = h.Store.Set(context.Background(), "task/99", "not json")

	res, err := run(t, p, "list_tasks", `{}`)
	if err != nil || !strings.Contains(res.Content, "good") {
		t.Errorf("list = %q, %v; one bad row must not hide the rest", res.Content, err)
	}
}

func TestTimestampsUseTheClock(t *testing.T) {
	p, h := setup(t, nil)
	_, _ = run(t, p, "add_task", `{"title":"x"}`)
	raw, _ := h.Store.Get(context.Background(), "task/1")
	if !strings.Contains(raw, plugintest.Start.Format(time.RFC3339)) {
		t.Errorf("created time not from the host clock: %s", raw)
	}
}
