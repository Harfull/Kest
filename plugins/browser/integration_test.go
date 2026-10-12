//go:build integration

package browser

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image/jpeg"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/Harfull/Kest/internal/plugin"
	"github.com/Harfull/Kest/internal/plugin/plugintest"
)

// pages is a tiny website served in memory through the plugin's own HTTP client,
// which is the path the browser's proxy takes.
func pages() map[string]string {
	var long strings.Builder
	for i := 1; i <= 40; i++ {
		fmt.Fprintf(&long, `<section style="height:400px"><h2>Section %d</h2><p>Plain filler paragraph %d.</p></section>`, i, i)
		if i == 30 {
			long.WriteString(`<section style="height:400px"><h2 id="needle">The needle lives here</h2></section>`)
		}
	}
	return map[string]string{
		"/": `<!doctype html><title>Long page</title><h1>Top of the long page</h1>
<a href="/two">Next page</a> <a href="/popup" target="_blank">Open popup</a>
<input id="q" placeholder="Search"> <select id="size"><option value="s">Small</option><option value="l">Large</option></select>
<button id="alert" onclick="alert('Are you sure?')">Warn me</button>
<button id="save" onclick="localStorage.setItem('note',document.querySelector('#q').value);document.cookie='session=kept; Max-Age=3600';document.querySelector('#status').textContent=localStorage.getItem('note')">Save</button>
<p id="status"></p><input id="file" type="file"><iframe src="/frame" width="300" height="60"></iframe>` + long.String() +
			`<script>document.querySelector('#status').textContent=(localStorage.getItem('note')||'empty')+' '+document.cookie</script>`,
		"/two":    `<!doctype html><title>Second</title><h1>Second page</h1><a href="/">Home</a>`,
		"/popup":  `<!doctype html><title>Popup</title><h1>Popup page</h1>`,
		"/frame":  `<!doctype html><button>Inside frame</button>`,
		"/status": `<!doctype html><title>Gone</title><h1>Missing</h1>`,
	}
}

type pageTransport struct{ pages map[string]string }

func (t pageTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Hostname() != "example.test" {
		return nil, plugin.ErrBlocked
	}
	body, ok := t.pages[r.URL.Path]
	status := 200
	if !ok {
		body, status = "<h1>Not found</h1>", 404
	}
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"text/html"}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
}

type session struct {
	t *testing.T
	h *plugintest.Host
	p *Plugin
}

func startSession(t *testing.T) *session {
	t.Helper()
	module := os.Getenv("KEST_BROWSER_TEST_MODULE_PATH")
	if module == "" {
		t.Skip("set KEST_BROWSER_TEST_MODULE_PATH to the installed Playwright directory")
	}
	h := plugintest.NewHost()
	h.Dir = t.TempDir()
	h.Client = &http.Client{Transport: pageTransport{pages()}}
	cfg, err := New().Manifest().ResolveConfig(map[string]any{"module_path": module})
	if err != nil {
		t.Fatal(err)
	}
	h.Cfg = cfg
	if os.Getenv("KEST_BROWSER_TEST_HEADLESS") == "false" {
		h.Cfg["headless"] = false
	}
	s := &session{t: t, h: h}
	s.restart()
	return s
}

func (s *session) restart() {
	if s.p != nil {
		if err := s.p.Close(context.Background()); err != nil {
			s.t.Fatal(err)
		}
	}
	p := New().(*Plugin)
	if err := p.Init(context.Background(), s.h); err != nil {
		s.t.Fatal(err)
	}
	s.t.Cleanup(func() { _ = p.Close(context.Background()) })
	s.p = p
}

func (s *session) try(action string, args arguments) (plugin.Result, error) {
	data, _ := json.Marshal(args)
	return s.p.run(context.Background(), action, plugin.Call{Args: data})
}

func (s *session) do(action string, args arguments) plugin.Result {
	s.t.Helper()
	res, err := s.try(action, args)
	if err != nil {
		s.t.Fatalf("%s: %v", action, err)
	}
	if !res.Untrusted {
		s.t.Fatalf("%s: browser result was trusted", action)
	}
	return res
}

