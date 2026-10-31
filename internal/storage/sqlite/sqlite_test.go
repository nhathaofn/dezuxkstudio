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

func TestBinariesRepository(t *testing.T) {
	db := open(t, t.TempDir())
	defer db.Close()
	repo := NewBinariesRepository(db)
	ctx := context.Background()

	// Initially empty
	p, err := repo.GetCustomPath(ctx, "ffmpeg")
	if err != nil || p != "" {
		t.Fatalf("expected empty custom path, got %q, err: %v", p, err)
	}

	all, err := repo.GetAllCustomPaths(ctx)
	if err != nil || len(all) != 0 {
		t.Fatalf("expected empty map, got %v, err: %v", all, err)
	}

	// Set custom path
	if err := repo.SetCustomPath(ctx, "ffmpeg", "C:\\tools\\ffmpeg.exe"); err != nil {
		t.Fatal(err)
	}
	p, err = repo.GetCustomPath(ctx, "ffmpeg")
	if err != nil || p != "C:\\tools\\ffmpeg.exe" {
		t.Fatalf("expected C:\\tools\\ffmpeg.exe, got %q, err: %v", p, err)
	}

	// Update custom path
	if err := repo.SetCustomPath(ctx, "ffmpeg", "C:\\new\\ffmpeg.exe"); err != nil {
		t.Fatal(err)
	}
	p, err = repo.GetCustomPath(ctx, "ffmpeg")
	if err != nil || p != "C:\\new\\ffmpeg.exe" {
		t.Fatalf("expected C:\\new\\ffmpeg.exe, got %q, err: %v", p, err)
	}
}

func TestMigration003DynamicProvidersSchema(t *testing.T) {
	db := open(t, t.TempDir())
	defer db.Close()
	ctx := context.Background()

	// Verify all 7 tables exist
	tables := []string{
		"dynamic_providers",
		"accounts",
		"account_secrets",
		"provider_features",
		"dynamic_endpoints",
		"payload_templates",
		"cookie_partition_rules",
	}

	for _, tbl := range tables {
		var name string
		err := db.sql.QueryRowContext(ctx, "SELECT name FROM sqlite_master WHERE type='table' AND name=?", tbl).Scan(&name)
		if err != nil {
			t.Fatalf("table %s not found: %v", tbl, err)
		}
	}

	// Verify foreign key cascades and inserts work properly
	// 1. Insert dynamic provider
	_, err := db.sql.ExecContext(ctx, `
		INSERT INTO dynamic_providers (id, name, base_url, auth_domain, allowed_domains_json, probe_rules_json)
		VALUES ('flow', 'Google Flow', 'https://flow.google.com', '.google.com', '["flow.google.com"]', '{"token":"sn"}')
	`)
	if err != nil {
		t.Fatalf("insert provider: %v", err)
	}

	// 2. Insert account
	_, err = db.sql.ExecContext(ctx, `
		INSERT INTO accounts (id, provider_id, label, identity, profile_dir)
		VALUES ('acc-1', 'flow', 'Default Account', 'user@gmail.com', 'C:\\profiles\\acc-1')
	`)
	if err != nil {
		t.Fatalf("insert account: %v", err)
	}

	// 3. Insert account_secrets (testing BLOB storage)
	cookieBlob := []byte{0x01, 0x02, 0x03, 0x04}
	tokenBlob := []byte{0xaa, 0xbb, 0xcc}
	_, err = db.sql.ExecContext(ctx, `
		INSERT INTO account_secrets (account_id, encrypted_cookies_blob, encrypted_tokens_blob, cookie_hash)
		VALUES ('acc-1', ?, ?, 'hash123')
	`, cookieBlob, tokenBlob)
	if err != nil {
		t.Fatalf("insert account_secrets: %v", err)
	}

	// 4. Verify read back
	var readCookie, readToken []byte
	var readHash string
	err = db.sql.QueryRowContext(ctx, `
		SELECT encrypted_cookies_blob, encrypted_tokens_blob, cookie_hash
		FROM account_secrets WHERE account_id = 'acc-1'
	`).Scan(&readCookie, &readToken, &readHash)
	if err != nil {
		t.Fatalf("read account_secrets: %v", err)
	}
	if len(readCookie) != 4 || len(readToken) != 3 || readHash != "hash123" {
		t.Fatalf("unexpected secrets data: cookie=%v, token=%v, hash=%s", readCookie, readToken, readHash)
	}

	// 5. Test CASCADE delete when provider is deleted
	_, err = db.sql.ExecContext(ctx, "DELETE FROM dynamic_providers WHERE id = 'flow'")
	if err != nil {
		t.Fatalf("delete provider: %v", err)
	}

	var count int
	_ = db.sql.QueryRowContext(ctx, "SELECT COUNT(*) FROM accounts WHERE provider_id = 'flow'").Scan(&count)
	if count != 0 {
		t.Fatalf("expected accounts to be cascade deleted, got count %d", count)
	}

	_ = db.sql.QueryRowContext(ctx, "SELECT COUNT(*) FROM account_secrets WHERE account_id = 'acc-1'").Scan(&count)
	if count != 0 {
		t.Fatalf("expected account_secrets to be cascade deleted, got count %d", count)
	}
}
