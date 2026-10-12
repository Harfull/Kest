// Package workout keeps the owner's workout log and weekly target.
//
// It records what the owner did (kind, minutes, how hard it felt) and reports
// counts back: in conversation, in the model's context and in the weekly review.
// It never prescribes training, sets targets on its own or gives medical advice.
// Reminders to train are habits in the coach, not part of this plugin.
package workout

import (
	"context"
	_ "embed" // manifest.toml
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
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
	maxMinutes  = 600
	maxBackDays = 60
	planKey     = "plan"
)

// session is one logged workout.
type session struct {
	ID      string    `json:"id"`
	Kind    string    `json:"kind"`
	Minutes int       `json:"minutes"`
	Effort  int       `json:"effort,omitempty"` // 1 to 10, how hard it felt
	Note    string    `json:"note,omitempty"`
	At      time.Time `json:"at"`
}

type plan struct {
	SessionsPerWeek int `json:"sessions_per_week"`
}

// Plugin implements plugin.Plugin, plugin.ContextProvider and plugin.Digester.
type Plugin struct {
	host plugin.Host
	book logbook.Book[session]
}

// New returns a fresh plugin instance.
func New() plugin.Plugin { return &Plugin{} }

// Manifest implements plugin.Plugin.
func (p *Plugin) Manifest() plugin.Manifest { return manifest }

