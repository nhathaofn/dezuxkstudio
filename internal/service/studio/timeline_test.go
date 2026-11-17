package studio_test

import (
	"context"
	"encoding/json"
	"errors"
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
	"app/internal/service/media"
	studiosvc "app/internal/service/studio"
	"app/internal/storage/sqlite"
)

type phase3StubWorkflow struct {
	epRoot string
}

func (w *phase3StubWorkflow) RunNode(ctx context.Context, nodeType string, raw json.RawMessage) (any, error) {
	switch nodeType {
	case contracts.NodeTypeTimelineAutoAssem:
		var in contracts.TimelineAutoAssembleInput
		if err := json.Unmarshal(raw, &in); err != nil {
			return nil, err
		}
		tl := domainstudio.EditTimeline{
			Version: 1,
			FPS:     in.FPS,
			Width:   1080,
			Height:  1920,
			Tracks: []domainstudio.Track{
				{
					ID:     "v1",
					Name:   "Video",
					Kind:   domainstudio.TrackVideo,
					Volume: 1,
					Clips: []domainstudio.TimelineClip{
						{
							ID:              "vclip-1",
							ShotID:          "shot-1",
							Label:           "Cảnh 1",
							FilePath:        filepath.Join(w.epRoot, "media", "videos", "s1.mp4"),
							TimelineStartMs: 0,
							SourceInMs:      0,
							SourceOutMs:     5000,
							Speed:           1,
							Volume:          1,
							TransitionIn:    domainstudio.TransitionCrossfade,
							TransitionMs:    400,
						},
					},
				},
			},
			Subtitles: []domainstudio.SubtitleCue{
				{ID: "sub-1", StartMs: 0, EndMs: 4000, Text: "Cảnh mở đầu tiếng Việt"},
			},
		}
		b, _ := json.Marshal(tl)
		return contracts.TimelineAutoAssembleOutput{
			TimelineJSON:  string(b),
			TimelinePath:  filepath.Join(in.ProjectDir, "timeline", "timeline.json"),
			DurationMs:    5000,
			TrackCount:    1,
			ClipCount:     1,
			SubtitleCount: 1,
		}, nil

	case contracts.NodeTypeMediaPeaksThumbs:
		var in contracts.MediaPeaksThumbsInput
		if err := json.Unmarshal(raw, &in); err != nil {
			return nil, err
		}
		if err := engine.CheckFile(ctx, in.OutputDir, true); err != nil {
			return nil, err
		}
		_ = os.MkdirAll(in.OutputDir, 0o755)
		spritePath := filepath.Join(in.OutputDir, "sprite_1.jpg")
		peaksPath := filepath.Join(in.OutputDir, "peaks_1.json")
		_ = os.WriteFile(spritePath, []byte("SPRITEJPG"), 0o600)
		_ = os.WriteFile(peaksPath, []byte(`{"peaks":[0.2,0.9]}`), 0o600)
		return contracts.MediaPeaksThumbsOutput{
			PeaksThumbsJSON:  `{"peaks":[0.2,0.9]}`,
			SpriteSheetPath:  spritePath,
			PeaksJSONPath:    peaksPath,
			Peaks:            []float64{0.2, 0.9},
			ThumbCount:       2,
			ThumbIntervalSec: 2,
			DurationMs:       5000,
		}, nil

	case contracts.NodeTypeRenderTimeline:
		var in contracts.RenderTimelineInput
		if err := json.Unmarshal(raw, &in); err != nil {
			return nil, err
		}
		if err := engine.CheckFile(ctx, in.OutputDir, true); err != nil {
			return nil, err
		}
		_ = os.MkdirAll(in.OutputDir, 0o755)
		outPath := filepath.Join(in.OutputDir, "rendered_final.mp4")
		srtPath := filepath.Join(in.OutputDir, "rendered_final.srt")
		assPath := filepath.Join(in.OutputDir, "rendered_final.ass")
		_ = os.WriteFile(outPath, []byte("MP4FINAL"), 0o600)
		_ = os.WriteFile(srtPath, []byte("1\n00:00:00,000 --> 00:00:04,000\nXin chào"), 0o600)
		_ = os.WriteFile(assPath, []byte("[Script Info]"), 0o600)
		return contracts.RenderTimelineOutput{
			Video:       engine.RefFor(outPath, engine.MediaVideo),
			OutputPath:  outPath,
			SRTPath:     srtPath,
			ASSPath:     assPath,
			DurationSec: 5.0,
		}, nil

	case contracts.NodeTypeExportNLEDraft:
		var in contracts.ExportNLEDraftInput
		if err := json.Unmarshal(raw, &in); err != nil {
			return nil, err
		}
		if err := engine.CheckFile(ctx, in.OutputDir, true); err != nil {
			return nil, err
		}
		_ = os.MkdirAll(in.OutputDir, 0o755)
		zipPath := filepath.Join(in.OutputDir, "draft_capcut.zip")
		_ = os.WriteFile(zipPath, []byte("PKZIPDATA"), 0o600)
		return contracts.ExportNLEDraftOutput{
			ExportPath: zipPath,
			Format:     in.Format,
			FileCount:  4,
			TotalBytes: 9,
		}, nil
	}
	return nil, errors.New("unexpected node " + nodeType)
}

