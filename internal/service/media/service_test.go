package media_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"app/internal/apperr"
	"app/internal/config"
	"app/internal/contracts"
	"app/internal/security"
	"app/internal/service/media"
	"app/internal/storage/sqlite"
)

type memoryMediaRepo struct {
	assets map[string]sqlite.MediaAssetRecord
	byPath map[string]string
}

func newMemoryMediaRepo() *memoryMediaRepo {
	return &memoryMediaRepo{
		assets: make(map[string]sqlite.MediaAssetRecord),
		byPath: make(map[string]string),
	}
}

func (m *memoryMediaRepo) RegisterAsset(ctx context.Context, asset sqlite.MediaAssetRecord) (*sqlite.MediaAssetRecord, error) {
	if existingID, ok := m.byPath[asset.FilePath]; ok {
		old := m.assets[existingID]
		asset.ID = old.ID
		asset.CreatedAt = old.CreatedAt
	} else if asset.ID == "" {
		asset.ID = "mem-" + asset.FilePath
	}
	m.assets[asset.ID] = asset
	m.byPath[asset.FilePath] = asset.ID
	return &asset, nil
}

func (m *memoryMediaRepo) GetAsset(ctx context.Context, id string) (*sqlite.MediaAssetRecord, error) {
	a, ok := m.assets[id]
	if !ok {
		return nil, nil
	}
	return &a, nil
}

func (m *memoryMediaRepo) GetAssetByPath(ctx context.Context, filePath string) (*sqlite.MediaAssetRecord, error) {
	id, ok := m.byPath[filePath]
	if !ok {
		return nil, nil
	}
	a := m.assets[id]
	return &a, nil
}

func (m *memoryMediaRepo) ListAssets(ctx context.Context, filter sqlite.MediaListFilter) ([]sqlite.MediaAssetRecord, int, error) {
	var out []sqlite.MediaAssetRecord
	for _, a := range m.assets {
		if filter.Kind != "" && a.Kind != filter.Kind {
			continue
		}
		if filter.Search != "" && !strings.Contains(a.FilePath, filter.Search) {
			continue
		}
		out = append(out, a)
	}
	return out, len(out), nil
}

func (m *memoryMediaRepo) DeleteAsset(ctx context.Context, id string) error {
	a, ok := m.assets[id]
	if ok {
		delete(m.byPath, a.FilePath)
		delete(m.assets, id)
	}
	return nil
}

func (m *memoryMediaRepo) PruneMissingAssets(ctx context.Context, missingIDs []string) (int64, error) {
	var count int64
	for _, id := range missingIDs {
		if a, ok := m.assets[id]; ok {
			delete(m.byPath, a.FilePath)
			delete(m.assets, id)
			count++
		}
	}
	return count, nil
}

func (m *memoryMediaRepo) PruneExpiredAssets(ctx context.Context, olderThan time.Time, maxItems int) (int64, error) {
	return 0, nil
}

func (m *memoryMediaRepo) UpdateLastVerified(ctx context.Context, id string, verifiedAt int64) error {
	if a, ok := m.assets[id]; ok {
		a.LastVerifiedAt = verifiedAt
		m.assets[id] = a
	}
	return nil
}

func TestMediaServiceRegistrationAndListing(t *testing.T) {
	tmpDir := t.TempDir()
	mediaDir := filepath.Join(tmpDir, "media")
	if err := os.MkdirAll(mediaDir, 0o755); err != nil {
		t.Fatal(err)
	}

	testFile := filepath.Join(mediaDir, "sample.png")
	if err := os.WriteFile(testFile, []byte("fake png content"), 0o644); err != nil {
		t.Fatal(err)
	}

	repo := newMemoryMediaRepo()
	secret := []byte("super-secret-signing-key-32bytes!")
	signer := security.NewTicketSigner(secret)

	svc := media.New(media.Options{
		Repo:     repo,
		Signer:   signer,
		MediaDir: mediaDir,
		Config: config.MediaConfig{
			TicketTTL: 10 * time.Minute,
		},
	})

	ctx := context.Background()
	item, err := svc.RegisterAsset(ctx, media.RegisterAssetParams{
		FilePath:       testFile,
		RegisteredRoot: mediaDir,
		Kind:           "image",
		SourceNode:     "test",
	})
	if err != nil {
		t.Fatalf("RegisterAsset failed: %v", err)
	}
	if item.ID == "" || item.FilePath != testFile {
		t.Fatalf("unexpected asset item: %+v", item)
	}

	// List
	res, err := svc.List(ctx, contracts.MediaListPayload{})
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if res.Total != 1 || len(res.Assets) != 1 {
		t.Fatalf("expected 1 asset, got total=%d, len=%d", res.Total, len(res.Assets))
	}

	// Sign URL
	signRes, err := svc.SignAssetURL(ctx, contracts.MediaSignURLPayload{
		AssetID: item.ID,
		Purpose: "preview",
	})
	if err != nil {
		t.Fatalf("SignAssetURL failed: %v", err)
	}
	if signRes.Ticket == "" || !strings.Contains(signRes.URL, "ticket=") {
		t.Fatalf("unexpected signed URL result: %+v", signRes)
	}

	// Verify the ticket signature with TicketSigner directly
	claims, err := signer.VerifyTicket(signRes.Ticket, item.ID, "preview")
	if err != nil {
		t.Fatalf("ticket verification failed: %v", err)
	}
	if claims.AssetID != item.ID {
		t.Fatalf("expected asset %s, got %s", item.ID, claims.AssetID)
	}

	// Resolve asset file
	filePath, mimeType, modTime, size, err := svc.ResolveAssetFile(ctx, item.ID)
	if err != nil {
		t.Fatalf("ResolveAssetFile failed: %v", err)
	}
	if filePath != testFile || size <= 0 || modTime.IsZero() || mimeType == "" {
		t.Fatalf("unexpected file resolution: path=%s, mime=%s, size=%d, modTime=%v", filePath, mimeType, size, modTime)
	}

	// Delete
	if err := svc.Delete(ctx, item.ID, true); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	if _, err := os.Stat(testFile); !os.IsNotExist(err) {
		t.Fatalf("expected file to be deleted, got err: %v", err)
	}
}

