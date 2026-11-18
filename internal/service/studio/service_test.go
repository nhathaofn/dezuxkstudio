package studio_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"app/internal/config"
	"app/internal/contracts"
	domainstudio "app/internal/domain/studio"
	"app/internal/engine"
	"app/internal/security"
	"app/internal/service/media"
	"app/internal/service/studio"
	"app/internal/storage/sqlite"
	mediagennodes "app/internal/tools/mediagen"
	studionodes "app/internal/tools/studio"
)

type fakeWorkflow struct {
	mu      sync.Mutex
	calls   []string
	counter int
}

func (f *fakeWorkflow) RunNode(_ context.Context, nodeType string, input json.RawMessage) (any, error) {
	f.mu.Lock()
	f.calls = append(f.calls, nodeType)
	f.counter++
	seq := f.counter
	f.mu.Unlock()

	switch nodeType {
	case studionodes.NodeTypeScriptNormalize:
		var in contracts.ScriptNormalizeInput
		_ = json.Unmarshal(input, &in)
		outJSON := `{"title":"Episode 1","genre":"drama","aspect_ratio":"16:9","logline":"Courier delivers key","scenes":[{"code":"S01","title":"Cafe","beats":[{"code":"B01","description":"Enters cafe","duration_sec":5,"asset_tags":["@Minh"]}]}]}`
		scriptPath := ""
		if in.ProjectDir != "" {
			scriptPath = filepath.Join(in.ProjectDir, "script", "normalized_script.json")
			_ = os.MkdirAll(filepath.Dir(scriptPath), 0o755)
			_ = os.WriteFile(scriptPath, []byte(outJSON), 0o644)
		}
		return contracts.ScriptNormalizeOutput{
			NormalizedScriptJSON: outJSON,
			ScriptPath:           scriptPath,
			SceneCount:           1,
			BeatCount:            1,
		}, nil

	case studionodes.NodeTypeExtractAssets:
		var in contracts.ExtractAssetsInput
		_ = json.Unmarshal(input, &in)
		outJSON := `{"style_prompt":"Neo-noir","assets":[{"kind":"character","name":"Minh","code_tag":"@Minh","description":"Courier","prompt":"3-view turnaround of @Minh"}]}`
		assetsPath := ""
		if in.ProjectDir != "" {
			assetsPath = filepath.Join(in.ProjectDir, "assets", "asset_bible.json")
			_ = os.MkdirAll(filepath.Dir(assetsPath), 0o755)
			_ = os.WriteFile(assetsPath, []byte(outJSON), 0o644)
		}
		return contracts.ExtractAssetsOutput{
			AssetBibleJSON: outJSON,
			AssetsPath:     assetsPath,
			AssetCount:     1,
		}, nil

	case studionodes.NodeTypeAuthorShotPrompts:
		var in contracts.AuthorShotPromptsInput
		_ = json.Unmarshal(input, &in)
		outJSON := `{"production_mode":"storyboard_i2v","shots":[{"seq":1,"scene_code":"S01","title":"Cafe Entrance","shot_mode":"single","duration_sec":5,"image_prompt":"Close-up of @Minh","video_prompt":"Slow push in","narration_text":"Heavy rain outside.","ref_asset_tags":["@Minh"]}]}`
		shotsPath := ""
		if in.ProjectDir != "" {
			shotsPath = filepath.Join(in.ProjectDir, "storyboard", "shots.json")
			_ = os.MkdirAll(filepath.Dir(shotsPath), 0o755)
			_ = os.WriteFile(shotsPath, []byte(outJSON), 0o644)
		}
		return contracts.AuthorShotPromptsOutput{
			ShotsJSON: outJSON,
			ShotsPath: shotsPath,
			ShotCount: 1,
		}, nil

	case mediagennodes.NodeTypeImage:
		var in mediagennodes.ImageGenerateInput
		_ = json.Unmarshal(input, &in)
		p := filepath.Join(in.OutputDir, fmt.Sprintf("img_%d.png", seq))
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		_ = os.WriteFile(p, []byte("png-data"), 0o644)
		return mediagennodes.ImageGenerateOutput{
			Image:     engine.RefFor(p, engine.MediaImage),
			Path:      p,
			MimeType:  "image/png",
			SizeBytes: 8,
		}, nil

	case mediagennodes.NodeTypeVideo:
		var in mediagennodes.VideoGenerateInput
		_ = json.Unmarshal(input, &in)
		p := filepath.Join(in.OutputDir, fmt.Sprintf("vid_%d.mp4", seq))
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		_ = os.WriteFile(p, []byte("mp4-data"), 0o644)
		return mediagennodes.VideoGenerateOutput{
			Video:     engine.RefFor(p, engine.MediaVideo),
			Path:      p,
			MimeType:  "video/mp4",
			Duration:  float64(in.Duration),
			SizeBytes: 8,
		}, nil

	case mediagennodes.NodeTypeTTS:
		var in mediagennodes.TTSGenerateInput
		_ = json.Unmarshal(input, &in)
		p := filepath.Join(in.OutputDir, fmt.Sprintf("tts_%d.wav", seq))
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		_ = os.WriteFile(p, []byte("wav-data"), 0o644)
		return mediagennodes.TTSGenerateOutput{
			Audio:     engine.RefFor(p, engine.MediaAudio),
			Path:      p,
			MimeType:  "audio/wav",
			SizeBytes: 8,
		}, nil

	case studionodes.NodeTypeVideoConcatBasic:
		var in contracts.VideoConcatBasicInput
		_ = json.Unmarshal(input, &in)
		p := filepath.Join(in.OutputDir, "final_movie.mp4")
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		_ = os.WriteFile(p, []byte("assembled-mp4"), 0o644)
		return contracts.VideoConcatBasicOutput{
			Video:       engine.RefFor(p, engine.MediaVideo),
			OutputPath:  p,
			AssetID:     "assembled-1",
			DurationSec: 5.0,
			ClipCount:   1,
		}, nil
	}
	return nil, fmt.Errorf("unexpected node type %q", nodeType)
}

