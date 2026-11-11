package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"path/filepath"
	"testing"
	"time"
)

func TestMigrations(t *testing.T) {
	dynamicProviderTables := []string{
		"dynamic_providers",
		"accounts",
		"account_secrets",
		"provider_features",
		"dynamic_endpoints",
		"payload_templates",
		"cookie_partition_rules",
	}

	t.Run("FreshDatabase", func(t *testing.T) {
		dir := t.TempDir()
		dbPath := filepath.Join(dir, "fresh.db")
		backupDir := filepath.Join(dir, "backups")

		db, err := Open(context.Background(), Options{
			Path:          dbPath,
			BackupDir:     backupDir,
			BackupsToKeep: 2,
			BusyTimeout:   time.Second,
			Logger:        slog.New(slog.DiscardHandler),
		})
		if err != nil {
			t.Fatalf("fresh database migration failed: %v", err)
		}
		defer db.Close()

		var maxVer int
		if err := db.sql.QueryRow("SELECT MAX(version) FROM _schema_migrations").Scan(&maxVer); err != nil {
			t.Fatalf("failed to query max version: %v", err)
		}
		if maxVer != 8 {
			t.Fatalf("expected latest version 8, got %d", maxVer)
		}

		var count int
		if err := db.sql.QueryRow("SELECT COUNT(*) FROM _schema_migrations").Scan(&count); err != nil {
			t.Fatalf("failed to count migrations: %v", err)
		}
		if count != 8 {
			t.Fatalf("expected 8 recorded migrations, got %d", count)
		}

		// Ensure dynamic provider tables do not exist
		for _, tbl := range dynamicProviderTables {
			var exists int
			err := db.sql.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?", tbl).Scan(&exists)
			if err != nil || exists != 0 {
				t.Errorf("table %s should not exist after v4, exists=%d, err=%v", tbl, exists, err)
			}
		}

		// Ensure core tables exist
		for _, tbl := range []string{"_schema_migrations", "app_metadata", "user_settings", "binary_configs", "agent_providers", "agent_chat_messages", "agent_allowed_dirs"} {
			var exists int
			err := db.sql.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?", tbl).Scan(&exists)
			if err != nil || exists != 1 {
				t.Errorf("expected core table %s to exist, exists=%d, err=%v", tbl, exists, err)
			}
		}
	})

	t.Run("UpgradeFromV1", func(t *testing.T) {
		dir := t.TempDir()
		dbPath := filepath.Join(dir, "v1.db")
		backupDir := filepath.Join(dir, "backups")

		// Create database manually at v1
		raw, err := sql.Open("sqlite", dbPath)
		if err != nil {
			t.Fatalf("failed to open raw sqlite: %v", err)
		}
		v1SQL, err := migrationFS.ReadFile("migrations/001_initial_schema.sql")
		if err != nil {
			raw.Close()
			t.Fatalf("failed to read 001_initial_schema.sql: %v", err)
		}
		if _, err := raw.Exec(`
			CREATE TABLE IF NOT EXISTS _schema_migrations (
				version INTEGER PRIMARY KEY,
				name TEXT NOT NULL,
				applied_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
			);
		`); err != nil {
			raw.Close()
			t.Fatal(err)
		}
		if _, err := raw.Exec(string(v1SQL)); err != nil {
			raw.Close()
			t.Fatal(err)
		}
		if _, err := raw.Exec("INSERT INTO _schema_migrations (version, name) VALUES (1, '001_initial_schema.sql')"); err != nil {
			raw.Close()
			t.Fatal(err)
		}
		if _, err := raw.Exec("INSERT INTO app_metadata (key, value) VALUES ('test_key', 'test_val')"); err != nil {
			raw.Close()
			t.Fatal(err)
		}
		raw.Close()

		// Open with sqlite.Open to upgrade from v1 to v4
		db, err := Open(context.Background(), Options{
			Path:          dbPath,
			BackupDir:     backupDir,
			BackupsToKeep: 2,
			BusyTimeout:   time.Second,
			Logger:        slog.New(slog.DiscardHandler),
		})
		if err != nil {
			t.Fatalf("upgrade from v1 failed: %v", err)
		}
		defer db.Close()

		var maxVer int
		if err := db.sql.QueryRow("SELECT MAX(version) FROM _schema_migrations").Scan(&maxVer); err != nil {
			t.Fatal(err)
		}
		if maxVer != 8 {
			t.Fatalf("expected version 8 after upgrade from v1, got %d", maxVer)
		}

		// Verify existing data preserved
		var val string
		if err := db.sql.QueryRow("SELECT value FROM app_metadata WHERE key = 'test_key'").Scan(&val); err != nil || val != "test_val" {
			t.Fatalf("expected app_metadata data preserved, got %q, err=%v", val, err)
		}
	})

	t.Run("UpgradeFromV2", func(t *testing.T) {
		dir := t.TempDir()
		dbPath := filepath.Join(dir, "v2.db")
		backupDir := filepath.Join(dir, "backups")

		// Create database at v2
		raw, err := sql.Open("sqlite", dbPath)
		if err != nil {
			t.Fatalf("failed to open raw sqlite: %v", err)
		}
		if _, err := raw.Exec(`
			CREATE TABLE IF NOT EXISTS _schema_migrations (
				version INTEGER PRIMARY KEY,
				name TEXT NOT NULL,
				applied_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
			);
		`); err != nil {
			raw.Close()
			t.Fatal(err)
		}
		for _, m := range []struct {
			v    int
			file string
		}{
			{1, "migrations/001_initial_schema.sql"},
			{2, "migrations/002_binaries.sql"},
		} {
			content, err := migrationFS.ReadFile(m.file)
			if err != nil {
				raw.Close()
				t.Fatal(err)
			}
			if _, err := raw.Exec(string(content)); err != nil {
				raw.Close()
				t.Fatal(err)
			}
			if _, err := raw.Exec("INSERT INTO _schema_migrations (version, name) VALUES (?, ?)", m.v, filepath.Base(m.file)); err != nil {
				raw.Close()
				t.Fatal(err)
			}
		}
		if _, err := raw.Exec("INSERT INTO binary_configs (id, custom_path) VALUES ('ffmpeg', 'C:/custom/ffmpeg.exe')"); err != nil {
			raw.Close()
			t.Fatal(err)
		}
		raw.Close()

		// Upgrade to latest
		db, err := Open(context.Background(), Options{
			Path:          dbPath,
			BackupDir:     backupDir,
			BackupsToKeep: 2,
			BusyTimeout:   time.Second,
			Logger:        slog.New(slog.DiscardHandler),
		})
		if err != nil {
			t.Fatalf("upgrade from v2 failed: %v", err)
		}
		defer db.Close()

		var maxVer int
		if err := db.sql.QueryRow("SELECT MAX(version) FROM _schema_migrations").Scan(&maxVer); err != nil {
			t.Fatal(err)
		}
		if maxVer != 8 {
			t.Fatalf("expected version 8 after upgrade from v2, got %d", maxVer)
		}

		var customPath string
		if err := db.sql.QueryRow("SELECT custom_path FROM binary_configs WHERE id = 'ffmpeg'").Scan(&customPath); err != nil || customPath != "C:/custom/ffmpeg.exe" {
			t.Fatalf("expected binary_configs data preserved, got %q, err=%v", customPath, err)
		}
	})

	t.Run("UpgradeFromV3_DropsDynamicProviderTables", func(t *testing.T) {
		dir := t.TempDir()
		dbPath := filepath.Join(dir, "v3.db")
		backupDir := filepath.Join(dir, "backups")

		// Create database at v3
		raw, err := sql.Open("sqlite", dbPath)
		if err != nil {
			t.Fatalf("failed to open raw sqlite: %v", err)
		}
		if _, err := raw.Exec(`
			CREATE TABLE IF NOT EXISTS _schema_migrations (
				version INTEGER PRIMARY KEY,
				name TEXT NOT NULL,
				applied_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
			);
		`); err != nil {
			raw.Close()
			t.Fatal(err)
		}
		for _, m := range []struct {
			v    int
			file string
		}{
			{1, "migrations/001_initial_schema.sql"},
			{2, "migrations/002_binaries.sql"},
			{3, "migrations/003_dynamic_providers.sql"},
		} {
			content, err := migrationFS.ReadFile(m.file)
			if err != nil {
				raw.Close()
				t.Fatal(err)
			}
			if _, err := raw.Exec(string(content)); err != nil {
				raw.Close()
				t.Fatal(err)
			}
			if _, err := raw.Exec("INSERT INTO _schema_migrations (version, name) VALUES (?, ?)", m.v, filepath.Base(m.file)); err != nil {
				raw.Close()
				t.Fatal(err)
			}
		}

		// Insert test records into dynamic provider tables
		if _, err := raw.Exec(`
			INSERT INTO dynamic_providers (id, name, base_url, auth_domain, allowed_domains_json, probe_rules_json)
			VALUES ('test_prov', 'Test Provider', 'https://test.local', '.test.local', '[]', '{}');
		`); err != nil {
			raw.Close()
			t.Fatalf("failed to insert provider: %v", err)
		}
		if _, err := raw.Exec(`
			INSERT INTO accounts (id, provider_id, label, profile_dir)
			VALUES ('acc_1', 'test_prov', 'Account 1', 'C:/profiles/1');
		`); err != nil {
			raw.Close()
			t.Fatalf("failed to insert account: %v", err)
		}
		raw.Close()

		// Upgrade to latest
		db, err := Open(context.Background(), Options{
			Path:          dbPath,
			BackupDir:     backupDir,
			BackupsToKeep: 2,
			BusyTimeout:   time.Second,
			Logger:        slog.New(slog.DiscardHandler),
		})
		if err != nil {
			t.Fatalf("upgrade from v3 failed: %v", err)
		}
		defer db.Close()

		var maxVer int
		if err := db.sql.QueryRow("SELECT MAX(version) FROM _schema_migrations").Scan(&maxVer); err != nil {
			t.Fatal(err)
		}
		if maxVer != 8 {
			t.Fatalf("expected version 8 after upgrade from v3, got %d", maxVer)
		}

		// Verify ALL 7 dynamic provider tables are dropped and DO NOT exist in sqlite_master
		for _, tbl := range dynamicProviderTables {
			var count int
			err := db.sql.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?", tbl).Scan(&count)
			if err != nil {
				t.Fatalf("error checking table %s: %v", tbl, err)
			}
			if count != 0 {
				t.Errorf("table %q still exists in sqlite_master after v4 migration", tbl)
			}
		}
	})

	t.Run("FutureVersionV8_ReturnsErrSchemaTooNew", func(t *testing.T) {
		dir := t.TempDir()
		dbPath := filepath.Join(dir, "v8.db")
		backupDir := filepath.Join(dir, "backups")

		// Create database with v8 recorded
		raw, err := sql.Open("sqlite", dbPath)
		if err != nil {
			t.Fatalf("failed to open raw sqlite: %v", err)
		}
		if _, err := raw.Exec(`
			CREATE TABLE IF NOT EXISTS _schema_migrations (
				version INTEGER PRIMARY KEY,
				name TEXT NOT NULL,
				applied_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
			);
			INSERT INTO _schema_migrations (version, name) VALUES (9, '009_future.sql');
		`); err != nil {
			raw.Close()
			t.Fatal(err)
		}
		raw.Close()

		_, err = Open(context.Background(), Options{
			Path:          dbPath,
			BackupDir:     backupDir,
			BackupsToKeep: 2,
			BusyTimeout:   time.Second,
			Logger:        slog.New(slog.DiscardHandler),
		})
		if err == nil {
			t.Fatalf("expected error for future version 9, got nil")
		}
		if !errors.Is(err, ErrSchemaTooNew) {
			t.Fatalf("expected ErrSchemaTooNew, got: %v", err)
		}
	})
}
