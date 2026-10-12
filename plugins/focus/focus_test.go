package focus

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Harfull/Kest/internal/plugin"
	"github.com/Harfull/Kest/internal/plugin/plugintest"
	"github.com/Harfull/Kest/internal/voice"
)

type rig struct {
	p     *Plugin
	h     *plugintest.Host
	tools map[string]plugin.Tool
}

func newRig(t *testing.T) *rig {
	t.Helper()
	h := plugintest.NewHost() // Sunday 11 October 2026, 14:00 UTC
	p := New().(*Plugin)
	if err := p.Init(context.Background(), h); err != nil {
		t.Fatal(err)
	}
	r := &rig{p: p, h: h, tools: map[string]plugin.Tool{}}
	for _, tool := range p.Tools() {
		r.tools[tool.Name] = tool
	}
	return r
}

func (r *rig) call(name string, args any) (plugin.Result, error) {
	data, _ := json.Marshal(args)
	return r.tools[name].Handler(context.Background(), plugin.Call{Args: data})
}

func (r *rig) must(t *testing.T, name string, args any) plugin.Result {
	t.Helper()
	res, err := r.call(name, args)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return res
}

func TestPriorities(t *testing.T) {
	r := newRig(t)
	if got := r.must(t, "list_priorities", map[string]any{}).Content; !strings.Contains(got, "No priorities") {
		t.Errorf("empty list = %q", got)
	}
	r.must(t, "set_priorities", map[string]any{"items": []string{"Write the report", "  Call the dentist\x1b[0m "}})
	got := r.must(t, "list_priorities", map[string]any{}).Content
	if got != "1. Write the report\n2. Call the dentist [0m" {
		t.Errorf("priorities = %q", got)
	}
	r.must(t, "complete_priority", map[string]any{"number": 1})
	if got := r.must(t, "list_priorities", map[string]any{}).Content; !strings.Contains(got, "1. Write the report (done)") || strings.Contains(got, "dentist (done)") {
		t.Errorf("after completing = %q", got)
	}
	for name, args := range map[string]map[string]any{
		"none":        {"items": []string{}},
		"four":        {"items": []string{"a", "b", "c", "d"}},
		"only blanks": {"items": []string{" ", ""}},
	} {
		if _, err := r.call("set_priorities", args); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := r.call("complete_priority", map[string]any{"number": 3}); err == nil {
		t.Error("completed a priority that does not exist")
	}
	// Tomorrow starts with a clean list.
	r.h.Clk.Advance(24 * time.Hour)
	if got := r.must(t, "list_priorities", map[string]any{}).Content; !strings.Contains(got, "No priorities") {
		t.Errorf("yesterday's priorities carried over: %q", got)
	}
}

func TestTimerRunsToTheEndAndAnnouncesIt(t *testing.T) {
	r := newRig(t)
	res := r.must(t, "start_focus", map[string]any{"task": "Quarterly report", "minutes": 25})
	if !strings.Contains(res.Content, "until 14:25") {
		t.Errorf("start = %q", res.Content)
	}
	jobs, _ := r.h.Jobs().List(context.Background())
	if len(jobs) != 1 || jobs[0].Name != jobEnd || !jobs[0].NextRun.Equal(plugintest.Start.Add(25*time.Minute)) {
		t.Fatalf("jobs = %+v", jobs)
	}
	if _, err := r.call("start_focus", map[string]any{"task": "Another"}); err == nil || !strings.Contains(err.Error(), "already running") {
		t.Errorf("a second timer started: %v", err)
	}
	if ctx := r.p.PromptContext(context.Background()); !strings.Contains(ctx, "A focus timer is running: Quarterly report until 14:25") {
		t.Errorf("context = %q", ctx)
	}

	r.h.Clk.Advance(25 * time.Minute)
	if err := r.p.RunJob(context.Background(), jobEnd, ""); err != nil {
		t.Fatal(err)
	}
	notes := r.h.Notifications()
	if len(notes) != 1 || !notes[0].Urgent || !strings.Contains(notes[0].Text, "Your 25 minutes on Quarterly report are up.") {
		t.Fatalf("notifications = %+v", notes)
	}
	if problems := voice.Check(notes[0].Text, 40); len(problems) > 0 {
		t.Errorf("the end message breaks the voice guide: %v", problems)
	}
	sum := r.must(t, "focus_summary", map[string]any{}).Content
	if !strings.Contains(sum, "Today: 1 stretch(es), 25 minutes.") {
		t.Errorf("summary = %q", sum)
	}
	// The timer is gone, so another can start, and a late duplicate job is harmless.
	if err := r.p.RunJob(context.Background(), jobEnd, ""); err != nil || len(r.h.Notifications()) != 1 {
		t.Errorf("a duplicate end job announced again: %v", err)
	}
	r.must(t, "start_focus", map[string]any{"task": "Email"})
}

func TestStopEarlyLogsTheTimeSpent(t *testing.T) {
	r := newRig(t)
	r.must(t, "start_focus", map[string]any{"task": "Reading", "minutes": 30})
	r.h.Clk.Advance(12 * time.Minute)
	res := r.must(t, "stop_focus", map[string]any{})
	if !strings.Contains(res.Content, "About 12 minute(s) on Reading") {
		t.Errorf("stop = %q", res.Content)
	}
	if jobs, _ := r.h.Jobs().List(context.Background()); len(jobs) != 0 {
		t.Errorf("the end job was left scheduled: %+v", jobs)
	}
	if got := r.must(t, "stop_focus", map[string]any{}).Content; !strings.Contains(got, "No focus timer") {
		t.Errorf("stopping nothing = %q", got)
	}
	if sum := r.must(t, "focus_summary", map[string]any{}).Content; !strings.Contains(sum, "Today: 1 stretch(es), 12 minutes.") {
		t.Errorf("summary = %q", sum)
	}
}

func TestStopImmediatelyLogsNothing(t *testing.T) {
	r := newRig(t)
	r.must(t, "start_focus", map[string]any{"task": "x"})
	r.must(t, "stop_focus", map[string]any{})
	if sum := r.must(t, "focus_summary", map[string]any{}).Content; !strings.Contains(sum, "Today: 0 stretch(es), 0 minutes.") {
		t.Errorf("summary = %q", sum)
	}
}

func TestTimerLimits(t *testing.T) {
	r := newRig(t)
	for name, args := range map[string]map[string]any{
		"too short": {"task": "x", "minutes": 4},
		"too long":  {"task": "x", "minutes": 121},
		"no task":   {"task": "  "},
	} {
		if _, err := r.call("start_focus", args); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if res := r.must(t, "start_focus", map[string]any{"task": "default length"}); !strings.Contains(res.Content, "25 minutes") {
		t.Errorf("default = %q", res.Content)
	}
}

func TestWeeklyDigest(t *testing.T) {
	r := newRig(t)
	if got := r.p.WeeklyDigest(context.Background()); got != "" {
		t.Errorf("empty week = %q", got)
	}
	for range 2 {
		r.must(t, "start_focus", map[string]any{"task": "x", "minutes": 20})
		r.h.Clk.Advance(20 * time.Minute)
		if err := r.p.RunJob(context.Background(), jobEnd, ""); err != nil {
			t.Fatal(err)
		}
	}
	digest := r.p.WeeklyDigest(context.Background())
	if digest != "Focus time this week: 2 stretch(es), 40 minutes." {
		t.Errorf("digest = %q", digest)
	}
	if problems := voice.Check(digest, 40); len(problems) > 0 {
		t.Errorf("the digest breaks the voice guide: %v", problems)
	}
}

func TestContractAndJobs(t *testing.T) {
	plugintest.RunContract(t, New, map[string]any{})
	if names := New().(*Plugin).JobNames(); len(names) != 1 || names[0] != "focus_end" {
		t.Errorf("job names = %v", names)
	}
	if err := New().(*Plugin).RunJob(context.Background(), "other", ""); err == nil {
		t.Error("an unknown job ran")
	}
}

func TestATimerWhoseJobWasDroppedDoesNotBlockTheNextOne(t *testing.T) {
	r := newRig(t)
	r.must(t, "start_focus", map[string]any{"task": "Old", "minutes": 25})
	r.h.Clk.Advance(25*time.Minute + staleAfter + time.Minute) // Kest was down; the end job expired
	if res := r.must(t, "start_focus", map[string]any{"task": "New"}); !strings.Contains(res.Content, "New") {
		t.Fatalf("start = %q", res.Content)
	}
}
