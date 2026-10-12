package messages

import (
	"strings"
	"testing"

	"github.com/Harfull/Kest/internal/voice"
)

// samples gives realistic data for each kind, in a "full" and an "empty" shape
// so optional parts of templates are exercised both ways.
var samples = map[string][]map[string]any{
	"morning": {
		{"Sleep": "My guess is about seven hours of sleep. Close?", "Habit": "Walk after lunch"},
		{"Sleep": "", "Habit": ""},
		{"Sleep": "", "Habit": "Stretching"},
	},
	"sleep_line": {{"Hours": "about seven hours"}},
	"sleep_ack":  {{}},
	"habit_prompt": {
		{"Habit": "Walk after lunch", "Anchor": "After lunch", "Behavior": "Walk for 10 minutes", "Tiny": "Put on shoes and step outside"},
		{"Habit": "Stretching", "Anchor": "", "Behavior": "Stretch for 5 minutes", "Tiny": "Touch your toes once"},
	},
	"habit_done": {
		{"Habit": "Walk after lunch", "Done7": 5, "Expected7": 6},
		{"Habit": "Walk after lunch", "Done7": 1, "Expected7": 3},
		{"Habit": "Walk after lunch", "Done7": 0, "Expected7": 0},
	},
	"habit_tiny":    {{"Habit": "Walk after lunch"}},
	"habit_skipped": {{"Habit": "Walk after lunch"}},
	"lapse_first":   {{"Habit": "Walk after lunch", "Tiny": "Put on shoes and step outside"}},
	"lapse_repeat":  {{"Habit": "Walk after lunch"}},
	"habit_paused":  {{"Habit": "Walk after lunch"}},
	"habit_resize":  {{}},
	"evening":       {{}},
	"evening_none":  {{}},
	"rough_day":     {{}},
	"good_day":      {{}},
	"winddown":      {{}},
	"weekly": {
		{"Summary": "Walk after lunch: 5 of 7 days. Stretching: 3 of 4 days. Sleep averaged about seven hours.", "Suggestion": "Evenings keep slipping, so earlier workouts might fit better."},
		{"Summary": "Walk after lunch: 5 of 7 days.", "Suggestion": ""},
	},
	"backoff":      {{}},
	"backoff_less": {{}},
	"backoff_keep": {{}},
}

func load(t *testing.T) *Set {
	t.Helper()
	s, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	return s
}

func TestEveryKindHasSamplesAndVariants(t *testing.T) {
	s := load(t)
	for _, k := range s.Kinds() {
		if _, ok := samples[k]; !ok {
			t.Errorf("kind %q has no sample data in the test; add one so its wording is checked", k)
		}
	}
	for k := range samples {
		if len(s.Variants(k)) == 0 {
			t.Errorf("sample for %q but no such kind in messages.toml", k)
		}
	}
	// Kinds that repeat during a week need more than one wording.
	for _, k := range []string{"morning", "habit_prompt", "habit_done", "evening", "lapse_first", "lapse_repeat", "weekly", "rough_day", "winddown"} {
		if n := len(s.Variants(k)); n < 2 {
			t.Errorf("kind %q has %d variant(s); repeated messages need at least 2", k, n)
		}
	}
}

func TestEveryVariantFollowsTheVoiceGuide(t *testing.T) {
	s := load(t)
	for kind, shapes := range samples {
		for i, data := range shapes {
			all, err := s.RenderAll(kind, data)
			if err != nil {
				t.Errorf("%s (data %d): %v", kind, i, err)
				continue
			}
			for variant, text := range all {
				id := kind + "/" + variant
				if text == "" {
					t.Errorf("%s renders empty", id)
				}
				if strings.Contains(text, "{{") || strings.Contains(text, "<no value>") || strings.Contains(text, "  ") {
					t.Errorf("%s has template residue or double spaces: %q", id, text)
				}
				if v := voice.Check(text, 50); len(v) > 0 {
					t.Errorf("%s breaks the voice guide: %v\n  %q", id, v, text)
				}
				if n := strings.Count(text, "?"); n > 1 {
					t.Errorf("%s asks %d questions; the guide says one at a time:\n  %q", id, n, text)
				}
				if strings.HasPrefix(text, " ") || strings.HasSuffix(text, " ") {
					t.Errorf("%s has stray outer whitespace", id)
				}
			}
		}
	}
}

