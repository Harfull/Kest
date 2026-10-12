package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// --- habits ------------------------------------------------------------------

// Habit states.
const (
	HabitActive  = "active"
	HabitPaused  = "paused"
	HabitRetired = "retired"
)

// Habit is a habit with its if-then plan.
type Habit struct {
	ID          string
	Name        string
	Why         string
	Anchor      string // "After I finish lunch"
	Behavior    string // "Walk for 10 minutes"
	TinyVersion string // the worst-day version
	Cadence     string // daily | weekdays | days:mon,wed | times:3
	WindowStart string // local HH:MM, when the prompt goes out
	WindowEnd   string // local HH:MM, when the prompt stops being useful
	State       string
	CreatedAt   time.Time
}

const habitCols = "id, name, why, anchor, behavior, tiny_version, cadence, window_start, window_end, state, created_at"

func scanHabit(r interface{ Scan(...any) error }) (Habit, error) {
	var h Habit
	var created int64
	err := r.Scan(&h.ID, &h.Name, &h.Why, &h.Anchor, &h.Behavior, &h.TinyVersion, &h.Cadence,
		&h.WindowStart, &h.WindowEnd, &h.State, &created)
	h.CreatedAt = fromMS(created)
	return h, err
}

// AddHabit stores a new habit.
func (d *DB) AddHabit(ctx context.Context, h Habit) error {
	if h.State == "" {
		h.State = HabitActive
	}
	_, err := d.w.ExecContext(ctx, `
		INSERT INTO habits (`+habitCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		h.ID, h.Name, h.Why, h.Anchor, h.Behavior, h.TinyVersion, h.Cadence,
		h.WindowStart, h.WindowEnd, h.State, ms(h.CreatedAt))
	if err != nil {
		return fmt.Errorf("add habit: %w", err)
	}
	return nil
}

