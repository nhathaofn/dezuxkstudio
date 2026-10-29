package sqlite

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"app/internal/domain/preferences"
)

func open(t *testing.T, dir string) *DB {
	t.Helper()
	db, err := Open(context.Background(), Options{
		Path:          filepath.Join(dir, "test.db"),
		BackupDir:     filepath.Join(dir, "backups"),
		BackupsToKeep: 2,
		BusyTimeout:   time.Second,
		Logger:        slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func TestMigrationsAreIdempotent(t *testing.T) {
	dir := t.TempDir()
	db := open(t, dir)
	var n int
	if err := db.sql.QueryRow("SELECT COUNT(*) FROM _schema_migrations").Scan(&n); err != nil || n == 0 {
		t.Fatalf("migrations not recorded: %d %v", n, err)
	}
	_ = db.Close()

	db = open(t, dir)
	defer db.Close()
	var again int
	_ = db.sql.QueryRow("SELECT COUNT(*) FROM _schema_migrations").Scan(&again)
	if again != n {
		t.Fatalf("re-open re-applied migrations: %d → %d", n, again)
	}
}

func TestRefusesNewerSchema(t *testing.T) {
	dir := t.TempDir()
	db := open(t, dir)
	if _, err := db.sql.Exec("INSERT INTO _schema_migrations (version, name) VALUES (999, 'future.sql')"); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	_, err := Open(context.Background(), Options{
		Path: filepath.Join(dir, "test.db"), BackupDir: dir, BackupsToKeep: 1,
		BusyTimeout: time.Second, Logger: slog.New(slog.DiscardHandler),
	})
	if !errors.Is(err, ErrSchemaTooNew) {
		t.Fatalf("err = %v", err)
	}
}

func TestEmbeddedMigrationsAreWellFormed(t *testing.T) {
	ms, err := loadMigrations(migrationFS)
	if err != nil {
		t.Fatal(err)
	}
	for i, m := range ms {
		if i > 0 && m.version <= ms[i-1].version {
			t.Fatalf("migrations not strictly ordered: %v", ms)
		}
	}
}

func TestBackupPrunesOldSnapshots(t *testing.T) {
	dir := t.TempDir()
	db := open(t, dir)
	defer db.Close()
	backups := filepath.Join(dir, "backups")
	for i := 0; i < 4; i++ {
		if err := db.backup(context.Background(), backups, i+1, 2); err != nil {
			t.Fatal(err)
		}
	}
	matches, _ := filepath.Glob(filepath.Join(backups, "test-v*.db"))
	if len(matches) != 2 {
		t.Fatalf("want 2 backups, got %v", matches)
	}
}

func TestPreferencesRepository(t *testing.T) {
	db := open(t, t.TempDir())
	defer db.Close()
	repo := NewPreferencesRepository(db)
	ctx := context.Background()

	if _, found, err := repo.Load(ctx); err != nil || found {
		t.Fatalf("empty db: found=%v err=%v", found, err)
	}
	want := preferences.Preferences{SchemaVersion: preferences.SchemaVersion, Theme: preferences.ThemeDark, Language: preferences.LanguageVietnamese}
	if err := repo.Save(ctx, want); err != nil {
		t.Fatal(err)
	}
	got, found, err := repo.Load(ctx)
	if err != nil || !found || got != want {
		t.Fatalf("round trip: %+v %v %v", got, found, err)
	}

	if _, err := db.sql.Exec("UPDATE user_settings SET value = '{broken' WHERE key = ?", preferencesKey); err != nil {
		t.Fatal(err)
	}
	got, found, err = repo.Load(ctx)
	if err != nil || !found || got.SchemaVersion != 0 {
		t.Fatalf("corrupt doc should load as legacy: %+v %v %v", got, found, err)
	}
}
