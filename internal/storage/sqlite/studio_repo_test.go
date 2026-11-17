package sqlite

import (
	"context"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"app/internal/domain/studio"
)

func openTestStudioDB(t *testing.T) *DB {
	t.Helper()
	dir := t.TempDir()
	db, err := Open(context.Background(), Options{
		Path:          filepath.Join(dir, "studio.db"),
		BackupDir:     filepath.Join(dir, "backups"),
		BackupsToKeep: 2,
		BusyTimeout:   time.Second,
		Logger:        slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestStudioRepositoryCRUDAndCascade(t *testing.T) {
	ctx := context.Background()
	db := openTestStudioDB(t)
	repo := NewStudioRepository(db)

	proj, err := repo.CreateProject(ctx, studio.Project{
		Title:          "Cánh Đồng Mây",
		Genre:          studio.GenreNarration,
		ProductionMode: studio.ModeStoryboardI2V,
		AspectRatio:    "16:9",
		StylePrompt:    "cinematic watercolor",
	})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if proj.Slug != "canh-dong-may" {
		t.Fatalf("unexpected slug: %q", proj.Slug)
	}

	gotBySlug, err := repo.GetProject(ctx, "canh-dong-may")
	if err != nil || gotBySlug.ID != proj.ID {
		t.Fatalf("GetProject by slug: %+v err=%v", gotBySlug, err)
	}

	// Duplicate slug fails with conflict
	if _, err := repo.CreateProject(ctx, studio.Project{Title: "Cánh Đồng Mây"}); err == nil {
		t.Fatal("expected duplicate slug error")
	}

	ep, err := repo.CreateEpisode(ctx, studio.Episode{
		ProjectID: proj.ID,
		Code:      "ep01",
		Title:     "Tập 1",
	})
	if err != nil {
		t.Fatalf("CreateEpisode: %v", err)
	}

	asset, err := repo.UpsertAsset(ctx, studio.AssetEntry{
		ProjectID:   proj.ID,
		Kind:        studio.AssetCharacter,
		Name:        "Minh",
		CodeTag:     "@Minh",
		Description: "Young traveler",
		Prompt:      "character turnaround sheet",
	})
	if err != nil {
		t.Fatalf("UpsertAsset: %v", err)
	}
	// Upsert same code_tag without ID updates the existing row
	asset2, err := repo.UpsertAsset(ctx, studio.AssetEntry{
		ProjectID:   proj.ID,
		Kind:        studio.AssetCharacter,
		Name:        "Minh",
		CodeTag:     "@Minh",
		Description: "Updated description",
		Prompt:      "updated turnaround",
	})
	if err != nil || asset2.ID != asset.ID || asset2.Description != "Updated description" {
		t.Fatalf("UpsertAsset update by tag failed: %+v err=%v", asset2, err)
	}

	shots, err := repo.ReplaceShots(ctx, ep.ID, []studio.Shot{
		{
			SceneCode:     "S01",
			Title:         "Opening",
			DurationSec:   5,
			ImagePrompt:   "@Minh standing on hill",
			VideoPrompt:   "slow push in",
			NarrationText: "Chuyện bắt đầu từ sáng sớm.",
			RefAssetTags:  []string{"@Minh"},
			Dialogues:     []studio.DialogueLine{{Speaker: "Minh", Line: "Đi thôi"}},
		},
		{
			SceneCode:   "S01",
			Title:       "Depart",
			DurationSec: 6,
			ImagePrompt: "train leaving station",
		},
	})
	if err != nil {
		t.Fatalf("ReplaceShots: %v", err)
	}
	if len(shots) != 2 || shots[0].Seq != 1 || shots[1].Seq != 2 {
		t.Fatalf("unexpected shots after ReplaceShots: %+v", shots)
	}

	reordered, err := repo.ReorderShots(ctx, ep.ID, []string{shots[1].ID, shots[0].ID})
	if err != nil {
		t.Fatalf("ReorderShots: %v", err)
	}
	if reordered[0].ID != shots[1].ID || reordered[0].Seq != 1 || reordered[1].Seq != 2 {
		t.Fatalf("unexpected shot order: %+v", reordered)
	}

	// Cascade delete removes episodes, assets, and shots
	if err := repo.DeleteProject(ctx, proj.ID); err != nil {
		t.Fatalf("DeleteProject: %v", err)
	}
	if eps, _ := repo.ListEpisodes(ctx, proj.ID); len(eps) != 0 {
		t.Fatalf("expected episodes deleted on cascade, got %d", len(eps))
	}
	if shs, _ := repo.ListShots(ctx, ep.ID); len(shs) != 0 {
		t.Fatalf("expected shots deleted on cascade, got %d", len(shs))
	}
	if asts, _ := repo.ListAssets(ctx, proj.ID); len(asts) != 0 {
		t.Fatalf("expected assets deleted on cascade, got %d", len(asts))
	}
}
