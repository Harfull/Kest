package telegram

import (
	"fmt"
	"html"
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	// MaxMessageChars is Telegram's limit for message text after parsing.
	MaxMessageChars = 4096
	// maxCallbackBytes is Telegram's limit for inline button data.
	maxCallbackBytes = 64
	// splitAt leaves headroom under the limit so conversion never overflows it.
	splitAt = 4000
)

var (
	boldRe = regexp.MustCompile(`\*\*([^*\n]+?)\*\*`)
	codeRe = regexp.MustCompile("`([^`\n]+?)`")
)

// ToHTML converts Kest's light markup to Telegram HTML. All text is escaped
// first, so model output or external content can never inject tags.
//
// Supported: **bold** and `code`. Anything unmatched stays literal.
func ToHTML(s string) string {
	s = html.EscapeString(s)
	s = codeRe.ReplaceAllString(s, "<code>$1</code>")
	s = boldRe.ReplaceAllString(s, "<b>$1</b>")
	return s
}

// Split breaks text into parts of at most limit characters, preferring
// paragraph breaks, then line breaks, then spaces. Parts are trimmed.
func Split(text string, limit int) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}

	var parts []string
	for utf8.RuneCountInString(text) > limit {
		cut := cutPoint(text, limit)
		parts = append(parts, strings.TrimSpace(text[:cut]))
		text = strings.TrimSpace(text[cut:])
	}
	if text != "" {
		parts = append(parts, text)
	}
	return parts
}

// cutPoint returns a byte index at which to split text so that the head holds
// at most limit runes, breaking at the best boundary in the second half.
func cutPoint(text string, limit int) int {
	// Byte offset of the limit-th rune.
	end, n := 0, 0
	for i := range text {
		if n == limit {
			end = i
			break
		}
		n++
	}
	if end == 0 {
		return len(text)
	}

	head := text[:end]
	floor := end / 2 // do not make a tiny first part
	for _, sep := range []string{"\n\n", "\n", ". ", " "} {
		if i := strings.LastIndex(head, sep); i >= floor {
			return i + len(sep)
		}
	}
	return end
}

// validateButtons checks Telegram's constraints on inline buttons.
func validateButtons(rows [][]Button) error {
	for _, row := range rows {
		for _, b := range row {
			switch {
			case strings.TrimSpace(b.Label) == "":
				return fmt.Errorf("button has no label")
			case b.Data == "":
				return fmt.Errorf("button %q has no data", b.Label)
			case len(b.Data) > maxCallbackBytes:
				return fmt.Errorf("button %q data is %d bytes, limit is %d", b.Label, len(b.Data), maxCallbackBytes)
			}
		}
	}
	return nil
}
