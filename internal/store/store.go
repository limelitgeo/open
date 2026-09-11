// Copyright 2026 Limelit. Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the repository root for the full terms.

// Package store owns the SQLite database: opening it, migrating it to HEAD,
// and handing out the *sql.DB.
//
// SQLite is pure Go (modernc.org/sqlite), so the binary needs no cgo, no
// Docker and no Postgres. That is what lets `go install` and a scratch image
// both work with no toolchain on the host.
package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// DB wraps the connection pool.
type DB struct {
	*sql.DB
	path string
}

// Open opens (creating if needed) the database at path and migrates it to
// HEAD. The parent directory is created as well, so a fresh install needs no
// setup step of its own.
func Open(ctx context.Context, path string) (*DB, error) {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create data dir %s: %w", dir, err)
		}
	}
	// WAL keeps the scheduler writing while the UI reads. busy_timeout turns
	// the lock contention that follows from that into a wait, not an error.
	dsn := path + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(ON)"
	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite %s: %w", path, err)
	}
	// One writer. SQLite serializes writes anyway, and an unbounded pool turns
	// that serialization into "database is locked" instead of a queue.
	sqlDB.SetMaxOpenConns(1)
	if err := sqlDB.PingContext(ctx); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("ping sqlite %s: %w", path, err)
	}
	db := &DB{DB: sqlDB, path: path}
	if err := db.migrate(ctx); err != nil {
		sqlDB.Close()
		return nil, err
	}
	return db, nil
}

// Path is the database file, for logs and diagnostics.
func (db *DB) Path() string { return db.path }

// migrate applies every embedded migration that has not run yet, in filename
// order, each in its own transaction. Applied names are recorded so a
// re-run is a no-op.
func (db *DB) migrate(ctx context.Context) error {
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migration (
		name       TEXT PRIMARY KEY,
		applied_at TEXT NOT NULL DEFAULT (datetime('now'))
	)`); err != nil {
		return fmt.Errorf("create schema_migration: %w", err)
	}

	entries, err := migrationFS.ReadDir("migrations")
	if err != nil {
		return fmt.Errorf("read migrations: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	for _, name := range names {
		var applied int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migration WHERE name = ?`, name).Scan(&applied); err != nil {
			return fmt.Errorf("check migration %s: %w", name, err)
		}
		if applied > 0 {
			continue
		}
		body, err := migrationFS.ReadFile("migrations/" + name)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", name, err)
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin migration %s: %w", name, err)
		}
		if _, err := tx.ExecContext(ctx, string(body)); err != nil {
			tx.Rollback()
			return fmt.Errorf("apply migration %s: %w", name, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migration (name) VALUES (?)`, name); err != nil {
			tx.Rollback()
			return fmt.Errorf("record migration %s: %w", name, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %s: %w", name, err)
		}
	}
	return nil
}

// AppliedMigrations lists the migrations that have run, oldest first.
func (db *DB) AppliedMigrations(ctx context.Context) ([]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT name FROM schema_migration ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}
