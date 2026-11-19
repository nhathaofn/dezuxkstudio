package core_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"app/internal/config"
	"app/internal/contracts"
	"app/internal/core"
	domainprefs "app/internal/domain/preferences"
	"app/internal/paths"
	"app/internal/security"
	"app/internal/storage/sqlite"
	"app/internal/tools/watermark"
)

func generateTestPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 16, 16))
	draw.Draw(img, img.Bounds(), &image.Uniform{C: color.RGBA{R: 50, G: 120, B: 200, A: 255}}, image.Point{}, draw.Src)
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

func TestCore_OptionsValidation(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := config.Default(config.ModeDesktop)
	p := paths.Paths{Data: t.TempDir()}

	// Missing Config
	if _, err := core.New(ctx, core.Options{Paths: p, Logger: logger, OpenDirectory: func(string) error { return nil }, HTTPClient: http.DefaultClient}); err == nil {
		t.Error("expected error with missing Config, got nil")
	}

	// Missing Logger
	if _, err := core.New(ctx, core.Options{Config: cfg, Paths: p, OpenDirectory: func(string) error { return nil }, HTTPClient: http.DefaultClient}); err == nil {
		t.Error("expected error with missing Logger, got nil")
	}

	// Missing OpenDirectory
	if _, err := core.New(ctx, core.Options{Config: cfg, Paths: p, Logger: logger, HTTPClient: http.DefaultClient}); err == nil {
		t.Error("expected error with missing OpenDirectory, got nil")
	}

	// Missing HTTPClient
	if _, err := core.New(ctx, core.Options{Config: cfg, Paths: p, Logger: logger, OpenDirectory: func(string) error { return nil }}); err == nil {
		t.Error("expected error with missing HTTPClient, got nil")
	}
}

type testFrame struct {
	Type    contracts.MessageType `json:"type"`
	ID      string                `json:"id"`
	Success bool                  `json:"success"`
	Data    json.RawMessage       `json:"data"`
	Error   *contracts.ErrorBody  `json:"error"`
}

func dialCoreWS(t *testing.T, wsURL, token string) (*websocket.Conn, func(action string, payload any) (testFrame, error)) {
	t.Helper()
	var subprotocols []string
	if token != "" {
		subprotocols = []string{contracts.ProtocolVersion, contracts.AuthSubprotocolPrefix + token}
	} else {
		subprotocols = []string{contracts.ProtocolVersion}
	}

	wsConn, _, err := websocket.Dial(context.Background(), wsURL, &websocket.DialOptions{
		Subprotocols: subprotocols,
	})
	if err != nil {
		t.Fatalf("websocket.Dial: %v", err)
	}

	seq := 0
	caller := func(action string, payload any) (testFrame, error) {
		t.Helper()
		seq++
		raw, err := json.Marshal(payload)
		if err != nil {
			return testFrame{}, err
		}
		id := action + "-" + string(rune('a'+seq))
		req := contracts.ClientMessage{
			Type:    contracts.MessageAction,
			ID:      id,
			Action:  action,
			Payload: raw,
		}
		data, err := json.Marshal(req)
		if err != nil {
			return testFrame{}, err
		}
		if err := wsConn.Write(context.Background(), websocket.MessageText, data); err != nil {
			return testFrame{}, err
		}

		for {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_, respBytes, err := wsConn.Read(ctx)
			cancel()
			if err != nil {
				return testFrame{}, err
			}
			var resp testFrame
			if err := json.Unmarshal(respBytes, &resp); err != nil {
				continue
			}
			if resp.Type == contracts.MessageResponse && resp.ID == id {
				return resp, nil
			}
		}
	}

	return wsConn, caller
}

func TestCore_PreferencesAndWatermarkWiring(t *testing.T) {
	tempDir := t.TempDir()
	p, err := paths.Resolve("core_test", paths.Overrides{DataDir: filepath.Join(tempDir, "data"), LogDir: filepath.Join(tempDir, "logs")})
	if err != nil {
		t.Fatalf("resolve paths: %v", err)
	}
	p.Config = filepath.Join(tempDir, "config")
	if err := p.Ensure(); err != nil {
		t.Fatalf("ensure paths: %v", err)
	}

	cfg := config.Default(config.ModeDesktop)
	// Tests must never download the real agent CLIs in the background.
	cfg.Agent.AutoInstallCLI, cfg.Agent.AutoUpdateCLI = false, false
	cfg.Server.Host = "127.0.0.1"
	cfg.Server.Port = 0
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	token, _ := security.NewToken()

	c, err := core.New(context.Background(), core.Options{
		Config:        cfg,
		Paths:         p,
		Logger:        logger,
		AuthToken:     token,
		OpenDirectory: func(string) error { return nil },
		HTTPClient:    http.DefaultClient,
	})
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}
	if err := c.Start(); err != nil {
		t.Fatalf("core.Start: %v", err)
	}
	defer func() {
		if err := c.Close(); err != nil {
			t.Errorf("core.Close: %v", err)
		}
	}()

	wsConn, call := dialCoreWS(t, c.WSURL(), token)
	defer wsConn.CloseNow()

	// 1. Update theme & language preferences
	patchResp, err := call(contracts.ActionPreferencesUpdate, map[string]string{
		"theme":    "dark",
		"language": "vi",
	})
	if err != nil {
		t.Fatalf("preferences:update failed: %v", err)
	}
	if !patchResp.Success {
		t.Fatalf("preferences:update returned failure: %+v", patchResp.Error)
	}

	getResp, err := call(contracts.ActionPreferencesGet, contracts.Empty{})
	if err != nil {
		t.Fatalf("preferences:get failed: %v", err)
	}
	var prefs contracts.Preferences
	if err := json.Unmarshal(getResp.Data, &prefs); err != nil {
		t.Fatalf("unmarshal preferences: %v", err)
	}
	if prefs.Theme != "dark" || prefs.Language != "vi" {
		t.Fatalf("expected dark/vi preferences, got %+v", prefs)
	}

	// 2. Run watermark remover node
	imgPath := filepath.Join(tempDir, "sample.png")
	if err := os.WriteFile(imgPath, generateTestPNG(t), 0o644); err != nil {
		t.Fatalf("write sample png: %v", err)
	}
	runResp, err := call(contracts.ActionWorkflowRunNode, contracts.WorkflowRunNodePayload{
		Type:  watermark.NodeType,
		Input: json.RawMessage(`{"image": "` + filepath.ToSlash(imgPath) + `"}`),
	})
	if err != nil {
		t.Fatalf("workflow:run_node failed: %v", err)
	}
	if !runResp.Success {
		t.Fatalf("workflow:run_node returned failure: %+v", runResp.Error)
	}
}

