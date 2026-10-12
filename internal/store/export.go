package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// exportDoc is the JSON shape written by Export. Secrets never live in the
// database, so nothing here needs redacting.
type exportDoc struct {
	ExportedAt time.Time                  `json:"exported_at"`
	Profile    map[string]json.RawMessage `json:"profile"`
	Messages   []exportMessage            `json:"messages"`
	Memories   []exportMemory             `json:"memories"`
	Usage      []exportUsage              `json:"llm_usage"`
}

type exportMemory struct {
	TS      time.Time `json:"ts"`
	Kind    string    `json:"kind"`
	Origin  string    `json:"origin"`
	Content string    `json:"content"`
}

type exportMessage struct {
	TS      time.Time `json:"ts"`
	Role    string    `json:"role"`
	Origin  string    `json:"origin"`
	Content string    `json:"content"`
}

type exportUsage struct {
	TS      time.Time `json:"ts"`
	Slot    string    `json:"slot"`
	Model   string    `json:"model"`
	CostUSD float64   `json:"cost_usd"`
}

// Export returns all personal data as indented JSON: preferences, conversation
// history and model usage.
func (d *DB) Export(ctx context.Context, now time.Time) ([]byte, error) {
	doc := exportDoc{
		ExportedAt: now.UTC(),
		Profile:    map[string]json.RawMessage{},
		Messages:   []exportMessage{},
		Memories:   []exportMemory{},
		Usage:      []exportUsage{},
	}

	prof, err := d.r.QueryContext(ctx, "SELECT key, value FROM profile ORDER BY key")
	if err != nil {
		return nil, fmt.Errorf("export profile: %w", err)
	}
	for prof.Next() {
		var k, v string
		if err := prof.Scan(&k, &v); err != nil {
			_ = prof.Close()
			return nil, fmt.Errorf("export profile: %w", err)
		}
		if json.Valid([]byte(v)) {
			doc.Profile[k] = json.RawMessage(v)
		} else {
			quoted, _ := json.Marshal(v)
			doc.Profile[k] = quoted
		}
	}
	if err := prof.Err(); err != nil {
		_ = prof.Close()
		return nil, fmt.Errorf("export profile: %w", err)
	}
	if err := prof.Close(); err != nil {
		return nil, err
	}

	msgs, err := d.r.QueryContext(ctx, "SELECT ts, role, origin, content FROM messages ORDER BY id")
	if err != nil {
		return nil, fmt.Errorf("export messages: %w", err)
	}
	for msgs.Next() {
		var m exportMessage
		var ts int64
		if err := msgs.Scan(&ts, &m.Role, &m.Origin, &m.Content); err != nil {
			_ = msgs.Close()
			return nil, fmt.Errorf("export messages: %w", err)
		}
		m.TS = fromMS(ts)
		doc.Messages = append(doc.Messages, m)
	}
	if err := msgs.Err(); err != nil {
		_ = msgs.Close()
		return nil, fmt.Errorf("export messages: %w", err)
	}
	if err := msgs.Close(); err != nil {
		return nil, err
	}

	mems, err := d.r.QueryContext(ctx, "SELECT ts, kind, origin, content FROM memories ORDER BY id")
	if err != nil {
		return nil, fmt.Errorf("export memories: %w", err)
	}
	for mems.Next() {
		var m exportMemory
		var ts int64
		if err := mems.Scan(&ts, &m.Kind, &m.Origin, &m.Content); err != nil {
			_ = mems.Close()
			return nil, fmt.Errorf("export memories: %w", err)
		}
		m.TS = fromMS(ts)
		doc.Memories = append(doc.Memories, m)
	}
	if err := mems.Err(); err != nil {
		_ = mems.Close()
		return nil, fmt.Errorf("export memories: %w", err)
	}
	if err := mems.Close(); err != nil {
		return nil, err
	}

	use, err := d.r.QueryContext(ctx, "SELECT ts, slot, model, cost_usd FROM llm_usage ORDER BY id")
	if err != nil {
		return nil, fmt.Errorf("export usage: %w", err)
	}
	for use.Next() {
		var u exportUsage
		var ts int64
		if err := use.Scan(&ts, &u.Slot, &u.Model, &u.CostUSD); err != nil {
			_ = use.Close()
			return nil, fmt.Errorf("export usage: %w", err)
		}
		u.TS = fromMS(ts)
		doc.Usage = append(doc.Usage, u)
	}
	if err := use.Err(); err != nil {
		_ = use.Close()
		return nil, fmt.Errorf("export usage: %w", err)
	}
	if err := use.Close(); err != nil {
		return nil, err
	}

	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode export: %w", err)
	}
	return out, nil
}

// Forget deletes the owner's conversation history, memories and stored
// preferences. Plugin data such as tasks is kept.
// Scheduled jobs, audit entries and cost records are kept: they hold no
// conversation content and the budget guard needs them.
func (d *DB) Forget(ctx context.Context) error {
	return d.Tx(ctx, func(tx *sql.Tx) error {
		for _, table := range []string{"messages", "memories", "profile"} {
			if _, err := tx.ExecContext(ctx, "DELETE FROM "+table); err != nil { //nolint:gosec // fixed table names
				return fmt.Errorf("forget %s: %w", table, err)
			}
		}
		return nil
	})
}
