// Package app wires Kest's components together and owns the process lifecycle.
//
// It is the only package that constructs concrete implementations. Behavior
// that is more than wiring lives in the packages it assembles.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Harfull/Kest/internal/agent"
	"github.com/Harfull/Kest/internal/clock"
	"github.com/Harfull/Kest/internal/config"
	"github.com/Harfull/Kest/internal/llm"
	"github.com/Harfull/Kest/internal/scheduler"
	"github.com/Harfull/Kest/internal/store"
	"github.com/Harfull/Kest/internal/telegram"
	"github.com/Harfull/Kest/internal/version"
)

// ReadyMessage is logged once startup has finished. The Pterodactyl egg
// watches the console for this exact text to mark the server as running, so
// changing it requires updating deploy/pterodactyl.
const ReadyMessage = "kest: ready"

// ShutdownTimeout bounds how long components get to stop after cancellation.
const ShutdownTimeout = 10 * time.Second

// App is the assembled application.
type App struct {
	cfg   config.Config
	log   *slog.Logger
	clock clock.Clock
	loc   *time.Location

	transport telegram.Transport // injected in tests; built from config otherwise
}

// Option customizes an App, mainly for tests.
type Option func(*App)

// WithTransport replaces the Telegram connection.
func WithTransport(t telegram.Transport) Option { return func(a *App) { a.transport = t } }

// WithClock replaces the system clock.
func WithClock(c clock.Clock) Option { return func(a *App) { a.clock = c } }

// New prepares the data directory and returns an App.
func New(cfg config.Config, log *slog.Logger, opts ...Option) (*App, error) {
	if err := os.MkdirAll(cfg.DataDir, 0o750); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	loc, err := time.LoadLocation(cfg.Timezone)
	if err != nil {
		return nil, fmt.Errorf("load timezone %q: %w", cfg.Timezone, err)
	}

	a := &App{cfg: cfg, log: log, clock: clock.Real{}, loc: loc}
	for _, o := range opts {
		o(a)
	}
	return a, nil
}

// Run starts every component and blocks until ctx is cancelled, then stops
// them in reverse order.
func (a *App) Run(ctx context.Context) error {
	a.log.InfoContext(ctx, "starting",
		"version", version.Version,
		"commit", version.Commit,
		"data_dir", a.cfg.DataDir,
		"timezone", a.cfg.Timezone,
		"owners", len(a.cfg.Telegram.OwnerIDs),
	)

	db, err := store.Open(ctx, filepath.Join(a.cfg.DataDir, "kest.db"))
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			a.log.ErrorContext(ctx, "close database", "err", err)
		}
	}()

	client, err := llm.New(llm.Options{
		BaseURL:       a.cfg.LLM.BaseURL,
		APIKey:        a.cfg.LLM.APIKey.Reveal(),
		General:       a.cfg.LLM.Models.General,
		Fallbacks:     a.cfg.LLM.Models.Fallbacks,
		Transcription: a.cfg.LLM.Models.Transcription,
		TTS:           a.cfg.LLM.Models.TTS,
		TTSVoice:      a.cfg.LLM.TTSVoice,
		ZDROnly:       a.cfg.LLM.ZDROnly,
		Referer:       "https://github.com/Harfull/Kest",
		Title:         "Kest",
		HTTP:          &http.Client{Timeout: 90 * time.Second},
		Ledger:        db,
		Clock:         a.clock,
	})
	if err != nil {
		return err
	}
	budget := llm.NewBudget(a.cfg.LLM.DailyBudgetUSD, db, a.clock, a.loc)
	client.SetBudget(budget)

	tr := a.transport
	if tr == nil {
		tr, err = telegram.New(telegram.Options{
			Token:    a.cfg.Telegram.Token.Reveal(),
			OwnerIDs: a.cfg.Telegram.OwnerIDs,
			Log:      a.log,
		})
		if err != nil {
			return err
		}
	}

	sch := scheduler.New(db, a.clock, a.log, scheduler.Options{})
	m := &maintenance{db: db, dir: a.cfg.DataDir, clock: a.clock, log: a.log}
	sch.Register(maintenanceJob, m.run)
	if _, err := sch.Ensure(ctx, scheduler.Spec{
		Owner:   "system",
		Name:    maintenanceJob,
		Rule:    "daily@03:30",
		Zone:    a.cfg.Timezone,
		Misfire: scheduler.MisfireSkip,
	}); err != nil {
		return fmt.Errorf("schedule maintenance: %w", err)
	}

	c := &chat{
		cfg:     a.cfg,
		log:     a.log,
		clock:   a.clock,
		loc:     a.loc,
		tr:      tr,
		agent:   agent.New(client, db, a.clock, a.loc, 0),
		llm:     client,
		db:      db,
		sched:   sch,
		budget:  budget,
		started: a.clock.Now(),
		queue:   make(chan func(context.Context), queueSize),
	}

	if err := tr.SetCommands(ctx, commandMenu); err != nil {
		// The menu is a convenience; the bot works without it.
		a.log.WarnContext(ctx, "could not register the command menu", "err", err)
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var wg sync.WaitGroup
	errs := make(chan error, 3)
	start := func(name string, fn func(context.Context) error) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := fn(runCtx)
			if err == nil || (runCtx.Err() != nil && errors.Is(err, context.Canceled)) {
				return // finished, or stopped because we are shutting down
			}
			errs <- fmt.Errorf("%s: %w", name, err)
			cancel()
		}()
	}
	start("scheduler", sch.Run)
	start("worker", func(ctx context.Context) error { c.work(ctx); return nil })
	start("telegram", func(ctx context.Context) error { return tr.Run(ctx, c.handle) })

	a.log.InfoContext(ctx, ReadyMessage)

	<-runCtx.Done()
	a.log.InfoContext(ctx, "stopping")

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(ShutdownTimeout):
		a.log.WarnContext(ctx, "shutdown timed out; exiting anyway")
	}

	close(errs)
	var failures []error
	for err := range errs {
		failures = append(failures, err)
	}
	return errors.Join(failures...)
}
