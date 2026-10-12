package gmail

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Harfull/Kest/internal/plugin"
	"github.com/Harfull/Kest/internal/plugin/plugintest"
	"github.com/Harfull/Kest/plugins/internal/mailkit"
)

const danaRaw = "From: Dana <dana@example.com>\r\nTo: owner@example.com\r\nSubject: Cafe Friday?\r\nDate: Sun, 11 Oct 2026 14:03:00 +0000\r\nMessage-ID: <m1@example.com>\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nAre you free Friday?\r\n"

// google is a stand-in for the OAuth and Gmail endpoints.
type google struct {
	mu          sync.Mutex
	tokens      int
	badGrant    bool
	revokeNext  bool // answer the next API call with 401 once
	queries     []string
	sent        []map[string]any
	drafts      []map[string]any
	modified    []string
	scopeDenied bool
	lastAuth    string
	rateLimited bool
}

func (g *google) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	writeJSON := func(status int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(v)
	}
	if r.URL.Path == "/token" {
		_ = r.ParseForm()
		if g.badGrant {
			writeJSON(400, map[string]string{"error": "invalid_grant"})
			return
		}
		if r.Form.Get("client_id") != "cid" || r.Form.Get("client_secret") != "csecret" || r.Form.Get("refresh_token") != "rtoken" || r.Form.Get("grant_type") != "refresh_token" {
			writeJSON(401, map[string]string{"error": "invalid_client"})
			return
		}
		g.tokens++
		writeJSON(200, map[string]any{"access_token": fmt.Sprintf("access-%d", g.tokens), "expires_in": 3600, "token_type": "Bearer"})
		return
	}

	g.lastAuth = r.Header.Get("Authorization")
	if !strings.HasPrefix(g.lastAuth, "Bearer access-") || g.revokeNext {
		g.revokeNext = false
		writeJSON(401, map[string]any{"error": map[string]any{"message": "Invalid Credentials"}})
		return
	}
	if g.scopeDenied {
		writeJSON(403, map[string]any{"error": map[string]any{"message": "Request had insufficient authentication scopes.", "errors": []map[string]string{{"reason": "insufficientPermissions"}}}})
		return
	}
	if g.rateLimited {
		writeJSON(429, map[string]any{"error": map[string]any{"message": "slow down"}})
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/gmail/v1/users/me")
	switch {
	case path == "/profile":
		writeJSON(200, map[string]string{"emailAddress": "owner@example.com"})
	case path == "/messages" && r.Method == http.MethodGet:
		g.queries = append(g.queries, r.URL.Query().Get("q"))
		writeJSON(200, map[string]any{"messages": []map[string]string{{"id": "m2", "threadId": "t2"}, {"id": "m1", "threadId": "t1"}}})
	case path == "/messages/m1" && r.URL.Query().Get("format") == "metadata":
		writeJSON(200, map[string]any{"id": "m1", "labelIds": []string{"INBOX"}, "snippet": "Are you free", "internalDate": "1791727380000",
			"payload": map[string]any{"headers": []map[string]string{{"name": "From", "value": "Dana <dana@example.com>"}, {"name": "Subject", "value": "Cafe Friday?"}, {"name": "Date", "value": "Sun, 11 Oct 2026 14:03:00 +0000"}}}})
	case path == "/messages/m2" && r.URL.Query().Get("format") == "metadata":
		writeJSON(200, map[string]any{"id": "m2", "labelIds": []string{"INBOX", "UNREAD"}, "snippet": "Please review",
			"payload": map[string]any{"headers": []map[string]string{{"name": "From", "value": "Boss <boss@work.example>"}, {"name": "Subject", "value": "=?UTF-8?Q?Caf=C3=A9_budget?="}, {"name": "Date", "value": "Mon, 12 Oct 2026 07:00:00 +0000"}}}})
	case path == "/messages/m1" && r.URL.Query().Get("format") == "raw":
		writeJSON(200, map[string]any{"id": "m1", "threadId": "t1", "labelIds": []string{"INBOX"}, "raw": base64.RawURLEncoding.EncodeToString([]byte(danaRaw))})
	case path == "/messages/missing":
		writeJSON(404, map[string]any{"error": map[string]any{"message": "Requested entity was not found."}})
	case path == "/messages/send" && r.Method == http.MethodPost:
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		g.sent = append(g.sent, body)
		writeJSON(200, map[string]string{"id": "sent1"})
	case path == "/drafts" && r.Method == http.MethodPost:
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		g.drafts = append(g.drafts, body)
		writeJSON(200, map[string]string{"id": "d1"})
	case strings.HasSuffix(path, "/modify") && r.Method == http.MethodPost:
		g.modified = append(g.modified, path)
		writeJSON(200, map[string]string{"id": "m2"})
	default:
		writeJSON(404, map[string]any{"error": map[string]any{"message": "unknown " + path}})
	}
}

