package agent

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"app/internal/config"
	"app/internal/contracts"
	domainagent "app/internal/domain/agent"
	domainmedia "app/internal/domain/mediagen"
	domainstudio "app/internal/domain/studio"
	"app/internal/engine"
	"app/internal/paths"
	studiosvc "app/internal/service/studio"
	"app/internal/service/workflow"
	"app/internal/storage/sqlite"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type stubStudioMCPTextGen struct{}

func (s *stubStudioMCPTextGen) GenerateText(_ context.Context, req domainmedia.TextRequest) (domainmedia.TextResult, error) {
	switch req.JSONSchemaName {
	case "normalized_script":
		payload := json.RawMessage(`{"title":"Ep 1","genre":"drama","aspect_ratio":"16:9","logline":"L","scenes":[{"code":"S01","title":"Room","beats":[{"code":"B01","description":"Enter","duration_sec":5,"asset_tags":["@An"]}]}]}`)
		return domainmedia.TextResult{Text: string(payload), ParsedJSON: payload, Provider: "stub", Model: "text-1"}, nil
	default:
		return domainmedia.TextResult{Text: "{}", ParsedJSON: json.RawMessage(`{}`)}, nil
	}
}

func callMCPJSON(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) map[string]any {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      name,
		Arguments: args,
	})
	if err != nil {
		t.Fatalf("CallTool(%s): %v", name, err)
	}
	if res.IsError {
		var msg string
		if len(res.Content) > 0 {
			if tc, ok := res.Content[0].(*mcp.TextContent); ok {
				msg = tc.Text
			}
		}
		t.Fatalf("CallTool(%s) returned error: %s", name, msg)
	}
	tc, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("CallTool(%s) expected TextContent, got %T", name, res.Content[0])
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(tc.Text), &out); err != nil {
		t.Fatalf("unmarshal CallTool(%s): %v (%s)", name, err, tc.Text)
	}
	return out
}

