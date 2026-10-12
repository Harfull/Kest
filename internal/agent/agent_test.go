package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Harfull/Kest/internal/clock"
	"github.com/Harfull/Kest/internal/llm"
	"github.com/Harfull/Kest/internal/plugin"
	"github.com/Harfull/Kest/internal/store"
)

// --- test doubles --------------------------------------------------------------

type step func(req llm.ChatRequest) (*llm.ChatResponse, error)

// scripted is a Chatter that plays back canned responses and records requests.
type scripted struct {
	mu    sync.Mutex
	steps []step
	reqs  []llm.ChatRequest
	i     int
}

func (s *scripted) Chat(_ context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reqs = append(s.reqs, req)
	if s.i >= len(s.steps) {
		return nil, errors.New("script exhausted")
	}
	st := s.steps[s.i]
	s.i++
	return st(req)
}

func (s *scripted) requests() []llm.ChatRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]llm.ChatRequest(nil), s.reqs...)
}

func say(text string) step {
	return func(llm.ChatRequest) (*llm.ChatResponse, error) {
		return &llm.ChatResponse{
			Message: llm.Message{Role: llm.RoleAssistant, Content: text},
			Usage:   llm.Usage{CompletionTokens: 7},
		}, nil
	}
}

func fail(err error) step {
	return func(llm.ChatRequest) (*llm.ChatResponse, error) { return nil, err }
}

func tc(id, wire, args string) llm.ToolCall {
	return llm.ToolCall{ID: id, Type: "function", Function: llm.FunctionCall{Name: wire, Arguments: args}}
}

// callTools makes the model request the given tool calls.
func callTools(calls ...llm.ToolCall) step {
	return func(llm.ChatRequest) (*llm.ChatResponse, error) {
		return &llm.ChatResponse{
			Message:      llm.Message{Role: llm.RoleAssistant, ToolCalls: calls},
			FinishReason: "tool_calls",
		}, nil
	}
}

// repeat returns n copies of a step.
func repeat(n int, s step) []step {
	out := make([]step, n)
	for i := range out {
		out[i] = s
	}
	return out
}

// box is a Toolbox over a fixed set of tools.
type box struct{ refs []plugin.ToolRef }

func (b box) Tools() []plugin.ToolRef { return b.refs }
func (b box) Lookup(wire string) (plugin.ToolRef, bool) {
	for _, r := range b.refs {
		if r.Wire() == wire {
			return r, true
		}
	}
	return plugin.ToolRef{}, false
}

type handler func(ctx context.Context, call plugin.Call) (plugin.Result, error)

func mk(pluginID, name string, risk plugin.Risk, h handler) plugin.ToolRef {
	return plugin.ToolRef{Plugin: pluginID, Tool: plugin.Tool{
		Name: name, Description: "test tool " + name, Schema: json.RawMessage(`{"type":"object"}`), Risk: risk, Handler: h,
	}}
}

// counter records how many times a handler ran and with what arguments.
type counter struct {
	mu   sync.Mutex
	args []string
	ctxs []context.Context
}

func (c *counter) n() int { c.mu.Lock(); defer c.mu.Unlock(); return len(c.args) }

func (c *counter) handler(res plugin.Result) handler {
	return func(ctx context.Context, call plugin.Call) (plugin.Result, error) {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.args = append(c.args, string(call.Args))
		c.ctxs = append(c.ctxs, ctx)
		return res, nil
	}
}

// answerer is a Confirmer with a fixed answer that records requests.
type answerer struct {
	mu      sync.Mutex
	approve bool
	err     error
	asked   []ConfirmRequest
}

func (a *answerer) Confirm(_ context.Context, r ConfirmRequest) (bool, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.asked = append(a.asked, r)
	return a.approve, a.err
}

func (a *answerer) count() int { a.mu.Lock(); defer a.mu.Unlock(); return len(a.asked) }

var epoch = time.Date(2026, 10, 11, 14, 3, 0, 0, time.UTC)

