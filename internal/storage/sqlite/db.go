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
	"runtime"
	"strings"
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

// DB is an open, fully migrated database with a serialized writer handle and a concurrent WAL reader pool.
type DB struct {
	sql    *sql.DB
	reader *sql.DB
	path   string
	log    *slog.Logger
}

func isMemoryDSN(path string) bool {
	return path == ":memory:" || strings.HasPrefix(path, "file::memory:") || strings.Contains(path, "mode=memory")
}

// Open opens (or creates) the database, applies pending migrations, and sets up the concurrent WAL reader pool.
func Open(ctx context.Context, o Options) (*DB, error) {
	if o.Path == "" || o.BackupDir == "" || o.BackupsToKeep <= 0 || o.BusyTimeout <= 0 || o.Logger == nil {
		return nil, errors.New("sqlite: incomplete options")
	}
	mem := isMemoryDSN(o.Path)
	if !mem {
		if err := os.MkdirAll(filepath.Dir(o.Path), 0o700); err != nil {
			return nil, fmt.Errorf("sqlite: create data dir: %w", err)
		}
	}

	dsn := fmt.Sprintf("%s?_pragma=busy_timeout(%d)&_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)",
		o.Path, o.BusyTimeout.Milliseconds())
	if mem {
		dsn = o.Path
	}
	raw, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("sqlite: open %s: %w", o.Path, err)
	}
	// A single connection serialises writers and avoids SQLITE_BUSY.
	raw.SetMaxOpenConns(1)
	raw.SetMaxIdleConns(1)
	raw.SetConnMaxLifetime(0)

	if err := raw.PingContext(ctx); err != nil {
		_ = raw.Close()
		return nil, fmt.Errorf("sqlite: connect %s: %w", o.Path, err)
	}

	db := &DB{sql: raw, reader: raw, path: o.Path, log: o.Logger}
	if err := db.migrate(ctx, o.BackupDir, o.BackupsToKeep); err != nil {
		_ = raw.Close()
		return nil, err
	}

	if !mem {
		readDSN := fmt.Sprintf("%s?_pragma=busy_timeout(%d)&_pragma=foreign_keys(1)&_pragma=query_only(1)&_pragma=synchronous(NORMAL)",
			o.Path, o.BusyTimeout.Milliseconds())
		reader, err := sql.Open("sqlite", readDSN)
		if err != nil {
			_ = raw.Close()
			return nil, fmt.Errorf("sqlite: open reader pool %s: %w", o.Path, err)
		}
		readConns := max(4, runtime.NumCPU())
		reader.SetMaxOpenConns(readConns)
		reader.SetMaxIdleConns(readConns)
		reader.SetConnMaxLifetime(0)
		if err := reader.PingContext(ctx); err != nil {
			_ = reader.Close()
			_ = raw.Close()
			return nil, fmt.Errorf("sqlite: connect reader pool %s: %w", o.Path, err)
		}
		db.reader = reader
	}

	o.Logger.Info("sqlite: database ready", "path", o.Path)
	return db, nil
}

func (db *DB) read() *sql.DB {
	if db.reader != nil {
		return db.reader
	}
	return db.sql
}

// Path returns the database file path.
func (db *DB) Path() string { return db.path }

// Close closes the reader pool, checkpoints the WAL, and closes the writer connection.
func (db *DB) Close() error {
	var errs []error
	if db.reader != nil && db.reader != db.sql {
		if err := db.reader.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if _, err := db.sql.Exec("PRAGMA wal_checkpoint(TRUNCATE);"); err != nil {
		db.log.Warn("sqlite: wal checkpoint failed", "error", err)
	}
	if err := db.sql.Close(); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// inTx runs fn in one transaction: committed when fn returns nil, rolled back otherwise.
// Repositories share it instead of opening transactions by hand.
func (db *DB) inTx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sqlite: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }() // no-op after Commit
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sqlite: commit: %w", err)
	}
	return nil
}
