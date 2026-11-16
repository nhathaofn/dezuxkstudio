package sqlite

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestMediaRepoRegisterAndGet(t *testing.T) {
	ctx := context.Background()
	db := open(t, t.TempDir())
	defer db.Close()
	repo := NewMediaRepository(db)

	now := time.Now().Unix()
	item := MediaAssetRecord{
		Kind:           "image",
		SourceNode:     "tool.watermark_remover",
		FilePath:       "C:/workspace/media/out1.png",
		RegisteredRoot: "C:/workspace/media",
		MIMEType:       "image/png",
		SizeBytes:      1024,
		Width:          100,
		Height:         200,
		DurationSec:    0,
		MetadataJSON:   `{"author":"tester"}`,
		CreatedAt:      now,
		LastVerifiedAt: now,
	}

	registered, err := repo.RegisterAsset(ctx, item)
	if err != nil {
		t.Fatalf("RegisterAsset failed: %v", err)
	}
	if registered.ID == "" {
		t.Fatal("expected non-empty ID")
	}
	if registered.FilePath != item.FilePath {
		t.Fatalf("expected filePath %s, got %s", item.FilePath, registered.FilePath)
	}

	// Retrieve by ID
	got, err := repo.GetAsset(ctx, registered.ID)
	if err != nil {
		t.Fatalf("GetAsset failed: %v", err)
	}
	if got == nil {
		t.Fatal("expected asset, got nil")
	}
	if got.ID != registered.ID || got.FilePath != item.FilePath || got.Width != 100 || got.Height != 200 {
		t.Fatalf("asset mismatch: %+v", got)
	}

	// Retrieve by FilePath
	gotByPath, err := repo.GetAssetByPath(ctx, item.FilePath)
	if err != nil {
		t.Fatalf("GetAssetByPath failed: %v", err)
	}
	if gotByPath == nil || gotByPath.ID != registered.ID {
		t.Fatalf("expected asset by path: %+v", gotByPath)
	}

	// Upsert on same file path should preserve original ID and created_at
	updateItem := MediaAssetRecord{
		Kind:           "image",
		SourceNode:     "tool.generate_image",
		FilePath:       item.FilePath,
		RegisteredRoot: "C:/workspace/media",
		MIMEType:       "image/png",
		SizeBytes:      2048,
		Width:          300,
		Height:         400,
		MetadataJSON:   `{"updated":true}`,
	}
	updated, err := repo.RegisterAsset(ctx, updateItem)
	if err != nil {
		t.Fatalf("upsert RegisterAsset failed: %v", err)
	}
	if updated.ID != registered.ID {
		t.Fatalf("expected stable ID %s, got %s", registered.ID, updated.ID)
	}
	if updated.CreatedAt != registered.CreatedAt {
		t.Fatalf("expected preserved CreatedAt %d, got %d", registered.CreatedAt, updated.CreatedAt)
	}
	if updated.SizeBytes != 2048 || updated.Width != 300 {
		t.Fatalf("expected updated attributes, got %+v", updated)
	}
}

func TestMediaRepoGetMissing(t *testing.T) {
	ctx := context.Background()
	db := open(t, t.TempDir())
	defer db.Close()
	repo := NewMediaRepository(db)

	got, err := repo.GetAsset(ctx, "non-existent-id")
	if err != nil {
		t.Fatalf("expected no error for missing asset, got: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil for missing asset, got: %+v", got)
	}

	gotPath, err := repo.GetAssetByPath(ctx, "non-existent-path")
	if err != nil {
		t.Fatalf("expected no error for missing path, got: %v", err)
	}
	if gotPath != nil {
		t.Fatalf("expected nil for missing path, got: %+v", gotPath)
	}
}