type fakeMediaRegistrar struct {
	next int
}

func (f *fakeMediaRegistrar) RegisterAsset(_ context.Context, p media.RegisterAssetParams) (*contracts.MediaAssetItem, error) {
	f.next++
	return &contracts.MediaAssetItem{
		ID:       filepath.Base(p.FilePath) + "-asset",
		FilePath: p.FilePath,
		Kind:     p.Kind,
	}, nil
}

func (f *fakeMediaRegistrar) AddAllowedRoot(string) {}

func TestPhase3TimelineServiceLifecycleAndConcurrency(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()
	db, err := sqlite.Open(ctx, sqlite.Options{
		Path:          filepath.Join(tmp, "studio_p3.db"),
		BackupDir:     filepath.Join(tmp, "backups"),
		BackupsToKeep: 2,
		BusyTimeout:   time.Second,
		Logger:        slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	projDir := filepath.Join(tmp, "projects")
	wf := &phase3StubWorkflow{}
	repo := sqlite.NewStudioRepository(db)
	var openedDirs []string
	var events []string

	svc := studiosvc.New(studiosvc.Options{
		Repo:        repo,
		Workflow:    wf,
		Media:       &fakeMediaRegistrar{},
		ProjectsDir: projDir,
		OpenDirectory: func(dir string) error {
			openedDirs = append(openedDirs, dir)
			return nil
		},
		OnEvent: func(ev string, _ any) {
			events = append(events, ev)
		},
		Logger: slog.Default(),
	})

	created, err := svc.CreateProject(ctx, contracts.StudioProjectCreatePayload{
		Title:       "Phim Ngắn Dọc",
		AspectRatio: "9:16",
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.ActiveEpisode == nil {
		t.Fatal("expected initial ActiveEpisode")
	}
	epID := created.ActiveEpisode.Episode.ID
	projSlug := created.Project.Slug
	epCode := created.ActiveEpisode.Episode.Code
	epRoot := filepath.Join(projDir, projSlug, "episodes", epCode)
	wf.epRoot = epRoot

	vidPath := filepath.Join(epRoot, "media", "videos", "s1.mp4")
	_ = os.MkdirAll(filepath.Dir(vidPath), 0o755)
	_ = os.WriteFile(vidPath, []byte("MP4SHOT"), 0o600)

	if _, err := repo.ReplaceShots(ctx, epID, []domainstudio.Shot{
		{ID: "shot-1", EpisodeID: epID, Seq: 1, Title: "Mở đầu", DurationSec: 5, VideoPath: vidPath},
	}); err != nil {
		t.Fatal(err)
	}

	// 1. Default timeline matches 9:16 resolution
	tl0, err := svc.GetTimeline(ctx, epID)
	if err != nil {
		t.Fatal(err)
	}
	if tl0.Revision != 0 || tl0.Timeline.Width != 1080 || tl0.Timeline.Height != 1920 {
		t.Fatalf("expected 1080x1920 rev 0 timeline, got %+v", tl0)
	}

	// 2. Auto-assemble -> revision 1, hydrated AssetID, disk timeline.json written
	tl1, err := svc.AutoAssembleTimeline(ctx, contracts.StudioTimelineAutoAssemblePayload{
		EpisodeID: epID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if tl1.Revision != 1 || len(tl1.Timeline.Tracks[0].Clips) != 1 || tl1.Timeline.Tracks[0].Clips[0].AssetID == "" {
		t.Fatalf("unexpected assembled timeline: %+v", tl1)
	}
	if _, serr := os.Stat(tl1.TimelinePath); serr != nil {
		t.Fatalf("expected timeline.json on disk: %v", serr)
	}

	// 3. Apply ops with stale revision fails with conflict
	if _, cerr := svc.ApplyTimelineOps(ctx, contracts.StudioTimelineApplyOpsPayload{
		EpisodeID:        epID,
		ExpectedRevision: 0,
		Ops:              []contracts.StudioTimelineOp{{Op: "split_clip", ClipID: "vclip-1", SplitAtMs: 2500}},
	}); !apperr.IsCode(cerr, apperr.CodeConflict) {
		t.Fatalf("expected conflict on stale expected_revision, got %v", cerr)
	}

	// FileGuard rejection on add_clip
	guardCtx := engine.WithFileGuard(ctx, func(p string, _ bool) error {
		if strings.Contains(p, "secret.mp4") {
			return errors.New("denied clip path")
		}
		return nil
	})
	if _, gerr := svc.ApplyTimelineOps(guardCtx, contracts.StudioTimelineApplyOpsPayload{
		EpisodeID:        epID,
		ExpectedRevision: 1,
		Ops: []contracts.StudioTimelineOp{{
			Op:      "add_clip",
			TrackID: "v1",
			Clip:    &contracts.StudioTimelineClip{ID: "bad", FilePath: filepath.Join(tmp, "secret.mp4"), SourceOutMs: 1000},
		}},
	}); gerr == nil || !strings.Contains(gerr.Error(), "denied clip path") {
		t.Fatalf("expected FileGuard error, got %v", gerr)
	}

	// Valid split_clip -> revision 2
	tl2, err := svc.ApplyTimelineOps(ctx, contracts.StudioTimelineApplyOpsPayload{
		EpisodeID:        epID,
		ExpectedRevision: 1,
		Actor:            "agent",
		Summary:          "split shot 1 at 2.5s",
		Ops: []contracts.StudioTimelineOp{
			{Op: "split_clip", ClipID: "vclip-1", NewClipID: "vclip-2", SplitAtMs: 2500},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if tl2.Revision != 2 || len(tl2.Timeline.Tracks[0].Clips) != 2 || !tl2.CanUndo {
		t.Fatalf("expected split rev 2 with 2 clips, got %+v", tl2)
	}

	// 4. Undo -> revision 1, Redo -> revision 2
	undone, err := svc.UndoTimeline(ctx, contracts.StudioTimelineRevisionNavPayload{
		EpisodeID:        epID,
		ExpectedRevision: 2,
	})
	if err != nil || undone.Revision != 1 || !undone.CanRedo || len(undone.Timeline.Tracks[0].Clips) != 1 {
		t.Fatalf("undo failed: err=%v res=%+v", err, undone)
	}
	redone, err := svc.RedoTimeline(ctx, contracts.StudioTimelineRevisionNavPayload{
		EpisodeID:        epID,
		ExpectedRevision: 1,
	})
	if err != nil || redone.Revision != 2 || len(redone.Timeline.Tracks[0].Clips) != 2 {
		t.Fatalf("redo failed: err=%v res=%+v", err, redone)
	}

	// 5. ComputeMediaPeaksThumbs updates clip peaks & sprite asset
	peaksOut, err := svc.ComputeMediaPeaksThumbs(ctx, contracts.StudioMediaPeaksThumbsPayload{
		EpisodeID: epID,
		MediaPath: vidPath,
		ClipID:    "vclip-1",
	})
	if err != nil || peaksOut.SpriteAssetID == "" || len(peaksOut.Peaks) != 2 {
		t.Fatalf("peaks failed: err=%v out=%+v", err, peaksOut)
	}

	// 6. RenderTimeline updates episode final video
	renderOut, err := svc.RenderTimeline(ctx, contracts.StudioTimelineRenderPayload{
		EpisodeID:     epID,
		BurnSubtitles: true,
	})
	if err != nil || renderOut.AssetID == "" || renderOut.Episode.FinalVideoPath != renderOut.OutputPath {
		t.Fatalf("render failed: err=%v out=%+v", err, renderOut)
	}

	// 7. ExportNLEDraft returns registered AssetID
	nleOut, err := svc.ExportNLEDraft(ctx, contracts.StudioTimelineExportNLEPayload{
		EpisodeID: epID,
		Format:    domainstudio.NLEFormatCapCut,
	})
	if err != nil || nleOut.AssetID == "" || nleOut.FileCount != 4 {
		t.Fatalf("nle export failed: err=%v out=%+v", err, nleOut)
	}

	// 8. OpenExportFolder validates containment and invokes callback
	if ferr := svc.OpenExportFolder(ctx, contracts.StudioOpenExportFolderPayload{
		EpisodeID: epID,
		FilePath:  filepath.Join(tmp, "outside.mp4"),
	}); !apperr.IsCode(ferr, apperr.CodeUnauthorized) {
		t.Fatalf("expected unauthorized for outside file_path, got %v", ferr)
	}
	if err := svc.OpenExportFolder(ctx, contracts.StudioOpenExportFolderPayload{EpisodeID: epID}); err != nil {
		t.Fatalf("open export folder failed: %v", err)
	}
	if len(openedDirs) != 1 || !strings.HasSuffix(openedDirs[0], "exports") {
		t.Fatalf("expected exports folder opened, got %v", openedDirs)
	}

	// 9. SyncWorkspace reconciles external edits to timeline.json
	afterPeaks, _ := svc.GetTimeline(ctx, epID)
	extTL := afterPeaks.Timeline
	extTL.Subtitles = append(extTL.Subtitles, contracts.StudioSubtitleCue{
		ID: "sub-disk", StartMs: 4100, EndMs: 4900, Text: "Phụ đề sửa tay trên đĩa",
	})
	bDisk, _ := json.MarshalIndent(extTL, "", "  ")
	if err := os.WriteFile(afterPeaks.TimelinePath, bDisk, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SyncWorkspace(ctx, created.Project.ID); err != nil {
		t.Fatal(err)
	}
	syncedTL, err := svc.GetTimeline(ctx, epID)
	if err != nil || len(syncedTL.Timeline.Subtitles) != 2 {
		t.Fatalf("expected synced disk timeline with 2 subtitles, got err=%v subs=%+v", err, syncedTL.Timeline.Subtitles)
	}
	_ = config.Default(config.ModeDesktop)
}
