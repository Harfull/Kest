package browser

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"unicode"
)

// result is what the worker reports after an action. Field names follow the
// worker's JSON.
type result struct {
	Tab        int       `json:"tab"`
	OpenedTab  bool      `json:"opened_tab"`
	Scope      string    `json:"scope"`
	Offset     int       `json:"offset"`
	Notes      []string  `json:"notes"`
	Moved      *bool     `json:"moved"`
	Find       *findInfo `json:"find"`
	Page       *pageInfo `json:"page"`
	Tabs       []tabInfo `json:"tabs"`
	Closed     int       `json:"closed"`
	Screenshot string    `json:"screenshot"`
	URL        string    `json:"url"`
	Title      string    `json:"title"`
}

type findInfo struct {
	Count    int      `json:"count"`
	Index    int      `json:"index"`
	Snippets []string `json:"snippets"`
}

type tabInfo struct {
	Tab    int    `json:"tab"`
	URL    string `json:"url"`
	Title  string `json:"title"`
	Active bool   `json:"active"`
}

type scrollInfo struct {
	Y        int `json:"y"`
	Height   int `json:"height"`
	Viewport int `json:"viewport"`
}

type pageInfo struct {
	URL      string      `json:"url"`
	Title    string      `json:"title"`
	Text     string      `json:"text"`
	Total    int         `json:"total"`
	Elements []element   `json:"elements"`
	Scroll   scrollInfo  `json:"scroll"`
	Frames   []frameInfo `json:"frames"`
}

type frameInfo struct {
	Frame    int       `json:"frame"`
	URL      string    `json:"url"`
	Text     string    `json:"text"`
	Elements []element `json:"elements"`
}

// element is one interactive thing on a page, addressed by Ref.
type element struct {
	Ref      string   `json:"ref"`
	Tag      string   `json:"tag"`
	Role     string   `json:"role"`
	Type     string   `json:"type"`
	Label    string   `json:"label"`
	Visible  bool     `json:"visible"`
	Href     string   `json:"href"`
	Value    *string  `json:"value"`
	Checked  *bool    `json:"checked"`
	Expanded *bool    `json:"expanded"`
	Options  []string `json:"options"`
	Disabled bool     `json:"disabled"`
}

// kind names an element the way a person would.
func (e element) kind() string {
	if e.Role != "" && e.Tag != "input" && e.Tag != "button" && e.Tag != "a" && e.Tag != "select" && e.Tag != "textarea" {
		return e.Role
	}
	switch e.Tag {
	case "a":
		return "link"
	case "button", "summary":
		return "button"
	case "select":
		return "dropdown"
	case "textarea":
		return "textbox"
	case "input":
		switch e.Type {
		case "checkbox", "radio":
			return e.Type
		case "submit", "button", "reset", "image":
			return "button"
		case "file":
			return "file input"
		case "password":
			return "password field"
		}
		return "textbox"
	}
	if e.Tag == "" {
		return "element"
	}
	return e.Tag
}

// tabView is what Kest last saw of one tab. Refs stay valid only while the page
// they came from is open, so they are dropped when the address changes.
type tabView struct {
	url  string
	refs map[string]element
}

// view remembers the browser's state between calls. It is the only place
// outside the worker that knows what a ref means, which lets approval prompts
// say "Click Add to cart" instead of showing an opaque id.
type view struct {
	mu     sync.Mutex
	active int
	tabs   map[int]*tabView
}

func newView() *view { return &view{tabs: map[int]*tabView{}} }

// update records a worker result.
func (v *view) update(r result) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if r.Tabs != nil || r.Closed != 0 {
		keep := map[int]bool{}
		for _, t := range r.Tabs {
			keep[t.Tab] = true
			v.setURL(t.Tab, t.URL)
			if t.Active {
				v.active = t.Tab
			}
		}
		for id := range v.tabs {
			if !keep[id] {
				delete(v.tabs, id)
			}
		}
		return
	}
	if r.Tab == 0 {
		v.active = 0
		return
	}
	v.active = r.Tab
	if r.Page == nil {
		return
	}
	tv := v.setURL(r.Tab, r.Page.URL)
	add := func(els []element) {
		for _, e := range els {
			tv.refs[e.Ref] = e
		}
	}
	add(r.Page.Elements)
	for _, f := range r.Page.Frames {
		add(f.Elements)
	}
}