func newAgent(t *testing.T, chat Chatter, tb Toolbox, cf Confirmer, mutate func(*Options)) (*Agent, *store.DB) {
	t.Helper()
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "kest.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	o := Options{Chat: chat, Store: db, Tools: tb, Confirm: cf, Clock: clock.NewFake(epoch), Loc: time.UTC, HistoryN: 4}
	if mutate != nil {
		mutate(&o)
	}
	return New(o), db
}

// --- persona and basic turns -----------------------------------------------------

func TestPersonaMatchesDocs(t *testing.T) {
	doc, err := os.ReadFile(filepath.Join("..", "..", "docs", "persona", "prompt-core.md"))
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile("(?s)```text\r?\n(.*?)\r?\n```").FindSubmatch(doc)
	if m == nil {
		t.Fatal("no fenced text block in docs/persona/prompt-core.md")
	}
	want := strings.TrimSpace(strings.ReplaceAll(string(m[1]), "\r\n", "\n"))
	got := strings.TrimSpace(strings.ReplaceAll(persona, "\r\n", "\n"))
	if got != want {
		t.Error("internal/agent/persona.txt is out of sync with docs/persona/prompt-core.md; update both together")
	}
}

func TestReplyBuildsPromptAndStoresTurns(t *testing.T) {
	chat := &scripted{steps: []step{say("  Morning. Ten minutes still counts.  ")}}
	a, db := newAgent(t, chat, nil, nil, nil)
	ctx := context.Background()

	got, err := a.Reply(ctx, "I don't feel like walking")
	if err != nil {
		t.Fatalf("Reply() error = %v", err)
	}
	if got != "Morning. Ten minutes still counts." {
		t.Errorf("reply = %q", got)
	}

	req := chat.requests()[0]
	msgs := req.Messages
	if msgs[0].Role != llm.RoleSystem || !strings.Contains(msgs[0].Content, "You are Kest") {
		t.Errorf("system prompt missing persona: %.60q", msgs[0].Content)
	}
	if !strings.Contains(msgs[0].Content, "Sunday 11 October 2026, 14:03") {
		t.Errorf("system prompt missing local time: %q", msgs[0].Content)
	}
	if last := msgs[len(msgs)-1]; last.Role != llm.RoleUser || last.Content != "I don't feel like walking" {
		t.Errorf("last message = %+v", last)
	}
	if len(req.Tools) == 0 {
		t.Error("memory tools not offered")
	}

	stored, _ := db.RecentMessages(ctx, 10)
	if len(stored) != 2 || stored[0].Role != "user" || stored[1].Role != "assistant" || stored[1].Origin != "kest" {
		t.Fatalf("stored = %+v", stored)
	}
	if stored[1].Tokens != 7 {
		t.Errorf("tokens = %d, want 7", stored[1].Tokens)
	}
}

func TestHistoryIsReplayedAndCapped(t *testing.T) {
	chat := &scripted{steps: []step{say("ok"), say("ok"), say("ok")}}
	a, _ := newAgent(t, chat, nil, nil, nil) // history window of 4 messages
	ctx := context.Background()

	for _, in := range []string{"one", "two", "three"} {
		if _, err := a.Reply(ctx, in); err != nil {
			t.Fatal(err)
		}
	}

	var contents []string
	for _, m := range chat.requests()[2].Messages[1:] { // third call: skip system
		contents = append(contents, m.Content)
	}
	if want := "one,ok,two,ok,three"; strings.Join(contents, ",") != want {
		t.Errorf("history in prompt = %v, want %s", contents, want)
	}
}

func TestModelFailureKeepsUserMessage(t *testing.T) {
	a, db := newAgent(t, &scripted{steps: []step{fail(errors.New("down"))}}, nil, nil, nil)

	if _, err := a.Reply(context.Background(), "hello"); err == nil {
		t.Fatal("Reply() succeeded despite model failure")
	}
	stored, _ := db.RecentMessages(context.Background(), 10)
	if len(stored) != 1 || stored[0].Content != "hello" {
		t.Errorf("stored = %+v, want the user's message only", stored)
	}
}

