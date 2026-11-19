package modules_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"app/internal/apperr"
	"app/internal/config"
	"app/internal/contracts"
	domainmedia "app/internal/domain/mediagen"
	"app/internal/paths"
	"app/internal/realtime"
	"app/internal/realtime/modules"
	studiosvc "app/internal/service/studio"
	"app/internal/service/workflow"
	"app/internal/storage/sqlite"
)

type stubStudioTextGen struct{}

func (s *stubStudioTextGen) GenerateText(_ context.Context, _ domainmedia.TextRequest) (domainmedia.TextResult, error) {
	payload := json.RawMessage(`{"title":"Ep 1","genre":"drama","aspect_ratio":"16:9","logline":"Test","scenes":[{"code":"S01","title":"Room","beats":[{"code":"B01","description":"Enter","duration_sec":5}]}]}`)
	return domainmedia.TextResult{Text: string(payload), ParsedJSON: payload, Provider: "stub", Model: "m1"}, nil
}

func TestStudioModule_RegisterDispatchAndFileGuard(t *testing.T) {
	root := t.TempDir()
	db, err := sqlite.Open(context.Background(), sqlite.Options{
		Path:          filepath.Join(root, "app.db"),
		BackupDir:     filepath.Join(root, "backups"),
		BackupsToKeep: 1,
		BusyTimeout:   time.Second,
		Logger:        slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	projDir := filepath.Join(root, "workspace", "projects")
	_ = os.MkdirAll(projDir, 0o755)

	wf, err := workflow.New(workflow.Options{
		Paths:   paths.Paths{Data: root},
		Logger:  slog.New(slog.DiscardHandler),
		TextGen: &stubStudioTextGen{},
	})
	if err != nil {
		t.Fatal(err)
	}

	svc := studiosvc.New(studiosvc.Options{
		Repo:         sqlite.NewStudioRepository(db),
		Workflow:     wf,
		StudioConfig: config.Default(config.ModeDesktop).Studio,
		ProjectsDir:  projDir,
		Logger:       slog.New(slog.DiscardHandler),
	})

	reg := realtime.NewRegistry(contracts.ActionNames())
	// Test FileGuard enforcement across studio actions (SaveScript + RunStage + Asset/Shot upserts).
	var guardCalled bool
	var denyAll bool
	mod := modules.Studio{
		Service: svc,
		FileGuard: func(path string, write bool) error {
			guardCalled = true
			if denyAll {
				return apperr.New(apperr.CodeUnauthorized, "denied by guard")
			}
			return nil
		},
	}
	mod.Register(reg)

	for _, action := range []string{
		contracts.ActionStudioProjectList,
		contracts.ActionStudioProjectCreate,
		contracts.ActionStudioProjectGet,
		contracts.ActionStudioProjectUpdate,
		contracts.ActionStudioProjectDelete,
		contracts.ActionStudioEpisodeCreate,
		contracts.ActionStudioEpisodeGet,
		contracts.ActionStudioEpisodeSaveScript,
		contracts.ActionStudioAssetUpsert,
		contracts.ActionStudioShotUpsert,
		contracts.ActionStudioShotReorder,
		contracts.ActionStudioEpisodeRunStage,
		contracts.ActionStudioQueueEnqueue,
		contracts.ActionStudioQueueList,
		contracts.ActionStudioQueueControl,
		contracts.ActionStudioCostSummary,
		contracts.ActionStudioPricingUpsert,
		contracts.ActionStudioVersionsList,
		contracts.ActionStudioVersionSelect,
		contracts.ActionStudioGridSplit,
		contracts.ActionStudioMaskEdit,
		contracts.ActionStudioExtractLastFrame,
		contracts.ActionStudioQCReview,
		contracts.ActionStudioBatchPreflight,
		contracts.ActionStudioTimelineGet,
		contracts.ActionStudioTimelineApplyOps,
		contracts.ActionStudioTimelineUndo,
		contracts.ActionStudioTimelineRedo,
		contracts.ActionStudioTimelineAutoAssemble,
		contracts.ActionStudioMediaPeaksThumbs,
		contracts.ActionStudioTimelineRender,
		contracts.ActionStudioTimelineExportNLE,
		contracts.ActionStudioOpenExportFolder,
	} {
		if h, ok := reg.Lookup(action); !ok || h == nil {
			t.Fatalf("missing handler for %q", action)
		}
	}

	hCreate, _ := reg.Lookup(contracts.ActionStudioProjectCreate)
	rawCreated, err := hCreate(context.Background(), &realtime.Request{}, []byte(`{"title":"Guarded Film"}`))
	if err != nil {
		t.Fatalf("studio:project_create failed: %v", err)
	}
	created := rawCreated.(contracts.StudioProjectDetailResult)
	epID := created.ActiveEpisode.Episode.ID

	hSave, _ := reg.Lookup(contracts.ActionStudioEpisodeSaveScript)
	if _, err := hSave(context.Background(), &realtime.Request{}, []byte(`{"episode_id":"`+epID+`","raw_script":"Hero walks in."}`)); err != nil {
		t.Fatalf("studio:episode_save_script failed: %v", err)
	}
	if !guardCalled {
		t.Fatal("expected FileGuard to be invoked during studio:episode_save_script")
	}

	guardCalled = false
	denyAll = true
	if _, err := hSave(context.Background(), &realtime.Request{}, []byte(`{"episode_id":"`+epID+`","raw_script":"Blocked write."}`)); err == nil || !apperr.IsCode(err, apperr.CodeUnauthorized) {
		t.Fatalf("expected ERR_UNAUTHORIZED from FileGuard on studio:episode_save_script, got %v", err)
	}

	guardCalled = false
	hRun, _ := reg.Lookup(contracts.ActionStudioEpisodeRunStage)
	if _, err = hRun(context.Background(), &realtime.Request{}, []byte(`{"episode_id":"`+epID+`","stage":"script_normalize"}`)); !apperr.IsCode(err, apperr.CodeValidation) {
		t.Fatalf("expected ERR_VALIDATION approval gate on unapproved script_normalize, got %v", err)
	}
	_, err = hRun(context.Background(), &realtime.Request{}, []byte(`{"episode_id":"`+epID+`","stage":"script_normalize","approved":true}`))
	if !guardCalled {
		t.Fatal("expected FileGuard to be invoked during studio:episode_run_stage")
	}
	if err == nil || !apperr.IsCode(err, apperr.CodeUnauthorized) {
		t.Fatalf("expected ERR_UNAUTHORIZED from FileGuard, got %v", err)
	}

	hAssetUpsert, _ := reg.Lookup(contracts.ActionStudioAssetUpsert)
	_, err = hAssetUpsert(context.Background(), &realtime.Request{}, []byte(`{"project_id":"`+created.Project.ID+`","kind":"character","name":"Minh","primary_path":"C:\\Windows\\System32\\cmd.exe"}`))
	if err == nil || !apperr.IsCode(err, apperr.CodeUnauthorized) {
		t.Fatalf("expected ERR_UNAUTHORIZED from FileGuard on asset_upsert primary_path, got %v", err)
	}

	hShotUpsert, _ := reg.Lookup(contracts.ActionStudioShotUpsert)
	_, err = hShotUpsert(context.Background(), &realtime.Request{}, []byte(`{"episode_id":"`+epID+`","title":"Bad","video_path":"C:\\Users\\owner\\.ssh\\id_rsa"}`))
	if err == nil || !apperr.IsCode(err, apperr.CodeUnauthorized) {
		t.Fatalf("expected ERR_UNAUTHORIZED from FileGuard on shot_upsert video_path, got %v", err)
	}

	lockedReg := realtime.NewRegistry(contracts.ActionNames())
	modules.Studio{Service: svc, LockHostAdmin: true}.Register(lockedReg)
	hOpenFolder, _ := lockedReg.Lookup(contracts.ActionStudioOpenExportFolder)
	if _, err := hOpenFolder(context.Background(), &realtime.Request{}, []byte(`{"episode_id":"`+epID+`"}`)); !apperr.IsCode(err, apperr.CodeUnavailable) {
		t.Fatalf("expected ERR_UNAVAILABLE when LockHostAdmin is true, got %v", err)
	}

	hTimelineGet, _ := reg.Lookup(contracts.ActionStudioTimelineGet)
	if _, err := hTimelineGet(context.Background(), &realtime.Request{}, []byte(`{"id":"   "}`)); !apperr.IsCode(err, apperr.CodeValidation) {
		t.Fatalf("expected ERR_VALIDATION for blank id on ActionStudioTimelineGet, got %v", err)
	}

	// Verify ActionStudioProjectCreate value-receiver Validate() runs in realtime.Handle
	if _, err := hCreate(context.Background(), &realtime.Request{}, []byte(`{"title":"   "}`)); !apperr.IsCode(err, apperr.CodeValidation) {
		t.Fatalf("expected ERR_VALIDATION for blank title on ActionStudioProjectCreate, got %v", err)
	}
	if _, err := hCreate(context.Background(), &realtime.Request{}, []byte(`{"title":"Valid","genre":"invalid_genre"}`)); !apperr.IsCode(err, apperr.CodeValidation) {
		t.Fatalf("expected ERR_VALIDATION for invalid genre on ActionStudioProjectCreate, got %v", err)
	}
}
