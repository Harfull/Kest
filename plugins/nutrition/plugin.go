// Package nutrition keeps a light, non-judgmental log of what the owner eats
// and drinks, and a short list of eating patterns they want to build.
//
// It deliberately has no calories, weights, targets or "good" and "bad" foods:
// the coach's voice guide rules out extreme dieting and body-focused talk, and a
// log that counts those invites them. It records meals and simple descriptors
// (protein, vegetables...) and reports how many days were logged. If the owner
// describes restricting, bingeing or purging, the model is told to put coaching
// aside and respond with care.
package nutrition

import (
	"context"
	_ "embed" // manifest.toml
	"encoding/json"
	"errors"
	"fmt"
	"slices"
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
	focusKey = "patterns"
	maxFocus = 3
)

// meals are the slots a meal can be logged in.
var meals = []string{"breakfast", "lunch", "snack", "dinner", "other"}

// tags are neutral descriptors, chosen to describe a meal and not to grade it.
var tags = []string{"protein", "vegetables", "fruit", "fiber", "homemade", "dairy", "whole_grains"}

// entry is one logged meal or drink.
type entry struct {
	Meal        string   `json:"meal,omitempty"`
	Description string   `json:"description,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Water       int      `json:"water,omitempty"` // glasses
	At          time.Time
}

// Plugin implements plugin.Plugin, plugin.ContextProvider and plugin.Digester.
type Plugin struct {
	host plugin.Host
	book logbook.Book[entry]
}

// New returns a fresh plugin instance.
func New() plugin.Plugin { return &Plugin{} }

// Manifest implements plugin.Plugin.
func (p *Plugin) Manifest() plugin.Manifest { return manifest }

// Init implements plugin.Plugin.
func (p *Plugin) Init(_ context.Context, h plugin.Host) error {
	p.host = h
	p.book = logbook.Book[entry]{KV: h.KV(), Prefix: "log/"}
	return nil
}

// Close implements plugin.Plugin.
func (p *Plugin) Close(context.Context) error { return nil }

func enum(values []string) string {
	q := make([]string, len(values))
	for i, v := range values {
		q[i] = `"` + v + `"`
	}
	return strings.Join(q, ",")
}

// Tools implements plugin.Plugin.
func (p *Plugin) Tools() []plugin.Tool {
	schema := func(props, required string) json.RawMessage {
		return json.RawMessage(`{"type":"object","properties":{` + props + `},"required":[` + required + `],"additionalProperties":false}`)
	}
	return []plugin.Tool{
		{
			Name: "log_meal", Risk: plugin.RiskWrite, Handler: p.logMeal,
			Description: "Record a meal or snack the owner ate (breakfast, lunch, dinner), in their words, optionally with descriptors. Log without comment on the food: no calories, no judging, no targets.",
			Schema: schema(`"meal":{"type":"string","enum":[`+enum(meals)+`]},"description":{"type":"string","maxLength":200},`+
				`"tags":{"type":"array","items":{"type":"string","enum":[`+enum(tags)+`]},"maxItems":6}`, `"meal","description"`),
		},
		{
			Name: "log_water", Risk: plugin.RiskWrite, Handler: p.logWater,
			Description: "Record glasses of water the owner drank (1 to 20).",
			Schema:      schema(`"glasses":{"type":"integer","minimum":1,"maximum":20}`, `"glasses"`),
		},
		{
			Name: "nutrition_history", Risk: plugin.RiskRead, Handler: p.history,
			Description: "Show what was logged over the last days (default 7, up to 30) and on how many days. Describe it plainly; never evaluate it.",
			Schema:      schema(`"days":{"type":"integer","minimum":1,"maximum":30}`, ""),
		},
		{
			Name: "set_eating_patterns", Risk: plugin.RiskWrite, Handler: p.setPatterns,
			Description: "Save up to three small eating patterns the owner wants to build, in their words, such as 'a vegetable with dinner' or 'breakfast before ten'. Replaces the list; an empty list clears it. Patterns are about adding something, never restricting.",
			Schema:      schema(`"patterns":{"type":"array","items":{"type":"string","maxLength":120},"maxItems":3}`, `"patterns"`),
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

func (p *Plugin) logMeal(ctx context.Context, call plugin.Call) (plugin.Result, error) {
	var a struct {
		Meal        string   `json:"meal"`
		Description string   `json:"description"`
		Tags        []string `json:"tags"`
	}
	if err := decode(call.Args, &a); err != nil {
		return plugin.Result{}, err
	}
	if !slices.Contains(meals, a.Meal) {
		return plugin.Result{}, fmt.Errorf("meal must be one of %s", strings.Join(meals, ", "))
	}
	desc := tidy(a.Description, 200)
	if desc == "" {
		return plugin.Result{}, errors.New("description is required")
	}
	var picked []string
	for _, t := range a.Tags {
		if !slices.Contains(tags, t) {
			return plugin.Result{}, fmt.Errorf("tags can be: %s", strings.Join(tags, ", "))
		}
		if !slices.Contains(picked, t) {
			picked = append(picked, t)
		}
	}
	if err := p.book.Add(ctx, p.today(), entry{Meal: a.Meal, Description: desc, Tags: picked, At: p.host.Clock().Now()}); err != nil {
		return plugin.Result{}, err
	}
	return plugin.Result{Content: "Logged " + a.Meal + ".", Summary: "logged a meal"}, nil
}

func (p *Plugin) logWater(ctx context.Context, call plugin.Call) (plugin.Result, error) {
	var a struct {
		Glasses int `json:"glasses"`
	}
	if err := decode(call.Args, &a); err != nil {
		return plugin.Result{}, err
	}
	if a.Glasses < 1 || a.Glasses > 20 {
		return plugin.Result{}, errors.New("glasses must be between 1 and 20")
	}
	day := p.today()
	if err := p.book.Add(ctx, day, entry{Water: a.Glasses, At: p.host.Clock().Now()}); err != nil {
		return plugin.Result{}, err
	}
	items, _ := p.book.Load(ctx, day)
	total := 0
	for _, e := range items {
		total += e.Water
	}
	return plugin.Result{Content: fmt.Sprintf("Logged. About %d glass(es) of water today.", total), Summary: "logged water"}, nil
}

func (p *Plugin) history(ctx context.Context, call plugin.Call) (plugin.Result, error) {
	var a struct {
		Days int `json:"days"`
	}
	if err := decode(call.Args, &a); err != nil {
		return plugin.Result{}, err
	}
	if a.Days <= 0 || a.Days > 30 {
		a.Days = 7
	}
	now, loc := p.host.Clock().Now(), p.host.Location()
	from := now.AddDate(0, 0, -(a.Days - 1))
	byDay, err := p.book.Range(ctx, logbook.Day(from, loc), logbook.Day(now, loc))
	if err != nil {
		return plugin.Result{}, err
	}
	if len(byDay) == 0 {
		return plugin.Result{Content: fmt.Sprintf("Nothing logged in the last %d days.", a.Days), Summary: "read nutrition history"}, nil
	}
	var b strings.Builder
	for _, day := range logbook.Days(from, now, loc) {
		items := byDay[day]
		if len(items) == 0 {
			continue
		}
		water := 0
		var parts []string
		for _, e := range items {
			water += e.Water
			if e.Meal != "" {
				parts = append(parts, e.Meal+": "+e.Description)
			}
		}
		fmt.Fprintf(&b, "%s: %s", day, strings.Join(parts, "; "))
		if water > 0 {
			fmt.Fprintf(&b, " (water: %d glasses)", water)
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "Logged on %d of the last %d days.", len(byDay), a.Days)
	return plugin.Result{Content: b.String(), Summary: "read nutrition history"}, nil
}

func (p *Plugin) loadPatterns(ctx context.Context) []string {
	var out []string
	if raw, err := p.host.KV().Get(ctx, focusKey); err == nil {
		_ = json.Unmarshal([]byte(raw), &out)
	}
	return out
}

func (p *Plugin) setPatterns(ctx context.Context, call plugin.Call) (plugin.Result, error) {
	var a struct {
		Patterns []string `json:"patterns"`
	}
	if err := decode(call.Args, &a); err != nil {
		return plugin.Result{}, err
	}
	if len(a.Patterns) > maxFocus {
		return plugin.Result{}, fmt.Errorf("at most %d patterns; fewer is easier to keep", maxFocus)
	}
	var clean []string
	for _, s := range a.Patterns {
		if s = tidy(s, 120); s != "" {
			clean = append(clean, s)
		}
	}
	if len(clean) == 0 {
		if err := p.host.KV().Delete(ctx, focusKey); err != nil {
			return plugin.Result{}, err
		}
		return plugin.Result{Content: "Cleared the eating patterns.", Summary: "cleared eating patterns"}, nil
	}
	raw, _ := json.Marshal(clean)
	if err := p.host.KV().Set(ctx, focusKey, string(raw)); err != nil {
		return plugin.Result{}, err
	}
	return plugin.Result{Content: "Saved " + fmt.Sprint(len(clean)) + " pattern(s).", Summary: "saved eating patterns"}, nil
}

// loggedDays counts days with a logged meal from..to, and the days in the range.
func (p *Plugin) loggedDays(ctx context.Context, from, to time.Time) (logged int, err error) {
	loc := p.host.Location()
	byDay, err := p.book.Range(ctx, logbook.Day(from, loc), logbook.Day(to, loc))
	for _, items := range byDay {
		for _, e := range items {
			if e.Meal != "" {
				logged++
				break
			}
		}
	}
	return logged, err
}

// PromptContext implements plugin.ContextProvider.
func (p *Plugin) PromptContext(ctx context.Context) string {
	var b strings.Builder
	if pats := p.loadPatterns(ctx); len(pats) > 0 {
		b.WriteString("- Eating patterns the owner is building: " + strings.Join(pats, "; ") + ".\n")
	}
	b.WriteString("- Meal log: use nutrition__log_meal when the owner says what they ate, without comment on the food. Never give calorie or weight targets, mention weight, or call food good or bad. " +
		"If they describe skipping meals to compensate, bingeing, purging or fear of eating, drop the coaching tone, respond with care and suggest talking to a professional.\n")
	return b.String()
}

// WeeklyDigest implements plugin.Digester.
func (p *Plugin) WeeklyDigest(ctx context.Context) string {
	now := p.host.Clock().Now()
	logged, err := p.loggedDays(ctx, logbook.WeekStart(now, p.host.Location()), now)
	if err != nil || logged == 0 {
		return ""
	}
	return fmt.Sprintf("Meals were logged on %d day(s) this week.", logged)
}
