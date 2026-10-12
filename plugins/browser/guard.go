package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"
)

// sensitiveAction matches labels of controls that commit something: buying,
// sending, deleting, signing in, accepting terms. A task grant never covers
// them, so the owner sees each one. The list errs toward asking.
var sensitiveAction = regexp.MustCompile(`(?i)\b(buy|purchase|pay|payment|place order|order|checkout|check out|send|submit|post|publish|tweet|reply|comment|delete|remove|erase|cancel|confirm|authori[sz]e|allow|grant|transfer|withdraw|deposit|subscribe|unsubscribe|sign ?up|register|log ?in|sign ?in|donate|book|reserve|accept|agree|apply|save|update|change|reset|invite|share|install|download|upgrade|renew)\b`)

// sensitiveField matches fields that hold credentials or payment details.
var sensitiveField = regexp.MustCompile(`(?i)(password|passcode|\bpin\b|card|cvv|cvc|security code|\bssn\b|social security|iban|routing|account number|secret|token|api key)`)

// site approximates a registrable domain without a public-suffix list: the last
// two labels, or three when the second-last is short and the last is a country
// code (example.co.uk). Hosts that are IP addresses compare whole.
func site(host string) string {
	host = strings.TrimPrefix(strings.ToLower(host), "www.")
	if net.ParseIP(host) != nil {
		return host
	}
	parts := strings.Split(host, ".")
	if len(parts) <= 2 {
		return host
	}
	n := 2
	if len(parts[len(parts)-1]) == 2 && len(parts[len(parts)-2]) <= 3 {
		n = 3
	}
	return strings.Join(parts[len(parts)-n:], ".")
}

func hostOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

func sameSite(a, b string) bool {
	ha, hb := hostOf(a), hostOf(b)
	return ha != "" && hb != "" && site(ha) == site(hb)
}

func decodeArgs(raw json.RawMessage) arguments {
	var a arguments
	_ = json.Unmarshal(raw, &a) // arguments were validated before the call; a bad one just yields zero values
	return a
}

// elementOf resolves the element a call refers to, if it used a known ref.
func (p *Plugin) elementOf(a arguments) (page string, el element, known bool) {
	if a.Ref == "" {
		page, _, _ = p.view.lookup(a.Tab, "")
		return page, element{}, false
	}
	return p.view.lookup(a.Tab, a.Ref)
}

// describe renders what a call would do, for the owner's approval prompt. It is
// built from the structured arguments and the last page read, never from model
// prose, so it shows what will actually be clicked.
func (p *Plugin) describe(action string) func(json.RawMessage) string {
	return func(raw json.RawMessage) string {
		a := decodeArgs(raw)
		page, el, known := p.elementOf(a)
		on := ""
		if h := hostOf(page); h != "" {
			on = " on " + h
		}
		target := func() string {
			switch {
			case known && el.Label != "":
				return fmt.Sprintf("%s “%s”", el.kind(), clip(plain(el.Label), 80))
			case known:
				return el.kind() + " " + a.Ref
			case a.Ref != "":
				return "element " + a.Ref
			}
			return "the element matching " + clip(plain(a.Selector), 120)
		}
		switch action {
		case "navigate_page":
			where := ""
			if a.NewTab {
				where = " in a new tab"
			}
			return "Open " + clip(plain(a.URL), 300) + where
		case "click_element":
			s := "Click " + target() + on
			if known && el.Href != "" {
				s += " (goes to " + shortURL(el.Href, page) + ")"
			}
			return s
		case "click_position":
			return fmt.Sprintf("Click the point (%d, %d) of the screen%s", a.X, a.Y, on)
		case "fill_field":
			text := clip(plain(a.Text), 100)
			if known && (el.Type == "password" || sensitiveField.MatchString(el.Label)) {
				text = "(hidden)"
			}
			s := fmt.Sprintf("Type “%s” into %s%s", text, target(), on)
			if a.Submit {
				s += " and press Enter"
			}
			return s
		case "press_key":
			if a.Ref != "" || a.Selector != "" {
				return fmt.Sprintf("Press %s in %s%s", a.Key, target(), on)
			}
			return fmt.Sprintf("Press %s%s", a.Key, on)
		case "select_option":
			return fmt.Sprintf("Choose “%s” in %s%s", clip(plain(a.Value), 80), target(), on)
		case "hover_element":
			return "Hover over " + target() + on
		case "navigate_history":
			return strings.ToUpper(a.Direction[:1]) + a.Direction[1:] + " in the browser history" + on
		case "upload_file":
			return fmt.Sprintf("Upload the inbox file “%s” to %s%s", clip(plain(a.File), 100), target(), on)
		}
		return "Browser action " + action
	}
}

// covers decides whether a task grant still applies to one call. A grant is
// meant for ordinary browsing, so it stops at anything that commits something,
// at credentials, at blind clicks and at leaving the site the owner started on.
func (p *Plugin) covers(action string) func(context.Context, json.RawMessage) bool {
	return func(_ context.Context, raw json.RawMessage) bool {
		a := decodeArgs(raw)
		page, el, known := p.elementOf(a)
		switch action {
		case "navigate_page":
			return page != "" && sameSite(page, a.URL)
		case "navigate_history":
			return a.Direction == "back" || a.Direction == "forward"
		case "click_position":
			return false
		case "press_key":
			if a.Ref == "" && a.Selector == "" {
				return true
			}
		}
		if !known {
			return false // a selector, or a ref from a page that is gone, is not something the owner saw
		}
		if el.Disabled || el.Type == "password" || el.Type == "file" {
			return false
		}
		switch action {
		case "click_element", "hover_element", "select_option":
			if sensitiveAction.MatchString(el.Label) {
				return false
			}
			if el.Href != "" && !sameSite(page, el.Href) {
				return false
			}
			if el.Type == "submit" && el.Label == "" {
				return false
			}
		case "fill_field":
			if sensitiveField.MatchString(el.Label) || looksSecret(a.Text) {
				return false
			}
		case "press_key":
			if sensitiveAction.MatchString(el.Label) {
				return false
			}
		}
		return true
	}
}

// looksSecret flags text that resembles a card number or a long token, which
// should not be typed into a page under a blanket grant.
var secretText = regexp.MustCompile(`^(?:[0-9][ -]?){12,19}$|^[A-Za-z0-9_\-]{32,}$`)

func looksSecret(s string) bool { return secretText.MatchString(strings.TrimSpace(s)) }
