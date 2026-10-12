package telegram

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestToHTML(t *testing.T) {
	tests := []struct {
		name, in, want string
	}{
		{"plain", "hello", "hello"},
		{"escapes tags", "<script>alert(1)</script>", "&lt;script&gt;alert(1)&lt;/script&gt;"},
		{"escapes ampersand", "Q&A", "Q&amp;A"},
		{"bold", "this is **important** now", "this is <b>important</b> now"},
		{"code", "run `kest -version`", "run <code>kest -version</code>"},
		{"unmatched bold stays literal", "2 ** 3", "2 ** 3"},
		{"bold content is escaped", "**<b>**", "<b>&lt;b&gt;</b>"},
		{"quotes escaped", `say "hi"`, "say &#34;hi&#34;"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ToHTML(tt.in); got != tt.want {
				t.Errorf("ToHTML(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestSplit(t *testing.T) {
	t.Run("short text is one part", func(t *testing.T) {
		got := Split("  hello  ", 100)
		if len(got) != 1 || got[0] != "hello" {
			t.Errorf("Split = %q", got)
		}
	})

	t.Run("empty", func(t *testing.T) {
		if got := Split("   \n ", 10); got != nil {
			t.Errorf("Split = %q, want nil", got)
		}
	})

	t.Run("prefers paragraph breaks", func(t *testing.T) {
		a := strings.Repeat("a", 60)
		b := strings.Repeat("b", 60)
		got := Split(a+"\n\n"+b, 100)
		if len(got) != 2 || got[0] != a || got[1] != b {
			t.Errorf("Split = %q", got)
		}
	})

	t.Run("falls back to spaces", func(t *testing.T) {
		words := strings.Repeat("word ", 50)
		got := Split(words, 40)
		for _, p := range got {
			if utf8.RuneCountInString(p) > 40 {
				t.Errorf("part too long (%d): %q", utf8.RuneCountInString(p), p)
			}
			if strings.HasPrefix(p, "ord") || strings.HasSuffix(p, "wor") {
				t.Errorf("word split mid-way: %q", p)
			}
		}
	})

	t.Run("hard cut when no separator", func(t *testing.T) {
		got := Split(strings.Repeat("x", 250), 100)
		if len(got) != 3 || len(got[0]) != 100 {
			t.Errorf("lengths = %v", lens(got))
		}
	})

	t.Run("counts runes not bytes", func(t *testing.T) {
		got := Split(strings.Repeat("é", 150), 100)
		if len(got) != 2 || utf8.RuneCountInString(got[0]) != 100 {
			t.Errorf("lengths = %v", lens(got))
		}
		for _, p := range got {
			if !utf8.ValidString(p) {
				t.Errorf("invalid UTF-8 after split: %q", p)
			}
		}
	})

	t.Run("nothing is lost", func(t *testing.T) {
		src := strings.Repeat("alpha beta gamma\n\ndelta epsilon. ", 400)
		got := strings.Join(Split(src, splitAt), " ")
		if strings.Join(strings.Fields(got), " ") != strings.Join(strings.Fields(src), " ") {
			t.Error("content changed by splitting")
		}
	})
}

func lens(parts []string) []int {
	out := make([]int, len(parts))
	for i, p := range parts {
		out[i] = len(p)
	}
	return out
}

func TestValidateButtons(t *testing.T) {
	ok := [][]Button{{{Label: "Yes", Data: "confirm:abc"}, {Label: "No", Data: "cancel:abc"}}}
	if err := validateButtons(ok); err != nil {
		t.Errorf("valid buttons rejected: %v", err)
	}

	bad := map[string][][]Button{
		"no label": {{{Label: " ", Data: "x"}}},
		"no data":  {{{Label: "A"}}},
		"too long": {{{Label: "A", Data: strings.Repeat("x", 65)}}},
	}
	for name, b := range bad {
		if err := validateButtons(b); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