func TestMediaServiceRealpathSymlinkGuard(t *testing.T) {
	tmpDir := t.TempDir()
	mediaDir := filepath.Join(tmpDir, "media")
	outsideDir := filepath.Join(tmpDir, "outside")
	if err := os.MkdirAll(mediaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outsideDir, 0o755); err != nil {
		t.Fatal(err)
	}

	secretFile := filepath.Join(outsideDir, "secret.txt")
	if err := os.WriteFile(secretFile, []byte("classified"), 0o644); err != nil {
		t.Fatal(err)
	}

	repo := newMemoryMediaRepo()
	svc := media.New(media.Options{
		Repo:     repo,
		MediaDir: mediaDir,
	})

	ctx := context.Background()

	// 1. Path outside registered root is rejected
	_, err := svc.RegisterAsset(ctx, media.RegisterAssetParams{
		FilePath:       secretFile,
		RegisteredRoot: mediaDir,
	})
	if err == nil {
		t.Fatal("expected registration of outside file to fail with forbidden")
	}
	var ae *apperr.Error
	if errors.As(err, &ae) && ae.Code != apperr.CodeUnauthorized {
		t.Fatalf("expected CodeUnauthorized, got %s", ae.Code)
	}

	// 2. Relative path traversal (..) escaping root is rejected
	traversalPath := filepath.Join(mediaDir, "..", "outside", "secret.txt")
	_, err = svc.RegisterAsset(ctx, media.RegisterAssetParams{
		FilePath:       traversalPath,
		RegisteredRoot: mediaDir,
	})
	if err == nil {
		t.Fatal("expected path traversal registration to fail")
	}

	// 3. Symlink escape (symlink in mediaDir pointing to secret outside)
	symlinkPath := filepath.Join(mediaDir, "symlink_escape.txt")
	if err := os.Symlink(secretFile, symlinkPath); err == nil {
		// Symlinks are supported on this environment, test it
		_, err = svc.RegisterAsset(ctx, media.RegisterAssetParams{
			FilePath:       symlinkPath,
			RegisteredRoot: mediaDir,
		})
		if err == nil {
			t.Fatal("expected symlink escape to be rejected by realpath guard")
		}
	}
}

func TestMediaServiceAutoPruneMissingOnListing(t *testing.T) {
	tmpDir := t.TempDir()
	mediaDir := filepath.Join(tmpDir, "media")
	if err := os.MkdirAll(mediaDir, 0o755); err != nil {
		t.Fatal(err)
	}

	file1 := filepath.Join(mediaDir, "file1.png")
	file2 := filepath.Join(mediaDir, "file2.png")
	_ = os.WriteFile(file1, []byte("f1"), 0o644)
	_ = os.WriteFile(file2, []byte("f2"), 0o644)

	repo := newMemoryMediaRepo()
	svc := media.New(media.Options{
		Repo:     repo,
		MediaDir: mediaDir,
	})

	ctx := context.Background()
	_, _ = svc.RegisterAsset(ctx, media.RegisterAssetParams{FilePath: file1, RegisteredRoot: mediaDir})
	_, _ = svc.RegisterAsset(ctx, media.RegisterAssetParams{FilePath: file2, RegisteredRoot: mediaDir})

	// Verify 2 assets
	list1, _ := svc.List(ctx, contracts.MediaListPayload{})
	if list1.Total != 2 {
		t.Fatalf("expected 2 items, got %d", list1.Total)
	}

	// Delete file2 on disk
	_ = os.Remove(file2)

	// List again: file2 should be automatically pruned
	list2, err := svc.List(ctx, contracts.MediaListPayload{})
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if list2.Total != 1 || len(list2.Assets) != 1 || list2.Assets[0].FilePath != file1 {
		t.Fatalf("expected 1 remaining asset, got total=%d, assets=%+v", list2.Total, list2.Assets)
	}
}