func TestNoGuiltOrLossFraming(t *testing.T) {
	// The voice checker covers banned phrases; these are the coaching-specific
	// ones from the behavior research: no counting down misses, no "again".
	s := load(t)
	for kind, shapes := range samples {
		for _, data := range shapes {
			all, _ := s.RenderAll(kind, data)
			for variant, text := range all {
				lower := strings.ToLower(text)
				for _, bad := range []string{"again!", "you missed", "you failed", "disappoint", "behind", "lost", "broke", "in a row", "days in a row", "only "} {
					if strings.Contains(lower, bad) {
						t.Errorf("%s/%s contains %q: %q", kind, variant, bad, text)
					}
				}
			}
		}
	}
}

func TestRenderVariesAndAvoidsRepeats(t *testing.T) {
	s := load(t)
	data := samples["habit_prompt"][0]

	first, v1, err := s.Render("habit_prompt", data, "", 0)
	if err != nil || first == "" {
		t.Fatalf("Render() = %q, %q, %v", first, v1, err)
	}
	for seed := 0; seed < 20; seed++ {
		_, v, err := s.Render("habit_prompt", data, v1, seed)
		if err != nil {
			t.Fatal(err)
		}
		if v == v1 {
			t.Fatalf("seed %d repeated variant %q despite avoid", seed, v1)
		}
	}

	seen := map[string]bool{}
	for seed := 0; seed < 12; seed++ {
		_, v, _ := s.Render("habit_prompt", data, "", seed)
		seen[v] = true
	}
	if len(seen) < 2 {
		t.Errorf("only %d variant(s) used across seeds", len(seen))
	}

	// Deterministic for the same inputs, and negative seeds are fine.
	a, va, _ := s.Render("habit_prompt", data, "", 7)
	b, vb, _ := s.Render("habit_prompt", data, "", 7)
	if a != b || va != vb {
		t.Error("Render is not deterministic")
	}
	if _, _, err := s.Render("habit_prompt", data, "", -5); err != nil {
		t.Errorf("negative seed: %v", err)
	}

	// A kind with a single variant still renders when asked to avoid it.
	if text, _, err := s.Render("habit_paused", samples["habit_paused"][0], "a", 0); err != nil || text == "" {
		t.Errorf("single-variant avoid = %q, %v", text, err)
	}
}

func TestMissingKeysAndUnknownKindsAreErrors(t *testing.T) {
	s := load(t)
	if _, _, err := s.Render("habit_prompt", map[string]any{"Habit": "x"}, "", 0); err == nil {
		t.Error("a template with a missing key rendered; typos must fail loudly")
	}
	if _, _, err := s.Render("nonsense", nil, "", 0); err == nil {
		t.Error("unknown kind rendered")
	}
}

func TestLowerFirst(t *testing.T) {
	s := load(t)
	out, _, err := s.Render("habit_prompt", map[string]any{
		"Habit": "Walk After Lunch", "Anchor": "After lunch", "Behavior": "Walk for 10 minutes", "Tiny": "Put on shoes",
	}, "b", 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "walk After Lunch") && !strings.Contains(out, "walk for 10 minutes") {
		t.Errorf("lowerFirst not applied: %q", out)
	}
}

func TestParseRejectsBadFiles(t *testing.T) {
	for name, src := range map[string]string{
		"unknown key":       "[[message]]\nkind=\"a\"\nvariant=\"a\"\ntext=\"x\"\nextra=1\n",
		"missing text":      "[[message]]\nkind=\"a\"\nvariant=\"a\"\n",
		"duplicate variant": "[[message]]\nkind=\"a\"\nvariant=\"a\"\ntext=\"x\"\n[[message]]\nkind=\"a\"\nvariant=\"a\"\ntext=\"y\"\n",
		"bad template":      "[[message]]\nkind=\"a\"\nvariant=\"a\"\ntext=\"{{.Oops\"\n",
	} {
		if _, err := parse([]byte(src)); err == nil {
			t.Errorf("%s: parse succeeded", name)
		}
	}
}
