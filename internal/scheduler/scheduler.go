// Package scheduler runs Kest's persistent jobs: reminders, coaching slots and
// plugin work.
//
// Jobs live in SQLite so they survive restarts. Recurrence is evaluated in the
// job's own time zone with explicit daylight saving rules, missed runs follow a
// per-job policy, and time comes from an injected clock. See
// docs/architecture/scheduler.md.
//
// The scheduler decides when a handler runs. Whether a coaching message should
// actually be sent (quiet hours, budget) is the handler's decision.
package scheduler

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/Harfull/Kest/internal/clock"
	"github.com/Harfull/Kest/internal/store"
)

const (
	// maxIdle bounds how long the loop sleeps when nothing is due, as a safety
	// net against missed wake-ups.
	maxIdle = time.Minute
	// jobTimeout bounds one handler run.
	jobTimeout = 2 * time.Minute
	// maxCatchUpSteps bounds the loop that skips missed occurrences.
	maxCatchUpSteps = 1000
)

// Handler runs a job. A returned error is recorded; recurring jobs keep their
// schedule regardless.
type Handler func(ctx context.Context, job Job) error

// Scheduler persists and runs jobs. Create one with New.
type Scheduler struct {
	db    *store.DB
	clock clock.Clock
	log   *slog.Logger

	mu       sync.RWMutex
	handlers map[string]Handler

	sem  chan struct{}  // bounds concurrent handlers
	wg   sync.WaitGroup // tracks running handlers
	wake chan struct{}  // signals that the schedule changed

	zones sync.Map // name -> *time.Location
}

// Options configures a Scheduler.
type Options struct {
	Workers int // concurrent handlers; defaults to 4
}

// New returns a Scheduler.
func New(db *store.DB, clk clock.Clock, log *slog.Logger, opts Options) *Scheduler {
	if opts.Workers <= 0 {
		opts.Workers = 4
	}
	return &Scheduler{
		db:       db,
		clock:    clk,
		log:      log,
		handlers: make(map[string]Handler),
		sem:      make(chan struct{}, opts.Workers),
		wake:     make(chan struct{}, 1),
	}
}

// Register associates a handler with a job name. Register before Run.
func (s *Scheduler) Register(name string, h Handler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.handlers[name] = h
}

func (s *Scheduler) handler(name string) (Handler, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	h, ok := s.handlers[name]
	return h, ok
}

func (s *Scheduler) location(name string) (*time.Location, error) {
	if name == "" {
		return time.UTC, nil
	}
	if v, ok := s.zones.Load(name); ok {
		return v.(*time.Location), nil
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, fmt.Errorf("load time zone %q: %w", name, err)
	}
	s.zones.Store(name, loc)
	return loc, nil
}

// Schedule validates spec, stores the job and returns its ID.
func (s *Scheduler) Schedule(ctx context.Context, spec Spec) (string, error) {
	if spec.Name == "" || spec.Owner == "" {
		return "", errors.New("scheduler: job needs an owner and a name")
	}
	if spec.Zone == "" {
		spec.Zone = "UTC"
	}
	if spec.Payload == "" {
		spec.Payload = "{}"
	}
	if spec.Misfire.kind == "" {
		spec.Misfire = MisfireRunOnce
	}
	loc, err := s.location(spec.Zone)
	if err != nil {
		return "", err
	}

	now := s.clock.Now()
	next := spec.At
	if spec.Rule == "" {
		if next.IsZero() {
			return "", errors.New("scheduler: one-off job needs a run time")
		}
	} else {
		rule, err := ParseRule(spec.Rule)
		if err != nil {
			return "", err
		}
		if next.IsZero() {
			if next, err = rule.Next(now, loc); err != nil {
				return "", err
			}
		}
	}

	id, err := newID()
	if err != nil {
		return "", err
	}
	_, err = s.db.Write().ExecContext(ctx, `
		INSERT INTO jobs (id, owner, name, payload, rule, zone, next_run_at, misfire, state, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'scheduled', ?)`,
		id, spec.Owner, spec.Name, spec.Payload, spec.Rule, spec.Zone,
		next.UnixMilli(), spec.Misfire.String(), now.UnixMilli())
	if err != nil {
		return "", fmt.Errorf("store job: %w", err)
	}
	s.poke()
	return id, nil
}

// Cancel deletes a job.
func (s *Scheduler) Cancel(ctx context.Context, id string) error {
	res, err := s.db.Write().ExecContext(ctx, "DELETE FROM jobs WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("cancel job: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return store.ErrNotFound
	}
	s.poke()
	return nil
}

// Pause stops a job from running until Resume is called.
func (s *Scheduler) Pause(ctx context.Context, id string) error {
	return s.setState(ctx, id, StatePaused, StateScheduled)
}

// Resume re-enables a paused job. A recurring job continues from its next
// future occurrence; a one-off job that is overdue is handled by its misfire
// policy.
func (s *Scheduler) Resume(ctx context.Context, id string) error {
	if err := s.setState(ctx, id, StateScheduled, StatePaused); err != nil {
		return err
	}
	s.poke()
	return nil
}

