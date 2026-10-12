package nutrition

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

func TestLogMealsAndWater(t *testing.T) {
	r := newRig(t)
	r.must(t, "log_meal", map[string]any{"meal": "breakfast", "description": "oats with berries", "tags": []string{"fiber", "fruit", "fiber"}})
	r.must(t, "log_meal", map[string]any{"meal": "dinner", "description": "chicken and rice\x1b[0m", "tags": []string{"protein"}})
	r.must(t, "log_water", map[string]any{"glasses": 2})
	res := r.must(t, "log_water", map[string]any{"glasses": 3})
	if !strings.Contains(res.Content, "About 5 glass(es)") {
		t.Errorf("water = %q", res.Content)
	}
	hist := r.must(t, "nutrition_history", map[string]any{"days": 3}).Content
	for _, want := range []string{"2026-10-11: breakfast: oats with berries; dinner: chicken and rice [0m (water: 5 glasses)", "Logged on 1 of the last 3 days."} {
		if !strings.Contains(hist, want) {
			t.Errorf("history lacks %q:\n%s", want, hist)
		}
	}
	items, _ := r.p.book.Load(context.Background(), "2026-10-11")
	if len(items[0].Tags) != 2 {
		t.Errorf("a repeated tag was stored twice: %v", items[0].Tags)
	}
}

func TestInputsAreBounded(t *testing.T) {
	r := newRig(t)
	for name, args := range map[string]map[string]any{
		"unknown meal slot": {"meal": "brunch", "description": "x"},
		"no description":    {"meal": "lunch", "description": "  "},
		"judging tag":       {"meal": "lunch", "description": "x", "tags": []string{"junk"}},
		"calorie field":     {"meal": "lunch", "description": "x", "calories": 600},
	} {
		if _, err := r.call("log_meal", args); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	for _, g := range []int{0, 21} {
		if _, err := r.call("log_water", map[string]any{"glasses": g}); err == nil {
			t.Errorf("%d glasses accepted", g)
		}
	}
}

func TestPatterns(t *testing.T) {
	r := newRig(t)
	r.must(t, "set_eating_patterns", map[string]any{"patterns": []string{"a vegetable with dinner", "breakfast before ten"}})
	if ctx := r.p.PromptContext(context.Background()); !strings.Contains(ctx, "a vegetable with dinner; breakfast before ten") {
		t.Errorf("context = %q", ctx)
	}
	if _, err := r.call("set_eating_patterns", map[string]any{"patterns": []string{"a", "b", "c", "d"}}); err == nil {
		t.Error("four patterns accepted; fewer is easier to keep")
	}
	r.must(t, "set_eating_patterns", map[string]any{"patterns": []string{}})
	if ctx := r.p.PromptContext(context.Background()); strings.Contains(ctx, "Eating patterns") {
		t.Errorf("a cleared pattern list is still in the context: %q", ctx)
	}
}

func TestTheModelIsToldToStayOutOfDietTalk(t *testing.T) {
	ctx := newRig(t).p.PromptContext(context.Background())
	for _, want := range []string{"Never give calorie or weight targets", "good or bad", "bingeing, purging or fear of eating", "professional"} {
		if !strings.Contains(ctx, want) {
			t.Errorf("guidance lacks %q", want)
		}
	}
	for _, tool := range newRig(t).p.Tools() {
		for _, banned := range []string{"calorie count", "macro", "weight loss"} {
			if strings.Contains(strings.ToLower(tool.Description), banned) {
				t.Errorf("%s invites %q", tool.Name, banned)
			}
		}
	}
}

func TestWeeklyDigestCountsDaysAndStaysPlain(t *testing.T) {
	r := newRig(t)
	if got := r.p.WeeklyDigest(context.Background()); got != "" {
		t.Errorf("empty week = %q", got)
	}
	r.must(t, "log_water", map[string]any{"glasses": 2}) // water alone is not a meal day
	if got := r.p.WeeklyDigest(context.Background()); got != "" {
		t.Errorf("water counted as a meal day: %q", got)
	}
	for range 3 {
		r.must(t, "log_meal", map[string]any{"meal": "lunch", "description": "soup"})
		r.h.Clk.Advance(-24 * time.Hour)
	}
	r.h.Clk.Advance(3 * 24 * time.Hour)
	digest := r.p.WeeklyDigest(context.Background())
	// Today is Sunday, so the three days (Sunday, Saturday, Friday) are all in this week.
	if digest != "Meals were logged on 3 day(s) this week." {
		t.Errorf("digest = %q", digest)
	}
	if problems := voice.Check(digest, 40); len(problems) > 0 {
		t.Errorf("the digest breaks the voice guide: %v", problems)
	}
}

func TestContract(t *testing.T) { plugintest.RunContract(t, New, map[string]any{}) }