// ref finds the ref of the element whose line contains label.
func ref(t *testing.T, content, label string) string {
	t.Helper()
	m := regexp.MustCompile(`\[([f0-9]*e[0-9]+)\][^\n]*` + regexp.QuoteMeta(label)).FindStringSubmatch(content)
	if m == nil {
		t.Fatalf("no element %q in:\n%s", label, content)
	}
	return m[1]
}

func mustContain(t *testing.T, content string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(content, want) {
			t.Fatalf("missing %q in:\n%s", want, content)
		}
	}
}

func TestBrowsingALongPage(t *testing.T) {
	s := startSession(t)

	res := s.do("navigate_page", arguments{URL: "http://example.test/"})
	mustContain(t, res.Content, "Long page", "top of the page", "Top of the long page", `link "Next page"`, `dropdown "size"`)
	first := res.Content

	// Scrolling shows new text and says where the screen is.
	res = s.do("scroll_page", arguments{Direction: "down"})
	mustContain(t, res.Content, "Text on screen right now:", "down (screen shows")
	if strings.Contains(res.Content, "Top of the long page") {
		t.Fatalf("scrolled view still shows the top:\n%s", res.Content)
	}
	if res.Content == first {
		t.Fatal("scrolling changed nothing")
	}
	res = s.do("scroll_page", arguments{Direction: "down", Pixels: 3000})
	mustContain(t, res.Content, "Section")

	// Finding text jumps to it.
	res = s.do("find_text", arguments{Text: "needle lives"})
	mustContain(t, res.Content, "1 match(es)", "The needle lives here")

	res = s.do("scroll_page", arguments{Direction: "bottom"})
	mustContain(t, res.Content, "bottom of the page")
	res = s.do("scroll_page", arguments{Direction: "down"})
	mustContain(t, res.Content, "did not move")
	res = s.do("scroll_page", arguments{Direction: "top"})
	mustContain(t, res.Content, "top of the page", "Top of the long page")

	// The text of a long page can be read in parts.
	res = s.do("read_page", arguments{Scope: "page", Offset: 40})
	mustContain(t, res.Content, "characters 40 to")
}

func TestActingOnRefs(t *testing.T) {
	s := startSession(t)
	res := s.do("navigate_page", arguments{URL: "http://example.test/"})

	s.do("fill_field", arguments{Ref: ref(t, res.Content, `"Search"`), Text: "remember me"})
	res = s.do("select_option", arguments{Ref: ref(t, res.Content, "dropdown"), Value: "Large"})
	mustContain(t, res.Content, "Large*")
	res = s.do("click_element", arguments{Ref: ref(t, res.Content, `button "Save"`)})
	mustContain(t, res.Content, "remember me")

	// A dialog is dismissed and reported rather than hanging the page.
	res = s.do("click_element", arguments{Ref: ref(t, res.Content, `"Warn me"`)})
	mustContain(t, res.Content, "alert dialog was dismissed: Are you sure?")

	// A link that opens a popup moves to the new tab and says so.
	res = s.do("click_element", arguments{Ref: ref(t, res.Content, `"Open popup"`)})
	mustContain(t, res.Content, "a new tab opened", "Popup page")
	res = s.do("list_tabs", arguments{})
	mustContain(t, res.Content, "Open tabs:", "Long page", "Popup")

	// Elements inside a frame are addressed with a frame-prefixed ref.
	res = s.do("read_page", arguments{Tab: 1})
	frame := ref(t, res.Content, `"Inside frame"`)
	if !strings.HasPrefix(frame, "f") {
		t.Fatalf("frame ref = %s", frame)
	}
	s.do("click_element", arguments{Ref: frame})

	// Following a link and going back.
	res = s.do("click_element", arguments{Tab: 1, Ref: ref(t, res.Content, `"Next page"`)})
	mustContain(t, res.Content, "Second page")
	res = s.do("navigate_history", arguments{Direction: "back"})
	mustContain(t, res.Content, "Top of the long page")
	res = s.do("close_tab", arguments{Tab: 2})
	mustContain(t, res.Content, "Closed tab 2")

	// A bad ref is a normal, explained failure and does not restart the browser.
	_, err := s.try("click_element", arguments{Ref: "e9999"})
	if err == nil || !strings.Contains(err.Error(), "ref") {
		t.Fatalf("error = %v", err)
	}
	res = s.do("list_tabs", arguments{})
	mustContain(t, res.Content, "Long page")

	res = s.do("navigate_page", arguments{URL: "http://example.test/missing"})
	mustContain(t, res.Content, "HTTP 404")
}