// reroute sends every request to the fake, whatever host it names.
type reroute struct{ target *url.URL }

func (r reroute) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.URL.Scheme, clone.URL.Host = r.target.Scheme, r.target.Host
	if clone.URL.Path == "/token" || strings.HasPrefix(req.URL.Host, "oauth2.") {
		clone.URL.Path = "/token"
	}
	return http.DefaultTransport.RoundTrip(clone)
}

func newBackend(t *testing.T, g *google) (*backend, *plugintest.Host) {
	t.Helper()
	srv := httptest.NewServer(g)
	t.Cleanup(srv.Close)
	target, _ := url.Parse(srv.URL)
	h := plugintest.NewHost()
	h.Client = &http.Client{Transport: reroute{target}, Timeout: 5 * time.Second}
	b := &backend{http: h.Client, now: h.Clock().Now, maxChars: 6000, creds: func() (credentials, error) {
		return credentials{"cid", "csecret", "rtoken"}, nil
	}}
	return b, h
}

func TestSearchBuildsAQueryAndListsSummaries(t *testing.T) {
	g := &google{}
	b, _ := newBackend(t, g)
	got, err := b.Search(context.Background(), mailkit.Query{Text: `budget "x`, From: "boss", UnreadOnly: true, SinceDays: 3, Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != "m2" || got[1].ID != "m1" {
		t.Fatalf("ids = %+v", got)
	}
	if !got[0].Unread || got[1].Unread || got[0].Subject != "Café budget" || got[0].From != "Boss <boss@work.example>" || got[1].Snippet != "Are you free" {
		t.Errorf("summaries = %+v", got)
	}
	if want := `"budget x" from:"boss" is:unread after:2026/10/08`; g.queries[0] != want {
		t.Errorf("query = %q, want %q", g.queries[0], want)
	}
	if g.tokens != 1 {
		t.Errorf("%d token requests for one search; the token should be reused", g.tokens)
	}
}

func TestQueryValuesCannotInjectOperators(t *testing.T) {
	q := gmailQuery(mailkit.Query{From: `x" OR in:anywhere "`, Subject: `a\" label:secret`}, time.Now())
	if strings.Count(q, `"`) != 4 || strings.Contains(q, `\`) {
		t.Fatalf("query = %q", q)
	}
}

func TestReadDecodesTheRawMessage(t *testing.T) {
	b, _ := newBackend(t, &google{})
	m, err := b.Read(context.Background(), "m1")
	if err != nil {
		t.Fatal(err)
	}
	if m.Subject != "Cafe Friday?" || !strings.Contains(m.Text, "Are you free Friday?") || m.ThreadID != "t1" || m.MessageID != "<m1@example.com>" || m.Unread {
		t.Errorf("message = %+v", m)
	}
	if _, err := b.Read(context.Background(), "missing"); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("missing message: %v", err)
	}
}

func TestSendDraftAndMarkRead(t *testing.T) {
	g := &google{}
	b, _ := newBackend(t, g)
	ctx := context.Background()
	raw := []byte("From: owner@example.com\r\nTo: dana@example.com\r\nSubject: x\r\n\r\nhi\r\n")
	reply := &mailkit.Message{ThreadID: "t1"}
	if err := b.Send(ctx, raw, mailkit.Outgoing{Reply: reply}); err != nil {
		t.Fatal(err)
	}
	if err := b.Draft(ctx, raw, mailkit.Outgoing{}); err != nil {
		t.Fatal(err)
	}
	if err := b.MarkRead(ctx, "m2"); err != nil {
		t.Fatal(err)
	}
	if len(g.sent) != 1 || g.sent[0]["threadId"] != "t1" {
		t.Fatalf("sent = %+v", g.sent)
	}
	decoded, _ := base64.RawURLEncoding.DecodeString(g.sent[0]["raw"].(string))
	if string(decoded) != string(raw) {
		t.Error("the message was altered on the way")
	}
	msg := g.drafts[0]["message"].(map[string]any)
	if _, has := msg["threadId"]; has {
		t.Error("a draft with no reply carried a thread id")
	}
	if len(g.modified) != 1 || g.modified[0] != "/messages/m2/modify" {
		t.Errorf("modified = %v", g.modified)
	}
}

func TestExpiredAccessTokenIsRefreshedOnce(t *testing.T) {
	g := &google{}
	b, _ := newBackend(t, g)
	ctx := context.Background()
	if _, err := b.Read(ctx, "m1"); err != nil {
		t.Fatal(err)
	}
	g.mu.Lock()
	g.revokeNext = true
	g.mu.Unlock()
	if _, err := b.Read(ctx, "m1"); err != nil {
		t.Fatalf("a revoked access token was not replaced: %v", err)
	}
	if g.tokens != 2 || g.lastAuth != "Bearer access-2" {
		t.Errorf("tokens %d, last auth %q", g.tokens, g.lastAuth)
	}
}

func TestTokenIsRefreshedWhenItsLifetimeEnds(t *testing.T) {
	g := &google{}
	b, h := newBackend(t, g)
	for range 2 {
		if _, err := b.Read(context.Background(), "m1"); err != nil {
			t.Fatal(err)
		}
	}
	if g.tokens != 1 {
		t.Fatalf("%d refreshes inside the lifetime", g.tokens)
	}
	h.Clk.Advance(2 * time.Hour)
	if _, err := b.Read(context.Background(), "m1"); err != nil || g.tokens != 2 {
		t.Fatalf("after expiry: %v, %d tokens", err, g.tokens)
	}
}

func TestFailuresAreExplained(t *testing.T) {
	for name, tc := range map[string]struct {
		g    *google
		want string
	}{
		"revoked refresh token": {&google{badGrant: true}, "docs/guides/gmail-setup.md"},
		"missing scope":         {&google{scopeDenied: true}, "does not include the permission"},
		"rate limit":            {&google{rateLimited: true}, "rate limited"},
	} {
		t.Run(name, func(t *testing.T) {
			b, _ := newBackend(t, tc.g)
			_, err := b.Search(context.Background(), mailkit.Query{})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v", err)
			}
		})
	}
	g := &google{}
	b, _ := newBackend(t, g)
	b.creds = func() (credentials, error) { return credentials{"cid", "wrong", "rtoken"}, nil }
	if _, err := b.Read(context.Background(), "m1"); err == nil || !strings.Contains(err.Error(), "client id or secret") {
		t.Errorf("bad client: %v", err)
	}
}

func TestSecretsNeverAppearInErrors(t *testing.T) {
	g := &google{badGrant: true}
	b, _ := newBackend(t, g)
	_, err := b.Read(context.Background(), "m1")
	for _, secret := range []string{"cid", "csecret", "rtoken"} {
		if err != nil && strings.Contains(err.Error(), secret) {
			t.Errorf("error leaks %q: %v", secret, err)
		}
	}
}

func TestOwnAddressIsLearnedOnce(t *testing.T) {
	g := &google{}
	b, _ := newBackend(t, g)
	for range 2 {
		if a, err := b.ownAddress(context.Background()); err != nil || a != "owner@example.com" {
			t.Fatalf("address %q, %v", a, err)
		}
	}
}

func TestToolsSendFromTheAccountAddress(t *testing.T) {
	g := &google{}
	b, h := newBackend(t, g)
	var send plugin.Tool
	for _, tool := range mailkit.Tools(mailkit.Options{Host: h, Backend: b, FromFunc: b.ownAddress, MaxChars: 6000}) {
		if tool.Name == mailkit.ToolSend {
			send = tool
		}
	}
	args, _ := json.Marshal(map[string]any{"to": []string{"dana@example.com"}, "subject": "Friday", "body": "10 works.", "reply_to_id": "m1"})
	if _, err := send.Handler(context.Background(), plugin.Call{Args: args}); err != nil {
		t.Fatal(err)
	}
	raw, _ := base64.RawURLEncoding.DecodeString(g.sent[0]["raw"].(string))
	if !strings.Contains(string(raw), "From: <owner@example.com>") && !strings.Contains(string(raw), "From: owner@example.com") {
		t.Errorf("From header = %s", raw)
	}
	if g.sent[0]["threadId"] != "t1" || !strings.Contains(string(raw), "In-Reply-To: <m1@example.com>") {
		t.Errorf("reply was not threaded: %v", g.sent[0])
	}
}

// --- configuration ---------------------------------------------------------------------

func TestInitAndSecrets(t *testing.T) {
	h := plugintest.NewHost()
	cfg, err := manifest.ResolveConfig(map[string]any{"nudges": true, "nudge_from": "boss@work.example"})
	if err != nil {
		t.Fatal(err)
	}
	h.Cfg = cfg
	p := New().(*Plugin)
	if err := p.Init(context.Background(), h); err != nil {
		t.Fatal(err)
	}
	if jobs, _ := h.Jobs().List(context.Background()); len(jobs) != 1 {
		t.Fatalf("jobs = %+v", jobs)
	}
	if _, err := p.backend.creds(); err == nil || !strings.Contains(err.Error(), "KEST_SECRET_GMAIL_CLIENT_ID") && !strings.Contains(err.Error(), "KEST_SECRET_GMAIL") {
		t.Fatalf("missing secrets error = %v", err)
	}
	h.Secrets = map[string]string{"GMAIL_CLIENT_ID": "a", "GMAIL_CLIENT_SECRET": "b", "GMAIL_REFRESH_TOKEN": "c"}
	if c, err := p.backend.creds(); err != nil || c.refreshToken != "c" {
		t.Fatalf("creds = %+v, %v", c, err)
	}
}

func TestInitRefusesBadSettings(t *testing.T) {
	for name, raw := range map[string]map[string]any{
		"nudges without rules": {"nudges": true},
		"bad from address":     {"from_address": "not an address"},
		"nudge too often":      {"nudges": true, "nudge_from": "a@b.c", "nudge_minutes": 1},
		"tiny max_chars":       {"max_chars": 5},
	} {
		cfg, err := manifest.ResolveConfig(raw)
		if err != nil {
			t.Fatal(err)
		}
		h := plugintest.NewHost()
		h.Cfg = cfg
		if err := New().Init(context.Background(), h); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestContract(t *testing.T) { plugintest.RunContract(t, New, map[string]any{}) }

func TestNetworkIsLimitedToGoogle(t *testing.T) {
	for _, host := range manifest.Capabilities.Network {
		if !strings.HasSuffix(host, ".googleapis.com") {
			t.Errorf("unexpected host %q", host)
		}
	}
	if len(manifest.Capabilities.Network) != 2 {
		t.Errorf("network = %v", manifest.Capabilities.Network)
	}
}
