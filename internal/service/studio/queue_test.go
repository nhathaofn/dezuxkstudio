package studio_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"log/slog"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"app/internal/config"
	"app/internal/contracts"
	domainmedia "app/internal/domain/mediagen"
	studiosvc "app/internal/service/studio"
	"app/internal/storage/sqlite"
	studionodes "app/internal/tools/studio"
)

type crashTestVideoDriver struct {
	outDir      string
	submitCalls atomic.Int32
	pollCalls   atomic.Int32
}

func (d *crashTestVideoDriver) SubmitVideoJob(_ context.Context, req domainmedia.VideoRequest) (domainmedia.VideoSubmitToken, error) {
	d.submitCalls.Add(1)
	return domainmedia.VideoSubmitToken{
		Protocol:      "openai-video",
		ProviderID:    "openai",
		ModelRef:      "openai/sora-2",
		ModelID:       "sora-2",
		ProviderJobID: "remote-vid-999",
		OutputDir:     req.OutputDir,
		DurationSec:   req.DurationSec,
	}, nil
}

func (d *crashTestVideoDriver) PollVideoJobOnce(_ context.Context, tok domainmedia.VideoSubmitToken) (domainmedia.VideoPollStatus, error) {
	n := d.pollCalls.Add(1)
	if n == 1 {
		return domainmedia.VideoPollStatus{Done: false, Status: "processing", Progress: 50}, nil
	}
	outPath := filepath.Join(d.outDir, "recovered_shot.mp4")
	_ = os.WriteFile(outPath, []byte("MP4RECOVERED"), 0o600)
	return domainmedia.VideoPollStatus{
		Done:     true,
		Status:   "succeeded",
		Progress: 100,
		Result:   domainmedia.Result{Path: outPath, MimeType: "video/mp4", Provider: "openai", Model: "sora-2", Duration: float64(tok.DurationSec)},
	}, nil
}

type phase2NodeStubWorkflow struct {
	outDir       string
	lastMaskPath string
	lastPrompt   string
}

func (w *phase2NodeStubWorkflow) RunNode(_ context.Context, nodeType string, raw json.RawMessage) (any, error) {
	switch nodeType {
	case studionodes.NodeTypeMaskEdit:
		var in contracts.MaskEditInput
		_ = json.Unmarshal(raw, &in)
		w.lastMaskPath = in.MaskPath
		w.lastPrompt = in.Prompt
		outPath := filepath.Join(w.outDir, "edited_frame.png")
		_ = os.WriteFile(outPath, []byte("PNGEDITED"), 0o600)
		return contracts.MaskEditOutput{Path: outPath, MimeType: "image/png", Provider: "openai", Model: "gpt-image-1"}, nil
	case studionodes.NodeTypeExtractFrames:
		outPath := filepath.Join(w.outDir, "last_frame.png")
		_ = os.WriteFile(outPath, []byte("PNGLASTFRAME"), 0o600)
		return contracts.ExtractFramesOutput{
			Frames:        []contracts.ExtractedFrame{{Path: outPath, TimestampSec: 4.9, Width: 1280, Height: 720}},
			LastFramePath: outPath,
			OutputDir:     w.outDir,
		}, nil
	case studionodes.NodeTypeQCReview:
		return contracts.QCReviewOutput{
			Verdict:       "pass",
			OverallScore:  9.1,
			FFprobeJSON:   `{"format":{"duration":"5.0"}}`,
			Issues:        []contracts.QCReviewIssue{{Severity: "info", Category: "continuity", Message: "Clean"}},
			VisionSummary: "Matches reference",
		}, nil
	default:
		return map[string]any{}, nil
	}
}

