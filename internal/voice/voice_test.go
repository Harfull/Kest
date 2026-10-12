package voice

import (
	"strings"
	"testing"
)

func TestCheck(t *testing.T) {
	good := []string{
		"Morning. About seven hours last night by my guess. First up today: the report draft?",
		"Your walk window opens now. Ten minutes still counts.",
		"That happens. What would make the next meal easy to get right?",
		"Yesterday didn't happen, and that's fine. Want the two-minute version today?",
	}
	for _, s := range good {
		if v := Check(s, 40); len(v) != 0 {
			t.Errorf("Check(%q) = %v, want none", s, v)
		}
	}

	bad := map[string]string{
		"Kindly note that your session is due.": "kindly",
		"yo bestie go touch grass rn":           "bestie",
		"You must exercise today.":              "you must",
		"Don't break your streak!":              "streak",
		"Great question! Let's get going.":      "great question",
		"Amazing!! You did it!!":                "exclamation",
		"Nice work \U0001F4AA":                  "emoji",
		strings.Repeat("word ", 50):             "words",
	}
	for text, want := range bad {
		v := Check(text, 40)
		if len(v) == 0 || !strings.Contains(strings.Join(v, ";"), want) {
			t.Errorf("Check(%.30q) = %v, want a violation mentioning %q", text, v, want)
		}
	}

	// "slay" must not match inside "relay".
	if v := Check("I'll relay that message.", 0); len(v) != 0 {
		t.Errorf("whole-word matching failed: %v", v)
	}
}
