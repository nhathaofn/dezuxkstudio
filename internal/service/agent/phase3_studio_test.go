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

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"app/internal/contracts"
	studiosvc "app/internal/service/studio"
	"app/internal/storage/sqlite"
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
