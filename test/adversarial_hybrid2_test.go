package test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"app/internal/domain/preferences"
	"app/internal/engine"
	"app/internal/fsutil"
	"app/internal/paths"
	"app/internal/service/workflow"
	"app/internal/storage/sqlite"
	"app/internal/tools/gateway"
	"app/internal/tools/watermark"

	_ "modernc.org/sqlite"
)

// Helper: encode a distinct test PNG into raw bytes
func generateTestPNG(t *testing.T, w, h int, c color.Color) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(img, img.Bounds(), &image.Uniform{C: c}, image.Point{}, draw.Src)
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("failed to encode test png: %v", err)
	}
	return buf.Bytes()
}

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
// 2. Challenge DAG Node tool.ai_image_generator
// ============================================================================

func TestAdversarial_GatewayNode_InvalidURLAndServerDown(t *testing.T) {
	mediaDir := t.TempDir()

	// Scenario 2.1: Server down / completely unroutable port
	// Port 59199 is typically unused on localhost; connect fails immediately
	nodeDown := gateway.NewNode(gateway.Options{
		MediaDir:   mediaDir,
		GatewayURL: "http://127.0.0.1:59199",
		HTTPClient: &http.Client{Timeout: 500 * time.Millisecond},
	})

	input := json.RawMessage(`{"prompt": "hyper-detailed digital painting"}`)
	_, err := nodeDown.Run(context.Background(), input)
	if err == nil {
		t.Fatal("expected error when gateway server is down, got nil")
	}
	if !strings.Contains(err.Error(), "gateway request failed") {
		t.Errorf("expected 'gateway request failed' error message, got: %v", err)
	}

	// Scenario 2.2: Invalid URL scheme
	nodeBadScheme := gateway.NewNode(gateway.Options{
		MediaDir:   mediaDir,
		GatewayURL: "invalid-scheme://localhost:8080",
		HTTPClient: &http.Client{Timeout: 500 * time.Millisecond},
	})
	_, err = nodeBadScheme.Run(context.Background(), input)
	if err == nil {
		t.Fatal("expected error with unsupported URL scheme, got nil")
	}
}

func TestAdversarial_GatewayNode_CorruptBase64ImageData(t *testing.T) {
	mediaDir := t.TempDir()

	// Mock server that returns HTTP 200 but garbage base64 string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(gateway.GenerationResponse{
			Created:  1730000000,
			Provider: "flow",
			Data: []gateway.GenerationItem{
				{B64JSON: "NOT_VALID_BASE64_!@#$%^&*()"},
			},
		})
	}))
	defer srv.Close()

	node := gateway.NewNode(gateway.Options{
		MediaDir:   mediaDir,
		GatewayURL: srv.URL,
		HTTPClient: srv.Client(),
	})

	input := json.RawMessage(`{"prompt": "cyberpunk motorcycle"}`)
	_, err := node.Run(context.Background(), input)
	if err == nil {
		t.Fatal("expected error for corrupted base64 data, got nil")
	}
	if !strings.Contains(err.Error(), "decode b64_json image") {
		t.Errorf("expected 'decode b64_json image' error, got: %v", err)
	}

	// Verify no partial files were written to mediaDir
	entries, readErr := os.ReadDir(mediaDir)
	if readErr != nil {
		t.Fatalf("failed to read mediaDir: %v", readErr)
	}
	if len(entries) > 0 {
		t.Errorf("expected 0 files in mediaDir after corrupt b64 failure, found %d", len(entries))
	}
}

func TestAdversarial_GatewayNode_CorruptURLDownload(t *testing.T) {
	mediaDir := t.TempDir()

	// Mock server returning an image URL that yields HTTP 500
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/images/generations" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(gateway.GenerationResponse{
				Data: []gateway.GenerationItem{
					{URL: "http://" + r.Host + "/images/fail.png"},
				},
			})
			return
		}
		if r.URL.Path == "/images/fail.png" {
			http.Error(w, "internal image asset storage failure", http.StatusInternalServerError)
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	node := gateway.NewNode(gateway.Options{
		MediaDir:   mediaDir,
		GatewayURL: srv.URL,
		HTTPClient: srv.Client(),
	})

	_, err := node.Run(context.Background(), json.RawMessage(`{"prompt": "test prompt"}`))
	if err == nil {
		t.Fatal("expected error on failed image download")
	}
	if !strings.Contains(err.Error(), "image download failed (HTTP 500)") {
		t.Errorf("expected HTTP 500 error, got: %v", err)
	}
}

