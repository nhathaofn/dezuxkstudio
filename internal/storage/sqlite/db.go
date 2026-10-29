// Package sqlite is the local SQLite persistence layer: connection setup, versioned
// migrations with pre-migration backups, and repository implementations.
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite" // registers the "sqlite" driver
)

// Options configures Open.
type Options struct {
	Path          string // database file
	BackupDir     string // pre-migration snapshots
	BackupsToKeep int
	BusyTimeout   time.Duration
	Logger        *slog.Logger
}

// DB is an open, fully migrated database.
type DB struct {
	sql  *sql.DB
	path string
	log  *slog.Logger
}

// Open opens (or creates) the database and applies pending migrations.
func Open(ctx context.Context, o Options) (*DB, error) {
	if o.Path == "" || o.BackupDir == "" || o.BackupsToKeep <= 0 || o.BusyTimeout <= 0 || o.Logger == nil {
		return nil, errors.New("sqlite: incomplete options")
	}
	if err := os.MkdirAll(filepath.Dir(o.Path), 0o700); err != nil {
		return nil, fmt.Errorf("sqlite: create data dir: %w", err)
	}

	dsn := fmt.Sprintf("%s?_pragma=busy_timeout(%d)&_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)",
		o.Path, o.BusyTimeout.Milliseconds())
	raw, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("sqlite: open %s: %w", o.Path, err)
	}
	// A single connection serialises writers and avoids SQLITE_BUSY in a desktop app.
	raw.SetMaxOpenConns(1)
	raw.SetMaxIdleConns(1)
	raw.SetConnMaxLifetime(0)

	if err := raw.PingContext(ctx); err != nil {
		_ = raw.Close()
		return nil, fmt.Errorf("sqlite: connect %s: %w", o.Path, err)
	}

	db := &DB{sql: raw, path: o.Path, log: o.Logger}
	if err := db.migrate(ctx, o.BackupDir, o.BackupsToKeep); err != nil {
		_ = raw.Close()
		return nil, err
	}
	o.Logger.Info("sqlite: database ready", "path", o.Path)
	return db, nil
}

// Path returns the database file path.
func (db *DB) Path() string { return db.path }

// Close checkpoints the WAL and closes the database.
func (db *DB) Close() error {
	if _, err := db.sql.Exec("PRAGMA wal_checkpoint(TRUNCATE);"); err != nil {
		db.log.Warn("sqlite: wal checkpoint failed", "error", err)
	}
	return db.sql.Close()
}
