//go:build integration

package browser

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Harfull/Kest/internal/plugin"
	"github.com/Harfull/Kest/internal/plugin/plugintest"
)

type pageTransport struct{}

func (pageTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Hostname() != "example.test" {
		return nil, plugin.ErrBlocked
	}
	body := `<!doctype html><title>Browser fixture</title><h1>Local browser test</h1><input id="note"><input id="file" type="file"><button id="save" onclick="localStorage.setItem('note',document.querySelector('#note').value);document.cookie='session=kept; Max-Age=3600';document.querySelector('#status').textContent=localStorage.getItem('note')">Save</button><p id="status"></p><script>document.querySelector('#status').textContent=(localStorage.getItem('note')||'empty')+' '+document.cookie</script>`
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/html"}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
}

func TestPersistentChromiumSession(t *testing.T) {
	module := os.Getenv("KEST_BROWSER_TEST_MODULE_PATH")
	if module == "" {
		t.Skip("set KEST_BROWSER_TEST_MODULE_PATH to the installed Playwright directory")
	}
	h := plugintest.NewHost()
	h.Dir = t.TempDir()
	h.Client = &http.Client{Transport: pageTransport{}}
	cfg, err := New().Manifest().ResolveConfig(map[string]any{"module_path": module})
	if err != nil {
		t.Fatal(err)
	}
	h.Cfg = cfg
	if os.Getenv("KEST_BROWSER_TEST_HEADLESS") == "false" {
		h.Cfg["headless"] = false
	}
	start := func() *Plugin {
		p := New().(*Plugin)
		if err := p.Init(context.Background(), h); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = p.Close(context.Background()) })
		return p
	}
	call := func(p *Plugin, action string, args arguments) plugin.Result {
		data, _ := json.Marshal(args)
		res, err := p.run(context.Background(), action, plugin.Call{Args: data})
		if err != nil {
			t.Fatalf("%s: %v", action, err)
		}
		if !res.Untrusted {
			t.Fatal("browser result was trusted")
		}
		return res
	}
	p := start()
	res := call(p, "navigate_page", arguments{URL: "http://example.test/"})
	if !strings.Contains(res.Content, "Local browser test") {
		t.Fatal(res.Content)
	}
	call(p, "fill_field", arguments{Selector: "#note", Text: "remember me"})
	call(p, "click_element", arguments{Selector: "#save"})
	if err := os.WriteFile(filepath.Join(h.Dir, "inbox", "upload.txt"), []byte("owner file"), 0o600); err != nil {
		t.Fatal(err)
	}
	call(p, "upload_file", arguments{Selector: "#file", File: "upload.txt"})
	res = call(p, "capture_screenshot", arguments{})
	var shot struct {
		Screenshot string `json:"screenshot"`
	}
	if err := json.Unmarshal([]byte(res.Content), &shot); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(shot.Screenshot); err != nil || info.Size() == 0 {
		t.Fatalf("screenshot: %v", err)
	}
	if err := p.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	p = start()
	res = call(p, "navigate_page", arguments{URL: "http://example.test/"})
	for _, want := range []string{"remember me", "session=kept"} {
		if !strings.Contains(res.Content, want) {
			t.Fatalf("profile lost %q: %s", want, res.Content)
		}
	}
	// The proxy must prevent a public page redirecting into a local service.
	data := []byte(`{"url":"http://127.0.0.1/"}`)
	res, err = p.run(context.Background(), "navigate_page", plugin.Call{Args: data})
	if err == nil {
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