func TestStudioCrashRecoveryQueueSubmitsExactlyOnceAcrossRestart(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()
	db, err := sqlite.Open(ctx, sqlite.Options{
		Path:          filepath.Join(tmp, "studio.db"),
		BackupDir:     filepath.Join(tmp, "backups"),
		BackupsToKeep: 1,
		BusyTimeout:   time.Second,
		Logger:        slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	projDir := filepath.Join(tmp, "projects")
	driver := &crashTestVideoDriver{outDir: tmp}
	wf := &phase2NodeStubWorkflow{outDir: tmp}

	svc1 := studiosvc.New(studiosvc.Options{
		Repo:         sqlite.NewStudioRepository(db),
		Workflow:     wf,
		VideoMedia:   driver,
		StudioConfig: config.Default(config.ModeDesktop).Studio,
		ProjectsDir:  projDir,
		Logger:       slog.New(slog.DiscardHandler),
	})

	// Approval gate enforcement when AutoApproveBatches=false
	created, err := svc1.CreateProject(ctx, contracts.StudioProjectCreatePayload{
		Title:              "Crash Recovery Film",
		DefaultVideoModel:  "openai/sora-2",
		AutoApproveBatches: false,
	})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	epID := created.ActiveEpisode.Episode.ID
	kfPath := filepath.Join(tmp, "kf.png")
	_ = os.WriteFile(kfPath, []byte("PNGKF"), 0o600)
	upShot, err := svc1.UpsertShot(ctx, contracts.StudioShotUpsertPayload{
		EpisodeID:    epID,
		SceneCode:    "S01",
		Title:        "Shot 1",
		VideoPrompt:  ptrStr("drone push in"),
		DurationSec:  ptrInt(5),
		KeyframePath: ptrStr(kfPath),
	})
	if err != nil {
		t.Fatalf("UpsertShot: %v", err)
	}
	shotID := upShot.Shot.ID

	// Without Approved=true, gated project must refuse batch enqueue
	if _, err := svc1.EnqueueBatch(ctx, contracts.StudioQueueEnqueuePayload{
		EpisodeID: epID, Stage: "generate_videos",
	}); err == nil {
		t.Fatal("expected approval gate error when AutoApproveBatches=false and Approved=false")
	}

	// Approved enqueue persists durable job
	enq, err := svc1.EnqueueBatch(ctx, contracts.StudioQueueEnqueuePayload{
		EpisodeID: epID, Stage: "generate_videos", Approved: true,
	})
	if err != nil || len(enq.Jobs) != 1 || enq.EstimatedUSD <= 0 {
		t.Fatalf("EnqueueBatch = %+v err=%v", enq, err)
	}

	// Duplicate unforced click must be deduplicated by idempotency_key
	enq2, err := svc1.EnqueueBatch(ctx, contracts.StudioQueueEnqueuePayload{
		EpisodeID: epID, Stage: "generate_videos", Approved: true,
	})
	if err != nil || len(enq2.Jobs) != 0 || enq2.Skipped != 1 {
		t.Fatalf("expected idempotent deduplication, got %+v err=%v", enq2, err)
	}

	// Instance #1 submits once and crashes BEFORE polling finishes
	if !svc1.StepQueueOnce(ctx) {
		t.Fatal("expected StepQueueOnce on instance #1 to claim and submit job")
	}
	jobsAfterSubmit, _ := svc1.ListJobs(ctx, contracts.StudioQueueListPayload{ProjectID: created.Project.ID})
	if len(jobsAfterSubmit.Jobs) != 1 || jobsAfterSubmit.Jobs[0].ProviderJobID != "remote-vid-999" || jobsAfterSubmit.Jobs[0].Status != "polling" {
		t.Fatalf("unexpected persisted job state after submit: %+v", jobsAfterSubmit.Jobs)
	}

	// Simulate abrupt restart: instantiate fresh Service #2 on the same SQLite DB & same provider spy
	cfg2 := config.Default(config.ModeDesktop).Studio
	cfg2.QueuePollInterval = 5 * time.Millisecond
	svc2 := studiosvc.New(studiosvc.Options{
		Repo:         sqlite.NewStudioRepository(db),
		Workflow:     wf,
		VideoMedia:   driver,
		StudioConfig: cfg2,
		ProjectsDir:  projDir,
		Logger:       slog.New(slog.DiscardHandler),
	})
	if err := svc2.RecoverOnStartup(ctx); err != nil {
		t.Fatalf("RecoverOnStartup: %v", err)
	}
	// Poll 1 -> still processing; wait poll backoff; Poll 2 -> succeeded
	if !svc2.StepQueueOnce(ctx) {
		t.Fatal("expected StepQueueOnce poll #1")
	}
	time.Sleep(10 * time.Millisecond)
	if !svc2.StepQueueOnce(ctx) {
		t.Fatal("expected StepQueueOnce poll #2")
	}

	if got := driver.submitCalls.Load(); got != 1 {
		t.Fatalf("Submit API called %d times across restart, MUST be strictly 1", got)
	}
	if got := driver.pollCalls.Load(); got != 2 {
		t.Fatalf("Poll API called %d times, want 2", got)
	}

	epAfter, err := svc2.GetEpisode(ctx, epID)
	if err != nil || len(epAfter.Shots) != 1 || epAfter.Shots[0].VideoPath == "" {
		t.Fatalf("expected shot video updated on completion: %+v err=%v", epAfter.Shots, err)
	}

	// Usage hook / cost summary + versions + mask edit + last frame continuity + QC review
	svc2.HandleMediaUsage(ctx, domainmedia.UsageEvent{
		ProjectID:   created.Project.ID,
		EpisodeID:   epID,
		ShotID:      shotID,
		JobID:       enq.Jobs[0].ID,
		ProviderID:  "openai",
		ModelID:     "sora-2",
		Capability:  "video",
		Operation:   "generate_video",
		OutputUnits: 5,
		Unit:        "second",
	})
	costSum, err := svc2.GetCostSummary(ctx, contracts.StudioCostSummaryPayload{ProjectID: created.Project.ID})
	if err != nil || costSum.ActualTotalUSD != 0.50 {
		t.Fatalf("GetCostSummary = %+v err=%v", costSum, err)
	}

	// Tiny PNG base64 for canvas mask edit
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	img.SetRGBA(1, 1, color.RGBA{R: 255, G: 255, B: 255, A: 255})
	maskBufPath := filepath.Join(tmp, "m.png")
	mf, _ := os.Create(maskBufPath)
	_ = png.Encode(mf, img)
	_ = mf.Close()
	maskRaw, _ := os.ReadFile(maskBufPath)

	maskRes, err := svc2.ApplyMaskEdit(ctx, contracts.StudioMaskEditPayload{
		ProjectID:      created.Project.ID,
		EpisodeID:      epID,
		TargetKind:     "keyframe",
		TargetID:       shotID,
		ImagePath:      kfPath,
		MaskDataBase64: "data:image/png;base64," + base64.StdEncoding.EncodeToString(maskRaw),
		Prompt:         "change sky to sunset",
	})
	if err != nil || maskRes.Version == nil || wf.lastMaskPath == "" {
		t.Fatalf("ApplyMaskEdit = %+v err=%v maskPath=%q", maskRes, err, wf.lastMaskPath)
	}

	// Next shot continuity via ExtractLastFrame
	shot2, _ := svc2.UpsertShot(ctx, contracts.StudioShotUpsertPayload{EpisodeID: epID, SceneCode: "S01", Title: "Shot 2"})
	efRes, err := svc2.ExtractLastFrame(ctx, contracts.StudioExtractLastFramePayload{
		EpisodeID:    epID,
		SourceShotID: shotID,
		TargetShotID: shot2.Shot.ID,
	})
	if err != nil || efRes.FramePath == "" || efRes.Episode == nil || efRes.Episode.Shots[1].KeyframePath == "" {
		t.Fatalf("ExtractLastFrame = %+v err=%v", efRes, err)
	}

	qcRes, err := svc2.RunQCReview(ctx, contracts.StudioQCReviewPayload{
		ProjectID:  created.Project.ID,
		EpisodeID:  epID,
		TargetKind: "video",
		TargetID:   shotID,
		MediaPath:  epAfter.Shots[0].VideoPath,
	})
	if err != nil || qcRes.Verdict != "pass" {
		t.Fatalf("RunQCReview = %+v err=%v", qcRes, err)
	}
}

func ptrStr(v string) *string { return &v }
func ptrInt(v int) *int       { return &v }
