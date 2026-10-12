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
		{"blank selector", "click_element", arguments{}, true},
		{"key opens developer tools", "press_key", arguments{Key: "F12"}, true},
		{"key enters form", "press_key", arguments{Key: "Enter"}, false},
		{"unbounded scroll", "scroll_page", arguments{Pixels: 10001}, true},
		{"missing file", "upload_file", arguments{Selector: "input"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := validateArgs(tt.action, tt.args); (err != nil) != tt.wantError {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestOutboundActionsRequireApproval(t *testing.T) {
	p := New()
	for _, tool := range p.Tools() {
		switch tool.Name {
		case "navigate_page", "click_element", "fill_field", "press_key", "upload_file":
			if !tool.Outbound || tool.Risk != plugin.RiskWrite {
				t.Errorf("%s can send data without approval", tool.Name)
			}
		}
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
