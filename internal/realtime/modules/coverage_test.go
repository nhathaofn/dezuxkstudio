package modules_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"app/internal/apperr"
	"app/internal/config"
	"app/internal/contracts"
	domainprovider "app/internal/domain/provider"
	"app/internal/paths"
	"app/internal/realtime"
	"app/internal/realtime/modules"
	"app/internal/secrets"
	"app/internal/security"
	diagsvc "app/internal/service/diagnostics"
	mediasvc "app/internal/service/media"
	providersvc "app/internal/service/provider"
	"app/internal/service/workflow"
	"app/internal/storage/sqlite"
)

func TestDiagnosticsMediaProvidersWorkflowModules(t *testing.T) {
	root := t.TempDir()
	db, err := sqlite.Open(context.Background(), sqlite.Options{
		Path:          filepath.Join(root, "app.db"),
		BackupDir:     filepath.Join(root, "backups"),
		BackupsToKeep: 1,
		BusyTimeout:   time.Second,
		Logger:        slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	defer func() { _ = db.Close() }()

	// 1. Diagnostics module: LockHostAdmin blocks export; unlocked exports bundle.
	diagDir := filepath.Join(root, "diag")
	logDir := filepath.Join(root, "logs")
	_ = os.MkdirAll(logDir, 0o755)
	_ = os.WriteFile(filepath.Join(logDir, "app.log"), []byte("hello"), 0o644)
	dSvc, err := diagsvc.New(diagsvc.Options{
		OutputDir: diagDir,
		LogDir:    logDir,
		Keep:      2,
		Config:    func() map[string]string { return map[string]string{"mode": "desktop"} },
		Info:      func() contracts.SystemInfo { return contracts.SystemInfo{Mode: config.ModeDesktop} },
	})
	if err != nil {
		t.Fatalf("diagsvc.New: %v", err)
	}

	regLocked := realtime.NewRegistry(contracts.ActionNames())
	modules.Diagnostics{Service: dSvc, LockHostAdmin: true}.Register(regLocked)
	hDiagLocked, _ := regLocked.Lookup(contracts.ActionDiagnosticsExport)
	if _, err := hDiagLocked(context.Background(), &realtime.Request{}, []byte(`{}`)); !apperr.IsCode(err, apperr.CodeUnavailable) {
		t.Fatalf("expected ERR_UNAVAILABLE when diagnostics LockHostAdmin=true, got %v", err)
	}

	regOpen := realtime.NewRegistry(contracts.ActionNames())
	modules.Diagnostics{Service: dSvc, LockHostAdmin: false}.Register(regOpen)
	hDiagOpen, _ := regOpen.Lookup(contracts.ActionDiagnosticsExport)
	bundleRaw, err := hDiagOpen(context.Background(), &realtime.Request{}, []byte(`{}`))
	if err != nil {
		t.Fatalf("diagnostics:export failed: %v", err)
	}
	if bundleRaw.(contracts.DiagnosticsBundle).Path == "" {
		t.Fatal("expected exported diagnostics bundle path")
	}

	// 2. Media module: register input guarded by FileGuard + list + sign + delete.
	mediaDir := filepath.Join(root, "media")
	_ = os.MkdirAll(mediaDir, 0o755)
	imgPath := filepath.Join(mediaDir, "sample.png")
	_ = os.WriteFile(imgPath, []byte{0x89, 'P', 'N', 'G'}, 0o644)

	mSvc := mediasvc.New(mediasvc.Options{
		Repo:     sqlite.NewMediaRepository(db),
		Signer:   security.NewTicketSigner([]byte("0123456789abcdef0123456789abcdef")),
		MediaDir: mediaDir,
		Logger:   slog.New(slog.DiscardHandler),
	})
	modules.Media{
		Service: mSvc,
		FileGuard: func(p string, _ bool) error {
			if filepath.Base(p) == "blocked.png" {
				return apperr.New(apperr.CodeUnauthorized, "blocked")
			}
			return nil
		},
	}.Register(regOpen)

	hReg, _ := regOpen.Lookup(contracts.ActionMediaRegisterInput)
	if _, err := hReg(context.Background(), &realtime.Request{}, []byte(`{"file_path":"`+filepath.ToSlash(filepath.Join(mediaDir, "blocked.png"))+`","kind":"image"}`)); !apperr.IsCode(err, apperr.CodeUnauthorized) {
		t.Fatalf("expected FileGuard to block blocked.png register_input, got %v", err)
	}
	regRes, err := hReg(context.Background(), &realtime.Request{}, []byte(`{"file_path":"`+filepath.ToSlash(imgPath)+`","kind":"image"}`))
	if err != nil {
		t.Fatalf("media:register_input failed: %v", err)
	}
	assetID := regRes.(contracts.MediaAssetItem).ID

	hList, _ := regOpen.Lookup(contracts.ActionMediaList)
	listRes, err := hList(context.Background(), &realtime.Request{}, []byte(`{}`))
	if err != nil || len(listRes.(contracts.MediaListResult).Assets) == 0 {
		t.Fatalf("media:list failed: %v", err)
	}

	hSign, _ := regOpen.Lookup(contracts.ActionMediaSignURL)
	signRes, err := hSign(context.Background(), &realtime.Request{}, []byte(`{"asset_id":"`+assetID+`","purpose":"preview"}`))
	if err != nil || signRes.(contracts.MediaSignedURLResult).Ticket == "" {
		t.Fatalf("media:sign_url failed: %v", err)
	}

	hDel, _ := regOpen.Lookup(contracts.ActionMediaDelete)
	if _, err := hDel(context.Background(), &realtime.Request{}, []byte(`{"id":"`+assetID+`","delete_file":false}`)); err != nil {
		t.Fatalf("media:delete failed: %v", err)
	}

	// 3. Providers module: LockHostAdmin blocks mutators while List/Defaults work; unlocked allows update_settings.
	pSvc, err := providersvc.New(providersvc.Options{
		Repo:        sqlite.NewProviderRepository(db),
		Secrets:     secrets.NewMemory(),
		Client:      http.DefaultClient,
		TestTimeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("providersvc.New: %v", err)
	}
	defer pSvc.Close()

	modules.Providers{Service: pSvc, LockHostAdmin: true}.Register(regLocked)
	hProvList, _ := regLocked.Lookup(contracts.ActionProvidersList)
	if res, err := hProvList(context.Background(), &realtime.Request{}, []byte(`{}`)); err != nil || len(res.(contracts.ProvidersListResult).Builtins) == 0 {
		t.Fatalf("providers:list failed in locked mode: %v", err)
	}
	hProvUpdLocked, _ := regLocked.Lookup(contracts.ActionProvidersUpdateSettings)
	if _, err := hProvUpdLocked(context.Background(), &realtime.Request{}, []byte(`{"owner_id":"openai","settings":{"request_gap_ms":"50"}}`)); !apperr.IsCode(err, apperr.CodeUnavailable) {
		t.Fatalf("expected ERR_UNAVAILABLE for providers:update_settings in locked mode, got %v", err)
	}

	modules.Providers{Service: pSvc, LockHostAdmin: false}.Register(regOpen)
	hProvUpdOpen, _ := regOpen.Lookup(contracts.ActionProvidersUpdateSettings)
	if res, err := hProvUpdOpen(context.Background(), &realtime.Request{}, []byte(`{"owner_id":"openai","settings":{"request_gap_ms":"50"}}`)); err != nil || res.(contracts.ProviderSettingsResult).Settings[domainprovider.SettingRequestGapMs] != 50 {
		t.Fatalf("providers:update_settings unlocked failed: res=%+v err=%v", res, err)
	}

	// 4. Workflow module: specs, run_node error on unknown, and FileGuard propagated to run_graph.
	wfSvc, err := workflow.New(workflow.Options{
		Paths:  paths.Paths{Data: root},
		Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("workflow.New: %v", err)
	}
	modules.Workflow{Service: wfSvc}.Register(regOpen)
	hSpecs, _ := regOpen.Lookup(contracts.ActionWorkflowSpecs)
	specsRes, err := hSpecs(context.Background(), &realtime.Request{}, []byte(`{}`))
	if err != nil || len(specsRes.(contracts.WorkflowSpecsResult).Specs) == 0 {
		t.Fatalf("workflow:specs failed: %v", err)
	}
	hRunNode, _ := regOpen.Lookup(contracts.ActionWorkflowRunNode)
	if _, err := hRunNode(context.Background(), &realtime.Request{}, []byte(`{"type":"nonexistent"}`)); err == nil {
		t.Fatal("expected workflow:run_node unknown type error")
	}
	rawGraph, _ := json.Marshal(contracts.WorkflowRunGraphPayload{})
	hRunGraph, _ := regOpen.Lookup(contracts.ActionWorkflowRunGraph)
	if _, err := hRunGraph(context.Background(), &realtime.Request{}, rawGraph); err == nil {
		t.Fatal("expected workflow:run_graph empty graph validation error")
	}
}
