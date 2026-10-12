package workout

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

func TestLogAndReport(t *testing.T) {
	r := newRig(t)
	res := r.must(t, "log_workout", map[string]any{"kind": "Run", "minutes": 30, "effort": 6, "note": "easy loop"})
	if !strings.Contains(res.Content, "run, 30 minutes on 2026-10-11") || !strings.Contains(res.Content, "1 session(s), 30 minutes") {
		t.Errorf("log result = %q", res.Content)
	}
	r.h.Clk.Advance(-24 * time.Hour) // Saturday
	r.must(t, "log_workout", map[string]any{"kind": "strength", "minutes": 45})
	r.h.Clk.Advance(24 * time.Hour)

	res = r.must(t, "workout_history", map[string]any{"days": 7})
	for _, want := range []string{"2026-10-10", "strength, 45 min", "run, 30 min, effort 6/10, easy loop", "Total over 7 days: 2 session(s), 75 minutes."} {
		if !strings.Contains(res.Content, want) {
			t.Errorf("history lacks %q:\n%s", want, res.Content)
		}
	}
	if res := r.must(t, "workout_history", map[string]any{"days": 1}); !strings.Contains(res.Content, "1 session(s), 30 minutes") {
		t.Errorf("one day of history = %q", res.Content)
	}
}

func TestBackfillAndLimits(t *testing.T) {
	r := newRig(t)
	r.must(t, "log_workout", map[string]any{"kind": "walk", "minutes": 20, "day": "2026-10-05"})
	for name, args := range map[string]map[string]any{
		"future day":    {"kind": "run", "minutes": 10, "day": "2026-10-12"},
		"too far back":  {"kind": "run", "minutes": 10, "day": "2026-01-01"},
		"bad date":      {"kind": "run", "minutes": 10, "day": "yesterday"},
		"no kind":       {"kind": "  ", "minutes": 10},
		"huge duration": {"kind": "run", "minutes": 601},
		"zero duration": {"kind": "run", "minutes": 0},
		"effort 11":     {"kind": "run", "minutes": 10, "effort": 11},
		"unknown field": {"kind": "run", "minutes": 10, "calories": 500},
	} {
		if _, err := r.call("log_workout", args); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	res := r.must(t, "workout_history", map[string]any{"days": 14})
	if !strings.Contains(res.Content, "2026-10-05") || !strings.Contains(res.Content, "walk") {
		t.Errorf("a backfilled workout is missing: %s", res.Content)
	}
}

func TestTargetContextAndDigest(t *testing.T) {
	r := newRig(t)
	if got := r.p.WeeklyDigest(context.Background()); got != "" {
		t.Errorf("an empty week has a digest: %q", got)
	}
	r.must(t, "set_workout_target", map[string]any{"sessions_per_week": 3})
	r.h.Clk.Advance(-3 * 24 * time.Hour) // Thursday
	r.must(t, "log_workout", map[string]any{"kind": "run", "minutes": 40})
	r.h.Clk.Advance(3 * 24 * time.Hour)
	r.must(t, "log_workout", map[string]any{"kind": "yoga", "minutes": 25})

	digest := r.p.WeeklyDigest(context.Background())
	if digest != "Workouts this week: 2 session(s), 65 minutes, aiming for 3." {
		t.Errorf("digest = %q", digest)
	}
	if problems := voice.Check(digest, 40); len(problems) > 0 {
		t.Errorf("the digest breaks the voice guide: %v", problems)
	}
	ctxText := r.p.PromptContext(context.Background())
	for _, want := range []string{"Workouts this week: 2 session(s), 65 minutes, aiming for 3", "workout__log_workout", "never encourage pushing through pain"} {
		if !strings.Contains(ctxText, want) {
			t.Errorf("context lacks %q:\n%s", want, ctxText)
		}
	}
	if res := r.must(t, "set_workout_target", map[string]any{"sessions_per_week": 0}); !strings.Contains(res.Content, "Cleared") {
		t.Error(res.Content)
	}
	if got := r.p.WeeklyDigest(context.Background()); !strings.Contains(got, "2 session(s)") || strings.Contains(got, "aiming") {
		t.Errorf("digest after clearing the target = %q", got)
	}
	if _, err := r.call("set_workout_target", map[string]any{"sessions_per_week": 15}); err == nil {
		t.Error("a target of 15 a week was accepted")
	}
}

func TestWeekStartsOnMonday(t *testing.T) {
	r := newRig(t) // Sunday 11 October: the week began Monday 5 October
	r.h.Clk.Advance(-7 * 24 * time.Hour)
	r.must(t, "log_workout", map[string]any{"kind": "run", "minutes": 30}) // last Sunday, previous week
	r.h.Clk.Advance(7 * 24 * time.Hour)
	if got := r.p.WeeklyDigest(context.Background()); got != "" {
		t.Errorf("last week's workout counted toward this week: %q", got)
	}
}

func TestDeleteByID(t *testing.T) {
	r := newRig(t)
	r.must(t, "log_workout", map[string]any{"kind": "run", "minutes": 30})
	hist := r.must(t, "workout_history", map[string]any{}).Content
	start, end := strings.Index(hist, "["), strings.Index(hist, "]")
	id := hist[start+1 : end]
	r.must(t, "delete_workout", map[string]any{"id": id})
	if got := r.must(t, "workout_history", map[string]any{}).Content; !strings.Contains(got, "No workouts") {
		t.Errorf("the entry was not removed: %s", got)
	}
	if _, err := r.call("delete_workout", map[string]any{"id": "nope"}); err == nil {
		t.Error("deleting an unknown id succeeded")
	}
}

func TestLabelsAreTidied(t *testing.T) {
	r := newRig(t)
	r.must(t, "log_workout", map[string]any{"kind": "  Trail\x1b[31m   RUN ", "minutes": 30, "note": "line\nbreak\x07"})
	got := r.must(t, "workout_history", map[string]any{}).Content
	if strings.ContainsAny(got, "\x1b\x07") {
		t.Errorf("control characters were stored: %q", got)
	}
	if !strings.Contains(got, "trail [31m run, 30 min, line break") {
		t.Errorf("history = %q", got)
	}
}

func TestContract(t *testing.T) { plugintest.RunContract(t, New, map[string]any{}) }
