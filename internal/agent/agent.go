// Package agent turns the owner's messages into Kest's replies.
//
// Phase 1 is a plain conversation: persona prompt, recent history and one model
// call. Tool calling, confirmations and memory arrive in Phase 2 (see
// docs/roadmap.md); the Chatter seam below is where they will plug in.
package agent

import (
	"context"
	_ "embed" // persona.txt
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Harfull/Kest/internal/clock"
	"github.com/Harfull/Kest/internal/llm"
	"github.com/Harfull/Kest/internal/store"
)

// persona is the system prompt core. It must match the fenced block in
// docs/persona/prompt-core.md; a test enforces that.
//
//go:embed persona.txt
var persona string

// DefaultHistory is how many recent messages go into each model call.
const DefaultHistory = 20

// Chatter is the model call the agent needs. *llm.Client satisfies it.
type Chatter interface {
	Chat(ctx context.Context, req llm.ChatRequest) (*llm.ChatResponse, error)
}

// History stores conversation turns. *store.DB satisfies it.
type History interface {
	AddMessage(ctx context.Context, m store.Message) (int64, error)
	RecentMessages(ctx context.Context, n int) ([]store.Message, error)
}

// Agent answers messages. It is safe for concurrent use, but callers should
// feed it one turn at a time so history stays in order.
type Agent struct {
	chat    Chatter
	history History
	clock   clock.Clock
	loc     *time.Location
	n       int
}

// New returns an Agent that reports times in loc and sends up to historyN
// recent messages with each call (DefaultHistory if zero).
func New(chat Chatter, history History, clk clock.Clock, loc *time.Location, historyN int) *Agent {
	if historyN <= 0 {
		historyN = DefaultHistory
	}
	return &Agent{chat: chat, history: history, clock: clk, loc: loc, n: historyN}
}

// SystemPrompt returns the persona plus facts about the present moment.
func (a *Agent) SystemPrompt() string {
	now := a.clock.Now().In(a.loc)
	return strings.TrimSpace(persona) + "\n\nContext\n- Local time: " +
		now.Format("Monday 2 January 2006, 15:04") + " (" + a.loc.String() + ")\n" +
		"- No tools are connected yet: you cannot send email, set reminders or look anything up. Say so plainly if asked.\n"
}

// Reply records the owner's message, asks the model, records and returns the
// answer. If the model call fails the owner's message stays in history so the
// next turn still has it.
func (a *Agent) Reply(ctx context.Context, userText string) (string, error) {
	userText = strings.TrimSpace(userText)
	if userText == "" {
		return "", errors.New("agent: empty message")
	}

	prior, err := a.history.RecentMessages(ctx, a.n)
	if err != nil {
		return "", fmt.Errorf("load history: %w", err)
	}

	if _, err := a.history.AddMessage(ctx, store.Message{
		TS: a.clock.Now(), Role: llm.RoleUser, Content: userText, Origin: "user",
	}); err != nil {
		return "", fmt.Errorf("store message: %w", err)
	}

	msgs := make([]llm.Message, 0, len(prior)+2)
	msgs = append(msgs, llm.Message{Role: llm.RoleSystem, Content: a.SystemPrompt()})
	for _, m := range prior {
		// Only conversation turns are replayed; tool and system rows are not.
		if m.Role == llm.RoleUser || m.Role == llm.RoleAssistant {
			msgs = append(msgs, llm.Message{Role: m.Role, Content: m.Content})
		}
	}
	msgs = append(msgs, llm.Message{Role: llm.RoleUser, Content: userText})

	resp, err := a.chat.Chat(ctx, llm.ChatRequest{Messages: msgs})
	if err != nil {
		return "", fmt.Errorf("model call: %w", err)
	}
	reply := strings.TrimSpace(resp.Message.Content)
	if reply == "" {
		return "", errors.New("agent: the model returned an empty reply")
	}

	if _, err := a.history.AddMessage(ctx, store.Message{
		TS: a.clock.Now(), Role: llm.RoleAssistant, Content: reply, Origin: "kest",
		Tokens: resp.Usage.CompletionTokens,
	}); err != nil {
		return reply, fmt.Errorf("store reply: %w", err)
	}
	return reply, nil
}