func TestEmptyInputsAreRejected(t *testing.T) {
	a, _ := newAgent(t, &scripted{steps: []step{say("  ")}}, nil, nil, nil)

	if _, err := a.Reply(context.Background(), "   "); err == nil {
		t.Error("blank message accepted")
	}
	if _, err := a.Reply(context.Background(), "hi"); err == nil {
		t.Error("blank model reply accepted")
	}
}

// --- the tool loop ---------------------------------------------------------------

func TestToolLoopRoundTrip(t *testing.T) {
	var c counter
	tb := box{[]plugin.ToolRef{mk("clock", "time_now", plugin.RiskRead, c.handler(plugin.Result{Content: "14:03", Summary: "read the clock"}))}}
	chat := &scripted{steps: []step{
		callTools(tc("call_1", "clock__time_now", `{"zone":"UTC"}`)),
		say("It's 14:03."),
	}}
	a, db := newAgent(t, chat, tb, nil, nil)

	got, err := a.Reply(context.Background(), "what time is it?")
	if err != nil || got != "It's 14:03." {
		t.Fatalf("Reply() = %q, %v", got, err)
	}
	if c.n() != 1 || c.args[0] != `{"zone":"UTC"}` {
		t.Errorf("handler calls = %v", c.args)
	}

	reqs := chat.requests()
	if len(reqs) != 2 {
		t.Fatalf("model calls = %d, want 2", len(reqs))
	}
	// Tools are re-sent on every request, with parallel calls disabled.
	for i, r := range reqs {
		if len(r.Tools) == 0 || r.ParallelToolCalls == nil || *r.ParallelToolCalls {
			t.Errorf("request %d: tools/parallel flag wrong: %d tools, parallel=%v", i, len(r.Tools), r.ParallelToolCalls)
		}
	}
	// Second request: assistant tool request, then the matching tool result.
	m := reqs[1].Messages
	asst, tool := m[len(m)-2], m[len(m)-1]
	if asst.Role != llm.RoleAssistant || len(asst.ToolCalls) != 1 {
		t.Errorf("assistant message = %+v", asst)
	}
	if tool.Role != llm.RoleTool || tool.ToolCallID != "call_1" || tool.Content != "14:03" {
		t.Errorf("tool message = %+v", tool)
	}

	// The audit log records the call without its content; history keeps only turns.
	var n int
	_ = db.Read().QueryRow("SELECT COUNT(*) FROM audit_log WHERE action = 'clock.time_now' AND status = 'ok'").Scan(&n)
	if n != 1 {
		t.Errorf("audit rows = %d, want 1", n)
	}
	stored, _ := db.RecentMessages(context.Background(), 10)
	if len(stored) != 2 {
		t.Errorf("stored %d messages, want only the user turn and final reply", len(stored))
	}
}

func TestToolErrorsAreReportedToTheModel(t *testing.T) {
	boom := func(context.Context, plugin.Call) (plugin.Result, error) {
		return plugin.Result{}, errors.New(strings.Repeat("x", 1000))
	}
	pan := func(context.Context, plugin.Call) (plugin.Result, error) { panic("kaboom") }
	tb := box{[]plugin.ToolRef{mk("p", "boom", plugin.RiskRead, boom), mk("p", "pan", plugin.RiskRead, pan)}}

	tests := []struct {
		name string
		call llm.ToolCall
		want string
	}{
		{"handler error, truncated", tc("1", "p__boom", `{}`), "Error: xxx"},
		{"handler panic contained", tc("2", "p__pan", `{}`), "panicked"},
		{"unknown tool", tc("3", "nope__tool", `{}`), "no tool called"},
		{"arguments not an object", tc("4", "p__boom", `[1,2]`), "JSON object"},
		{"arguments not json", tc("5", "p__boom", `{oops`), "JSON object"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			chat := &scripted{steps: []step{callTools(tt.call), say("done")}}
			a, _ := newAgent(t, chat, tb, nil, nil)

			if got, err := a.Reply(context.Background(), "go"); err != nil || got != "done" {
				t.Fatalf("Reply() = %q, %v; the turn must survive a tool failure", got, err)
			}
			m := chat.requests()[1].Messages
			content := m[len(m)-1].Content
			if !strings.Contains(content, tt.want) {
				t.Errorf("tool result = %.80q, want it to mention %q", content, tt.want)
			}
			if len(content) > 400 {
				t.Errorf("tool result is %d chars; errors must be truncated", len(content))
			}
		})
	}
}

