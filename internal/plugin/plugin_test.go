package plugin_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"github.com/Harfull/Kest/internal/plugin"
	"github.com/Harfull/Kest/internal/plugin/plugintest"
)

const goodManifest = `
id = "demo"
name = "Demo"
version = "0.1.0"
description = "A demo plugin."

[capabilities]
network = ["example.com"]
secrets = ["DEMO_TOKEN"]

[config]
greeting = { type = "string", default = "hello" }
limit    = { type = "int", default = 5 }
ratio    = { type = "float" }
loud     = { type = "bool", default = false }
token_id = { type = "string", required = true }

[[tools]]
name = "say"
risk = "read"

[[tools]]
name = "erase"
risk = "destructive"
`

func TestParseRisk(t *testing.T) {
	for in, want := range map[string]plugin.Risk{"read": plugin.RiskRead, "WRITE": plugin.RiskWrite, "destructive": plugin.RiskDestructive} {
		got, err := plugin.ParseRisk(in)
		if err != nil || got != want {
			t.Errorf("ParseRisk(%q) = %v, %v", in, got, err)
		}
		if got.String() == "" {
			t.Error("empty String()")
		}
	}
	if _, err := plugin.ParseRisk("scary"); err == nil {
		t.Error("ParseRisk accepted junk")
	}
}

func TestParseManifest(t *testing.T) {
	m, err := plugin.ParseManifest([]byte(goodManifest))
	if err != nil {
		t.Fatalf("ParseManifest() error = %v", err)
	}
	if m.ID != "demo" || len(m.Tools) != 2 || m.Capabilities.Network[0] != "example.com" {
		t.Errorf("manifest = %+v", m)
	}

	bad := map[string]string{
		"unknown key":    goodManifest + "\nsurprise = 1\n",
		"bad id":         strings.Replace(goodManifest, `id = "demo"`, `id = "Demo_Plugin"`, 1),
		"missing name":   strings.Replace(goodManifest, `name = "Demo"`, `name = ""`, 1),
		"bad risk":       strings.Replace(goodManifest, `risk = "read"`, `risk = "spicy"`, 1),
		"duplicate tool": goodManifest + "\n[[tools]]\nname = \"say\"\nrisk = \"read\"\n",
		"bad config type": strings.Replace(goodManifest,
			`limit    = { type = "int", default = 5 }`, `limit = { type = "number" }`, 1),
		"default wrong type": strings.Replace(goodManifest,
			`limit    = { type = "int", default = 5 }`, `limit = { type = "int", default = "five" }`, 1),
	}
	for name, src := range bad {
		t.Run(name, func(t *testing.T) {
			if _, err := plugin.ParseManifest([]byte(src)); err == nil {
				t.Error("ParseManifest() succeeded, want error")
			}
		})
	}
}

func TestResolveConfig(t *testing.T) {
	m, _ := plugin.ParseManifest([]byte(goodManifest))

	cfg, err := m.ResolveConfig(map[string]any{"token_id": "abc", "limit": int64(9), "ratio": int64(2)})
	if err != nil {
		t.Fatalf("ResolveConfig() error = %v", err)
	}
	if cfg.String("greeting") != "hello" || cfg.Int("limit") != 9 || cfg.Float("ratio") != 2 || cfg.Bool("loud") || cfg.String("token_id") != "abc" {
		t.Errorf("cfg = %v", cfg)
	}

	for name, raw := range map[string]map[string]any{
		"required missing": {},
		"unknown key":      {"token_id": "x", "colour": "red"},
		"wrong type":       {"token_id": "x", "limit": "many"},
	} {
		if _, err := m.ResolveConfig(raw); err == nil {
			t.Errorf("%s: ResolveConfig() succeeded", name)
		}
	}
}

// demo is a configurable test plugin.
type demo struct {
	manifest plugin.Manifest
	tools    []plugin.Tool
	initErr  error
	panics   bool
	closed   *bool
	host     plugin.Host
}

