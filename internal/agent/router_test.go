package agent

import (
	"fmt"
	"slices"
	"testing"

	"github.com/Harfull/Kest/internal/plugin"
)

func ref(pluginID, name, desc string) plugin.ToolRef {
	return plugin.ToolRef{Plugin: pluginID, Tool: plugin.Tool{Name: name, Description: desc}}
}

// bigToolbox returns the always-on tools plus many filler tools and a few real-looking ones.
func bigToolbox() []plugin.ToolRef {
	refs := []plugin.ToolRef{
		ref("memory", "remember", "Save a fact about the owner."),
		ref("coach", "log_habit", "Record how a habit went."),
		ref("reminders", "create_reminder", "Create a reminder at a time."),
		ref("gmail", "search_threads", "Search the owner's email for messages."),
		ref("notion", "create_page", "Create a page in the notes workspace."),
	}
	for i := range 30 {
		refs = append(refs, ref(fmt.Sprintf("filler%d", i), "do_thing", "Does something unrelated and obscure."))
	}
	return refs
}

func wires(refs []plugin.ToolRef) []string {
	var out []string
	for _, r := range refs {
		out = append(out, r.Wire())
	}
	return out
}

func TestSmallToolSetsAreNeverFiltered(t *testing.T) {
	refs := bigToolbox()[:routeThreshold]
	if got := routeTools(refs, "nothing relevant"); len(got) != len(refs) {
		t.Errorf("routed %d of %d tools below the threshold", len(got), len(refs))
	}
}

func TestLargeToolSetsKeepAlwaysOnAndMatches(t *testing.T) {
	refs := bigToolbox()

	got := wires(routeTools(refs, "remind me to call mum tomorrow"))
	for _, want := range []string{"memory__remember", "coach__log_habit", "reminders__create_reminder"} {
		if !slices.Contains(got, want) {
			t.Errorf("tools = %v, missing %s", got, want)
		}
	}
	for _, unwanted := range []string{"gmail__search_threads", "notion__create_page"} {
		if slices.Contains(got, unwanted) {
			t.Errorf("tools = %v, should not include %s for a reminder", got, unwanted)
		}
	}
}

func TestMatchingIsByWordAndIgnoresPlurals(t *testing.T) {
	refs := bigToolbox()
	if got := wires(routeTools(refs, "any new emails in my inbox?")); !slices.Contains(got, "gmail__search_threads") {
		t.Errorf("tools = %v, want the email tool for a question about emails", got)
	}
	if got := wires(routeTools(refs, "add this to my notes")); !slices.Contains(got, "notion__create_page") {
		t.Errorf("tools = %v, want the notes tool", got)
	}
}

func TestAnUnrelatedMessageGetsOnlyTheAlwaysOnTools(t *testing.T) {
	got := routeTools(bigToolbox(), "how are you today")
	for _, r := range got {
		if !alwaysOn(r.Plugin) {
			t.Errorf("unexpected tool %s for small talk", r.Wire())
		}
	}
	if len(got) != 2 {
		t.Errorf("got %d tools, want the 2 always-on ones", len(got))
	}
}

func TestRoutingIsBoundedAndDeterministic(t *testing.T) {
	refs := bigToolbox()
	for i := range 40 {
		refs = append(refs, ref(fmt.Sprintf("extra%d", i), "search", "Search for things and stuff."))
	}
	first := wires(routeTools(refs, "search for things"))
	if len(first) > 2+routeExtra {
		t.Errorf("%d tools sent; the cap is %d", len(first), 2+routeExtra)
	}
	if second := wires(routeTools(refs, "search for things")); !slices.Equal(first, second) {
		t.Error("routing changed between identical calls")
	}
}
