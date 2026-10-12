package app

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Harfull/Kest/internal/clock"
	"github.com/Harfull/Kest/internal/scheduler"
	"github.com/Harfull/Kest/internal/store"
)

const (
	maintenanceJob = "maintenance"
	// backupsToKeep is how many nightly copies are retained.
	backupsToKeep = 7
	// messageRetention is how long raw conversation turns are kept.
	messageRetention = 90 * 24 * time.Hour
)

// maintenance performs the nightly housekeeping: a database backup and pruning
// of old conversation turns. Summaries and aggregates are kept.
type maintenance struct {
	db    *store.DB
	dir   string
	clock clock.Clock
	log   *slog.Logger
	// summarize condenses old conversation turns; optional.
	summarize func(ctx context.Context) (bool, error)
}

func (m *maintenance) run(ctx context.Context, _ scheduler.Job) error {
	backupDir := filepath.Join(m.dir, "backups")
	name := "kest-" + m.clock.Now().Format("20060102") + ".db"
	dest := filepath.Join(backupDir, name)

	// VACUUM INTO refuses to overwrite, and a second run on the same day should
	// replace the first.
	if err := os.Remove(dest); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("replace today's backup: %w", err)
	}
	if err := m.db.Backup(ctx, dest); err != nil {
		return err
	}
	if err := pruneBackups(backupDir, backupsToKeep); err != nil {
		return err
	}

	// Summarize before pruning, so a turn is condensed before it is deleted.
	if m.summarize != nil {
		// A failed summary (a spent budget, say) must not fail the backup.
		if _, err := m.summarize(ctx); err != nil {
			m.log.WarnContext(ctx, "summarize conversation", "err", err)
		}
	}

	n, err := m.db.PruneMessages(ctx, m.clock.Now().Add(-messageRetention))
	if err != nil {
		return err
	}

	m.log.InfoContext(ctx, "maintenance finished", "backup", name, "messages_pruned", n)
	return nil
}

// pruneBackups deletes all but the newest keep files named kest-*.db.
// Names embed the date, so lexical order is chronological.
func pruneBackups(dir string, keep int) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("list backups: %w", err)
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), "kest-") && strings.HasSuffix(e.Name(), ".db") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for len(names) > keep {
		if err := os.Remove(filepath.Join(dir, names[0])); err != nil {
			return fmt.Errorf("remove old backup: %w", err)
		}
		names = names[1:]
	}
	return nil
}