func TestCore_StagingGCAndBinariesWiring(t *testing.T) {
	tempDir := t.TempDir()
	p, err := paths.Resolve("core_test_staging", paths.Overrides{
		DataDir: filepath.Join(tempDir, "data"),
		LogDir:  filepath.Join(tempDir, "logs"),
	})
	if err != nil {
		t.Fatalf("resolve paths: %v", err)
	}
	p.Config = filepath.Join(tempDir, "config")
	if err := p.Ensure(); err != nil {
		t.Fatalf("ensure paths: %v", err)
	}

	stagingDir := p.Staging()
	if err := os.MkdirAll(stagingDir, 0o755); err != nil {
		t.Fatalf("mkdir staging: %v", err)
	}

	staleFile := filepath.Join(stagingDir, "stale_mediagen.tmp")
	if err := os.WriteFile(staleFile, []byte("stale content"), 0o644); err != nil {
		t.Fatalf("write stale file: %v", err)
	}
	oldTime := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(staleFile, oldTime, oldTime); err != nil {
		t.Fatalf("chtimes stale file: %v", err)
	}

	freshFile := filepath.Join(stagingDir, "fresh_mediagen.tmp")
	if err := os.WriteFile(freshFile, []byte("fresh content"), 0o644); err != nil {
		t.Fatalf("write fresh file: %v", err)
	}

	cfg := config.Default(config.ModeDesktop)
	cfg.Agent.AutoInstallCLI, cfg.Agent.AutoUpdateCLI = false, false
	cfg.Server.Host = "127.0.0.1"
	cfg.Server.Port = 0
	cfg.Media.StagingTTL = 24 * time.Hour
	cfg.Media.StagingGCInterval = 50 * time.Millisecond
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	token, _ := security.NewToken()

	c, err := core.New(context.Background(), core.Options{
		Config:        cfg,
		Paths:         p,
		Logger:        logger,
		AuthToken:     token,
		OpenDirectory: func(string) error { return nil },
		HTTPClient:    http.DefaultClient,
	})
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}
	if err := c.Start(); err != nil {
		t.Fatalf("core.Start: %v", err)
	}

	// Verify that staleFile was deleted during StartStagingGC initial prune
	deadline := time.Now().Add(3 * time.Second)
	pruned := false
	for time.Now().Before(deadline) {
		if _, err := os.Stat(staleFile); os.IsNotExist(err) {
			pruned = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !pruned {
		t.Errorf("expected staleFile to be pruned by staging GC, but it still exists")
	}

	// Verify freshFile is retained
	if _, err := os.Stat(freshFile); err != nil {
		t.Errorf("expected freshFile to exist, got err: %v", err)
	}

	// Verify an aged file inside MediaGenStaging is protected by MediaGen's LeaseRegistry
	// and never removed by HTTP media-staging-cleanup even if older than TTL.
	leasedMediaGenDir := p.MediaGenStaging()
	if err := os.MkdirAll(leasedMediaGenDir, 0o755); err != nil {
		t.Fatalf("mkdir mediagen staging: %v", err)
	}
	leasedFile := filepath.Join(leasedMediaGenDir, "long_video_job.tmp")
	releaseLease := c.MediaGen().LeaseFile(leasedFile)
	defer releaseLease()
	if err := os.WriteFile(leasedFile, []byte("active render chunk"), 0o644); err != nil {
		t.Fatalf("write leased mediagen file: %v", err)
	}
	if err := os.Chtimes(leasedFile, oldTime, oldTime); err != nil {
		t.Fatalf("chtimes leased file: %v", err)
	}

	time.Sleep(150 * time.Millisecond)
	if _, err := os.Stat(leasedFile); err != nil {
		t.Errorf("expected leased mediagen staging file %q to survive both GC loops: %v", leasedFile, err)
	}

	// Verify clean shutdown
	closeStart := time.Now()
	if err := c.Close(); err != nil {
		t.Errorf("core.Close error: %v", err)
	}
	if time.Since(closeStart) > 3*time.Second {
		t.Errorf("core.Close took longer than expected: %v", time.Since(closeStart))
	}
}

func createTestWorkspace(t *testing.T, name string) string {
	t.Helper()
	home, err := os.UserHomeDir()
	var base string
	if err == nil && home != "" {
		docs := filepath.Join(home, "Documents")
		if fi, err := os.Stat(docs); err == nil && fi.IsDir() {
			base = docs
		} else {
			base = home
		}
	} else {
		base = filepath.Join(os.TempDir(), "..", "..", "TestWorkspaces")
	}
	dir := filepath.Join(base, fmt.Sprintf("test_ws_%s_%d", name, time.Now().UnixNano()))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("createTestWorkspace: %v", err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(dir)
	})
	return dir
}

