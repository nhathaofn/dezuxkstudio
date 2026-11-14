package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
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
		if maxVer != latestMigration(t) {
			t.Fatalf("expected version %d, got %d", latestMigration(t), maxVer)
		}

		var count int
		if err := db.sql.QueryRow("SELECT COUNT(*) FROM _schema_migrations").Scan(&count); err != nil {
			t.Fatalf("failed to count migrations: %v", err)
		}
		if count != latestMigration(t) {
			t.Fatalf("expected %d recorded migrations, got %d", latestMigration(t), count)
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
		if maxVer != latestMigration(t) {
			t.Fatalf("expected version %d after upgrade from v1, got %d", latestMigration(t), maxVer)
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
		if maxVer != latestMigration(t) {
			t.Fatalf("expected version %d after upgrade from v2, got %d", latestMigration(t), maxVer)
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
		if maxVer != latestMigration(t) {
			t.Fatalf("expected version %d after upgrade from v3, got %d", latestMigration(t), maxVer)
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
			INSERT INTO _schema_migrations (version, name) VALUES (?, '999_future.sql');
		`, latestMigration(t)+1); err != nil {
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
			t.Fatalf("expected error for a future version, got nil")
		}
		if !errors.Is(err, ErrSchemaTooNew) {
			t.Fatalf("expected ErrSchemaTooNew, got: %v", err)
		}
	})

	t.Run("UpgradeFromV13_RescuesOrphansAndEnforcesForeignKeys", func(t *testing.T) {
		dir := t.TempDir()
		dbPath := filepath.Join(dir, "v13_orphans.db")
		backupDir := filepath.Join(dir, "backups")

		seedPre014DB(t, dbPath, func(raw *sql.DB) {
			if _, err := raw.Exec(`
				INSERT INTO agent_conversations (id, title, provider_id, created_at, updated_at)
				VALUES ('conv-valid', 'Existing chat', 'claude', 100, 200);
			`); err != nil {
				t.Fatal(err)
			}

			longPrompt := "   \n" + strings.Repeat("a", 95) + "\t  "
			if _, err := raw.Exec(`
				INSERT INTO agent_chat_messages (id, provider_id, role, text, thought, tool, created_at, conversation_id, metadata)
				VALUES
					('msg-valid-1', 'claude', 'user', 'existing prompt', '', '', 150, 'conv-valid', ''),
					('msg-legacy-1', 'codex', 'user', 'legacy question', 'th', 'read_file', 50, '', '{"subagents":[{"id":"s1"}]}'),
					('msg-legacy-2', 'codex', 'assistant', 'legacy answer', '', '', 75, '', ''),
					('msg-orph-1', 'antigravity', 'user', ?, '', '', 300, 'conv-orphan-long', ''),
					('msg-orph-2', 'claude', 'assistant', 'reply', '', '', 450, 'conv-orphan-long', ''),
					('msg-nouser-1', 'claude', 'assistant', 'assistant only', '', '', 500, 'conv-orphan-nouser', '');
			`, longPrompt); err != nil {
				t.Fatal(err)
			}

			if _, err := raw.Exec(`
				INSERT INTO agent_conversation_sessions (conversation_id, provider_id, session_id, updated_at)
				VALUES
					('conv-valid', 'claude', 'sess-valid', '2026-01-01T00:00:00Z'),
					('', 'codex', 'sess-legacy-codex', '2026-01-01T00:00:00Z'),
					('conv-orphan-sess-only', 'codex', 'sess-orph-codex', '2026-01-02T00:00:00Z'),
					('conv-orphan-sess-only', 'claude', 'sess-orph-claude', '2026-01-03T00:00:00Z');
			`); err != nil {
				t.Fatal(err)
			}
		})

		db, err := Open(context.Background(), Options{
			Path:          dbPath,
			BackupDir:     backupDir,
			BackupsToKeep: 2,
			BusyTimeout:   time.Second,
			Logger:        slog.New(slog.DiscardHandler),
		})
		if err != nil {
			t.Fatalf("upgrade from v13 failed: %v", err)
		}
		defer db.Close()

		var maxVer int
		if err := db.sql.QueryRow("SELECT MAX(version) FROM _schema_migrations").Scan(&maxVer); err != nil {
			t.Fatal(err)
		}
		if maxVer != latestMigration(t) {
			t.Fatalf("expected version %d, got %d", latestMigration(t), maxVer)
		}

		// PRAGMA foreign_key_check must return zero violations.
		fkCheck, err := db.sql.Query("PRAGMA foreign_key_check")
		if err != nil {
			t.Fatal(err)
		}
		defer fkCheck.Close()
		if fkCheck.Next() {
			t.Fatal("expected PRAGMA foreign_key_check to be empty after 014 migration")
		}

		// Verify all 6 messages and all 4 session rows survived intact.
		var msgCount, sessCount int
		if err := db.sql.QueryRow("SELECT COUNT(*) FROM agent_chat_messages").Scan(&msgCount); err != nil {
			t.Fatal(err)
		}
		if msgCount != 6 {
			t.Fatalf("expected all 6 messages preserved, got %d", msgCount)
		}
		if err := db.sql.QueryRow("SELECT COUNT(*) FROM agent_conversation_sessions").Scan(&sessCount); err != nil {
			t.Fatal(err)
		}
		if sessCount != 4 {
			t.Fatalf("expected all 4 sessions preserved, got %d", sessCount)
		}

		var meta string
		if err := db.sql.QueryRow("SELECT metadata FROM agent_chat_messages WHERE id = 'msg-legacy-1'").Scan(&meta); err != nil {
			t.Fatal(err)
		}
		if meta != `{"subagents":[{"id":"s1"}]}` {
			t.Fatalf("expected legacy metadata preserved, got %q", meta)
		}

		// Verify rescued and existing parent rows in agent_conversations.
		type convRow struct {
			title, provider string
			created, upd    int64
		}
		getConv := func(id string) convRow {
			t.Helper()
			var c convRow
			if err := db.sql.QueryRow(
				"SELECT title, provider_id, created_at, updated_at FROM agent_conversations WHERE id = ?", id,
			).Scan(&c.title, &c.provider, &c.created, &c.upd); err != nil {
				t.Fatalf("missing rescued conversation %q: %v", id, err)
			}
			return c
		}

		if c := getConv("conv-valid"); c != (convRow{"Existing chat", "claude", 100, 200}) {
			t.Fatalf("existing conversation modified: %+v", c)
		}
		if c := getConv(""); c != (convRow{"Legacy chat", "codex", 50, 75}) {
			t.Fatalf("legacy empty conversation mismatch: %+v", c)
		}
		if c := getConv("conv-orphan-long"); c != (convRow{strings.Repeat("a", 80), "antigravity", 300, 450}) {
			t.Fatalf("orphan message conversation mismatch: %+v", c)
		}
		if c := getConv("conv-orphan-nouser"); c != (convRow{"Recovered conversation", "claude", 500, 500}) {
			t.Fatalf("orphan assistant-only conversation mismatch: %+v", c)
		}
		sessConv := getConv("conv-orphan-sess-only")
		if sessConv.title != "Recovered conversation" || sessConv.provider != "claude" || sessConv.created < 1_000_000_000_000 || sessConv.upd != sessConv.created {
			t.Fatalf("orphan session conversation mismatch (want ms timestamps & MIN provider_id): %+v", sessConv)
		}

		// Verify FK definitions and recreated indexes.
		assertCascadeFK(t, db.sql, "agent_chat_messages", "agent_conversations")
		assertCascadeFK(t, db.sql, "agent_conversation_sessions", "agent_conversations")
		for _, idx := range []string{
			"idx_agent_chat_messages_created_at",
			"idx_agent_chat_messages_conversation",
			"idx_agent_conversation_sessions_conv",
		} {
			var count int
			if err := db.sql.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name=?", idx).Scan(&count); err != nil || count != 1 {
				t.Fatalf("expected index %s to exist, count=%d err=%v", idx, count, err)
			}
		}
	})

	t.Run("UpgradeFromV13_SessionOnlyEmptyConversationID", func(t *testing.T) {
		dir := t.TempDir()
		dbPath := filepath.Join(dir, "v13_sess_empty.db")
		backupDir := filepath.Join(dir, "backups")

		seedPre014DB(t, dbPath, func(raw *sql.DB) {
			if _, err := raw.Exec(`
				INSERT INTO agent_conversation_sessions (conversation_id, provider_id, session_id, updated_at)
				VALUES ('', 'codex', 'sess-only-empty', '2026-01-01T00:00:00Z');
			`); err != nil {
				t.Fatal(err)
			}
		})

		db, err := Open(context.Background(), Options{
			Path:          dbPath,
			BackupDir:     backupDir,
			BackupsToKeep: 2,
			BusyTimeout:   time.Second,
			Logger:        slog.New(slog.DiscardHandler),
		})
		if err != nil {
			t.Fatalf("upgrade from v13 with session-only empty conversation_id failed: %v", err)
		}
		defer db.Close()

		var title, prov string
		if err := db.sql.QueryRow("SELECT title, provider_id FROM agent_conversations WHERE id = ''").Scan(&title, &prov); err != nil {
			t.Fatalf("expected parent row for empty session conversation_id: %v", err)
		}
		if title != "Recovered conversation" || prov != "codex" {
			t.Fatalf("unexpected rescued session parent: title=%q provider=%q", title, prov)
		}

		fkCheck, err := db.sql.Query("PRAGMA foreign_key_check")
		if err != nil {
			t.Fatal(err)
		}
		defer fkCheck.Close()
		if fkCheck.Next() {
			t.Fatal("expected PRAGMA foreign_key_check to be empty")
		}
	})
}

func seedPre014DB(t *testing.T, dbPath string, seed func(*sql.DB)) {
	t.Helper()
	raw, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open raw sqlite: %v", err)
	}
	defer raw.Close()

	if _, err := raw.Exec(`
		CREATE TABLE IF NOT EXISTS _schema_migrations (
			version INTEGER PRIMARY KEY,
			name TEXT NOT NULL,
			applied_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
	`); err != nil {
		t.Fatal(err)
	}

	ms, err := loadMigrations(migrationFS)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range ms {
		if m.version >= 14 {
			continue
		}
		if _, err := raw.Exec(m.sql); err != nil {
			t.Fatalf("apply %s: %v", m.name, err)
		}
		if _, err := raw.Exec("INSERT INTO _schema_migrations (version, name) VALUES (?, ?)", m.version, m.name); err != nil {
			t.Fatalf("record %s: %v", m.name, err)
		}
	}
	if seed != nil {
		seed(raw)
	}
}

func assertCascadeFK(t *testing.T, db *sql.DB, table, targetTable string) {
	t.Helper()
	rows, err := db.Query("PRAGMA foreign_key_list(" + table + ")")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			id, seq                          int
			refTable, from, to, onUpd, onDel string
			match                            string
		)
		if err := rows.Scan(&id, &seq, &refTable, &from, &to, &onUpd, &onDel, &match); err != nil {
			t.Fatal(err)
		}
		if refTable == targetTable && from == "conversation_id" && to == "id" && onDel == "CASCADE" {
			return
		}
	}
	t.Fatalf("expected %s(conversation_id) -> %s(id) ON DELETE CASCADE FK", table, targetTable)
}

// latestMigration is the highest embedded migration version, so tests need no edit per migration.
func latestMigration(t *testing.T) int {
	t.Helper()
	ms, err := loadMigrations(migrationFS)
	if err != nil {
		t.Fatal(err)
	}
	return ms[len(ms)-1].version
}