func TestScreenshotsGoWhereAsked(t *testing.T) {
	s := startSession(t)
	s.do("navigate_page", arguments{URL: "http://example.test/"})

	res := s.do("screenshot", arguments{Send: true, Caption: "the page"})
	mustContain(t, res.Content, "Sent to the owner in Telegram")
	if res.Image != nil {
		t.Fatal("pixels went to the model although only send was asked")
	}
	notes := s.h.Notifications()
	if len(notes) != 1 || notes[0].Text != "the page" || len(notes[0].Photo) < 1000 || !bytes.HasPrefix(notes[0].Photo, []byte("\x89PNG")) {
		t.Fatalf("notifications = %d", len(notes))
	}

	// A page too tall to read as one image is refused with advice, not squashed.
	if _, err := s.try("screenshot", arguments{Look: true, FullPage: true}); err == nil || !strings.Contains(err.Error(), "too tall") {
		t.Fatalf("tall full-page error = %v", err)
	}
	s.do("navigate_page", arguments{URL: "http://example.test/two"})
	res = s.do("screenshot", arguments{Look: true, FullPage: true})
	if res.Image == nil || res.Image.MediaType != "image/jpeg" {
		t.Fatalf("image = %+v", res.Image)
	}
	if _, err := jpeg.DecodeConfig(bytes.NewReader(res.Image.Data)); err != nil {
		t.Fatalf("the model's image is not a JPEG: %v", err)
	}
	mustContain(t, res.Content, "attached")
	if strings.Contains(res.Content, "Sent to the owner") {
		t.Fatal("claimed a send that did not happen")
	}
	if len(s.h.Notifications()) != 1 {
		t.Fatal("a look-only screenshot reached Telegram")
	}

	res = s.do("screenshot", arguments{Look: true, Ref: ref(t, s.do("read_page", arguments{}).Content, `link "Home"`)})
	mustContain(t, res.Content, "one element")

	left, _ := filepath.Glob(filepath.Join(s.h.Dir, "screenshots", "*"))
	if len(left) != 0 {
		t.Fatalf("screenshot files were kept: %v", left)
	}
}

func TestPersistentChromiumSession(t *testing.T) {
	s := startSession(t)
	res := s.do("navigate_page", arguments{URL: "http://example.test/"})
	s.do("fill_field", arguments{Ref: ref(t, res.Content, `"Search"`), Text: "remember me"})
	s.do("click_element", arguments{Ref: ref(t, res.Content, `button "Save"`)})
	if err := os.WriteFile(filepath.Join(s.h.Dir, "inbox", "upload.txt"), []byte("owner file"), 0o600); err != nil {
		t.Fatal(err)
	}
	s.do("upload_file", arguments{Selector: "#file", File: "upload.txt"})

	s.restart()
	res = s.do("navigate_page", arguments{URL: "http://example.test/"})
	mustContain(t, res.Content, "remember me", "session=kept")

	// The proxy must prevent a public page redirecting into a local service.
	if res, err := s.try("navigate_page", arguments{URL: "http://127.0.0.1/"}); err == nil {
		t.Fatalf("loopback navigation was not refused: %s", res.Content)
	}
}

func TestPublicHTTPSChromium(t *testing.T) {
	module := os.Getenv("KEST_BROWSER_TEST_MODULE_PATH")
	url := os.Getenv("KEST_BROWSER_TEST_PUBLIC_URL")
	if module == "" || url == "" {
		t.Skip("set browser test module and public URL to enable external HTTPS smoke test")
	}
	h := plugintest.NewHost()
	h.Dir = t.TempDir()
	h.Client = plugin.NewHTTPClient(plugin.HTTPOptions{AllowHosts: []string{"*"}})
	cfg, err := New().Manifest().ResolveConfig(map[string]any{"module_path": module})
	if err != nil {
		t.Fatal(err)
	}
	h.Cfg = cfg
	p := New().(*Plugin)
	if err := p.Init(context.Background(), h); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Close(context.Background()) })
	data, _ := json.Marshal(arguments{URL: url})
	res, err := p.run(context.Background(), "navigate_page", plugin.Call{Args: data})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Content, "Example Domain") {
		t.Fatalf("unexpected HTTPS page: %s", res.Content)
	}
}