// Init implements plugin.Plugin.
func (p *Plugin) Init(_ context.Context, h plugin.Host) error {
	p.host = h
	p.book = logbook.Book[session]{KV: h.KV(), Prefix: "log/"}
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
			Name: "log_workout", Risk: plugin.RiskWrite, Handler: p.log,
			Description: "Record a workout the owner did, when they say they ran, lifted, walked, cycled, swam, trained or exercised: the kind (run, strength, walk, yoga...), minutes, optionally how hard it felt from 1 (easy) to 10 (all out) and a short note. day defaults to today. If they have a workout habit, also mark it done with coach__log_habit.",
			Schema: schema(`"kind":{"type":"string","maxLength":30},"minutes":{"type":"integer","minimum":1,"maximum":600},`+
				`"effort":{"type":"integer","minimum":1,"maximum":10},"note":{"type":"string","maxLength":200},"day":{"type":"string","description":"YYYY-MM-DD, up to 60 days back."}`, `"kind","minutes"`),
		},
		{
			Name: "workout_history", Risk: plugin.RiskRead, Handler: p.history,
			Description: "Show logged workouts and totals for the last days (default 14, up to 90). Use it to answer how the week went; describe counts and minutes, never grade them.",
			Schema:      schema(`"days":{"type":"integer","minimum":1,"maximum":90}`, ""),
		},
		{
			Name: "set_workout_target", Risk: plugin.RiskWrite, Handler: p.setTarget,
			Description: "Set how many workouts per week the owner is aiming for (0 to 14; 0 clears it). Only when they ask; suggest small, sustainable numbers.",
			Schema:      schema(`"sessions_per_week":{"type":"integer","minimum":0,"maximum":14}`, `"sessions_per_week"`),
		},
		{
			Name: "delete_workout", Risk: plugin.RiskWrite, Handler: p.remove,
			Description: "Remove a workout entry by its id from workout_history, for example one logged by mistake.",
			Schema:      schema(`"id":{"type":"string"}`, `"id"`),
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

// tidy keeps owner-written labels to one short line of printable text.
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

func (p *Plugin) loadPlan(ctx context.Context) plan {
	var pl plan
	if raw, err := p.host.KV().Get(ctx, planKey); err == nil {
		_ = json.Unmarshal([]byte(raw), &pl)
	}
	return pl
}

func (p *Plugin) log(ctx context.Context, call plugin.Call) (plugin.Result, error) {
	var a struct {
		Kind    string `json:"kind"`
		Minutes int    `json:"minutes"`
		Effort  int    `json:"effort"`
		Note    string `json:"note"`
		Day     string `json:"day"`
	}
	if err := decode(call.Args, &a); err != nil {
		return plugin.Result{}, err
	}
	kind := strings.ToLower(tidy(a.Kind, 30))
	switch {
	case kind == "":
		return plugin.Result{}, errors.New("kind is required, for example run or strength")
	case a.Minutes < 1 || a.Minutes > maxMinutes:
		return plugin.Result{}, fmt.Errorf("minutes must be between 1 and %d", maxMinutes)
	case a.Effort < 0 || a.Effort > 10:
		return plugin.Result{}, errors.New("effort must be between 1 and 10")
	}
	now, loc := p.host.Clock().Now(), p.host.Location()
	day := logbook.Day(now, loc)
	if a.Day != "" {
		d, err := logbook.ParseDay(a.Day, loc)
		if err != nil {
			return plugin.Result{}, err
		}
		today, _ := logbook.ParseDay(day, loc)
		if d.After(today) || today.Sub(d) > maxBackDays*24*time.Hour {
			return plugin.Result{}, fmt.Errorf("day must be today or up to %d days back", maxBackDays)
		}
		day = d.Format(logbook.DayLayout)
	}
	s := session{ID: strconv.FormatInt(now.UnixNano(), 36), Kind: kind, Minutes: a.Minutes, Effort: a.Effort, Note: tidy(a.Note, 200), At: now}
	if err := p.book.Add(ctx, day, s); err != nil {
		return plugin.Result{}, err
	}
	week, _ := p.weekStats(ctx, now)
	msg := fmt.Sprintf("Logged: %s, %d minutes on %s. This week: %s.", kind, a.Minutes, day, week.text(p.loadPlan(ctx)))
	return plugin.Result{Content: msg, Summary: "logged a workout"}, nil
}

type stats struct {
	sessions, minutes int
	byDay             map[string][]session
}

func (s stats) text(pl plan) string {
	t := fmt.Sprintf("%d session(s), %d minutes", s.sessions, s.minutes)
	if pl.SessionsPerWeek > 0 {
		t += fmt.Sprintf(", aiming for %d", pl.SessionsPerWeek)
	}
	return t
}

func (p *Plugin) rangeStats(ctx context.Context, from, to time.Time) (stats, error) {
	loc := p.host.Location()
	byDay, err := p.book.Range(ctx, logbook.Day(from, loc), logbook.Day(to, loc))
	st := stats{byDay: byDay}
	for _, items := range byDay {
		for _, s := range items {
			st.sessions++
			st.minutes += s.Minutes
		}
	}
	return st, err
}

func (p *Plugin) weekStats(ctx context.Context, now time.Time) (stats, error) {
	return p.rangeStats(ctx, logbook.WeekStart(now, p.host.Location()), now)
}

func (p *Plugin) history(ctx context.Context, call plugin.Call) (plugin.Result, error) {
	var a struct {
		Days int `json:"days"`
	}
	if err := decode(call.Args, &a); err != nil {
		return plugin.Result{}, err
	}
	if a.Days <= 0 || a.Days > 90 {
		a.Days = 14
	}
	now, loc := p.host.Clock().Now(), p.host.Location()
	from := now.AddDate(0, 0, -(a.Days - 1))
	st, err := p.rangeStats(ctx, from, now)
	if err != nil {
		return plugin.Result{}, err
	}
	if st.sessions == 0 {
		return plugin.Result{Content: fmt.Sprintf("No workouts logged in the last %d days.", a.Days), Summary: "read workout history"}, nil
	}
	var b strings.Builder
	for _, day := range logbook.Days(from, now, loc) {
		for _, s := range st.byDay[day] {
			fmt.Fprintf(&b, "%s [%s] %s, %d min", day, s.ID, s.Kind, s.Minutes)
			if s.Effort > 0 {
				fmt.Fprintf(&b, ", effort %d/10", s.Effort)
			}
			if s.Note != "" {
				b.WriteString(", " + s.Note)
			}
			b.WriteString("\n")
		}
	}
	fmt.Fprintf(&b, "Total over %d days: %d session(s), %d minutes.", a.Days, st.sessions, st.minutes)
	return plugin.Result{Content: b.String(), Summary: "read workout history"}, nil
}

func (p *Plugin) setTarget(ctx context.Context, call plugin.Call) (plugin.Result, error) {
	var a struct {
		Sessions *int `json:"sessions_per_week"`
	}
	if err := decode(call.Args, &a); err != nil {
		return plugin.Result{}, err
	}
	if a.Sessions == nil || *a.Sessions < 0 || *a.Sessions > 14 {
		return plugin.Result{}, errors.New("sessions_per_week must be between 0 and 14")
	}
	if *a.Sessions == 0 {
		if err := p.host.KV().Delete(ctx, planKey); err != nil {
			return plugin.Result{}, err
		}
		return plugin.Result{Content: "Cleared the weekly target.", Summary: "cleared the workout target"}, nil
	}
	raw, _ := json.Marshal(plan{SessionsPerWeek: *a.Sessions})
	if err := p.host.KV().Set(ctx, planKey, string(raw)); err != nil {
		return plugin.Result{}, err
	}
	return plugin.Result{Content: fmt.Sprintf("Weekly target set to %d session(s).", *a.Sessions), Summary: "set the workout target"}, nil
}

func (p *Plugin) remove(ctx context.Context, call plugin.Call) (plugin.Result, error) {
	var a struct {
		ID string `json:"id"`
	}
	if err := decode(call.Args, &a); err != nil {
		return plugin.Result{}, err
	}
	if strings.TrimSpace(a.ID) == "" {
		return plugin.Result{}, errors.New("id is required")
	}
	now := p.host.Clock().Now()
	days, err := p.book.Range(ctx, logbook.Day(now.AddDate(0, 0, -maxBackDays), p.host.Location()), logbook.Day(now, p.host.Location()))
	if err != nil {
		return plugin.Result{}, err
	}
	for day, items := range days {
		for i, s := range items {
			if s.ID == a.ID {
				if err := p.book.Save(ctx, day, append(items[:i:i], items[i+1:]...)); err != nil {
					return plugin.Result{}, err
				}
				return plugin.Result{Content: "Removed that entry.", Summary: "removed a workout"}, nil
			}
		}
	}
	return plugin.Result{}, errors.New("no workout with that id in the last 60 days")
}

// PromptContext implements plugin.ContextProvider.
func (p *Plugin) PromptContext(ctx context.Context) string {
	now := p.host.Clock().Now()
	var b strings.Builder
	if st, err := p.weekStats(ctx, now); err == nil {
		pl := p.loadPlan(ctx)
		if st.sessions > 0 || pl.SessionsPerWeek > 0 {
			fmt.Fprintf(&b, "- Workouts this week: %s.\n", st.text(pl))
		}
	}
	b.WriteString("- Workout log: use workout__log_workout when the owner says what they did. Report counts and minutes, never grade them. No medical advice; for pain or a health concern suggest a professional, and never encourage pushing through pain.\n")
	return b.String()
}

// WeeklyDigest implements plugin.Digester.
func (p *Plugin) WeeklyDigest(ctx context.Context) string {
	st, err := p.weekStats(ctx, p.host.Clock().Now())
	pl := p.loadPlan(ctx)
	if err != nil || (st.sessions == 0 && pl.SessionsPerWeek == 0) {
		return ""
	}
	return "Workouts this week: " + st.text(pl) + "."
}
