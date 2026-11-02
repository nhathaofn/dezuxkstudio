package test

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"image/png"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"app/internal/apperr"
	"app/internal/contracts"
	domainbinaries "app/internal/domain/binaries"
	"app/internal/domain/preferences"
	"app/internal/secrets"
	binariessvc "app/internal/service/binaries"
	"app/internal/storage/sqlite"
	"app/internal/tools/gateway"
	toolswatermark "app/internal/tools/watermark"
	"app/pkg/watermark"

	_ "golang.org/x/image/webp"
	_ "modernc.org/sqlite"
)

type dummyBinRepo struct{}

func (d *dummyBinRepo) GetCustomPath(ctx context.Context, id domainbinaries.BinaryID) (string, error) {
	return "", nil
}

func (d *dummyBinRepo) GetAllCustomPaths(ctx context.Context) (map[domainbinaries.BinaryID]string, error) {
	return make(map[domainbinaries.BinaryID]string), nil
}

func (d *dummyBinRepo) SetCustomPath(ctx context.Context, id domainbinaries.BinaryID, customPath string) error {
	return nil
}

// ============================================================================
// ADVERSARIAL SCENARIO 1: SQLite Migrations Resilience
// ============================================================================

func TestAdversarial_Scenario1_MigrationsResilience(t *testing.T) {
	dynamicProviderTables := []string{
		"cookie_partition_rules",
		"payload_templates",
		"dynamic_endpoints",
		"provider_features",
		"account_secrets",
		"accounts",
		"dynamic_providers",
	}

	indexesToCheck := []string{
		"idx_accounts_provider_status",
		"idx_provider_features_provider",
		"idx_dynamic_endpoints_provider_feature",
		"idx_cookie_partition_rules_provider",
	}

	t.Run("Upgrade_V1_To_V4_Cleanly", func(t *testing.T) {
		tempDir := t.TempDir()
		dbPath := filepath.Join(tempDir, "v1_upgrade.db")
		backupDir := filepath.Join(tempDir, "backups")

		rawDB, err := sql.Open("sqlite", dbPath)
		if err != nil {
			t.Fatalf("failed to open raw sqlite: %v", err)
		}

		v1SQL := `
			CREATE TABLE IF NOT EXISTS _schema_migrations (
				version INTEGER PRIMARY KEY,
				name TEXT NOT NULL,
				applied_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
			);
			CREATE TABLE IF NOT EXISTS app_metadata (
				key TEXT PRIMARY KEY,
				value TEXT NOT NULL,
				updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
			);
			CREATE TABLE IF NOT EXISTS user_settings (
				id INTEGER PRIMARY KEY CHECK (id = 1),
				schema_version INTEGER NOT NULL DEFAULT 1,
				theme TEXT NOT NULL DEFAULT 'light',
				language TEXT NOT NULL DEFAULT 'en',
				gateway_url TEXT NOT NULL DEFAULT 'http://127.0.0.1:8080',
				updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
			);
			INSERT INTO _schema_migrations (version, name) VALUES (1, '001_initial_schema.sql');
			INSERT INTO app_metadata (key, value) VALUES ('installation_id', 'v1-test-id');
			INSERT INTO user_settings (id, theme, language, gateway_url) VALUES (1, 'dark', 'vi', 'http://127.0.0.1:9090');
		`
		if _, err := rawDB.Exec(v1SQL); err != nil {
			rawDB.Close()
			t.Fatalf("failed to seed v1 database: %v", err)
		}
		rawDB.Close()

		db, err := sqlite.Open(context.Background(), sqlite.Options{
			Path:          dbPath,
			BackupDir:     backupDir,
			BackupsToKeep: 2,
			BusyTimeout:   time.Second,
			Logger:        slog.New(slog.DiscardHandler),
		})
		if err != nil {
			t.Fatalf("migration from v1 to v4 failed: %v", err)
		}
		_ = db.Close()

		// Verify persisted database
		checkDB, err := sql.Open("sqlite", dbPath)
		if err != nil {
			t.Fatalf("failed to reopen db: %v", err)
		}
		defer checkDB.Close()

		var maxVer int
		if err := checkDB.QueryRow("SELECT MAX(version) FROM _schema_migrations").Scan(&maxVer); err != nil {
			t.Fatalf("query max version failed: %v", err)
		}
		if maxVer != 4 {
			t.Fatalf("expected version 4, got %d", maxVer)
		}

		var metaVal string
		if err := checkDB.QueryRow("SELECT value FROM app_metadata WHERE key = 'installation_id'").Scan(&metaVal); err != nil || metaVal != "v1-test-id" {
			t.Fatalf("v1 app_metadata corrupt or missing: %v", err)
		}
		var theme string
		if err := checkDB.QueryRow("SELECT theme FROM user_settings WHERE id = 1").Scan(&theme); err != nil || theme != "dark" {
			t.Fatalf("v1 user_settings theme corrupt or missing: %v", err)
		}
	})

	t.Run("Upgrade_V2_To_V4_Cleanly", func(t *testing.T) {
		tempDir := t.TempDir()
		dbPath := filepath.Join(tempDir, "v2_upgrade.db")
		backupDir := filepath.Join(tempDir, "backups")

		rawDB, err := sql.Open("sqlite", dbPath)
		if err != nil {
			t.Fatalf("failed to open raw sqlite: %v", err)
		}

		v2SQL := `
			CREATE TABLE IF NOT EXISTS _schema_migrations (
				version INTEGER PRIMARY KEY,
				name TEXT NOT NULL,
				applied_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
			);
			CREATE TABLE IF NOT EXISTS app_metadata (
				key TEXT PRIMARY KEY,
				value TEXT NOT NULL,
				updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
			);
			CREATE TABLE IF NOT EXISTS user_settings (
				id INTEGER PRIMARY KEY CHECK (id = 1),
				schema_version INTEGER NOT NULL DEFAULT 1,
				theme TEXT NOT NULL DEFAULT 'light',
				language TEXT NOT NULL DEFAULT 'en',
				gateway_url TEXT NOT NULL DEFAULT 'http://127.0.0.1:8080',
				updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
			);
			CREATE TABLE IF NOT EXISTS binary_configs (
				id TEXT PRIMARY KEY,
				custom_path TEXT NOT NULL DEFAULT '',
				created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
				updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
			);
			INSERT INTO _schema_migrations (version, name) VALUES (1, '001_initial_schema.sql'), (2, '002_binaries.sql');
			INSERT INTO binary_configs (id, custom_path) VALUES ('yt-dlp', 'C:/bin/yt-dlp.exe');
		`
		if _, err := rawDB.Exec(v2SQL); err != nil {
			rawDB.Close()
			t.Fatalf("failed to seed v2 database: %v", err)
		}
		rawDB.Close()

		db, err := sqlite.Open(context.Background(), sqlite.Options{
			Path:          dbPath,
			BackupDir:     backupDir,
			BackupsToKeep: 2,
			BusyTimeout:   time.Second,
			Logger:        slog.New(slog.DiscardHandler),
		})
		if err != nil {
			t.Fatalf("migration from v2 to v4 failed: %v", err)
		}
		_ = db.Close()

		checkDB, err := sql.Open("sqlite", dbPath)
		if err != nil {
			t.Fatalf("failed to reopen db: %v", err)
		}
		defer checkDB.Close()

		var maxVer int
		if err := checkDB.QueryRow("SELECT MAX(version) FROM _schema_migrations").Scan(&maxVer); err != nil {
			t.Fatalf("query max version failed: %v", err)
		}
		if maxVer != 4 {
			t.Fatalf("expected version 4, got %d", maxVer)
		}

		var customPath string
		if err := checkDB.QueryRow("SELECT custom_path FROM binary_configs WHERE id = 'yt-dlp'").Scan(&customPath); err != nil || customPath != "C:/bin/yt-dlp.exe" {
			t.Fatalf("v2 binary_configs custom_path corrupt or missing: %v", err)
		}
	})

	t.Run("Upgrade_V3_With_Dirty_Rows_In_All_7_Tables_FK_Resilience", func(t *testing.T) {
		tempDir := t.TempDir()
		dbPath := filepath.Join(tempDir, "v3_dirty.db")
		backupDir := filepath.Join(tempDir, "backups")

		rawDB, err := sql.Open("sqlite", dbPath)
		if err != nil {
			t.Fatalf("failed to open raw sqlite: %v", err)
		}

		if _, err := rawDB.Exec("PRAGMA foreign_keys = ON;"); err != nil {
			rawDB.Close()
			t.Fatalf("failed to enable foreign keys: %v", err)
		}

		v3Schema := `
			CREATE TABLE IF NOT EXISTS _schema_migrations (
				version INTEGER PRIMARY KEY,
				name TEXT NOT NULL,
				applied_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
			);
			CREATE TABLE IF NOT EXISTS app_metadata (
				key TEXT PRIMARY KEY,
				value TEXT NOT NULL,
				updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
			);
			CREATE TABLE IF NOT EXISTS user_settings (
				id INTEGER PRIMARY KEY CHECK (id = 1),
				schema_version INTEGER NOT NULL DEFAULT 1,
				theme TEXT NOT NULL DEFAULT 'light',
				language TEXT NOT NULL DEFAULT 'en',
				gateway_url TEXT NOT NULL DEFAULT 'http://127.0.0.1:8080',
				updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
			);
			CREATE TABLE IF NOT EXISTS binary_configs (
				id TEXT PRIMARY KEY,
				custom_path TEXT NOT NULL DEFAULT '',
				created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
				updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
			);

			CREATE TABLE IF NOT EXISTS dynamic_providers (
				id TEXT PRIMARY KEY,
				name TEXT NOT NULL,
				base_url TEXT NOT NULL,
				auth_domain TEXT NOT NULL,
				allowed_domains_json TEXT NOT NULL,
				probe_rules_json TEXT NOT NULL,
				security_rules_json TEXT NOT NULL DEFAULT '{}',
				status TEXT NOT NULL DEFAULT 'active',
				created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
				updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
			);

			CREATE TABLE IF NOT EXISTS accounts (
				id TEXT PRIMARY KEY,
				provider_id TEXT NOT NULL,
				label TEXT NOT NULL,
				identity TEXT NOT NULL DEFAULT '',
				status TEXT NOT NULL DEFAULT 'unverified',
				profile_dir TEXT NOT NULL,
				proxy_id TEXT NOT NULL DEFAULT '',
				paused_until TIMESTAMP,
				usage_json TEXT NOT NULL DEFAULT '[]',
				metadata_json TEXT NOT NULL DEFAULT '{}',
				last_checked_at TIMESTAMP,
				created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
				updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
				FOREIGN KEY (provider_id) REFERENCES dynamic_providers(id) ON DELETE CASCADE
			);

			CREATE TABLE IF NOT EXISTS account_secrets (
				account_id TEXT PRIMARY KEY,
				encrypted_cookies_blob BLOB NOT NULL,
				encrypted_tokens_blob BLOB NOT NULL,
				cookie_hash TEXT NOT NULL DEFAULT '',
				updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
				FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE
			);

			CREATE TABLE IF NOT EXISTS provider_features (
				id TEXT PRIMARY KEY,
				provider_id TEXT NOT NULL,
				feature_key TEXT NOT NULL,
				name TEXT NOT NULL,
				zone_type TEXT NOT NULL,
				dag_node_type TEXT NOT NULL UNIQUE,
				execution_channel TEXT NOT NULL,
				inputs_schema_json TEXT NOT NULL,
				outputs_schema_json TEXT NOT NULL,
				params_schema_json TEXT NOT NULL,
				created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
				FOREIGN KEY (provider_id) REFERENCES dynamic_providers(id) ON DELETE CASCADE
			);

			CREATE TABLE IF NOT EXISTS dynamic_endpoints (
				id TEXT PRIMARY KEY,
				provider_id TEXT NOT NULL,
				feature_key TEXT NOT NULL,
				http_method TEXT NOT NULL DEFAULT 'POST',
				url_template TEXT NOT NULL,
				headers_template_json TEXT NOT NULL,
				rpc_method_name TEXT NOT NULL DEFAULT '',
				resolved_rpc_id TEXT NOT NULL DEFAULT '',
				resolution_strategy TEXT NOT NULL,
				expires_at TIMESTAMP,
				updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
				FOREIGN KEY (provider_id) REFERENCES dynamic_providers(id) ON DELETE CASCADE
			);

			CREATE TABLE IF NOT EXISTS payload_templates (
				id TEXT PRIMARY KEY,
				feature_key TEXT NOT NULL,
				schema_type TEXT NOT NULL,
				template_body TEXT NOT NULL,
				field_mappings_json TEXT NOT NULL,
				baseline_sample_json TEXT NOT NULL DEFAULT '{}',
				version INTEGER NOT NULL DEFAULT 1,
				updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
			);

			CREATE TABLE IF NOT EXISTS cookie_partition_rules (
				id TEXT PRIMARY KEY,
				provider_id TEXT NOT NULL,
				zone_type TEXT NOT NULL,
				domain_pattern TEXT NOT NULL,
				name_pattern TEXT NOT NULL,
				is_required INTEGER NOT NULL DEFAULT 1,
				FOREIGN KEY (provider_id) REFERENCES dynamic_providers(id) ON DELETE CASCADE
			);

			CREATE INDEX IF NOT EXISTS idx_accounts_provider_status ON accounts(provider_id, status);
			CREATE INDEX IF NOT EXISTS idx_provider_features_provider ON provider_features(provider_id);
			CREATE INDEX IF NOT EXISTS idx_dynamic_endpoints_provider_feature ON dynamic_endpoints(provider_id, feature_key);
			CREATE INDEX IF NOT EXISTS idx_cookie_partition_rules_provider ON cookie_partition_rules(provider_id);

			INSERT INTO _schema_migrations (version, name) VALUES
				(1, '001_initial_schema.sql'),
				(2, '002_binaries.sql'),
				(3, '003_dynamic_providers.sql');
		`
		if _, err := rawDB.Exec(v3Schema); err != nil {
			rawDB.Close()
			t.Fatalf("failed to setup v3 schema: %v", err)
		}

		dirtyInserts := `
			INSERT INTO dynamic_providers (id, name, base_url, auth_domain, allowed_domains_json, probe_rules_json)
			VALUES ('prov_flow', 'Google Flow', 'https://flow.google.com', '.google.com', '[]', '{}');

			INSERT INTO accounts (id, provider_id, label, profile_dir)
			VALUES ('acc_user1', 'prov_flow', 'My Primary Account', 'C:/profiles/acc1');

			INSERT INTO account_secrets (account_id, encrypted_cookies_blob, encrypted_tokens_blob, cookie_hash)
			VALUES ('acc_user1', X'DEADBEEF', X'CAFEBABE', 'sha256hash');

			INSERT INTO provider_features (id, provider_id, feature_key, name, zone_type, dag_node_type, execution_channel, inputs_schema_json, outputs_schema_json, params_schema_json)
			VALUES ('feat_img', 'prov_flow', 'image_gen', 'Image Generation', 'execution', 'flow.image.generate', 'direct_http', '{}', '{}', '{}');

			INSERT INTO dynamic_endpoints (id, provider_id, feature_key, url_template, headers_template_json, resolution_strategy)
			VALUES ('ep_batch', 'prov_flow', 'image_gen', 'https://flow.google.com/batch', '{}', 'static_url');

			INSERT INTO payload_templates (id, feature_key, schema_type, template_body, field_mappings_json)
			VALUES ('tpl_flow', 'image_gen', 'json_object_map', '{"prompt": "{{prompt}}"}', '{}');

			INSERT INTO cookie_partition_rules (id, provider_id, zone_type, domain_pattern, name_pattern)
			VALUES ('cpr_sid', 'prov_flow', 'auth', '*.google.com', '^SID$');
		`
		if _, err := rawDB.Exec(dirtyInserts); err != nil {
			rawDB.Close()
			t.Fatalf("failed to insert dirty rows into all 7 tables: %v", err)
		}
		rawDB.Close()

		db, err := sqlite.Open(context.Background(), sqlite.Options{
			Path:          dbPath,
			BackupDir:     backupDir,
			BackupsToKeep: 2,
			BusyTimeout:   time.Second,
			Logger:        slog.New(slog.DiscardHandler),
		})
		if err != nil {
			t.Fatalf("ADVERSARIAL FAILURE: migration 004 crashed with foreign key or execution error: %v", err)
		}
		_ = db.Close()

		checkDB, err := sql.Open("sqlite", dbPath)
		if err != nil {
			t.Fatalf("failed to reopen db: %v", err)
		}
		defer checkDB.Close()

		for _, tbl := range dynamicProviderTables {
			var count int
			err := checkDB.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?", tbl).Scan(&count)
			if err != nil {
				t.Fatalf("sqlite_master query failed for table %s: %v", tbl, err)
			}
			if count != 0 {
				t.Fatalf("SECURITY VIOLATION: dynamic provider table %q still exists in sqlite_master after v4 migration!", tbl)
			}
		}

		for _, idx := range indexesToCheck {
			var count int
			err := checkDB.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name=?", idx).Scan(&count)
			if err != nil {
				t.Fatalf("sqlite_master query failed for index %s: %v", idx, err)
			}
			if count != 0 {
				t.Fatalf("SECURITY VIOLATION: legacy index %q still exists in sqlite_master!", idx)
			}
		}

		var maxVer int
		if err := checkDB.QueryRow("SELECT MAX(version) FROM _schema_migrations").Scan(&maxVer); err != nil || maxVer != 4 {
			t.Fatalf("expected max version 4, got %d (err: %v)", maxVer, err)
		}
	})

	t.Run("Future_Version_5_Database_Strictly_Rejected_ErrSchemaTooNew", func(t *testing.T) {
		tempDir := t.TempDir()
		dbPath := filepath.Join(tempDir, "v5_future.db")
		backupDir := filepath.Join(tempDir, "backups")

		rawDB, err := sql.Open("sqlite", dbPath)
		if err != nil {
			t.Fatalf("failed to open raw sqlite: %v", err)
		}

		futureSQL := `
			CREATE TABLE IF NOT EXISTS _schema_migrations (
				version INTEGER PRIMARY KEY,
				name TEXT NOT NULL,
				applied_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
			);
			INSERT INTO _schema_migrations (version, name) VALUES (5, '005_future_unsupported.sql');
		`
		if _, err := rawDB.Exec(futureSQL); err != nil {
			rawDB.Close()
			t.Fatalf("failed to seed v5 future db: %v", err)
		}
		rawDB.Close()

		_, err = sqlite.Open(context.Background(), sqlite.Options{
			Path:          dbPath,
			BackupDir:     backupDir,
			BackupsToKeep: 2,
			BusyTimeout:   time.Second,
			Logger:        slog.New(slog.DiscardHandler),
		})
		if err == nil {
			t.Fatalf("ADVERSARIAL FAILURE: expected sqlite.Open to reject future schema version 5, got nil")
		}
		if !errors.Is(err, sqlite.ErrSchemaTooNew) {
			t.Fatalf("ADVERSARIAL FAILURE: expected ErrSchemaTooNew, got: %v", err)
		}
	})
}

