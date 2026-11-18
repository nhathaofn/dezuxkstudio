package agent

import (
	"bytes"
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
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"app/internal/contracts"
	studiosvc "app/internal/service/studio"
	"app/internal/service/workflow"
	"app/internal/storage/sqlite"
	studionodes "app/internal/tools/studio"
)

func TestStudioPhase3MCPTimelineToolsAndEditTimelineSkill(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	ws := filepath.Join(base, "ws")
	projectsDir := filepath.Join(ws, "projects")
	if err := os.MkdirAll(projectsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	db, err := sqlite.Open(ctx, sqlite.Options{
		Path:          filepath.Join(base, "studio_p3_agent.db"),
		BackupDir:     filepath.Join(base, "backups"),
		BackupsToKeep: 1,
		BusyTimeout:   time.Second,
		Logger:        slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	studioSvc := studiosvc.New(studiosvc.Options{
		Repo:        sqlite.NewStudioRepository(db),
		ProjectsDir: projectsDir,
		Logger:      slog.New(slog.DiscardHandler),
	})

	created, err := studioSvc.CreateProject(ctx, contracts.StudioProjectCreatePayload{
		Title:       "Agent Timeline Film",
		AspectRatio: "16:9",
	})
	if err != nil {
		t.Fatal(err)
	}
	epID := created.ActiveEpisode.Episode.ID
	epRoot := filepath.Join(projectsDir, created.Project.Slug, "episodes", created.ActiveEpisode.Episode.Code)
	clipPath := filepath.Join(epRoot, "media", "videos", "shot1.mp4")
	if err := os.MkdirAll(filepath.Dir(clipPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(clipPath, []byte("MP4BYTES"), 0o600); err != nil {
		t.Fatal(err)
	}
	untrustedPath := filepath.Join(base, "outside", "secret.mp4")
	_ = os.MkdirAll(filepath.Dir(untrustedPath), 0o755)
	_ = os.WriteFile(untrustedPath, []byte("OUTSIDE"), 0o600)

	svc, err := New(Options{
		Repo:         newFakeRepo(),
		DataDir:      filepath.Join(base, "data"),
		WorkspaceDir: ws,
		Studio:       studioSvc,
		Logger:       slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = svc.Close() }()

	// 1. Verify edit-timeline skill is synced and enabled in starter customizations
	snap, err := svc.ListCustomizations(ctx)
	if err != nil {
		t.Fatalf("ListCustomizations: %v", err)
	}
	foundSkill := false
	for _, sk := range snap.Skills {
		if sk.Name == "edit-timeline" && sk.Enabled {
			foundSkill = true
			break
		}
	}
	if !foundSkill {
		t.Fatalf("expected built-in enabled edit-timeline skill in snapshot")
	}

	// 2. Connect MCP client over in-memory transport
	sT, cT := mcp.NewInMemoryTransports()
	ss, err := svc.buildMCPServer().Connect(ctx, sT, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ss.Close() }()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "p3-test"}, nil).Connect(ctx, cT, nil)
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
		"studio_timeline_get",
		"studio_timeline_apply_ops",
		"studio_timeline_undo",
		"studio_timeline_redo",
	} {
		if !seen[want] {
			t.Fatalf("missing Phase 3 MCP tool %q", want)
		}
	}

	// 3. studio_timeline_get -> revision 0
	resGet, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "studio_timeline_get",
		Arguments: map[string]any{"episode_id": epID},
	})
	if err != nil || resGet.IsError {
		t.Fatalf("studio_timeline_get failed: err=%v res=%+v", err, resGet)
	}
	var tl0 contracts.StudioTimelineResult
	if err := json.Unmarshal([]byte(resGet.Content[0].(*mcp.TextContent).Text), &tl0); err != nil {
		t.Fatal(err)
	}
	if tl0.Revision != 0 {
		t.Fatalf("expected revision 0, got %d", tl0.Revision)
	}

	// 4. Reject untrusted outside file path via policy.ValidateRead before applying
	resBadPath, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name: "studio_timeline_apply_ops",
		Arguments: map[string]any{
			"episode_id":        epID,
			"expected_revision": 0,
			"ops": []map[string]any{{
				"op":       "add_clip",
				"track_id": "v1",
				"clip": map[string]any{
					"id":        "bad-clip",
					"file_path": untrustedPath,
				},
			}},
		},
	})
	if err != nil {
		t.Fatalf("CallTool failed unexpectedly: %v", err)
	}
	if !resBadPath.IsError {
		t.Fatalf("expected error response for untrusted clip file_path, got %+v", resBadPath)
	}

	// 5. Valid apply ops -> revision 1
	resApply, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name: "studio_timeline_apply_ops",
		Arguments: map[string]any{
			"episode_id":        epID,
			"expected_revision": 0,
			"summary":           "add shot 1 and Vietnamese cue",
			"ops": []map[string]any{
				{
					"op":       "add_clip",
					"track_id": "v1",
					"clip": map[string]any{
						"id":                "c1",
						"label":             "Cảnh 1",
						"file_path":         clipPath,
						"timeline_start_ms": 0,
						"source_in_ms":      0,
						"source_out_ms":     4000,
					},
				},
				{
					"op": "add_subtitle",
					"subtitle": map[string]any{
						"id":       "s1",
						"start_ms": 0,
						"end_ms":   3500,
						"text":     "Đêm Sài Gòn yên tĩnh lạ thường.",
					},
				},
			},
		},
	})
	if err != nil || resApply.IsError {
		errTxt := ""
		if resApply != nil && len(resApply.Content) > 0 {
			if tc, ok := resApply.Content[0].(*mcp.TextContent); ok {
				errTxt = tc.Text
			}
		}
		t.Fatalf("studio_timeline_apply_ops failed: err=%v text=%q", err, errTxt)
	}
	var tl1 contracts.StudioTimelineResult
	if err := json.Unmarshal([]byte(resApply.Content[0].(*mcp.TextContent).Text), &tl1); err != nil {
		t.Fatal(err)
	}
	if tl1.Revision != 1 || len(tl1.Timeline.Tracks[0].Clips) != 1 || len(tl1.Timeline.Subtitles) != 1 {
		t.Fatalf("unexpected rev 1 timeline: %+v", tl1)
	}

	// 6. Second edit (split_clip) -> revision 2
	resSplit, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name: "studio_timeline_apply_ops",
		Arguments: map[string]any{
			"episode_id":        epID,
			"expected_revision": 1,
			"summary":           "split clip c1",
			"ops": []map[string]any{{
				"op":          "split_clip",
				"clip_id":     "c1",
				"new_clip_id": "c2",
				"split_at_ms": 2000,
			}},
		},
	})
	if err != nil || resSplit.IsError {
		t.Fatalf("studio_timeline_apply_ops split failed: err=%v res=%+v", err, resSplit)
	}
	var tl2 contracts.StudioTimelineResult
	_ = json.Unmarshal([]byte(resSplit.Content[0].(*mcp.TextContent).Text), &tl2)
	if tl2.Revision != 2 || len(tl2.Timeline.Tracks[0].Clips) != 2 {
		t.Fatalf("expected rev 2 with 2 clips, got %+v", tl2)
	}

	// 7. Stale expected_revision triggers conflict error
	resConflict, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name: "studio_timeline_apply_ops",
		Arguments: map[string]any{
			"episode_id":        epID,
			"expected_revision": 0,
			"ops": []map[string]any{{
				"op":     "add_marker",
				"marker": map[string]any{"id": "m1", "time_ms": 1000, "label": "Beat"},
			}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !resConflict.IsError || !strings.Contains(strings.ToLower(resConflict.Content[0].(*mcp.TextContent).Text), "conflict") {
		t.Fatalf("expected conflict error on stale expected_revision, got %+v", resConflict)
	}

	// 8. Undo rev 2 -> rev 1, Redo rev 1 -> rev 2
	resUndo, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "studio_timeline_undo",
		Arguments: map[string]any{"episode_id": epID, "expected_revision": 2},
	})
	if err != nil || resUndo.IsError {
		t.Fatalf("studio_timeline_undo failed: err=%v res=%+v", err, resUndo)
	}
	var tlUndo contracts.StudioTimelineResult
	_ = json.Unmarshal([]byte(resUndo.Content[0].(*mcp.TextContent).Text), &tlUndo)
	if tlUndo.Revision != 1 || !tlUndo.CanRedo || len(tlUndo.Timeline.Tracks[0].Clips) != 1 {
		t.Fatalf("expected revision 1 with CanRedo=true and 1 clip, got %+v", tlUndo)
	}

	resRedo, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "studio_timeline_redo",
		Arguments: map[string]any{"episode_id": epID, "expected_revision": 1},
	})
	if err != nil || resRedo.IsError {
		t.Fatalf("studio_timeline_redo failed: err=%v res=%+v", err, resRedo)
	}
	var tlRedo contracts.StudioTimelineResult
	_ = json.Unmarshal([]byte(resRedo.Content[0].(*mcp.TextContent).Text), &tlRedo)
	if tlRedo.Revision != 2 || len(tlRedo.Timeline.Tracks[0].Clips) != 2 {
		t.Fatalf("expected revision 2 after redo, got %+v", tlRedo)
	}
}