func TestMediaServiceRegisterInputAndSignURL(t *testing.T) {
	tmpDir := t.TempDir()
	mediaDir := filepath.Join(tmpDir, "media")
	if err := os.MkdirAll(mediaDir, 0o755); err != nil {
		t.Fatal(err)
	}

	testFile := filepath.Join(mediaDir, "audio.wav")
	if err := os.WriteFile(testFile, []byte("fake wav audio content"), 0o644); err != nil {
		t.Fatal(err)
	}

	repo := newMemoryMediaRepo()
	secret := []byte("test-signing-key-for-media-service-tests")
	signer := security.NewTicketSigner(secret)

	var changedCalls int
	svc := media.New(media.Options{
		Repo:     repo,
		Signer:   signer,
		MediaDir: mediaDir,
		OnChanged: func() {
			changedCalls++
		},
		Config: config.MediaConfig{
			TicketTTL: 15 * time.Minute,
		},
	})

	ctx := context.Background()

	// RegisterInput
	item, err := svc.RegisterInput(ctx, contracts.MediaRegisterInputPayload{
		FilePath:   testFile,
		Kind:       "audio",
		SourceNode: "upload",
	})
	if err != nil {
		t.Fatalf("RegisterInput failed: %v", err)
	}
	if item.ID == "" || item.Kind != "audio" || item.SourceNode != "upload" {
		t.Fatalf("unexpected registered input item: %+v", item)
	}
	if changedCalls != 1 {
		t.Fatalf("expected OnChanged to be called once, got %d", changedCalls)
	}

	// SignURL helper
	signedURL, err := svc.SignURL(ctx, item.ID, "download")
	if err != nil {
		t.Fatalf("SignURL failed: %v", err)
	}
	if !strings.Contains(signedURL, "purpose=download") || !strings.Contains(signedURL, "ticket=") {
		t.Fatalf("unexpected signed url: %s", signedURL)
	}

	// SignURL invalid asset ID
	_, err = svc.SignURL(ctx, "non-existent", "preview")
	if err == nil {
		t.Fatal("expected SignURL to fail for non-existent asset")
	}

	// Resolve non-existent asset
	_, _, _, _, err = svc.ResolveAssetFile(ctx, "non-existent")
	if err == nil {
		t.Fatal("expected ResolveAssetFile to fail for non-existent asset")
	}
}

