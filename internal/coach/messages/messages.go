// Package messages holds Kest's proactive messages as data.
//
// Wording lives in messages.toml, grouped by situation with several variants
// each, so tone can be reviewed and changed without touching code. A test
// renders every variant and checks it against the voice guide.
package messages

import (
	"bytes"
	_ "embed" // messages.toml
	"errors"
	"fmt"
	"sort"
	"strings"
	"text/template"
	"unicode"

	"github.com/pelletier/go-toml/v2"
)

//go:embed messages.toml
var source []byte

type entry struct {
	variant string
	tmpl    *template.Template
}

// Set is the loaded collection of messages.
type Set struct {
	byKind map[string][]entry
}

var funcs = template.FuncMap{
	// lowerFirst lowercases the first letter so a phrase can sit mid-sentence.
	"lowerFirst": func(s string) string {
		for i, r := range s {
			return string(unicode.ToLower(r)) + s[i+len(string(r)):]
		}
		return s
	},
}

// Load parses the embedded messages. A malformed template is an error.
func Load() (*Set, error) { return parse(source) }

func parse(data []byte) (*Set, error) {
	var doc struct {
		Message []struct {
			Kind    string `toml:"kind"`
			Variant string `toml:"variant"`
			Text    string `toml:"text"`
		} `toml:"message"`
	}
	dec := toml.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("parse messages: %w", err)
	}

	s := &Set{byKind: map[string][]entry{}}
	var errs []error
	seen := map[string]bool{}
	for _, m := range doc.Message {
		id := m.Kind + "/" + m.Variant
		switch {
		case m.Kind == "" || m.Variant == "" || strings.TrimSpace(m.Text) == "":
			errs = append(errs, fmt.Errorf("message %q needs a kind, a variant and text", id))
			continue
		case seen[id]:
			errs = append(errs, fmt.Errorf("message %q is defined twice", id))
			continue
		}
		seen[id] = true

		t, err := template.New(id).Funcs(funcs).Option("missingkey=error").Parse(m.Text)
		if err != nil {
			errs = append(errs, fmt.Errorf("message %q: %w", id, err))
			continue
		}
		s.byKind[m.Kind] = append(s.byKind[m.Kind], entry{variant: m.Variant, tmpl: t})
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return s, nil
}

// Kinds lists the available message kinds, sorted.
func (s *Set) Kinds() []string {
	out := make([]string, 0, len(s.byKind))
	for k := range s.byKind {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Variants lists the variant names of a kind in file order.
func (s *Set) Variants(kind string) []string {
	var out []string
	for _, e := range s.byKind[kind] {
		out = append(out, e.variant)
	}
	return out
}

// Render fills in a message of the given kind and returns its text and the
// variant used. data must supply every key the template uses.
//
// The variant is chosen by seed, skipping avoid (the one used last time) when
// there is a choice, so the same wording does not repeat back to back. The same
// seed and avoid always give the same variant, which keeps tests deterministic.
func (s *Set) Render(kind string, data map[string]any, avoid string, seed int) (text, variant string, err error) {
	entries := s.byKind[kind]
	if len(entries) == 0 {
		return "", "", fmt.Errorf("no messages of kind %q", kind)
	}

	candidates := entries
	if len(entries) > 1 && avoid != "" {
		candidates = nil
		for _, e := range entries {
			if e.variant != avoid {
				candidates = append(candidates, e)
			}
		}
	}
	if seed < 0 {
		seed = -seed
	}
	e := candidates[seed%len(candidates)]

	var b bytes.Buffer
	if err := e.tmpl.Execute(&b, data); err != nil {
		return "", "", fmt.Errorf("render %s/%s: %w", kind, e.variant, err)
	}
	return tidy(b.String()), e.variant, nil
}

// RenderAll renders every variant of a kind. Tests use it.
func (s *Set) RenderAll(kind string, data map[string]any) (map[string]string, error) {
	out := map[string]string{}
	for _, e := range s.byKind[kind] {
		var b bytes.Buffer
		if err := e.tmpl.Execute(&b, data); err != nil {
			return nil, fmt.Errorf("render %s/%s: %w", kind, e.variant, err)
		}
		out[e.variant] = tidy(b.String())
	}
	return out, nil
}

// tidy squeezes the whitespace that conditional template parts leave behind.
func tidy(s string) string { return strings.Join(strings.Fields(s), " ") }