func TestEmptyArgumentsDefaultToObject(t *testing.T) {
	var c counter
	tb := box{[]plugin.ToolRef{mk("p", "ping", plugin.RiskRead, c.handler(plugin.Result{Content: "pong"}))}}
	chat := &scripted{steps: []step{callTools(tc("1", "p__ping", "")), say("ok")}}
	a, _ := newAgent(t, chat, tb, nil, nil)

	if _, err := a.Reply(context.Background(), "ping"); err != nil {
		t.Fatal(err)
	}
	if c.n() != 1 || c.args[0] != `{}` {
		t.Errorf("args = %v, want {}", c.args)
	}
}

func TestIterationCapEndsTheTurn(t *testing.T) {
	var c counter
	tb := box{[]plugin.ToolRef{mk("p", "ping", plugin.RiskRead, c.handler(plugin.Result{Content: "pong"}))}}
	steps := repeat(5, callTools(tc("x", "p__ping", `{}`)))
	steps = append(steps, say("Here is what I have."))
	chat := &scripted{steps: steps}
	a, _ := newAgent(t, chat, tb, nil, func(o *Options) { o.MaxIterations = 5 })

	got, err := a.Reply(context.Background(), "loop forever")
	if err != nil || got != "Here is what I have." {
		t.Fatalf("Reply() = %q, %v", got, err)
	}
	if c.n() != 5 {
		t.Errorf("tool ran %d times, want exactly the cap (5)", c.n())
	}
	reqs := chat.requests()
	last := reqs[len(reqs)-1]
	if last.ToolChoice != "none" {
		t.Errorf("final request ToolChoice = %q, want none", last.ToolChoice)
	}
}

func TestToolCallCapPerTurn(t *testing.T) {
	var c counter
	tb := box{[]plugin.ToolRef{mk("p", "ping", plugin.RiskRead, c.handler(plugin.Result{Content: "pong"}))}}
	var calls []llm.ToolCall
	for i := range 15 {
		calls = append(calls, tc(string(rune('a'+i)), "p__ping", `{}`))
	}
	chat := &scripted{steps: []step{callTools(calls...), say("done")}}
	a, _ := newAgent(t, chat, tb, nil, func(o *Options) { o.MaxToolCalls = 12 })

	if _, err := a.Reply(context.Background(), "spam"); err != nil {
		t.Fatal(err)
	}
	if c.n() != 12 {
		t.Errorf("tool ran %d times, want 12", c.n())
	}
	m := chat.requests()[1].Messages
	if !strings.Contains(m[len(m)-1].Content, "too many tool calls") {
		t.Errorf("surplus call result = %q", m[len(m)-1].Content)
	}
	// Every call, including the surplus, gets a matching result.
	results := 0
	for _, msg := range m {
		if msg.Role == llm.RoleTool {
			results++
		}
	}
	if results != 15 {
		t.Errorf("tool results = %d, want 15", results)
	}
}

func TestSystemPromptListsNoRawToolOutput(t *testing.T) {
	a, _ := newAgent(t, &scripted{}, nil, nil, nil)
	p := a.SystemPrompt(context.Background())
	for _, want := range []string{"Local time:", "absolute local time", "do not try it again"} {
		if !strings.Contains(p, want) {
			t.Errorf("system prompt missing %q", want)
		}
	}
}
