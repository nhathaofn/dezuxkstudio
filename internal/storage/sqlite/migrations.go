package sqlite

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// ErrSchemaTooNew means the database was migrated by a newer application version.
var ErrSchemaTooNew = errors.New("sqlite: database schema is newer than this application")

// migrationName enforces "NNN_snake_case.sql".
var migrationName = regexp.MustCompile(`^(\d{3,})_([a-z0-9_]+)\.sql$`)

type migration struct {
	version int
	name    string
	sql     string
}

// loadMigrations parses and validates the embedded migrations, sorted by version.
func loadMigrations(fsys embed.FS) ([]migration, error) {
	entries, err := fsys.ReadDir("migrations")
	if err != nil {
		return nil, fmt.Errorf("sqlite: read migrations: %w", err)
	}
	seen := map[int]string{}
	var out []migration
	for _, e := range entries {
		m := migrationName.FindStringSubmatch(e.Name())
		if e.IsDir() || m == nil {
			return nil, fmt.Errorf("sqlite: invalid migration file name %q (want NNN_name.sql)", e.Name())
		}
		v, _ := strconv.Atoi(m[1])
		if prev, dup := seen[v]; dup {
			return nil, fmt.Errorf("sqlite: duplicate migration version %d (%s, %s)", v, prev, e.Name())
		}
		seen[v] = e.Name()
		body, err := fsys.ReadFile("migrations/" + e.Name())
		if err != nil {
			return nil, fmt.Errorf("sqlite: read %s: %w", e.Name(), err)
		}
		out = append(out, migration{version: v, name: e.Name(), sql: string(body)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].version < out[j].version })
	return out, nil
}

func (db *DB) migrate(ctx context.Context, backupDir string, keep int) error {
	all, err := loadMigrations(migrationFS)
	if err != nil {
		return err
	}
	if _, err := db.sql.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS _schema_migrations (
			version    INTEGER PRIMARY KEY,
			name       TEXT NOT NULL,
			applied_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
		);`); err != nil {
		return fmt.Errorf("sqlite: create migrations table: %w", err)
	}

	var current int
	if err := db.sql.QueryRowContext(ctx, "SELECT COALESCE(MAX(version), 0) FROM _schema_migrations").Scan(&current); err != nil {
		return fmt.Errorf("sqlite: read schema version: %w", err)
	}
	latest := 0
	if len(all) > 0 {
		latest = all[len(all)-1].version
	}
	if current > latest {
		return fmt.Errorf("%w (database v%d, application v%d)", ErrSchemaTooNew, current, latest)
	}

	var pending []migration
	for _, m := range all {
		if m.version > current {
			pending = append(pending, m)
		}
	}
	if len(pending) == 0 {
		return nil
	}

	if current > 0 {
		if err := db.backup(ctx, backupDir, current, keep); err != nil {
			return err
		}
	}
	for _, m := range pending {
		if err := db.apply(ctx, m); err != nil {
			return err
		}
		db.log.Info("sqlite: migration applied", "version", m.version, "name", m.name)
	}
	return nil
}

func (db *DB) apply(ctx context.Context, m migration) error {
	tx, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sqlite: begin %s: %w", m.name, err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, m.sql); err != nil {
		return fmt.Errorf("sqlite: apply %s: %w", m.name, err)
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO _schema_migrations (version, name) VALUES (?, ?)", m.version, m.name); err != nil {
		return fmt.Errorf("sqlite: record %s: %w", m.name, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sqlite: commit %s: %w", m.name, err)
	}
	return nil
}

// backup snapshots the database with VACUUM INTO before migrating, then prunes old snapshots.
func (db *DB) backup(ctx context.Context, dir string, version, keep int) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("sqlite: create backup dir: %w", err)
	}
	base := strings.TrimSuffix(filepath.Base(db.path), filepath.Ext(db.path))
	target := filepath.Join(dir, fmt.Sprintf("%s-v%03d-%s.db", base, version, time.Now().UTC().Format("20060102T150405.000000000Z")))
	if _, err := db.sql.ExecContext(ctx, "VACUUM INTO ?", target); err != nil {
		return fmt.Errorf("sqlite: backup before migration: %w", err)
	}
	db.log.Info("sqlite: pre-migration backup written", "path", target)

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var backups []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), base+"-v") && strings.HasSuffix(e.Name(), ".db") {
			backups = append(backups, e.Name())
		}
	}
	sort.Strings(backups)
	for len(backups) > keep {
		_ = os.Remove(filepath.Join(dir, backups[0]))
		backups = backups[1:]
	}
	return nil
}
