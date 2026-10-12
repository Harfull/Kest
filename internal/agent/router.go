package agent

import (
	"sort"
	"strings"
	"unicode"

	"github.com/Harfull/Kest/internal/plugin"
)

// Tool routing keeps prompts small when many plugins are loaded. Every tool
// definition costs tokens on every model call, and a model shown 100 tools
// picks worse than one shown 15. With a handful of tools nothing is filtered.
const (
	// routeThreshold is the tool count up to which everything is sent.
	routeThreshold = 24
	// routeExtra is how many matching tools are added to the always-on set.
	routeExtra = 14
)

// alwaysOn are the plugins whose tools are sent on every turn: what Kest
// remembers about the owner, and the coaching tools the daily rhythm depends on.
func alwaysOn(pluginID string) bool { return pluginID == "memory" || pluginID == "coach" }

// routeTools returns the tools to show the model for a message. It is
// deterministic and makes no model call: it scores each tool by how many words
// of the message appear in its plugin id, name and description, and keeps the
// best. A tool the message never mentions is hidden until the owner's wording
// gets near it, which is the trade for a smaller prompt.
func routeTools(refs []plugin.ToolRef, message string) []plugin.ToolRef {
	if len(refs) <= routeThreshold {
		return refs
	}
	words := wordSet(message)

	type scored struct {
		ref   plugin.ToolRef
		score int
	}
	var keep []plugin.ToolRef
	var rest []scored
	for _, r := range refs {
		if alwaysOn(r.Plugin) {
			keep = append(keep, r)
			continue
		}
		score := 0
		for w := range wordSet(r.Plugin + " " + strings.ReplaceAll(r.Tool.Name, "_", " ")) {
			if words[w] {
				score += 3 // a name or plugin match is a strong signal
			}
		}
		for w := range wordSet(r.Tool.Description) {
			if words[w] {
				score++
			}
		}
		if score > 0 {
			rest = append(rest, scored{r, score})
		}
	}

	sort.SliceStable(rest, func(i, j int) bool { return rest[i].score > rest[j].score })
	for _, s := range rest[:min(len(rest), routeExtra)] {
		keep = append(keep, s.ref)
	}
	sortRefs(keep)
	return keep
}

// isStopWord reports common words that say nothing about which tool is meant.
func isStopWord(w string) bool {
	switch w {
	case "the", "and", "for", "with", "that", "this", "from", "your", "you", "can", "will", "when", "what", "have", "has", "are", "was",
		"not", "any", "all", "use", "used", "set", "get", "give", "please", "tool", "into", "about", "then", "just", "also", "its":
		return true
	}
	return false
}

// stem removes a common ending so "reminders", "reminder" and "reminding" meet
// at "remind". It is deliberately crude: both sides of a match go through it.
func stem(w string) string {
	for _, suffix := range []string{"ing", "ers", "er", "ed", "es", "s"} {
		if strings.HasSuffix(w, suffix) && len(w)-len(suffix) >= 4 {
			return strings.TrimSuffix(w, suffix)
		}
	}
	return w
}

// wordSet splits text into lower-case words of three or more letters, stemmed.
func wordSet(s string) map[string]bool {
	out := map[string]bool{}
	for _, w := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		if len(w) < 3 || isStopWord(w) {
			continue
		}
		out[stem(w)] = true
	}
	return out
}