func setupStudioServiceTest(t *testing.T) (*studio.Service, *sqlite.DB, *media.Service, *fakeWorkflow, string, *[]string) {
	t.Helper()
	root := t.TempDir()
	projectsDir := filepath.Join(root, "workspace", "projects")
	mediaDir := filepath.Join(root, "media")
	_ = os.MkdirAll(projectsDir, 0o755)
	_ = os.MkdirAll(mediaDir, 0o755)

	db, err := sqlite.Open(context.Background(), sqlite.Options{
		Path:          filepath.Join(root, "app.db"),
		BackupDir:     filepath.Join(root, "backups"),
		BackupsToKeep: 2,
		BusyTimeout:   time.Second,
		Logger:        slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	signer := security.NewTicketSigner([]byte("0123456789abcdef0123456789abcdef"))
	mediaSvc := media.New(media.Options{
		Repo:         sqlite.NewMediaRepository(db),
		Signer:       signer,
		MediaDir:     mediaDir,
		AllowedRoots: []string{mediaDir, projectsDir},
		Logger:       slog.New(slog.DiscardHandler),
	})

	var mu sync.Mutex
	var events []string
	wf := &fakeWorkflow{}

	svc := studio.New(studio.Options{
		Repo:         sqlite.NewStudioRepository(db),
		Workflow:     wf,
		Media:        mediaSvc,
		StudioConfig: config.Default(config.ModeDesktop).Studio,
		ProjectsDir:  projectsDir,
		OnEvent: func(name string, _ any) {
			mu.Lock()
			events = append(events, name)
			mu.Unlock()
		},
		Logger: slog.New(slog.DiscardHandler),
	})

	return svc, db, mediaSvc, wf, projectsDir, &events
}

func TestStudioService_FullMVPWorkflowAndWorkspaceSync(t *testing.T) {
	ctx := context.Background()
	svc, _, _, wf, projectsDir, events := setupStudioServiceTest(t)

	// 1. Create Project (creates slug folder, project.json, ep01)
	created, err := svc.CreateProject(ctx, contracts.StudioProjectCreatePayload{
		Title:              "Mưa Đêm Sài Gòn",
		Genre:              domainstudio.GenreDrama,
		ProductionMode:     domainstudio.ModeStoryboardI2V,
		AspectRatio:        "16:9",
		StylePrompt:        "Neo-noir anime",
		AutoApproveBatches: true,
	})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if created.ActiveEpisode == nil {
		t.Fatal("expected initial ActiveEpisode")
	}
	epID := created.ActiveEpisode.Episode.ID
	if created.Project.Slug != "mua-dem-sai-gon" {
		t.Fatalf("unexpected slug: %q", created.Project.Slug)
	}
	if _, err := os.Stat(filepath.Join(projectsDir, "mua-dem-sai-gon", "project.json")); err != nil {
		t.Fatalf("expected project.json on disk: %v", err)
	}

	// 2. Save raw script
	rawScript := "Minh bước vào quán cà phê giữa cơn mưa."
	saved, err := svc.SaveScript(ctx, contracts.StudioEpisodeSaveScriptPayload{
		EpisodeID: epID,
		RawScript: &rawScript,
	})
	if err != nil {
		t.Fatalf("SaveScript: %v", err)
	}
	if saved.RawScript != rawScript {
		t.Fatalf("expected raw script persisted, got %q", saved.RawScript)
	}

	// 3. Run all Phase 1 stages sequentially
	stages := []contracts.StudioRunStageAction{
		contracts.StudioStageScriptNormalize,
		contracts.StudioStageExtractAssets,
		contracts.StudioStageGenerateAssets,
		contracts.StudioStageAuthorShots,
		contracts.StudioStageGenerateKeyframes,
		contracts.StudioStageGenerateTTS,
		contracts.StudioStageGenerateVideos,
		contracts.StudioStageConcatVideo,
	}
	var lastRes contracts.StudioRunStageResult
	for _, st := range stages {
		lastRes, err = svc.RunStage(ctx, contracts.StudioEpisodeRunStagePayload{
			EpisodeID: epID,
			Stage:     st,
		})
		if err != nil {
			t.Fatalf("RunStage(%s): %v", st, err)
		}
	}

	if lastRes.OutputPath == "" {
		t.Fatal("expected assembled video output path")
	}
	if lastRes.Episode.FinalVideoPath != lastRes.OutputPath {
		t.Fatalf("expected FinalVideoPath %q, got %q", lastRes.OutputPath, lastRes.Episode.FinalVideoPath)
	}
	if len(lastRes.Episode.Assets) != 1 || lastRes.Episode.Assets[0].PrimaryAssetID == "" {
		t.Fatalf("expected registered asset primary ID, got %+v", lastRes.Episode.Assets)
	}
	if len(lastRes.Episode.Shots) != 1 || lastRes.Episode.Shots[0].VideoAssetID == "" || lastRes.Episode.Shots[0].KeyframeAssetID == "" {
		t.Fatalf("expected shot media asset IDs populated, got %+v", lastRes.Episode.Shots)
	}
	if len(wf.calls) != len(stages) {
		t.Fatalf("expected %d node calls, got %d (%v)", len(stages), len(wf.calls), wf.calls)
	}
	if len(*events) == 0 {
		t.Fatal("expected studio events emitted")
	}

	// 4. Hybrid Disk -> SQLite SyncWorkspace: modify asset_bible.json on disk preserving PrimaryPath
	biblePath := filepath.Join(created.Project.RootDir, "assets", "asset_bible.json")
	var pkg domainstudio.AssetBiblePackage
	b, _ := os.ReadFile(biblePath)
	_ = json.Unmarshal(b, &pkg)
	pkg.Assets = append(pkg.Assets, domainstudio.AssetEntry{
		Kind:        domainstudio.AssetScene,
		Name:        "Old Diner",
		CodeTag:     "@Diner",
		Description: "Wet neon windows",
		Prompt:      "Wide shot retro diner interior",
	})
	updBytes, _ := json.MarshalIndent(pkg, "", "  ")
	if err := os.WriteFile(biblePath, updBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	synced, err := svc.SyncWorkspace(ctx, created.Project.ID)
	if err != nil {
		t.Fatalf("SyncWorkspace: %v", err)
	}
	if len(synced.Assets) != 2 {
		t.Fatalf("expected 2 synced assets, got %d", len(synced.Assets))
	}
	if synced.Assets[0].PrimaryPath == "" {
		t.Fatal("expected SyncWorkspace to preserve existing PrimaryPath on @Minh")
	}

	// 5. Reorder & Delete Project with DeleteFiles=true
	newProjectsDir := filepath.Join(t.TempDir(), "projects2")
	svc.SetProjectsDir(newProjectsDir)
	if svc.ProjectsDir() != newProjectsDir {
		t.Fatalf("expected updated projects dir %q, got %q", newProjectsDir, svc.ProjectsDir())
	}
}

func TestStudioService_FileGuardsAndResolvedVersionModel(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()
	db, err := sqlite.Open(ctx, sqlite.Options{
		Path:          filepath.Join(tmp, "studio_guards.db"),
		BackupDir:     filepath.Join(tmp, "backups"),
		BackupsToKeep: 1,
		BusyTimeout:   time.Second,
		Logger:        slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	projectsDir := filepath.Join(tmp, "projects")
	svc := studio.New(studio.Options{
		Repo:         sqlite.NewStudioRepository(db),
		Workflow:     &fakeWorkflow{},
		StudioConfig: config.Default(config.ModeDesktop).Studio,
		ProjectsDir:  projectsDir,
		Logger:       slog.New(slog.DiscardHandler),
	})

	created, err := svc.CreateProject(ctx, contracts.StudioProjectCreatePayload{
		Title:              "Guarded Project",
		DefaultImageModel:  "openai/gpt-image-1",
		DefaultVideoModel:  "openai/sora-2",
		AutoApproveBatches: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	epID := created.ActiveEpisode.Episode.ID

	// 1. SaveScript enforces FileGuard before writing workspace files
	denyWriteCtx := engine.WithFileGuard(ctx, func(_ string, write bool) error {
		if write {
			return errors.New("write blocked by guard")
		}
		return nil
	})
	blockedScript := "SHOULD NOT WRITE"
	if _, serr := svc.SaveScript(denyWriteCtx, contracts.StudioEpisodeSaveScriptPayload{
		EpisodeID: epID,
		RawScript: &blockedScript,
	}); serr == nil || !strings.Contains(serr.Error(), "write blocked by guard") {
		t.Fatalf("expected SaveScript write guard error, got %v", serr)
	}
	rawPath := created.ActiveEpisode.Episode.RawScriptPath
	if rawBytes, rerr := os.ReadFile(rawPath); rerr == nil && strings.Contains(string(rawBytes), blockedScript) {
		t.Fatalf("expected raw script not written when FileGuard blocks write")
	}

	// 2. RunStage(generate_assets, generate_keyframes, generate_videos) with empty p.Model
	//    records resolved DefaultImageModel / DefaultVideoModel on MediaVersions.
	validScript := "Minh walks into a cafe."
	if _, serr := svc.SaveScript(ctx, contracts.StudioEpisodeSaveScriptPayload{
		EpisodeID: epID, RawScript: &validScript,
	}); serr != nil {
		t.Fatal(serr)
	}
	for _, st := range []contracts.StudioRunStageAction{
		contracts.StudioStageScriptNormalize,
		contracts.StudioStageExtractAssets,
		contracts.StudioStageGenerateAssets,
		contracts.StudioStageAuthorShots,
		contracts.StudioStageGenerateKeyframes,
		contracts.StudioStageGenerateVideos,
	} {
		if _, rerr := svc.RunStage(ctx, contracts.StudioEpisodeRunStagePayload{EpisodeID: epID, Stage: st}); rerr != nil {
			t.Fatalf("RunStage(%s): %v", st, rerr)
		}
	}
	epDetail, err := svc.GetEpisode(ctx, epID)
	if err != nil || len(epDetail.Assets) == 0 || len(epDetail.Shots) == 0 {
		t.Fatalf("unexpected episode detail: %+v err=%v", epDetail, err)
	}
	assetVers, err := svc.ListVersions(ctx, contracts.StudioVersionsListPayload{
		TargetKind: "asset", TargetID: epDetail.Assets[0].ID,
	})
	if err != nil || len(assetVers.Versions) == 0 || assetVers.Versions[0].ModelRef != "openai/gpt-image-1" {
		t.Fatalf("expected resolved asset version model_ref openai/gpt-image-1, got %+v err=%v", assetVers.Versions, err)
	}
	vidVers, err := svc.ListVersions(ctx, contracts.StudioVersionsListPayload{
		TargetKind: "video", TargetID: epDetail.Shots[0].ID,
	})
	if err != nil || len(vidVers.Versions) == 0 || vidVers.Versions[0].ModelRef != "openai/sora-2" {
		t.Fatalf("expected resolved video version model_ref openai/sora-2, got %+v err=%v", vidVers.Versions, err)
	}

	// 3. DeleteProject(DeleteFiles: true) enforces FileGuard before os.RemoveAll
	if derr := svc.DeleteProject(denyWriteCtx, contracts.StudioProjectDeletePayload{
		ID:          created.Project.ID,
		DeleteFiles: true,
	}); derr == nil || !strings.Contains(derr.Error(), "write blocked by guard") {
		t.Fatalf("expected DeleteProject FileGuard error, got %v", derr)
	}
	if _, serr := os.Stat(filepath.Join(created.Project.RootDir, "project.json")); serr != nil {
		t.Fatalf("expected project directory preserved when FileGuard denied DeleteFiles: %v", serr)
	}

	// Clean DeleteProject with permitted FileGuard removes project dir on disk
	created2, err := svc.CreateProject(ctx, contracts.StudioProjectCreatePayload{Title: "Disposable Film"})
	if err != nil {
		t.Fatal(err)
	}
	if derr := svc.DeleteProject(ctx, contracts.StudioProjectDeletePayload{
		ID:          created2.Project.ID,
		DeleteFiles: true,
	}); derr != nil {
		t.Fatalf("expected permitted DeleteProject to succeed, got %v", derr)
	}
	if _, serr := os.Stat(created2.Project.RootDir); !os.IsNotExist(serr) {
		t.Fatalf("expected project dir deleted, stat err=%v", serr)
	}
}
