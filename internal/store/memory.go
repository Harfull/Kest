package store

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Memory kinds.
const (
	MemoryFact    = "fact"
	MemorySummary = "summary"
)

// Memory origins. Only OriginUser and OriginKest entries may shape behavior;
// everything else is quarantined data. See docs/architecture/security.md.
const (
	OriginUser = "user" // the owner said it
	OriginKest = "kest" // Kest derived it from the owner's own words
)

// TrustedOrigin reports whether memories from origin may influence prompts as
// facts about the owner.
func TrustedOrigin(origin string) bool { return origin == OriginUser || origin == OriginKest }

// Memory is one long-term fact or conversation summary.
type Memory struct {
	ID          int64
	TS          time.Time
	Kind        string
	Content     string
	Origin      string
	Confidence  float64
	SourceMsgID int64
}

// AddMemory stores a memory and returns its ID.
func (d *DB) AddMemory(ctx context.Context, m Memory) (int64, error) {
	if m.Origin == "" {
		return 0, fmt.Errorf("add memory: origin is required")
	}
	if m.Confidence == 0 {
		m.Confidence = 1
	}
	res, err := d.w.ExecContext(ctx, `
		INSERT INTO memories (ts, kind, content, origin, confidence, source_msg_id)
		VALUES (?, ?, ?, ?, ?, NULLIF(?, 0))`,
		ms(m.TS), m.Kind, m.Content, m.Origin, m.Confidence, m.SourceMsgID)
	if err != nil {
		return 0, fmt.Errorf("add memory: %w", err)
	}
	return res.LastInsertId()
}

// RecentMemories returns up to n memories of kind from trusted origins, newest
// first.
func (d *DB) RecentMemories(ctx context.Context, kind string, n int) ([]Memory, error) {
	return d.queryMemories(ctx, `
		SELECT id, ts, kind, content, origin, confidence, COALESCE(source_msg_id, 0)
		FROM memories WHERE kind = ? AND origin IN ('user', 'kest')
		ORDER BY ts DESC, id DESC LIMIT ?`, kind, n)
}

// SearchMemories returns trusted facts whose text contains every word of query,
// newest first. It is a plain substring search; embeddings are not needed at
// this scale.
func (d *DB) SearchMemories(ctx context.Context, query string, n int) ([]Memory, error) {
	words := strings.Fields(strings.ToLower(query))
	if len(words) == 0 {
		return d.RecentMemories(ctx, MemoryFact, n)
	}
	q := `SELECT id, ts, kind, content, origin, confidence, COALESCE(source_msg_id, 0)
		FROM memories WHERE kind = 'fact' AND origin IN ('user', 'kest')`
	args := []any{}
	for _, w := range words {
		q += ` AND lower(content) LIKE ? ESCAPE '\'`
		args = append(args, "%"+strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(w)+"%")
	}
	q += ` ORDER BY ts DESC, id DESC LIMIT ?`
	args = append(args, n)
	return d.queryMemories(ctx, q, args...)
}

// DeleteMemory removes a memory. It returns ErrNotFound if it does not exist.
func (d *DB) DeleteMemory(ctx context.Context, id int64) error {
	res, err := d.w.ExecContext(ctx, "DELETE FROM memories WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("delete memory: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// MessagesAfter returns user and assistant messages with ID greater than
// afterID, oldest first, up to limit. It feeds summarization.
func (d *DB) MessagesAfter(ctx context.Context, afterID int64, limit int) ([]Message, error) {
	rows, err := d.r.QueryContext(ctx, `
		SELECT id, ts, role, content, origin, COALESCE(tool_calls, ''), tokens
		FROM messages WHERE id > ? AND role IN ('user', 'assistant')
		ORDER BY id ASC LIMIT ?`, afterID, limit)
	if err != nil {
		return nil, fmt.Errorf("query messages: %w", err)
	}
	defer rows.Close() //nolint:errcheck // surfaced via rows.Err

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

// LatestMessageID returns the highest message ID, or 0 if there are none.
func (d *DB) LatestMessageID(ctx context.Context) (int64, error) {
	var id int64
	err := d.r.QueryRowContext(ctx, "SELECT COALESCE(MAX(id), 0) FROM messages").Scan(&id)
	return id, err
}

func (d *DB) queryMemories(ctx context.Context, q string, args ...any) ([]Memory, error) {
	rows, err := d.r.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("query memories: %w", err)
	}
	defer rows.Close() //nolint:errcheck // surfaced via rows.Err

	var out []Memory
	for rows.Next() {
		var m Memory
		var ts int64
		if err := rows.Scan(&m.ID, &ts, &m.Kind, &m.Content, &m.Origin, &m.Confidence, &m.SourceMsgID); err != nil {
			return nil, fmt.Errorf("scan memory: %w", err)
		}
		m.TS = fromMS(ts)
		out = append(out, m)
	}
	return out, rows.Err()
}