func TestCore_DynamicTreeResolution_DefaultAndCustom(t *testing.T) {
	// 1. Core startup with default preferences
	tempDir1 := t.TempDir()
	p1, err := paths.Resolve("core_tree_default", paths.Overrides{
		DataDir: filepath.Join(tempDir1, "data"),
		LogDir:  filepath.Join(tempDir1, "logs"),
	})
	if err != nil {
		t.Fatalf("resolve paths: %v", err)
	}
	p1.Config = filepath.Join(tempDir1, "config")
	_ = p1.Ensure()

	cfg1 := config.Default(config.ModeDesktop)
	cfg1.Agent.AutoInstallCLI, cfg1.Agent.AutoUpdateCLI = false, false
	cfg1.Server.Host = "127.0.0.1"
	cfg1.Server.Port = 0
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	token1, _ := security.NewToken()

	c1, err := core.New(context.Background(), core.Options{
		Config:        cfg1,
		Paths:         p1,
		Logger:        logger,
		AuthToken:     token1,
		OpenDirectory: func(string) error { return nil },
		HTTPClient:    http.DefaultClient,
	})
	if err != nil {
		t.Fatalf("core.New default: %v", err)
	}
	defer c1.Close()

	tree1 := c1.WorkspaceTree()
	if tree1.Root != p1.DefaultStudioRoot() {
		t.Fatalf("expected root=%q, got %q", p1.DefaultStudioRoot(), tree1.Root)
	}
	// Verify directory existence
	for _, dir := range []string{tree1.Root, tree1.MediaDir, tree1.ImagesDir, tree1.VideosDir, tree1.TTSDir, tree1.WatermarkDir, tree1.WorkspaceDir} {
		if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
			t.Fatalf("expected directory %q to exist, err=%v", dir, err)
		}
	}

	// 2. Core startup with pre-seeded custom workspace root
	tempDir2 := t.TempDir()
	p2, err := paths.Resolve("core_tree_custom", paths.Overrides{
		DataDir: filepath.Join(tempDir2, "data"),
		LogDir:  filepath.Join(tempDir2, "logs"),
	})
	if err != nil {
		t.Fatalf("resolve paths: %v", err)
	}
	p2.Config = filepath.Join(tempDir2, "config")
	_ = p2.Ensure()

	customRoot := createTestWorkspace(t, "seed")
	// Pre-seed SQLite preferences
	cfg2 := config.Default(config.ModeDesktop)
	cfg2.Agent.AutoInstallCLI, cfg2.Agent.AutoUpdateCLI = false, false
	cfg2.Server.Host = "127.0.0.1"
	cfg2.Server.Port = 0

	dbPath := filepath.Join(p2.Data, cfg2.Storage.DBFile)
	db, err := sqlite.Open(context.Background(), sqlite.Options{
		Path:          dbPath,
		BackupDir:     p2.Backups(),
		BackupsToKeep: cfg2.Storage.BackupsToKeep,
		BusyTimeout:   cfg2.Storage.BusyTimeout,
		Logger:        logger,
	})
	if err != nil {
		t.Fatalf("seed db open: %v", err)
	}
	repo := sqlite.NewPreferencesRepository(db)
	seededPrefs := domainprefs.Defaults()
	seededPrefs.WorkspaceRoot = customRoot
	if err := repo.Save(context.Background(), seededPrefs); err != nil {
		_ = db.Close()
		t.Fatalf("seed preferences save: %v", err)
	}
	_ = db.Close()

	token2, _ := security.NewToken()
	c2, err := core.New(context.Background(), core.Options{
		Config:        cfg2,
		Paths:         p2,
		Logger:        logger,
		AuthToken:     token2,
		OpenDirectory: func(string) error { return nil },
		HTTPClient:    http.DefaultClient,
	})
	if err != nil {
		t.Fatalf("core.New custom: %v", err)
	}
	defer c2.Close()

	tree2 := c2.WorkspaceTree()
	if tree2.Root != customRoot {
		t.Fatalf("expected custom root=%q, got %q", customRoot, tree2.Root)
	}
	for _, dir := range []string{tree2.Root, tree2.MediaDir, tree2.ImagesDir, tree2.VideosDir, tree2.TTSDir, tree2.WatermarkDir, tree2.WorkspaceDir} {
		if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
			t.Fatalf("expected custom directory %q to exist, err=%v", dir, err)
		}
	}
}