func (s *Scheduler) setState(ctx context.Context, id string, to, from State) error {
	res, err := s.db.Write().ExecContext(ctx,
		"UPDATE jobs SET state = ? WHERE id = ? AND state = ?", string(to), id, string(from))
	if err != nil {
		return fmt.Errorf("set job state: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("job %s is not %s: %w", id, from, store.ErrNotFound)
	}
	return nil
}

// Get returns one job.
func (s *Scheduler) Get(ctx context.Context, id string) (Job, error) {
	row := s.db.Read().QueryRowContext(ctx, jobSelect+" WHERE id = ?", id)
	j, err := scanJob(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, store.ErrNotFound
	}
	return j, err
}

// Upcoming returns up to n scheduled jobs in run order.
func (s *Scheduler) Upcoming(ctx context.Context, n int) ([]Job, error) {
	rows, err := s.db.Read().QueryContext(ctx,
		jobSelect+" WHERE state = 'scheduled' ORDER BY next_run_at LIMIT ?", n)
	if err != nil {
		return nil, fmt.Errorf("list upcoming jobs: %w", err)
	}
	defer rows.Close() //nolint:errcheck // surfaced via rows.Err

	var out []Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// Run processes jobs until ctx is cancelled, then waits for running handlers.
func (s *Scheduler) Run(ctx context.Context) error {
	if err := s.recoverInterrupted(ctx); err != nil {
		if ctx.Err() != nil {
			return nil // shut down before starting: not a failure
		}
		return err
	}

	for {
		if err := s.Tick(ctx); err != nil && ctx.Err() == nil {
			s.log.ErrorContext(ctx, "scheduler tick failed", "err", err)
		}

		wait, err := s.untilNext(ctx)
		if err != nil {
			wait = maxIdle
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			s.wg.Wait()
			return nil
		case <-s.wake:
			timer.Stop()
		case <-timer.C:
		}
	}
}

// Wait blocks until all dispatched handlers have finished. Tests and shutdown
// use it.
func (s *Scheduler) Wait() { s.wg.Wait() }

// Tick claims every due job and dispatches it. It returns without waiting for
// handlers; use Wait for that.
func (s *Scheduler) Tick(ctx context.Context) error {
	due, err := s.claimDue(ctx)
	if err != nil {
		return err
	}
	for _, j := range due {
		select {
		case s.sem <- struct{}{}:
		case <-ctx.Done():
			return ctx.Err()
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			defer func() { <-s.sem }()
			s.execute(ctx, j)
		}()
	}
	return nil
}

// claimDue marks due jobs as running and returns them.
func (s *Scheduler) claimDue(ctx context.Context) ([]Job, error) {
	now := s.clock.Now()
	var claimed []Job

	err := s.db.Tx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx,
			jobSelect+" WHERE state = 'scheduled' AND next_run_at <= ? ORDER BY next_run_at", now.UnixMilli())
		if err != nil {
			return err
		}
		var jobs []Job
		for rows.Next() {
			j, err := scanJob(rows)
			if err != nil {
				_ = rows.Close()
				return err
			}
			jobs = append(jobs, j)
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return err
		}

		for _, j := range jobs {
			if _, err := tx.ExecContext(ctx, "UPDATE jobs SET state = 'running' WHERE id = ?", j.ID); err != nil {
				return err
			}
			j.State = StateRunning
			claimed = append(claimed, j)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("claim due jobs: %w", err)
	}
	return claimed, nil
}

// execute applies the misfire policy, runs the handler, and stores the outcome
// together with the next run time.
func (s *Scheduler) execute(ctx context.Context, j Job) {
	now := s.clock.Now()
	lateBy := now.Sub(j.NextRunAt)

	var runErr error
	ran := false
	status := "ok"

	switch h, ok := s.handler(j.Name); {
	case !j.Misfire.shouldRun(lateBy):
		status = "skipped"
		s.log.InfoContext(ctx, "skipping overdue job", "job", j.Name, "late_by", lateBy.Round(time.Second).String())
	case !ok:
		status = "failed"
		runErr = fmt.Errorf("no handler registered for %q", j.Name)
	default:
		ran = true
		runErr = s.runHandler(ctx, h, j)
		if runErr != nil {
			status = "failed"
		}
	}

	if runErr != nil {
		s.log.ErrorContext(ctx, "job failed", "job", j.Name, "id", j.ID, "err", runErr)
	}
	s.finish(ctx, j, now, ran, status, runErr)
}

func (s *Scheduler) runHandler(ctx context.Context, h Handler, j Job) (err error) {
	ctx, cancel := context.WithTimeout(ctx, jobTimeout)
	defer cancel()
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("handler panicked: %v", p)
		}
	}()
	return h(ctx, j)
}

