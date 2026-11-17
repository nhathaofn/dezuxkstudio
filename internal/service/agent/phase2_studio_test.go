package agent

import (
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"app/internal/config"
	"app/internal/contracts"
	domainagent "app/internal/domain/agent"
	domainmedia "app/internal/domain/mediagen"
	domainprovider "app/internal/domain/provider"
	domainstudio "app/internal/domain/studio"
	"app/internal/paths"
	studiosvc "app/internal/service/studio"
	"app/internal/service/workflow"
	"app/internal/storage/sqlite"
)

type stubPhase2AgentStudio struct {
	stubUntrustedStudioManager
	autoApprove  bool
	enqueueCalls int
	lastApproved bool
}

func (s *stubPhase2AgentStudio) GetEpisode(_ context.Context, id string) (contracts.StudioEpisodeDetailResult, error) {
	return contracts.StudioEpisodeDetailResult{
		Project: contracts.StudioProject{
			ID:                 "proj-p2",
			Title:              "Phase2",
			RootDir:            s.untrustedRoot,
			AutoApproveBatches: s.autoApprove,
		},
		Episode: contracts.StudioEpisode{
			ID:        id,
			ProjectID: "proj-p2",
			Code:      "ep01",
		},
	}, nil
}

func (s *stubPhase2AgentStudio) EnqueueBatch(_ context.Context, p contracts.StudioQueueEnqueuePayload) (contracts.StudioQueueEnqueueResult, error) {
	s.enqueueCalls++
	s.lastApproved = p.Approved
	return contracts.StudioQueueEnqueueResult{Summary: "enqueued 1"}, nil
}
func (s *stubPhase2AgentStudio) ListJobs(context.Context, contracts.StudioQueueListPayload) (contracts.StudioQueueListResult, error) {
	return contracts.StudioQueueListResult{}, nil
}
func (s *stubPhase2AgentStudio) ControlQueue(context.Context, contracts.StudioQueueControlPayload) (contracts.StudioQueueControlResult, error) {
	return contracts.StudioQueueControlResult{Affected: 1}, nil
}
func (s *stubPhase2AgentStudio) GetCostSummary(context.Context, contracts.StudioCostSummaryPayload) (contracts.StudioCostSummaryResult, error) {
	return contracts.StudioCostSummaryResult{Currency: "USD"}, nil
}
func (s *stubPhase2AgentStudio) ListVersions(context.Context, contracts.StudioVersionsListPayload) (contracts.StudioVersionsListResult, error) {
	return contracts.StudioVersionsListResult{}, nil
}
func (s *stubPhase2AgentStudio) SelectVersion(context.Context, contracts.StudioVersionSelectPayload) (contracts.StudioVersionSelectResult, error) {
	return contracts.StudioVersionSelectResult{}, nil
}
func (s *stubPhase2AgentStudio) RunQCReview(context.Context, contracts.StudioQCReviewPayload) (contracts.StudioQCReport, error) {
	return contracts.StudioQCReport{Verdict: "pass", OverallScore: 9.0}, nil
}
func (s *stubPhase2AgentStudio) RunBatchPreflight(context.Context, contracts.StudioBatchPreflightPayload) (contracts.BatchPreflightOutput, error) {
	return contracts.BatchPreflightOutput{Ready: true, EstimatedUSD: 0.2}, nil
}