// UpdateHabit replaces a habit's editable fields.
func (d *DB) UpdateHabit(ctx context.Context, h Habit) error {
	res, err := d.w.ExecContext(ctx, `
		UPDATE habits SET name = ?, why = ?, anchor = ?, behavior = ?, tiny_version = ?, cadence = ?,
		    window_start = ?, window_end = ?, state = ? WHERE id = ?`,
		h.Name, h.Why, h.Anchor, h.Behavior, h.TinyVersion, h.Cadence, h.WindowStart, h.WindowEnd, h.State, h.ID)
	if err != nil {
		return fmt.Errorf("update habit: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// GetHabit returns one habit or ErrNotFound.
func (d *DB) GetHabit(ctx context.Context, id string) (Habit, error) {
	h, err := scanHabit(d.r.QueryRowContext(ctx, "SELECT "+habitCols+" FROM habits WHERE id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return Habit{}, ErrNotFound
	}
	if err != nil {
		return Habit{}, fmt.Errorf("get habit: %w", err)
	}
	return h, nil
}

// ListHabits returns habits in creation order. Retired habits are omitted
// unless includeRetired is set.
func (d *DB) ListHabits(ctx context.Context, includeRetired bool) ([]Habit, error) {
	q := "SELECT " + habitCols + " FROM habits"
	if !includeRetired {
		q += " WHERE state != 'retired'"
	}
	rows, err := d.r.QueryContext(ctx, q+" ORDER BY created_at, id")
	if err != nil {
		return nil, fmt.Errorf("list habits: %w", err)
	}
	defer rows.Close() //nolint:errcheck // surfaced via rows.Err

	var out []Habit
	for rows.Next() {
		h, err := scanHabit(rows)
		if err != nil {
			return nil, fmt.Errorf("scan habit: %w", err)
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// --- habit logs ----------------------------------------------------------------

// Habit log statuses.
const (
	LogDone    = "done"
	LogTiny    = "tiny"
	LogSkipped = "skipped"
)

// HabitLog records what happened with a habit on one local day.
type HabitLog struct {
	HabitID string
	Day     string // YYYY-MM-DD, local
	Status  string
	Note    string
	TS      time.Time
}

// SetHabitLog records or replaces the outcome for a habit on a day.
func (d *DB) SetHabitLog(ctx context.Context, l HabitLog) error {
	_, err := d.w.ExecContext(ctx, `
		INSERT INTO habit_logs (habit_id, day, status, note, ts) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (habit_id, day) DO UPDATE SET status = excluded.status, note = excluded.note, ts = excluded.ts`,
		l.HabitID, l.Day, l.Status, l.Note, ms(l.TS))
	if err != nil {
		return fmt.Errorf("set habit log: %w", err)
	}
	return nil
}

// HabitLogs returns the logs for a habit on days from..to inclusive (YYYY-MM-DD),
// oldest first.
func (d *DB) HabitLogs(ctx context.Context, habitID, from, to string) ([]HabitLog, error) {
	rows, err := d.r.QueryContext(ctx, `
		SELECT habit_id, day, status, note, ts FROM habit_logs
		WHERE habit_id = ? AND day >= ? AND day <= ? ORDER BY day`, habitID, from, to)
	if err != nil {
		return nil, fmt.Errorf("habit logs: %w", err)
	}
	defer rows.Close() //nolint:errcheck // surfaced via rows.Err

	var out []HabitLog
	for rows.Next() {
		var l HabitLog
		var ts int64
		if err := rows.Scan(&l.HabitID, &l.Day, &l.Status, &l.Note, &ts); err != nil {
			return nil, fmt.Errorf("scan habit log: %w", err)
		}
		l.TS = fromMS(ts)
		out = append(out, l)
	}
	return out, rows.Err()
}

// ShowingUpDays counts the days on which a habit was done or done in its tiny
// form, over all time.
func (d *DB) ShowingUpDays(ctx context.Context, habitID string) (int, error) {
	var n int
	err := d.r.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM habit_logs WHERE habit_id = ? AND status IN ('done', 'tiny')", habitID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count showing-up days: %w", err)
	}
	return n, nil
}

// --- sleep ---------------------------------------------------------------------

// Sleep event kinds.
const (
	SleepBedtime  = "bedtime"
	SleepWake     = "wake"
	SleepActivity = "activity"
	SleepImport   = "import"
)

// SleepEvent is a raw signal about sleep.
type SleepEvent struct {
	TS     time.Time
	Kind   string
	Source string
}

// AddSleepEvent records a signal.
func (d *DB) AddSleepEvent(ctx context.Context, e SleepEvent) error {
	if e.Source == "" {
		e.Source = "chat"
	}
	_, err := d.w.ExecContext(ctx,
		"INSERT INTO sleep_events (ts, kind, source) VALUES (?, ?, ?)", ms(e.TS), e.Kind, e.Source)
	if err != nil {
		return fmt.Errorf("add sleep event: %w", err)
	}
	return nil
}

// SleepEvents returns events with from <= ts < to, oldest first.
func (d *DB) SleepEvents(ctx context.Context, from, to time.Time) ([]SleepEvent, error) {
	rows, err := d.r.QueryContext(ctx,
		"SELECT ts, kind, source FROM sleep_events WHERE ts >= ? AND ts < ? ORDER BY ts, id", ms(from), ms(to))
	if err != nil {
		return nil, fmt.Errorf("sleep events: %w", err)
	}
	defer rows.Close() //nolint:errcheck // surfaced via rows.Err

	var out []SleepEvent
	for rows.Next() {
		var e SleepEvent
		var ts int64
		if err := rows.Scan(&ts, &e.Kind, &e.Source); err != nil {
			return nil, fmt.Errorf("scan sleep event: %w", err)
		}
		e.TS = fromMS(ts)
		out = append(out, e)
	}
	return out, rows.Err()
}

// LastActivity returns the time of the most recent activity event at or before
// t, and whether there is one.
func (d *DB) LastActivity(ctx context.Context, t time.Time) (time.Time, bool, error) {
	var ts sql.NullInt64
	err := d.r.QueryRowContext(ctx,
		"SELECT MAX(ts) FROM sleep_events WHERE kind = 'activity' AND ts <= ?", ms(t)).Scan(&ts)
	if err != nil {
		return time.Time{}, false, fmt.Errorf("last activity: %w", err)
	}
	return fromMS(ts.Int64), ts.Valid, nil
}

// Sleep night confidence levels, strongest first.
const (
	ConfExplicit  = "explicit"  // the owner gave both times
	ConfConfirmed = "confirmed" // the owner confirmed an estimate
	ConfInferred  = "inferred"  // derived from gaps in chat activity
	ConfPrior     = "prior"     // only the declared schedule
)

// SleepNight is one night's estimate, keyed by the local date of waking.
type SleepNight struct {
	Day           string
	Onset, Wake   time.Time
	Confidence    string
	UserConfirmed bool
}

// SetSleepNight records or replaces a night.
func (d *DB) SetSleepNight(ctx context.Context, n SleepNight) error {
	confirmed := 0
	if n.UserConfirmed {
		confirmed = 1
	}
	_, err := d.w.ExecContext(ctx, `
		INSERT INTO sleep_nights (day, onset, wake, confidence, user_confirmed) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (day) DO UPDATE SET onset = excluded.onset, wake = excluded.wake,
		    confidence = excluded.confidence, user_confirmed = excluded.user_confirmed`,
		n.Day, ms(n.Onset), ms(n.Wake), n.Confidence, confirmed)
	if err != nil {
		return fmt.Errorf("set sleep night: %w", err)
	}
	return nil
}

// SleepNights returns up to n nights ending at or before day (YYYY-MM-DD),
// newest first.
func (d *DB) SleepNights(ctx context.Context, day string, n int) ([]SleepNight, error) {
	rows, err := d.r.QueryContext(ctx, `
		SELECT day, onset, wake, confidence, user_confirmed FROM sleep_nights
		WHERE day <= ? ORDER BY day DESC LIMIT ?`, day, n)
	if err != nil {
		return nil, fmt.Errorf("sleep nights: %w", err)
	}
	defer rows.Close() //nolint:errcheck // surfaced via rows.Err

	var out []SleepNight
	for rows.Next() {
		var s SleepNight
		var onset, wake int64
		var confirmed int
		if err := rows.Scan(&s.Day, &onset, &wake, &s.Confidence, &confirmed); err != nil {
			return nil, fmt.Errorf("scan sleep night: %w", err)
		}
		s.Onset, s.Wake, s.UserConfirmed = fromMS(onset), fromMS(wake), confirmed == 1
		out = append(out, s)
	}
	return out, rows.Err()
}

// GetSleepNight returns one night or ErrNotFound.
func (d *DB) GetSleepNight(ctx context.Context, day string) (SleepNight, error) {
	rows, err := d.SleepNights(ctx, day, 1)
	if err != nil {
		return SleepNight{}, err
	}
	if len(rows) == 0 || rows[0].Day != day {
		return SleepNight{}, ErrNotFound
	}
	return rows[0], nil
}

// --- proactive messages ------------------------------------------------------------

// Notification is one proactive message Kest sent.
type Notification struct {
	ID          int64
	TS          time.Time
	Kind        string
	Variant     string
	RespondedAt time.Time // zero if the owner has not reacted
}

// AddNotification records a proactive message and returns its ID.
func (d *DB) AddNotification(ctx context.Context, n Notification) (int64, error) {
	res, err := d.w.ExecContext(ctx,
		"INSERT INTO notifications (ts, kind, variant) VALUES (?, ?, ?)", ms(n.TS), n.Kind, n.Variant)
	if err != nil {
		return 0, fmt.Errorf("add notification: %w", err)
	}
	return res.LastInsertId()
}

// Notifications returns messages sent at or after since, oldest first.
func (d *DB) Notifications(ctx context.Context, since time.Time) ([]Notification, error) {
	rows, err := d.r.QueryContext(ctx, `
		SELECT id, ts, kind, variant, responded_at FROM notifications WHERE ts >= ? ORDER BY ts, id`, ms(since))
	if err != nil {
		return nil, fmt.Errorf("notifications: %w", err)
	}
	defer rows.Close() //nolint:errcheck // surfaced via rows.Err

	var out []Notification
	for rows.Next() {
		var n Notification
		var ts int64
		var resp sql.NullInt64
		if err := rows.Scan(&n.ID, &ts, &n.Kind, &n.Variant, &resp); err != nil {
			return nil, fmt.Errorf("scan notification: %w", err)
		}
		n.TS = fromMS(ts)
		if resp.Valid {
			n.RespondedAt = fromMS(resp.Int64)
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// MarkResponded records that the owner reacted at t to the most recent
// unanswered message sent within window before t. It reports whether a message
// was marked.
func (d *DB) MarkResponded(ctx context.Context, t time.Time, window time.Duration) (bool, error) {
	res, err := d.w.ExecContext(ctx, `
		UPDATE notifications SET responded_at = ?
		WHERE id = (SELECT id FROM notifications
		            WHERE responded_at IS NULL AND ts <= ? AND ts >= ?
		            ORDER BY ts DESC, id DESC LIMIT 1)`,
		ms(t), ms(t), ms(t.Add(-window)))
	if err != nil {
		return false, fmt.Errorf("mark responded: %w", err)
	}
	n, err := res.RowsAffected()
	return n > 0, err
}
