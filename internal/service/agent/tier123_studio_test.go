package agent

import (
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"app/internal/config"
	"app/internal/contracts"
	domainagent "app/internal/domain/agent"
	"app/internal/engine"
	"app/internal/paths"
	studiosvc "app/internal/service/studio"
	"app/internal/service/workflow"
	"app/internal/storage/sqlite"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type stubTextReadiness struct{ ready bool }

func (s stubTextReadiness) IsTextProviderReady(context.Context) bool { return s.ready }

type tier123AgentWorkflow struct {
	*workflow.Service
}

func (w *tier123AgentWorkflow) RunNode(ctx context.Context, nodeType string, input json.RawMessage) (any, error) {
	if nodeType == contracts.NodeTypeVideoContactSheet {
		var in contracts.VideoContactSheetInput
		_ = json.Unmarshal(input, &in)
		outDir := in.OutputDir
		if outDir == "" {
			outDir = filepath.Dir(in.VideoPath.LocalURL)
		}
		if err := os.MkdirAll(outDir, 0o755); err != nil {
			return nil, err
		}
		sheetPath := filepath.Join(outDir, "sheet_agent_test.jpg")
		img := image.NewRGBA(image.Rect(0, 0, 160, 90))
		for y := 0; y < 90; y++ {
			for x := 0; x < 160; x++ {
				img.Set(x, y, color.RGBA{R: 40, G: 110, B: 200, A: 255})
			}
		}
		f, err := os.Create(sheetPath)
		if err != nil {
			return nil, err
		}
		encErr := jpeg.Encode(f, img, &jpeg.Options{Quality: 85})
		_ = f.Close()
		if encErr != nil {
			return nil, encErr
		}
		return contracts.VideoContactSheetOutput{
			Sheets:     []engine.MediaRef{engine.RefFor(sheetPath, engine.MediaImage)},
			SheetPaths: []string{sheetPath},
			Signals: contracts.VideoSignals{
				DurationSec: 4.0,
				Cuts:        []contracts.VideoSignalCut{{TimeSec: 1.5, Score: 0.72}},
			},
			Frames: []contracts.ContactSheetFrameMeta{
				{Index: 0, SheetIndex: 0, TimeSec: 1.5, TimelineMs: 1500, Signals: []string{"CUT"}},
			},
			Summary: "1 contact sheet",
		}, nil
	}
	return w.Service.RunNode(ctx, nodeType, input)
}

func TestTier123MCPTools_WorkflowPlanBatchRefactorInspectAndTemplates(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	ws := filepath.Join(base, "data", "workspace")
	projectsDir := filepath.Join(ws, "projects")
	mediaDir := filepath.Join(base, "data", "media")
	profilesDir := filepath.Join(base, "data", "agent-profiles")
	for _, d := range []string{ws, projectsDir, mediaDir, profilesDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	db, err := sqlite.Open(ctx, sqlite.Options{
		Path:          filepath.Join(base, "data", "studio_tier123.db"),
		BackupDir:     filepath.Join(base, "data", "backups"),
		BackupsToKeep: 1,
		BusyTimeout:   time.Second,
		Logger:        slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	baseWF, err := workflow.New(workflow.Options{
		Paths:   paths.Paths{Data: filepath.Join(base, "data")},
		Logger:  slog.New(slog.DiscardHandler),
		TextGen: &stubStudioMCPTextGen{},
	})
	if err != nil {
		t.Fatal(err)
	}
	wf := &tier123AgentWorkflow{Service: baseWF}

	cfg := config.Default(config.ModeDesktop).Studio
	repo := sqlite.NewStudioRepository(db)
	stSvc := studiosvc.New(studiosvc.Options{
		Repo:          repo,
		Workflow:      wf,
		StudioConfig:  cfg,
		ProjectsDir:   projectsDir,
		TextReadiness: stubTextReadiness{ready: false},
		Logger:        slog.New(slog.DiscardHandler),
	})

	agentRepo := sqlite.NewAgentRepository(db)
	if err := agentRepo.SaveConversation(ctx, domainagent.Conversation{
		ID:         "conv-tier123",
		ProviderID: domainagent.ProviderCodex,
		Title:      "Tier123 Test",
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
	svc.mu.Lock()
	svc.conversationID = "conv-tier123"
	svc.mu.Unlock()

	sT, cT := mcp.NewInMemoryTransports()
	ss, err := svc.buildMCPServer().Connect(ctx, sT, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ss.Close() })
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "tier123-test"}, nil).Connect(ctx, cT, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })

	// 1. workflow_list_templates exposes all 3 canonical DAG templates.
	tplRes := callMCPJSON(t, cs, "workflow_list_templates", map[string]any{})
	tplList, _ := tplRes["templates"].([]any)
	if len(tplList) != 3 {
		t.Fatalf("expected 3 workflow templates, got %d (%+v)", len(tplList), tplRes)
	}

	// 2. Create gated project + assets + check studio_workflow_plan.
	created := callMCPJSON(t, cs, "studio_project_create", map[string]any{
		"title":                "Tier123 Noir",
		"genre":                "narration",
		"auto_approve_batches": false,
	})
	projID := created["project"].(map[string]any)["id"].(string)
	epID := created["active_episode"].(map[string]any)["episode"].(map[string]any)["id"].(string)

	callMCPJSON(t, cs, "studio_save_script", map[string]any{
		"episode_id": epID,
		"raw_script": "@Minh steps into @Cafe under neon rain while @Minh_Alt checks a note.",
	})

	callMCPJSON(t, cs, "studio_asset_upsert", map[string]any{
		"project_id": projID,
		"kind":       "character",
		"name":       "Minh",
		"code_tag":   "@Minh",
		"prompt":     "Detective Minh",
	})
	callMCPJSON(t, cs, "studio_asset_upsert", map[string]any{
		"project_id": projID,
		"kind":       "character",
		"name":       "Minh Duplicate",
		"code_tag":   "@Minh_Alt",
		"prompt":     "Duplicate",
	})
	aMinhDetail, err := stSvc.GetProject(ctx, projID)
	if err != nil {
		t.Fatal(err)
	}
	var minhID, dupID string
	for _, a := range aMinhDetail.Assets {
		switch a.CodeTag {
		case "@Minh":
			minhID = a.ID
		case "@Minh_Alt":
			dupID = a.ID
		}
	}
	if minhID == "" || dupID == "" {
		t.Fatalf("missing assets after upsert: %+v", aMinhDetail.Assets)
	}

	planRes := callMCPJSON(t, cs, "studio_workflow_plan", map[string]any{"episode_id": epID})
	if ready, _ := planRes["text_provider_ready"].(bool); ready {
		t.Fatalf("expected text_provider_ready=false when stub readiness is false")
	}

	// 3. Batch upsert shots (replace_all=false) runs directly without approval.
	batchRes := callMCPJSON(t, cs, "studio_shots_batch_upsert", map[string]any{
		"episode_id": epID,
		"shots": []map[string]any{
			{
				"seq":            1,
				"scene_code":     "S01",
				"title":          "Alley Arrival",
				"image_prompt":   "Rainy neon alley @Minh",
				"duration_sec":   4,
				"ref_asset_tags": []string{"@Minh", "@Minh_Alt"},
			},
			{
				"seq":            2,
				"scene_code":     "S01",
				"title":          "Letter Check",
				"image_prompt":   "Close up @Minh_Alt holding letter",
				"duration_sec":   5,
				"narration_text": "Một lá thư ướt sũng.",
				"ref_asset_tags": []string{"@Minh_Alt"},
			},
		},
	})
	if got := len(batchRes["shots"].([]any)); got != 2 {
		t.Fatalf("expected 2 batch-upserted shots, got %d", got)
	}

	// 4. Batch upsert with replace_all=true on gated project triggers approval.
	gateReplace := callMCPJSON(t, cs, "studio_shots_batch_upsert", map[string]any{
		"episode_id":  epID,
		"replace_all": true,
		"shots": []map[string]any{
			{
				"seq":            1,
				"scene_code":     "S01",
				"title":          "Replaced Shot",
				"image_prompt":   "Wide shot @Minh and @Minh_Alt",
				"duration_sec":   4,
				"ref_asset_tags": []string{"@Minh", "@Minh_Alt"},
			},
		},
	})
	if gateReplace["status"] != "needs_approval" {
		t.Fatalf("expected replace_all=true on gated project to require approval, got %+v", gateReplace)
	}
	reqID, _ := gateReplace["approval_id"].(string)
	if err := svc.ResolveApproval(ctx, reqID, true); err != nil {
		t.Fatalf("ResolveApproval(replace_all): %v", err)
	}
	afterReplace, err := stSvc.GetEpisode(ctx, epID)
	if err != nil || len(afterReplace.Shots) != 1 || afterReplace.Shots[0].Title != "Replaced Shot" {
		t.Fatalf("expected 1 replaced shot Replaced Shot after approval, got %+v (err=%v)", afterReplace.Shots, err)
	}

	// 5. Asset rename cascades @Minh -> @MinhHero across raw script & shots.
	renRes := callMCPJSON(t, cs, "studio_asset_rename", map[string]any{
		"project_id":   projID,
		"asset_id":     minhID,
		"new_name":     "Minh Hero",
		"new_code_tag": "@MinhHero",
	})
	if updatedShots, _ := renRes["updated_shots_count"].(float64); updatedShots < 1 {
		t.Fatalf("expected updated_shots_count >= 1 after rename, got %+v", renRes)
	}

	// 6. Asset merge dry_run=true does not delete source asset, dry_run=false requires approval then merges.
	dryMerge := callMCPJSON(t, cs, "studio_asset_merge", map[string]any{
		"project_id":      projID,
		"source_asset_id": dupID,
		"target_asset_id": minhID,
		"dry_run":         true,
	})
	if dry, _ := dryMerge["dry_run"].(bool); !dry {
		t.Fatalf("expected dry_run=true in response: %+v", dryMerge)
	}
	gateMerge := callMCPJSON(t, cs, "studio_asset_merge", map[string]any{
		"project_id":      projID,
		"source_asset_id": dupID,
		"target_asset_id": minhID,
		"dry_run":         false,
	})
	if gateMerge["status"] != "needs_approval" {
		t.Fatalf("expected merge dry_run=false on gated project to require approval, got %+v", gateMerge)
	}
	if err := svc.ResolveApproval(ctx, gateMerge["approval_id"].(string), true); err != nil {
		t.Fatalf("ResolveApproval(merge): %v", err)
	}
	projDetail, err := stSvc.GetProject(ctx, projID)
	if err != nil {
		t.Fatal(err)
	}
	if len(projDetail.Assets) != 1 || projDetail.Assets[0].CodeTag != "@MinhHero" {
		t.Fatalf("expected 1 surviving asset @MinhHero after merge, got %+v", projDetail.Assets)
	}

	// 7. Attach video file inside episode workspace, then call studio_inspect_shots -> returns TextContent + ImageContent!
	epDetail, err := stSvc.GetEpisode(ctx, epID)
	if err != nil {
		t.Fatal(err)
	}
	sh := epDetail.Shots[0]
	vidPath := filepath.Join(projectsDir, epDetail.Project.Slug, "episodes", epDetail.Episode.Code, "media", "videos", "sh01.mp4")
	if err := os.MkdirAll(filepath.Dir(vidPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(vidPath, []byte("mp4-test"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := stSvc.UpsertShot(ctx, contracts.StudioShotUpsertPayload{
		ID:        sh.ID,
		EpisodeID: epID,
		VideoPath: &vidPath,
	}); err != nil {
		t.Fatal(err)
	}

	inspectRes, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name: "studio_inspect_shots",
		Arguments: map[string]any{
			"episode_id": epID,
		},
	})
	if err != nil {
		t.Fatalf("studio_inspect_shots CallTool: %v", err)
	}
	if inspectRes.IsError || len(inspectRes.Content) < 2 {
		t.Fatalf("expected TextContent + ImageContent from studio_inspect_shots, got isError=%v contentLen=%d (%+v)",
			inspectRes.IsError, len(inspectRes.Content), inspectRes.Content)
	}
	if _, ok := inspectRes.Content[0].(*mcp.TextContent); !ok {
		t.Fatalf("expected Content[0] to be TextContent, got %T", inspectRes.Content[0])
	}
	imgContent, ok := inspectRes.Content[1].(*mcp.ImageContent)
	if !ok || len(imgContent.Data) == 0 || !strings.HasPrefix(imgContent.MIMEType, "image/") {
		t.Fatalf("expected Content[1] to be non-empty ImageContent, got %T len=%d mime=%v", inspectRes.Content[1], len(imgContent.Data), imgContent.MIMEType)
	}

	// 8. studio_queue_wait returns cleanly when episode queue is idle.
	waitRes := callMCPJSON(t, cs, "studio_queue_wait", map[string]any{
		"episode_id":  epID,
		"timeout_sec": 1,
	})
	if allDone, _ := waitRes["all_done"].(bool); !allDone {
		t.Fatalf("expected all_done=true on empty queue, got %+v", waitRes)
	}

	// 9. Embedded subagent manifests carry Tier 1-3 tools.
	for _, slug := range []string{"script-analyst", "visual-reviewer"} {
		sp, _, err := svc.customizations.ResolveSubagent(slug)
		if err != nil {
			t.Fatalf("ResolveSubagent(%s): %v", slug, err)
		}
		joined := strings.Join(sp.Tools, ",")
		if slug == "script-analyst" && !strings.Contains(joined, "studio_shots_batch_upsert") {
			t.Fatalf("script-analyst missing studio_shots_batch_upsert: %v", sp.Tools)
		}
		if slug == "visual-reviewer" && !strings.Contains(joined, "studio_inspect_shots") {
			t.Fatalf("visual-reviewer missing studio_inspect_shots: %v", sp.Tools)
		}
	}

	// 10. Explicit runtime provider/model override fails loudly instead of falling back silently.
	parent := &turn{providerID: domainagent.ProviderCodex}
	spec := domainagent.SubagentInfo{Name: "visual-reviewer"}
	if _, _, _, _, err := svc.resolveSubagentExecutionTargetWithOverrides(ctx, parent, spec, "nonexistent-provider", ""); err == nil {
		t.Fatalf("expected error on invalid explicit runtime provider override")
	}
	_, _, _, _, compatErr := svc.resolveSubagentExecutionTargetWithOverrides(ctx, parent, spec, "", "claude-3-7-sonnet")
	if compatErr == nil {
		t.Fatalf("expected error when explicit runtime model is incompatible with root provider %s", parent.providerID)
	}
}