func (d *demo) Manifest() plugin.Manifest { return d.manifest }
func (d *demo) Init(_ context.Context, h plugin.Host) error {
	if d.panics {
		panic("boom")
	}
	d.host = h
	return d.initErr
}
func (d *demo) Tools() []plugin.Tool { return d.tools }
func (d *demo) Close(context.Context) error {
	if d.closed != nil {
		*d.closed = true
	}
	return nil
}

type jobDemo struct{ *demo }

func (j jobDemo) JobNames() []string { return []string{"tick"} }
func (j jobDemo) RunJob(_ context.Context, name, payload string) error {
	return errors.New("ran " + name + ":" + payload)
}

func tool(name string, risk plugin.Risk) plugin.Tool {
	return plugin.Tool{
		Name: name, Description: "does " + name, Schema: json.RawMessage(`{"type":"object"}`), Risk: risk,
		Handler: func(context.Context, plugin.Call) (plugin.Result, error) { return plugin.Result{Content: "ok"}, nil },
	}
}

func newDemo() *demo {
	m, _ := plugin.ParseManifest([]byte(goodManifest))
	return &demo{manifest: m, tools: []plugin.Tool{tool("say", plugin.RiskRead), tool("erase", plugin.RiskDestructive)}}
}

func TestRegistryLoadsAndServesTools(t *testing.T) {
	reg := plugin.NewRegistry(plugintest.Deps())
	closed := false
	d := newDemo()
	d.closed = &closed

	if err := reg.Load(context.Background(), "demo", func() plugin.Plugin { return d }, map[string]any{"token_id": "t"}); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	tools := reg.Tools()
	if len(tools) != 2 || tools[0].Wire() != "demo__erase" || tools[1].Full() != "demo.say" {
		t.Fatalf("tools = %+v", tools)
	}
	if _, ok := reg.Lookup("demo__say"); !ok {
		t.Error("Lookup(demo__say) failed")
	}
	if _, ok := reg.Lookup("demo.say"); ok {
		t.Error("Lookup accepted a dotted name")
	}
	if st := reg.Statuses(); len(st) != 1 || st[0].Err != nil || st[0].Tools != 2 {
		t.Errorf("statuses = %+v", st)
	}

	if err := reg.Close(context.Background()); err != nil || !closed {
		t.Errorf("Close() = %v, closed = %v", err, closed)
	}
	if len(reg.Tools()) != 0 {
		t.Error("tools remain after Close")
	}
}

func TestRegistryRejectsInconsistentPlugins(t *testing.T) {
	cfg := map[string]any{"token_id": "t"}
	tests := []struct {
		name   string
		mutate func(*demo)
		id     string
	}{
		{"risk understated in manifest", func(d *demo) { d.tools[1] = tool("erase", plugin.RiskWrite) }, "demo"},
		{"tool missing from manifest", func(d *demo) { d.tools = append(d.tools, tool("extra", plugin.RiskRead)) }, "demo"},
		{"manifest tool not provided", func(d *demo) { d.tools = d.tools[:1] }, "demo"},
		{"invalid tool", func(d *demo) { d.tools[0].Description = "" }, "demo"},
		{"init fails", func(d *demo) { d.initErr = errors.New("no network") }, "demo"},
		{"init panics", func(d *demo) { d.panics = true }, "demo"},
		{"id mismatch", func(*demo) {}, "other"},
		{"jobs declared but not implemented", func(d *demo) { d.manifest.Capabilities.Jobs = true }, "demo"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reg := plugin.NewRegistry(plugintest.Deps())
			d := newDemo()
			tt.mutate(d)

			err := reg.Load(context.Background(), tt.id, func() plugin.Plugin { return d }, cfg)
			if err == nil {
				t.Fatal("Load() succeeded, want error")
			}
			if len(reg.Tools()) != 0 {
				t.Error("a rejected plugin exposed tools")
			}
			if st := reg.Statuses(); len(st) != 1 || st[0].Err == nil {
				t.Errorf("statuses = %+v, want one disabled entry", st)
			}
		})
	}
}

