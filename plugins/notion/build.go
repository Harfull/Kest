package notion

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const (
	maxBlocks    = 100  // Notion accepts at most 100 children per request
	maxTextChars = 2000 // and at most 2000 characters in one rich text object
)

var (
	numberedRe = regexp.MustCompile(`^\d{1,3}[.)] `)
	todoRe     = regexp.MustCompile(`^\[( |x|X)\] `)
)

func textObject(s string) []obj {
	var parts []obj
	r := []rune(s)
	for len(r) > 0 {
		n := min(len(r), maxTextChars)
		parts = append(parts, obj{"type": "text", "text": obj{"content": string(r[:n])}})
		r = r[n:]
	}
	if len(parts) == 0 {
		parts = []obj{{"type": "text", "text": obj{"content": ""}}}
	}
	return parts
}

func block(kind string, extra obj, text string) obj {
	body := obj{"rich_text": textObject(text)}
	for k, v := range extra {
		body[k] = v
	}
	return obj{"object": "block", "type": kind, kind: body}
}

// toBlocks turns plain text into Notion blocks: "# " headings, "- " bullets,
// "1. " numbered items, "[ ] " to-dos and "> " quotes; every other line is a
// paragraph. Blank lines separate paragraphs and are dropped.
func toBlocks(text string) ([]obj, error) {
	var out []obj
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		line = strings.TrimRight(clean(line), " \t")
		if strings.TrimSpace(line) == "" {
			continue
		}
		switch {
		case strings.HasPrefix(line, "### "):
			out = append(out, block("heading_3", nil, line[4:]))
		case strings.HasPrefix(line, "## "):
			out = append(out, block("heading_2", nil, line[3:]))
		case strings.HasPrefix(line, "# "):
			out = append(out, block("heading_1", nil, line[2:]))
		case strings.HasPrefix(line, "- "), strings.HasPrefix(line, "* "):
			out = append(out, block("bulleted_list_item", nil, line[2:]))
		case numberedRe.MatchString(line):
			out = append(out, block("numbered_list_item", nil, numberedRe.ReplaceAllString(line, "")))
		case todoRe.MatchString(line):
			out = append(out, block("to_do", obj{"checked": strings.EqualFold(line[:3], "[x]")}, line[4:]))
		case strings.HasPrefix(line, "> "):
			out = append(out, block("quote", nil, line[2:]))
		default:
			out = append(out, block("paragraph", nil, line))
		}
		if len(out) > maxBlocks {
			return nil, fmt.Errorf("at most %d lines can be added at once", maxBlocks)
		}
	}
	return out, nil
}

// schema maps a data source's property names to their types.
func schemaOf(ds obj) map[string]string {
	out := map[string]string{}
	for name, p := range sub(ds, "properties") {
		if o, ok := p.(obj); ok {
			out[name] = str(o, "type")
		}
	}
	return out
}

// titleProperty returns the name of the property that holds the title.
func titleProperty(schema map[string]string) string {
	names := make([]string, 0, len(schema))
	for n, t := range schema {
		if t == "title" {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		return ""
	}
	return names[0]
}

// buildProperties converts simple values (text, numbers, booleans, lists of
// strings, ISO dates) into Notion property objects using the data source
// schema. A property that does not exist, or whose type cannot be written, is
// an error that names it, so a typo does not silently drop a value.
func buildProperties(schema map[string]string, values map[string]any) (obj, error) {
	out := obj{}
	names := make([]string, 0, len(values))
	for n := range values {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		typ, ok := schema[name]
		if !ok {
			return nil, fmt.Errorf("the database has no property called %q (properties: %s)", clip(oneLine(name), 40), strings.Join(sortedNames(schema), ", "))
		}
		v := values[name]
		p, err := propertyFor(typ, v)
		if err != nil {
			return nil, fmt.Errorf("property %q: %w", clip(oneLine(name), 40), err)
		}
		out[name] = p
	}
	return out, nil
}

func sortedNames(schema map[string]string) []string {
	names := make([]string, 0, len(schema))
	for n := range schema {
		names = append(names, clip(oneLine(n), 40))
	}
	sort.Strings(names)
	return names
}

func propertyFor(typ string, v any) (obj, error) {
	asString := func() (string, error) {
		switch t := v.(type) {
		case string:
			return t, nil
		case float64:
			return strconv.FormatFloat(t, 'f', -1, 64), nil
		case bool:
			return strconv.FormatBool(t), nil
		}
		return "", errors.New("expected text")
	}
	switch typ {
	case "title":
		s, err := asString()
		return obj{"title": textObject(s)}, err
	case "rich_text":
		s, err := asString()
		return obj{"rich_text": textObject(s)}, err
	case "number":
		switch t := v.(type) {
		case float64:
			if math.IsNaN(t) || math.IsInf(t, 0) {
				return nil, errors.New("not a number")
			}
			return obj{"number": t}, nil
		case string:
			f, err := strconv.ParseFloat(strings.TrimSpace(t), 64)
			if err != nil {
				return nil, errors.New("expected a number")
			}
			return obj{"number": f}, nil
		}
		return nil, errors.New("expected a number")
	case "checkbox":
		switch t := v.(type) {
		case bool:
			return obj{"checkbox": t}, nil
		case string:
			return obj{"checkbox": strings.EqualFold(t, "true") || strings.EqualFold(t, "yes")}, nil
		}
		return nil, errors.New("expected true or false")
	case "select", "status":
		s, err := asString()
		return obj{typ: obj{"name": s}}, err
	case "multi_select":
		var names []obj
		switch t := v.(type) {
		case []any:
			for _, x := range t {
				s, ok := x.(string)
				if !ok {
					return nil, errors.New("expected a list of names")
				}
				names = append(names, obj{"name": s})
			}
		case string:
			for _, s := range strings.Split(t, ",") {
				if s = strings.TrimSpace(s); s != "" {
					names = append(names, obj{"name": s})
				}
			}
		default:
			return nil, errors.New("expected a list of names")
		}
		return obj{"multi_select": names}, nil
	case "date":
		s, err := asString()
		if err != nil {
			return nil, err
		}
		start, end, _ := strings.Cut(s, "/")
		d := obj{"start": strings.TrimSpace(start)}
		if end != "" {
			d["end"] = strings.TrimSpace(end)
		}
		return obj{"date": d}, nil
	case "url", "email", "phone_number":
		s, err := asString()
		return obj{typ: s}, err
	}
	return nil, fmt.Errorf("properties of type %s cannot be set from here", typ)
}
