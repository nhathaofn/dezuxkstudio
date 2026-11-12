package sqlite

import (
	"context"
	"database/sql"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"app/internal/domain/provider"
)

// TestMigration011CarriesCustomModels builds a database at version 10, fills the old
// custom_provider_models table and checks Open moves the rows into provider_models.
func TestMigration011CarriesCustomModels(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "v10.db")

	raw, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`CREATE TABLE IF NOT EXISTS _schema_migrations (
		version INTEGER PRIMARY KEY, name TEXT NOT NULL, applied_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP);`); err != nil {
		t.Fatal(err)
	}
	all, err := loadMigrations(migrationFS)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range all {
		if m.version > 10 {
			break
		}
		if _, err := raw.Exec(m.sql); err != nil {
			t.Fatalf("%s: %v", m.name, err)
		}
		if _, err := raw.Exec("INSERT INTO _schema_migrations (version, name) VALUES (?, ?)", m.version, m.name); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := raw.Exec(`INSERT INTO custom_providers (id, name, discovery_format, base_url, created_at, updated_at)
		VALUES ('cp1', 'Local', 'openai', 'http://x', 1, 1), ('cp2', 'Gem', 'gemini', 'http://y', 1, 1)`); err != nil {
		t.Fatal(err)
	}
	rows := []struct {
		owner, model, endpoint string
		enabled, def, pos      int
	}{
		{"cp1", "chat", "openai-chat", 1, 1, 0},
		{"cp1", "resp", "openai-responses", 1, 0, 1},
		{"cp1", "img", "openai-images", 0, 0, 2},
		{"cp1", "img2", "openai-chat-image", 1, 1, 3},
		{"cp1", "vid", "openai-video", 1, 1, 4},
		{"cp1", "voice", "openai-tts", 1, 1, 5},
		{"cp2", "gveo", "gemini-video", 1, 1, 0},
		{"cp2", "gimg", "gemini-image", 1, 1, 1},
		{"cp2", "gtts", "gemini-tts", 1, 1, 2},
		{"cp2", "gllm", "gemini-generate", 1, 1, 3},
	}
	for _, r := range rows {
		if _, err := raw.Exec(`INSERT INTO custom_provider_models (provider_id, model_id, endpoint, enabled, is_default, position)
			VALUES (?, ?, ?, ?, ?, ?)`, r.owner, r.model, r.endpoint, r.enabled, r.def, r.pos); err != nil {
			t.Fatal(err)
		}
	}
	raw.Close()

	db, err := Open(context.Background(), Options{
		Path: dbPath, BackupDir: filepath.Join(dir, "backups"), BackupsToKeep: 2,
		BusyTimeout: time.Second, Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("upgrade from v10: %v", err)
	}
	defer db.Close()

	var old int
	if err := db.sql.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE name='custom_provider_models'").Scan(&old); err != nil || old != 0 {
		t.Fatalf("old table must be dropped: %d %v", old, err)
	}
	for _, tbl := range []string{"provider_models", "provider_settings", "provider_defaults"} {
		var n int
		if err := db.sql.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?", tbl).Scan(&n); err != nil || n != 1 {
			t.Fatalf("table %s missing", tbl)
		}
	}

	want := map[string]struct {
		cap      provider.Capability
		endpoint string
		enabled  bool
		def      bool
	}{
		"cp1/chat":  {provider.CapabilityLLM, "openai-chat", true, true},
		"cp1/resp":  {provider.CapabilityLLM, "openai-responses", true, false},
		"cp1/img":   {provider.CapabilityImage, "openai-images", false, false},
		"cp1/img2":  {provider.CapabilityImage, "openai-chat-image", true, true},
		"cp1/vid":   {provider.CapabilityVideo, "openai-video", true, true},
		"cp1/voice": {provider.CapabilityTTS, "openai-tts", true, true},
		"cp2/gveo":  {provider.CapabilityVideo, "gemini-video", true, true},
		"cp2/gimg":  {provider.CapabilityImage, "gemini-image", true, true},
		"cp2/gtts":  {provider.CapabilityTTS, "gemini-tts", true, true},
		"cp2/gllm":  {provider.CapabilityLLM, "gemini-generate", true, true},
	}
	repo := NewProviderRepository(db)
	total := 0
	for _, owner := range []string{"cp1", "cp2"} {
		models, err := repo.ListModels(context.Background(), owner)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range models {
			total++
			w, ok := want[owner+"/"+m.ModelID]
			if !ok {
				t.Fatalf("unexpected row %s/%s", owner, m.ModelID)
			}
			if m.Capability != w.cap || string(m.Endpoint) != w.endpoint || m.Enabled != w.enabled || m.IsDefault != w.def || m.UserAdded {
				t.Errorf("%s/%s = %+v, want %+v", owner, m.ModelID, m, w)
			}
			// every carried endpoint must agree with the domain catalog
			spec, ok := provider.EndpointByKey(m.Endpoint)
			if !ok || spec.Capability != m.Capability {
				t.Errorf("%s/%s: capability %s disagrees with endpoint catalog", owner, m.ModelID, m.Capability)
			}
		}
	}
	if total != len(rows) {
		t.Fatalf("carried %d of %d rows", total, len(rows))
	}
	// order (position) is preserved
	cp1, _ := repo.ListModels(context.Background(), "cp1")
	if cp1[0].ModelID != "chat" || cp1[5].ModelID != "voice" {
		t.Fatalf("order lost: %+v", cp1)
	}
	// providers still load through the repository
	cps, err := repo.ListCustom(context.Background())
	if err != nil || len(cps) != 2 || len(cps[0].Models) == 0 {
		t.Fatalf("%+v %v", cps, err)
	}
}
