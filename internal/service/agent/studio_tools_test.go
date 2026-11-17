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
