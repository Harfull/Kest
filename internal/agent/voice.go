package agent

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

// bannedPhrases come from docs/persona/voice.md ("Words and habits to avoid").
// Matching is case-insensitive on whole words or phrases.
var bannedPhrases = []string{
	// too formal
	"kindly", "i would like to inform you", "per your request", "utilize",
	// too slangy
	"bestie", "no cap", "slay", "rizz", "lowkey", "fr fr", "bruh", "vibes", "it's giving", "touch grass",
	// pushy or shaming
	"you must", "you need to", "you failed", "lazy", "excuses", "no pain no gain", "don't break the chain",
	// hollow cheer
	"let's goooo", "you've got this",
	// filler openers
	"great question", "i hope this finds you well",
	// guilt and loss framing
	"streak",
}

var bannedRes = func() []*regexp.Regexp {
	out := make([]*regexp.Regexp, len(bannedPhrases))
	for i, p := range bannedPhrases {
		out[i] = regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(p) + `\b`)
	}
	return out
}()

// CheckVoice reports places where text breaks the voice guide. An empty result
// means the text is within the rules. It is used by tests and the evaluation
// harness today and by the coach's message validator later.
//
// maxWords <= 0 disables the length check.
func CheckVoice(text string, maxWords int) []string {
	var v []string

	for i, re := range bannedRes {
		if re.MatchString(text) {
			v = append(v, fmt.Sprintf("uses %q", bannedPhrases[i]))
		}
	}
	if n := strings.Count(text, "!"); n > 1 {
		v = append(v, fmt.Sprintf("has %d exclamation marks (at most 1)", n))
	}
	if hasEmoji(text) {
		v = append(v, "contains emoji")
	}
	if maxWords > 0 {
		if n := len(strings.Fields(text)); n > maxWords {
			v = append(v, fmt.Sprintf("is %d words (limit %d)", n, maxWords))
		}
	}
	return v
}

func hasEmoji(s string) bool {
	for _, r := range s {
		switch {
		case r >= 0x1F300 && r <= 0x1FAFF, // pictographs, emoticons, symbols
			r >= 0x2600 && r <= 0x27BF, // misc symbols and dingbats
			r == 0x2B50, r == 0x2B55, r == 0x231A, r == 0x231B,
			unicode.Is(unicode.Variation_Selector, r) && r == 0xFE0F:
			return true
		}
	}
	return false
}