func TestStudioPhase2MCPToolsAndVisualQCCustomizations(t *testing.T) {
	base := t.TempDir()
	ws := filepath.Join(base, "ws")
	mgr := &stubPhase2AgentStudio{
		stubUntrustedStudioManager: stubUntrustedStudioManager{untrustedRoot: filepath.Join(ws, "projects", "p1")},
		autoApprove:                true,
	}
	svc, err := New(Options{
		Repo:         newFakeRepo(),
		DataDir:      filepath.Join(base, "data"),
		WorkspaceDir: ws,
		Studio:       mgr,
		Logger:       slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = svc.Close() }()

	snap, err := svc.ListCustomizations(context.Background())
	if err != nil {
		t.Fatalf("ListCustomizations: %v", err)
	}
	var foundSkill, foundSub bool
	for _, sk := range snap.Skills {
		if sk.Name == "visual-qc" {
			foundSkill = true
		}
	}
	for _, sb := range snap.Subagents {
		if sb.Name == "visual-reviewer" {
			foundSub = true
		}
	}
	if !foundSkill || !foundSub {
		t.Fatalf("expected built-in visual-qc skill and visual-reviewer subagent, got skill=%v sub=%v", foundSkill, foundSub)
	}

	// Verify MCP exposes studio Phase 2 tools and approval gate passes Approved=true
	ctx := context.Background()
	sT, cT := mcp.NewInMemoryTransports()
	ss, err := svc.buildMCPServer().Connect(ctx, sT, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ss.Close() }()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(ctx, cT, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cs.Close() }()

	tools, err := cs.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, tl := range tools.Tools {
		seen[tl.Name] = true
	}
	for _, want := range []string{
		"studio_batch_preflight", "studio_queue_enqueue", "studio_queue_list",
		"studio_queue_control", "studio_cost_summary", "studio_versions_list",
		"studio_version_select", "studio_qc_review",
	} {
		if !seen[want] {
			t.Fatalf("missing Phase 2 MCP tool %q", want)
		}
	}

	// studio_queue_enqueue executes with AutoApproveBatches=true inside trusted folder and sets Approved=true
	if _, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "studio_queue_enqueue",
		Arguments: map[string]any{"episode_id": "ep1", "stage": "generate_videos"},
	}); err != nil {
		t.Fatalf("CallTool studio_queue_enqueue: %v", err)
	}
	if mgr.enqueueCalls != 1 || !mgr.lastApproved {
		t.Fatalf("expected EnqueueBatch called with Approved=true, got calls=%d approved=%v", mgr.enqueueCalls, mgr.lastApproved)
	}

	// Approval gate test: when AutoApproveBatches=false, studio_queue_enqueue pauses for approval,
	// and ResolveApproval(..., true) executes EnqueueBatch with Approved=true.
	mgr.autoApprove = false
	mgr.enqueueCalls = 0
	mgr.lastApproved = false
	if _, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "studio_queue_enqueue",
		Arguments: map[string]any{"episode_id": "ep1", "stage": "generate_videos"},
	}); err != nil {
		t.Fatalf("CallTool gated studio_queue_enqueue: %v", err)
	}
	if mgr.enqueueCalls != 0 {
		t.Fatalf("expected 0 direct enqueue calls before approval, got %d", mgr.enqueueCalls)
	}
	pending := svc.ListApprovals()
	if len(pending) != 1 || pending[0].Tool != "studio_queue_enqueue" {
		t.Fatalf("expected 1 pending studio_queue_enqueue approval, got %+v", pending)
	}
	if err := svc.ResolveApproval(ctx, pending[0].ID, true); err != nil {
		t.Fatalf("ResolveApproval: %v", err)
	}
	if mgr.enqueueCalls != 1 || !mgr.lastApproved {
		t.Fatalf("expected approved EnqueueBatch call with Approved=true, got calls=%d approved=%v", mgr.enqueueCalls, mgr.lastApproved)
	}
}

type stubPhase2AgentMedia struct {
	outDir string
}

func (s *stubPhase2AgentMedia) GenerateText(_ context.Context, req domainmedia.TextRequest) (domainmedia.TextResult, error) {
	if req.JSONSchemaName == "qc_review" {
		b := json.RawMessage(`{"verdict":"pass","overall_score":9.5,"summary":"Looks sharp","issues":[]}`)
		return domainmedia.TextResult{Text: string(b), ParsedJSON: b, Provider: "stub", Model: "vision-1"}, nil
	}
	return domainmedia.TextResult{Text: "{}", ParsedJSON: json.RawMessage(`{}`)}, nil
}

func (s *stubPhase2AgentMedia) GenerateImage(_ context.Context, _ domainmedia.ImageRequest) (domainmedia.Result, error) {
	p := filepath.Join(s.outDir, "mcp_mask_out.png")
	writeTinyTestPNG(p, 8, 8)
	return domainmedia.Result{Path: p, MimeType: "image/png", Provider: "stub", Model: "img-1"}, nil
}

func (s *stubPhase2AgentMedia) GenerateVideo(context.Context, domainmedia.VideoRequest) (domainmedia.Result, error) {
	p := filepath.Join(s.outDir, "mcp_vid_out.mp4")
	_ = os.WriteFile(p, []byte("MP4"), 0o600)
	return domainmedia.Result{Path: p, MimeType: "video/mp4", Provider: "stub", Model: "vid-1"}, nil
}

func (s *stubPhase2AgentMedia) GenerateTTS(_ context.Context, _ domainmedia.TTSRequest) (domainmedia.Result, error) {
	p := filepath.Join(s.outDir, "mcp_tts_out.mp3")
	_ = os.WriteFile(p, []byte("ID3"), 0o600)
	return domainmedia.Result{Path: p, MimeType: "audio/mpeg", Provider: "stub", Model: "tts-1"}, nil
}

