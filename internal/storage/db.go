// Package storage persists endpoints, captured requests, replay attempts, and
// runtime settings in SQLite.
//
// Concurrency: SQLite allows one writer at a time. Writes go through a pool
// with a single connection (transactions use BEGIN IMMEDIATE), and reads use a
// separate read-only pool, so slow searches never block request capture. WAL
// mode lets readers and the writer proceed concurrently, and a 5 second busy
// timeout covers other processes such as `relaybox cleanup`.
package storage

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite" // registers the "sqlite" driver
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// ErrNotFound is returned when a row does not exist.
var ErrNotFound = errors.New("not found")

// ErrDuplicateName is returned when an endpoint name is already in use
// (names are compared case-insensitively).
var ErrDuplicateName = errors.New("endpoint name already in use")

// DB is the Relaybox database.
type DB struct {
	w    *sql.DB
	r    *sql.DB
	path string
	now  func() time.Time
}

// Open opens (creating if needed) the database at path and applies pending
// migrations.
func Open(ctx context.Context, path string) (*DB, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("database path is empty")
	}
	if dir := filepath.Dir(path); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("create database directory %s: %w", dir, err)
		}
	}

	common := "_busy_timeout=5000&_foreign_keys=1"
	w, err := sql.Open("sqlite", path+"?"+common+"&_auto_vacuum=INCREMENTAL&_journal_mode=WAL&_synchronous=NORMAL&_txlock=immediate")
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	w.SetMaxOpenConns(1)
	w.SetMaxIdleConns(1)
	w.SetConnMaxLifetime(0)

	db := &DB{w: w, path: path, now: time.Now}
	if err := w.PingContext(ctx); err != nil {
		w.Close()
		return nil, fmt.Errorf("open database %s: %w", path, err)
	}
	if err := db.migrate(ctx); err != nil {
		w.Close()
		return nil, err
	}

	r, err := sql.Open("sqlite", path+"?"+common+"&_query_only=1")
	if err != nil {
		w.Close()
		return nil, fmt.Errorf("open database: %w", err)
	}
	r.SetMaxOpenConns(4)
	r.SetMaxIdleConns(4)
	db.r = r
	return db, nil
}

// Close closes both connection pools.
func (db *DB) Close() error {
	var errs []error
	if db.r != nil {
		errs = append(errs, db.r.Close())
	}
	if db.w != nil {
		// Fold the WAL back into the main file so a copied database is complete.
		_, _ = db.w.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
		errs = append(errs, db.w.Close())
	}
	return errors.Join(errs...)
}

// Ping verifies that the database answers queries on both pools.
func (db *DB) Ping(ctx context.Context) error {
	var one int
	if err := db.r.QueryRowContext(ctx, "SELECT 1").Scan(&one); err != nil {
		return err
	}
	return db.w.QueryRowContext(ctx, "SELECT 1").Scan(&one)
}

// Path returns the database file path.
func (db *DB) Path() string { return db.path }

// SizeBytes returns the size of the database file plus its write-ahead log.
func (db *DB) SizeBytes() int64 {
	var total int64
	for _, suffix := range []string{"", "-wal"} {
		if fi, err := os.Stat(db.path + suffix); err == nil {
			total += fi.Size()
		}
	}
	return total
}

// QuickCheck runs SQLite's quick integrity check.
func (db *DB) QuickCheck(ctx context.Context) error {
	var result string
	if err := db.r.QueryRowContext(ctx, "PRAGMA quick_check").Scan(&result); err != nil {
		return err
	}
	if result != "ok" {
		return fmt.Errorf("integrity check failed: %s", result)
	}
	return nil
}

// SchemaVersion returns the highest applied migration version.
func (db *DB) SchemaVersion(ctx context.Context) (int, error) {
	var v sql.NullInt64
	err := db.r.QueryRowContext(ctx, "SELECT MAX(version) FROM schema_migrations").Scan(&v)
	return int(v.Int64), err
}

type migration struct {
	version int
	name    string
	sql     string
}

func loadMigrations() ([]migration, error) {
	entries, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		return nil, err
	}
	var out []migration
	for _, e := range entries {
		name := e.Name()
		num, rest, ok := strings.Cut(strings.TrimSuffix(name, ".sql"), "_")
		v, err := strconv.Atoi(num)
		if !ok || err != nil || v <= 0 {
			return nil, fmt.Errorf("migration file %q must be named NNNN_description.sql", name)
		}
		body, err := migrationFS.ReadFile("migrations/" + name)
		if err != nil {
			return nil, err
		}
		out = append(out, migration{version: v, name: rest, sql: string(body)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].version < out[j].version })
	for i := 1; i < len(out); i++ {
		if out[i].version == out[i-1].version {
			return nil, fmt.Errorf("duplicate migration version %d", out[i].version)
		}
	}
	return out, nil
}

func (db *DB) migrate(ctx context.Context) error {
	if _, err := db.w.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version    INTEGER PRIMARY KEY,
		name       TEXT    NOT NULL,
		applied_at INTEGER NOT NULL
	)`); err != nil {
		return fmt.Errorf("prepare migrations table: %w", err)
	}

	migrations, err := loadMigrations()
	if err != nil {
		return fmt.Errorf("load migrations: %w", err)
	}
	latest := migrations[len(migrations)-1].version

	applied := make(map[int]bool)
	rows, err := db.w.QueryContext(ctx, "SELECT version FROM schema_migrations")
	if err != nil {
		return fmt.Errorf("read applied migrations: %w", err)
	}
	maxApplied := 0
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			rows.Close()
			return err
		}
		applied[v] = true
		maxApplied = max(maxApplied, v)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if maxApplied > latest {
		return fmt.Errorf("database schema version %d is newer than this build of Relaybox supports (%d); upgrade Relaybox or use a different RELAYBOX_DATABASE_PATH", maxApplied, latest)
	}

	for _, m := range migrations {
		if applied[m.version] {
			continue
		}
		if err := db.applyMigration(ctx, m); err != nil {
			return fmt.Errorf("apply migration %04d_%s: %w", m.version, m.name, err)
		}
	}
	return nil
}

func (db *DB) applyMigration(ctx context.Context, m migration) error {
	tx, err := db.w.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, m.sql); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		"INSERT INTO schema_migrations (version, name, applied_at) VALUES (?, ?, ?)",
		m.version, m.name, db.now().UnixMilli()); err != nil {
		return err
	}
	return tx.Commit()
}

// Vacuum returns free pages to the file system after large deletions.
func (db *DB) Vacuum(ctx context.Context) error {
	_, err := db.w.ExecContext(ctx, "PRAGMA incremental_vacuum")
	return err
}

func millis(t time.Time) int64 { return t.UnixMilli() }

func fromMillis(ms int64) time.Time { return time.UnixMilli(ms).UTC() }

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.Repeat("?,", n-1) + "?"
}

func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

func isDuplicateName(err error) bool {
	return isUniqueViolation(err) && strings.Contains(err.Error(), "endpoints.name")
}
