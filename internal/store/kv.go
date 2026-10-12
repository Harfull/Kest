package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// KV is key/value storage scoped to one plugin.
type KV struct {
	db     *DB
	plugin string
	now    func() time.Time
}

// KV returns storage scoped to the named plugin. now supplies timestamps.
func (d *DB) KV(plugin string, now func() time.Time) *KV {
	return &KV{db: d, plugin: plugin, now: now}
}

// KVItem is one stored entry.
type KVItem struct{ Key, Value string }

// Get returns the value for key, or ErrNotFound.
func (k *KV) Get(ctx context.Context, key string) (string, error) {
	var v string
	err := k.db.r.QueryRowContext(ctx,
		"SELECT value FROM plugin_kv WHERE plugin = ? AND key = ?", k.plugin, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("kv get: %w", err)
	}
	return v, nil
}

// Set stores a value.
func (k *KV) Set(ctx context.Context, key, value string) error {
	_, err := k.db.w.ExecContext(ctx, `
		INSERT INTO plugin_kv (plugin, key, value, updated_at) VALUES (?, ?, ?, ?)
		ON CONFLICT (plugin, key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		k.plugin, key, value, ms(k.now()))
	if err != nil {
		return fmt.Errorf("kv set: %w", err)
	}
	return nil
}

// Delete removes a key. Deleting a missing key is not an error.
func (k *KV) Delete(ctx context.Context, key string) error {
	if _, err := k.db.w.ExecContext(ctx,
		"DELETE FROM plugin_kv WHERE plugin = ? AND key = ?", k.plugin, key); err != nil {
		return fmt.Errorf("kv delete: %w", err)
	}
	return nil
}

// List returns entries whose key starts with prefix, ordered by key.
func (k *KV) List(ctx context.Context, prefix string) ([]KVItem, error) {
	// Escape LIKE wildcards so the prefix is matched literally.
	esc := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(prefix)
	rows, err := k.db.r.QueryContext(ctx,
		`SELECT key, value FROM plugin_kv WHERE plugin = ? AND key LIKE ? ESCAPE '\' ORDER BY key`,
		k.plugin, esc+"%")
	if err != nil {
		return nil, fmt.Errorf("kv list: %w", err)
	}
	defer rows.Close() //nolint:errcheck // surfaced via rows.Err

	var out []KVItem
	for rows.Next() {
		var it KVItem
		if err := rows.Scan(&it.Key, &it.Value); err != nil {
			return nil, fmt.Errorf("kv scan: %w", err)
		}
		out = append(out, it)
	}
	return out, rows.Err()
}
