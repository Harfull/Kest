// Package agent turns the owner's messages into Kest's replies.
//
// A turn builds a prompt (persona, local time, memory, recent history), calls
// the model, and runs any tools it asks for under the tool policy: risky calls
// wait for the owner's approval, and once untrusted content has entered a turn
// every further state change needs approval too. See
// docs/architecture/security.md.
package agent

import (
	"context"
	_ "embed" // persona.txt
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Harfull/Kest/internal/audit"
	"github.com/Harfull/Kest/internal/clock"
	"github.com/Harfull/Kest/internal/llm"
	"github.com/Harfull/Kest/internal/plugin"
	"github.com/Harfull/Kest/internal/store"
)

// persona is the system prompt core. It must match the fenced block in
// docs/persona/prompt-core.md; a test enforces that.
//
//go:embed persona.txt
var persona string

// Defaults for Options.
const (
	DefaultHistory       = 20
	DefaultMaxIterations = 8
	DefaultMaxToolCalls  = 12
	toolTimeout          = 60 * time.Second
)

// Origins stored on assistant messages. Replies from turns that read untrusted
// content are tagged so summarization can leave them out.
const (
	originReply        = "kest"
	originReplyTainted = "kest:tainted"
)

// Chatter is the model call the agent needs. *llm.Client satisfies it.
type Chatter interface {
	Chat(ctx context.Context, req llm.ChatRequest) (*llm.ChatResponse, error)
}

// Store is the persistence the agent needs. *store.DB satisfies it.
type Store interface {
	AddMessage(ctx context.Context, m store.Message) (int64, error)
	RecentMessages(ctx context.Context, n int) ([]store.Message, error)
	MessagesAfter(ctx context.Context, afterID int64, limit int) ([]store.Message, error)
	LatestMessageID(ctx context.Context) (int64, error)
	AddMemory(ctx context.Context, m store.Memory) (int64, error)
	RecentMemories(ctx context.Context, kind string, n int) ([]store.Memory, error)
	SearchMemories(ctx context.Context, query string, n int) ([]store.Memory, error)
	DeleteMemory(ctx context.Context, id int64) error
	GetProfile(ctx context.Context, key string) (string, error)
	SetProfile(ctx context.Context, key, value string, now time.Time) error
	audit.Store
}

// Toolbox supplies the tools the model may call. *plugin.Registry satisfies it.
type Toolbox interface {
	Tools() []plugin.ToolRef
	Lookup(wire string) (plugin.ToolRef, bool)
}

// Options configures an Agent.
type Options struct {
	Chat  Chatter
	Store Store
	Tools Toolbox          // optional; the built-in memory tools are always added
	Extra []plugin.ToolRef // further built-in tools, such as the coach's; they win name clashes like the memory tools
	// Context adds facts to the system prompt each turn. It returns trusted text only.
	Context func(ctx context.Context) string
	Confirm Confirmer // required for any tool that can need approval
	Clock   clock.Clock
	Loc     *time.Location
	Log     *slog.Logger

	HistoryN      int // recent messages sent with each call
	MaxIterations int // model calls per turn
	MaxToolCalls  int // tool calls per turn
}

// Agent answers messages. Feed it one turn at a time so history stays ordered.
type Agent struct {
	chat    Chatter
	db      Store
	tools   Toolbox
	confirm Confirmer
	clock   clock.Clock
	loc     *time.Location
	log     *slog.Logger

	extraContext func(ctx context.Context) string
	locked       atomic.Bool
	audit        *audit.Log

	historyN, maxIter, maxCalls int
}

// New returns an Agent.
func New(o Options) *Agent {
	if o.HistoryN <= 0 {
		o.HistoryN = DefaultHistory
	}
	if o.MaxIterations <= 0 {
		o.MaxIterations = DefaultMaxIterations
	}
	if o.MaxToolCalls <= 0 {
		o.MaxToolCalls = DefaultMaxToolCalls
	}
	if o.Clock == nil {
		o.Clock = clock.Real{}
	}
	if o.Loc == nil {
		o.Loc = time.UTC
	}
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}

	a := &Agent{
		chat: o.Chat, db: o.Store, confirm: o.Confirm, clock: o.Clock, loc: o.Loc, log: o.Log,
		historyN: o.HistoryN, maxIter: o.MaxIterations, maxCalls: o.MaxToolCalls,
		extraContext: o.Context,
		audit:        audit.New(o.Store, o.Clock, o.Log),
	}
	core := append(memoryTools{db: o.Store, clock: o.Clock}.refs(), o.Extra...)
	a.tools = merge(o.Tools, core)
	return a
}