func TestCore_HotReloadPropagation_OnPreferencesUpdate(t *testing.T) {
	tempDir := t.TempDir()
	p, err := paths.Resolve("core_hotreload", paths.Overrides{
		DataDir: filepath.Join(tempDir, "data"),
		LogDir:  filepath.Join(tempDir, "logs"),
	})
	if err != nil {
		t.Fatalf("resolve paths: %v", err)
	}
	p.Config = filepath.Join(tempDir, "config")
	_ = p.Ensure()

	cfg := config.Default(config.ModeDesktop)
	cfg.Agent.AutoInstallCLI, cfg.Agent.AutoUpdateCLI = false, false
	cfg.Server.Host = "127.0.0.1"
	cfg.Server.Port = 0
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	token, _ := security.NewToken()

	c, err := core.New(context.Background(), core.Options{
		Config:        cfg,
		Paths:         p,
		Logger:        logger,
		AuthToken:     token,
		OpenDirectory: func(string) error { return nil },
		HTTPClient:    http.DefaultClient,
	})
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}
	if err := c.Start(); err != nil {
		t.Fatalf("core.Start: %v", err)
	}
	defer c.Close()

	wsConn, call := dialCoreWS(t, c.WSURL(), token)
	defer wsConn.CloseNow()

	// 1. Initial system:info matches default studio root
	infoResp, err := call(contracts.ActionSystemInfo, contracts.Empty{})
	if err != nil {
		t.Fatalf("system:info initial failed: %v", err)
	}
	var sysInfo contracts.SystemInfo
	if err := json.Unmarshal(infoResp.Data, &sysInfo); err != nil {
		t.Fatalf("unmarshal sysInfo: %v", err)
	}
	if sysInfo.Paths.WorkspaceRoot != p.DefaultStudioRoot() {
		t.Fatalf("expected initial WorkspaceRoot=%q, got %q", p.DefaultStudioRoot(), sysInfo.Paths.WorkspaceRoot)
	}

	// 2. Update to custom workspace root 1
	custom1 := createTestWorkspace(t, "hot1")
	patchResp, err := call(contracts.ActionPreferencesUpdate, map[string]any{
		"workspace_root": custom1,
	})
	if err != nil {
		t.Fatalf("preferences:update custom1 failed: %v", err)
	}
	if !patchResp.Success {
		t.Fatalf("preferences:update custom1 returned error: %+v", patchResp.Error)
	}
	var updated1 contracts.Preferences
	if err := json.Unmarshal(patchResp.Data, &updated1); err != nil {
		t.Fatalf("unmarshal updated1: %v", err)
	}
	if updated1.WorkspaceRoot != custom1 {
		t.Fatalf("expected updated1.WorkspaceRoot=%q, got %q", custom1, updated1.WorkspaceRoot)
	}

	// Dynamic tree snapshot must now reflect custom1
	if c.WorkspaceTree().Root != custom1 {
		t.Fatalf("c.WorkspaceTree().Root=%q, want %q", c.WorkspaceTree().Root, custom1)
	}
	// Verify directory was created
	if fi, err := os.Stat(filepath.Join(custom1, "media", "images")); err != nil || !fi.IsDir() {
		t.Fatalf("expected custom1 images dir to exist: %v", err)
	}

	// system:info must dynamically reflect custom1
	infoResp2, err := call(contracts.ActionSystemInfo, contracts.Empty{})
	if err != nil {
		t.Fatalf("system:info 2 failed: %v", err)
	}
	var sysInfo2 contracts.SystemInfo
	if err := json.Unmarshal(infoResp2.Data, &sysInfo2); err != nil {
		t.Fatalf("unmarshal sysInfo2: %v", err)
	}
	if sysInfo2.Paths.WorkspaceRoot != custom1 {
		t.Fatalf("expected sysInfo2.WorkspaceRoot=%q, got %q", custom1, sysInfo2.Paths.WorkspaceRoot)
	}
	if sysInfo2.Paths.MediaDir != filepath.Join(custom1, "media") {
		t.Fatalf("expected sysInfo2.MediaDir=%q, got %q", filepath.Join(custom1, "media"), sysInfo2.Paths.MediaDir)
	}

	// 3. Update to custom workspace root 2: custom1 should be automatically added to allowed dirs
	custom2 := createTestWorkspace(t, "hot2")
	patchResp2, err := call(contracts.ActionPreferencesUpdate, map[string]any{
		"workspace_root": custom2,
	})
	if err != nil {
		t.Fatalf("preferences:update custom2 failed: %v", err)
	}
	if !patchResp2.Success {
		t.Fatalf("preferences:update custom2 returned error: %+v", patchResp2.Error)
	}

	if c.WorkspaceTree().Root != custom2 {
		t.Fatalf("c.WorkspaceTree().Root=%q, want %q", c.WorkspaceTree().Root, custom2)
	}

	// Query agent:list_allowed_dirs to confirm custom1 was added to keep media read permissions
	allowedResp, err := call(contracts.ActionAgentListAllowedDirs, contracts.Empty{})
	if err != nil {
		t.Fatalf("agent:list_allowed_dirs failed: %v", err)
	}
	var allowedDirs contracts.AgentAllowedDirsResult
	if err := json.Unmarshal(allowedResp.Data, &allowedDirs); err != nil {
		t.Fatalf("unmarshal allowedDirs: %v", err)
	}
	foundCustom1 := false
	for _, d := range allowedDirs.Dirs {
		if filepath.Clean(d) == filepath.Clean(custom1) {
			foundCustom1 = true
			break
		}
	}
	if !foundCustom1 {
		t.Fatalf("expected previous custom root %q in allowed dirs, got: %v", custom1, allowedDirs.Dirs)
	}

	// 4. Update agent_skip_trusted_approval
	patchResp3, err := call(contracts.ActionPreferencesUpdate, map[string]any{
		"agent_skip_trusted_approval": true,
	})
	if err != nil {
		t.Fatalf("preferences:update skip_trusted failed: %v", err)
	}
	if !patchResp3.Success {
		t.Fatalf("preferences:update skip_trusted returned error: %+v", patchResp3.Error)
	}
	var updated3 contracts.Preferences
	if err := json.Unmarshal(patchResp3.Data, &updated3); err != nil {
		t.Fatalf("unmarshal updated3: %v", err)
	}
	if !updated3.AgentSkipTrustedApproval {
		t.Fatalf("expected AgentSkipTrustedApproval=true, got false")
	}
}

