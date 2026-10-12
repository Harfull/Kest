// Package logbook stores a plugin's records by local day in its key/value
// store: one JSON list per day, under a prefix such as "workouts/2026-10-12".
//
// The workout, nutrition and focus plugins share it. It is not a database: it
// is for a handful of small records a day, read back by day or by week.
package logbook

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Harfull/Kest/internal/plugin"
)

// DayLayout is how days are written: a local calendar date.
const DayLayout = "2006-01-02"

// Book is the records of one kind.
type Book[T any] struct {
	KV     plugin.KV
	Prefix string // for example "workouts/"
}

// Day returns the local calendar day containing t.
func Day(t time.Time, loc *time.Location) string { return t.In(loc).Format(DayLayout) }

// ParseDay checks and returns a day written as 2026-10-12.
func ParseDay(s string, loc *time.Location) (time.Time, error) {
	t, err := time.ParseInLocation(DayLayout, strings.TrimSpace(s), loc)
	if err != nil {
		return time.Time{}, fmt.Errorf("%q is not a date like 2026-10-12", s)
	}
	return t, nil
}

// Load returns the records of one day, oldest first.
func (b Book[T]) Load(ctx context.Context, day string) ([]T, error) {
	raw, err := b.KV.Get(ctx, b.Prefix+day)
	if errors.Is(err, plugin.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load %s: %w", day, err)
	}
	var items []T
	if err := json.Unmarshal([]byte(raw), &items); err != nil {
		return nil, fmt.Errorf("read %s: %w", day, err)
	}
	return items, nil
}

// Save replaces the records of one day. An empty list removes the day.
func (b Book[T]) Save(ctx context.Context, day string, items []T) error {
	if len(items) == 0 {
		return b.KV.Delete(ctx, b.Prefix+day)
	}
	raw, err := json.Marshal(items)
	if err != nil {
		return fmt.Errorf("encode %s: %w", day, err)
	}
	return b.KV.Set(ctx, b.Prefix+day, string(raw))
}

// Add appends one record to a day.
func (b Book[T]) Add(ctx context.Context, day string, item T) error {
	items, err := b.Load(ctx, day)
	if err != nil {
		return err
	}
	return b.Save(ctx, day, append(items, item))
}

// Range returns the records of the days from..to inclusive, by day.
func (b Book[T]) Range(ctx context.Context, from, to string) (map[string][]T, error) {
	entries, err := b.KV.List(ctx, b.Prefix)
	if err != nil {
		return nil, fmt.Errorf("list %s: %w", b.Prefix, err)
	}
	out := map[string][]T{}
	for _, e := range entries {
		day := strings.TrimPrefix(e.Key, b.Prefix)
		if day < from || day > to {
			continue
		}
		var items []T
		if json.Unmarshal([]byte(e.Value), &items) != nil {
			continue // a damaged day is skipped, not fatal for the week
		}
		out[day] = items
	}
	return out, nil
}

// Days lists the days from..to inclusive, oldest first, including days with no
// records, so a caller can show gaps.
func Days(from, to time.Time, loc *time.Location) []string {
	var out []string
	for d := from.In(loc); !d.After(to.In(loc)) && len(out) < 400; d = d.AddDate(0, 0, 1) {
		out = append(out, d.Format(DayLayout))
	}
	return out
}

// WeekStart is the Monday on or before t, in loc.
func WeekStart(t time.Time, loc *time.Location) time.Time {
	l := t.In(loc)
	l = time.Date(l.Year(), l.Month(), l.Day(), 0, 0, 0, 0, loc)
	return l.AddDate(0, 0, -((int(l.Weekday()) + 6) % 7))
}