func TestAdversarial_GatewayNode_AtomicWriteFailureSimulation(t *testing.T) {
	tempDir := t.TempDir()
	targetPath := filepath.Join(tempDir, "test_atomic_output.bin")

	// Verify fsutil.WriteAtomic cleanup when write function fails mid-way
	testErr := errors.New("simulated catastrophic write error")
	err := fsutil.WriteAtomic(targetPath, 0644, func(w io.Writer) error {
		// Write partial bytes then error out
		_, _ = w.Write([]byte("PARTIAL_BYTES_SHOULD_BE_PURGED"))
		return testErr
	})

	if !errors.Is(err, testErr) {
		t.Fatalf("expected simulated error, got %v", err)
	}

	// 1. Destination targetPath must not exist
	if _, statErr := os.Stat(targetPath); !os.IsNotExist(statErr) {
		t.Fatalf("atomic failure violated: target file exists!")
	}

	// 2. Directory must contain NO leftover .tmp-* files
	subDir := filepath.Dir(targetPath)
	entries, _ := os.ReadDir(subDir)
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") && strings.Contains(entry.Name(), ".tmp-") {
			t.Fatalf("atomic failure left behind orphaned temp file: %s", entry.Name())
		}
	}

	// 3. Test node behavior when MediaDir is impossible to create (e.g. points to an existing file)
	blockerFile := filepath.Join(tempDir, "file_as_dir")
	if err := os.WriteFile(blockerFile, []byte("BLOCKER"), 0644); err != nil {
		t.Fatal(err)
	}

	validPNG := generateTestPNG(t, 64, 64, color.RGBA{R: 255, A: 255})
	validB64 := base64.StdEncoding.EncodeToString(validPNG)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(gateway.GenerationResponse{
			Data: []gateway.GenerationItem{{B64JSON: validB64}},
		})
	}))
	defer srv.Close()

	// MediaDir is a child of the blocker file
	unwritableMediaDir := filepath.Join(blockerFile, "media")
	node := gateway.NewNode(gateway.Options{
		MediaDir:   unwritableMediaDir,
		GatewayURL: srv.URL,
		HTTPClient: srv.Client(),
	})

	_, err = node.Run(context.Background(), json.RawMessage(`{"prompt": "portrait"}`))
	if err == nil {
		t.Fatal("expected error when media directory creation fails, got nil")
	}
	if !strings.Contains(err.Error(), "create media directory") {
		t.Errorf("expected 'create media directory' error, got: %v", err)
	}
}

func TestAdversarial_TwoNodePipeline_E2E_IntactImageVerification(t *testing.T) {
	tempDir := t.TempDir()
	appPaths := paths.Paths{Data: tempDir}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	// Generate a 128x128 RGBA test image with recognizable gradient/shape
	originalPNG := generateTestPNG(t, 128, 128, color.RGBA{R: 20, G: 180, B: 90, A: 255})
	b64Original := base64.StdEncoding.EncodeToString(originalPNG)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/images/generations" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(gateway.GenerationResponse{
			Created:  time.Now().Unix(),
			Provider: "flow",
			Data: []gateway.GenerationItem{
				{B64JSON: b64Original},
			},
		})
	}))
	defer srv.Close()

	svc, err := workflow.New(workflow.Options{
		Paths:         appPaths,
		Logger:        logger,
		GatewayURL:    srv.URL,
		GatewayAPIKey: "challenge-test-token",
		HTTPClient:    srv.Client(),
	})
	if err != nil {
		t.Fatalf("failed to create workflow service: %v", err)
	}

	// Build 2-node graph:
	// tool.ai_image_generator (image) -> tool.watermark_remover (image)
	graph := engine.Graph{
		Nodes: []engine.GraphNode{
			{
				ID:   "gen_node",
				Type: gateway.NodeType,
				Params: json.RawMessage(`{
					"prompt": "futuristic cityscape with golden skyline",
					"negative_prompt": "blurry, low resolution",
					"aspect_ratio": "1:1"
				}`),
			},
			{
				ID:   "wm_node",
				Type: watermark.NodeType,
				Params: json.RawMessage(`{
					"inplace": false
				}`),
			},
		},
		Edges: []engine.Edge{
			{
				From: engine.Endpoint{Node: "gen_node", Port: "image"},
				To:   engine.Endpoint{Node: "wm_node", Port: "image"},
			},
		},
	}

	ctx := context.Background()
	result, err := svc.RunGraph(ctx, graph)
	if err != nil {
		t.Fatalf("RunGraph failed: %v", err)
	}

	if !result.Done() {
		t.Fatalf("expected graph execution to be Done, got: %+v", result)
	}

	// Verify gen_node
	genRun := result.Nodes["gen_node"]
	if genRun.Status != engine.RunDone {
		t.Fatalf("gen_node status = %s, err = %v", genRun.Status, genRun.Error)
	}

	// Verify wm_node
	wmRun := result.Nodes["wm_node"]
	if wmRun.Status != engine.RunDone {
		t.Fatalf("wm_node status = %s, err = %v", wmRun.Status, wmRun.Error)
	}

	var wmOut watermark.NodeOutput
	if err := json.Unmarshal(wmRun.Output, &wmOut); err != nil {
		t.Fatalf("failed to unmarshal watermark output: %v", err)
	}

	cleanedPath := wmOut.Image.LocalURL
	if cleanedPath == "" {
		t.Fatal("cleaned image output path is empty")
	}

	// Empirical verification: read and decode the generated cleaned image
	f, err := os.Open(cleanedPath)
	if err != nil {
		t.Fatalf("failed to open cleaned output image file %s: %v", cleanedPath, err)
	}
	defer f.Close()

	decodedImg, format, err := image.Decode(f)
	if err != nil {
		t.Fatalf("cleaned image is corrupted or invalid: %v", err)
	}
	if format != "png" {
		t.Errorf("expected png format, got %s", format)
	}

	bounds := decodedImg.Bounds()
	if bounds.Dx() != 128 || bounds.Dy() != 128 {
		t.Errorf("expected dimensions 128x128, got %dx%d", bounds.Dx(), bounds.Dy())
	}
}