func TestCore_ServerMode_PreferencesLockdown(t *testing.T) {
	tempDir := t.TempDir()
	p, err := paths.Resolve("core_server_lock", paths.Overrides{
		DataDir: filepath.Join(tempDir, "data"),
		LogDir:  filepath.Join(tempDir, "logs"),
	})
	if err != nil {
		t.Fatalf("resolve paths: %v", err)
	}
	p.Config = filepath.Join(tempDir, "config")
	_ = p.Ensure()

	cfg := config.Default(config.ModeServer)
	cfg.Agent.AutoInstallCLI, cfg.Agent.AutoUpdateCLI = false, false
	cfg.Server.Host = "127.0.0.1"
	cfg.Server.Port = 0
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	token, _ := security.NewToken()

	c, err := core.New(context.Background(), core.Options{
		Config:        cfg,
		Paths:         p,
		Logger:        logger,
		AuthToken:     token,
		OpenDirectory: func(string) error { return nil },
		HTTPClient:    http.DefaultClient,
	})
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}
	if err := c.Start(); err != nil {
		t.Fatalf("core.Start: %v", err)
	}
	defer c.Close()

	wsConn, call := dialCoreWS(t, c.WSURL(), token)
	defer wsConn.CloseNow()

	const wantMsg = "trusted folders and workspace settings can only be changed in the desktop app on this machine"

	assertUnavailable := func(name string, payload any) {
		t.Helper()
		resp, err := call(contracts.ActionPreferencesUpdate, payload)
		if err != nil {
			t.Fatalf("%s: call failed: %v", name, err)
		}
		if resp.Success {
			t.Fatalf("%s: expected failure, got success", name)
		}
		if resp.Error == nil || resp.Error.Code != "ERR_UNAVAILABLE" {
			t.Fatalf("%s: expected ERR_UNAVAILABLE, got %+v", name, resp.Error)
		}
		if resp.Error.Message != wantMsg {
			t.Fatalf("%s: expected error message %q, got %q", name, wantMsg, resp.Error.Message)
		}
	}

	assertUnavailable("workspace_root", map[string]any{"workspace_root": filepath.Join(tempDir, "custom")})
	assertUnavailable("onboarding_completed", map[string]any{"onboarding_completed": true})
	assertUnavailable("agent_skip_trusted_approval", map[string]any{"agent_skip_trusted_approval": true})

	// Theme update in server mode must be allowed
	themeResp, err := call(contracts.ActionPreferencesUpdate, map[string]any{"theme": "dark"})
	if err != nil {
		t.Fatalf("theme update call failed: %v", err)
	}
	if !themeResp.Success {
		t.Fatalf("expected theme update to succeed, got error: %+v", themeResp.Error)
	}
}

func TestCore_DefaultStudioMediaRetentionAcrossRestart_And_TempDirRejected(t *testing.T) {
	tempDir := t.TempDir()
	p, err := paths.Resolve("core_restart_retention", paths.Overrides{
		DataDir: filepath.Join(tempDir, "data"),
		LogDir:  filepath.Join(tempDir, "logs"),
	})
	if err != nil {
		t.Fatalf("resolve paths: %v", err)
	}
	p.Config = filepath.Join(tempDir, "config")
	_ = p.Ensure()

	cfg := config.Default(config.ModeDesktop)
	cfg.Agent.AutoInstallCLI, cfg.Agent.AutoUpdateCLI = false, false
	cfg.Server.Host = "127.0.0.1"
	cfg.Server.Port = 0
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	token1, _ := security.NewToken()

	c1, err := core.New(context.Background(), core.Options{
		Config:        cfg,
		Paths:         p,
		Logger:        logger,
		AuthToken:     token1,
		OpenDirectory: func(string) error { return nil },
		HTTPClient:    http.DefaultClient,
	})
	if err != nil {
		t.Fatalf("core.New #1: %v", err)
	}
	if err := c1.Start(); err != nil {
		t.Fatalf("core.Start #1: %v", err)
	}

	ws1, call1 := dialCoreWS(t, c1.WSURL(), token1)

	// 1. Register an asset while using System Default (<Data>/studio/media)
	defaultImg := filepath.Join(p.DefaultStudioRoot(), "media", "images", "default_created.png")
	if err := os.MkdirAll(filepath.Dir(defaultImg), 0o755); err != nil {
		t.Fatalf("mkdir default studio media: %v", err)
	}
	imgBytes := generateTestPNG(t)
	if err := os.WriteFile(defaultImg, imgBytes, 0o644); err != nil {
		t.Fatalf("write default png: %v", err)
	}
	regResp, err := call1(contracts.ActionMediaRegisterInput, contracts.MediaRegisterInputPayload{
		FilePath:   defaultImg,
		Kind:       "image",
		SourceNode: "test",
	})
	if err != nil || !regResp.Success {
		t.Fatalf("register default studio asset failed: err=%v resp=%+v", err, regResp.Error)
	}
	var asset contracts.MediaAssetItem
	if err := json.Unmarshal(regResp.Data, &asset); err != nil {
		t.Fatalf("unmarshal asset: %v", err)
	}

	// 2. Verify os.TempDir() inside LocalAppData cannot bypass WorkspaceRoot protection
	if localApp := os.Getenv("LOCALAPPDATA"); localApp != "" {
		tempSub := filepath.Join(localApp, "Temp", "dezuxk_should_reject")
		rejResp, err := call1(contracts.ActionPreferencesUpdate, map[string]any{
			"workspace_root": tempSub,
		})
		if err != nil {
			t.Fatalf("preferences:update tempSub call failed: %v", err)
		}
		if rejResp.Success || rejResp.Error == nil || rejResp.Error.Code != "ERR_VALIDATION" {
			t.Fatalf("expected ERR_VALIDATION for Temp dir inside LocalAppData, got success=%v err=%+v", rejResp.Success, rejResp.Error)
		}
	}

	// 3. Switch away from System Default to a custom WorkspaceRoot
	customRoot := createTestWorkspace(t, "after_default")
	updResp, err := call1(contracts.ActionPreferencesUpdate, map[string]any{
		"workspace_root":       customRoot,
		"onboarding_completed": true,
	})
	if err != nil || !updResp.Success {
		t.Fatalf("switch to custom root failed: err=%v resp=%+v", err, updResp.Error)
	}

	ws1.CloseNow()
	if err := c1.Close(); err != nil {
		t.Fatalf("core.Close #1: %v", err)
	}

	// 4. Restart Core with the same DB/paths and verify old System Default asset is still downloadable
	token2, _ := security.NewToken()
	c2, err := core.New(context.Background(), core.Options{
		Config:        cfg,
		Paths:         p,
		Logger:        logger,
		AuthToken:     token2,
		OpenDirectory: func(string) error { return nil },
		HTTPClient:    http.DefaultClient,
	})
	if err != nil {
		t.Fatalf("core.New #2: %v", err)
	}
	if err := c2.Start(); err != nil {
		t.Fatalf("core.Start #2: %v", err)
	}
	defer func() {
		if err := c2.Close(); err != nil {
			t.Errorf("core.Close #2: %v", err)
		}
	}()

	httpBase := "http" + c2.WSURL()[len("ws"):len(c2.WSURL())-len("/ws")]
	req, _ := http.NewRequest(http.MethodGet, httpBase+"/api/media/download?asset_id="+asset.ID, nil)
	req.Header.Set("Authorization", "Bearer "+token2)
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("download asset after restart: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK for default studio asset after switching root and restarting, got %d", resp.StatusCode)
	}
	gotBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}
	if !bytes.Equal(gotBytes, imgBytes) {
		t.Fatalf("downloaded asset bytes mismatch after restart")
	}
}