func TestStudioMCPTools_CRUDAndApprovalGate(t *testing.T) {
	base := t.TempDir()
	ws := filepath.Join(base, "data", "workspace")
	projectsDir := filepath.Join(ws, "projects")
	mediaDir := filepath.Join(base, "data", "media")
	profilesDir := filepath.Join(base, "data", "agent-profiles")
	outsideDir := t.TempDir()
	for _, d := range []string{ws, projectsDir, mediaDir, profilesDir} {
		_ = os.MkdirAll(d, 0o755)
	}

	db, err := sqlite.Open(context.Background(), sqlite.Options{
		Path:          filepath.Join(base, "data", "studio.db"),
		BackupDir:     filepath.Join(base, "data", "backups"),
		BackupsToKeep: 1,
		BusyTimeout:   time.Second,
		Logger:        slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	wf, err := workflow.New(workflow.Options{
		Paths:   paths.Paths{Data: filepath.Join(base, "data")},
		Logger:  slog.New(slog.DiscardHandler),
		TextGen: &stubStudioMCPTextGen{},
	})
	if err != nil {
		t.Fatal(err)
	}

	stSvc := studiosvc.New(studiosvc.Options{
		Repo:         sqlite.NewStudioRepository(db),
		Workflow:     wf,
		StudioConfig: config.Default(config.ModeDesktop).Studio,
		ProjectsDir:  projectsDir,
		Logger:       slog.New(slog.DiscardHandler),
	})

	agentRepo := sqlite.NewAgentRepository(db)
	ctx := context.Background()
	if err := agentRepo.SaveConversation(ctx, domainagent.Conversation{
		ID:         "test-conv-1",
		ProviderID: domainagent.ProviderCodex,
		Title:      "Studio Test",
		CreatedAt:  time.Now().UnixMilli(),
		UpdatedAt:  time.Now().UnixMilli(),
	}); err != nil {
		t.Fatal(err)
	}
	svc, err := New(Options{
		Repo:         agentRepo,
		DataDir:      filepath.Join(base, "data"),
		WorkspaceDir: ws,
		MediaDir:     mediaDir,
		ProfilesDir:  profilesDir,
		Logger:       slog.New(slog.DiscardHandler),
		Workflow:     wf,
		Studio:       stSvc,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Close() })
	svc.policy.protected = []string{profilesDir, filepath.Join(base, "data", "logs")}
	svc.mu.Lock()
	svc.conversationID = "test-conv-1"
	svc.mu.Unlock()

	serverT, clientT := mcp.NewInMemoryTransports()
	if _, err := svc.buildMCPServer().Connect(ctx, serverT, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cs.Close() }()

	// 1. Create a gated project (auto_approve_batches=false) via MCP
	created := callMCPJSON(t, cs, "studio_project_create", map[string]any{
		"title":                "Cyber Rain",
		"genre":                "drama",
		"production_mode":      "storyboard_i2v",
		"auto_approve_batches": false,
	})
	projObj := created["project"].(map[string]any)
	projID := projObj["id"].(string)
	epObj := created["active_episode"].(map[string]any)["episode"].(map[string]any)
	epID := epObj["id"].(string)

	// 2. Save script + upsert asset + upsert shot via MCP
	callMCPJSON(t, cs, "studio_save_script", map[string]any{
		"episode_id": epID,
		"raw_script": "An bước vào hẻm nhỏ dưới mưa.",
	})
	callMCPJSON(t, cs, "studio_asset_upsert", map[string]any{
		"project_id":  projID,
		"kind":        "character",
		"name":        "An",
		"code_tag":    "@An",
		"description": "Detective",
		"prompt":      "3-view turnaround sheet of An",
	})
	callMCPJSON(t, cs, "studio_shot_upsert", map[string]any{
		"episode_id":   epID,
		"scene_code":   "S01",
		"title":        "Alley Entrance",
		"image_prompt": "Wide shot of @An entering neon alley",
		"video_prompt": "Slow tracking shot",
	})

	// Verify Disk <-> SQLite sync after external disk edit
	biblePath := filepath.Join(projObj["root_dir"].(string), "assets", "asset_bible.json")
	rawBible, _ := os.ReadFile(biblePath)
	var biblePkg domainstudio.AssetBiblePackage
	_ = json.Unmarshal(rawBible, &biblePkg)
	biblePkg.Assets = append(biblePkg.Assets, domainstudio.AssetEntry{
		Kind: domainstudio.AssetProp, Name: "Key", CodeTag: "@Key", Prompt: "Silver antique key",
	})
	newBible, _ := json.MarshalIndent(biblePkg, "", "  ")
	if err := os.WriteFile(biblePath, newBible, 0o644); err != nil {
		t.Fatal(err)
	}
	synced := callMCPJSON(t, cs, "studio_sync_workspace", map[string]any{"project_id": projID})
	if len(synced["assets"].([]any)) != 2 {
		t.Fatalf("expected 2 synced assets, got %v", synced["assets"])
	}

	// 3. Running stage on auto_approve_batches=false MUST require approval
	stageRes := callMCPJSON(t, cs, "studio_episode_run_stage", map[string]any{
		"episode_id": epID,
		"stage":      "script_normalize",
	})
	if stageRes["status"] != "needs_approval" {
		t.Fatalf("expected needs_approval when auto_approve_batches=false, got %+v", stageRes)
	}
	appID, _ := stageRes["approval_id"].(string)
	if appID == "" {
		t.Fatal("expected non-empty approval_id")
	}
	if err := svc.ResolveApproval(ctx, appID, true); err != nil {
		t.Fatalf("ResolveApproval: %v", err)
	}
	gotEp := callMCPJSON(t, cs, "studio_episode_get", map[string]any{"episode_id": epID})
	if gotEp["normalized_script"] == nil {
		t.Fatal("expected normalized_script populated after ResolveApproval")
	}

	// 4. Reject untrusted outside narration_audio_path
	outsideAudio := filepath.Join(outsideDir, "secret.wav")
	_ = os.WriteFile(outsideAudio, []byte("wav"), 0o644)
	errCall, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name: "studio_episode_run_stage",
		Arguments: map[string]any{
			"episode_id":           epID,
			"stage":                "concat_video",
			"narration_audio_path": outsideAudio,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !errCall.IsError {
		t.Fatal("expected IsError when narration_audio_path is outside trusted directories")
	}

	ep2 := callMCPJSON(t, cs, "studio_episode_create", map[string]any{
		"project_id": projID,
		"code":       "ep02",
		"title":      "Episode 2",
	})
	if ep2["episode"].(map[string]any)["code"] != "ep02" {
		t.Fatalf("expected studio_episode_create ep02, got %+v", ep2)
	}

	badAssetRes, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name: "studio_asset_upsert",
		Arguments: map[string]any{
			"project_id":   projID,
			"kind":         "character",
			"name":         "Evil",
			"primary_path": outsideAudio,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !badAssetRes.IsError {
		t.Fatal("expected IsError when studio_asset_upsert primary_path is outside trusted directories")
	}

	badShotRes, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name: "studio_shot_upsert",
		Arguments: map[string]any{
			"episode_id": epID,
			"title":      "Evil Shot",
			"video_path": filepath.Join(projObj["root_dir"].(string), "payload.exe"),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !badShotRes.IsError {
		t.Fatal("expected IsError when studio_shot_upsert video_path has forbidden extension")
	}

	// 5. AutoApproveBatches=true inside trusted workspace projectsDir executes directly without needs_approval
	trustedProj := callMCPJSON(t, cs, "studio_project_create", map[string]any{
		"title":                "Auto Trusted Film",
		"auto_approve_batches": true,
	})
	trustedEpID := trustedProj["active_episode"].(map[string]any)["episode"].(map[string]any)["id"].(string)
	callMCPJSON(t, cs, "studio_save_script", map[string]any{
		"episode_id": trustedEpID,
		"raw_script": "Short auto script.",
	})
	autoRun := callMCPJSON(t, cs, "studio_episode_run_stage", map[string]any{
		"episode_id": trustedEpID,
		"stage":      "script_normalize",
	})
	if autoRun["status"] == "needs_approval" {
		t.Fatalf("expected direct execution when auto_approve_batches=true inside trusted workspace, got %+v", autoRun)
	}
	if summary, _ := autoRun["summary"].(string); !strings.Contains(summary, "Normalized") {
		t.Fatalf("expected run summary, got %+v", autoRun)
	}

	// 6. Fail-closed check: AutoApproveBatches=true with UNTRUSTED RootDir must STILL queue approval!
	untrustedSvc := &stubUntrustedStudioManager{untrustedRoot: outsideDir}
	svcUntrusted, err := New(Options{
		Repo:         newFakeRepo(),
		DataDir:      filepath.Join(base, "data2"),
		WorkspaceDir: ws,
		MediaDir:     mediaDir,
		ProfilesDir:  profilesDir,
		Logger:       slog.New(slog.DiscardHandler),
		Studio:       untrustedSvc,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = svcUntrusted.Close() }()
	sT2, cT2 := mcp.NewInMemoryTransports()
	if _, err := svcUntrusted.buildMCPServer().Connect(ctx, sT2, nil); err != nil {
		t.Fatal(err)
	}
	cs2, err := mcp.NewClient(&mcp.Implementation{Name: "test2"}, nil).Connect(ctx, cT2, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cs2.Close() }()
	untrustedRes := callMCPJSON(t, cs2, "studio_episode_run_stage", map[string]any{
		"episode_id": "ep-untrusted",
		"stage":      "script_normalize",
	})
	if untrustedRes["status"] != "needs_approval" {
		t.Fatalf("expected fail-closed needs_approval when RootDir is untrusted, got %+v", untrustedRes)
	}

	// 7. Verify built-in studio customizations are embedded and valid
	custSnap, snapErr := svc.customizations.Snapshot(svc.toolNames())
	if snapErr != nil {
		t.Fatalf("customizations.Snapshot: %v", snapErr)
	}
	foundSkill := false
	foundSub := false
	for _, sk := range custSnap.Skills {
		if sk.Name == "film-director" && sk.Enabled {
			foundSkill = true
		}
	}
	for _, sub := range custSnap.Subagents {
		if sub.Name == "script-analyst" && sub.Enabled && len(sub.Tools) > 0 {
			foundSub = true
		}
	}
	if !foundSkill || !foundSub {
		t.Fatalf("expected film-director skill and valid script-analyst subagent synced (skill=%v, subagent=%v)", foundSkill, foundSub)
	}
}

type stubUntrustedStudioManager struct {
	untrustedRoot string
}

func (s *stubUntrustedStudioManager) ListProjects(context.Context) (contracts.StudioProjectListResult, error) {
	return contracts.StudioProjectListResult{}, nil
}
func (s *stubUntrustedStudioManager) CreateProject(context.Context, contracts.StudioProjectCreatePayload) (contracts.StudioProjectDetailResult, error) {
	return contracts.StudioProjectDetailResult{}, nil
}
func (s *stubUntrustedStudioManager) GetProject(context.Context, string) (contracts.StudioProjectDetailResult, error) {
	return contracts.StudioProjectDetailResult{}, nil
}
func (s *stubUntrustedStudioManager) CreateEpisode(context.Context, contracts.StudioEpisodeCreatePayload) (contracts.StudioEpisodeDetailResult, error) {
	return contracts.StudioEpisodeDetailResult{}, nil
}
func (s *stubUntrustedStudioManager) GetEpisode(_ context.Context, id string) (contracts.StudioEpisodeDetailResult, error) {
	return contracts.StudioEpisodeDetailResult{
		Project: contracts.StudioProject{
			ID:                 "proj-untrusted",
			Title:              "Untrusted",
			RootDir:            s.untrustedRoot,
			AutoApproveBatches: true,
		},
		Episode: contracts.StudioEpisode{
			ID:        id,
			ProjectID: "proj-untrusted",
			Code:      "ep01",
		},
	}, nil
}
func (s *stubUntrustedStudioManager) SaveScript(context.Context, contracts.StudioEpisodeSaveScriptPayload) (contracts.StudioEpisodeDetailResult, error) {
	return contracts.StudioEpisodeDetailResult{}, nil
}
func (s *stubUntrustedStudioManager) UpsertAsset(context.Context, contracts.StudioAssetUpsertPayload) (contracts.StudioAssetUpsertResult, error) {
	return contracts.StudioAssetUpsertResult{}, nil
}
func (s *stubUntrustedStudioManager) UpsertShot(context.Context, contracts.StudioShotUpsertPayload) (contracts.StudioShotUpsertResult, error) {
	return contracts.StudioShotUpsertResult{}, nil
}
func (s *stubUntrustedStudioManager) SyncWorkspace(context.Context, string) (*contracts.StudioProjectDetailResult, error) {
	return nil, nil
}
func (s *stubUntrustedStudioManager) RunStage(context.Context, contracts.StudioEpisodeRunStagePayload) (contracts.StudioRunStageResult, error) {
	return contracts.StudioRunStageResult{Summary: "should not run"}, nil
}

// Keep unused domainagent import satisfied if needed in extended assertions.
var _ = domainagent.ApprovalRequest{}

type postRestartStudioWorkflowStub struct {
	p3ParityWorkflowStub
	qcCalls int
}

func (w *postRestartStudioWorkflowStub) Specs(context.Context) []engine.Spec {
	return nil
}

func (w *postRestartStudioWorkflowStub) RunGraph(context.Context, engine.Graph) (engine.GraphResult, error) {
	return engine.GraphResult{}, nil
}

func (w *postRestartStudioWorkflowStub) RunNode(ctx context.Context, nodeType string, raw json.RawMessage) (any, error) {
	if nodeType == contracts.NodeTypeQCReview {
		w.qcCalls++
		return contracts.QCReviewOutput{
			Verdict:       "pass",
			OverallScore:  9.2,
			VisionSummary: "Clean frame",
			Issues:        []contracts.QCReviewIssue{},
			SampleFrames:  []string{},
		}, nil
	}
	return w.p3ParityWorkflowStub.RunNode(ctx, nodeType, raw)
}

func TestStudioSecurityGatesPostRestartApprovalsAndSubagentManifests(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	dataDir := filepath.Join(base, "data")
	ws := filepath.Join(dataDir, "workspace")
	projectsDir := filepath.Join(ws, "projects")
	mediaDir := filepath.Join(dataDir, "media")
	profilesDir := filepath.Join(dataDir, "agent-profiles")
	outsideDir := t.TempDir()
	for _, d := range []string{ws, projectsDir, mediaDir, profilesDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	db, err := sqlite.Open(ctx, sqlite.Options{
		Path:          filepath.Join(dataDir, "studio_gates.db"),
		BackupDir:     filepath.Join(dataDir, "backups"),
		BackupsToKeep: 1,
		BusyTimeout:   time.Second,
		Logger:        slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	wfStub := &postRestartStudioWorkflowStub{}
	studioRepo := sqlite.NewStudioRepository(db)
	stSvc := studiosvc.New(studiosvc.Options{
		Repo:         studioRepo,
		Workflow:     wfStub,
		StudioConfig: config.Default(config.ModeDesktop).Studio,
		ProjectsDir:  projectsDir,
		Logger:       slog.New(slog.DiscardHandler),
	})

	agentRepo := sqlite.NewAgentRepository(db)
	convID := "conv-post-restart"
	if err := agentRepo.SaveConversation(ctx, domainagent.Conversation{
		ID:         convID,
		ProviderID: domainagent.ProviderCodex,
		Title:      "Post-Restart Security Gates",
		CreatedAt:  time.Now().UnixMilli(),
		UpdatedAt:  time.Now().UnixMilli(),
	}); err != nil {
		t.Fatal(err)
	}

	newAgentSvc := func() *Service {
		s, err := New(Options{
			Repo:         agentRepo,
			DataDir:      dataDir,
			WorkspaceDir: ws,
			ProjectsDir:  projectsDir,
			MediaDir:     mediaDir,
			ProfilesDir:  profilesDir,
			Logger:       slog.New(slog.DiscardHandler),
			Workflow:     wfStub,
			Studio:       stSvc,
		})
		if err != nil {
			t.Fatal(err)
		}
		s.policy.protected = []string{profilesDir, filepath.Join(dataDir, "logs")}
		s.mu.Lock()
		s.conversationID = convID
		s.mu.Unlock()
		return s
	}

	svc1 := newAgentSvc()
	t.Cleanup(func() { _ = svc1.Close() })

	// 1. Validate built-in subagent manifests: both enabled, zero non-existent tools
	availTools := make(map[string]bool)
	for _, tl := range svc1.availableSubagentTools() {
		availTools[tl] = true
	}
	for _, subName := range []string{"visual-reviewer", "script-analyst"} {
		spec, _, rerr := svc1.customizations.ResolveSubagent(subName)
		if rerr != nil {
			t.Fatalf("ResolveSubagent(%s): %v", subName, rerr)
		}
		if !spec.Enabled {
			t.Fatalf("expected built-in subagent %q to be enabled by default", subName)
		}
		if len(spec.Tools) == 0 {
			t.Fatalf("expected built-in subagent %q to declare tools", subName)
		}
		for _, toolName := range spec.Tools {
			if !availTools[toolName] {
				t.Fatalf("subagent %q declares unavailable/unknown MCP tool %q", subName, toolName)
			}
		}
	}

	sT, cT := mcp.NewInMemoryTransports()
	if _, err := svc1.buildMCPServer().Connect(ctx, sT, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "gates-test"}, nil).Connect(ctx, cT, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cs.Close() }()

	created := callMCPJSON(t, cs, "studio_project_create", map[string]any{
		"title":                "Gated Film",
		"auto_approve_batches": false,
	})
	projID := created["project"].(map[string]any)["id"].(string)
	epID := created["active_episode"].(map[string]any)["episode"].(map[string]any)["id"].(string)
	projRoot := created["project"].(map[string]any)["root_dir"].(string)

	validImg := filepath.Join(projRoot, "assets", "frame.png")
	validVid := filepath.Join(projRoot, "episodes", "ep01", "media", "videos", "shot1.mp4")
	outsideImg := filepath.Join(outsideDir, "outside.png")
	dotenvInWs := filepath.Join(ws, ".env")
	_ = os.MkdirAll(filepath.Dir(validImg), 0o755)
	_ = os.MkdirAll(filepath.Dir(validVid), 0o755)
	writeTinyTestPNG(validImg, 8, 8)
	writeTinyTestPNG(outsideImg, 8, 8)
	_ = os.WriteFile(validVid, []byte("MP4BYTES"), 0o600)
	_ = os.WriteFile(dotenvInWs, []byte("API_KEY=secret"), 0o600)

	assetRes := callMCPJSON(t, cs, "studio_asset_upsert", map[string]any{
		"project_id": projID,
		"kind":       "prop",
		"name":       "Relic",
		"code_tag":   "@Relic",
	})
	assetID := assetRes["asset"].(map[string]any)["id"].(string)

	shotRes := callMCPJSON(t, cs, "studio_shot_upsert", map[string]any{
		"episode_id":    epID,
		"title":         "Shot 1",
		"keyframe_path": validImg,
		"video_path":    validVid,
	})
	shotID := shotRes["shot"].(map[string]any)["id"].(string)

	// Seed a failed queue job to test studio_queue_control(retry) approval gate + post-restart execution
	failedJob, err := studioRepo.CreateJob(ctx, domainstudio.Job{
		ProjectID:      projID,
		EpisodeID:      epID,
		ShotID:         shotID,
		JobKind:        domainstudio.JobImage,
		IdempotencyKey: "idem-retry-1",
		Status:         domainstudio.JobFailed,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Seed a non-empty timeline (revision 1) so studio_timeline_auto_assemble triggers replacement approval
	callMCPJSON(t, cs, "studio_timeline_apply_ops", map[string]any{
		"episode_id":        epID,
		"expected_revision": 0,
		"ops": []map[string]any{
			{"op": "add_clip", "track_id": "v1", "clip": map[string]any{"id": "c1", "file_path": validVid, "source_out_ms": 4000, "mute": true}},
		},
	})

	// 2. studio_qc_review blocks outside paths AND .env inside workspace on both media_path & reference_paths
	for _, badArgs := range []map[string]any{
		{"project_id": projID, "target_id": shotID, "media_path": outsideImg, "skip_vision": true},
		{"project_id": projID, "target_id": shotID, "media_path": dotenvInWs, "skip_vision": true},
		{"project_id": projID, "target_id": shotID, "media_path": validImg, "reference_paths": []string{outsideImg}, "skip_vision": true},
		{"project_id": projID, "target_id": shotID, "media_path": validImg, "reference_paths": []string{dotenvInWs}, "skip_vision": true},
	} {
		res, cerr := cs.CallTool(ctx, &mcp.CallToolParams{Name: "studio_qc_review", Arguments: badArgs})
		if cerr != nil {
			t.Fatal(cerr)
		}
		if !res.IsError {
			t.Fatalf("expected studio_qc_review to block disallowed path in %+v", badArgs)
		}
	}

	// studio_qc_review with skip_vision=true runs without approval even on gated project
	qcTech := callMCPJSON(t, cs, "studio_qc_review", map[string]any{
		"project_id":  projID,
		"episode_id":  epID,
		"target_kind": "keyframe",
		"target_id":   shotID,
		"media_path":  validImg,
		"skip_vision": true,
	})
	if qcTech["verdict"] != "pass" {
		t.Fatalf("expected skip_vision=true studio_qc_review to run directly, got %+v", qcTech)
	}

	// 3. Queue approvals for every gated Studio tool on svc1, then close svc1 and execute all of them
	// via post-restart ResolveApproval (memItem.exec == nil) on a brand-new svc2!
	queuedIDs := make([]string, 0, 9)
	queueGate := func(tool string, args map[string]any) {
		t.Helper()
		out := callMCPJSON(t, cs, tool, args)
		if out["status"] != "needs_approval" {
			t.Fatalf("expected %s to return needs_approval, got %+v", tool, out)
		}
		id, _ := out["approval_id"].(string)
		if id == "" {
			t.Fatalf("expected approval_id for %s", tool)
		}
		queuedIDs = append(queuedIDs, id)
	}

	// Privilege escalation attempt: enabling auto_approve_batches MUST gate!
	queueGate("studio_project_update", map[string]any{
		"id":                   projID,
		"title":                "Gated Film Approved",
		"auto_approve_batches": true,
	})
	// Confirm project is STILL auto_approve_batches=false before approval!
	beforeApproval, err := stSvc.GetProject(ctx, projID)
	if err != nil {
		t.Fatal(err)
	}
	if beforeApproval.Project.AutoApproveBatches {
		t.Fatal("privilege escalation: auto_approve_batches flipped to true before user approval!")
	}

	queueGate("studio_asset_upsert", map[string]any{
		"id":         assetID,
		"project_id": projID,
		"delete":     true,
	})
	queueGate("studio_pricing_upsert", map[string]any{
		"provider_id":    "openai",
		"model_id":       "gpt-image-1",
		"capability":     "image",
		"unit":           "image",
		"unit_price_usd": 0.04,
	})
	queueGate("studio_queue_control", map[string]any{
		"action": "retry",
		"job_id": failedJob.ID,
	})
	queueGate("studio_qc_review", map[string]any{
		"project_id":  projID,
		"episode_id":  epID,
		"target_kind": "keyframe",
		"target_id":   shotID,
		"media_path":  validImg,
		"skip_vision": false,
	})
	expRev := 1
	queueGate("studio_timeline_auto_assemble", map[string]any{
		"episode_id":        epID,
		"expected_revision": expRev,
	})
	queueGate("studio_timeline_render", map[string]any{
		"episode_id": epID,
		"filename":   "master.mp4",
		"overwrite":  true,
	})
	queueGate("studio_timeline_export_nle", map[string]any{
		"episode_id": epID,
		"format":     "fcpxml",
	})
	queueGate("studio_project_delete", map[string]any{
		"id":           projID,
		"delete_files": true,
	})

	// Simulate app restart: close svc1 and instantiate a fresh svc2 with empty in-memory approvalStore!
	_ = cs.Close()
	_ = svc1.Close()

	svc2 := newAgentSvc()
	defer func() { _ = svc2.Close() }()

	// Resolve every persisted approval in order via post-restart switch (memItem.exec == nil)
	for i, appID := range queuedIDs {
		if err := svc2.ResolveApproval(ctx, appID, true); err != nil {
			t.Fatalf("post-restart ResolveApproval[%d] (%s): %v", i, appID, err)
		}
	}

	// Project should be deleted cleanly at the end of the sequence, and folder removed
	if _, err := stSvc.GetProject(ctx, projID); err == nil {
		t.Fatal("expected project to be deleted by post-restart studio_project_delete approval")
	}
	if _, statErr := os.Stat(projRoot); !os.IsNotExist(statErr) {
		t.Fatalf("expected deleted project directory %s to be removed, statErr=%v", projRoot, statErr)
	}
}
