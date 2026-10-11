package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ms converts a time to unix milliseconds.
func ms(t time.Time) int64 { return t.UnixMilli() }

// fromMS converts unix milliseconds to a UTC time.
func fromMS(v int64) time.Time { return time.UnixMilli(v).UTC() }

// --- profile -----------------------------------------------------------------

// GetProfile returns the JSON value stored under key, or ErrNotFound.
func (d *DB) GetProfile(ctx context.Context, key string) (string, error) {
	var v string
	err := d.r.QueryRowContext(ctx, "SELECT value FROM profile WHERE key = ?", key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("get profile %q: %w", key, err)
	}
	return v, nil
}

// SetProfile stores a JSON value under key.
func (d *DB) SetProfile(ctx context.Context, key, value string, now time.Time) error {
	_, err := d.w.ExecContext(ctx, `
		INSERT INTO profile (key, value, updated_at) VALUES (?, ?, ?)
		ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		key, value, ms(now))
	if err != nil {
		return fmt.Errorf("set profile %q: %w", key, err)
	}
	return nil
}

// --- messages ----------------------------------------------------------------

// Message is one stored conversation turn.
type Message struct {
	ID        int64
	TS        time.Time
	Role      string // user, assistant, system, tool
	Content   string
	Origin    string // user, kest, tool:<plugin>, web
	ToolCalls string // JSON, empty if none
	Tokens    int
}

// AddMessage stores a message and returns its ID.
func (d *DB) AddMessage(ctx context.Context, m Message) (int64, error) {
	if m.Origin == "" {
		m.Origin = "user"
	}
	res, err := d.w.ExecContext(ctx, `
		INSERT INTO messages (ts, role, content, origin, tool_calls, tokens)
		VALUES (?, ?, ?, ?, NULLIF(?, ''), ?)`,
		ms(m.TS), m.Role, m.Content, m.Origin, m.ToolCalls, m.Tokens)
	if err != nil {
		return 0, fmt.Errorf("add message: %w", err)
	}
	return res.LastInsertId()
}

// RecentMessages returns up to n messages, oldest first.
func (d *DB) RecentMessages(ctx context.Context, n int) ([]Message, error) {
	rows, err := d.r.QueryContext(ctx, `
		SELECT id, ts, role, content, origin, COALESCE(tool_calls, ''), tokens
		FROM (SELECT * FROM messages ORDER BY id DESC LIMIT ?)
		ORDER BY id ASC`, n)
	if err != nil {
		return nil, fmt.Errorf("query recent messages: %w", err)
	}
	defer rows.Close() //nolint:errcheck // read-only rows; error surfaces via rows.Err

	var out []Message
	for rows.Next() {
		var m Message
		var ts int64
		if err := rows.Scan(&m.ID, &ts, &m.Role, &m.Content, &m.Origin, &m.ToolCalls, &m.Tokens); err != nil {
			return nil, fmt.Errorf("scan message: %w", err)
		}
		m.TS = fromMS(ts)
		out = append(out, m)
	}
	return out, rows.Err()
}

// PruneMessages deletes messages older than cutoff and returns how many.
func (d *DB) PruneMessages(ctx context.Context, cutoff time.Time) (int64, error) {
	res, err := d.w.ExecContext(ctx, "DELETE FROM messages WHERE ts < ?", ms(cutoff))
	if err != nil {
		return 0, fmt.Errorf("prune messages: %w", err)
	}
	return res.RowsAffected()
}

// --- LLM usage ---------------------------------------------------------------

// Usage records the cost of one model call.
type Usage struct {
	TS               time.Time
	Slot             string
	Model            string
	PromptTokens     int
	CompletionTokens int
	CostUSD          float64
	GenerationID     string
}

// AddUsage records a model call.
func (d *DB) AddUsage(ctx context.Context, u Usage) error {
	_, err := d.w.ExecContext(ctx, `
		INSERT INTO llm_usage (ts, slot, model, prompt_tokens, completion_tokens, cost_usd, generation_id)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		ms(u.TS), u.Slot, u.Model, u.PromptTokens, u.CompletionTokens, u.CostUSD, u.GenerationID)
	if err != nil {
		return fmt.Errorf("add usage: %w", err)
	}
	return nil
}

// SpendSince returns total cost in USD recorded at or after since.
func (d *DB) SpendSince(ctx context.Context, since time.Time) (float64, error) {
	var total float64
	err := d.r.QueryRowContext(ctx,
		"SELECT COALESCE(SUM(cost_usd), 0) FROM llm_usage WHERE ts >= ?", ms(since)).Scan(&total)
	if err != nil {
		return 0, fmt.Errorf("sum spend: %w", err)
	}
	return total, nil
}

// --- audit -------------------------------------------------------------------

// AuditEntry describes something Kest did. It never holds message bodies.
type AuditEntry struct {
	TS        time.Time
	Actor     string
	Action    string
	Risk      string
	Confirmed bool
	Status    string
	Summary   string
}

// AddAudit appends an audit entry.
func (d *DB) AddAudit(ctx context.Context, e AuditEntry) error {
	if e.Risk == "" {
		e.Risk = "read"
	}
	confirmed := 0
	if e.Confirmed {
		confirmed = 1
	}
	_, err := d.w.ExecContext(ctx, `
		INSERT INTO audit_log (ts, actor, action, risk, confirmed, status, summary)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		ms(e.TS), e.Actor, e.Action, e.Risk, confirmed, e.Status, e.Summary)
	if err != nil {
		return fmt.Errorf("add audit entry: %w", err)
	}
	return nil
}
