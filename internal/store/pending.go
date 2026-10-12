package store

import (
	"context"
	"fmt"
	"time"
)

// Pending action states.
const (
	PendingWaiting   = "pending"
	PendingApproved  = "approved"
	PendingCancelled = "cancelled"
	PendingExpired   = "expired"
)

// PendingAction is a risky tool call waiting for the owner's decision.
type PendingAction struct {
	ID        string
	TS        time.Time
	Tool      string
	Args      string // JSON
	Summary   string
	ExpiresAt time.Time
}

// AddPending records an action awaiting confirmation.
func (d *DB) AddPending(ctx context.Context, p PendingAction) error {
	_, err := d.w.ExecContext(ctx, `
		INSERT INTO pending_actions (id, ts, tool, args, summary, expires_at, state)
		VALUES (?, ?, ?, ?, ?, ?, 'pending')`,
		p.ID, ms(p.TS), p.Tool, p.Args, p.Summary, ms(p.ExpiresAt))
	if err != nil {
		return fmt.Errorf("add pending action: %w", err)
	}
	return nil
}

// ResolvePending moves a pending action to state (approved, cancelled or
// expired). It returns false if the action was not pending, which makes double
// presses and late presses harmless.
func (d *DB) ResolvePending(ctx context.Context, id, state string) (bool, error) {
	res, err := d.w.ExecContext(ctx,
		"UPDATE pending_actions SET state = ? WHERE id = ? AND state = 'pending'", state, id)
	if err != nil {
		return false, fmt.Errorf("resolve pending action: %w", err)
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// CancelAllPending cancels every action left pending, for example after a
// restart, when nothing is waiting for an answer any more.
func (d *DB) CancelAllPending(ctx context.Context) (int64, error) {
	res, err := d.w.ExecContext(ctx,
		"UPDATE pending_actions SET state = 'cancelled' WHERE state = 'pending'")
	if err != nil {
		return 0, fmt.Errorf("cancel pending actions: %w", err)
	}
	return res.RowsAffected()
}

// PendingState returns the current state of an action, or ErrNotFound.
func (d *DB) PendingState(ctx context.Context, id string) (string, error) {
	var s string
	err := d.r.QueryRowContext(ctx, "SELECT state FROM pending_actions WHERE id = ?", id).Scan(&s)
	if err != nil {
		return "", ErrNotFound
	}
	return s, nil
}