// SystemPrompt returns the persona plus facts about the present moment and
// what Kest knows about the owner.
func (a *Agent) SystemPrompt(ctx context.Context) string {
	now := a.clock.Now().In(a.loc)

	var b strings.Builder
	b.WriteString(strings.TrimSpace(persona))
	b.WriteString("\n\nContext\n- Local time: " + now.Format("Monday 2 January 2006, 15:04") + " (" + a.loc.String() + ")\n")
	b.WriteString("- When a tool needs a time, give an absolute local time such as 2026-10-12T09:00, worked out from the local time above.\n")
	b.WriteString("- Use tools only for things the owner asked for. If the owner declines an action, do not try it again.\n")
	b.WriteString(a.memoryContext(ctx))
	if a.extraContext != nil {
		b.WriteString(a.extraContext(ctx))
	}
	return b.String()
}

// SetLockdown turns lockdown on or off. While it is on, only read tools run:
// anything that writes, deletes or sends is refused, whatever the owner is asked.
func (a *Agent) SetLockdown(on bool) { a.locked.Store(on) }

// Locked reports whether lockdown is on.
func (a *Agent) Locked() bool { return a.locked.Load() }

// Reply records the owner's message, runs the turn and returns Kest's answer.
// If the model call fails the owner's message stays in history, so the next
// turn still has it.
func (a *Agent) Reply(ctx context.Context, userText string) (string, error) {
	userText = strings.TrimSpace(userText)
	if userText == "" {
		return "", errors.New("agent: empty message")
	}

	prior, err := a.db.RecentMessages(ctx, a.historyN)
	if err != nil {
		return "", fmt.Errorf("load history: %w", err)
	}
	if _, err := a.db.AddMessage(ctx, store.Message{
		TS: a.clock.Now(), Role: llm.RoleUser, Content: userText, Origin: "user",
	}); err != nil {
		return "", fmt.Errorf("store message: %w", err)
	}

	msgs := make([]llm.Message, 0, len(prior)+2)
	msgs = append(msgs, llm.Message{Role: llm.RoleSystem, Content: a.SystemPrompt(ctx)})
	for _, m := range prior {
		// Only conversation turns are replayed; tool and system rows are not.
		if m.Role == llm.RoleUser || m.Role == llm.RoleAssistant {
			msgs = append(msgs, llm.Message{Role: m.Role, Content: m.Content})
		}
	}
	msgs = append(msgs, llm.Message{Role: llm.RoleUser, Content: userText})

	reply, wasTainted, tokens, err := a.loop(ctx, msgs, userText)
	if err != nil {
		return "", err
	}

	origin := originReply
	if wasTainted {
		origin = originReplyTainted
	}
	if _, err := a.db.AddMessage(ctx, store.Message{
		TS: a.clock.Now(), Role: llm.RoleAssistant, Content: reply, Origin: origin, Tokens: tokens,
	}); err != nil {
		return reply, fmt.Errorf("store reply: %w", err)
	}
	return reply, nil
}

// loop runs the model/tool cycle and returns the final text, whether the turn
// read untrusted content, and the tokens of the final completion.
func (a *Agent) loop(ctx context.Context, msgs []llm.Message, userText string) (reply string, wasTainted bool, tokens int, err error) {
	defs := a.toolDefs(routeTools(a.tools.Tools(), userText))
	noParallel := false // run tools one at a time so approvals are unambiguous
	calls := 0

	for iter := 0; iter < a.maxIter; iter++ {
		req := llm.ChatRequest{Messages: msgs}
		if len(defs) > 0 {
			req.Tools, req.ParallelToolCalls = defs, &noParallel
		}
		resp, err := a.chat.Chat(ctx, req)
		if err != nil {
			return "", wasTainted, 0, fmt.Errorf("model call: %w", err)
		}
		msg := resp.Message

		if len(msg.ToolCalls) == 0 {
			text := strings.TrimSpace(msg.Content)
			if text == "" {
				return "", wasTainted, 0, errors.New("agent: the model returned an empty reply")
			}
			return text, wasTainted, resp.Usage.CompletionTokens, nil
		}

		msgs = append(msgs, msg) // the assistant's tool request must precede the results
		for _, tc := range msg.ToolCalls {
			calls++
			var content string
			if calls > a.maxCalls {
				content = "Error: too many tool calls in this turn. Answer the owner with what you have."
			} else {
				var untrusted bool
				content, untrusted = a.runTool(withTaintIf(ctx, wasTainted), tc)
				if untrusted {
					wasTainted = true
				}
			}
			msgs = append(msgs, llm.Message{Role: llm.RoleTool, ToolCallID: tc.ID, Content: content})
		}
	}

	// Out of iterations: ask for a plain answer with no further tools.
	msgs = append(msgs, llm.Message{
		Role:    llm.RoleSystem,
		Content: "The tool limit for this turn has been reached. Answer the owner now, plainly, with what you have.",
	})
	resp, err := a.chat.Chat(ctx, llm.ChatRequest{Messages: msgs, ToolChoice: "none"})
	if err != nil {
		return "", wasTainted, 0, fmt.Errorf("model call: %w", err)
	}
	text := strings.TrimSpace(resp.Message.Content)
	if text == "" {
		return "", wasTainted, 0, errors.New("agent: the model returned an empty reply")
	}
	return text, wasTainted, resp.Usage.CompletionTokens, nil
}

