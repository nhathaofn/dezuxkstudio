package studio_test

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"app/internal/apperr"
	"app/internal/config"
	"app/internal/contracts"
	domainstudio "app/internal/domain/studio"
	"app/internal/engine"
	"app/internal/safego"
	"app/internal/service/studio"
	"app/internal/storage/sqlite"
)

type stubTextReadiness struct {
	ready bool
}

func (s stubTextReadiness) IsTextProviderReady(_ context.Context) bool {
	return s.ready
}

type tier123Workflow struct {
	fakeWorkflow
}

func (w *tier123Workflow) RunNode(ctx context.Context, nodeType string, input json.RawMessage) (any, error) {
	if nodeType == contracts.NodeTypeVideoContactSheet {
		var in contracts.VideoContactSheetInput
		_ = json.Unmarshal(input, &in)
		outDir := in.OutputDir
		if outDir == "" {
			outDir = filepath.Dir(in.VideoPath.LocalURL)
		}
		_ = os.MkdirAll(outDir, 0o755)
		sheetPath := filepath.Join(outDir, fmt.Sprintf("sheet_%s.jpg", in.ShotID))
		_ = os.WriteFile(sheetPath, []byte("fake-jpeg"), 0o644)
		return contracts.VideoContactSheetOutput{
			Sheets:     []engine.MediaRef{engine.RefFor(sheetPath, engine.MediaImage)},
			SheetPaths: []string{sheetPath},
			Signals: contracts.VideoSignals{
				DurationSec: 4.0,
				Blacks:      []contracts.VideoSignalBlack{},
				Freezes:     []contracts.VideoSignalFreeze{},
				Cuts:        []contracts.VideoSignalCut{{TimeSec: 1.5, Score: 0.72}},
			},
			Frames: []contracts.ContactSheetFrameMeta{
				{Index: 0, SheetIndex: 0, TimeSec: 1.5, TimelineMs: 1500, Signals: []string{"CUT"}},
			},
			Summary: "1 contact sheet, 1 cut",
		}, nil
	}
	return w.fakeWorkflow.RunNode(ctx, nodeType, input)
}

