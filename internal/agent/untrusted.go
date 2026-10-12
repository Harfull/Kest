package agent

import (
	"fmt"
	"strings"
	"unicode"
)

// Limits for content that enters the model's context.
const (
	// maxToolResultChars caps any single tool result.
	maxToolResultChars = 8000
	// maxFactChars caps a stored fact shown in the prompt.
	maxFactChars = 300
)

// Wrap presents untrusted content to the model as clearly labeled data.
//
// The content is stripped of control characters, truncated, and any text that
// looks like the wrapper's own closing tag is neutralized, so hostile content
// cannot "close" the block and continue as if it were instructions. The system
// prompt tells the model that nothing inside such a block is an instruction.
func Wrap(source, content string) string {
	content = Sanitize(content, maxToolResultChars)
	return fmt.Sprintf("<external_data source=%q trust=\"untrusted\">\n%s\n</external_data>", sanitizeLabel(source), content)
}

// Sanitize removes control characters (other than newline and tab), neutralizes
// tag lookalikes that could break out of a wrapper, and truncates to max runes.
func Sanitize(s string, max int) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '\n' || r == '\t':
			b.WriteRune(r)
		case unicode.IsControl(r), r == 0x2028, r == 0x2029, r == 0xFEFF, isBidi(r):
			// dropped: invisible or direction-changing characters can hide text
		default:
			b.WriteRune(r)
		}
	}
	out := neutralizeTags(b.String())

	if rs := []rune(out); len(rs) > max {
		out = string(rs[:max]) + "\n[truncated]"
	}
	return out
}

// neutralizeTags breaks up "<external_data" and "</external_data" in any case,
// with any spacing, so they cannot act as delimiters.
func neutralizeTags(s string) string {
	lower := strings.ToLower(s)
	var b strings.Builder
	i := 0
	for i < len(s) {
		if s[i] == '<' {
			j := i + 1
			for j < len(s) && (s[j] == ' ' || s[j] == '\t' || s[j] == '/' || s[j] == '\n') {
				j++
			}
			if strings.HasPrefix(lower[j:], "external_data") {
				b.WriteString("&lt;")
				i++
				continue
			}
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

func isBidi(r rune) bool {
	return (r >= 0x202A && r <= 0x202E) || (r >= 0x2066 && r <= 0x2069) || r == 0x200E || r == 0x200F
}

// sanitizeLabel keeps source labels short and printable.
func sanitizeLabel(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == '"' || r == '<' || r == '>' {
			return -1
		}
		return r
	}, s)
	if len(s) > 80 {
		s = s[:80]
	}
	return s
}