func TestOnePluginFailingDoesNotAffectOthers(t *testing.T) {
	reg := plugin.NewRegistry(plugintest.Deps())
	cfg := map[string]any{"token_id": "t"}

	bad := newDemo()
	bad.initErr = errors.New("nope")
	_ = reg.Load(context.Background(), "demo", func() plugin.Plugin { return bad }, cfg)

	m, _ := plugin.ParseManifest([]byte(`id="good"
name="Good"
version="1"
description="fine"
[[tools]]
name="ping"
risk="read"`))
	good := &demo{manifest: m, tools: []plugin.Tool{tool("ping", plugin.RiskRead)}}
	if err := reg.Load(context.Background(), "good", func() plugin.Plugin { return good }, nil); err != nil {
		t.Fatal(err)
	}
	if _, ok := reg.Lookup("good__ping"); !ok {
		t.Error("healthy plugin not served after a sibling failed")
	}
}

func TestJobHandlers(t *testing.T) {
	m, _ := plugin.ParseManifest([]byte(`id="clock"
name="Clock"
version="1"
description="ticks"
[capabilities]
jobs = true
[[tools]]
name="noop"
risk="read"`))
	d := &demo{manifest: m, tools: []plugin.Tool{tool("noop", plugin.RiskRead)}}
	reg := plugin.NewRegistry(plugintest.Deps())
	if err := reg.Load(context.Background(), "clock", func() plugin.Plugin { return jobDemo{d} }, nil); err != nil {
		t.Fatal(err)
	}

	h, ok := reg.JobHandlers()["clock/tick"]
	if !ok {
		t.Fatalf("handlers = %v", reg.JobHandlers())
	}
	if err := h(context.Background(), "p"); err == nil || err.Error() != "ran tick:p" {
		t.Errorf("handler result = %v", err)
	}
}

func TestHostSecretsAreRestrictedToManifest(t *testing.T) {
	deps := plugintest.Deps()
	deps.Secret = func(name string) (string, bool) { return "value-of-" + name, true }
	reg := plugin.NewRegistry(deps)

	d := newDemo()
	if err := reg.Load(context.Background(), "demo", func() plugin.Plugin { return d }, map[string]any{"token_id": "t"}); err != nil {
		t.Fatal(err)
	}

	if v, err := d.host.Secret("DEMO_TOKEN"); err != nil || v != "value-of-DEMO_TOKEN" {
		t.Errorf("declared secret = %q, %v", v, err)
	}
	if _, err := d.host.Secret("OTHER_PLUGINS_TOKEN"); err == nil {
		t.Error("plugin read a secret it did not declare")
	}
}

func TestHostNotifyAndStorageScoping(t *testing.T) {
	var got []plugin.Notification
	deps := plugintest.Deps()
	deps.Notifier = func(_ context.Context, n plugin.Notification) error { got = append(got, n); return nil }
	reg := plugin.NewRegistry(deps)

	d := newDemo()
	if err := reg.Load(context.Background(), "demo", func() plugin.Plugin { return d }, map[string]any{"token_id": "t"}); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	if err := d.host.Notify(ctx, plugin.Notification{Text: "  hi  "}); err != nil || len(got) != 1 || got[0].Text != "hi" {
		t.Errorf("Notify: %v %v", err, got)
	}
	if err := d.host.Notify(ctx, plugin.Notification{Text: "  "}); err == nil {
		t.Error("blank notification accepted")
	}

	if err := d.host.KV().Set(ctx, "k", "v"); err != nil {
		t.Fatal(err)
	}
	if v, err := d.host.KV().Get(ctx, "k"); err != nil || v != "v" {
		t.Errorf("KV round trip = %q, %v", v, err)
	}
}

func TestContractHelperAcceptsGoodPlugin(t *testing.T) {
	plugintest.RunContract(t, func() plugin.Plugin {
		d := newDemo()
		return d
	}, map[string]any{"token_id": "t"})
}

