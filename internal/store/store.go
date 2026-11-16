// Package store persists stations, rollups and the uplink outbox in SQLite.
//
// The database lives on an SD card, so the design minimizes writes: raw
// samples never touch disk, rollups arrive in one transaction per flush, and
// the journal runs in WAL mode with synchronous=NORMAL, which batches fsyncs
// to checkpoints while still surviving a process crash. A power cut can lose
// the last few commits, which is acceptable because rollups are also sitting
// in the outbox until acknowledged and the station list rebuilds itself.
//
// The driver is modernc.org/sqlite, pure Go, so the arm64 build stays a
// plain cross compile.
package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite" // database/sql driver registration
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// TimeLayout is how every timestamp is stored: fixed width UTC so that
// string comparison is chronological.
const TimeLayout = "2006-01-02T15:04:05.000000Z"

// rollback undoes tx on error paths and deferred cleanup. After a successful
// Commit it returns sql.ErrTxDone, which is expected and ignored; any other
// failure is logged because the caller is already returning its own error.
func rollback(tx *sql.Tx) {
	if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
		slog.Warn("store: rollback failed", "err", err)
	}
}

// FormatTime renders t for storage.
func FormatTime(t time.Time) string { return t.UTC().Format(TimeLayout) }

// ParseTime reads a stored timestamp.
func ParseTime(s string) (time.Time, error) {
	t, err := time.Parse(TimeLayout, s)
	if err != nil {
		// Tolerate RFC 3339 written by other tools.
		t, err = time.Parse(time.RFC3339Nano, s)
	}
	return t.UTC(), err
}

// Store is an open database. Methods are safe for concurrent use; the pool
// is limited to one connection so writers never see SQLITE_BUSY from each
// other and the Pi never runs more than one query at a time.
type Store struct {
	db   *sql.DB
	path string

	// mu serializes multi-statement operations that must not interleave
	// with each other at the Go level, such as prune loops.
	mu sync.Mutex
}

// Open opens or creates the database at path and applies migrations.
// ":memory:" is accepted for tests but WAL is then meaningless.
func Open(path string) (*Store, error) {
	dsn := path
	if path != ":memory:" {
		dsn = "file:" + path + "?" + url.Values{
			"_pragma": []string{
				"busy_timeout(5000)",
				"journal_mode(WAL)",
				"synchronous(NORMAL)",
				"foreign_keys(ON)",
			},
		}.Encode()
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("store: open: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetConnMaxLifetime(0)
	s := &Store{db: db, path: path}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := s.applyPragmas(ctx, path != ":memory:"); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := s.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// Close closes the database. It checkpoints the WAL first so the main file
// is complete for backups.
func (s *Store) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// Best effort: a failed checkpoint only leaves the WAL for the next open.
	if _, err := s.db.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		slog.Warn("store: wal checkpoint on close failed", "err", err)
	}
	return s.db.Close()
}

// Path returns the database path.
func (s *Store) Path() string { return s.path }

func (s *Store) applyPragmas(ctx context.Context, wal bool) error {
	// The DSN pragmas apply per connection; run them again explicitly so a
	// driver that ignores the query string still ends up configured, and
	// verify the journal mode because a read-only filesystem silently falls
	// back to DELETE.
	stmts := []string{
		"PRAGMA busy_timeout = 5000",
		"PRAGMA synchronous = NORMAL",
		"PRAGMA foreign_keys = ON",
	}
	if wal {
		stmts = append([]string{"PRAGMA journal_mode = WAL"}, stmts...)
	}
	for _, q := range stmts {
		if _, err := s.db.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("store: %s: %w", q, err)
		}
	}
	if wal {
		var mode string
		if err := s.db.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode); err != nil {
			return fmt.Errorf("store: read journal_mode: %w", err)
		}
		if !strings.EqualFold(mode, "wal") {
			return fmt.Errorf("store: journal_mode is %q, want wal", mode)
		}
	}
	return nil
}

// Migrations are embedded SQL files named NNNN_description.sql and applied
// in numeric order inside one transaction each. schema_migrations records
// what has run.
func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY,
		name TEXT NOT NULL,
		applied_at TEXT NOT NULL
	)`); err != nil {
		return fmt.Errorf("store: create schema_migrations: %w", err)
	}
	entries, err := migrationFS.ReadDir("migrations")
	if err != nil {
		return err
	}
	type mig struct {
		version int
		name    string
	}
	var migs []mig
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".sql") {
			continue
		}
		v, err := strconv.Atoi(strings.SplitN(name, "_", 2)[0])
		if err != nil {
			return fmt.Errorf("store: bad migration name %q", name)
		}
		migs = append(migs, mig{v, name})
	}
	sort.Slice(migs, func(i, j int) bool { return migs[i].version < migs[j].version })

	for _, m := range migs {
		var n int
		if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_migrations WHERE version = ?", m.version).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			continue
		}
		body, err := migrationFS.ReadFile("migrations/" + m.name)
		if err != nil {
			return err
		}
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, string(body)); err != nil {
			rollback(tx)
			return fmt.Errorf("store: migration %s: %w", m.name, err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations (version, name, applied_at) VALUES (?, ?, ?)",
			m.version, m.name, FormatTime(time.Now())); err != nil {
			rollback(tx)
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// SchemaVersion returns the highest applied migration.
func (s *Store) SchemaVersion(ctx context.Context) (int, error) {
	var v sql.NullInt64
	if err := s.db.QueryRowContext(ctx, "SELECT MAX(version) FROM schema_migrations").Scan(&v); err != nil {
		return 0, err
	}
	return int(v.Int64), nil
}

// Get reads a kv entry.
func (s *Store) Get(ctx context.Context, key string) (string, bool, error) {
	var v string
	err := s.db.QueryRowContext(ctx, "SELECT value FROM kv WHERE key = ?", key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return v, true, nil
}

// Set writes a kv entry.
func (s *Store) Set(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx,
		"INSERT INTO kv (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value", key, value)
	return err
}

// Vacuum reclaims space after a large prune. It rewrites the file, so call
// it rarely (the outbox prune calls it only when it dropped a lot).
func (s *Store) Vacuum(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, "VACUUM")
	return err
}

// FileSize returns the size in bytes of the database pages in use, which
// is what the SD card is paying for.
func (s *Store) FileSize(ctx context.Context) (int64, error) {
	var pageCount, pageSize int64
	if err := s.db.QueryRowContext(ctx, "PRAGMA page_count").Scan(&pageCount); err != nil {
		return 0, err
	}
	if err := s.db.QueryRowContext(ctx, "PRAGMA page_size").Scan(&pageSize); err != nil {
		return 0, err
	}
	return pageCount * pageSize, nil
}