// ============================================================================
// ADVERSARIAL SCENARIO 2: SSRF Attack Scenarios & Secret Leaks
// ============================================================================

func TestAdversarial_Scenario2_SSRF_And_SecretLeaks(t *testing.T) {
	var officialReceivedAuth string
	var officialCallCount int
	officialSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		officialCallCount++
		officialReceivedAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(gateway.GenerationResponse{
			Created:  1730000000,
			Provider: "flow",
			Data: []gateway.GenerationItem{
				{B64JSON: base64.StdEncoding.EncodeToString([]byte("OFFICIAL_GENERATED_IMAGE_DATA"))},
			},
		})
	}))
	defer officialSrv.Close()

	var attackerReceivedAuth string
	var attackerCallCount int
	attackerSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attackerCallCount++
		attackerReceivedAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(gateway.GenerationResponse{
			Created:  1730000000,
			Provider: "attacker",
			Data: []gateway.GenerationItem{
				{B64JSON: base64.StdEncoding.EncodeToString([]byte("EVIL_IMAGE_DATA"))},
			},
		})
	}))
	defer attackerSrv.Close()

	secretToken := "super-confidential-bearer-key-12345"
	memStore := secrets.NewMemory()
	_ = memStore.Set(secrets.KeyGatewayAPIKey, secretToken)

	mediaDir := t.TempDir()
	node := gateway.NewNode(gateway.Options{
		MediaDir:   mediaDir,
		GatewayURL: officialSrv.URL,
		HTTPClient: officialSrv.Client(),
		Secrets:    memStore,
	})

	t.Run("SSRF_ExternalHost_BearerTokenWithheld", func(t *testing.T) {
		attackerReceivedAuth = ""
		attackerCallCount = 0

		payload, _ := json.Marshal(map[string]string{
			"prompt":      "generate scenic view",
			"gateway_url": attackerSrv.URL,
		})

		_, err := node.Run(context.Background(), payload)
		if err != nil {
			t.Fatalf("node.Run to external server failed: %v", err)
		}

		if attackerCallCount == 0 {
			t.Fatalf("attacker server was not called")
		}
		if attackerReceivedAuth != "" {
			t.Fatalf("CRITICAL SSRF VULNERABILITY: Bearer token was leaked to external server! Received: %q", attackerReceivedAuth)
		}
	})

	t.Run("SSRF_PortMismatch_BearerTokenWithheld", func(t *testing.T) {
		attackerReceivedAuth = ""

		payload, _ := json.Marshal(map[string]string{
			"prompt":      "generate test image",
			"gateway_url": attackerSrv.URL,
		})

		_, err := node.Run(context.Background(), payload)
		if err != nil {
			t.Fatalf("node.Run failed: %v", err)
		}

		if attackerReceivedAuth != "" {
			t.Fatalf("CRITICAL SSRF VULNERABILITY: Bearer token attached to mismatched host/port: %q", attackerReceivedAuth)
		}
	})

	t.Run("SSRF_NonHTTPSchemes_StrictlyRejected_ERR_VALIDATION", func(t *testing.T) {
		forbiddenURLs := []string{
			"ftp://bad-server.com/malicious",
			"file:///etc/passwd",
			"gopher://evil.local:70",
			"javascript:alert('xss')",
			"data:text/plain;base64,SGVsbG8=",
		}

		for _, badURL := range forbiddenURLs {
			payload, _ := json.Marshal(map[string]string{
				"prompt":      "test prompt",
				"gateway_url": badURL,
			})

			_, err := node.Run(context.Background(), payload)
			if err == nil {
				t.Fatalf("ADVERSARIAL FAILURE: non-http scheme %q was accepted without error!", badURL)
			}
			if !apperr.IsCode(err, apperr.CodeValidation) {
				t.Fatalf("ADVERSARIAL FAILURE: expected ERR_VALIDATION for scheme %q, got: %v", badURL, err)
			}
		}
	})

	t.Run("OfficialGateway_BearerTokenAttachedCorrectly", func(t *testing.T) {
		officialReceivedAuth = ""

		payload1, _ := json.Marshal(map[string]string{
			"prompt":      "test prompt",
			"gateway_url": officialSrv.URL,
		})
		_, err := node.Run(context.Background(), payload1)
		if err != nil {
			t.Fatalf("node.Run to official URL failed: %v", err)
		}
		if officialReceivedAuth != "Bearer "+secretToken {
			t.Fatalf("expected Bearer %s, got: %q", secretToken, officialReceivedAuth)
		}

		officialReceivedAuth = ""
		payload2, _ := json.Marshal(map[string]string{
			"prompt":      "test prompt with empty gateway_url",
			"gateway_url": "",
		})
		_, err = node.Run(context.Background(), payload2)
		if err != nil {
			t.Fatalf("node.Run with empty gateway_url failed: %v", err)
		}
		if officialReceivedAuth != "Bearer "+secretToken {
			t.Fatalf("expected Bearer token attached for empty URL fallback, got: %q", officialReceivedAuth)
		}
	})

	t.Run("Preferences_DTO_Never_Leaks_Plaintext_APIKey", func(t *testing.T) {
		prefDomain := preferences.Preferences{
			SchemaVersion: 1,
			Theme:         preferences.ThemeDark,
			Language:      preferences.LanguageEnglish,
			GatewayURL:    "http://127.0.0.1:8080",
		}

		dto := contracts.NewPreferences(prefDomain, true)

		if !dto.HasGatewayApiKey {
			t.Fatalf("expected HasGatewayApiKey = true")
		}

		jsonBytes, err := json.Marshal(dto)
		if err != nil {
			t.Fatalf("marshal preferences DTO failed: %v", err)
		}

		jsonStr := string(jsonBytes)

		if strings.Contains(jsonStr, secretToken) {
			t.Fatalf("SECURITY VIOLATION: JSON representation contains secret token %q!", secretToken)
		}

		var rawMap map[string]any
		if err := json.Unmarshal(jsonBytes, &rawMap); err != nil {
			t.Fatalf("unmarshal json failed: %v", err)
		}

		if _, exists := rawMap["gatewayApiKey"]; exists {
			t.Fatalf("SECURITY VIOLATION: preferences JSON contains 'gatewayApiKey' field: %v", rawMap["gatewayApiKey"])
		}
		if _, exists := rawMap["gateway_api_key"]; exists {
			t.Fatalf("SECURITY VIOLATION: preferences JSON contains 'gateway_api_key' field: %v", rawMap["gateway_api_key"])
		}
		if hasKeyVal, exists := rawMap["hasGatewayApiKey"]; !exists || hasKeyVal != true {
			t.Fatalf("expected hasGatewayApiKey=true in JSON, got: %v", hasKeyVal)
		}
	})
}

