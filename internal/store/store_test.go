package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"testing/fstest"
	"time"
)

func openTest(t *testing.T) *DB {
	t.Helper()
	db, err := Open(context.Background(), filepath.Join(t.TempDir(), "kest.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

var t0 = time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)

func TestOpenAppliesMigrationsAndPragmas(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()

	v, err := db.SchemaVersion(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if v < 1 {
		t.Fatalf("SchemaVersion() = %d, want >= 1", v)
	}

	// verifyPragmas already ran in Open; check the one that silently fails.
	var mode string
	if err := db.Read().QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if mode != "wal" {
		t.Fatalf("journal_mode = %q, want wal", mode)
	}
}

func TestReopenKeepsDataAndDoesNotReapplyMigrations(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kest.db")
	ctx := context.Background()

	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SetProfile(ctx, "k", `"v"`, t0); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db, err = Open(ctx, path)
	if err != nil {
		t.Fatalf("second Open() error = %v", err)
	}
	defer db.Close() //nolint:errcheck // test cleanup

	got, err := db.GetProfile(ctx, "k")
	if err != nil || got != `"v"` {
		t.Fatalf("GetProfile() = %q, %v; want %q, nil", got, err, `"v"`)
	}
}

func TestProfile(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()

	if _, err := db.GetProfile(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetProfile(missing) error = %v, want ErrNotFound", err)
	}

	if err := db.SetProfile(ctx, "tz", `"Europe/London"`, t0); err != nil {
		t.Fatal(err)
	}
	if err := db.SetProfile(ctx, "tz", `"Asia/Tokyo"`, t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	got, err := db.GetProfile(ctx, "tz")
	if err != nil || got != `"Asia/Tokyo"` {
		t.Fatalf("GetProfile() = %q, %v; want updated value", got, err)
	}
}

func TestMessagesRecentAndPrune(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()

	for i := range 5 {
		_, err := db.AddMessage(ctx, Message{
			TS:      t0.Add(time.Duration(i) * time.Hour),
			Role:    "user",
			Content: string(rune('a' + i)),
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	got, err := db.RecentMessages(ctx, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].Content != "c" || got[2].Content != "e" {
		t.Fatalf("RecentMessages(3) = %+v, want c,d,e oldest first", got)
	}
	if got[0].Origin != "user" || !got[0].TS.Equal(t0.Add(2*time.Hour)) {
		t.Errorf("defaults/timestamps wrong: %+v", got[0])
	}

	n, err := db.PruneMessages(ctx, t0.Add(2*time.Hour))
	if err != nil || n != 2 {
		t.Fatalf("PruneMessages() = %d, %v; want 2, nil", n, err)
	}
}

func TestMessageRoleConstraint(t *testing.T) {
	db := openTest(t)
	_, err := db.AddMessage(context.Background(), Message{TS: t0, Role: "wizard", Content: "x"})
	if err == nil {
		t.Fatal("AddMessage() accepted an invalid role")
	}
}

func TestUsageSpend(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()

	for _, u := range []Usage{
		{TS: t0.Add(-48 * time.Hour), Slot: "general", Model: "m", CostUSD: 5},
		{TS: t0, Slot: "general", Model: "m", CostUSD: 0.25},
		{TS: t0.Add(time.Minute), Slot: "tts", Model: "m", CostUSD: 0.5},
	} {
		if err := db.AddUsage(ctx, u); err != nil {
			t.Fatal(err)
		}
	}

	got, err := db.SpendSince(ctx, t0)
	if err != nil {
		t.Fatal(err)
	}
	if got != 0.75 {
		t.Fatalf("SpendSince() = %v, want 0.75", got)
	}

	none, err := db.SpendSince(ctx, t0.Add(24*time.Hour))
	if err != nil || none != 0 {
		t.Fatalf("SpendSince(future) = %v, %v; want 0, nil", none, err)
	}
}

func TestAudit(t *testing.T) {
	db := openTest(t)
	err := db.AddAudit(context.Background(), AuditEntry{
		TS: t0, Actor: "owner", Action: "gmail.send", Risk: "destructive", Confirmed: true, Status: "ok",
	})
	if err != nil {
		t.Fatalf("AddAudit() error = %v", err)
	}
}

func TestTxRollsBackOnError(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()
	boom := errors.New("boom")

	err := db.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "INSERT INTO profile (key, value, updated_at) VALUES ('a', '1', 0)"); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("Tx() error = %v, want boom", err)
	}
	if _, err := db.GetProfile(ctx, "a"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("row survived rollback: %v", err)
	}
}

// TestConcurrentAccess guards against the SQLITE_BUSY failure reported for the
// pure-Go driver when many goroutines read and write at once.
func TestConcurrentAccess(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()

	var wg sync.WaitGroup
	errs := make(chan error, 64)

	for i := range 8 {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for j := range 20 {
				if _, err := db.AddMessage(ctx, Message{TS: t0, Role: "user", Content: string(rune('a' + (i+j)%26))}); err != nil {
					errs <- err
					return
				}
			}
		}()
		go func() {
			defer wg.Done()
			for range 20 {
				if _, err := db.RecentMessages(ctx, 10); err != nil {
					errs <- err
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		t.Errorf("concurrent access error: %v", err)
	}
}

func TestBackup(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()
	if err := db.SetProfile(ctx, "k", `1`, t0); err != nil {
		t.Fatal(err)
	}

	dest := filepath.Join(t.TempDir(), "backups", "copy.db")
	if err := db.Backup(ctx, dest); err != nil {
		t.Fatalf("Backup() error = %v", err)
	}

	copyDB, err := Open(ctx, dest)
	if err != nil {
		t.Fatalf("open backup: %v", err)
	}
	defer copyDB.Close() //nolint:errcheck // test cleanup
	if got, err := copyDB.GetProfile(ctx, "k"); err != nil || got != "1" {
		t.Fatalf("backup content = %q, %v", got, err)
	}
}

func TestLoadMigrationsValidation(t *testing.T) {
	file := func(name string) *fstest.MapFile { return &fstest.MapFile{Data: []byte("SELECT 1;")} }

	tests := []struct {
		name    string
		files   []string
		wantErr bool
	}{
		{"contiguous", []string{"0001_a.sql", "0002_b.sql"}, false},
		{"gap", []string{"0001_a.sql", "0003_c.sql"}, true},
		{"starts at two", []string{"0002_a.sql"}, true},
		{"bad name", []string{"init.sql"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fsys := fstest.MapFS{}
			for _, f := range tt.files {
				fsys["migrations/"+f] = file(f)
			}
			_, err := loadMigrations(fsys)
			if (err != nil) != tt.wantErr {
				t.Fatalf("loadMigrations() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestRealMigrationsAreValid(t *testing.T) {
	if _, err := loadMigrations(migrationFS); err != nil {
		t.Fatal(err)
	}
}