// --- HTTP policy ---------------------------------------------------------------

func TestHTTPClientAllowlist(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("hi")) }))
	defer ok.Close()

	c := plugin.NewHTTPClient(plugin.HTTPOptions{AllowHosts: []string{"127.0.0.1"}, AllowPrivate: true})
	if err := fetch(c, ok.URL); err != nil {
		t.Fatalf("allowed host failed: %v", err)
	}

	blocked := plugin.NewHTTPClient(plugin.HTTPOptions{AllowHosts: []string{"example.com"}, AllowPrivate: true})
	if err := fetch(blocked, ok.URL); !errors.Is(err, plugin.ErrBlocked) {
		t.Errorf("unlisted host error = %v, want ErrBlocked", err)
	}

	for _, u := range []string{"file:///etc/passwd", "ftp://127.0.0.1/x", "gopher://127.0.0.1/"} {
		if err := fetch(c, u); err == nil {
			t.Errorf("scheme of %s allowed", u)
		}
	}
}

func TestHTTPClientRefusesPrivateAddresses(t *testing.T) {
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("secret")) }))
	defer local.Close()

	// Wildcard allowlist, but the production guard is on (AllowPrivate false).
	c := plugin.NewHTTPClient(plugin.HTTPOptions{AllowHosts: []string{"*"}})
	if err := fetch(c, local.URL); err == nil || !errors.Is(err, plugin.ErrBlocked) {
		t.Errorf("loopback request error = %v, want ErrBlocked", err)
	}
	if err := fetch(c, "http://169.254.169.254/latest/meta-data/"); err == nil {
		t.Error("cloud metadata address reachable")
	}
}

func TestHTTPClientRechecksRedirects(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("target")) }))
	defer target.Close()
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer redirector.Close()

	// Both servers are on 127.0.0.1, so allow that host: the redirect is followed.
	c := plugin.NewHTTPClient(plugin.HTTPOptions{AllowHosts: []string{"127.0.0.1"}, AllowPrivate: true})
	if err := fetch(c, redirector.URL); err != nil {
		t.Fatalf("redirect within allowlist failed: %v", err)
	}

	// A redirect to a host outside the allowlist is refused.
	external := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://evil.example/steal", http.StatusFound)
	}))
	defer external.Close()
	if err := fetch(c, external.URL); !errors.Is(err, plugin.ErrBlocked) {
		t.Errorf("redirect to unlisted host error = %v, want ErrBlocked", err)
	}
}

func TestHTTPClientStopsRedirectLoops(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, srv.URL, http.StatusFound)
	}))
	defer srv.Close()

	c := plugin.NewHTTPClient(plugin.HTTPOptions{AllowHosts: []string{"127.0.0.1"}, AllowPrivate: true})
	if err := fetch(c, srv.URL); err == nil {
		t.Error("redirect loop was followed forever")
	}
}

func TestPrivateAddressClassification(t *testing.T) {
	// Exercised through the exported behavior: these must be refused by the dial guard.
	private := []string{"127.0.0.1", "10.1.2.3", "172.16.0.1", "192.168.1.1", "169.254.169.254", "100.64.0.1", "0.0.0.0", "::1", "fe80::1", "fc00::1", "::ffff:10.0.0.1"}
	public := []string{"8.8.8.8", "1.1.1.1", "93.184.216.34", "2606:4700:4700::1111"}

	for _, s := range private {
		if !plugin.IsPrivateAddr(netip.MustParseAddr(s)) {
			t.Errorf("%s classified public", s)
		}
	}
	for _, s := range public {
		if plugin.IsPrivateAddr(netip.MustParseAddr(s)) {
			t.Errorf("%s classified private", s)
		}
	}
}

// fetch GETs url with c, closes the body and returns the error, if any.
func fetch(c *http.Client, url string) error {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	return resp.Body.Close()
}
