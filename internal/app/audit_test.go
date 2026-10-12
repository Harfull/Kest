package app

import (
	"strings"
	"testing"

	"github.com/Harfull/Kest/internal/telegram"
)

func TestAuditCommandListsWhatKestDid(t *testing.T) {
	e := start(t)

	e.send(telegram.Update{Command: "audit"})
	if got := e.lastText(t, 1); got != msgAuditEmpty {
		t.Errorf("empty /audit = %q", got)
	}

	e.router.script.Store(callThenEcho("coach__list_habits", `{}`))
	e.send(telegram.Update{Text: "what habits do I have"})
	e.lastText(t, 2)

	e.send(telegram.Update{Command: "audit"})
	got := e.lastText(t, 3)
	if !strings.Contains(got, "coach.list_habits (read)") || !strings.Contains(got, "ok") {
		t.Errorf("/audit after a tool call:\n%s", got)
	}
}

func TestLockdownRefusalIsAudited(t *testing.T) {
	e := start(t)
	e.router.script.Store(callThenEcho("coach__create_habit", walkHabit))

	e.send(telegram.Update{Command: "lockdown"})
	e.lastText(t, 1)
	e.send(telegram.Update{Text: "start a walking habit"})
	e.lastText(t, 2)

	e.send(telegram.Update{Command: "audit"})
	got := e.lastText(t, 3)
	if !strings.Contains(got, "coach.create_habit (write)") || !strings.Contains(got, "declined") || !strings.Contains(got, "lockdown") {
		t.Errorf("/audit after a refused write:\n%s", got)
	}
}