func TestMediaRepoListAndFilter(t *testing.T) {
	ctx := context.Background()
	db := open(t, t.TempDir())
	defer db.Close()
	repo := NewMediaRepository(db)

	now := time.Now().Unix()
	for i := 0; i < 10; i++ {
		kind := "image"
		if i%2 == 0 {
			kind = "video"
		}
		_, err := repo.RegisterAsset(ctx, MediaAssetRecord{
			Kind:           kind,
			SourceNode:     "test",
			FilePath:       fmt.Sprintf("C:/workspace/media/file_%d.dat", i),
			RegisteredRoot: "C:/workspace/media",
			MIMEType:       "application/octet-stream",
			SizeBytes:      int64(100 * (i + 1)),
			CreatedAt:      now + int64(i),
			MetadataJSON:   fmt.Sprintf(`{"index":%d}`, i),
		})
		if err != nil {
			t.Fatalf("register asset %d failed: %v", i, err)
		}
	}

	// List all
	all, total, err := repo.ListAssets(ctx, MediaListFilter{Limit: 20})
	if err != nil {
		t.Fatalf("ListAssets failed: %v", err)
	}
	if total != 10 || len(all) != 10 {
		t.Fatalf("expected 10 items, got total=%d, len=%d", total, len(all))
	}

	// Filter by kind
	images, imgTotal, err := repo.ListAssets(ctx, MediaListFilter{Kind: "image"})
	if err != nil {
		t.Fatalf("filter by kind failed: %v", err)
	}
	if imgTotal != 5 || len(images) != 5 {
		t.Fatalf("expected 5 images, got total=%d, len=%d", imgTotal, len(images))
	}

	// Pagination
	page1, _, err := repo.ListAssets(ctx, MediaListFilter{Limit: 3, Offset: 0})
	if err != nil {
		t.Fatalf("page 1 failed: %v", err)
	}
	if len(page1) != 3 {
		t.Fatalf("expected 3 items on page 1, got %d", len(page1))
	}
	page2, _, err := repo.ListAssets(ctx, MediaListFilter{Limit: 3, Offset: 3})
	if err != nil {
		t.Fatalf("page 2 failed: %v", err)
	}
	if len(page2) != 3 {
		t.Fatalf("expected 3 items on page 2, got %d", len(page2))
	}
	if page1[0].ID == page2[0].ID {
		t.Fatal("page 1 and page 2 must not have the same first item")
	}

	// Search
	searched, sTotal, err := repo.ListAssets(ctx, MediaListFilter{Search: "file_7"})
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if sTotal != 1 || len(searched) != 1 || searched[0].FilePath != "C:/workspace/media/file_7.dat" {
		t.Fatalf("expected match for file_7, got: %+v", searched)
	}
}

func TestMediaRepoDeleteAndPrune(t *testing.T) {
	ctx := context.Background()
	db := open(t, t.TempDir())
	defer db.Close()
	repo := NewMediaRepository(db)

	now := time.Now().Unix()
	var ids []string
	for i := 0; i < 6; i++ {
		a, err := repo.RegisterAsset(ctx, MediaAssetRecord{
			Kind:           "image",
			SourceNode:     "test",
			FilePath:       fmt.Sprintf("C:/workspace/media/item_%d.png", i),
			RegisteredRoot: "C:/workspace/media",
			MIMEType:       "image/png",
			SizeBytes:      100,
			CreatedAt:      now - int64(100*(6-i)), // older items have smaller created_at
		})
		if err != nil {
			t.Fatalf("register failed: %v", err)
		}
		ids = append(ids, a.ID)
	}

	// Delete single asset
	if err := repo.DeleteAsset(ctx, ids[0]); err != nil {
		t.Fatalf("DeleteAsset failed: %v", err)
	}
	got, err := repo.GetAsset(ctx, ids[0])
	if err != nil || got != nil {
		t.Fatalf("expected deleted asset to be nil, got: %v, %+v", err, got)
	}

	// Prune missing assets
	pruned, err := repo.PruneMissingAssets(ctx, []string{ids[1], ids[2]})
	if err != nil {
		t.Fatalf("PruneMissingAssets failed: %v", err)
	}
	if pruned != 2 {
		t.Fatalf("expected 2 pruned, got %d", pruned)
	}

	// Prune by maxItems (currently 3 remaining: ids[3], ids[4], ids[5])
	// If maxItems = 2, it should prune the oldest 1 item (ids[3])
	excessPruned, err := repo.PruneExpiredAssets(ctx, time.Time{}, 2)
	if err != nil {
		t.Fatalf("PruneExpiredAssets maxItems failed: %v", err)
	}
	if excessPruned != 1 {
		t.Fatalf("expected 1 excess pruned, got %d", excessPruned)
	}
	remaining, total, err := repo.ListAssets(ctx, MediaListFilter{})
	if err != nil || total != 2 || len(remaining) != 2 {
		t.Fatalf("expected 2 remaining assets, got total=%d, len=%d", total, len(remaining))
	}
}

func TestMediaRepoUpdateLastVerified(t *testing.T) {
	ctx := context.Background()
	db := open(t, t.TempDir())
	defer db.Close()
	repo := NewMediaRepository(db)

	asset, err := repo.RegisterAsset(ctx, MediaAssetRecord{
		Kind:           "image",
		SourceNode:     "test",
		FilePath:       "C:/workspace/media/verify.png",
		RegisteredRoot: "C:/workspace/media",
		MIMEType:       "image/png",
		SizeBytes:      500,
		CreatedAt:      1000,
		LastVerifiedAt: 1000,
	})
	if err != nil {
		t.Fatalf("register failed: %v", err)
	}

	if err := repo.UpdateLastVerified(ctx, asset.ID, 2000); err != nil {
		t.Fatalf("UpdateLastVerified failed: %v", err)
	}

	got, err := repo.GetAsset(ctx, asset.ID)
	if err != nil || got == nil {
		t.Fatalf("GetAsset failed: %v", err)
	}
	if got.LastVerifiedAt != 2000 {
		t.Fatalf("expected last_verified_at 2000, got %d", got.LastVerifiedAt)
	}
}
