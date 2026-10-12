package app

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Harfull/Kest/internal/clock"
	"github.com/Harfull/Kest/internal/config"
	"github.com/Harfull/Kest/internal/plugin"
	"github.com/Harfull/Kest/internal/store"
	"github.com/Harfull/Kest/internal/telegram"
)

// dangerPlugin offers one destructive tool and counts how often it really ran.
type dangerPlugin struct{ runs *atomic.Int32 }

var dangerManifest = plugin.MustParseManifest(`id = "danger"
name = "Danger"
version = "1"
description = "A destructive tool for tests."
[[tools]]
name = "wipe"
risk = "destructive"`)

func (d dangerPlugin) Manifest() plugin.Manifest               { return dangerManifest }
func (d dangerPlugin) Init(context.Context, plugin.Host) error { return nil }
func (d dangerPlugin) Close(context.Context) error             { return nil }
func (d dangerPlugin) Tools() []plugin.Tool {
	return []plugin.Tool{{
		Name: "wipe", Description: "Wipe everything.", Risk: plugin.RiskDestructive,
		Schema: json.RawMessage(`{"type":"object","properties":{"target":{"type":"string"}},"additionalProperties":false}`),
		Handler: func(context.Context, plugin.Call) (plugin.Result, error) {
			d.runs.Add(1)
			return plugin.Result{Content: "wiped"}, nil
		},
	}}
}

func withDanger(runs *atomic.Int32) Option {
	return WithPlugins(map[string]plugin.Factory{"danger": func() plugin.Plugin { return dangerPlugin{runs} }})
}

func enable(ids ...string) func(*config.Config) {
	return func(c *config.Config) { c.EnabledPlugins = ids }
}

// callThenEcho scripts the model: the first request becomes a tool call, and
// once a tool result arrives the model repeats it as its final answer.
func callThenEcho(name, args string) func(chatRequest) string {
	return func(req chatRequest) string {
		if role, content := req.last(); role == "tool" {
			return textMessage("Result: " + content)
		}
		return toolCallMessage("call_1", name, args)
	}
}

func TestToolCallRunsThroughTheRealApp(t *testing.T) {
	e := startWith(t, enable("tasks"))
	e.router.script.Store(callThenEcho("tasks__add_task", `{"title":"buy running shoes","due":"2026-10-20"}`))

	e.send(telegram.Update{Text: "add a task to buy running shoes"})

	got := e.lastText(t, 1)
	if got != "Result: Added task 1: buy running shoes (due 2026-10-20)" {
		t.Errorf("reply = %q", got)
	}
	// A clean write needs no approval prompt.
	if n := len(e.fake.Snapshot()); n != 1 {
		t.Errorf("%d messages sent, want just the answer", n)
	}
}

func TestDestructiveToolWaitsForApproval(t *testing.T) {
	for _, approve := range []bool{true, false} {
		name := map[bool]string{true: "approved", false: "skipped"}[approve]
		t.Run(name, func(t *testing.T) {
			var runs atomic.Int32
			e := startWith(t, enable("danger"), withDanger(&runs))
			e.router.script.Store(callThenEcho("danger__wipe", `{"target":"everything"}`))

			e.send(telegram.Update{Text: "wipe it"})

			// The approval prompt shows the exact arguments and two buttons.
			prompt := e.lastText(t, 1)
			if !strings.Contains(prompt, "danger.wipe") || !strings.Contains(prompt, "target: everything") {
				t.Errorf("prompt = %q", prompt)
			}
			r := e.fake.Snapshot()[0]
			if len(r.Buttons) != 1 || len(r.Buttons[0]) != 2 {
				t.Fatalf("buttons = %+v", r.Buttons)
			}
			if runs.Load() != 0 {
				t.Fatal("tool ran before the owner answered")
			}

			data := r.Buttons[0][0].Data // Go ahead
			if !approve {
				data = r.Buttons[0][1].Data // Skip
			}
			e.send(telegram.Update{Callback: &telegram.Callback{ID: "cb", Data: data, MessageID: 1}})

			final := e.lastText(t, 2)
			if approve && (final != "Result: wiped" || runs.Load() != 1) {
				t.Errorf("approved: reply %q, runs %d", final, runs.Load())
			}
			if !approve && (!strings.Contains(final, "did not approve") || runs.Load() != 0) {
				t.Errorf("skipped: reply %q, runs %d", final, runs.Load())
			}
			if cleared := e.fake.Cleared(); len(cleared) != 1 || cleared[0] != 1 {
				t.Errorf("buttons not removed after the decision: %v", cleared)
			}
		})
	}
}

func TestUnansweredApprovalIsSkipped(t *testing.T) {
	var runs atomic.Int32
	e := startWith(t, enable("danger"), withDanger(&runs), WithApprovalTimeout(150*time.Millisecond))
	e.router.script.Store(callThenEcho("danger__wipe", `{}`))

	e.send(telegram.Update{Text: "wipe it"})

	waitFor(t, "timeout notice", func() bool {
		for _, r := range e.fake.Snapshot() {
			if r.Text == msgApprovalTimedOut {
				return true
			}
		}
		return false
	})
	if runs.Load() != 0 {
		t.Error("a destructive tool ran without any answer")
	}

	// A late press does nothing and the owner is told why.
	r := e.fake.Snapshot()[0]
	e.send(telegram.Update{Callback: &telegram.Callback{ID: "late", Data: r.Buttons[0][0].Data, MessageID: 1}})
	time.Sleep(50 * time.Millisecond)
	if runs.Load() != 0 {
		t.Error("a late approval ran the tool")
	}

	// The queue is free again: the next message is answered normally.
	e.router.script.Store(func(chatRequest) string { return textMessage("back to normal") })
	e.send(telegram.Update{Text: "hello?"})
	waitFor(t, "next answer", func() bool {
		s := e.fake.Snapshot()
		return s[len(s)-1].Text == "back to normal"
	})
}