func newTier123TestService(t *testing.T, textReady bool, cfgOverride ...func(*config.StudioConfig)) (*studio.Service, *sqlite.DB, string) {
	t.Helper()
	tmp := t.TempDir()
	db, err := sqlite.Open(context.Background(), sqlite.Options{
		Path:          filepath.Join(tmp, "app.db"),
		BackupDir:     filepath.Join(tmp, "backups"),
		BackupsToKeep: 1,
		BusyTimeout:   2 * time.Second,
		Logger:        slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	projectsDir := filepath.Join(tmp, "projects")
	cfg := config.Default(config.ModeDesktop).Studio
	cfg.LongScriptChunkChars = 200
	cfg.MaxBatchShotsUpsert = 5
	cfg.QueueWaitPollInterval = 40 * time.Millisecond
	for _, fn := range cfgOverride {
		fn(&cfg)
	}

	svc := studio.New(studio.Options{
		Repo:          sqlite.NewStudioRepository(db, sqlite.WithStudioConfig(cfg)),
		Workflow:      &tier123Workflow{},
		TextReadiness: stubTextReadiness{ready: textReady},
		StudioConfig:  cfg,
		ProjectsDir:   projectsDir,
		Logger:        slog.New(slog.DiscardHandler),
	})
	return svc, db, projectsDir
}

func TestWorkflowPlanBatchShotsAndAssetRefactor(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := newTier123TestService(t, false)

	projDetail, err := svc.CreateProject(ctx, contracts.StudioProjectCreatePayload{
		Title:              "No LLM Key Demo",
		Genre:              domainstudio.GenreDrama,
		AutoApproveBatches: true,
	})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	projID := projDetail.Project.ID
	epID := projDetail.ActiveEpisode.Episode.ID

	// 1. Empty episode -> next action save_script, mode agent_direct (because textReady=false)
	plan1, err := svc.GetWorkflowPlan(ctx, epID)
	if err != nil {
		t.Fatalf("GetWorkflowPlan(1): %v", err)
	}
	if plan1.TextProviderReady || plan1.RecommendedAuthoringMode != "agent_direct" || plan1.NextAction.Action != "save_script" {
		t.Fatalf("unexpected initial plan: %+v", plan1)
	}

	// 2. Save raw + normalized script directly
	raw := "Minh walks into Old Cafe."
	_, err = svc.SaveScript(ctx, contracts.StudioEpisodeSaveScriptPayload{
		EpisodeID: epID,
		RawScript: &raw,
		NormalizedScript: &contracts.StudioNormalizedScript{
			Title:       "Ep 1",
			Genre:       domainstudio.GenreDrama,
			AspectRatio: "16:9",
			Characters:  []string{"Minh", "Minh Copy"},
			Scenes: []contracts.StudioScriptScene{{
				Code:  "S01",
				Title: "Cafe",
				Beats: []contracts.StudioScriptBeat{{
					Code:        "B01",
					Description: "@Minh meets @Minh_Copy",
					DurationSec: 4,
					AssetTags:   []string{"@Minh", "@Minh_Copy"},
					Dialogues:   []contracts.StudioDialogueLine{{Speaker: "Minh", Line: "Chào!"}},
				}},
			}},
		},
	})
	if err != nil {
		t.Fatalf("SaveScript: %v", err)
	}

	// Create two duplicate character assets
	a1, err := svc.UpsertAsset(ctx, contracts.StudioAssetUpsertPayload{
		ProjectID: projID, Kind: domainstudio.AssetCharacter, Name: "Minh", CodeTag: "@Minh", Prompt: "Minh portrait",
	})
	if err != nil {
		t.Fatal(err)
	}
	a2, err := svc.UpsertAsset(ctx, contracts.StudioAssetUpsertPayload{
		ProjectID: projID, Kind: domainstudio.AssetCharacter, Name: "Minh Copy", CodeTag: "@Minh_Copy", Prompt: "Minh copy",
	})
	if err != nil {
		t.Fatal(err)
	}

	// 3. BatchUpsertShots: enforce cap (>5), then upsert 2 shots, then patch by Seq
	tooMany := make([]contracts.StudioShotBatchItem, 6)
	if _, err := svc.BatchUpsertShots(ctx, contracts.StudioShotsBatchUpsertPayload{
		EpisodeID: epID, Shots: tooMany,
	}); !apperr.IsCode(err, apperr.CodeValidation) {
		t.Fatalf("expected validation error when batch > MaxBatchShotsUpsert, got %v", err)
	}

	imgP1 := "@Minh enters @Minh_Copy"
	batched, err := svc.BatchUpsertShots(ctx, contracts.StudioShotsBatchUpsertPayload{
		EpisodeID: epID,
		Shots: []contracts.StudioShotBatchItem{
			{
				SceneCode: "S01", Title: "Shot 1", ImagePrompt: &imgP1, RefAssetTags: []string{"@Minh", "@Minh_Copy"},
				Dialogues: []contracts.StudioDialogueLine{{Speaker: "Minh", Line: "Chào!"}},
			},
			{
				SceneCode: "S01", Title: "Shot 2", RefAssetTags: []string{"@Minh_Copy"},
			},
		},
	})
	if err != nil || len(batched.Shots) != 2 {
		t.Fatalf("BatchUpsertShots failed: len=%d err=%v", len(batched.Shots), err)
	}

	// Patch shot #2 by Seq without ID
	seq2 := 2
	patchedTitle := "Shot 2 Patched"
	batched2, err := svc.BatchUpsertShots(ctx, contracts.StudioShotsBatchUpsertPayload{
		EpisodeID: epID,
		Shots: []contracts.StudioShotBatchItem{
			{Seq: &seq2, Title: patchedTitle},
		},
	})
	if err != nil || len(batched2.Shots) != 2 || batched2.Shots[1].Title != patchedTitle {
		t.Fatalf("patch by Seq failed: %+v err=%v", batched2.Shots, err)
	}

	// 4. MergeAssets dry-run then live merge (@Minh_Copy -> @Minh)
	dryRes, err := svc.MergeAssets(ctx, contracts.StudioAssetMergePayload{
		ProjectID: projID, SourceAssetID: a2.Asset.ID, TargetAssetID: a1.Asset.ID, DryRun: true,
	})
	if err != nil || !dryRes.DryRun || dryRes.ImpactedEpisodeCount != 1 || dryRes.ImpactedShotCount != 2 {
		t.Fatalf("MergeAssets dry_run unexpected: %+v err=%v", dryRes, err)
	}
	mergeRes, err := svc.MergeAssets(ctx, contracts.StudioAssetMergePayload{
		ProjectID: projID, SourceAssetID: a2.Asset.ID, TargetAssetID: a1.Asset.ID,
	})
	if err != nil || len(mergeRes.Assets) != 1 || mergeRes.UpdatedShotsCount != 2 {
		t.Fatalf("MergeAssets live unexpected: %+v err=%v", mergeRes, err)
	}

	// 5. RenameAsset (@Minh -> @MinhPro, speaker "Minh" -> "Minh Pro")
	renRes, err := svc.RenameAsset(ctx, contracts.StudioAssetRenamePayload{
		ProjectID: projID, AssetID: a1.Asset.ID, NewName: "Minh Pro", NewCodeTag: "@MinhPro",
	})
	if err != nil || renRes.NewCodeTag != "@MinhPro" || renRes.UpdatedScriptsCount != 1 || renRes.UpdatedShotsCount != 2 {
		t.Fatalf("RenameAsset unexpected: %+v err=%v", renRes, err)
	}

	epAfter, err := svc.GetEpisode(ctx, epID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(epAfter.Shots[0].ImagePrompt, "@MinhPro") {
		t.Fatalf("expected @MinhPro in shot prompt after merge+rename, got %q", epAfter.Shots[0].ImagePrompt)
	}
	if epAfter.NormalizedScript == nil || epAfter.NormalizedScript.Scenes[0].Beats[0].Dialogues[0].Speaker != "Minh Pro" {
		t.Fatalf("expected speaker cascaded to Minh Pro in script: %+v", epAfter.NormalizedScript)
	}

	// 6. InspectShots on video-ready shot
	epRoot := filepath.Dir(filepath.Dir(epAfter.Episode.ScriptJSONPath))
	vidFile := filepath.Join(epRoot, "media", "videos", "shot1.mp4")
	_ = os.MkdirAll(filepath.Dir(vidFile), 0o755)
	_ = os.WriteFile(vidFile, []byte("mp4"), 0o644)
	if _, err := svc.UpsertShot(ctx, contracts.StudioShotUpsertPayload{
		ID: epAfter.Shots[0].ID, EpisodeID: epID, VideoPath: &vidFile,
	}); err != nil {
		t.Fatal(err)
	}
	inspRes, err := svc.InspectShots(ctx, contracts.StudioInspectShotsPayload{EpisodeID: epID})
	if err != nil || len(inspRes.Shots) != 1 || len(inspRes.Shots[0].SheetPaths) != 1 {
		t.Fatalf("InspectShots unexpected: %+v err=%v", inspRes, err)
	}
}

func TestWaitForQueueWakesOnJobCompletionAndTimesOutCleanly(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := newTier123TestService(t, true)

	projDetail, err := svc.CreateProject(ctx, contracts.StudioProjectCreatePayload{
		Title:              "Queue Wait Demo",
		AutoApproveBatches: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	epID := projDetail.ActiveEpisode.Episode.ID

	if _, err := svc.BatchUpsertShots(ctx, contracts.StudioShotsBatchUpsertPayload{
		EpisodeID: epID,
		Shots: []contracts.StudioShotBatchItem{
			{SceneCode: "S01", Title: "Shot 1"},
		},
	}); err != nil {
		t.Fatal(err)
	}

	enq, err := svc.EnqueueBatch(ctx, contracts.StudioQueueEnqueuePayload{
		EpisodeID: epID,
		Stage:     "generate_keyframes",
		Approved:  true,
	})
	if err != nil || len(enq.Jobs) != 1 {
		t.Fatalf("EnqueueBatch: %+v err=%v", enq, err)
	}

	// 1. Timeout cleanly while job is still queued
	toRes, err := svc.WaitForQueue(ctx, contracts.StudioQueueWaitPayload{
		EpisodeID:  epID,
		TimeoutSec: 1,
	})
	if err != nil || !toRes.TimedOut || toRes.AllDone || toRes.PendingCount != 1 {
		t.Fatalf("expected TimedOut=true PendingCount=1, got %+v err=%v", toRes, err)
	}

	// 2. Wake immediately when worker executes StepQueueOnce
	safego.Go("test-step-queue", func() {
		time.Sleep(60 * time.Millisecond)
		svc.StepQueueOnce(ctx)
	})

	doneRes, err := svc.WaitForQueue(ctx, contracts.StudioQueueWaitPayload{
		EpisodeID:  epID,
		TimeoutSec: 5,
	})
	if err != nil || !doneRes.AllDone || doneRes.TimedOut || doneRes.SucceededCount != 1 {
		t.Fatalf("expected AllDone=true SucceededCount=1, got %+v err=%v", doneRes, err)
	}
}
