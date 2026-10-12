package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Harfull/Kest/internal/clock"
	"github.com/Harfull/Kest/internal/plugin"
	"github.com/Harfull/Kest/internal/store"
)

// ConfirmRequest describes an action awaiting the owner's decision.
type ConfirmRequest struct {
	Tool    string // "plugin.tool"
	Risk    plugin.Risk
	Reason  string
	Args    json.RawMessage
	Summary string // human-readable rendering of the exact arguments
	// Grantable offers the owner a third choice, approving this and the tool's
	// further grantable calls for the rest of the turn.
	Grantable bool
}

// Confirmer asks the owner to approve an action. It returns true only on an
// explicit approval; a timeout or any failure is a refusal.
type Confirmer interface {
	Confirm(ctx context.Context, req ConfirmRequest) (bool, error)
}

// Choice is the owner's answer to an approval prompt.
type Choice int

// Choices. The zero value is a refusal, so an unanswered prompt fails closed.
const (
	// Skip declines the action.
	Skip Choice = iota
	// Once approves exactly this call.
	Once
	// Task approves this call and the plugin's grantable calls for the rest of
	// the turn.
	Task
)

// ChoiceConfirmer is a Confirmer that can also report a task grant. The agent
// uses it when available and treats a plain Confirmer's yes as Once.
type ChoiceConfirmer interface {
	Confirmer
	ConfirmChoice(ctx context.Context, req ConfirmRequest) (Choice, error)
}

// PendingStore persists pending actions. *store.DB satisfies it.
type PendingStore interface {
	AddPending(ctx context.Context, p store.PendingAction) error
	ResolvePending(ctx context.Context, id, state string) (bool, error)
}

// Prompt is what the transport shows the owner: the exact action and two
// buttons whose data carries ID.
type Prompt struct {
	ID      string
	Tool    string
	Risk    plugin.Risk
	Reason  string
	Summary string
	// Grantable adds an "Allow for this task" button.
	Grantable bool
}

// Approval button data is "act:ok:<id>", "act:task:<id>" or "act:no:<id>".
const (
	ApproveData = "act:ok:"
	TaskData    = "act:task:"
	DeclineData = "act:no:"
)

// ParseDecision extracts the action ID and choice from button data. A task
// grant counts as an approval here; use ParseChoice to tell them apart.
func ParseDecision(data string) (id string, approve, ok bool) {
	id, choice, ok := ParseChoice(data)
	return id, choice != Skip, ok
}

// ParseChoice extracts the action ID and choice from button data.
func ParseChoice(data string) (id string, choice Choice, ok bool) {
	switch {
	case strings.HasPrefix(data, ApproveData):
		return strings.TrimPrefix(data, ApproveData), Once, true
	case strings.HasPrefix(data, TaskData):
		return strings.TrimPrefix(data, TaskData), Task, true
	case strings.HasPrefix(data, DeclineData):
		return strings.TrimPrefix(data, DeclineData), Skip, true
	}
	return "", Skip, false
}

// Confirmations implements Confirmer on top of a persistent pending-action
// table and a button prompt. A turn that needs approval waits here, bounded by
// a time limit, while button presses arrive on another goroutine.
type Confirmations struct {
	store PendingStore
	clock clock.Clock
	ttl   time.Duration

	// Ask shows the prompt and returns an identifier for the message, which is
	// handed back to Settled. Required.
	Ask func(ctx context.Context, p Prompt) (messageID int, err error)
	// Settled is called after a decision, timeout or failure, so the transport
	// can remove the buttons. Optional.
	Settled func(ctx context.Context, messageID int, outcome string)

	mu      sync.Mutex
	waiters map[string]chan Choice
}

// NewConfirmations returns a Confirmations whose prompts expire after ttl.
func NewConfirmations(s PendingStore, clk clock.Clock, ttl time.Duration) *Confirmations {
	return &Confirmations{store: s, clock: clk, ttl: ttl, waiters: map[string]chan Choice{}}
}

