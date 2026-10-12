package notion

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

// obj is a decoded JSON object. Notion's responses are deeply nested and vary by
// type, so they are read defensively instead of through rigid structs.
type obj = map[string]any

func str(o obj, key string) string { s, _ := o[key].(string); return s }

func sub(o obj, key string) obj { m, _ := o[key].(obj); return m }

func list(o obj, key string) []any { l, _ := o[key].([]any); return l }

// clean makes text safe for a prompt: control characters become spaces.
func clean(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return ' '
		}
		return r
	}, s)
}

func oneLine(s string) string { return strings.Join(strings.Fields(clean(s)), " ") }

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// richText joins the plain text of a rich_text array.
func richText(v any) string {
	var b strings.Builder
	arr, _ := v.([]any)
	for _, item := range arr {
		if o, ok := item.(obj); ok {
			b.WriteString(str(o, "plain_text"))
		}
	}
	return b.String()
}

// pageTitle finds the title of a page or data source object.
func pageTitle(o obj) string {
	if t := richText(o["title"]); t != "" { // data sources and databases
		return t
	}
	props := sub(o, "properties")
	names := make([]string, 0, len(props))
	for n := range props {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if p, ok := props[n].(obj); ok && str(p, "type") == "title" {
			return richText(p["title"])
		}
	}
	return ""
}

// formatResults renders a list of search or query results, one per line.
func formatResults(results []any, max int) string {
	var b strings.Builder
	for i, r := range results {
		if i >= max {
			break
		}
		o, ok := r.(obj)
		if !ok {
			continue
		}
		title := oneLine(pageTitle(o))
		if title == "" {
			title = "(untitled)"
		}
		kind := str(o, "object")
		if kind == "data_source" {
			kind = "database"
		}
		edited := str(o, "last_edited_time")
		if len(edited) >= 10 {
			edited = edited[:10]
		}
		fmt.Fprintf(&b, "[%s] %s: %s", str(o, "id"), kind, clip(title, 100))
		if edited != "" {
			b.WriteString(" (edited " + edited + ")")
		}
		if u := str(o, "url"); u != "" {
			b.WriteString(" " + clip(u, 120))
		}
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

// formatProperties renders a page's properties as "name: value" lines.
func formatProperties(props obj) string {
	names := make([]string, 0, len(props))
	for n := range props {
		names = append(names, n)
	}
	sort.Strings(names)
	var b strings.Builder
	for _, n := range names {
		p, ok := props[n].(obj)
		if !ok {
			continue
		}
		if v := propertyValue(p); v != "" {
			fmt.Fprintf(&b, "%s: %s\n", clip(oneLine(n), 60), clip(oneLine(v), 200))
		}
	}
	return strings.TrimSpace(b.String())
}

func propertyValue(p obj) string {
	t := str(p, "type")
	v := p[t]
	switch t {
	case "title", "rich_text":
		return richText(v)
	case "number":
		if f, ok := v.(float64); ok {
			return fmt.Sprint(f)
		}
	case "select", "status":
		if o, ok := v.(obj); ok {
			return str(o, "name")
		}
	case "multi_select":
		var names []string
		for _, x := range list(p, t) {
			if o, ok := x.(obj); ok {
				names = append(names, str(o, "name"))
			}
		}
		return strings.Join(names, ", ")
	case "date":
		if o, ok := v.(obj); ok {
			s := str(o, "start")
			if e := str(o, "end"); e != "" {
				s += " to " + e
			}
			return s
		}
	case "checkbox":
		if b, ok := v.(bool); ok {
			return map[bool]string{true: "yes", false: "no"}[b]
		}
	case "url", "email", "phone_number":
		s, _ := v.(string)
		return s
	case "people":
		var names []string
		for _, x := range list(p, t) {
			if o, ok := x.(obj); ok {
				names = append(names, str(o, "name"))
			}
		}
		return strings.Join(names, ", ")
	case "relation":
		return fmt.Sprintf("%d linked", len(list(p, t)))
	}
	return ""
}

// formatBlocks renders a page's top-level blocks as readable text.
func formatBlocks(blocks []any, maxChars int) string {
	var b strings.Builder
	n := 0
	for _, x := range blocks {
		o, ok := x.(obj)
		if !ok {
			continue
		}
		t := str(o, "type")
		body := sub(o, t)
		text := richText(body["rich_text"])
		var line string
		switch t {
		case "paragraph":
			line = text
		case "heading_1":
			line = "# " + text
		case "heading_2":
			line = "## " + text
		case "heading_3":
			line = "### " + text
		case "bulleted_list_item":
			line = "- " + text
		case "numbered_list_item":
			n++
			line = fmt.Sprintf("%d. %s", n, text)
		case "to_do":
			box := "[ ] "
			if done, _ := body["checked"].(bool); done {
				box = "[x] "
			}
			line = box + text
		case "quote", "callout", "toggle":
			line = "> " + text
		case "code":
			line = "```" + str(body, "language") + "\n" + text + "\n```"
		case "divider":
			line = "---"
		case "child_page":
			line = "(subpage: " + str(body, "title") + ")"
		case "child_database":
			line = "(database: " + str(body, "title") + ")"
		default:
			if text != "" {
				line = text
			} else {
				line = "[" + t + "]"
			}
		}
		if t != "numbered_list_item" {
			n = 0
		}
		b.WriteString(clean(line) + "\n")
		if maxChars > 0 && b.Len() > maxChars*2 {
			break
		}
	}
	out := strings.TrimSpace(b.String())
	if r := []rune(out); maxChars > 0 && len(r) > maxChars {
		out = string(r[:maxChars]) + "…[cut]"
	}
	return out
}

// --- ids -------------------------------------------------------------------------------

var (
	uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-?[0-9a-fA-F]{4}-?[0-9a-fA-F]{4}-?[0-9a-fA-F]{4}-?[0-9a-fA-F]{12}$`)
	hexRe  = regexp.MustCompile(`[0-9a-fA-F]{32}`)
)

// parseID accepts a Notion id, with or without dashes, or a notion.so link, and
// returns the dashed form. Anything else is refused, so a value can never add
// path segments or query text to an API request.
func parseID(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if strings.Contains(s, "notion.so/") || strings.Contains(s, "notion.site/") {
		s, _, _ = strings.Cut(s, "?")
		s, _, _ = strings.Cut(s, "#")
		all := hexRe.FindAllString(s, -1)
		if len(all) == 0 {
			return "", false
		}
		s = all[len(all)-1]
	}
	if !uuidRe.MatchString(s) {
		return "", false
	}
	h := strings.ToLower(strings.ReplaceAll(s, "-", ""))
	return h[:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:], true
}