// setURL returns the tab's view, forgetting its refs if the address changed.
// The caller holds v.mu.
func (v *view) setURL(tab int, address string) *tabView {
	tv := v.tabs[tab]
	if tv == nil || tv.url != address {
		tv = &tabView{url: address, refs: map[string]element{}}
		v.tabs[tab] = tv
	}
	return tv
}

// lookup finds the tab's address and, if ref is known, the element.
func (v *view) lookup(tab int, ref string) (address string, el element, known bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if tab == 0 {
		tab = v.active
	}
	tv := v.tabs[tab]
	if tv == nil {
		return "", element{}, false
	}
	el, known = tv.refs[ref]
	return tv.url, el, known
}

func (v *view) reset() {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.active = 0
	v.tabs = map[int]*tabView{}
}

// --- formatting ----------------------------------------------------------------

// shortURL keeps links readable: same-site links lose the host.
func shortURL(raw, base string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return clip(raw, 120)
	}
	if b, err := url.Parse(base); err == nil && b.Host == u.Host {
		path := u.EscapedPath()
		if path == "" {
			path = "/"
		}
		if u.RawQuery != "" {
			path += "?" + u.RawQuery
		}
		return clip(path, 120)
	}
	return clip(u.Host+u.EscapedPath(), 120)
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// plain removes control characters so page text cannot smuggle terminal or
// layout tricks into labels. Newlines survive only where the caller wants them.
func plain(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
}

func (e element) line(base string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[%s] %s", e.Ref, e.kind())
	if e.Label != "" {
		fmt.Fprintf(&b, " %q", clip(plain(e.Label), 100))
	}
	if e.Href != "" {
		b.WriteString(" -> " + shortURL(e.Href, base))
	}
	if e.Value != nil && *e.Value != "" {
		fmt.Fprintf(&b, " value=%q", clip(plain(*e.Value), 80))
	}
	if e.Checked != nil {
		if *e.Checked {
			b.WriteString(" (checked)")
		} else {
			b.WriteString(" (unchecked)")
		}
	}
	if e.Expanded != nil {
		if *e.Expanded {
			b.WriteString(" (expanded)")
		} else {
			b.WriteString(" (collapsed)")
		}
	}
	if len(e.Options) > 0 {
		b.WriteString(" options: " + clip(plain(strings.Join(e.Options, " | ")), 300) + " (* = selected)")
	}
	if e.Disabled {
		b.WriteString(" (disabled)")
	}
	if !e.Visible {
		b.WriteString(" (off screen)")
	}
	return b.String()
}

