package app

import (
	"strings"
	"testing"

	"github.com/Harfull/Kest/internal/telegram"
)

// The coaching plugins keep the owner's data and show it to the model on later
// turns, without any tool call.
func TestWorkoutLogReachesThePromptOnTheNextTurn(t *testing.T) {
	e := startWith(t, enable("workout", "nutrition", "focus"))
	e.router.script.Store(callThenEcho("workout__log_workout", `{"kind":"run","minutes":30}`))

	e.send(telegram.Update{Text: "I ran for half an hour"})
	if got := e.lastText(t, 1); !strings.Contains(got, "Logged: run, 30 minutes on 2026-10-11") {
		t.Fatalf("reply = %q", got)
	}

	e.router.script.Store(func(chatRequest) string { return textMessage("ok") })
	e.send(telegram.Update{Text: "how is my week going?"})
	e.lastText(t, 2)
	body, _ := e.router.lastChat.Load().(string)
	for _, want := range []string{"Workouts this week: 1 session(s), 30 minutes", "workout__log_workout", "Never give calorie or weight targets", "focus__start_focus"} {
		if !strings.Contains(body, want) {
			t.Errorf("the model was not shown %q", want)
		}
	}
}

func TestEveryNewPluginLoadsWithItsDefaults(t *testing.T) {
	// mailbox needs host names, gmail needs nothing until used, notion needs nothing.
	e := startWith(t, enable("focus", "workout", "nutrition", "gmail", "notion"))
	e.send(telegram.Update{Command: "status"})
	got := e.lastText(t, 1)
	for _, id := range []string{"focus", "workout", "nutrition", "gmail", "notion"} {
		if !strings.Contains(got, id) {
			t.Errorf("/status does not list %s:\n%s", id, got)
		}
	}
	if strings.Contains(got, "Could not load") {
		t.Errorf("a plugin failed to load:\n%s", got)
	}
}
