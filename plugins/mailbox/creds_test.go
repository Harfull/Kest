package mailbox

import "strings"

// credentials reads the two arguments of LOGIN: quoted strings with escapes,
// or a literal that the server already received.
func (f *fakeIMAP) credentials(args string, literals []string) (user, pass string) {
	var toks []string
	for rest := strings.TrimSpace(args); rest != ""; rest = strings.TrimSpace(rest) {
		switch {
		case strings.HasPrefix(rest, "\x00"):
			toks = append(toks, literals[0])
			literals = literals[1:]
			rest = rest[1:]
		case strings.HasPrefix(rest, `"`):
			var b strings.Builder
			i := 1
			for ; i < len(rest) && rest[i] != '"'; i++ {
				if rest[i] == '\\' {
					i++
				}
				b.WriteByte(rest[i])
			}
			toks = append(toks, b.String())
			rest = rest[min(i+1, len(rest)):]
		default:
			tok, more, _ := strings.Cut(rest, " ")
			toks = append(toks, tok)
			rest = more
		}
	}
	if len(toks) < 2 {
		return "", ""
	}
	return toks[0], toks[1]
}