var _ ChoiceConfirmer = (*Confirmations)(nil)

// Confirm implements Confirmer.
func (c *Confirmations) Confirm(ctx context.Context, req ConfirmRequest) (bool, error) {
	req.Grantable = false // a plain yes or no has no way to carry a grant
	choice, err := c.ConfirmChoice(ctx, req)
	return choice != Skip, err
}

// ConfirmChoice implements ChoiceConfirmer.
func (c *Confirmations) ConfirmChoice(ctx context.Context, req ConfirmRequest) (Choice, error) {
	if c.Ask == nil {
		return Skip, errors.New("no way to ask the owner for confirmation")
	}
	id, err := newActionID()
	if err != nil {
		return Skip, err
	}
	now := c.clock.Now()
	if err := c.store.AddPending(ctx, store.PendingAction{
		ID: id, TS: now, Tool: req.Tool, Args: string(req.Args), Summary: req.Summary, ExpiresAt: now.Add(c.ttl),
	}); err != nil {
		return Skip, err
	}

	ch := make(chan Choice, 1)
	c.mu.Lock()
	c.waiters[id] = ch
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.waiters, id)
		c.mu.Unlock()
	}()

	// Settle with a context that survives cancellation of the turn.
	settleCtx := context.WithoutCancel(ctx)

	msgID, err := c.Ask(ctx, Prompt{ID: id, Tool: req.Tool, Risk: req.Risk, Reason: req.Reason, Summary: req.Summary, Grantable: req.Grantable})
	if err != nil {
		_, _ = c.store.ResolvePending(settleCtx, id, store.PendingCancelled)
		return Skip, fmt.Errorf("ask for confirmation: %w", err)
	}

	timer := time.NewTimer(c.ttl)
	defer timer.Stop()

	choice := Skip
	outcome := store.PendingExpired
	select {
	case choice = <-ch:
		outcome = store.PendingCancelled
		if choice != Skip {
			outcome = store.PendingApproved
		}
	case <-timer.C:
		_, _ = c.store.ResolvePending(settleCtx, id, store.PendingExpired)
	case <-ctx.Done():
		_, _ = c.store.ResolvePending(settleCtx, id, store.PendingCancelled)
		outcome = store.PendingCancelled
	}

	if c.Settled != nil {
		c.Settled(settleCtx, msgID, outcome)
	}
	return choice, nil
}

// Resolve records the owner's button press as a plain approval or refusal.
func (c *Confirmations) Resolve(ctx context.Context, id string, approve bool) bool {
	if approve {
		return c.ResolveChoice(ctx, id, Once)
	}
	return c.ResolveChoice(ctx, id, Skip)
}

// ResolveChoice records the owner's button press. It reports whether the press
// took effect: false means the action is unknown, already decided or expired,
// and nothing runs.
func (c *Confirmations) ResolveChoice(ctx context.Context, id string, choice Choice) bool {
	state := store.PendingCancelled
	if choice != Skip {
		state = store.PendingApproved
	}
	ok, err := c.store.ResolvePending(ctx, id, state)
	if err != nil || !ok {
		return false
	}
	c.mu.Lock()
	ch := c.waiters[id]
	c.mu.Unlock()
	if ch != nil {
		ch <- choice
	}
	return true
}

func newActionID() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate action id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// DescribeArgs renders arguments for a confirmation prompt: one "key: value"
// line per field, values truncated. It is built from the structured call, never
// from model prose, so the owner sees what will actually run.
func DescribeArgs(args json.RawMessage) string {
	var m map[string]any
	if err := json.Unmarshal(args, &m); err != nil || len(m) == 0 {
		return "(no arguments)"
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	for _, k := range keys {
		v := fmt.Sprint(m[k])
		if s, ok := m[k].(string); ok {
			v = s
		}
		b.WriteString(k + ": " + Sanitize(v, 200) + "\n")
	}
	return strings.TrimSpace(b.String())
}