// ============================================================================
// 3. Challenge Frontend & Preferences Edge Cases
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

	// Test 3.1: Empty GatewayURL fallback in Migrate
	emptyURLPref := preferences.Preferences{
		SchemaVersion: preferences.SchemaVersion,
		Theme:         preferences.ThemeDark,
		Language:      preferences.LanguageEnglish,
		GatewayURL:    "",
	}
	migratedEmpty, err := preferences.Migrate(emptyURLPref)
	if err != nil {
		t.Fatalf("Migrate failed: %v", err)
	}
	if migratedEmpty.GatewayURL != "http://127.0.0.1:8080" {
		t.Errorf("empty GatewayURL must fall back to default, got %q", migratedEmpty.GatewayURL)
	}

	// Test 3.2: Long URL persistence (10,000 characters)
	longURL := "http://gateway.enterprise-domain-proxy.internal:8080/path/" + strings.Repeat("sub/", 100)

	longPref := preferences.Preferences{
		SchemaVersion: preferences.SchemaVersion,
		Theme:         preferences.ThemeLight,
		Language:      preferences.LanguageVietnamese,
		GatewayURL:    longURL,
	}

	if err := repo.Save(ctx, longPref); err != nil {
		t.Fatalf("failed to save preferences with long url: %v", err)
	}

	loaded, found, err := repo.Load(ctx)
	if err != nil || !found {
		t.Fatalf("failed to load preferences: found=%v, err=%v", found, err)
	}
	if loaded.GatewayURL != longURL {
		t.Errorf("long GatewayURL was truncated or corrupted")
	}

	// Test 3.3: SQL Injection payload in GatewayURL
	sqlInjectionPref := preferences.Preferences{
		SchemaVersion: preferences.SchemaVersion,
		Theme:         preferences.ThemeDark,
		Language:      preferences.LanguageEnglish,
		GatewayURL:    "http://127.0.0.1:8080'; DROP TABLE user_settings; --",
	}

	if err := repo.Save(ctx, sqlInjectionPref); err != nil {
		t.Fatalf("Save with SQL injection payload failed: %v", err)
	}

	loadedInjection, found, err := repo.Load(ctx)
	if err != nil || !found {
		t.Fatalf("failed to load after injection payload: %v", err)
	}
	if loadedInjection.GatewayURL != sqlInjectionPref.GatewayURL {
		t.Errorf("SQL injection altered the stored URL: %s", loadedInjection.GatewayURL)
	}

	// Test 3.4: Unicode and Emoji preservation
	unicodePref := preferences.Preferences{
		SchemaVersion: preferences.SchemaVersion,
		Theme:         preferences.ThemeSystem,
		Language:      preferences.LanguageVietnamese,
		GatewayURL:    "http://cổng-ai.máy-chủ.vn:8080/🔑-tiếng-việt-⭐",
	}
	if err := repo.Save(ctx, unicodePref); err != nil {
		t.Fatalf("Save unicode pref failed: %v", err)
	}

	loadedUnicode, found, err := repo.Load(ctx)
	if err != nil || !found {
		t.Fatalf("failed to load unicode pref: %v", err)
	}
	if loadedUnicode.GatewayURL != unicodePref.GatewayURL {
		t.Errorf("Unicode GatewayURL corrupted: expected %s, got %s", unicodePref.GatewayURL, loadedUnicode.GatewayURL)
	}
}
