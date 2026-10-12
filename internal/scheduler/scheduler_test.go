package scheduler

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Harfull/Kest/internal/clock"
	"github.com/Harfull/Kest/internal/store"
)

type rig struct {
	db  *store.DB
	clk *clock.Fake
	s   *Scheduler
}

func newRig(t *testing.T, start time.Time) *rig {
	t.Helper()
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "kest.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	clk := clock.NewFake(start)
	return &rig{db: db, clk: clk, s: New(db, clk, slog.New(slog.DiscardHandler), Options{})}
}

// tick runs one scheduling pass and waits for handlers to finish.
func (r *rig) tick(t *testing.T) {
	t.Helper()
	if err := r.s.Tick(context.Background()); err != nil {
		t.Fatalf("Tick() error = %v", err)
	}
	r.s.Wait()
}

var base = time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)

func TestOneOffRunsOnceWhenDue(t *testing.T) {
	r := newRig(t, base)
	var runs atomic.Int32
	r.s.Register("ping", func(context.Context, Job) error { runs.Add(1); return nil })

	id, err := r.s.Schedule(context.Background(), Spec{Owner: "system", Name: "ping", At: base.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}

	r.tick(t)
	if runs.Load() != 0 {
		t.Fatal("job ran before it was due")
	}

	r.clk.Advance(time.Hour)
	r.tick(t)
	r.tick(t) // a second pass must not run it again
	if runs.Load() != 1 {
		t.Fatalf("runs = %d, want 1", runs.Load())
	}

	j, err := r.s.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if j.State != StateDone || j.RunCount != 1 {
		t.Errorf("job = %+v, want done with 1 run", j)
	}
}

func TestPayloadReachesHandler(t *testing.T) {
	r := newRig(t, base)
	got := make(chan Job, 1)
	r.s.Register("pay", func(_ context.Context, j Job) error { got <- j; return nil })

	_, err := r.s.Schedule(context.Background(), Spec{
		Owner: "coach", Name: "pay", Payload: `{"habit":"walk"}`, At: base,
	})
	if err != nil {
		t.Fatal(err)
	}
	r.tick(t)

	j := <-got
	if j.Payload != `{"habit":"walk"}` || j.Owner != "coach" {
		t.Errorf("job = %+v", j)
	}
}

func TestRecurringReschedulesWithoutDrift(t *testing.T) {
	r := newRig(t, base)
	r.s.Register("tick", func(context.Context, Job) error { return nil })

	id, err := r.s.Schedule(context.Background(), Spec{
		Owner: "system", Name: "tick", Rule: "every:10m", At: base.Add(10 * time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}

	// The scheduler wakes 3 seconds late; the next run must still land on the grid.
	r.clk.Set(base.Add(10*time.Minute + 3*time.Second))
	r.tick(t)

	j, _ := r.s.Get(context.Background(), id)
	if want := base.Add(20 * time.Minute); !j.NextRunAt.Equal(want) {
		t.Errorf("NextRunAt = %v, want %v", j.NextRunAt, want)
	}
	if j.State != StateScheduled {
		t.Errorf("state = %s, want scheduled", j.State)
	}
}

func TestDailyRuleUsesJobZone(t *testing.T) {
	r := newRig(t, base) // 12:00 UTC = 08:00 in New York (EDT)
	r.s.Register("morning", func(context.Context, Job) error { return nil })

	id, err := r.s.Schedule(context.Background(), Spec{
		Owner: "coach", Name: "morning", Rule: "daily@07:30", Zone: "America/New_York",
	})
	if err != nil {
		t.Fatal(err)
	}
	j, _ := r.s.Get(context.Background(), id)

	// 07:30 EDT has passed today, so tomorrow at 11:30 UTC.
	if want := time.Date(2026, 10, 12, 11, 30, 0, 0, time.UTC); !j.NextRunAt.Equal(want) {
		t.Errorf("NextRunAt = %v, want %v", j.NextRunAt, want)
	}
}

func TestMisfirePolicies(t *testing.T) {
	tests := []struct {
		name    string
		misfire Misfire
		lateBy  time.Duration
		wantRun bool
	}{
		{"run_once runs however late", MisfireRunOnce, 72 * time.Hour, true},
		{"skip drops late run", MisfireSkip, 10 * time.Minute, false},
		{"skip allows on-time run", MisfireSkip, 5 * time.Second, true},
		{"expire_after within grace", MisfireExpireAfter(30 * time.Minute), 20 * time.Minute, true},
		{"expire_after past grace", MisfireExpireAfter(30 * time.Minute), 45 * time.Minute, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newRig(t, base)
			var runs atomic.Int32
			r.s.Register("h", func(context.Context, Job) error { runs.Add(1); return nil })

			due := base
			if _, err := r.s.Schedule(context.Background(), Spec{
				Owner: "system", Name: "h", At: due, Misfire: tt.misfire,
			}); err != nil {
				t.Fatal(err)
			}

			r.clk.Set(due.Add(tt.lateBy))
			r.tick(t)

			if got := runs.Load() == 1; got != tt.wantRun {
				t.Errorf("ran = %v, want %v", got, tt.wantRun)
			}
		})
	}
}

func TestRecurringDoesNotReplayMissedRuns(t *testing.T) {
	r := newRig(t, base)
	var runs atomic.Int32
	r.s.Register("tick", func(context.Context, Job) error { runs.Add(1); return nil })

	id, err := r.s.Schedule(context.Background(), Spec{
		Owner: "system", Name: "tick", Rule: "every:1h", At: base, Misfire: MisfireRunOnce,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Down for 5 days and 30 minutes.
	r.clk.Set(base.Add(5*24*time.Hour + 30*time.Minute))
	r.tick(t)
	r.tick(t)

	if runs.Load() != 1 {
		t.Fatalf("runs = %d, want exactly one catch-up run", runs.Load())
	}
	j, _ := r.s.Get(context.Background(), id)
	// Now is 12:30 five days later; the next hourly grid point is 13:00.
	if want := base.Add(5*24*time.Hour + time.Hour); !j.NextRunAt.Equal(want) {
		t.Errorf("NextRunAt = %v, want %v (next grid point after now)", j.NextRunAt, want)
	}
}

func TestFailureIsRecorded(t *testing.T) {
	r := newRig(t, base)
	r.s.Register("bad", func(context.Context, Job) error { return errors.New("boom") })

	oneOff, _ := r.s.Schedule(context.Background(), Spec{Owner: "system", Name: "bad", At: base})
	recurring, _ := r.s.Schedule(context.Background(), Spec{Owner: "system", Name: "bad", Rule: "every:1h", At: base})
	r.tick(t)

	j, _ := r.s.Get(context.Background(), oneOff)
	if j.State != StateFailed || j.LastError != "boom" {
		t.Errorf("one-off = %+v, want failed with error", j)
	}
	j, _ = r.s.Get(context.Background(), recurring)
	if j.State != StateScheduled || j.LastError != "boom" {
		t.Errorf("recurring = %+v, want still scheduled with error noted", j)
	}
}

func TestPanicIsContained(t *testing.T) {
	r := newRig(t, base)
	r.s.Register("panics", func(context.Context, Job) error { panic("kaboom") })

	id, _ := r.s.Schedule(context.Background(), Spec{Owner: "system", Name: "panics", At: base})
	r.tick(t)

	j, _ := r.s.Get(context.Background(), id)
	if j.State != StateFailed || j.LastError == "" {
		t.Errorf("job = %+v, want failed with panic recorded", j)
	}
}

func TestMissingHandlerFailsJob(t *testing.T) {
	r := newRig(t, base)
	id, _ := r.s.Schedule(context.Background(), Spec{Owner: "system", Name: "nobody", At: base})
	r.tick(t)

	j, _ := r.s.Get(context.Background(), id)
	if j.State != StateFailed {
		t.Errorf("state = %s, want failed", j.State)
	}
}

func TestPauseResumeCancel(t *testing.T) {
	r := newRig(t, base)
	var runs atomic.Int32
	r.s.Register("h", func(context.Context, Job) error { runs.Add(1); return nil })
	ctx := context.Background()

	id, _ := r.s.Schedule(ctx, Spec{Owner: "system", Name: "h", Rule: "every:1h", At: base.Add(time.Minute)})

	if err := r.s.Pause(ctx, id); err != nil {
		t.Fatal(err)
	}
	r.clk.Advance(time.Hour)
	r.tick(t)
	if runs.Load() != 0 {
		t.Fatal("paused job ran")
	}

	if err := r.s.Resume(ctx, id); err != nil {
		t.Fatal(err)
	}
	r.tick(t)
	if runs.Load() != 1 {
		t.Fatalf("runs after resume = %d, want 1", runs.Load())
	}

	if err := r.s.Cancel(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := r.s.Get(ctx, id); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("Get after cancel error = %v, want ErrNotFound", err)
	}
	if err := r.s.Cancel(ctx, id); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("second Cancel error = %v, want ErrNotFound", err)
	}
}

func TestSurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kest.db")
	ctx := context.Background()
	clk := clock.NewFake(base)

	db1, err := store.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	s1 := New(db1, clk, slog.New(slog.DiscardHandler), Options{})
	id, err := s1.Schedule(ctx, Spec{Owner: "system", Name: "h", At: base.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a crash mid-run: the job is left in the running state.
	if _, err := db1.Write().ExecContext(ctx, "UPDATE jobs SET state='running' WHERE id=?", id); err != nil {
		t.Fatal(err)
	}
	if err := db1.Close(); err != nil {
		t.Fatal(err)
	}

	db2, err := store.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close() //nolint:errcheck // test cleanup
	s2 := New(db2, clk, slog.New(slog.DiscardHandler), Options{})
	var runs atomic.Int32
	s2.Register("h", func(context.Context, Job) error { runs.Add(1); return nil })

	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { _ = s2.Run(runCtx); close(done) }()

	// Let Run recover the interrupted job, then make it due.
	waitFor(t, func() bool {
		j, err := s2.Get(ctx, id)
		return err == nil && j.State == StateScheduled
	})
	clk.Advance(2 * time.Hour)
	s2.poke()
	waitFor(t, func() bool { return runs.Load() == 1 })

	cancel()
	<-done
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not reached in time")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestScheduleValidation(t *testing.T) {
	r := newRig(t, base)
	ctx := context.Background()

	tests := []struct {
		name string
		spec Spec
	}{
		{"no name", Spec{Owner: "system", At: base}},
		{"no owner", Spec{Name: "h", At: base}},
		{"one-off without time", Spec{Owner: "system", Name: "h"}},
		{"bad rule", Spec{Owner: "system", Name: "h", Rule: "sometimes"}},
		{"bad zone", Spec{Owner: "system", Name: "h", Rule: "daily@07:00", Zone: "Mars/Base"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := r.s.Schedule(ctx, tt.spec); err == nil {
				t.Fatal("Schedule() succeeded, want error")
			}
		})
	}
}

func TestUpcomingOrder(t *testing.T) {
	r := newRig(t, base)
	ctx := context.Background()

	for _, d := range []time.Duration{3 * time.Hour, time.Hour, 2 * time.Hour} {
		if _, err := r.s.Schedule(ctx, Spec{Owner: "system", Name: "h", At: base.Add(d)}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := r.s.Upcoming(ctx, 2)
	if err != nil || len(got) != 2 {
		t.Fatalf("Upcoming() = %v, %v", got, err)
	}
	if !got[0].NextRunAt.Equal(base.Add(time.Hour)) || !got[1].NextRunAt.Equal(base.Add(2*time.Hour)) {
		t.Errorf("wrong order: %v, %v", got[0].NextRunAt, got[1].NextRunAt)
	}
}

func TestConcurrentHandlersAreBounded(t *testing.T) {
	r := newRig(t, base)
	r.s = New(r.db, r.clk, slog.New(slog.DiscardHandler), Options{Workers: 2})

	var cur, peak atomic.Int32
	var mu sync.Mutex
	r.s.Register("slow", func(context.Context, Job) error {
		n := cur.Add(1)
		mu.Lock()
		if n > peak.Load() {
			peak.Store(n)
		}
		mu.Unlock()
		time.Sleep(20 * time.Millisecond)
		cur.Add(-1)
		return nil
	})

	for range 6 {
		if _, err := r.s.Schedule(context.Background(), Spec{Owner: "system", Name: "slow", At: base}); err != nil {
			t.Fatal(err)
		}
	}
	r.tick(t)

	if peak.Load() > 2 {
		t.Errorf("peak concurrency = %d, want <= 2", peak.Load())
	}
}

func TestEnsureIsIdempotent(t *testing.T) {
	r := newRig(t, base)
	ctx := context.Background()
	spec := Spec{Owner: "system", Name: "backup", Rule: "daily@03:30"}

	created, err := r.s.Ensure(ctx, spec)
	if err != nil || !created {
		t.Fatalf("first Ensure() = %v, %v; want true, nil", created, err)
	}
	created, err = r.s.Ensure(ctx, spec)
	if err != nil || created {
		t.Fatalf("second Ensure() = %v, %v; want false, nil", created, err)
	}

	jobs, _ := r.s.Upcoming(ctx, 10)
	if len(jobs) != 1 {
		t.Errorf("jobs = %d, want 1", len(jobs))
	}
}

func TestListOwned(t *testing.T) {
	r := newRig(t, base)
	ctx := context.Background()
	for _, spec := range []Spec{
		{Owner: "plugin:a", Name: "a/x", At: base.Add(2 * time.Hour)},
		{Owner: "plugin:a", Name: "a/y", At: base.Add(time.Hour)},
		{Owner: "plugin:b", Name: "b/x", At: base.Add(time.Minute)},
	} {
		if _, err := r.s.Schedule(ctx, spec); err != nil {
			t.Fatal(err)
		}
	}

	got, err := r.s.ListOwned(ctx, "plugin:a")
	if err != nil || len(got) != 2 || got[0].Name != "a/y" || got[1].Name != "a/x" {
		t.Errorf("ListOwned(a) = %+v, %v; want a's two jobs in run order and none of b's", got, err)
	}
	if none, _ := r.s.ListOwned(ctx, "plugin:zzz"); len(none) != 0 {
		t.Errorf("ListOwned(unknown) = %v", none)
	}
}
