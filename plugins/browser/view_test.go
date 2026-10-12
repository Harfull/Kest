package browser

import (
	"strings"
	"testing"
)

func TestFormatPageShowsPositionTextAndRefs(t *testing.T) {
	yes := true
	r := result{Tab: 2, OpenedTab: true, Scope: "viewport", Notes: []string{"A confirm dialog was dismissed: Leave?"}, Page: &pageInfo{
		URL: "https://shop.example.com/items", Title: "Items", Text: "Red shoes\n$40", Total: 13,
		Scroll: scrollInfo{Y: 1600, Height: 4000, Viewport: 800},
		Elements: []element{
			{Ref: "e1", Tag: "a", Label: "Red shoes", Href: "https://shop.example.com/items/7", Visible: true},
			{Ref: "e2", Tag: "input", Type: "checkbox", Label: "In stock", Checked: &yes, Visible: true},
			{Ref: "e3", Tag: "select", Label: "Size", Options: []string{"8", "9*", "10"}, Visible: true},
			{Ref: "e4", Tag: "input", Type: "password", Label: "Password"},
			{Ref: "f1e0", Tag: "button", Label: "Play", Disabled: true, Visible: true},
		},
		Frames: []frameInfo{{Frame: 1, URL: "https://video.example.net/embed", Text: "Video player", Elements: []element{{Ref: "f1e1", Tag: "button", Label: "Mute", Visible: true}}}},
	}}
	got := formatPage(r)
	for _, want := range []string{
		"Note: A confirm dialog was dismissed", "Tab 2 (a new tab opened): https://shop.example.com/items", "Title: Items",
		"Position: 40% down (screen shows 1600 to 2400 of 4000 px)", "Text on screen right now:", "Red shoes",
		`[e1] link "Red shoes" -> /items/7`, `[e2] checkbox "In stock" (checked)`, "options: 8 | 9* | 10",
		"[e4] password field", "(off screen)", "(disabled)", "Frame 1 (https://video.example.net/embed)", `[f1e1] button "Mute"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("page lacks %q:\n%s", want, got)
		}
	}
}

func TestFormatPagePagesLongText(t *testing.T) {
	r := result{Tab: 1, Scope: "page", Offset: 0, Page: &pageInfo{URL: "https://example.com/", Text: strings.Repeat("a", 100), Total: 450, Scroll: scrollInfo{Height: 700, Viewport: 800}}}
	got := formatPage(r)
	for _, want := range []string{"characters 0 to 100 of 450", "read_page with offset=100", "the whole page fits on screen"} {
		if !strings.Contains(got, want) {
			t.Errorf("lacks %q:\n%s", want, got)
		}
	}
	r.Offset = 400
	r.Page.Text = strings.Repeat("a", 50)
	if got := formatPage(r); strings.Contains(got, "more text follows") {
		t.Errorf("last page of text offers more:\n%s", got)
	}
}

func TestFormatPageReportsScrollThatDidNotMove(t *testing.T) {
	no := false
	got := formatPage(result{Tab: 1, Scope: "viewport", Moved: &no, Page: &pageInfo{URL: "https://example.com/", Scroll: scrollInfo{Y: 3200, Height: 4000, Viewport: 800}}})
	for _, want := range []string{"did not move", "bottom of the page", "(no text)", "No clickable or editable elements on screen"} {
		if !strings.Contains(got, want) {
			t.Errorf("lacks %q:\n%s", want, got)
		}
	}
}

func TestFormatFind(t *testing.T) {
	got := formatPage(result{Tab: 1, Scope: "viewport", Find: &findInfo{Count: 7, Index: 0, Snippets: []string{"the price is $40"}}, Page: &pageInfo{URL: "https://example.com/"}})
	for _, want := range []string{"7 match(es)", "the price is $40", "use find_text with index"} {
		if !strings.Contains(got, want) {
			t.Errorf("lacks %q:\n%s", want, got)
		}
	}
	got = formatPage(result{Tab: 1, Scope: "viewport", Find: &findInfo{Count: 0, Index: -1}, Page: &pageInfo{URL: "https://example.com/"}})
	if !strings.Contains(got, "no visible match") {
		t.Errorf("empty find: %s", got)
	}
}

func TestPageTextCannotInjectControlCharacters(t *testing.T) {
	got := formatPage(result{Tab: 1, Notes: []string{"bad\x1b[31m note"}, Page: &pageInfo{URL: "https://example.com/\x07", Title: "t\x00itle"}})
	if strings.ContainsAny(got, "\x1b\x07\x00") {
		t.Fatalf("control characters survived: %q", got)
	}
}

func TestFormatTabs(t *testing.T) {
	got := formatTabs(result{Closed: 2, Tabs: []tabInfo{{Tab: 1, URL: "https://a.example/", Title: "A", Active: true}}})
	for _, want := range []string{"Closed tab 2", "1 (active): A - https://a.example/"} {
		if !strings.Contains(got, want) {
			t.Errorf("lacks %q:\n%s", want, got)
		}
	}
	if got := formatTabs(result{}); got != "No tabs are open." {
		t.Errorf("empty = %q", got)
	}
}