func (s *stubPhase2AgentMedia) GenerateLipSync(_ context.Context, _ domainmedia.LipSyncRequest) (domainmedia.Result, error) {
	p := filepath.Join(s.outDir, "mcp_lipsync_out.mp4")
	_ = os.WriteFile(p, []byte("MP4LS"), 0o600)
	return domainmedia.Result{Path: p, MimeType: "video/mp4", Provider: "stub", Model: "ls-1"}, nil
}

type stubPhase2Preflight struct{}

func (s *stubPhase2Preflight) CheckCapability(_ context.Context, _ domainprovider.Capability, _ string) (string, string, error) {
	return "openai", "sora-2", nil
}
func (s *stubPhase2Preflight) EstimateUnitPriceUSD(_ context.Context, _, _, _, _ string) (float64, bool, error) {
	return 0.08, false, nil
}

func writeTinyTestPNG(path string, w, h int) {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, color.RGBA{R: 120, G: 200, B: 80, A: 255})
		}
	}
	f, _ := os.Create(path)
	_ = png.Encode(f, img)
	_ = f.Close()
}

func TestPhase2NodesAndUntrustedGuardThroughRealWorkflowAndAgentMCP(t *testing.T) {
	base := t.TempDir()
	ws := filepath.Join(base, "data", "workspace")
	mediaDir := filepath.Join(base, "data", "media")
	profilesDir := filepath.Join(base, "data", "agent-profiles")
	outsideDir := t.TempDir()
	for _, d := range []string{ws, mediaDir, profilesDir} {
		_ = os.MkdirAll(d, 0o755)
	}

	gridImg := filepath.Join(mediaDir, "grid.png")
	maskImg := filepath.Join(mediaDir, "mask.png")
	vidClip := filepath.Join(mediaDir, "clip.mp4")
	audTrack := filepath.Join(mediaDir, "line.mp3")
	outsideImg := filepath.Join(outsideDir, "secret.png")
	outsideVid := filepath.Join(outsideDir, "secret.mp4")
	writeTinyTestPNG(gridImg, 32, 32)
	writeTinyTestPNG(maskImg, 32, 32)
	_ = os.WriteFile(vidClip, []byte("MP4SRC"), 0o600)
	_ = os.WriteFile(audTrack, []byte("ID3SRC"), 0o600)
	writeTinyTestPNG(outsideImg, 16, 16)
	_ = os.WriteFile(outsideVid, []byte("OUTSIDE"), 0o600)

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

	mgStub := &stubPhase2AgentMedia{outDir: mediaDir}
	wf, err := workflow.New(workflow.Options{
		Paths:           paths.Paths{Data: filepath.Join(base, "data")},
		Logger:          slog.New(slog.DiscardHandler),
		MediaGen:        mgStub,
		TextGen:         mgStub,
		ProviderChecker: &stubPhase2Preflight{},
		CostEstimator:   &stubPhase2Preflight{},
		WorkspaceTreeFunc: func() paths.WorkspaceTree {
			return paths.WorkspaceTree{
				Root:         filepath.Join(base, "data"),
				WorkspaceDir: ws,
				ProjectsDir:  filepath.Join(ws, "projects"),
				MediaDir:     mediaDir,
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	stSvc := studiosvc.New(studiosvc.Options{
		Repo:         sqlite.NewStudioRepository(db),
		Workflow:     wf,
		StudioConfig: config.Default(config.ModeDesktop).Studio,
		ProjectsDir:  filepath.Join(ws, "projects"),
		Logger:       slog.New(slog.DiscardHandler),
	})

	ctx := context.Background()
	agentRepo := sqlite.NewAgentRepository(db)
	if err := agentRepo.SaveConversation(ctx, domainagent.Conversation{
		ID:         "test-conv-phase2",
		ProviderID: domainagent.ProviderCodex,
		Title:      "Phase 2 Verification",
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
	svc.conversationID = "test-conv-phase2"
	svc.mu.Unlock()

	sT, cT := mcp.NewInMemoryTransports()
	if _, err := svc.buildMCPServer().Connect(ctx, sT, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(ctx, cT, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cs.Close() }()

	// 1. All 8 new Phase 2 nodes must appear in workflow_list_specs
	specsRes := callMCPJSON(t, cs, "workflow_list_specs", map[string]any{})
	rawSpecs, _ := json.Marshal(specsRes["specs"])
	specText := string(rawSpecs)
	for _, wantNode := range []string{
		contracts.NodeTypeBatchPreflight,
		contracts.NodeTypeGridSplit,
		contracts.NodeTypeMaskEdit,
		contracts.NodeTypeExtractFrames,
		contracts.NodeTypeTTSDubMux,
		contracts.NodeTypeLipSyncGenerate,
		contracts.NodeTypeQCReview,
		contracts.NodeTypeVideoWMRemove,
	} {
		if !strings.Contains(specText, wantNode) {
			t.Fatalf("missing Phase 2 node %q in workflow_list_specs: %s", wantNode, specText)
		}
	}

	// 2. Trusted calls to Phase 2 nodes through Agent MCP workflow_run_node succeed with Spec Params
	gridRes, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name: "workflow_run_node",
		Arguments: map[string]any{
			"type": contracts.NodeTypeGridSplit,
			"input": map[string]any{
				"image_path": gridImg,
				"rows":       2,
				"cols":       2,
				"output_dir": mediaDir,
			},
		},
	})
	if err != nil || gridRes.IsError {
		t.Fatalf("workflow_run_node(tool.grid_split) failed: err=%v res=%+v", err, gridRes)
	}

	maskRes, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name: "workflow_run_node",
		Arguments: map[string]any{
			"type": contracts.NodeTypeMaskEdit,
			"input": map[string]any{
				"image_path":   gridImg,
				"mask_path":    maskImg,
				"prompt":       "glowing visor",
				"aspect_ratio": "16:9",
				"output_dir":   mediaDir,
			},
		},
	})
	if err != nil || maskRes.IsError {
		t.Fatalf("workflow_run_node(tool.mask_edit) failed: err=%v res=%+v", err, maskRes)
	}

	qcRes, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name: "workflow_run_node",
		Arguments: map[string]any{
			"type": contracts.NodeTypeQCReview,
			"input": map[string]any{
				"media_path":      gridImg,
				"reference_paths": []string{maskImg},
				"prompt_expected": "clean portrait",
				"target_kind":     "shot_keyframe",
			},
		},
	})
	if err != nil || qcRes.IsError {
		t.Fatalf("workflow_run_node(tool.qc_review) failed: err=%v res=%+v", err, qcRes)
	}

	preRes, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name: "workflow_run_node",
		Arguments: map[string]any{
			"type": contracts.NodeTypeBatchPreflight,
			"input": map[string]any{
				"capability": "video",
				"item_count": 3,
			},
		},
	})
	if err != nil || preRes.IsError {
		t.Fatalf("workflow_run_node(tool.batch_preflight) failed: err=%v res=%+v", err, preRes)
	}

	// Verify lip-sync node validates both PortVideo and PortAudio through Agent MCP
	lsRes, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name: "workflow_run_node",
		Arguments: map[string]any{
			"type": contracts.NodeTypeLipSyncGenerate,
			"input": map[string]any{
				"video_path": vidClip,
				"audio_path": audTrack,
				"face_index": 0,
				"output_dir": mediaDir,
			},
		},
	})
	if err != nil || lsRes.IsError {
		t.Fatalf("workflow_run_node(tool.lipsync_generate) failed: err=%v res=%+v", err, lsRes)
	}

	// 3. Untrusted outside-workspace path rejected cleanly before node execution
	untrustedRes, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name: "workflow_run_node",
		Arguments: map[string]any{
			"type": contracts.NodeTypeQCReview,
			"input": map[string]any{
				"media_path": outsideImg,
			},
		},
	})
	if err == nil && !untrustedRes.IsError {
		t.Fatal("expected workflow_run_node(tool.qc_review) to deny outside-workspace file")
	}

	// 4. studio_queue_enqueue forces interactive approval even if auto_approve_batches=true when a shot points outside workspace
	projCreated := callMCPJSON(t, cs, "studio_project_create", map[string]any{
		"title":                "Trust Guard Check",
		"auto_approve_batches": true,
	})
	epObj := projCreated["active_episode"].(map[string]any)["episode"].(map[string]any)
	epID := epObj["id"].(string)
	_, err = sqlite.NewStudioRepository(db).UpsertShot(ctx, domainstudio.Shot{
		EpisodeID: epID,
		Seq:       1,
		Title:     "Untrusted Shot",
		VideoPath: outsideVid,
	})
	if err != nil {
		t.Fatal(err)
	}
	enqOut := callMCPJSON(t, cs, "studio_queue_enqueue", map[string]any{
		"episode_id": epID,
		"stage":      "lipsync",
	})
	if enqOut["status"] != "needs_approval" {
		t.Fatalf("expected studio_queue_enqueue with untrusted shot file to require needs_approval despite auto_approve_batches=true, got: %+v", enqOut)
	}
}
