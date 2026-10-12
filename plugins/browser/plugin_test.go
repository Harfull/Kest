package browser

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/Harfull/Kest/internal/plugin"
	"github.com/Harfull/Kest/internal/plugin/plugintest"
)

func TestContract(t *testing.T) {
	node, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	plugintest.RunContract(t, New, map[string]any{"node_path": node})
}

func TestBrowserArguments(t *testing.T) {
	tests := []struct {
		name, action string
		args         arguments
		wantError    bool
	}{
		{"public page", "navigate_page", arguments{URL: "https://example.com"}, false},
		{"local file", "navigate_page", arguments{URL: "file:///etc/passwd"}, true},
		{"inline script", "navigate_page", arguments{URL: "javascript:alert(1)"}, true},
		{"URL credentials", "navigate_page", arguments{URL: "https://secret@example.com"}, true},
		{"click needs a target", "click_element", arguments{}, true},
		{"click by ref", "click_element", arguments{Ref: "e3"}, false},
		{"click by frame ref", "click_element", arguments{Ref: "f2e10"}, false},
		{"malformed ref", "click_element", arguments{Ref: "e3]; alert(1)"}, true},
		{"key opens developer tools", "press_key", arguments{Key: "F12"}, true},
		{"key enters form", "press_key", arguments{Key: "Enter"}, false},
		{"key pages down", "press_key", arguments{Key: "PageDown"}, false},
		{"scroll down", "scroll_page", arguments{Direction: "down"}, false},
		{"scroll sideways", "scroll_page", arguments{Direction: "left"}, true},
		{"unbounded scroll", "scroll_page", arguments{Pixels: 10001}, true},
		{"negative scroll", "scroll_page", arguments{Pixels: -1}, true},
		{"missing file", "upload_file", arguments{Selector: "input"}, true},
		{"select needs a value", "select_option", arguments{Ref: "e1"}, true},
		{"find needs text", "find_text", arguments{}, true},
		{"history direction", "navigate_history", arguments{Direction: "sideways"}, true},
		{"history back", "navigate_history", arguments{Direction: "back"}, false},
		{"screenshot with no destination", "screenshot", arguments{}, true},
		{"screenshot to owner", "screenshot", arguments{Send: true}, false},
		{"screenshot to model", "screenshot", arguments{Look: true}, false},
		{"click off screen", "click_position", arguments{X: 1280, Y: 10}, true},
		{"read scope", "read_page", arguments{Scope: "everything"}, true},
		{"wait too long", "wait_for", arguments{WaitMS: 16000}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := validateArgs(tt.action, tt.args); (err != nil) != tt.wantError {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

// Anything that can change a website or send what the owner typed must still
// reach the owner's approval; only reading and looking run freely.
func TestRiskMatchesWhatEachToolCanDo(t *testing.T) {
	asks := map[string]bool{
		"navigate_page": true, "click_element": true, "click_position": true, "fill_field": true, "press_key": true,
		"select_option": true, "hover_element": true, "navigate_history": true, "upload_file": true,
	}
	free := []string{"read_page", "scroll_page", "find_text", "wait_for", "screenshot", "list_tabs", "close_tab"}
	byName := map[string]plugin.Tool{}
	for _, tool := range New().(*Plugin).Tools() {
		byName[tool.Name] = tool
	}
	for name := range asks {
		tool, ok := byName[name]
		if !ok {
			t.Fatalf("missing tool %s", name)
		}
		if !tool.Outbound || tool.Risk != plugin.RiskWrite {
			t.Errorf("%s can act on a website without approval", name)
		}
		if tool.Describe == nil {
			t.Errorf("%s has no readable approval summary", name)
		}
	}
	for _, name := range free {
		tool, ok := byName[name]
		if !ok {
			t.Fatalf("missing tool %s", name)
		}
		if tool.Outbound || tool.Risk != plugin.RiskRead {
			t.Errorf("%s should run without approval", name)
		}
	}
	if byName["upload_file"].Grantable {
		t.Error("uploads must be approved one at a time")
	}
	if !byName["click_element"].Grantable {
		t.Error("click_element should accept a task grant")
	}
}

func TestToolsFitInAPrompt(t *testing.T) {
	size := 0
	for _, tool := range New().(*Plugin).Tools() {
		size += len(tool.Description) + len(tool.Schema)
		if len(tool.Description) > 330 {
			t.Errorf("%s description is %d characters; the browser's tools are sent on every turn", tool.Name, len(tool.Description))
		}
	}
	if size > 11000 {
		t.Errorf("browser tool definitions are %d bytes; trim them", size)
	}
}

func TestProxyRefusesPrivateDestinations(t *testing.T) {
	h := plugintest.NewHost()
	h.Client = plugin.NewHTTPClient(plugin.HTTPOptions{AllowHosts: []string{"*"}})
	px := &proxy{host: h, username: "test", password: "secret"}
	for _, tt := range []struct {
		name, method, target string
		status               int
	}{
		{"IPv4 loopback", "GET", "http://127.0.0.1/", 502},
		{"metadata", "GET", "http://169.254.169.254/", 502},
		{"IPv6 loopback", "GET", "http://[::1]/", 502},
		{"HTTPS private", "CONNECT", "http://127.0.0.1:443/", 502},
		{"non web port", "CONNECT", "http://example.com:22/", 403},
	} {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequestWithContext(context.Background(), tt.method, tt.target, nil)
			if tt.method == http.MethodConnect {
				req.Host = strings.TrimSuffix(strings.TrimPrefix(tt.target, "http://"), "/")
			}
			req.Header.Set("Proxy-Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("test:secret")))
			res := httptest.NewRecorder()
			px.ServeHTTP(res, req)
			if res.Code != tt.status {
				t.Fatalf("status = %d; %s", res.Code, res.Body.String())
			}
		})
	}
}

func TestProxyRequiresAuthentication(t *testing.T) {
	px := &proxy{username: "test", password: "secret"}
	res := httptest.NewRecorder()
	px.ServeHTTP(res, httptest.NewRequestWithContext(context.Background(), "GET", "http://example.com/", nil))
	if res.Code != http.StatusProxyAuthRequired {
		t.Fatalf("status = %d", res.Code)
	}
}

func TestCloseAndCanceledCallsDoNotLaunch(t *testing.T) {
	p := New().(*Plugin)
	h := plugintest.NewHost()
	h.Cfg = plugin.Config{"timeout_seconds": 1}
	p.host = h
	if err := p.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, err := p.run(context.Background(), "read_page", plugin.Call{Args: []byte(`{}`)})
	if err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p.gate <- struct{}{}
	_, err = p.run(ctx, "read_page", plugin.Call{Args: []byte(`{}`)})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
	<-p.gate
}
