package llm

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Harfull/Kest/internal/clock"
)

// ErrBudgetExceeded is returned for proactive calls once the daily limit is hit.
var ErrBudgetExceeded = errors.New("llm: daily budget exceeded")

// Budget enforces a daily spend limit measured from local midnight.
type Budget struct {
	limitUSD float64
	ledger   Ledger
	clock    clock.Clock
	loc      *time.Location
}

// NewBudget returns a Budget. A limit of zero or less disables it.
func NewBudget(limitUSD float64, ledger Ledger, clk clock.Clock, loc *time.Location) *Budget {
	return &Budget{limitUSD: limitUSD, ledger: ledger, clock: clk, loc: loc}
}

// Spent returns today's spend in USD.
func (b *Budget) Spent(ctx context.Context) (float64, error) {
	now := b.clock.Now().In(b.loc)
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, b.loc)
	return b.ledger.SpendSince(ctx, midnight)
}

// Check returns ErrBudgetExceeded when today's spend has reached the limit.
func (b *Budget) Check(ctx context.Context) error {
	if b.limitUSD <= 0 {
		return nil
	}
	spent, err := b.Spent(ctx)
	if err != nil {
		return fmt.Errorf("check budget: %w", err)
	}
	if spent >= b.limitUSD {
		return fmt.Errorf("%w ($%.2f of $%.2f)", ErrBudgetExceeded, spent, b.limitUSD)
	}
	return nil
}

// Limit returns the configured daily limit in USD.
func (b *Budget) Limit() float64 { return b.limitUSD }

// SetBudget attaches a Budget to the client. Proactive chat calls are refused
// while it reports the limit as reached.
func (c *Client) SetBudget(b *Budget) { c.budget = b }
