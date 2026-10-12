package reminders_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Harfull/Kest/internal/plugin"
	"github.com/Harfull/Kest/internal/plugin/plugintest"
	"github.com/Harfull/Kest/plugins/reminders"
)

func setup(t *testing.T, cfg plugin.Config) (*reminders.Plugin, *plugintest.Host) {
	t.Helper()
	h := plugintest.NewHost()
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	h.Zone = ny // 14:00 UTC is 10:00 on Sunday 11 October 2026 in New York
	for k, v := range cfg {
		h.Cfg[k] = v
	}
	p := reminders.New().(*reminders.Plugin)
	if err := p.Init(context.Background(), h); err != nil {
		t.Fatal(err)
	}
	return p, h
}

func run(t *testing.T, p *reminders.Plugin, tool, args string) (plugin.Result, error) {
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
	plugintest.RunContract(t, reminders.New, nil)
}

func TestCreateOneOffUsesOwnersZone(t *testing.T) {
	p, h := setup(t, nil)

	res, err := run(t, p, "create_reminder", `{"text":"stretch","at":"2026-10-12T09:00"}`)
	if err != nil {
		t.Fatalf("create error = %v", err)
	}
	if !strings.Contains(res.Content, "Mon 12 Oct, 09:00") || !strings.Contains(res.Content, "stretch") {
		t.Errorf("result = %q", res.Content)
	}

	jobs, _ := h.JobStore.List(context.Background())
	if len(jobs) != 1 {
		t.Fatalf("jobs = %v", jobs)
	}
	if want := time.Date(2026, 10, 12, 13, 0, 0, 0, time.UTC); !jobs[0].NextRun.Equal(want) {
		t.Errorf("scheduled at %v, want %v (09:00 New York)", jobs[0].NextRun, want)
	}
	if jobs[0].Name != "fire" || jobs[0].Rule != "" {
		t.Errorf("job = %+v", jobs[0])
	}
}

func TestCreateAcceptsOtherTimeFormats(t *testing.T) {
	p, h := setup(t, nil)
	for _, at := range []string{"2026-10-12 09:00", "2026-10-12T09:00:00", "2026-10-12T13:00:00Z"} {
		if _, err := run(t, p, "create_reminder", `{"text":"x","at":"`+at+`"}`); err != nil {
			t.Errorf("at=%q: %v", at, err)
		}
	}
	jobs, _ := h.JobStore.List(context.Background())
	for _, j := range jobs {
		if !j.NextRun.Equal(time.Date(2026, 10, 12, 13, 0, 0, 0, time.UTC)) {
			t.Errorf("job at %v", j.NextRun)
		}
	}
}

func TestCreateRepeating(t *testing.T) {
	p, h := setup(t, nil)

	if _, err := run(t, p, "create_reminder", `{"text":"drink water","repeat":"every:2h"}`); err != nil {
		t.Fatal(err)
	}
	jobs, _ := h.JobStore.List(context.Background())
	if len(jobs) != 1 || jobs[0].Rule != "every:2h" {
		t.Fatalf("jobs = %+v", jobs)
	}
}

func TestCreateValidation(t *testing.T) {
	p, h := setup(t, plugin.Config{"max_active": 1})

	bad := map[string]string{
		"missing text":       `{"at":"2026-10-12T09:00"}`,
		"blank text":         `{"text":"  ","at":"2026-10-12T09:00"}`,
		"no time at all":     `{"text":"x"}`,
		"time in the past":   `{"text":"x","at":"2026-10-11T09:00"}`,
		"unreadable time":    `{"text":"x","at":"tomorrow morning"}`,
		"text too long":      `{"text":"` + strings.Repeat("a", 301) + `","at":"2026-10-12T09:00"}`,
		"arguments not json": `[1]`,
	}
	for name, args := range bad {
		if _, err := run(t, p, "create_reminder", args); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if jobs, _ := h.JobStore.List(context.Background()); len(jobs) != 0 {
		t.Errorf("invalid requests left %d jobs behind", len(jobs))
	}

	if _, err := run(t, p, "create_reminder", `{"text":"one","at":"2026-10-12T09:00"}`); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, p, "create_reminder", `{"text":"two","at":"2026-10-12T10:00"}`); err == nil {
		t.Error("max_active limit not enforced")
	}
}

func TestPastErrorMentionsLocalTimes(t *testing.T) {
	p, _ := setup(t, nil)
	_, err := run(t, p, "create_reminder", `{"text":"x","at":"2026-10-11T09:00"}`)
	if err == nil || !strings.Contains(err.Error(), "already passed") || !strings.Contains(err.Error(), "10:00") {
		t.Errorf("error = %v; it should tell the model what time it is now", err)
	}
}

func TestListAndCancel(t *testing.T) {
	p, _ := setup(t, nil)

	res, _ := run(t, p, "list_reminders", `{}`)
	if res.Content != "No reminders are set." {
		t.Errorf("empty list = %q", res.Content)
	}

	_, _ = run(t, p, "create_reminder", `{"text":"call mum","at":"2026-10-12T18:30"}`)
	_, _ = run(t, p, "create_reminder", `{"text":"stretch","at":"2026-10-12T09:00","repeat":"daily@09:00"}`)

	res, err := run(t, p, "list_reminders", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(res.Content, "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], "stretch") || !strings.Contains(lines[0], "repeats daily@09:00") ||
		!strings.Contains(lines[1], "call mum") || !strings.Contains(lines[1], "18:30") {
		t.Fatalf("list = %q", res.Content)
	}

	id := strings.TrimSuffix(strings.Fields(lines[1])[1], ":")
	if _, err := run(t, p, "cancel_reminder", `{"id":"`+id+`"}`); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if _, err := run(t, p, "cancel_reminder", `{"id":"`+id+`"}`); err == nil {
		t.Error("cancelling twice succeeded")
	}
	if _, err := run(t, p, "cancel_reminder", `{}`); err == nil {
		t.Error("cancel without id succeeded")
	}
}

func TestRunJobNotifiesUrgently(t *testing.T) {
	p, h := setup(t, nil)

	if err := p.RunJob(context.Background(), "fire", `{"text":"stretch"}`); err != nil {
		t.Fatal(err)
	}
	n := h.Notifications()
	if len(n) != 1 || n[0].Text != "Reminder: stretch" || !n[0].Urgent {
		t.Errorf("notifications = %+v; a reminder the owner asked for must be urgent", n)
	}

	for name, args := range map[string][2]string{
		"unknown job": {"other", `{"text":"x"}`},
		"bad payload": {"fire", `nope`},
		"no text":     {"fire", `{"text":" "}`},
	} {
		if err := p.RunJob(context.Background(), args[0], args[1]); err == nil {
			t.Errorf("%s: RunJob succeeded", name)
		}
	}
	if len(h.Notifications()) != 1 {
		t.Error("a failed job still sent a notification")
	}
}
