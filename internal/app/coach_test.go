package app

import (
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Harfull/Kest/internal/agent"
	"github.com/Harfull/Kest/internal/clock"
	"github.com/Harfull/Kest/internal/plugin"
	"github.com/Harfull/Kest/internal/scheduler"
	"github.com/Harfull/Kest/internal/store"
	"github.com/Harfull/Kest/internal/telegram"
)

const walkHabit = `{"name":"Walk after lunch","behavior":"Walk for 10 minutes","tiny_version":"Put on shoes and step outside","anchor":"after lunch"}`

func TestModelCanSetUpAndListHabits(t *testing.T) {
	e := start(t)
	e.router.script.Store(func(req chatRequest) string {
		role, content := req.last()
		switch {
		case role == "tool":
			return textMessage("Result: " + content)
		case strings.Contains(content, "list"):
			return toolCallMessage("c2", "coach__list_habits", `{}`)
		}
		return toolCallMessage("c1", "coach__create_habit", walkHabit)
	})

	e.send(telegram.Update{Text: "I want to walk after lunch"})
	if got := e.lastText(t, 1); strings.Contains(got, "Error:") {
		t.Fatalf("create_habit failed: %s", got)
	}

	e.send(telegram.Update{Text: "list my habits"})
	if got := e.lastText(t, 2); !strings.Contains(got, "Walk after lunch") {
		t.Errorf("list_habits = %q, want the new habit", got)
	}
}

func TestSystemPromptCarriesCoachGuidance(t *testing.T) {
	e := start(t)
	var system string
	e.router.script.Store(func(req chatRequest) string {
		system = req.Messages[0].Content
		return textMessage("ok")
	})

	e.send(telegram.Update{Text: "hello"})
	e.lastText(t, 1)

	if !strings.Contains(system, "Habit coaching") || !strings.Contains(system, "Check-ins:") {
		t.Errorf("system prompt lacks the coach's context:\n%s", system)
	}
}

func TestCoachButtonsAreHandledAndCleared(t *testing.T) {
	e := start(t)

	e.send(telegram.Update{Callback: &telegram.Callback{ID: "cb1", Data: "e:r", MessageID: 7}})

	if got := e.lastText(t, 1); got == "" {
		t.Fatal("no reply to a coach button")
	}
	if cleared := e.fake.Cleared(); len(cleared) != 1 || cleared[0] != 7 {
		t.Errorf("cleared buttons = %v, want [7]", cleared)
	}
}

func TestSettingsCommandShowsCheckIns(t *testing.T) {
	e := start(t)

	e.send(telegram.Update{Command: "settings"})

	got := e.lastText(t, 1)
	for _, want := range []string{"Check-ins:", "Quiet hours: 23:00 to 07:00", "just tell me"} {
		if !strings.Contains(got, want) {
			t.Errorf("/settings missing %q:\n%s", want, got)
		}
	}
}

func TestOwnerCanSwitchCheckInsOffByAsking(t *testing.T) {
	e := start(t)
	e.router.script.Store(callThenEcho("coach__set_preferences", `{"evening":false,"max_per_day":2}`))

	e.send(telegram.Update{Text: "fewer messages please, and no evening check-in"})
	e.lastText(t, 1)

	e.send(telegram.Update{Command: "settings"})
	got := e.lastText(t, 2)
	if !strings.Contains(got, "evening off") || !strings.Contains(got, "Up to 2 a day") {
		t.Errorf("/settings after the change:\n%s", got)
	}
}

func TestLockdownRefusesWritesButAllowsReads(t *testing.T) {
	e := start(t)
	e.router.script.Store(func(req chatRequest) string {
		role, content := req.last()
		switch {
		case role == "tool":
			return textMessage("Result: " + content)
		case strings.Contains(content, "list"):
			return toolCallMessage("c2", "coach__list_habits", `{}`)
		}
		return toolCallMessage("c1", "coach__create_habit", walkHabit)
	})

	e.send(telegram.Update{Command: "lockdown"})
	if got := e.lastText(t, 1); !strings.Contains(got, "Lockdown is on") {
		t.Fatalf("/lockdown reply = %q", got)
	}

	e.send(telegram.Update{Text: "start a walking habit"})
	if got := e.lastText(t, 2); !strings.Contains(got, "lockdown is on") {
		t.Errorf("a write went through in lockdown: %q", got)
	}

	e.send(telegram.Update{Text: "list my habits"})
	if got := e.lastText(t, 3); strings.Contains(got, "lockdown") || strings.Contains(got, "Error:") {
		t.Errorf("a read was blocked in lockdown: %q", got)
	}
}

func TestNotifierStaysQuietInLockdown(t *testing.T) {
	fake := telegram.NewFake()
	c := &chat{tr: fake, ownerChat: 1, agent: agent.New(agent.Options{})}

	if err := c.notify(context.Background(), plugin.Notification{Text: "Reminder: stretch"}); err != nil {
		t.Fatal(err)
	}
	c.agent.SetLockdown(true)
	if err := c.notify(context.Background(), plugin.Notification{Text: "Reminder: drink water"}); err != nil {
		t.Fatal(err)
	}

	sent := fake.Snapshot()
	if len(sent) != 1 || sent[0].Text != "Reminder: stretch" {
		t.Errorf("sent = %+v, want only the message from before lockdown", sent)
	}
}

func TestCoachJobIsScheduledOnceAndHiddenFromStatus(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "kest.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close() //nolint:errcheck // test cleanup

	sch := scheduler.New(db, clock.NewFake(epoch), slog.New(slog.DiscardHandler), scheduler.Options{})
	for range 2 { // a restart must not add a second job
		if err := ensureCoachJob(ctx, sch, "UTC"); err != nil {
			t.Fatalf("ensureCoachJob() = %v", err)
		}
	}
	jobs, err := sch.Upcoming(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	var n int
	for _, j := range jobs {
		if j.Name == coachJob {
			n++
		}
	}
	if n != 1 {
		t.Errorf("%d %s jobs, want 1", n, coachJob)
	}

	e := start(t)
	e.send(telegram.Update{Command: "status"})
	if got := e.lastText(t, 1); strings.Contains(got, coachJob) {
		t.Errorf("/status lists the internal tick job:\n%s", got)
	}
}