// ============================================================================
// ADVERSARIAL SCENARIO 3: Binary Tampering & SHA256 Checksum Enforcement
// ============================================================================

func TestAdversarial_Scenario3_BinaryTampering_SHA256(t *testing.T) {
	fakeZipContent := func(t *testing.T, payload []byte) []byte {
		var buf bytes.Buffer
		w := zip.NewWriter(&buf)
		f, err := w.Create("bin/ffmpeg.exe")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write(payload); err != nil {
			t.Fatal(err)
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		return buf.Bytes()
	}

	rawBinaryPayload := []byte("#!/bin/sh\necho 'yt-dlp 2025.01.26'\n")
	rawBinaryHash := sha256.Sum256(rawBinaryPayload)
	expectedRawSHA256 := hex.EncodeToString(rawBinaryHash[:])

	t.Run("Zip_Tampered_Checksum_Purges_File_And_Returns_ERR_INTEGRITY", func(t *testing.T) {
		validZipBytes := fakeZipContent(t, []byte("GENUINE_FFMPEG_EXECUTABLE_CONTENT"))
		validHash := sha256.Sum256(validZipBytes)
		validSHA256 := hex.EncodeToString(validHash[:])

		tamperedZipBytes := make([]byte, len(validZipBytes))
		copy(tamperedZipBytes, validZipBytes)
		tamperedZipBytes[len(tamperedZipBytes)/2] ^= 0xFF

		tamperedSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/zip")
			_, _ = w.Write(tamperedZipBytes)
		}))
		defer tamperedSrv.Close()

		toolsDir := t.TempDir()

		origManifest := domainbinaries.Registry[domainbinaries.IDFFmpeg]
		defer func() {
			domainbinaries.Registry[domainbinaries.IDFFmpeg] = origManifest
		}()

		patchedManifest := origManifest
		patchedManifest.Downloads = map[string]domainbinaries.DownloadSpec{
			fmt.Sprintf("%s_%s", runtime.GOOS, runtime.GOARCH): {
				URL:           tamperedSrv.URL + "/ffmpeg.zip",
				ArchiveFormat: domainbinaries.ArchiveFormatZip,
				ExtractFile:   "bin/ffmpeg.exe",
				SHA256:        validSHA256,
			},
		}
		domainbinaries.Registry[domainbinaries.IDFFmpeg] = patchedManifest

		svc := binariessvc.New(&dummyBinRepo{}, toolsDir, slog.New(slog.DiscardHandler))

		_, err := svc.Download(context.Background(), domainbinaries.IDFFmpeg, nil)
		if err == nil {
			t.Fatalf("ADVERSARIAL FAILURE: tampered zip binary was accepted without error!")
		}

		if !apperr.IsCode(err, apperr.CodeIntegrity) {
			t.Fatalf("ADVERSARIAL FAILURE: expected ERR_INTEGRITY, got: %v", err)
		}

		entries, err := os.ReadDir(toolsDir)
		if err != nil {
			t.Fatalf("read tools dir failed: %v", err)
		}
		for _, e := range entries {
			t.Fatalf("SECURITY VIOLATION: tampered artifact was left on disk: %s", e.Name())
		}
	})

	t.Run("RawBinary_Tampered_Checksum_Purges_File_And_Returns_ERR_INTEGRITY", func(t *testing.T) {
		tamperedRawBytes := make([]byte, len(rawBinaryPayload))
		copy(tamperedRawBytes, rawBinaryPayload)
		tamperedRawBytes[0] = 'X'

		tamperedSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = w.Write(tamperedRawBytes)
		}))
		defer tamperedSrv.Close()

		toolsDir := t.TempDir()

		origManifest := domainbinaries.Registry[domainbinaries.IDYtDlp]
		defer func() {
			domainbinaries.Registry[domainbinaries.IDYtDlp] = origManifest
		}()

		patchedManifest := origManifest
		patchedManifest.Downloads = map[string]domainbinaries.DownloadSpec{
			fmt.Sprintf("%s_%s", runtime.GOOS, runtime.GOARCH): {
				URL:           tamperedSrv.URL + "/yt-dlp.exe",
				ArchiveFormat: domainbinaries.ArchiveFormatRaw,
				ExtractFile:   "yt-dlp.exe",
				SHA256:        expectedRawSHA256,
			},
		}
		domainbinaries.Registry[domainbinaries.IDYtDlp] = patchedManifest

		svc := binariessvc.New(&dummyBinRepo{}, toolsDir, slog.New(slog.DiscardHandler))

		_, err := svc.Download(context.Background(), domainbinaries.IDYtDlp, nil)
		if err == nil {
			t.Fatalf("ADVERSARIAL FAILURE: tampered raw binary was accepted without error!")
		}
		if !apperr.IsCode(err, apperr.CodeIntegrity) {
			t.Fatalf("ADVERSARIAL FAILURE: expected ERR_INTEGRITY, got: %v", err)
		}

		targetPath := filepath.Join(toolsDir, origManifest.ExecutableName())
		if _, err := os.Stat(targetPath); !os.IsNotExist(err) {
			t.Fatalf("SECURITY VIOLATION: tampered target file %s was not purged from disk!", targetPath)
		}
	})

	t.Run("Valid_Checksum_Passes_Cleanly", func(t *testing.T) {
		validSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = w.Write(rawBinaryPayload)
		}))
		defer validSrv.Close()

		toolsDir := t.TempDir()

		origManifest := domainbinaries.Registry[domainbinaries.IDYtDlp]
		defer func() {
			domainbinaries.Registry[domainbinaries.IDYtDlp] = origManifest
		}()

		patchedManifest := origManifest
		patchedManifest.Downloads = map[string]domainbinaries.DownloadSpec{
			fmt.Sprintf("%s_%s", runtime.GOOS, runtime.GOARCH): {
				URL:           validSrv.URL + "/yt-dlp.exe",
				ArchiveFormat: domainbinaries.ArchiveFormatRaw,
				ExtractFile:   "yt-dlp.exe",
				SHA256:        expectedRawSHA256,
			},
		}
		domainbinaries.Registry[domainbinaries.IDYtDlp] = patchedManifest

		svc := binariessvc.New(&dummyBinRepo{}, toolsDir, slog.New(slog.DiscardHandler))

		targetPath := filepath.Join(toolsDir, origManifest.ExecutableName())
		_, _ = svc.Download(context.Background(), domainbinaries.IDYtDlp, nil)

		data, err := os.ReadFile(targetPath)
		if err != nil {
			t.Fatalf("valid binary missing from disk: %v", err)
		}
		if !bytes.Equal(data, rawBinaryPayload) {
			t.Fatalf("content mismatch on disk")
		}
	})
}