func TestStrangersCannotApprove(t *testing.T) {
	// The transport drops non-owner updates before they reach the app; this
	// checks the app's own defense: an unknown action id does nothing.
	var runs atomic.Int32
	e := startWith(t, enable("danger"), withDanger(&runs), WithApprovalTimeout(300*time.Millisecond))
	e.router.script.Store(callThenEcho("danger__wipe", `{}`))

	e.send(telegram.Update{Text: "wipe it"})
	e.lastText(t, 1)

	e.send(telegram.Update{Callback: &telegram.Callback{ID: "x", Data: "act:ok:0000000000000000", MessageID: 1}})
	time.Sleep(400 * time.Millisecond) // let the real prompt expire
	if runs.Load() != 0 {
		t.Error("a forged approval id ran the tool")
	}
}

func TestReminderFiresAndIsDeliveredUrgently(t *testing.T) {
	// Real clock: the scheduler must actually wake up and deliver.
	e := startWith(t, enable("reminders"), WithClock(clock.Real{}))
	e.router.script.Store(func(req chatRequest) string {
		if role, content := req.last(); role == "tool" {
			return textMessage(content)
		}
		at := time.Now().UTC().Add(1500 * time.Millisecond).Format("2006-01-02T15:04:05")
		return toolCallMessage("c1", "reminders__create_reminder", fmt.Sprintf(`{"text":"stretch","at":%q}`, at))
	})

	e.send(telegram.Update{Text: "remind me to stretch in a moment"})

	waitFor(t, "reminder confirmation and delivery", func() bool {
		var set, fired bool
		for _, r := range e.fake.Snapshot() {
			set = set || strings.Contains(r.Text, "set for")
			fired = fired || r.Text == "Reminder: stretch"
		}
		return set && fired
	})
}

func TestStatusReportsPluginsAndFailures(t *testing.T) {
	e := startWith(t, enable("tasks", "ghost"))

	e.send(telegram.Update{Command: "status"})
	got := e.lastText(t, 1)

	for _, want := range []string{"Plugins: tasks.", "Could not load: ghost"} {
		if !strings.Contains(got, want) {
			t.Errorf("status missing %q:\n%s", want, got)
		}
	}
}

func TestBadPluginSettingsDisableOnlyThatPlugin(t *testing.T) {
	e := startWith(t, func(c *config.Config) {
		c.EnabledPlugins = []string{"tasks", "reminders"}
		c.Plugins = map[string]map[string]any{"tasks": {"max_open": "lots"}}
	})

	e.send(telegram.Update{Command: "status"})
	got := e.lastText(t, 1)
	if !strings.Contains(got, "Plugins: reminders.") || !strings.Contains(got, "Could not load: tasks") {
		t.Errorf("status = %s", got)
	}
}

func TestApprovalsLeftFromAPreviousRunAreCancelled(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(dir, "kest.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AddPending(ctx, store.PendingAction{
		ID: "stale0stale0stale", TS: epoch, Tool: "danger.wipe", Args: "{}", Summary: "x", ExpiresAt: epoch.Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	e := startWith(t, func(c *config.Config) { c.DataDir = dir })
	e.stop(t)

	db, err = store.Open(ctx, filepath.Join(dir, "kest.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close() //nolint:errcheck // test cleanup
	if st, _ := db.PendingState(ctx, "stale0stale0stale"); st != store.PendingCancelled {
		t.Errorf("stale approval state = %q, want cancelled", st)
	}
}

// secretPlugin reads one declared secret and tries one it never declared.
type secretPlugin struct {
	got        *string
	undeclared *error
}

var secretManifest = plugin.MustParseManifest(`id = "vault"
name = "Vault"
version = "1"
description = "Reads a secret for tests."
[capabilities]
secrets = ["VAULT_TOKEN"]
[[tools]]
name = "peek"
risk = "read"`)

func (p secretPlugin) Manifest() plugin.Manifest { return secretManifest }
func (p secretPlugin) Init(_ context.Context, h plugin.Host) error {
	*p.got, _ = h.Secret("VAULT_TOKEN")
	_, *p.undeclared = h.Secret("SOMEONE_ELSES_TOKEN")
	return nil
}
func (p secretPlugin) Close(context.Context) error { return nil }
func (p secretPlugin) Tools() []plugin.Tool {
	return []plugin.Tool{{
		Name: "peek", Description: "Peek.", Risk: plugin.RiskRead,
		Schema:  json.RawMessage(`{"type":"object"}`),
		Handler: func(context.Context, plugin.Call) (plugin.Result, error) { return plugin.Result{Content: "ok"}, nil },
	}}
}

func TestPluginSecretsComeFromThePrefixedEnvironmentAndOnlyIfDeclared(t *testing.T) {
	var got string
	var undeclared error
	env := map[string]string{
		"KEST_SECRET_VAULT_TOKEN":         "s3cret",
		"KEST_SECRET_SOMEONE_ELSES_TOKEN": "not for you",
		"VAULT_TOKEN":                     "wrong place",
	}
	lookup := func(name string) (string, bool) { v, ok := env[name]; return v, ok }

	startWith(t, enable("vault"),
		WithEnv(lookup),
		WithPlugins(map[string]plugin.Factory{"vault": func() plugin.Plugin { return secretPlugin{&got, &undeclared} }}),
	)

	if got != "s3cret" {
		t.Errorf("declared secret = %q, want the value of KEST_SECRET_VAULT_TOKEN", got)
	}
	if undeclared == nil {
		t.Error("the plugin read a secret it never declared")
	}
}