func TestCore_StudioWorkspaceRootHotReloadAndWorkflowGuard(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()
	p := paths.Paths{
		Config: filepath.Join(tmp, "config"),
		Data:   filepath.Join(tmp, "data"),
		Logs:   filepath.Join(tmp, "logs"),
	}
	if err := p.Ensure(); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default(config.ModeDesktop)
	cfg.Providers.CheckOnStartup = false
	cfg.Providers.AutoCheck = false
	token, _ := security.NewToken()

	c, err := core.New(ctx, core.Options{
		Config:        cfg,
		Paths:         p,
		Logger:        slog.New(slog.DiscardHandler),
		AuthToken:     token,
		OpenDirectory: func(string) error { return nil },
		HTTPClient:    http.DefaultClient,
	})
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}
	if err := c.Start(); err != nil {
		t.Fatalf("core.Start: %v", err)
	}
	defer func() { _ = c.Close() }()

	initProjectsDir := c.WorkspaceTree().ProjectsDir
	if initProjectsDir == "" || c.Studio().ProjectsDir() != initProjectsDir {
		t.Fatalf("expected initial ProjectsDir=%q matching studioSvc=%q", initProjectsDir, c.Studio().ProjectsDir())
	}

	ws, call := dialCoreWS(t, c.WSURL(), token)
	defer ws.CloseNow()

	// Create initial project in default ProjectsDir
	r1, err := call(contracts.ActionStudioProjectCreate, map[string]any{"title": "First Film"})
	if err != nil || !r1.Success {
		t.Fatalf("studio:project_create #1 failed: err=%v resp=%+v", err, r1.Error)
	}
	var detail1 contracts.StudioProjectDetailResult
	if err := json.Unmarshal(r1.Data, &detail1); err != nil {
		t.Fatal(err)
	}
	firstFilmArtifact := filepath.Join(detail1.Project.RootDir, "episodes", "ep01", "script", "normalized_script.json")

	// Switch WorkspaceRoot via preferences:update
	newRoot := createTestWorkspace(t, "studio_hot_reload")
	upd, err := call(contracts.ActionPreferencesUpdate, map[string]any{
		"workspace_root":       newRoot,
		"onboarding_completed": true,
	})
	if err != nil || !upd.Success {
		t.Fatalf("preferences:update failed: err=%v resp=%+v", err, upd.Error)
	}

	wantNewProjectsDir := filepath.Join(newRoot, "workspace", "projects")
	if !strings.EqualFold(filepath.Clean(c.Studio().ProjectsDir()), filepath.Clean(wantNewProjectsDir)) {
		t.Fatalf("expected hot-reloaded ProjectsDir=%q, got %q", wantNewProjectsDir, c.Studio().ProjectsDir())
	}

	// "First Film" in the previous default <Data>/studio/workspace/projects must remain trusted & editable after hot-reload
	if err := c.Agent().CheckTrustedFile(firstFilmArtifact, true); err != nil {
		t.Fatalf("CheckTrustedFile should retain access to First Film artifact %q after hot-reload: %v", firstFilmArtifact, err)
	}
	saveFirst, err := call(contracts.ActionStudioEpisodeSaveScript, map[string]any{
		"episode_id": detail1.ActiveEpisode.Episode.ID,
		"raw_script": "Cảnh mở đầu giữ nguyên sau khi đổi thư mục gốc.",
	})
	if err != nil || !saveFirst.Success {
		t.Fatalf("First Film episode_save_script after hot-reload failed: err=%v resp=%+v", err, saveFirst.Error)
	}

	// New project should be created inside the updated ProjectsDir
	r2, err := call(contracts.ActionStudioProjectCreate, map[string]any{"title": "Second Film"})
	if err != nil || !r2.Success {
		t.Fatalf("studio:project_create #2 failed: err=%v resp=%+v", err, r2.Error)
	}
	var detail2 contracts.StudioProjectDetailResult
	if err := json.Unmarshal(r2.Data, &detail2); err != nil {
		t.Fatal(err)
	}
	expectedPrefix := filepath.Clean(wantNewProjectsDir)
	if !strings.HasPrefix(strings.ToLower(filepath.Clean(detail2.Project.RootDir)), strings.ToLower(expectedPrefix)) {
		t.Fatalf("expected new project RootDir inside %q, got %q", expectedPrefix, detail2.Project.RootDir)
	}

	// Workflow FileGuard (CheckTrustedFile) must permit files in new ProjectsDir & reject untrusted external dirs
	newScriptArtifact := filepath.Join(detail2.Project.RootDir, "episodes", "ep01", "script", "normalized_script.json")
	if err := c.Agent().CheckTrustedFile(newScriptArtifact, true); err != nil {
		t.Fatalf("CheckTrustedFile should permit new ProjectsDir path %q, got %v", newScriptArtifact, err)
	}
	untrustedDir := t.TempDir()
	outsideFile := filepath.Join(untrustedDir, "evil.json")
	if err := c.Agent().CheckTrustedFile(outsideFile, true); err == nil {
		t.Fatalf("CheckTrustedFile should reject untrusted path %q", outsideFile)
	}

	// Restart Core with same Paths/DB and verify "First Film" remains trusted and accessible
	ws.CloseNow()
	if err := c.Close(); err != nil {
		t.Fatalf("core.Close #1: %v", err)
	}

	token2, _ := security.NewToken()
	c2, err := core.New(ctx, core.Options{
		Config:        cfg,
		Paths:         p,
		Logger:        slog.New(slog.DiscardHandler),
		AuthToken:     token2,
		OpenDirectory: func(string) error { return nil },
		HTTPClient:    http.DefaultClient,
	})
	if err != nil {
		t.Fatalf("core.New #2: %v", err)
	}
	if err := c2.Start(); err != nil {
		t.Fatalf("core.Start #2: %v", err)
	}
	defer func() { _ = c2.Close() }()

	if err := c2.Agent().CheckTrustedFile(firstFilmArtifact, true); err != nil {
		t.Fatalf("CheckTrustedFile should permit First Film artifact %q after restart: %v", firstFilmArtifact, err)
	}
	ws2, call2 := dialCoreWS(t, c2.WSURL(), token2)
	defer ws2.CloseNow()
	getFirst, err := call2(contracts.ActionStudioProjectGet, map[string]any{"id": detail1.Project.ID})
	if err != nil || !getFirst.Success {
		t.Fatalf("studio:project_get First Film after restart failed: err=%v resp=%+v", err, getFirst.Error)
	}
}