// finish records the run and computes the next occurrence.
func (s *Scheduler) finish(ctx context.Context, j Job, started time.Time, ran bool, status string, runErr error) {
	// Persist even if the run context was cancelled during shutdown.
	ctx = context.WithoutCancel(ctx)

	errText := ""
	if runErr != nil {
		errText = runErr.Error()
	}

	state, next := StateDone, j.NextRunAt
	if j.Rule != "" {
		state = StateScheduled
		var err error
		if next, err = s.nextAfterRun(j, s.clock.Now()); err != nil {
			s.log.ErrorContext(ctx, "cannot compute next run; pausing job", "job", j.Name, "id", j.ID, "err", err)
			state = StatePaused
		}
	} else if runErr != nil {
		state = StateFailed
	}

	err := s.db.Tx(ctx, func(tx *sql.Tx) error {
		lastRun := any(nil)
		if ran {
			lastRun = started.UnixMilli()
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE jobs SET state = ?, next_run_at = ?, last_error = ?,
			    last_run_at = COALESCE(?, last_run_at), run_count = run_count + ?
			WHERE id = ?`,
			string(state), next.UnixMilli(), errText, lastRun, boolInt(ran), j.ID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `
			INSERT INTO job_runs (job_id, started_at, finished_at, status, error)
			VALUES (?, ?, ?, ?, ?)`,
			j.ID, started.UnixMilli(), s.clock.Now().UnixMilli(), status, errText)
		return err
	})
	if err != nil {
		s.log.ErrorContext(ctx, "store job result", "job", j.Name, "id", j.ID, "err", err)
	}
	s.poke()
}

// nextAfterRun returns the next occurrence strictly after now, derived from the
// scheduled time so intervals do not drift, but never replaying missed runs.
func (s *Scheduler) nextAfterRun(j Job, now time.Time) (time.Time, error) {
	rule, err := ParseRule(j.Rule)
	if err != nil {
		return time.Time{}, err
	}
	loc, err := s.location(j.Zone)
	if err != nil {
		return time.Time{}, err
	}

	next := j.NextRunAt
	for i := 0; i < maxCatchUpSteps; i++ {
		if next, err = rule.Next(next, loc); err != nil {
			return time.Time{}, err
		}
		if next.After(now) {
			return next, nil
		}
	}
	// Down for a very long time relative to the interval: restart from now.
	return rule.Next(now, loc)
}

// recoverInterrupted returns jobs left "running" by a crash to the schedule.
// Their next_run_at is in the past, so the misfire policy decides what happens.
func (s *Scheduler) recoverInterrupted(ctx context.Context) error {
	res, err := s.db.Write().ExecContext(ctx, "UPDATE jobs SET state = 'scheduled' WHERE state = 'running'")
	if err != nil {
		return fmt.Errorf("recover interrupted jobs: %w", err)
	}
	if n, _ := res.RowsAffected(); n > 0 {
		s.log.WarnContext(ctx, "recovered jobs interrupted by a previous shutdown", "count", n)
	}
	return nil
}

// untilNext returns how long to sleep before the earliest scheduled job.
func (s *Scheduler) untilNext(ctx context.Context) (time.Duration, error) {
	var next sql.NullInt64
	err := s.db.Read().QueryRowContext(ctx,
		"SELECT MIN(next_run_at) FROM jobs WHERE state = 'scheduled'").Scan(&next)
	if err != nil {
		return 0, err
	}
	if !next.Valid {
		return maxIdle, nil
	}
	d := time.UnixMilli(next.Int64).Sub(s.clock.Now())
	return min(max(d, 0), maxIdle), nil
}

// poke wakes the run loop so it re-reads the schedule.
func (s *Scheduler) poke() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

const jobSelect = `SELECT id, owner, name, payload, rule, zone, next_run_at, misfire, state,
	last_run_at, run_count, last_error, created_at FROM jobs`

type scanner interface{ Scan(dest ...any) error }

func scanJob(r scanner) (Job, error) {
	var (
		j       Job
		next    int64
		lastRun sql.NullInt64
		created int64
		misfire string
		state   string
	)
	if err := r.Scan(&j.ID, &j.Owner, &j.Name, &j.Payload, &j.Rule, &j.Zone, &next, &misfire, &state,
		&lastRun, &j.RunCount, &j.LastError, &created); err != nil {
		return Job{}, err
	}
	j.NextRunAt = time.UnixMilli(next).UTC()
	j.CreatedAt = time.UnixMilli(created).UTC()
	if lastRun.Valid {
		j.LastRunAt = time.UnixMilli(lastRun.Int64).UTC()
	}
	j.State = State(state)

	m, err := ParseMisfire(misfire)
	if err != nil {
		return Job{}, err
	}
	j.Misfire = m
	return j, nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func newID() (string, error) {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate job id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// Ensure schedules spec unless a job with the same owner and name already
// exists in any state. It makes startup registration of built-in jobs
// idempotent, and reports whether a job was created.
func (s *Scheduler) Ensure(ctx context.Context, spec Spec) (bool, error) {
	var n int
	err := s.db.Read().QueryRowContext(ctx,
		"SELECT COUNT(*) FROM jobs WHERE owner = ? AND name = ?", spec.Owner, spec.Name).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("look up job %s/%s: %w", spec.Owner, spec.Name, err)
	}
	if n > 0 {
		return false, nil
	}
	if _, err := s.Schedule(ctx, spec); err != nil {
		return false, err
	}
	return true, nil
}
