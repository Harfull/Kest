package webfetch_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Harfull/Kest/internal/plugin"
	"github.com/Harfull/Kest/internal/plugin/plugintest"
	"github.com/Harfull/Kest/plugins/webfetch"
)

func setup(t *testing.T) (*webfetch.Plugin, *plugintest.Host) {
	t.Helper()
	h := plugintest.NewHost()
	h.Cfg["max_chars"] = 6000
	h.Cfg["user_agent"] = "KestTest/1"
	p := webfetch.New().(*webfetch.Plugin)
	if err := p.Init(context.Background(), h); err != nil {
		t.Fatal(err)
	}
	return p, h
}

func fetch(t *testing.T, p *webfetch.Plugin, url string) (plugin.Result, error) {
	t.Helper()
	args, _ := json.Marshal(map[string]string{"url": url})
	return p.Tools()[0].Handler(context.Background(), plugin.Call{Args: args})
}

func TestContract(t *testing.T) {
	plugintest.RunContract(t, webfetch.New, map[string]any{})
}

const page = `<!doctype html><html><head><title>  Running   plan </title>
<style>body{color:red}</style><script>alert("steal")</script></head>
<body><nav>Home | About</nav>
<h1>Week one</h1><p>Run   three times.</p><p>Rest&nbsp;on <b>Sunday</b>.</p>
<ul><li>Easy pace</li><li>No hills</li></ul>
<script>document.write("hidden")</script><noscript>enable js</noscript>
<footer>copyright</footer></body></html>`

func TestFetchExtractsReadableText(t *testing.T) {
	var gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(page))
	}))
	defer srv.Close()
	p, _ := setup(t)

	res, err := fetch(t, p, srv.URL+"/plan")
	if err != nil {
		t.Fatalf("fetch error = %v", err)
	}

	if !res.Untrusted {
		t.Fatal("web content must be marked untrusted")
	}
	for _, want := range []string{"Title: Running plan", "Week one", "Run three times.", "Easy pace", "No hills", "Sunday"} {
		if !strings.Contains(res.Content, want) {
			t.Errorf("missing %q in:\n%s", want, res.Content)
		}
	}
	for _, leak := range []string{"alert(", "steal", "color:red", "hidden", "enable js", "copyright", "Home | About"} {
		if strings.Contains(res.Content, leak) {
			t.Errorf("non-content %q leaked into the text:\n%s", leak, res.Content)
		}
	}
	if gotUA != "KestTest/1" {
		t.Errorf("User-Agent = %q", gotUA)
	}
}

func TestFetchPlainTextAndJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "json") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ok": true}`))
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("line one\n\n\n\nline   two"))
	}))
	defer srv.Close()
	p, _ := setup(t)

	res, err := fetch(t, p, srv.URL+"/notes.txt")
	if err != nil || !strings.HasSuffix(res.Content, "line one\n\nline two") {
		t.Errorf("text = %q, %v", res.Content, err)
	}
	if res, err = fetch(t, p, srv.URL+"/data.json"); err != nil || !strings.Contains(res.Content, `{"ok": true}`) {
		t.Errorf("json = %q, %v", res.Content, err)
	}
}

func TestFetchTruncatesLongPages(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(strings.Repeat("word ", 5000)))
	}))
	defer srv.Close()
	p, h := setup(t)
	h.Cfg["max_chars"] = 100

	res, err := fetch(t, p, srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if body := res.Content[strings.Index(res.Content, "\n\n")+2:]; len([]rune(body)) > 120 || !strings.HasSuffix(body, "[cut]") {
		t.Errorf("body not cut to the limit: %d runes", len([]rune(body)))
	}
}

func TestFetchErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/missing":
			http.NotFound(w, r)
		case "/pdf":
			w.Header().Set("Content-Type", "application/pdf")
			_, _ = w.Write([]byte("%PDF-1.4"))
		case "/empty":
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte("<html><body><script>x</script></body></html>"))
		}
	}))
	defer srv.Close()
	p, _ := setup(t)

	tests := []struct{ name, url, want string }{
		{"not found", srv.URL + "/missing", "status 404"},
		{"unsupported type", srv.URL + "/pdf", "can't read content of type"},
		{"no text", srv.URL + "/empty", "no readable text"},
		{"no scheme", "example.com/page", "full http"},
		{"wrong scheme", "file:///etc/passwd", "full http"},
		{"ftp", "ftp://example.com/x", "full http"},
		{"empty", "", "url is required"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := fetch(t, p, tt.url)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %v, want it to mention %q", err, tt.want)
			}
		})
	}
}

// With the production network policy the plugin must not reach internal services.
func TestFetchRefusesInternalAddresses(t *testing.T) {
	internal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("internal secrets"))
	}))
	defer internal.Close()

	p, h := setup(t)
	h.Client = plugin.NewHTTPClient(plugin.HTTPOptions{AllowHosts: []string{"*"}}) // AllowPrivate stays false

	for _, u := range []string{internal.URL, "http://169.254.169.254/latest/meta-data/", "http://[::1]:8080/"} {
		res, err := fetch(t, p, u)
		if err == nil {
			t.Errorf("%s was fetched: %q", u, res.Content)
			continue
		}
		if strings.Contains(err.Error(), "internal secrets") {
			t.Errorf("error leaks response content: %v", err)
		}
	}

	_, err := fetch(t, p, internal.URL)
	if err == nil || !strings.Contains(err.Error(), "isn't allowed") {
		t.Errorf("error = %v; the model should be told the address is not allowed", err)
	}
	if errors.Is(err, plugin.ErrBlocked) {
		t.Log("note: raw policy error is wrapped away from the model, which is intended")
	}
}

func TestFetchFollowsAllowedRedirects(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/old" {
			http.Redirect(w, r, srv.URL+"/new", http.StatusMovedPermanently)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("moved here"))
	}))
	defer srv.Close()
	p, _ := setup(t)

	res, err := fetch(t, p, srv.URL+"/old")
	if err != nil || !strings.Contains(res.Content, "moved here") || !strings.Contains(res.Content, "/new") {
		t.Errorf("result = %q, %v", res.Content, err)
	}
}
