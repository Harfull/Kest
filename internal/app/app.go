// Package app wires Kest's components together and owns the process lifecycle.
//
// It is the only package that constructs concrete implementations. It does not
// contain business logic.
package app

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/Harfull/Kest/internal/config"
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
	cfg config.Config
	log *slog.Logger
}

// New prepares the data directory and returns an App.
func New(cfg config.Config, log *slog.Logger) (*App, error) {
	if err := os.MkdirAll(cfg.DataDir, 0o750); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	return &App{cfg: cfg, log: log}, nil
}

// Run starts every component and blocks until ctx is cancelled.
//
// Components are added here as the roadmap progresses; see docs/roadmap.md.
func (a *App) Run(ctx context.Context) error {
	a.log.InfoContext(ctx, "starting",
		"version", version.Version,
		"commit", version.Commit,
		"data_dir", a.cfg.DataDir,
		"timezone", a.cfg.Timezone,
		"owners", len(a.cfg.Telegram.OwnerIDs),
	)

	// Phase 1 starts the store, scheduler, LLM client and Telegram transport here.

	a.log.InfoContext(ctx, ReadyMessage)

	<-ctx.Done()
	// ctx is already cancelled here; the logger ignores it, and sloglint wants one passed.
	a.log.InfoContext(ctx, "stopping")
	return nil
}