// formatPage renders a page for the model.
func formatPage(r result) string {
	var b strings.Builder
	p := r.Page
	for _, n := range r.Notes {
		b.WriteString("Note: " + plain(n) + "\n")
	}
	opened := ""
	if r.OpenedTab {
		opened = " (a new tab opened)"
	}
	fmt.Fprintf(&b, "Tab %d%s: %s\n", r.Tab, opened, clip(plain(p.URL), 300))
	if p.Title != "" {
		fmt.Fprintf(&b, "Title: %s\n", clip(plain(p.Title), 200))
	}
	b.WriteString(positionLine(p.Scroll) + "\n")

	if r.Find != nil {
		b.WriteString(findLine(r.Find) + "\n")
	}
	if r.Moved != nil && !*r.Moved {
		b.WriteString("The page did not move: you are probably at the end, or the page does not scroll.\n")
	}

	if r.Scope == "viewport" {
		b.WriteString("\nText on screen right now:\n")
	} else {
		end := r.Offset + len([]rune(p.Text))
		fmt.Fprintf(&b, "\nPage text (characters %d to %d of %d):\n", r.Offset, end, p.Total)
	}
	if text := compactText(p.Text); text == "" {
		b.WriteString("(no text)\n")
	} else {
		b.WriteString(text + "\n")
	}
	if r.Scope != "viewport" && r.Offset+len([]rune(p.Text)) < p.Total {
		fmt.Fprintf(&b, "(more text follows: read_page with offset=%d continues; scroll_page shows other parts of the screen)\n", r.Offset+len([]rune(p.Text)))
	}

	if len(p.Elements) == 0 {
		if r.Scope == "viewport" {
			b.WriteString("\nNo clickable or editable elements on screen.\n")
		} else {
			b.WriteString("\nNo clickable or editable elements.\n")
		}
	} else {
		if r.Scope == "viewport" {
			b.WriteString("\nElements on screen:\n")
		} else {
			b.WriteString("\nElements:\n")
		}
		for _, e := range p.Elements {
			b.WriteString(e.line(p.URL) + "\n")
		}
	}
	for _, f := range p.Frames {
		fmt.Fprintf(&b, "\nFrame %d (%s):\n", f.Frame, clip(plain(f.URL), 120))
		if t := strings.TrimSpace(f.Text); t != "" {
			b.WriteString(clip(t, 1500) + "\n")
		}
		for _, e := range f.Elements {
			b.WriteString(e.line(f.URL) + "\n")
		}
	}
	return strings.TrimSpace(b.String())
}

var blankRuns = regexp.MustCompile(`[ \t]*\n[ \t\n]*`)

// compactText drops the blank lines and trailing spaces that page text is full
// of; they cost tokens and carry nothing.
func compactText(s string) string {
	s = strings.Map(func(r rune) rune {
		if r != '\n' && r != '\t' && unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
	return strings.TrimSpace(blankRuns.ReplaceAllString(s, "\n"))
}

func positionLine(s scrollInfo) string {
	if s.Height <= 0 || s.Viewport <= 0 {
		return "Position: unknown"
	}
	end := s.Y + s.Viewport
	switch {
	case s.Height <= s.Viewport:
		return "Position: the whole page fits on screen"
	case s.Y <= 2:
		return fmt.Sprintf("Position: top of the page (screen shows 0 to %d of %d px)", min(end, s.Height), s.Height)
	case end >= s.Height-2:
		return fmt.Sprintf("Position: bottom of the page (screen shows %d to %d of %d px)", s.Y, s.Height, s.Height)
	}
	return fmt.Sprintf("Position: %d%% down (screen shows %d to %d of %d px)", s.Y*100/s.Height, s.Y, end, s.Height)
}

func findLine(f *findInfo) string {
	if f.Count == 0 {
		return "Find: no visible match for that text on this page."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Find: %d match(es); scrolled match %d into view. Matches:", f.Count, f.Index+1)
	for i, s := range f.Snippets {
		fmt.Fprintf(&b, "\n  %d. %s", i+1, clip(plain(s), 160))
	}
	if f.Count > len(f.Snippets) {
		b.WriteString("\n  (use find_text with index to jump to later matches)")
	}
	return b.String()
}

// formatTabs renders the open tabs.
func formatTabs(r result) string {
	var b strings.Builder
	for _, n := range r.Notes {
		b.WriteString("Note: " + plain(n) + "\n")
	}
	if r.Closed != 0 {
		fmt.Fprintf(&b, "Closed tab %d.\n", r.Closed)
	}
	if len(r.Tabs) == 0 {
		b.WriteString("No tabs are open.")
		return b.String()
	}
	b.WriteString("Open tabs:\n")
	for _, t := range r.Tabs {
		mark := ""
		if t.Active {
			mark = " (active)"
		}
		fmt.Fprintf(&b, "  %d%s: %s - %s\n", t.Tab, mark, clip(plain(t.Title), 80), clip(plain(t.URL), 200))
	}
	return strings.TrimSpace(b.String())
}