func TestCore_ServerModeBlocksMediaRegisterInputRootPoisoningAndDownload(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()
	p := paths.Paths{
		Config: filepath.Join(tmp, "config"),
		Data:   filepath.Join(tmp, "data"),
		Logs:   filepath.Join(tmp, "logs"),
	}
	if err := p.Ensure(); err != nil {
		t.Fatal(err)
	}

	cfg := config.Default(config.ModeServer)
	cfg.Server.Host = "127.0.0.1"
	cfg.Server.Port = 0
	cfg.Providers.CheckOnStartup = false
	cfg.Providers.AutoCheck = false
	token, _ := security.NewToken()

	c, err := core.New(ctx, core.Options{
		Config:        cfg,
		Paths:         p,
		Logger:        slog.New(slog.DiscardHandler),
		AuthToken:     token,
		OpenDirectory: func(string) error { return nil },
		HTTPClient:    http.DefaultClient,
	})
	if err != nil {
		t.Fatalf("core.New server: %v", err)
	}
	if err := c.Start(); err != nil {
		t.Fatalf("core.Start server: %v", err)
	}
	defer func() { _ = c.Close() }()

	ws, call := dialCoreWS(t, c.WSURL(), token)
	defer ws.CloseNow()

	outsideDir := t.TempDir()
	outsideImg := filepath.Join(outsideDir, "host_secret.png")
	if err := os.WriteFile(outsideImg, []byte("secret-png"), 0o600); err != nil {
		t.Fatal(err)
	}

	// 1. Remote client attempts media:register_input on untrusted host path -> MUST fail with ERR_UNAUTHORIZED
	regResp, err := call(contracts.ActionMediaRegisterInput, map[string]any{
		"file_path": outsideImg,
	})
	if err != nil {
		t.Fatalf("media:register_input transport err: %v", err)
	}
	if regResp.Success || regResp.Error == nil || regResp.Error.Code != "ERR_UNAUTHORIZED" {
		t.Fatalf("expected ERR_UNAUTHORIZED on server media:register_input, got success=%v err=%+v", regResp.Success, regResp.Error)
	}

	// 2. Subsequent GET /api/media/download?path=<outsideImg> MUST be 403 Forbidden (allowedRoots was NOT poisoned)
	httpBase := "http" + c.WSURL()[len("ws"):len(c.WSURL())-len("/ws")]
	dlReq, _ := http.NewRequest(http.MethodGet, httpBase+"/api/media/download?path="+url.QueryEscape(outsideImg), nil)
	dlReq.Header.Set("Authorization", "Bearer "+token)
	dlResp, err := (&http.Client{Timeout: 5 * time.Second}).Do(dlReq)
	if err != nil {
		t.Fatalf("download request failed: %v", err)
	}
	_ = dlResp.Body.Close()
	if dlResp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden for outside path download in server mode, got %d", dlResp.StatusCode)
	}

	// 3. Even inside MediaDir, secret/non-media files (.env, secrets.enc) cannot be downloaded via ?path=
	mediaSecret := filepath.Join(c.WorkspaceTree().MediaDir, "secrets.enc")
	if err := os.WriteFile(mediaSecret, []byte("encrypted-keys"), 0o600); err != nil {
		t.Fatal(err)
	}
	secReq, _ := http.NewRequest(http.MethodGet, httpBase+"/api/media/download?path=secrets.enc", nil)
	secReq.Header.Set("Authorization", "Bearer "+token)
	secResp, err := (&http.Client{Timeout: 5 * time.Second}).Do(secReq)
	if err != nil {
		t.Fatalf("download secrets.enc request failed: %v", err)
	}
	_ = secResp.Body.Close()
	if secResp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden for secrets.enc inside mediaDir, got %d", secResp.StatusCode)
	}
}