func TestMediaServiceDynamicMediaDirAndRootRetention(t *testing.T) {
	tmpDir := t.TempDir()
	rootA := filepath.Join(tmpDir, "mediaA")
	rootB := filepath.Join(tmpDir, "mediaB")
	stagingDir := filepath.Join(tmpDir, "staging")
	if err := os.MkdirAll(rootA, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(rootB, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(stagingDir, 0o755); err != nil {
		t.Fatal(err)
	}

	fileA := filepath.Join(rootA, "imageA.png")
	if err := os.WriteFile(fileA, []byte("image-a-content"), 0o644); err != nil {
		t.Fatal(err)
	}
	fileB := filepath.Join(rootB, "imageB.png")
	if err := os.WriteFile(fileB, []byte("image-b-content"), 0o644); err != nil {
		t.Fatal(err)
	}

	repo := newMemoryMediaRepo()
	signer := security.NewTicketSigner([]byte("test-key-32-bytes-long-for-test-media"))
	svc := media.New(media.Options{
		Repo:       repo,
		Signer:     signer,
		MediaDir:   rootA,
		StagingDir: stagingDir,
	})

	if svc.MediaDir() != rootA {
		t.Fatalf("expected MediaDir=%s, got %s", rootA, svc.MediaDir())
	}
	if svc.StagingDir() != stagingDir {
		t.Fatalf("expected StagingDir=%s, got %s", stagingDir, svc.StagingDir())
	}

	ctx := context.Background()
	itemA, err := svc.RegisterAsset(ctx, media.RegisterAssetParams{
		FilePath:       fileA,
		Kind:           "image",
		SourceNode:     "test",
		RegisteredRoot: rootA,
	})
	if err != nil {
		t.Fatalf("register asset A failed: %v", err)
	}

	// Update to rootB
	svc.SetMediaDir(rootB)
	if svc.MediaDir() != rootB {
		t.Fatalf("expected updated MediaDir=%s, got %s", rootB, svc.MediaDir())
	}

	// Register file in rootB without explicitly specifying registered root (defaults to dynamic mediaDir)
	itemB, err := svc.RegisterAsset(ctx, media.RegisterAssetParams{
		FilePath:   fileB,
		Kind:       "image",
		SourceNode: "test",
	})
	if err != nil {
		t.Fatalf("register asset B failed: %v", err)
	}

	// Both paths and staging must be authorized
	if !svc.IsPathAuthorized(fileA) {
		t.Error("old media file in rootA should remain authorized")
	}
	if !svc.IsPathAuthorized(fileB) {
		t.Error("new media file in rootB must be authorized")
	}
	if !svc.IsPathAuthorized(stagingDir) {
		t.Error("stagingDir must be authorized")
	}

	// Verify resolve asset file still works for old asset A
	resolvedA, _, _, _, err := svc.ResolveAssetFile(ctx, itemA.ID)
	if err != nil {
		t.Fatalf("ResolveAssetFile for item A failed: %v", err)
	}
	if resolvedA != fileA {
		t.Errorf("expected %s, got %s", fileA, resolvedA)
	}

	resolvedB, _, _, _, err := svc.ResolveAssetFile(ctx, itemB.ID)
	if err != nil {
		t.Fatalf("ResolveAssetFile for item B failed: %v", err)
	}
	if resolvedB != fileB {
		t.Errorf("expected %s, got %s", fileB, resolvedB)
	}
}

func TestMediaServiceEvictionProtectsCurrentMediaDirAndStagingDir(t *testing.T) {
	tmpDir := t.TempDir()
	rootA := filepath.Join(tmpDir, "mediaA")
	stagingDir := filepath.Join(tmpDir, "staging")
	extra1 := filepath.Join(tmpDir, "extra1")
	extra2 := filepath.Join(tmpDir, "extra2")
	rootB := filepath.Join(tmpDir, "mediaB")

	for _, d := range []string{rootA, stagingDir, extra1, extra2, rootB} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	repo := newMemoryMediaRepo()
	svc := media.New(media.Options{
		Repo:         repo,
		MediaDir:     rootA,
		StagingDir:   stagingDir,
		AllowedRoots: []string{extra1},
		Config: config.MediaConfig{
			RegisteredRootsMax: 3,
		},
	})

	// Roots now: [rootA, stagingDir, extra1] (total 3 = max)
	if !svc.IsPathAuthorized(rootA) || !svc.IsPathAuthorized(stagingDir) || !svc.IsPathAuthorized(extra1) {
		t.Fatal("initial roots should all be authorized")
	}

	// Add extra2: should evict extra1, leaving rootA and stagingDir untouched
	svc.AddAllowedRoot(extra2)
	if !svc.IsPathAuthorized(rootA) {
		t.Error("current mediaDir rootA must not be evicted")
	}
	if !svc.IsPathAuthorized(stagingDir) {
		t.Error("current stagingDir must not be evicted")
	}
	if !svc.IsPathAuthorized(extra2) {
		t.Error("newly added extra2 must be authorized")
	}
	if svc.IsPathAuthorized(extra1) {
		t.Error("extra1 should have been evicted")
	}

	// Now switch mediaDir to rootB: should add rootB and evict extra2
	svc.SetMediaDir(rootB)
	if !svc.IsPathAuthorized(rootB) {
		t.Error("new mediaDir rootB must be authorized")
	}
	if !svc.IsPathAuthorized(rootA) {
		t.Error("old mediaDir rootA should remain authorized")
	}
	if !svc.IsPathAuthorized(stagingDir) {
		t.Error("stagingDir must not be evicted")
	}
	if svc.IsPathAuthorized(extra2) {
		t.Error("extra2 should have been evicted")
	}
}

func TestMediaServiceConcurrency(t *testing.T) {
	tmpDir := t.TempDir()
	staging := filepath.Join(tmpDir, "staging")
	_ = os.MkdirAll(staging, 0o755)

	svc := media.New(media.Options{
		Repo:       newMemoryMediaRepo(),
		MediaDir:   filepath.Join(tmpDir, "initial"),
		StagingDir: staging,
	})

	done := make(chan struct{})
	go func() {
		for i := 0; i < 100; i++ {
			svc.SetMediaDir(filepath.Join(tmpDir, "dir", "a"))
			svc.AddAllowedRoot(filepath.Join(tmpDir, "dir", "b"))
		}
		close(done)
	}()

	for i := 0; i < 100; i++ {
		_ = svc.MediaDir()
		_ = svc.StagingDir()
		_ = svc.IsPathAuthorized(filepath.Join(tmpDir, "dir", "a", "file.png"))
	}
	<-done
}