func withTaintIf(ctx context.Context, t bool) context.Context {
	if t {
		return withTaint(ctx)
	}
	return ctx
}

// toolDefs converts tools to model tool definitions.
func (a *Agent) toolDefs(refs []plugin.ToolRef) []llm.Tool {
	defs := make([]llm.Tool, len(refs))
	for i, r := range refs {
		defs[i] = llm.Tool{
			Name:        r.Wire(),
			Description: r.Tool.Description,
			Parameters:  r.Tool.Schema,
		}
	}
	return defs
}

// runTool applies the policy, runs one tool call and returns the text for the
// model plus whether it carries untrusted content.
func (a *Agent) runTool(ctx context.Context, tc llm.ToolCall) (content string, untrusted bool) {
	ref, ok := a.tools.Lookup(tc.Function.Name)
	if !ok {
		return "Error: there is no tool called " + sanitizeLabel(tc.Function.Name) + ".", false
	}
	full := ref.Full()

	args := json.RawMessage(strings.TrimSpace(tc.Function.Arguments))
	if len(args) == 0 {
		args = json.RawMessage(`{}`)
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(args, &obj); err != nil {
		return "Error: the arguments must be a JSON object.", false
	}

	risk, decision, reason := Decide(ref.Tool.Risk, ref.Tool.Outbound, tainted(ctx))
	if a.locked.Load() && risk != plugin.RiskRead {
		a.audit.Record(ctx, audit.Entry{Action: full, Risk: risk.String(), Status: audit.StatusDeclined, Summary: "lockdown is on"})
		return "Error: lockdown is on, so only reading is allowed. Tell the owner this was not done and that it will work again after a restart.", false
	}

	entry := audit.Entry{Action: full, Risk: risk.String()}
	finish := func(status, summary string) {
		entry.Status, entry.Summary = status, summary
		a.audit.Record(ctx, entry)
	}

	if decision == Confirm {
		describe := DescribeArgs
		if ref.Tool.Describe != nil {
			describe = ref.Tool.Describe
		}
		approved, err := a.askOwner(ctx, ConfirmRequest{
			Tool: full, Risk: risk, Reason: reason, Args: args, Summary: describe(args),
		})
		if err != nil {
			a.log.WarnContext(ctx, "confirmation failed", "tool", full, "err", err)
			finish(audit.StatusError, "confirmation failed")
			return "Error: I couldn't ask the owner to confirm this, so it was not done.", false
		}
		if !approved {
			finish(audit.StatusDeclined, "owner declined or did not answer")
			return "The owner did not approve this action, so it was not done. Do not try it again; tell the owner it was skipped.", false
		}
		entry.Confirmed = true
	}

	tctx, cancel := context.WithTimeout(ctx, toolTimeout)
	defer cancel()
	res, err := a.safeHandler(tctx, ref, plugin.Call{ID: tc.ID, Args: args})
	if err != nil {
		finish(audit.StatusError, "tool failed")
		a.log.WarnContext(ctx, "tool failed", "tool", full, "err", err)
		return "Error: " + Sanitize(err.Error(), 300), false
	}

	finish(audit.StatusOK, res.Summary)
	if res.Untrusted {
		return Wrap(full, res.Content), true
	}
	return Sanitize(res.Content, maxToolResultChars), false
}

func (a *Agent) askOwner(ctx context.Context, req ConfirmRequest) (bool, error) {
	if a.confirm == nil {
		return false, errors.New("no confirmer configured")
	}
	return a.confirm.Confirm(ctx, req)
}

// safeHandler runs a tool handler and turns a panic into an error.
func (a *Agent) safeHandler(ctx context.Context, ref plugin.ToolRef, call plugin.Call) (res plugin.Result, err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("tool panicked: %v", p)
		}
	}()
	return ref.Tool.Handler(ctx, call)
}

// --- toolbox composition ---------------------------------------------------------

type merged struct {
	byWire map[string]plugin.ToolRef
	order  []plugin.ToolRef
}

// merge combines an optional toolbox with fixed tool refs. Core tools win on a
// name clash, so a plugin cannot shadow them.
func merge(tb Toolbox, core []plugin.ToolRef) Toolbox {
	m := &merged{byWire: map[string]plugin.ToolRef{}}
	if tb != nil {
		for _, r := range tb.Tools() {
			m.byWire[r.Wire()] = r
		}
	}
	for _, r := range core {
		m.byWire[r.Wire()] = r
	}
	for _, r := range m.byWire {
		m.order = append(m.order, r)
	}
	sortRefs(m.order)
	return m
}

func (m *merged) Tools() []plugin.ToolRef { return m.order }
func (m *merged) Lookup(wire string) (plugin.ToolRef, bool) {
	r, ok := m.byWire[wire]
	return r, ok
}

func sortRefs(refs []plugin.ToolRef) {
	sort.Slice(refs, func(i, j int) bool { return refs[i].Wire() < refs[j].Wire() })
}
