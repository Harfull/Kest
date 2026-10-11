// Package store owns Kest's SQLite database: opening it safely, applying
// migrations and exposing repositories.
//
// It is the only package that knows the database driver. It does not contain
// business rules; those live in the packages that use the repositories.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite" // registers the pure-Go "sqlite" driver
)

// busyTimeoutMS is how long a connection waits for a lock before failing.
const busyTimeoutMS = 5000

// ErrNotFound is returned when a requested row does not exist.
var ErrNotFound = errors.New("not found")

// DB is the application database: one serialized writer and a small read pool.
//
// SQLite in WAL mode allows readers during a write, but concurrent writers
// contend for the lock. Funnelling every write through a single connection
// removes SQLITE_BUSY from the picture; see docs/decisions/0003-sqlite-modernc.md.
type DB struct {
	w *sql.DB
	r *sql.DB
}

// Open opens (creating if needed) the database at path, verifies that the
// required pragmas took effect, and applies pending migrations.
func Open(ctx context.Context, path string) (*DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, fmt.Errorf("create database directory: %w", err)
	}

	w, err := sql.Open("sqlite", dsn(path, true))
	if err != nil {
		return nil, fmt.Errorf("open write connection: %w", err)
	}
	w.SetMaxOpenConns(1)

	// The write pool must exist before the read pool so the file is created.
	if err := w.PingContext(ctx); err != nil {
		_ = w.Close()
		return nil, fmt.Errorf("connect to database: %w", err)
	}

	r, err := sql.Open("sqlite", dsn(path, false))
	if err != nil {
		_ = w.Close()
		return nil, fmt.Errorf("open read pool: %w", err)
	}
	r.SetMaxOpenConns(4)

	db := &DB{w: w, r: r}

	if err := db.verifyPragmas(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := db.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

// dsn builds a connection string. The modernc driver silently ignores unknown
// parameters, so every option uses the _pragma form it understands and
// verifyPragmas checks the result.
func dsn(path string, writer bool) string {
	q := url.Values{}
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", fmt.Sprintf("busy_timeout(%d)", busyTimeoutMS))
	q.Add("_pragma", "foreign_keys(ON)")
	q.Add("_pragma", "synchronous(NORMAL)")
	if writer {
		q.Set("_txlock", "immediate")
	}
	return (&url.URL{Scheme: "file", Opaque: filepath.ToSlash(path), RawQuery: q.Encode()}).String()
}

// verifyPragmas fails startup if a safety setting did not apply.
func (d *DB) verifyPragmas(ctx context.Context) error {
	for name, pool := range map[string]*sql.DB{"write": d.w, "read": d.r} {
		var mode string
		if err := pool.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode); err != nil {
			return fmt.Errorf("check journal_mode on %s pool: %w", name, err)
		}
		if mode != "wal" {
			return fmt.Errorf("%s pool journal_mode = %q, want wal", name, mode)
		}

		var fk int
		if err := pool.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&fk); err != nil {
			return fmt.Errorf("check foreign_keys on %s pool: %w", name, err)
		}
		if fk != 1 {
			return fmt.Errorf("%s pool foreign_keys = %d, want 1", name, fk)
		}

		var busy int
		if err := pool.QueryRowContext(ctx, "PRAGMA busy_timeout").Scan(&busy); err != nil {
			return fmt.Errorf("check busy_timeout on %s pool: %w", name, err)
		}
		if busy != busyTimeoutMS {
			return fmt.Errorf("%s pool busy_timeout = %d, want %d", name, busy, busyTimeoutMS)
		}
	}
	return nil
}

// Write returns the serialized write handle. Use it for every INSERT, UPDATE,
// DELETE and for transactions.
func (d *DB) Write() *sql.DB { return d.w }

// Read returns the read pool. Use it for queries that do not write.
func (d *DB) Read() *sql.DB { return d.r }

// Close closes both pools.
func (d *DB) Close() error {
	return errors.Join(d.r.Close(), d.w.Close())
}

// Tx runs fn in a write transaction, committing if it returns nil.
func (d *DB) Tx(ctx context.Context, fn func(*sql.Tx) error) (err error) {
	tx, err := d.w.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p)
		}
	}()

	if err := fn(tx); err != nil {
		if rbErr := tx.Rollback(); rbErr != nil {
			return errors.Join(err, fmt.Errorf("rollback: %w", rbErr))
		}
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

// Backup writes a consistent copy of the database to dest using VACUUM INTO.
// dest must not already exist.
func (d *DB) Backup(ctx context.Context, dest string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o750); err != nil {
		return fmt.Errorf("create backup directory: %w", err)
	}
	if _, err := d.r.ExecContext(ctx, "VACUUM INTO ?", dest); err != nil {
		return fmt.Errorf("vacuum into %s: %w", dest, err)
	}
	return nil
}