func TestPhase3AgentWorkflowRunNode_RealSpecsAndFileGuard(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	ws := filepath.Join(base, "ws")
	mediaDir := filepath.Join(base, "media")
	_ = os.MkdirAll(ws, 0o755)
	_ = os.MkdirAll(mediaDir, 0o755)

	shotVid := filepath.Join(ws, "projects", "film", "ep01", "media", "videos", "shot1.mp4")
	outsideBGM := filepath.Join(base, "outside", "bgm.mp3")
	exportsDir := filepath.Join(ws, "projects", "film", "ep01", "exports")
	_ = os.MkdirAll(filepath.Dir(shotVid), 0o755)
	_ = os.MkdirAll(filepath.Dir(outsideBGM), 0o755)
	_ = os.MkdirAll(exportsDir, 0o755)
	_ = os.WriteFile(shotVid, []byte("MP4DATA"), 0o600)
	_ = os.WriteFile(outsideBGM, []byte("MP3OUTSIDE"), 0o600)

	wf, err := workflow.New(workflow.Options{
		Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatal(err)
	}

	svc, err := New(Options{
		Repo:         newFakeRepo(),
		DataDir:      filepath.Join(base, "data"),
		WorkspaceDir: ws,
		MediaDir:     mediaDir,
		Workflow:     wf,
		Logger:       slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = svc.Close() }()

	sT, cT := mcp.NewInMemoryTransports()
	ss, err := svc.buildMCPServer().Connect(ctx, sT, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ss.Close() }()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "p3-wf-test"}, nil).Connect(ctx, cT, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cs.Close() }()

	// 1. All 4 Phase 3 nodes appear in workflow_list_specs
	specsRes, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "workflow_list_specs"})
	if err != nil || specsRes.IsError {
		t.Fatalf("workflow_list_specs failed: err=%v res=%+v", err, specsRes)
	}
	specText := specsRes.Content[0].(*mcp.TextContent).Text
	for _, want := range []string{
		contracts.NodeTypeTimelineAutoAssem,
		contracts.NodeTypeMediaPeaksThumbs,
		contracts.NodeTypeRenderTimeline,
		contracts.NodeTypeExportNLEDraft,
	} {
		if !strings.Contains(specText, want) {
			t.Fatalf("missing Phase 3 node %q in workflow_list_specs", want)
		}
	}

	// 2. Trusted workflow_run_node(tool.timeline_auto_assemble) succeeds with Spec param defaults
	assemRes, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name: "workflow_run_node",
		Arguments: map[string]any{
			"type": contracts.NodeTypeTimelineAutoAssem,
			"input": map[string]any{
				"shots_json": `[{"id":"s1","seq":1,"title":"Shot 1","duration_sec":4,"video_path":"` + filepath.ToSlash(shotVid) + `","narration_text":"Xin chào Việt Nam"}]`,
			},
		},
	})
	if err != nil || assemRes.IsError {
		t.Fatalf("workflow_run_node(tool.timeline_auto_assemble) failed: err=%v res=%+v", err, assemRes)
	}
	var assemWrap map[string]any
	_ = json.Unmarshal([]byte(assemRes.Content[0].(*mcp.TextContent).Text), &assemWrap)
	outObj, _ := assemWrap["result"].(map[string]any)
	tlJSON, _ := outObj["timeline_json"].(string)
	if !strings.Contains(tlJSON, "Xin chào Việt Nam") {
		t.Fatalf("expected assembled timeline_json with subtitle cue, got %+v", assemWrap)
	}

	// 3. Untrusted outside BGM file rejected by AccessPolicy CheckTrustedFile
	badBGMRes, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name: "workflow_run_node",
		Arguments: map[string]any{
			"type": contracts.NodeTypeTimelineAutoAssem,
			"input": map[string]any{
				"shots_json": `[{"id":"s1","seq":1,"title":"Shot 1","duration_sec":4,"video_path":"` + filepath.ToSlash(shotVid) + `"}]`,
				"bgm_path":   outsideBGM,
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !badBGMRes.IsError {
		t.Fatalf("expected untrusted bgm_path to be denied by FileGuard, got %+v", badBGMRes)
	}

	// 4. Trusted workflow_run_node(tool.export_nle_draft) creates CapCut draft zip under exportsDir
	expRes, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name: "workflow_run_node",
		Arguments: map[string]any{
			"type": contracts.NodeTypeExportNLEDraft,
			"input": map[string]any{
				"timeline_json": tlJSON,
				"format":        "capcut",
				"output_dir":    exportsDir,
				"project_name":  "Review Test",
			},
		},
	})
	if err != nil || expRes.IsError {
		t.Fatalf("workflow_run_node(tool.export_nle_draft) failed: err=%v res=%+v", err, expRes)
	}
	var expWrap map[string]any
	_ = json.Unmarshal([]byte(expRes.Content[0].(*mcp.TextContent).Text), &expWrap)
	expOut, _ := expWrap["result"].(map[string]any)
	expPath, _ := expOut["export_path"].(string)
	if !strings.HasSuffix(expPath, ".zip") {
		t.Fatalf("expected .zip NLE draft path, got %+v", expWrap)
	}

	// 5. Untrusted clip inside timeline_json rejected by tool.export_nle_draft FileGuard
	var badTLObj map[string]any
	if err := json.Unmarshal([]byte(tlJSON), &badTLObj); err != nil {
		t.Fatal(err)
	}
	tracks := badTLObj["tracks"].([]any)
	clips := tracks[0].(map[string]any)["clips"].([]any)
	clips[0].(map[string]any)["file_path"] = outsideBGM
	badTLBytes, _ := json.Marshal(badTLObj)
	badExpRes, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name: "workflow_run_node",
		Arguments: map[string]any{
			"type": contracts.NodeTypeExportNLEDraft,
			"input": map[string]any{
				"timeline_json": string(badTLBytes),
				"format":        "capcut",
				"output_dir":    exportsDir,
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !badExpRes.IsError {
		t.Fatalf("expected untrusted media inside timeline_json to be denied by FileGuard, got %+v", badExpRes)
	}
}

type p3ParityWorkflowStub struct {
	maskCalls int
}

func (w *p3ParityWorkflowStub) RunNode(_ context.Context, nodeType string, raw json.RawMessage) (any, error) {
	switch nodeType {
	case studionodes.NodeTypeGridSplit:
		var in contracts.GridSplitInput
		_ = json.Unmarshal(raw, &in)
		_ = os.MkdirAll(in.OutputDir, 0o755)
		p := filepath.Join(in.OutputDir, "panel_0_0.png")
		_ = os.WriteFile(p, []byte("PNGPANEL"), 0o600)
		return contracts.GridSplitOutput{
			Panels: []contracts.GridPanel{{Index: 0, Row: 0, Col: 0, Path: p, Width: 64, Height: 64}},
		}, nil
	case studionodes.NodeTypeMaskEdit:
		w.maskCalls++
		var in contracts.MaskEditInput
		_ = json.Unmarshal(raw, &in)
		_ = os.MkdirAll(in.OutputDir, 0o755)
		p := filepath.Join(in.OutputDir, "masked_out.png")
		_ = os.WriteFile(p, []byte("PNGMASKED"), 0o600)
		return contracts.MaskEditOutput{Path: p, MimeType: "image/png", Provider: "openai", Model: "gpt-image-1"}, nil
	case studionodes.NodeTypeExtractFrames:
		var in contracts.ExtractFramesInput
		_ = json.Unmarshal(raw, &in)
		_ = os.MkdirAll(in.OutputDir, 0o755)
		p := filepath.Join(in.OutputDir, "last_frame.png")
		_ = os.WriteFile(p, []byte("PNGLAST"), 0o600)
		return contracts.ExtractFramesOutput{
			Frames:        []contracts.ExtractedFrame{{Path: p, TimestampSec: 4.9, Width: 1280, Height: 720}},
			LastFramePath: p,
			OutputDir:     in.OutputDir,
		}, nil
	case studionodes.NodeTypeTimelineAutoAssem:
		var in contracts.TimelineAutoAssembleInput
		_ = json.Unmarshal(raw, &in)
		tl := contracts.StudioEditTimeline{
			Version: 1,
			FPS:     24,
			Width:   1280,
			Height:  720,
			Tracks: []contracts.StudioTimelineTrack{{
				ID:     "v1",
				Kind:   "video",
				Volume: 1,
				Clips: []contracts.StudioTimelineClip{{
					ID:          "clip-1",
					FilePath:    filepath.Join(in.ProjectDir, "media", "videos", "shot1.mp4"),
					SourceOutMs: 4000,
					Speed:       1,
					Volume:      1,
				}},
			}},
		}
		tlBytes, _ := json.Marshal(tl)
		return contracts.TimelineAutoAssembleOutput{TimelineJSON: string(tlBytes), ClipCount: 1, DurationMs: 4000}, nil
	case studionodes.NodeTypeMediaPeaksThumbs:
		var in contracts.MediaPeaksThumbsInput
		_ = json.Unmarshal(raw, &in)
		_ = os.MkdirAll(in.OutputDir, 0o755)
		sp := filepath.Join(in.OutputDir, "sprite.jpg")
		_ = os.WriteFile(sp, []byte("SPRITE"), 0o600)
		return contracts.MediaPeaksThumbsOutput{SpriteSheetPath: sp, Peaks: []float64{0.2, 0.8}, DurationMs: 4000}, nil
	case studionodes.NodeTypeRenderTimeline:
		var in contracts.RenderTimelineInput
		_ = json.Unmarshal(raw, &in)
		_ = os.MkdirAll(in.OutputDir, 0o755)
		mp4 := filepath.Join(in.OutputDir, "final.mp4")
		_ = os.WriteFile(mp4, []byte("FINALMP4"), 0o600)
		return contracts.RenderTimelineOutput{OutputPath: mp4, DurationSec: 4.0}, nil
	case studionodes.NodeTypeExportNLEDraft:
		var in contracts.ExportNLEDraftInput
		_ = json.Unmarshal(raw, &in)
		_ = os.MkdirAll(in.OutputDir, 0o755)
		z := filepath.Join(in.OutputDir, "draft.zip")
		_ = os.WriteFile(z, []byte("PKZIP"), 0o600)
		return contracts.ExportNLEDraftOutput{Format: in.Format, ExportPath: z, FileCount: 2}, nil
	default:
		return map[string]any{}, nil
	}
}

func TestStudioExtendedMCPToolsParityPolicyAndMaskApproval(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	ws := filepath.Join(base, "ws")
	projectsDir := filepath.Join(ws, "projects")
	if err := os.MkdirAll(projectsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	db, err := sqlite.Open(ctx, sqlite.Options{
		Path:          filepath.Join(base, "studio_parity.db"),
		BackupDir:     filepath.Join(base, "backups"),
		BackupsToKeep: 1,
		BusyTimeout:   time.Second,
		Logger:        slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	wfStub := &p3ParityWorkflowStub{}
	studioSvc := studiosvc.New(studiosvc.Options{
		Repo:        sqlite.NewStudioRepository(db),
		Workflow:    wfStub,
		ProjectsDir: projectsDir,
		Logger:      slog.New(slog.DiscardHandler),
	})

	created, err := studioSvc.CreateProject(ctx, contracts.StudioProjectCreatePayload{
		Title:              "Parity Film",
		AspectRatio:        "16:9",
		AutoApproveBatches: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	epID := created.ActiveEpisode.Episode.ID
	epRoot := filepath.Join(projectsDir, created.Project.Slug, "episodes", created.ActiveEpisode.Episode.Code)
	gridImg := filepath.Join(epRoot, "media", "grid.png")
	shotVid := filepath.Join(epRoot, "media", "videos", "shot1.mp4")
	outsideFile := filepath.Join(base, "outside", "leak.png")
	_ = os.MkdirAll(filepath.Dir(gridImg), 0o755)
	_ = os.MkdirAll(filepath.Dir(shotVid), 0o755)
	_ = os.MkdirAll(filepath.Dir(outsideFile), 0o755)

	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	img.SetRGBA(0, 0, color.RGBA{R: 255, G: 255, B: 255, A: 255})
	var pngBuf bytes.Buffer
	_ = png.Encode(&pngBuf, img)
	_ = os.WriteFile(gridImg, pngBuf.Bytes(), 0o600)
	_ = os.WriteFile(outsideFile, pngBuf.Bytes(), 0o600)
	_ = os.WriteFile(shotVid, []byte("MP4BYTES"), 0o600)
	maskB64 := "data:image/png;base64," + base64.StdEncoding.EncodeToString(pngBuf.Bytes())

	shot1, err := studioSvc.UpsertShot(ctx, contracts.StudioShotUpsertPayload{
		EpisodeID: epID, SceneCode: "S01", Title: "Shot 1",
		KeyframePath: &gridImg, VideoPath: &shotVid,
	})
	if err != nil {
		t.Fatal(err)
	}
	shot2, err := studioSvc.UpsertShot(ctx, contracts.StudioShotUpsertPayload{
		EpisodeID: epID, SceneCode: "S01", Title: "Shot 2",
	})
	if err != nil {
		t.Fatal(err)
	}

	svc, err := New(Options{
		Repo:         newFakeRepo(),
		DataDir:      filepath.Join(base, "data"),
		WorkspaceDir: ws,
		Studio:       studioSvc,
		Logger:       slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = svc.Close() }()

	sT, cT := mcp.NewInMemoryTransports()
	ss, err := svc.buildMCPServer().Connect(ctx, sT, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ss.Close() }()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "parity-test"}, nil).Connect(ctx, cT, nil)
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
		"studio_project_get", "studio_project_delete",
		"studio_project_update", "studio_shot_reorder", "studio_pricing_upsert",
		"studio_grid_split", "studio_mask_edit", "studio_extract_last_frame",
		"studio_timeline_auto_assemble", "studio_media_peaks_thumbs",
		"studio_timeline_render", "studio_timeline_export_nle",
	} {
		if !seen[want] {
			t.Fatalf("missing parity MCP tool %q", want)
		}
	}

	// 1. Policy blocks outside paths on studio_grid_split & studio_mask_edit & studio_timeline_auto_assemble
	for _, call := range []struct {
		name string
		args map[string]any
	}{
		{"studio_grid_split", map[string]any{"project_id": created.Project.ID, "image_path": outsideFile, "rows": 2, "cols": 2}},
		{"studio_mask_edit", map[string]any{"project_id": created.Project.ID, "image_path": outsideFile, "mask_data_base64": maskB64, "prompt": "fix"}},
		{"studio_timeline_auto_assemble", map[string]any{"episode_id": epID, "bgm_path": outsideFile}},
	} {
		res, cerr := cs.CallTool(ctx, &mcp.CallToolParams{Name: call.name, Arguments: call.args})
		if cerr != nil {
			t.Fatal(cerr)
		}
		if !res.IsError {
			t.Fatalf("%s should reject outside workspace path", call.name)
		}
	}

	// 2. studio_mask_edit obeys AutoApproveBatches=false gate -> creates approval -> ResolveApproval executes ApplyMaskEdit
	maskCallRes, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name: "studio_mask_edit",
		Arguments: map[string]any{
			"project_id":       created.Project.ID,
			"episode_id":       epID,
			"target_kind":      "keyframe",
			"target_id":        shot1.Shot.ID,
			"image_path":       gridImg,
			"mask_data_base64": maskB64,
			"prompt":           "replace raindrop",
		},
	})
	if err != nil || maskCallRes.IsError {
		t.Fatalf("studio_mask_edit approval enqueue failed: err=%v res=%+v", err, maskCallRes)
	}
	if wfStub.maskCalls != 0 {
		t.Fatalf("expected 0 mask node executions before approval, got %d", wfStub.maskCalls)
	}
	pending := svc.ListApprovals()
	if len(pending) != 1 || pending[0].Tool != "studio_mask_edit" {
		t.Fatalf("expected pending studio_mask_edit approval, got %+v", pending)
	}
	if err := svc.ResolveApproval(ctx, pending[0].ID, true); err != nil {
		t.Fatalf("ResolveApproval(studio_mask_edit): %v", err)
	}
	if wfStub.maskCalls != 1 {
		t.Fatalf("expected 1 mask execution after approval, got %d", wfStub.maskCalls)
	}

	// 3. studio_project_get reads detail; studio_project_update(auto_approve_batches=true) & studio_pricing_upsert gate for approval
	gotProj := callMCPJSON(t, cs, "studio_project_get", map[string]any{"id": created.Project.ID})
	if gotProj["project"].(map[string]any)["id"] != created.Project.ID {
		t.Fatalf("unexpected studio_project_get result: %+v", gotProj)
	}

	updOut := callMCPJSON(t, cs, "studio_project_update", map[string]any{
		"id":                   created.Project.ID,
		"title":                "Parity Film v2",
		"auto_approve_batches": true,
	})
	if updOut["status"] != "needs_approval" {
		t.Fatalf("expected studio_project_update(auto_approve_batches=true) to require approval, got %+v", updOut)
	}
	if err := svc.ResolveApproval(ctx, updOut["approval_id"].(string), true); err != nil {
		t.Fatalf("ResolveApproval(studio_project_update): %v", err)
	}

	priceOut := callMCPJSON(t, cs, "studio_pricing_upsert", map[string]any{
		"provider_id":    "openai",
		"model_id":       "sora-2",
		"capability":     "video",
		"unit":           "second",
		"unit_price_usd": 0.12,
	})
	if priceOut["status"] != "needs_approval" {
		t.Fatalf("expected studio_pricing_upsert to require approval, got %+v", priceOut)
	}
	if err := svc.ResolveApproval(ctx, priceOut["approval_id"].(string), true); err != nil {
		t.Fatalf("ResolveApproval(studio_pricing_upsert): %v", err)
	}

	// 4. Trusted parity tools execute end-to-end once project is approved inside trusted workspace
	for _, call := range []struct {
		name string
		args map[string]any
	}{
		{"studio_shot_reorder", map[string]any{"episode_id": epID, "shot_ids": []string{shot2.Shot.ID, shot1.Shot.ID}}},
		{"studio_grid_split", map[string]any{"project_id": created.Project.ID, "episode_id": epID, "target_kind": "grid_panel", "target_id": shot1.Shot.ID, "image_path": gridImg, "rows": 2, "cols": 2}},
		{"studio_extract_last_frame", map[string]any{"episode_id": epID, "source_shot_id": shot1.Shot.ID, "target_shot_id": shot2.Shot.ID}},
		{"studio_timeline_auto_assemble", map[string]any{"episode_id": epID}},
		{"studio_media_peaks_thumbs", map[string]any{"episode_id": epID, "media_path": shotVid, "clip_id": "clip-1"}},
		{"studio_timeline_render", map[string]any{"episode_id": epID, "filename": "out.mp4"}},
		{"studio_timeline_export_nle", map[string]any{"episode_id": epID, "format": "capcut"}},
	} {
		out := callMCPJSON(t, cs, call.name, call.args)
		if out["status"] == "needs_approval" {
			t.Fatalf("%s unexpectedly gated on auto-approved trusted project: %+v", call.name, out)
		}
	}

	// 5. studio_project_delete always requires approval even on auto-approved projects
	delOut := callMCPJSON(t, cs, "studio_project_delete", map[string]any{
		"id":           created.Project.ID,
		"delete_files": true,
	})
	if delOut["status"] != "needs_approval" {
		t.Fatalf("expected studio_project_delete to require approval, got %+v", delOut)
	}
	if err := svc.ResolveApproval(ctx, delOut["approval_id"].(string), true); err != nil {
		t.Fatalf("ResolveApproval(studio_project_delete): %v", err)
	}
	if _, err := studioSvc.GetProject(ctx, created.Project.ID); err == nil {
		t.Fatalf("expected deleted project %s to be gone after ResolveApproval", created.Project.ID)
	}
}