// ============================================================================
// ADVERSARIAL SCENARIO 4: Watermark MIME Header & Transcode Integrity
// ============================================================================

func TestAdversarial_Scenario4_WatermarkMIME_And_TranscodeIntegrity(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 256, 256))
	draw.Draw(img, img.Bounds(), &image.Uniform{C: color.RGBA{R: 120, G: 180, B: 240, A: 255}}, image.Point{}, draw.Src)

	defTemplates := watermark.DefaultTemplates()
	if len(defTemplates) == 0 {
		t.Fatalf("no default templates found")
	}
	tmpl := defTemplates[0] // 48px calibrated template

	stampX, stampY := 256-48-64, 256-48-64

	alpha := tmpl.Alpha
	for y := 0; y < 48; y++ {
		for x := 0; x < 48; x++ {
			a := float64(alpha[y*48+x]) * 0.65
			origC := img.RGBAAt(stampX+x, stampY+y)
			r := uint8(float64(origC.R)*(1-a) + 255*a)
			g := uint8(float64(origC.G)*(1-a) + 255*a)
			b := uint8(float64(origC.B)*(1-a) + 255*a)
			img.SetRGBA(stampX+x, stampY+y, color.RGBA{R: r, G: g, B: b, A: 255})
		}
	}

	eng := watermark.New(watermark.Config{
		Corner:    watermark.CornerBottomRight,
		Templates: []watermark.Template{tmpl},
	})

	t.Run("JPEG_Output_Preserves_JPEG_MagicBytes", func(t *testing.T) {
		var buf bytes.Buffer
		if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 95}); err != nil {
			t.Fatalf("encode jpeg: %v", err)
		}
		jpegBytes := buf.Bytes()

		out, removed, outFmt, err := eng.RemoveWithFormat(jpegBytes)
		if err != nil {
			t.Fatalf("RemoveWithFormat failed: %v", err)
		}
		if !removed {
			t.Fatalf("watermark should be removed from JPEG")
		}
		if outFmt != watermark.FormatJPEG {
			t.Fatalf("expected FormatJPEG, got %s", outFmt)
		}

		if len(out) < 3 || out[0] != 0xFF || out[1] != 0xD8 || out[2] != 0xFF {
			t.Fatalf("JPEG output corrupt: missing 0xFF 0xD8 0xFF magic bytes: %x", out[:min(len(out), 4)])
		}
	})

	t.Run("PNG_Output_Preserves_PNG_MagicBytes", func(t *testing.T) {
		var buf bytes.Buffer
		if err := png.Encode(&buf, img); err != nil {
			t.Fatalf("encode png: %v", err)
		}
		pngBytes := buf.Bytes()

		out, removed, outFmt, err := eng.RemoveWithFormat(pngBytes)
		if err != nil {
			t.Fatalf("RemoveWithFormat failed: %v", err)
		}
		if !removed {
			t.Fatalf("watermark should be removed from PNG")
		}
		if outFmt != watermark.FormatPNG {
			t.Fatalf("expected FormatPNG, got %s", outFmt)
		}

		pngMagic := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}
		if len(out) < 8 || !bytes.Equal(out[:8], pngMagic) {
			t.Fatalf("PNG output corrupt: missing standard PNG magic header")
		}
	})

	t.Run("WebP_SniffFormat_And_PureGo_Decoding", func(t *testing.T) {
		validWebPBytes := []byte("RIFF\x1a\x00\x00\x00WEBPVP8L\x0d\x00\x00\x00\x2f\x00\x00\x00\x10\x07\x10\x11\x11\x88\x88\xfe\x07\x00")

		sniffed := watermark.SniffFormat(validWebPBytes)
		if sniffed != watermark.FormatWebP {
			t.Fatalf("SniffFormat failed to identify WebP: got %s", sniffed)
		}

		// Verify pure-Go WebP decoding succeeds via golang.org/x/image/webp
		out, removed, outFmt, err := eng.RemoveWithFormat(validWebPBytes)
		if err != nil {
			t.Fatalf("RemoveWithFormat on WebP failed: %v", err)
		}
		// Since 1x1 blank image has no watermark, removed must be false and original WebP preserved
		if removed {
			t.Fatalf("expected removed=false for unmarked 1x1 webp")
		}
		if outFmt != watermark.FormatWebP {
			t.Fatalf("expected FormatWebP preserved when no watermark, got %s", outFmt)
		}
		if !bytes.Equal(out, validWebPBytes) {
			t.Fatalf("unmarked WebP data was altered")
		}
	})

	t.Run("Watermark_Node_WebP_Transcoded_To_Lossless_PNG_With_Extension_Alignment", func(t *testing.T) {
		tempDir := t.TempDir()
		outDir := filepath.Join(tempDir, "cleaned_output")

		var buf bytes.Buffer
		_ = png.Encode(&buf, img)
		webpImgPath := filepath.Join(tempDir, "photo_scene.webp")
		if err := os.WriteFile(webpImgPath, buf.Bytes(), 0644); err != nil {
			t.Fatalf("failed to write test webp file: %v", err)
		}

		node := toolswatermark.NewNode(toolswatermark.Options{
			OutputDir: outDir,
			Templates: []watermark.Template{tmpl},
		})

		inputPayload, _ := json.Marshal(map[string]any{
			"images":     []string{webpImgPath},
			"corner":     "bottom-right",
			"inplace":    false,
			"output_dir": outDir,
		})

		res, err := node.Run(context.Background(), inputPayload)
		if err != nil {
			t.Fatalf("watermark node run failed: %v", err)
		}

		outNode, ok := res.(toolswatermark.NodeOutput)
		if !ok {
			t.Fatalf("expected toolswatermark.NodeOutput, got %T", res)
		}

		if len(outNode.Images) == 0 {
			t.Fatalf("expected output images in node result")
		}

		outFilePath := outNode.Images[0].LocalURL
		outExt := filepath.Ext(outFilePath)

		// Assert 1: Output filename aligns to .png, eliminating .webp mismatch
		if !strings.EqualFold(outExt, ".png") {
			t.Fatalf("DATA INTEGRITY VIOLATION: expected output extension .png, got %q (full path: %s)", outExt, outFilePath)
		}

		// Assert 2: Read written output file and assert standard PNG magic bytes
		outputBytes, err := os.ReadFile(outFilePath)
		if err != nil {
			t.Fatalf("failed to read output file: %v", err)
		}
		pngMagic := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}
		if len(outputBytes) < 8 || !bytes.Equal(outputBytes[:8], pngMagic) {
			t.Fatalf("transcoded output file missing PNG magic bytes: %x", outputBytes[:min(len(outputBytes), 8)])
		}

		// Assert 3: Image decodes validly as PNG
		_, err = png.Decode(bytes.NewReader(outputBytes))
		if err != nil {
			t.Fatalf("transcoded output file is not a valid decodable PNG: %v", err)
		}
	})

	t.Run("Watermark_Node_WebP_InPlace_Overwrites_And_Purges_Original", func(t *testing.T) {
		tempDir := t.TempDir()

		var buf bytes.Buffer
		_ = png.Encode(&buf, img)
		origWebPPath := filepath.Join(tempDir, "inplace_target.webp")
		if err := os.WriteFile(origWebPPath, buf.Bytes(), 0644); err != nil {
			t.Fatalf("failed to write test webp file: %v", err)
		}

		node := toolswatermark.NewNode(toolswatermark.Options{
			Templates: []watermark.Template{tmpl},
		})

		inputPayload, _ := json.Marshal(map[string]any{
			"images":  []string{origWebPPath},
			"corner":  "bottom-right",
			"inplace": true,
		})

		res, err := node.Run(context.Background(), inputPayload)
		if err != nil {
			t.Fatalf("watermark node in-place run failed: %v", err)
		}

		outNode, ok := res.(toolswatermark.NodeOutput)
		if !ok || len(outNode.Images) == 0 {
			t.Fatalf("expected valid node output with images")
		}

		expectedNewPath := filepath.Join(tempDir, "inplace_target.png")
		if outNode.Images[0].LocalURL != expectedNewPath {
			t.Fatalf("expected in-place path %s, got %s", expectedNewPath, outNode.Images[0].LocalURL)
		}

		// CRITICAL INTEGRITY CHECK: Original .webp must be purged from disk
		if _, err := os.Stat(origWebPPath); !os.IsNotExist(err) {
			t.Fatalf("DATA INTEGRITY VIOLATION: original .webp file was not purged during in-place WebP transcode!")
		}

		// Target .png must exist and be valid PNG
		cleanedData, err := os.ReadFile(expectedNewPath)
		if err != nil {
			t.Fatalf("failed to read expected .png: %v", err)
		}
		pngMagic := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}
		if len(cleanedData) < 8 || !bytes.Equal(cleanedData[:8], pngMagic) {
			t.Fatalf("in-place transcoded PNG missing magic bytes")
		}
	})


	t.Run("Corrupted_And_NonImage_Bytes_Cleanly_Handled", func(t *testing.T) {
		corruptedInputs := [][]byte{
			[]byte("THIS_IS_DEFINITELY_NOT_AN_IMAGE_FILE"),
			[]byte{0xFF, 0xD8, 0xFF, 0x00, 0x00},
			[]byte{0x89, 0x50, 0x4E, 0x47},
			nil,
			[]byte{},
		}

		for i, corrupt := range corruptedInputs {
			_, _, _, err := eng.RemoveWithFormat(corrupt)
			if err == nil {
				t.Fatalf("ADVERSARIAL FAILURE: corrupted input index %d succeeded without error!", i)
			}
		}
	})
}
