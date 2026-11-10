package test

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"app/internal/domain/preferences"
	"app/internal/storage/sqlite"

	_ "modernc.org/sqlite"
)

// ============================================================================
// 1. Challenge Provider Purge & Clean Database Migration Runner
// ============================================================================

func TestAdversarial_SQLite_CleanDatabaseMigration(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "clean_migration_test.db")
	backupDir := filepath.Join(tempDir, "backups")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	// Step 1: Open fresh database with sqlite.Open, triggering automatic migrations
	db, err := sqlite.Open(context.Background(), sqlite.Options{
		Path:          dbPath,
		BackupDir:     backupDir,
		BackupsToKeep: 2,
		BusyTimeout:   2 * time.Second,
		Logger:        logger,
	})
	if err != nil {
		t.Fatalf("failed to open clean sqlite database: %v", err)
	}
	_ = db.Close()

	// Step 2: Open direct raw database connection to inspect schema tables and migration history
	rawDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("failed to open raw sqlite db for inspection: %v", err)
	}
	defer rawDB.Close()

	// Assert _schema_migrations contents
	rows, err := rawDB.Query("SELECT version, name FROM _schema_migrations ORDER BY version ASC")
	if err != nil {
		t.Fatalf("failed to query _schema_migrations: %v", err)
	}
	defer rows.Close()

	type recordedMigration struct {
		version int
		name    string
	}
	var recorded []recordedMigration
	for rows.Next() {
		var rm recordedMigration
		if err := rows.Scan(&rm.version, &rm.name); err != nil {
			t.Fatalf("failed to scan migration row: %v", err)
		}
		recorded = append(recorded, rm)
	}

	// Clean database migration runner must apply migrations 1 through 4
	if len(recorded) != 4 {
		t.Fatalf("expected exactly 4 applied migrations, got %d: %+v", len(recorded), recorded)
	}
	if recorded[0].version != 1 || recorded[0].name != "001_initial_schema.sql" {
		t.Errorf("migration 1 mismatch: got %+v", recorded[0])
	}
	if recorded[1].version != 2 || recorded[1].name != "002_binaries.sql" {
		t.Errorf("migration 2 mismatch: got %+v", recorded[1])
	}
	if recorded[2].version != 3 || recorded[2].name != "003_dynamic_providers.sql" {
		t.Errorf("migration 3 mismatch: got %+v", recorded[2])
	}
	if recorded[3].version != 4 || recorded[3].name != "004_drop_dynamic_providers.sql" {
		t.Errorf("migration 4 mismatch: got %+v", recorded[3])
	}

	// Verify maximum version in _schema_migrations is 4
	var maxVer int
	if err := rawDB.QueryRow("SELECT MAX(version) FROM _schema_migrations").Scan(&maxVer); err != nil {
		t.Fatalf("failed to get max version: %v", err)
	}
	if maxVer != 4 {
		t.Errorf("expected max version 4, got %d", maxVer)
	}

	// Verify expected tables exist
	for _, expectedTable := range []string{"_schema_migrations", "app_metadata", "user_settings", "binary_configs"} {
		var count int
		err := rawDB.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?", expectedTable).Scan(&count)
		if err != nil || count != 1 {
			t.Errorf("expected table %q to exist, count=%d, err=%v", expectedTable, count, err)
		}
	}

	// Verify purge: STRICTLY confirm that none of the 7 legacy provider tables exist
	providerTables := []string{
		"dynamic_providers",
		"accounts",
		"account_secrets",
		"provider_features",
		"dynamic_endpoints",
		"payload_templates",
		"cookie_partition_rules",
	}
	for _, pt := range providerTables {
		var count int
		err := rawDB.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?", pt).Scan(&count)
		if err != nil {
			t.Fatalf("error checking table %s: %v", pt, err)
		}
		if count > 0 {
			t.Fatalf("CRITICAL PURGE VIOLATION: legacy provider table %q exists in clean database schema!", pt)
		}
	}
}

// ============================================================================
// 2. Challenge Preferences Edge Cases & Persistence
// ============================================================================

func TestAdversarial_Preferences_EdgeCasesAndPersistence(t *testing.T) {
	tempDir := t.TempDir()
	db, err := sqlite.Open(context.Background(), sqlite.Options{
		Path:          filepath.Join(tempDir, "prefs_edge.db"),
		BackupDir:     filepath.Join(tempDir, "backups"),
		BackupsToKeep: 2,
		BusyTimeout:   time.Second,
		Logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatalf("failed to open test sqlite db: %v", err)
	}
	defer db.Close()

	repo := sqlite.NewPreferencesRepository(db)
	ctx := context.Background()

	// Legacy schema migration preserves valid theme and language
	legacyPref := preferences.Preferences{
		SchemaVersion: 0,
		Theme:         preferences.ThemeDark,
		Language:      preferences.LanguageVietnamese,
	}
	migrated, err := preferences.Migrate(legacyPref)
	if err != nil {
		t.Fatalf("Migrate failed: %v", err)
	}
	if migrated.SchemaVersion != preferences.SchemaVersion || migrated.Theme != preferences.ThemeDark || migrated.Language != preferences.LanguageVietnamese {
		t.Fatalf("unexpected migrated preferences: %+v", migrated)
	}

	// Save & Load roundtrip preserves preferences in SQLite
	if err := repo.Save(ctx, migrated); err != nil {
		t.Fatalf("failed to save preferences: %v", err)
	}
	loaded, found, err := repo.Load(ctx)
	if err != nil || !found {
		t.Fatalf("failed to load preferences: found=%v, err=%v", found, err)
	}
	if loaded != migrated {
		t.Fatalf("loaded preferences mismatch: got %+v, want %+v", loaded, migrated)
	}
}
