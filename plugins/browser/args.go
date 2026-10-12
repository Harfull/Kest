package browser

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"
)

// arguments are every parameter any browser tool accepts. Each tool's JSON
// Schema exposes only the ones that tool uses; the decoder rejects the rest.
type arguments struct {
	Tab       int    `json:"tab,omitempty"`
	URL       string `json:"url,omitempty"`
	Ref       string `json:"ref,omitempty"`
	Selector  string `json:"selector,omitempty"`
	Frame     int    `json:"frame,omitempty"`
	Text      string `json:"text,omitempty"`
	Key       string `json:"key,omitempty"`
	Pixels    int    `json:"pixels,omitempty"`
	File      string `json:"file,omitempty"`
	FullPage  bool   `json:"full_page,omitempty"`
	Caption   string `json:"caption,omitempty"`
	Value     string `json:"value,omitempty"`
	State     string `json:"state,omitempty"`
	WaitMS    int    `json:"wait_ms,omitempty"`
	NewTab    bool   `json:"new_tab,omitempty"`
	X         int    `json:"x,omitempty"`
	Y         int    `json:"y,omitempty"`
	Scope     string `json:"scope,omitempty"`
	Offset    int    `json:"offset,omitempty"`
	Direction string `json:"direction,omitempty"`
	Index     int    `json:"index,omitempty"`
	Submit    bool   `json:"submit,omitempty"`
	Send      bool   `json:"send,omitempty"`
	Look      bool   `json:"look,omitempty"`
}

// workerArgs is what the worker receives: the model's arguments plus values
// only this package may set.
type workerArgs struct {
	arguments
	// Expect is the label the element had when the page was read. The worker
	// refuses to act if the ref now points at something else.
	Expect *string `json:"expect,omitempty"`
	// MaxHeight bounds full-page screenshots, in pixels.
	MaxHeight int `json:"max_height,omitempty"`
}

// The viewport is fixed by the worker; coordinates are checked against it.
const (
	viewportWidth  = 1280
	viewportHeight = 800
)

var refRe = regexp.MustCompile(`^(f[1-9][0-9]?)?e[0-9]{1,6}$`)

// needsTarget lists the actions that act on exactly one element.
func needsTarget(action string) bool {
	switch action {
	case "click_element", "fill_field", "select_option", "hover_element", "upload_file":
		return true
	}
	return false
}

func validateArgs(action string, a arguments) error {
	if a.Tab < 0 || a.Tab > 1000000 {
		return errors.New("invalid tab id")
	}
	if a.Frame < 0 || a.Frame > 64 {
		return errors.New("frame must be an index between 0 and 64")
	}
	if utf8.RuneCountInString(a.Caption) > 1024 {
		return errors.New("caption must be at most 1024 characters")
	}
	if len(a.Selector) > 2000 {
		return errors.New("selector must be at most 2000 bytes")
	}
	if a.Ref != "" && !refRe.MatchString(a.Ref) {
		return errors.New("ref must look like e3, as listed by read_page")
	}
	if len(a.Text) > 20000 || len(a.Value) > 2000 {
		return errors.New("text or value is too long")
	}
	if needsTarget(action) && a.Ref == "" && strings.TrimSpace(a.Selector) == "" {
		return errors.New("ref is required (use a ref from read_page)")
	}
	switch action {
	case "navigate_page":
		u, err := url.Parse(a.URL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil {
			return errors.New("url must be an http or https address without credentials")
		}
	case "read_page":
		if a.Scope != "" && a.Scope != "viewport" && a.Scope != "page" {
			return errors.New("scope must be viewport or page")
		}
		if a.Offset < 0 || a.Offset > 10_000_000 {
			return errors.New("offset must be a character position in the page text")
		}
	case "scroll_page":
		switch a.Direction {
		case "", "down", "up", "top", "bottom":
		default:
			return errors.New("direction must be down, up, top or bottom")
		}
		if a.Pixels < 0 || a.Pixels > 10000 {
			return errors.New("pixels must be between 0 and 10000")
		}
	case "find_text":
		if strings.TrimSpace(a.Text) == "" || utf8.RuneCountInString(a.Text) > 200 {
			return errors.New("text to find is required and must be at most 200 characters")
		}
		if a.Index < 0 || a.Index > 199 {
			return errors.New("index must be between 0 and 199")
		}
	case "click_position":
		if a.X < 0 || a.X >= viewportWidth || a.Y < 0 || a.Y >= viewportHeight {
			return fmt.Errorf("click position must be inside the %d by %d viewport", viewportWidth, viewportHeight)
		}
	case "select_option":
		if a.Value == "" {
			return errors.New("value is required: the option's value or visible label")
		}
	case "press_key":
		if !validKey(a.Key) {
			return errors.New("unsupported key; use Enter, Tab, Escape, Space, Backspace, Delete, an arrow key, PageUp, PageDown, Home or End")
		}
	case "navigate_history":
		switch a.Direction {
		case "back", "forward", "reload":
		default:
			return errors.New("direction must be back, forward or reload")
		}
	case "wait_for":
		if a.WaitMS < 0 || a.WaitMS > 15000 {
			return errors.New("wait_ms must be between 0 and 15000")
		}
		switch a.State {
		case "", "visible", "hidden", "attached", "detached":
		default:
			return errors.New("state must be visible, hidden, attached or detached")
		}
		if utf8.RuneCountInString(a.Text) > 200 {
			return errors.New("text must be at most 200 characters")
		}
	case "upload_file":
		if a.File == "" || len(a.File) > 256 {
			return errors.New("file is required and must be relative to the browser inbox")
		}
	case "screenshot":
		if !a.Send && !a.Look {
			return errors.New("set send to show the screenshot to the owner, look to see it yourself, or both")
		}
	}
	return nil
}

func validKey(key string) bool {
	switch key {
	case "Enter", "Tab", "Escape", "Space", "Backspace", "Delete", "ArrowDown", "ArrowUp", "ArrowLeft", "ArrowRight", "PageDown", "PageUp", "Home", "End":
		return true
	}
	return false
}
