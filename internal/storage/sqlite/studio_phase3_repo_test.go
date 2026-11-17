package sqlite

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"app/internal/apperr"
	"app/internal/domain/studio"
)

func TestStudioPhase3TimelineRevisionsUndoRedoAndConflict(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, err := Open(ctx, Options{
		Path:          filepath.Join(dir, "timeline.db"),
		BackupDir:     filepath.Join(dir, "backups"),
		BackupsToKeep: 1,
		BusyTimeout:   time.Second,
		Logger:        slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	repo := NewStudioRepository(db)
	proj, err := repo.CreateProject(ctx, studio.Project{
		Title:          "Timeline Test",
		Genre:          studio.GenreDrama,
		ProductionMode: studio.ModeStoryboardI2V,
		AspectRatio:    "16:9",
	})
	if err != nil {
		t.Fatal(err)
	}
	ep, err := repo.CreateEpisode(ctx, studio.Episode{
		ProjectID: proj.ID,
		Code:      "ep01",
		Title:     "Ep 1",
		Stage:     studio.StageExport,
	})
	if err != nil {
		t.Fatal(err)
	}

	// 1. Fresh episode starts at revision 0
	rec0, err := repo.GetTimeline(ctx, ep.ID)
	if err != nil {
		t.Fatalf("GetTimeline initial: %v", err)
	}
	if rec0.Revision != 0 || rec0.CanUndo || rec0.CanRedo {
		t.Fatalf("expected revision 0 with no undo/redo, got %+v", rec0)
	}

	// 2. Save revision 1
	tl1 := studio.DefaultEditTimeline(1920, 1080, 24)
	tl1.Tracks[0].Clips = []studio.TimelineClip{{
		ID:          "clip-1",
		FilePath:    "D:/videos/s1.mp4",
		SourceInMs:  0,
		SourceOutMs: 4000,
		Speed:       1.0,
		Volume:      1.0,
	}}
	rec1, err := repo.SaveTimelineRevision(ctx, ep.ID, 0, studio.ActorNode, "auto assemble", tl1)
	if err != nil {
		t.Fatalf("SaveTimelineRevision rev 1: %v", err)
	}
	if rec1.Revision != 1 || rec1.CanUndo || rec1.CanRedo {
		t.Fatalf("unexpected rev 1 state: %+v", rec1)
	}

	// 3. Stale expected_revision = 0 must conflict
	if _, err := repo.SaveTimelineRevision(ctx, ep.ID, 0, studio.ActorAgent, "stale", tl1); err == nil {
		t.Fatal("expected optimistic revision conflict")
	} else {
		var ae *apperr.Error
		if !errors.As(err, &ae) || ae.Code != apperr.CodeConflict {
			t.Fatalf("expected CodeConflict, got %v", err)
		}
	}

	// 4. Save revision 2 (add subtitle) and revision 3 (add another subtitle)
	tl2 := tl1.Clone()
	tl2.Subtitles = []studio.SubtitleCue{{ID: "sub-1", StartMs: 0, EndMs: 2000, Text: "Xin chào"}}
	rec2, err := repo.SaveTimelineRevision(ctx, ep.ID, 1, studio.ActorUser, "add sub 1", tl2)
	if err != nil {
		t.Fatalf("SaveTimelineRevision rev 2: %v", err)
	}
	if rec2.Revision != 2 || !rec2.CanUndo || rec2.CanRedo {
		t.Fatalf("unexpected rev 2 state: %+v", rec2)
	}

	tl3 := tl2.Clone()
	tl3.Subtitles = append(tl3.Subtitles, studio.SubtitleCue{ID: "sub-2", StartMs: 2100, EndMs: 3900, Text: "Cảnh hai"})
	rec3, err := repo.SaveTimelineRevision(ctx, ep.ID, 2, studio.ActorAgent, "add sub 2", tl3)
	if err != nil {
		t.Fatalf("SaveTimelineRevision rev 3: %v", err)
	}
	if rec3.Revision != 3 || !rec3.CanUndo || rec3.CanRedo {
		t.Fatalf("unexpected rev 3 state: %+v", rec3)
	}

	// 5. Undo -> back to rev 2; Redo -> forward to rev 3; Undo -> back to rev 2
	undoRec, err := repo.UndoTimeline(ctx, ep.ID, 3)
	if err != nil {
		t.Fatalf("UndoTimeline: %v", err)
	}
	if undoRec.Revision != 2 || len(undoRec.Timeline.Subtitles) != 1 || !undoRec.CanUndo || !undoRec.CanRedo {
		t.Fatalf("unexpected undo state: %+v", undoRec)
	}

	redoRec, err := repo.RedoTimeline(ctx, ep.ID, 2)
	if err != nil {
		t.Fatalf("RedoTimeline: %v", err)
	}
	if redoRec.Revision != 3 || len(redoRec.Timeline.Subtitles) != 2 || !redoRec.CanUndo || redoRec.CanRedo {
		t.Fatalf("unexpected redo state: %+v", redoRec)
	}

	undoRec2, err := repo.UndoTimeline(ctx, ep.ID, 3)
	if err != nil {
		t.Fatalf("UndoTimeline again: %v", err)
	}

	// 6. New edit at rev 2 prunes redo tail (old rev 3) and writes new rev 3
	tlNew3 := undoRec2.Timeline.Clone()
	tlNew3.Subtitles[0].Text = "Bản sửa mới"
	recNew3, err := repo.SaveTimelineRevision(ctx, ep.ID, 2, studio.ActorUser, "rewrite sub 1", tlNew3)
	if err != nil {
		t.Fatalf("SaveTimelineRevision branch rev 3: %v", err)
	}
	if recNew3.Revision != 3 || recNew3.CanRedo || !recNew3.CanUndo {
		t.Fatalf("expected redo tail pruned after new edit at rev 2, got %+v", recNew3)
	}
	if _, err := repo.RedoTimeline(ctx, ep.ID, 3); err == nil {
		t.Fatal("expected redo error when at latest revision")
	}

	revs, err := repo.ListTimelineRevisions(ctx, ep.ID, 10)
	if err != nil || len(revs) != 3 {
		t.Fatalf("ListTimelineRevisions: len=%d err=%v", len(revs), err)
	}
}
