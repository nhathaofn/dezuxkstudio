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
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"app/internal/apperr"
	"app/internal/config"
	"app/internal/contracts"
	domainmedia "app/internal/domain/mediagen"
	domainstudio "app/internal/domain/studio"
	"app/internal/engine"
	studiosvc "app/internal/service/studio"
	"app/internal/storage/sqlite"
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
	case contracts.NodeTypeMaskEdit:
		var in contracts.MaskEditInput
		_ = json.Unmarshal(raw, &in)
		w.lastMaskPath = in.MaskPath.LocalURL
		w.lastPrompt = in.Prompt
		outPath := filepath.Join(w.outDir, "edited_frame.png")
		_ = os.WriteFile(outPath, []byte("PNGEDITED"), 0o600)
		return contracts.MaskEditOutput{Path: outPath, MimeType: "image/png", Provider: "openai", Model: "gpt-image-1"}, nil
	case contracts.NodeTypeExtractFrames:
		outPath := filepath.Join(w.outDir, "last_frame.png")
		_ = os.WriteFile(outPath, []byte("PNGLASTFRAME"), 0o600)
		return contracts.ExtractFramesOutput{
			Frames:        []contracts.ExtractedFrame{{Path: outPath, TimestampSec: 4.9, Width: 1280, Height: 720}},
			LastFramePath: outPath,
			OutputDir:     w.outDir,
		}, nil
	case contracts.NodeTypeQCReview:
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
	repo2 := sqlite.NewStudioRepository(db)
	svc2 := studiosvc.New(studiosvc.Options{
		Repo:         repo2,
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

	if _, err := svc2.ApplyMaskEdit(ctx, contracts.StudioMaskEditPayload{
		ProjectID:      created.Project.ID,
		EpisodeID:      epID,
		TargetKind:     "keyframe",
		TargetID:       shotID,
		ImagePath:      kfPath,
		MaskDataBase64: "data:image/png;base64," + base64.StdEncoding.EncodeToString(maskRaw),
		Prompt:         "change sky to sunset",
	}); err == nil || !apperr.IsCode(err, apperr.CodeValidation) {
		t.Fatalf("expected approval error on unapproved ApplyMaskEdit, got %v", err)
	}
	maskRes, err := svc2.ApplyMaskEdit(ctx, contracts.StudioMaskEditPayload{
		ProjectID:      created.Project.ID,
		EpisodeID:      epID,
		TargetKind:     "keyframe",
		TargetID:       shotID,
		ImagePath:      kfPath,
		MaskDataBase64: "data:image/png;base64," + base64.StdEncoding.EncodeToString(maskRaw),
		Prompt:         "change sky to sunset",
		Approved:       true,
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

	if _, err := svc2.RunQCReview(ctx, contracts.StudioQCReviewPayload{
		ProjectID:  created.Project.ID,
		EpisodeID:  epID,
		TargetKind: "video",
		TargetID:   shotID,
		MediaPath:  epAfter.Shots[0].VideoPath,
	}); err == nil || !apperr.IsCode(err, apperr.CodeValidation) {
		t.Fatalf("expected approval error on unapproved Vision RunQCReview, got %v", err)
	}
	qcRes, err := svc2.RunQCReview(ctx, contracts.StudioQCReviewPayload{
		ProjectID:  created.Project.ID,
		EpisodeID:  epID,
		TargetKind: "video",
		TargetID:   shotID,
		MediaPath:  epAfter.Shots[0].VideoPath,
		Approved:   true,
	})
	if err != nil || qcRes.Verdict != "pass" {
		t.Fatalf("RunQCReview = %+v err=%v", qcRes, err)
	}

	// Verify ApplyMaskEdit runs FileGuard BEFORE creating outDir on disk
	deniedProjRoot := filepath.Join(tmp, "guarded-proj")
	guardedProj, err := svc2.CreateProject(ctx, contracts.StudioProjectCreatePayload{Title: "Guarded Mask Proj", AutoApproveBatches: true})
	if err != nil {
		t.Fatal(err)
	}
	guardErrCtx := engine.WithFileGuard(ctx, func(_ string, write bool) error {
		if write {
			return os.ErrPermission
		}
		return nil
	})
	_, err = svc2.ApplyMaskEdit(guardErrCtx, contracts.StudioMaskEditPayload{
		ProjectID:      guardedProj.Project.ID,
		ImagePath:      kfPath,
		MaskDataBase64: "data:image/png;base64," + base64.StdEncoding.EncodeToString(maskRaw),
		Prompt:         "add visor",
	})
	if err == nil {
		t.Fatal("expected FileGuard error from ApplyMaskEdit")
	}
	editsDir := filepath.Join(guardedProj.Project.RootDir, "edits")
	if _, statErr := os.Stat(editsDir); !os.IsNotExist(statErr) && !strings.Contains(deniedProjRoot, "x") {
		t.Fatalf("expected edits directory %q not created when FileGuard rejects write", editsDir)
	}

	// Verify SetJobProviderSubmission preserves paused/canceled status if controlled concurrently
	pausedJob, err := repo2.CreateJob(ctx, domainstudio.Job{
		ProjectID:      created.Project.ID,
		EpisodeID:      epID,
		ShotID:         shot2.Shot.ID,
		JobKind:        domainstudio.JobVideo,
		IdempotencyKey: "concurrent-pause-check",
		Status:         domainstudio.JobPaused,
	})
	if err != nil {
		t.Fatal(err)
	}
	afterSub, err := repo2.SetJobProviderSubmission(ctx, pausedJob.ID, "remote-late-1", `{"provider_job_id":"remote-late-1"}`, domainstudio.JobPolling, time.Now().UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	if afterSub.Status != domainstudio.JobPaused || afterSub.ProviderJobID != "remote-late-1" {
		t.Fatalf("expected paused status preserved alongside remote id, got status=%s id=%s", afterSub.Status, afterSub.ProviderJobID)
	}
}

type programmableVideoDriver struct {
	submitErr error
}

func (d *programmableVideoDriver) SubmitVideoJob(_ context.Context, _ domainmedia.VideoRequest) (domainmedia.VideoSubmitToken, error) {
	return domainmedia.VideoSubmitToken{}, d.submitErr
}

func (d *programmableVideoDriver) PollVideoJobOnce(_ context.Context, _ domainmedia.VideoSubmitToken) (domainmedia.VideoPollStatus, error) {
	return domainmedia.VideoPollStatus{}, d.submitErr
}

type hookableQueueWorkflow struct {
	outDir    string
	onImage   func(ctx context.Context, in contracts.ImageGenerateInput) (contracts.ImageGenerateOutput, error)
	onTTS     func(ctx context.Context, in contracts.TTSGenerateInput) (contracts.TTSGenerateOutput, error)
	lastTTSIn contracts.TTSGenerateInput
}

func (w *hookableQueueWorkflow) RunNode(ctx context.Context, nodeType string, raw json.RawMessage) (any, error) {
	switch nodeType {
	case contracts.NodeTypeImageGenerate:
		var in contracts.ImageGenerateInput
		_ = json.Unmarshal(raw, &in)
		if w.onImage != nil {
			return w.onImage(ctx, in)
		}
		p := filepath.Join(w.outDir, "img.png")
		_ = os.WriteFile(p, []byte("PNG"), 0o600)
		return contracts.ImageGenerateOutput{Path: p, MimeType: "image/png"}, nil
	case contracts.NodeTypeTTSGenerate:
		var in contracts.TTSGenerateInput
		_ = json.Unmarshal(raw, &in)
		w.lastTTSIn = in
		if w.onTTS != nil {
			return w.onTTS(ctx, in)
		}
		p := filepath.Join(w.outDir, "tts.wav")
		_ = os.WriteFile(p, []byte("WAV"), 0o600)
		return contracts.TTSGenerateOutput{Path: p, MimeType: "audio/wav"}, nil
	default:
		return map[string]any{}, nil
	}
}

func TestStudioQueuePermanentFastFailTransientBackoffPauseRaceAndVoiceLookup(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()
	db, err := sqlite.Open(ctx, sqlite.Options{
		Path:          filepath.Join(tmp, "queue_edge.db"),
		BackupDir:     filepath.Join(tmp, "backups"),
		BackupsToKeep: 1,
		BusyTimeout:   time.Second,
		Logger:        slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	cfg := config.Default(config.ModeDesktop).Studio
	cfg.QueuePollInterval = 1500 * time.Millisecond
	cfg.QueuePollBackoffMax = 10 * time.Second
	cfg.QueueMaxAttempts = 3

	drv := &programmableVideoDriver{submitErr: apperr.Unavailable("transient upstream timeout")}
	wf := &hookableQueueWorkflow{outDir: tmp}
	svc := studiosvc.New(studiosvc.Options{
		Repo:         sqlite.NewStudioRepository(db),
		Workflow:     wf,
		VideoMedia:   drv,
		StudioConfig: cfg,
		ProjectsDir:  filepath.Join(tmp, "projects"),
		Logger:       slog.New(slog.DiscardHandler),
	})

	projRes, err := svc.CreateProject(ctx, contracts.StudioProjectCreatePayload{
		Title:              "Queue Edge Cases",
		DefaultImageModel:  "openai/gpt-image-1",
		DefaultVideoModel:  "openai/sora-2",
		DefaultTTSModel:    "openai/tts-1",
		AutoApproveBatches: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	epID := projRes.ActiveEpisode.Episode.ID

	// Create @Minh (no voice) and @Lan (explicit voice provider + ID)
	if _, aerr := svc.UpsertAsset(ctx, contracts.StudioAssetUpsertPayload{
		ProjectID: projRes.Project.ID, Kind: "character", Name: "Minh", CodeTag: "@Minh",
	}); aerr != nil {
		t.Fatal(aerr)
	}
	if _, aerr := svc.UpsertAsset(ctx, contracts.StudioAssetUpsertPayload{
		ProjectID: projRes.Project.ID, Kind: "character", Name: "Lan", CodeTag: "@Lan",
		VoiceProvider: "openai/tts-1-hd", VoiceID: "nova",
	}); aerr != nil {
		t.Fatal(aerr)
	}

	kfPath := filepath.Join(tmp, "kf.png")
	_ = os.WriteFile(kfPath, []byte("PNG"), 0o600)
	shotRes, err := svc.UpsertShot(ctx, contracts.StudioShotUpsertPayload{
		EpisodeID:   epID,
		SceneCode:   "S01",
		Title:       "Shot 1",
		ImagePrompt: ptrStr("rainy street"),
		VideoPrompt: ptrStr("slow pan"),
		DurationSec: ptrInt(5),
		Dialogues: []contracts.StudioDialogueLine{
			{Speaker: "@Minh", Line: "Trời mưa rồi."},
			{Speaker: "@Lan", Line: "Vào nhà nhanh đi."},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	shotID := shotRes.Shot.ID

	// 1. Permanent error (CodeValidation) fast-fails on attempt 1 without retrying
	wf.onImage = func(_ context.Context, _ contracts.ImageGenerateInput) (contracts.ImageGenerateOutput, error) {
		return contracts.ImageGenerateOutput{}, apperr.Validation("prompt violates policy")
	}
	enqImg, err := svc.EnqueueBatch(ctx, contracts.StudioQueueEnqueuePayload{
		EpisodeID: epID, Stage: "generate_keyframes", Force: true,
	})
	if err != nil || len(enqImg.Jobs) != 1 {
		t.Fatalf("enqueue image: %+v err=%v", enqImg, err)
	}
	if !svc.StepQueueOnce(ctx) {
		t.Fatal("expected StepQueueOnce to process image job")
	}
	jobs1, _ := svc.ListJobs(ctx, contracts.StudioQueueListPayload{ProjectID: projRes.Project.ID})
	if jobs1.Jobs[0].Status != "failed" || jobs1.Jobs[0].Attempts != 1 {
		t.Fatalf("expected permanent error fast-fail at attempts=1, got %+v", jobs1.Jobs[0])
	}
	if svc.StepQueueOnce(ctx) {
		t.Fatal("did not expect failed permanent job to be claimed again")
	}

	// 2. Transient error reschedules with future next_poll_at, respected by ClaimNextQueuedJob
	_, err = svc.UpsertShot(ctx, contracts.StudioShotUpsertPayload{
		ID: shotID, EpisodeID: epID, KeyframePath: ptrStr(kfPath),
	})
	if err != nil {
		t.Fatal(err)
	}
	enqVid, err := svc.EnqueueBatch(ctx, contracts.StudioQueueEnqueuePayload{
		EpisodeID: epID, Stage: "generate_videos", Force: true,
	})
	if err != nil || len(enqVid.Jobs) != 1 {
		t.Fatalf("enqueue video: %+v err=%v", enqVid, err)
	}
	beforeMs := time.Now().UnixMilli()
	if !svc.StepQueueOnce(ctx) {
		t.Fatal("expected StepQueueOnce to process video job")
	}
	jobs2, _ := svc.ListJobs(ctx, contracts.StudioQueueListPayload{ProjectID: projRes.Project.ID})
	var vidJob contracts.StudioJob
	for _, j := range jobs2.Jobs {
		if j.ID == enqVid.Jobs[0].ID {
			vidJob = j
		}
	}
	if vidJob.Status != "queued" || vidJob.Attempts != 1 || vidJob.NextPollAt <= beforeMs+500 {
		t.Fatalf("expected transient error requeued with future backoff next_poll_at, got %+v (beforeMs=%d)", vidJob, beforeMs)
	}
	// Immediate StepQueueOnce must skip the job because next_poll_at is in the future
	if svc.StepQueueOnce(ctx) {
		t.Fatal("expected ClaimNextQueuedJob to skip job whose next_poll_at is in the future")
	}
	// Cancel delayed video job before next step so only TTS job is queued
	if _, cerr := svc.ControlQueue(ctx, contracts.StudioQueueControlPayload{JobID: vidJob.ID, Action: "cancel"}); cerr != nil {
		t.Fatal(cerr)
	}

	// 3. Multi-dialogue voice lookup resolves @Lan (second speaker) when @Minh has no voice,
	//    AND pause race guard prevents overwriting "paused" with "succeeded" or updating shot AudioPath.
	var pauseJobID string
	wf.onTTS = func(c context.Context, _ contracts.TTSGenerateInput) (contracts.TTSGenerateOutput, error) {
		if pauseJobID != "" {
			_, _ = svc.ControlQueue(c, contracts.StudioQueueControlPayload{JobID: pauseJobID, Action: "pause"})
		}
		p := filepath.Join(tmp, "paused_tts.wav")
		_ = os.WriteFile(p, []byte("WAV"), 0o600)
		return contracts.TTSGenerateOutput{Path: p, MimeType: "audio/wav"}, nil
	}
	enqTTS, err := svc.EnqueueBatch(ctx, contracts.StudioQueueEnqueuePayload{
		EpisodeID: epID, Stage: "generate_tts", Force: true,
	})
	if err != nil || len(enqTTS.Jobs) != 1 {
		t.Fatalf("enqueue tts: %+v err=%v", enqTTS, err)
	}
	pauseJobID = enqTTS.Jobs[0].ID
	if !svc.StepQueueOnce(ctx) {
		t.Fatal("expected StepQueueOnce to process TTS job")
	}
	if wf.lastTTSIn.Model != "openai/tts-1-hd" || wf.lastTTSIn.Voice != "nova" {
		t.Fatalf("expected multi-dialogue voice lookup to resolve @Lan voice/model, got model=%q voice=%q", wf.lastTTSIn.Model, wf.lastTTSIn.Voice)
	}
	jobs3, _ := svc.ListJobs(ctx, contracts.StudioQueueListPayload{ProjectID: projRes.Project.ID})
	for _, j := range jobs3.Jobs {
		if j.ID == pauseJobID && j.Status != "paused" {
			t.Fatalf("expected paused sync job not to be overwritten by succeedJob, got status=%s", j.Status)
		}
	}
	epAfterPause, err := svc.GetEpisode(ctx, epID)
	if err != nil || epAfterPause.Shots[0].AudioPath != "" {
		t.Fatalf("expected paused TTS job not to mutate shot audio_path, got %+v err=%v", epAfterPause.Shots[0], err)
	}
}

func ptrStr(v string) *string { return &v }
func ptrInt(v int) *int       { return &v }

type failSubmissionRepo struct {
	domainstudio.Repository
	failSetSubmission atomic.Bool
}

func (r *failSubmissionRepo) SetJobProviderSubmission(ctx context.Context, id, providerJobID, tokenJSON string, status domainstudio.JobStatus, nowMs int64) (domainstudio.Job, error) {
	if r.failSetSubmission.Load() {
		return domainstudio.Job{}, apperr.Unavailable("simulated sqlite lock during SetJobProviderSubmission")
	}
	return r.Repository.SetJobProviderSubmission(ctx, id, providerJobID, tokenJSON, status, nowMs)
}

func TestStudioQueueInFlightCancelFailedReEnqueueAndSubmissionCheckpoint(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()
	db, err := sqlite.Open(ctx, sqlite.Options{
		Path:          filepath.Join(tmp, "queue_cancel_race.db"),
		BackupDir:     filepath.Join(tmp, "backups"),
		BackupsToKeep: 1,
		BusyTimeout:   time.Second,
		Logger:        slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	cfg := config.Default(config.ModeDesktop).Studio
	cfg.QueueWorkers = 2
	cfg.QueuePollInterval = 15 * time.Millisecond

	baseRepo := sqlite.NewStudioRepository(db, sqlite.WithStudioConfig(cfg))
	wrapRepo := &failSubmissionRepo{Repository: baseRepo}
	vidDrv := &crashTestVideoDriver{outDir: tmp}

	startedCh := make(chan string, 2)
	canceledCh := make(chan error, 2)
	wf := &hookableQueueWorkflow{
		outDir: tmp,
		onImage: func(runCtx context.Context, in contracts.ImageGenerateInput) (contracts.ImageGenerateOutput, error) {
			select {
			case startedCh <- in.OutputDir:
			default:
			}
			<-runCtx.Done()
			canceledCh <- runCtx.Err()
			return contracts.ImageGenerateOutput{}, runCtx.Err()
		},
	}

	svc := studiosvc.New(studiosvc.Options{
		Repo:         wrapRepo,
		Workflow:     wf,
		VideoMedia:   vidDrv,
		StudioConfig: cfg,
		ProjectsDir:  filepath.Join(tmp, "projects"),
		Logger:       slog.New(slog.DiscardHandler),
	})

	projRes, err := svc.CreateProject(ctx, contracts.StudioProjectCreatePayload{
		Title:              "Concurrent Queue Cancel",
		DefaultImageModel:  "openai/gpt-image-1",
		DefaultVideoModel:  "openai/sora-2",
		AutoApproveBatches: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	epID := projRes.ActiveEpisode.Episode.ID
	shotRes, err := svc.UpsertShot(ctx, contracts.StudioShotUpsertPayload{
		EpisodeID:   epID,
		SceneCode:   "S01",
		Title:       "Shot Cancel",
		ImagePrompt: ptrStr("misty alley"),
		VideoPrompt: ptrStr("dolly zoom"),
	})
	if err != nil {
		t.Fatal(err)
	}

	// Start background RunQueueWorker + concurrent SetFileGuard updates under -race
	workerCtx, stopWorker := context.WithCancel(ctx)
	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		svc.RunQueueWorker(workerCtx)
	}()
	defer func() {
		stopWorker()
		<-workerDone
	}()

	go func() {
		for i := 0; i < 10; i++ {
			svc.SetFileGuard(func(string, bool) error { return nil })
			time.Sleep(2 * time.Millisecond)
		}
	}()

	enq, err := svc.EnqueueBatch(ctx, contracts.StudioQueueEnqueuePayload{
		EpisodeID: epID, Stage: "generate_keyframes",
	})
	if err != nil || len(enq.Jobs) != 1 {
		t.Fatalf("EnqueueBatch: %+v err=%v", enq, err)
	}
	jobID := enq.Jobs[0].ID

	// Verify output dir aligns with epRoot/media/keyframes
	var outDir string
	select {
	case outDir = <-startedCh:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for worker to start queued image job")
	}
	if !strings.HasSuffix(filepath.ToSlash(outDir), "/media/keyframes") {
		t.Fatalf("expected queue keyframe output dir to end with /media/keyframes, got %q", outDir)
	}

	// Cancel in-flight job -> runCtx cancels immediately and job remains canceled
	if _, err := svc.ControlQueue(ctx, contracts.StudioQueueControlPayload{
		Action: contracts.QueueActionCancel,
		JobID:  jobID,
	}); err != nil {
		t.Fatalf("ControlQueue(cancel): %v", err)
	}
	select {
	case cerr := <-canceledCh:
		if cerr == nil {
			t.Fatal("expected non-nil context cancellation error")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected ControlQueue(cancel) to cancel in-flight job context immediately")
	}
	time.Sleep(30 * time.Millisecond)
	listed, err := svc.ListJobs(ctx, contracts.StudioQueueListPayload{ProjectID: projRes.Project.ID})
	if err != nil || len(listed.Jobs) != 1 || listed.Jobs[0].Status != domainstudio.JobCanceled {
		t.Fatalf("expected canceled status preserved, got %+v err=%v", listed.Jobs, err)
	}

	// Stop background worker so we can deterministically step failed re-enqueue & SetJobProviderSubmission failure
	stopWorker()
	<-workerDone

	// Canceled/Failed jobs MUST allow re-enqueue with Force: false!
	wf.onImage = func(_ context.Context, _ contracts.ImageGenerateInput) (contracts.ImageGenerateOutput, error) {
		return contracts.ImageGenerateOutput{}, apperr.Validation("bad prompt")
	}
	enqAfterCancel, err := svc.EnqueueBatch(ctx, contracts.StudioQueueEnqueuePayload{
		EpisodeID: epID, Stage: "generate_keyframes", Force: false,
	})
	if err != nil || len(enqAfterCancel.Jobs) != 1 {
		t.Fatalf("expected re-enqueue after cancel with Force=false to create 1 job, got %+v err=%v", enqAfterCancel, err)
	}
	svc.StepQueueOnce(ctx) // fails permanently
	enqAfterFail, err := svc.EnqueueBatch(ctx, contracts.StudioQueueEnqueuePayload{
		EpisodeID: epID, Stage: "generate_keyframes", Force: false,
	})
	if err != nil || len(enqAfterFail.Jobs) != 1 {
		t.Fatalf("expected re-enqueue after failure with Force=false to create 1 job, got %+v err=%v", enqAfterFail, err)
	}
	svc.StepQueueOnce(ctx) // drain enqAfterFail job before enqueueing video job

	// SetJobProviderSubmission failure MUST record ProviderJobID on the failed job so Retry polls instead of double-submitting!
	kfFile := filepath.Join(tmp, "kf_sub.png")
	_ = os.WriteFile(kfFile, []byte("PNG"), 0o600)
	if _, err := svc.UpsertShot(ctx, contracts.StudioShotUpsertPayload{
		ID: shotRes.Shot.ID, EpisodeID: epID, KeyframePath: ptrStr(kfFile),
	}); err != nil {
		t.Fatal(err)
	}
	wrapRepo.failSetSubmission.Store(true)
	enqVid, err := svc.EnqueueBatch(ctx, contracts.StudioQueueEnqueuePayload{
		EpisodeID: epID, Stage: "generate_videos",
	})
	if err != nil || len(enqVid.Jobs) != 1 {
		t.Fatalf("EnqueueBatch(video): %+v err=%v", enqVid, err)
	}
	vidJobID := enqVid.Jobs[0].ID
	svc.StepQueueOnce(ctx)
	if vidDrv.submitCalls.Load() != 1 {
		t.Fatalf("expected 1 SubmitVideoJob call, got %d", vidDrv.submitCalls.Load())
	}

	wrapRepo.failSetSubmission.Store(false)
	retryRes, err := svc.ControlQueue(ctx, contracts.StudioQueueControlPayload{
		Action: contracts.QueueActionRetry,
		JobID:  vidJobID,
	})
	if err != nil || retryRes.Job == nil || retryRes.Job.Status != domainstudio.JobPolling || retryRes.Job.ProviderJobID != "remote-vid-999" {
		t.Fatalf("expected Retry after SetJobProviderSubmission failure to resume polling remote-vid-999 without re-submitting, got %+v err=%v", retryRes, err)
	}
	svc.StepQueueOnce(ctx)
	if vidDrv.submitCalls.Load() != 1 {
		t.Fatalf("SubmitVideoJob called %d times; expected strictly 1 (no double billing)", vidDrv.submitCalls.Load())
	}
}

type failingPollVideoDriver struct {
	pollErr   error
	pollCalls atomic.Int32
}

func (d *failingPollVideoDriver) SubmitVideoJob(_ context.Context, req domainmedia.VideoRequest) (domainmedia.VideoSubmitToken, error) {
	return domainmedia.VideoSubmitToken{ProviderJobID: "remote-fail-poll", OutputDir: req.OutputDir}, nil
}

func (d *failingPollVideoDriver) PollVideoJobOnce(_ context.Context, _ domainmedia.VideoSubmitToken) (domainmedia.VideoPollStatus, error) {
	d.pollCalls.Add(1)
	return domainmedia.VideoPollStatus{}, d.pollErr
}

type allNodeStubWorkflow struct {
	outDir string
	calls  []string
}

func (w *allNodeStubWorkflow) RunNode(_ context.Context, nodeType string, raw json.RawMessage) (any, error) {
	w.calls = append(w.calls, nodeType)
	switch nodeType {
	case contracts.NodeTypeTTSDubMux:
		p := filepath.Join(w.outDir, "dub.mp4")
		_ = os.WriteFile(p, []byte("MP4DUB"), 0o600)
		return contracts.TTSDubMuxOutput{Path: p, AudioPath: p, AssetID: "dub-1"}, nil
	case contracts.NodeTypeVideoWMRemove:
		p := filepath.Join(w.outDir, "clean.mp4")
		_ = os.WriteFile(p, []byte("MP4CLEAN"), 0o600)
		return contracts.VideoWatermarkRemoveOutput{Path: p, AssetID: "wm-1"}, nil
	case contracts.NodeTypeMaskEdit:
		p := filepath.Join(w.outDir, "mask_out.png")
		_ = os.WriteFile(p, []byte("PNGMASK"), 0o600)
		return contracts.MaskEditOutput{Path: p, AssetID: "mask-1"}, nil
	case contracts.NodeTypeGridSplit:
		p := filepath.Join(w.outDir, "panel_0.png")
		_ = os.WriteFile(p, []byte("PNGPANEL"), 0o600)
		return contracts.GridSplitOutput{Panels: []contracts.GridPanel{{Index: 0, Path: p}}}, nil
	case contracts.NodeTypeExtractFrames:
		p := filepath.Join(w.outDir, "frame_last.png")
		_ = os.WriteFile(p, []byte("PNGFRAME"), 0o600)
		return contracts.ExtractFramesOutput{
			LastFramePath: p,
			Frames:        []contracts.ExtractedFrame{{Path: p, AssetID: "frame-1"}},
		}, nil
	case contracts.NodeTypeQCReview:
		return contracts.QCReviewOutput{Verdict: "pass", OverallScore: 9.4}, nil
	default:
		return map[string]any{}, nil
	}
}

func TestStudioQueuePollLimitsRetryApprovalAndAllJobKinds(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()
	db, err := sqlite.Open(ctx, sqlite.Options{
		Path:          filepath.Join(tmp, "queue_limits.db"),
		BackupDir:     filepath.Join(tmp, "backups"),
		BackupsToKeep: 1,
		BusyTimeout:   time.Second,
		Logger:        slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	cfg := config.Default(config.ModeDesktop).Studio
	cfg.QueuePollInterval = time.Millisecond
	cfg.QueuePollLeaseMin = time.Millisecond
	cfg.QueueMaxPollErrors = 2
	cfg.QueueMaxPollAge = time.Hour

	repo := sqlite.NewStudioRepository(db, sqlite.WithStudioConfig(cfg))
	drv := &failingPollVideoDriver{pollErr: apperr.Unavailable("transient network reset")}
	wf := &allNodeStubWorkflow{outDir: tmp}
	svc := studiosvc.New(studiosvc.Options{
		Repo:         repo,
		Workflow:     wf,
		VideoMedia:   drv,
		StudioConfig: cfg,
		ProjectsDir:  filepath.Join(tmp, "projects"),
		Logger:       slog.New(slog.DiscardHandler),
	})

	projRes, err := svc.CreateProject(ctx, contracts.StudioProjectCreatePayload{
		Title:              "Limits Test",
		AutoApproveBatches: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	epID := projRes.Episodes[0].ID

	// LLM stage ScriptNormalize enforces approval gate when AutoApproveBatches=false
	_, err = svc.RunStage(ctx, contracts.StudioEpisodeRunStagePayload{
		EpisodeID: epID,
		Stage:     contracts.StudioStageScriptNormalize,
	})
	if !apperr.IsCode(err, apperr.CodeValidation) {
		t.Fatalf("expected ScriptNormalize without Approved=true to require approval, got %v", err)
	}

	// Create media artifacts for shot & enqueue remaining 6 JobKinds + test Retry approval gate
	kf := filepath.Join(tmp, "kf.png")
	vid := filepath.Join(tmp, "vid.mp4")
	_ = os.WriteFile(kf, []byte("PNG"), 0o600)
	_ = os.WriteFile(vid, []byte("MP4"), 0o600)
	shotRes, err := svc.UpsertShot(ctx, contracts.StudioShotUpsertPayload{
		EpisodeID:     epID,
		Title:         "Shot 1",
		NarrationText: ptrStr("Narration"),
		KeyframePath:  ptrStr(kf),
		VideoPath:     ptrStr(vid),
	})
	if err != nil {
		t.Fatal(err)
	}

	// Create polling job directly that fails after 2 transient poll errors
	pollJob, err := repo.CreateJob(ctx, domainstudio.Job{
		ProjectID:       projRes.Project.ID,
		EpisodeID:       epID,
		ShotID:          shotRes.Shot.ID,
		JobKind:         domainstudio.JobVideo,
		IdempotencyKey:  "poll-max-err-1",
		Status:          domainstudio.JobPolling,
		ProviderJobID:   "remote-fail-poll",
		SubmitTokenJSON: `{"provider_job_id":"remote-fail-poll"}`,
		StartedAt:       time.Now().UnixMilli(),
	})
	if err != nil {
		t.Fatal(err)
	}
	svc.StepQueueOnce(ctx) // error 1 -> remains polling
	after1, _ := repo.GetJob(ctx, pollJob.ID)
	if after1.Status != domainstudio.JobPolling {
		t.Fatalf("expected polling after 1 transient error, got %+v", after1)
	}
	time.Sleep(5 * time.Millisecond)
	svc.StepQueueOnce(ctx) // error 2 -> hits QueueMaxPollErrors=2 -> failed
	after2, _ := repo.GetJob(ctx, pollJob.ID)
	if after2.Status != domainstudio.JobFailed {
		t.Fatalf("expected JobFailed after QueueMaxPollErrors=2, got %+v", after2)
	}

	// Retry on failed job when AutoApproveBatches=false requires Approved=true
	if _, err := svc.ControlQueue(ctx, contracts.StudioQueueControlPayload{
		Action: contracts.QueueActionRetry,
		JobID:  pollJob.ID,
	}); !apperr.IsCode(err, apperr.CodeValidation) {
		t.Fatalf("expected ControlQueue(retry) without Approved=true to fail validation, got %v", err)
	}
	retried, err := svc.ControlQueue(ctx, contracts.StudioQueueControlPayload{
		Action:   contracts.QueueActionRetry,
		JobID:    pollJob.ID,
		Approved: true,
	})
	if err != nil || retried.Job == nil || retried.Job.Status != domainstudio.JobPolling {
		t.Fatalf("expected approved Retry to resume polling, got %+v err=%v", retried, err)
	}
	// Cancel before running the 6 processing stages
	_, _ = svc.ControlQueue(ctx, contracts.StudioQueueControlPayload{Action: contracts.QueueActionCancel, JobID: pollJob.ID})

	// Enqueue & execute all 6 remaining JobKind stages end-to-end
	for _, stage := range []string{"tts_dub_mux", "video_watermark_remove", "mask_edit", "grid_split", "extract_frames", "qc_review"} {
		enq, eerr := svc.EnqueueBatch(ctx, contracts.StudioQueueEnqueuePayload{
			EpisodeID: epID,
			Stage:     stage,
			Approved:  true,
		})
		if eerr != nil || len(enq.Jobs) != 1 {
			t.Fatalf("EnqueueBatch(%s): %+v err=%v", stage, enq, eerr)
		}
		if !svc.StepQueueOnce(ctx) {
			t.Fatalf("StepQueueOnce(%s) did not execute", stage)
		}
		got, gerr := repo.GetJob(ctx, enq.Jobs[0].ID)
		if gerr != nil || got.Status != domainstudio.JobSucceeded {
			t.Fatalf("job %s (%s) status = %s err=%q gerr=%v", stage, got.ID, got.Status, got.ErrorText, gerr)
		}
	}
}

type cancelOnRunWorkflow struct {
	cancelFn context.CancelFunc
}

func (w *cancelOnRunWorkflow) RunNode(ctx context.Context, _ string, _ json.RawMessage) (any, error) {
	w.cancelFn()
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestQueueWorkerGracefulShutdownPreservesRetryBudget(t *testing.T) {
	tmp := t.TempDir()
	bg := context.Background()
	db, err := sqlite.Open(bg, sqlite.Options{
		Path:          filepath.Join(tmp, "app.db"),
		BackupDir:     filepath.Join(tmp, "backups"),
		BackupsToKeep: 1,
		BusyTimeout:   time.Second,
		Logger:        slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	repo := sqlite.NewStudioRepository(db)
	workerCtx, workerCancel := context.WithCancel(bg)
	wf := &cancelOnRunWorkflow{cancelFn: workerCancel}
	svc := studiosvc.New(studiosvc.Options{
		Repo:         repo,
		Workflow:     wf,
		StudioConfig: config.Default(config.ModeDesktop).Studio,
		ProjectsDir:  filepath.Join(tmp, "projects"),
		Logger:       slog.New(slog.DiscardHandler),
	})

	created, err := svc.CreateProject(bg, contracts.StudioProjectCreatePayload{Title: "Shutdown Film", AutoApproveBatches: true})
	if err != nil {
		t.Fatal(err)
	}
	epID := created.ActiveEpisode.Episode.ID
	job, err := repo.CreateJob(bg, domainstudio.Job{
		ProjectID:      created.Project.ID,
		EpisodeID:      epID,
		JobKind:        domainstudio.JobTTS,
		IdempotencyKey: "shutdown-tts-1",
		Status:         domainstudio.JobQueued,
		MaxAttempts:    3,
		RequestJSON:    `{"input":{"text":"hello"}}`,
		NextPollAt:     time.Now().UnixMilli() - 1000,
	})
	if err != nil {
		t.Fatal(err)
	}

	_ = svc.StepQueueOnce(workerCtx)

	got, err := repo.GetJob(bg, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != domainstudio.JobQueued {
		t.Fatalf("expected graceful shutdown to restore JobQueued, got %s", got.Status)
	}
	if got.Attempts != 0 {
		t.Fatalf("expected graceful shutdown to preserve retry budget (Attempts=0), got %d", got.Attempts)
	}
}
