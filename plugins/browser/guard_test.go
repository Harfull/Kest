package browser

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Harfull/Kest/internal/plugin"
	"github.com/Harfull/Kest/internal/plugin/plugintest"
)

func testHost(t *testing.T) plugin.Host {
	t.Helper()
	return plugintest.NewHost()
}

// shopView is a plugin that has just read a shop page.
func shopView() *Plugin {
	p := New().(*Plugin)
	p.view.update(result{Tab: 1, Page: &pageInfo{
		URL: "https://shop.example.com/items/7",
		Elements: []element{
			{Ref: "e1", Tag: "a", Label: "Reviews", Href: "https://shop.example.com/items/7/reviews", Visible: true},
			{Ref: "e2", Tag: "button", Label: "Add to cart", Visible: true},
			{Ref: "e3", Tag: "button", Label: "Buy now", Visible: true},
			{Ref: "e4", Tag: "input", Type: "text", Label: "Search", Visible: true},
			{Ref: "e5", Tag: "input", Type: "password", Label: "Password", Visible: true},
			{Ref: "e6", Tag: "a", Label: "Manufacturer", Href: "https://maker.example.org/", Visible: true},
			{Ref: "e7", Tag: "input", Type: "text", Label: "Card number", Visible: true},
			{Ref: "e8", Tag: "button", Label: "Details", Disabled: true, Visible: true},
		},
	}})
	return p
}

func call(t *testing.T, args any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestApprovalPromptNamesTheElementNotTheRef(t *testing.T) {
	p := shopView()
	for _, tt := range []struct {
		action string
		args   arguments
		want   []string
	}{
		{"click_element", arguments{Ref: "e2"}, []string{"Click", "button", "Add to cart", "shop.example.com"}},
		{"click_element", arguments{Ref: "e1"}, []string{"link", "Reviews", "/items/7/reviews"}},
		{"fill_field", arguments{Ref: "e4", Text: "red shoes", Submit: true}, []string{"red shoes", "Search", "press Enter"}},
		{"fill_field", arguments{Ref: "e5", Text: "hunter2"}, []string{"(hidden)", "Password"}},
		{"fill_field", arguments{Ref: "e7", Text: "4111111111111111"}, []string{"(hidden)", "Card number"}},
		{"navigate_page", arguments{URL: "https://example.org/x", NewTab: true}, []string{"Open https://example.org/x", "new tab"}},
		{"click_element", arguments{Ref: "e99"}, []string{"element e99"}},
		{"click_element", arguments{Selector: "#go"}, []string{"#go"}},
		{"click_position", arguments{X: 4, Y: 9}, []string{"(4, 9)"}},
	} {
		got := p.describe(tt.action)(call(t, tt.args))
		for _, want := range tt.want {
			if !strings.Contains(got, want) {
				t.Errorf("%s %+v: %q lacks %q", tt.action, tt.args, got, want)
			}
		}
		if strings.Contains(got, "hunter2") || strings.Contains(got, "4111") {
			t.Errorf("approval prompt shows a secret: %q", got)
		}
	}
}

func TestTaskGrantStopsAtWhatCommitsOrLeavesTheSite(t *testing.T) {
	p := shopView()
	for _, tt := range []struct {
		name, action string
		args         arguments
		want         bool
	}{
		{"same site link", "click_element", arguments{Ref: "e1"}, true},
		{"reversible button", "click_element", arguments{Ref: "e2"}, true},
		{"buy", "click_element", arguments{Ref: "e3"}, false},
		{"search box", "fill_field", arguments{Ref: "e4", Text: "red shoes"}, true},
		{"password field", "fill_field", arguments{Ref: "e5", Text: "x"}, false},
		{"card field", "fill_field", arguments{Ref: "e7", Text: "x"}, false},
		{"secret looking text", "fill_field", arguments{Ref: "e4", Text: "4111 1111 1111 1111"}, false},
		{"link to another site", "click_element", arguments{Ref: "e6"}, false},
		{"disabled control", "click_element", arguments{Ref: "e8"}, false},
		{"selector instead of ref", "click_element", arguments{Selector: "#go"}, false},
		{"unknown ref", "click_element", arguments{Ref: "e99"}, false},
		{"blind click", "click_position", arguments{X: 5, Y: 5}, false},
		{"same site navigation", "navigate_page", arguments{URL: "https://www.shop.example.com/other"}, true},
		{"sibling subdomain of the same site", "navigate_page", arguments{URL: "https://cdn.example.com/"}, true},
		{"other site", "navigate_page", arguments{URL: "https://evil.example.net/?q=secret"}, false},
		{"back", "navigate_history", arguments{Direction: "back"}, true},
		{"reload can resend a form", "navigate_history", arguments{Direction: "reload"}, false},
		{"enter on the page", "press_key", arguments{Key: "Enter"}, true},
		{"enter on a buy button", "press_key", arguments{Key: "Enter", Ref: "e3"}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := p.covers(tt.action)(context.Background(), call(t, tt.args)); got != tt.want {
				t.Fatalf("covers = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTaskGrantNeedsAPageToStayOn(t *testing.T) {
	p := New().(*Plugin)
	if p.covers("navigate_page")(context.Background(), call(t, arguments{URL: "https://example.com"})) {
		t.Fatal("the first navigation of a session was covered by a grant")
	}
}

func TestSiteComparison(t *testing.T) {
	for _, tt := range []struct {
		a, b string
		same bool
	}{
		{"https://example.com/a", "https://www.example.com/b", true},
		{"https://news.example.com/", "https://shop.example.com/", true},
		{"https://example.com/", "https://example.org/", false},
		{"https://a.example.co.uk/", "https://b.example.co.uk/", true},
		{"https://example.co.uk/", "https://other.co.uk/", false},
		{"http://10.0.0.1/", "http://10.0.1.1/", false},
		{"not a url", "https://example.com", false},
	} {
		if got := sameSite(tt.a, tt.b); got != tt.same {
			t.Errorf("sameSite(%q, %q) = %v", tt.a, tt.b, got)
		}
	}
}

func TestViewForgetsRefsWhenThePageChanges(t *testing.T) {
	p := shopView()
	p.view.update(result{Tab: 1, Page: &pageInfo{URL: "https://shop.example.com/cart", Elements: []element{{Ref: "e0", Tag: "button", Label: "Pay"}}}})
	if _, _, known := p.view.lookup(1, "e2"); known {
		t.Fatal("a ref from the previous page is still trusted")
	}
	if _, el, known := p.view.lookup(0, "e0"); !known || el.Label != "Pay" {
		t.Fatal("the active tab's current refs are missing")
	}
	// Reading more of the same page keeps the refs the owner has already seen.
	p.view.update(result{Tab: 1, Page: &pageInfo{URL: "https://shop.example.com/cart", Elements: []element{{Ref: "e1", Tag: "a", Label: "Help"}}}})
	if _, _, known := p.view.lookup(1, "e0"); !known {
		t.Fatal("refs were lost when the same page was scrolled")
	}
	p.view.update(result{Tabs: nil, Closed: 1})
	if _, _, known := p.view.lookup(1, "e0"); known {
		t.Fatal("closed tab is still remembered")
	}
}

func TestScreenshotRateLimit(t *testing.T) {
	p := New().(*Plugin)
	p.host = testHost(t)
	for i := range screenshotsPerMinute {
		if !p.allowShot() {
			t.Fatalf("shot %d refused early", i)
		}
	}
	if p.allowShot() {
		t.Fatal("screenshots are not rate limited")
	}
}