func TestCore_TicketSecretIndependentOfAuthTokenAndLoggerWired(t *testing.T) {
	newCore := func(t *testing.T, logBuf *bytes.Buffer, token string) *core.Core {
		t.Helper()
		tmp := t.TempDir()
		p, err := paths.Resolve("core_test", paths.Overrides{DataDir: filepath.Join(tmp, "data"), LogDir: filepath.Join(tmp, "logs")})
		if err != nil {
			t.Fatal(err)
		}
		p.Config = filepath.Join(tmp, "config")
		if err := p.Ensure(); err != nil {
			t.Fatal(err)
		}
		cfg := config.Default(config.ModeServer)
		cfg.Agent.AutoInstallCLI, cfg.Agent.AutoUpdateCLI = false, false
		cfg.Providers.CheckOnStartup = false
		cfg.Server.Host = "127.0.0.1"
		cfg.Server.Port = 0
		cfg.Log.Level = "debug"

		var lg *slog.Logger
		if logBuf != nil {
			lg = slog.New(slog.NewTextHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
		} else {
			lg = slog.New(slog.DiscardHandler)
		}

		c, err := core.New(context.Background(), core.Options{
			Config:        cfg,
			Paths:         p,
			Logger:        lg,
			AuthToken:     token,
			OpenDirectory: func(string) error { return nil },
			HTTPClient:    http.DefaultClient,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := c.Start(); err != nil {
			_ = c.Close()
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = c.Close() })
		return c
	}

	const sharedToken = "identical-server-auth-token"
	var logBuf bytes.Buffer
	c1 := newCore(t, &logBuf, sharedToken)
	c2 := newCore(t, nil, sharedToken)

	// Register a media file in c1 and sign a URL.
	samplePath := filepath.Join(c1.WorkspaceTree().MediaDir, "sample.png")
	if err := os.WriteFile(samplePath, generateTestPNG(t), 0o600); err != nil {
		t.Fatal(err)
	}
	ws1, call1 := dialCoreWS(t, c1.WSURL(), sharedToken)
	defer ws1.CloseNow()

	regResp, err := call1(contracts.ActionMediaRegisterInput, map[string]any{"file_path": samplePath})
	if err != nil || !regResp.Success {
		t.Fatalf("register_input failed: err=%v resp=%+v", err, regResp)
	}
	var regData contracts.MediaAssetItem
	if err := json.Unmarshal(regResp.Data, &regData); err != nil {
		t.Fatal(err)
	}
	signResp, err := call1(contracts.ActionMediaSignURL, map[string]any{"asset_id": regData.ID, "purpose": "preview"})
	if err != nil || !signResp.Success {
		t.Fatalf("sign_url failed: err=%v resp=%+v", err, signResp)
	}
	var signData struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(signResp.Data, &signData); err != nil {
		t.Fatal(err)
	}
	u1, err := url.Parse(signData.URL)
	if err != nil {
		t.Fatal(err)
	}
	ticket := u1.Query().Get("ticket")
	if ticket == "" {
		t.Fatalf("missing ticket in signed URL: %s", signData.URL)
	}

	// Ticket signed by c1 must NOT validate on c2 even with the exact same AuthToken.
	httpBase2 := "http" + c2.WSURL()[len("ws"):len(c2.WSURL())-len("/ws")]
	resp2, err := (&http.Client{Timeout: 5 * time.Second}).Get(httpBase2 + "/api/media/download?ticket=" + url.QueryEscape(ticket))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp2.Body.Close()
	if resp2.StatusCode == http.StatusOK {
		t.Fatalf("ticket from c1 must not be accepted by c2 with identical AuthToken")
	}

	// RequestLogger must be wired on c1 HTTP router.
	httpBase1 := "http" + c1.WSURL()[len("ws"):len(c1.WSURL())-len("/ws")]
	healthResp, err := (&http.Client{Timeout: 5 * time.Second}).Get(httpBase1 + "/health")
	if err != nil {
		t.Fatal(err)
	}
	_ = healthResp.Body.Close()
	if !strings.Contains(logBuf.String(), "/health") {
		t.Fatalf("expected RequestLogger to log /health, got logs: %s", logBuf.String())
	}
}
