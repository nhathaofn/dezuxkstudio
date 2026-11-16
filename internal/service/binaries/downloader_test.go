package binaries

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"app/internal/apperr"
	"app/internal/contracts"
	domainbinaries "app/internal/domain/binaries"
)

func setupTestDownloadSpec(t *testing.T, id domainbinaries.BinaryID, spec domainbinaries.DownloadSpec) {
	t.Helper()
	orig := domainbinaries.Registry[id]
	updated := orig
	platformKey := fmt.Sprintf("%s_%s", runtime.GOOS, runtime.GOARCH)
	downloads := make(map[string]domainbinaries.DownloadSpec, len(orig.Downloads)+1)
	for k, v := range orig.Downloads {
		downloads[k] = v
	}
	downloads[platformKey] = spec
	updated.Downloads = downloads
	domainbinaries.Registry[id] = updated
	t.Cleanup(func() {
		domainbinaries.Registry[id] = orig
	})
}

// TestDownloader_FullFromScratch tests a fresh download of a raw binary with SHA-256 verification and cleanup.
func TestDownloader_FullFromScratch(t *testing.T) {
	toolsDir := t.TempDir()
	rawContent := []byte("#!/bin/sh\necho ffmpeg version 7.1\n")
	sum := sha256.Sum256(rawContent)
	expectedSHA := hex.EncodeToString(sum[:])

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(rawContent)))
		w.Header().Set("ETag", `"v1-clean"`)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(rawContent)
	}))
	defer server.Close()

	setupTestDownloadSpec(t, domainbinaries.IDFFmpeg, domainbinaries.DownloadSpec{
		URL:           server.URL + "/ffmpeg",
		ArchiveFormat: domainbinaries.ArchiveFormatRaw,
		SHA256:        expectedSHA,
	})

	svc := New(newMemoryRepo(), toolsDir, slog.New(slog.DiscardHandler))
	info, err := svc.Download(context.Background(), domainbinaries.IDFFmpeg)
	if err != nil {
		t.Fatalf("Download failed: %v", err)
	}
	if info.ID != domainbinaries.IDFFmpeg {
		t.Fatalf("expected ID %s, got %s", domainbinaries.IDFFmpeg, info.ID)
	}

	partPath := filepath.Join(toolsDir, fmt.Sprintf(".%s-download.part", domainbinaries.IDFFmpeg))
	metaPath := filepath.Join(toolsDir, fmt.Sprintf(".%s-download.part.meta", domainbinaries.IDFFmpeg))
	if _, err := os.Stat(partPath); !os.IsNotExist(err) {
		t.Errorf("temporary .part file was not cleaned up")
	}
	if _, err := os.Stat(metaPath); !os.IsNotExist(err) {
		t.Errorf("temporary .part.meta file was not cleaned up")
	}
}

// TestDownloader_Resume_206PartialContent tests successful resumption of a partial download via HTTP 206.
func TestDownloader_Resume_206PartialContent(t *testing.T) {
	toolsDir := t.TempDir()

	// 1000 bytes total content
	totalData := make([]byte, 1000)
	for i := range totalData {
		totalData[i] = byte(i % 256)
	}
	sum := sha256.Sum256(totalData)
	expectedSHA := hex.EncodeToString(sum[:])

	// First 400 bytes downloaded previously
	resumedOffset := int64(400)
	partPath := filepath.Join(toolsDir, fmt.Sprintf(".%s-download.part", domainbinaries.IDFFmpeg))
	metaPath := filepath.Join(toolsDir, fmt.Sprintf(".%s-download.part.meta", domainbinaries.IDFFmpeg))

	if err := os.WriteFile(partPath, totalData[:resumedOffset], 0644); err != nil {
		t.Fatal(err)
	}

	etag := `"etag-resume-test"`
	var serverURL string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rangeHdr := r.Header.Get("Range")
		ifRange := r.Header.Get("If-Range")

		if rangeHdr == fmt.Sprintf("bytes=%d-", resumedOffset) && ifRange == etag {
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", resumedOffset, len(totalData)-1, len(totalData)))
			w.Header().Set("Content-Length", fmt.Sprintf("%d", len(totalData)-int(resumedOffset)))
			w.Header().Set("ETag", etag)
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write(totalData[resumedOffset:])
			return
		}

		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(totalData)))
		w.Header().Set("ETag", etag)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(totalData)
	}))
	defer server.Close()
	serverURL = server.URL + "/ffmpeg"

	meta := downloadPartMeta{
		ETag:           etag,
		TotalBytes:     int64(len(totalData)),
		ExpectedSHA256: expectedSHA,
		URL:            serverURL,
		LastUpdated:    time.Now().UTC(),
	}
	if err := savePartMeta(metaPath, meta); err != nil {
		t.Fatal(err)
	}

	setupTestDownloadSpec(t, domainbinaries.IDFFmpeg, domainbinaries.DownloadSpec{
		URL:           serverURL,
		ArchiveFormat: domainbinaries.ArchiveFormatRaw,
		SHA256:        expectedSHA,
	})

	var mu sync.Mutex
	var progressList []contracts.BinaryProgress
	svc := New(newMemoryRepo(), toolsDir, slog.New(slog.DiscardHandler),
		WithOnProgress(func(bp contracts.BinaryProgress) {
			mu.Lock()
			progressList = append(progressList, bp)
			mu.Unlock()
		}),
	)

	_, err := svc.Download(context.Background(), domainbinaries.IDFFmpeg)
	if err != nil {
		t.Fatalf("Download failed: %v", err)
	}

	mu.Lock()
	events := append([]contracts.BinaryProgress(nil), progressList...)
	mu.Unlock()

	hasResume := false
	for _, ev := range events {
		if ev.IsResume && ev.ResumedFromBytes == resumedOffset {
			hasResume = true
			break
		}
	}
	if !hasResume {
		t.Errorf("expected at least one progress event with IsResume=true and ResumedFromBytes=%d, got %+v", resumedOffset, events)
	}

	targetPath := filepath.Join(toolsDir, domainbinaries.Registry[domainbinaries.IDFFmpeg].ExecutableName())
	gotBytes, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("failed to read installed binary: %v", err)
	}
	if !bytes.Equal(gotBytes, totalData) {
		t.Errorf("binary content mismatch after resume")
	}
}

// TestDownloader_ResumeFallback_200OK tests that if server returns 200 OK (no range support),
// the partial file is truncated and download restarts cleanly from byte 0 without file corruption.
func TestDownloader_ResumeFallback_200OK(t *testing.T) {
	toolsDir := t.TempDir()

	totalData := make([]byte, 800)
	for i := range totalData {
		totalData[i] = byte(i * 3 % 256)
	}
	sum := sha256.Sum256(totalData)
	expectedSHA := hex.EncodeToString(sum[:])

	partPath := filepath.Join(toolsDir, fmt.Sprintf(".%s-download.part", domainbinaries.IDFFmpeg))
	metaPath := filepath.Join(toolsDir, fmt.Sprintf(".%s-download.part.meta", domainbinaries.IDFFmpeg))

	// Pre-populate partial file
	if err := os.WriteFile(partPath, totalData[:300], 0644); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Server ignores Range header and always returns 200 OK with full file
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(totalData)))
		w.Header().Set("ETag", `"always-200"`)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(totalData)
	}))
	defer server.Close()

	meta := downloadPartMeta{
		ETag:           `"always-200"`,
		TotalBytes:     int64(len(totalData)),
		ExpectedSHA256: expectedSHA,
		URL:            server.URL + "/ffmpeg",
		LastUpdated:    time.Now().UTC(),
	}
	if err := savePartMeta(metaPath, meta); err != nil {
		t.Fatal(err)
	}

	setupTestDownloadSpec(t, domainbinaries.IDFFmpeg, domainbinaries.DownloadSpec{
		URL:           server.URL + "/ffmpeg",
		ArchiveFormat: domainbinaries.ArchiveFormatRaw,
		SHA256:        expectedSHA,
	})

	svc := New(newMemoryRepo(), toolsDir, slog.New(slog.DiscardHandler))
	_, err := svc.Download(context.Background(), domainbinaries.IDFFmpeg)
	if err != nil {
		t.Fatalf("Download failed: %v", err)
	}

	targetPath := filepath.Join(toolsDir, domainbinaries.Registry[domainbinaries.IDFFmpeg].ExecutableName())
	gotBytes, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("failed to read installed binary: %v", err)
	}
	if len(gotBytes) != len(totalData) {
		t.Fatalf("expected binary length %d, got %d (possible duplicate/corrupt append)", len(totalData), len(gotBytes))
	}
	if !bytes.Equal(gotBytes, totalData) {
		t.Errorf("binary content mismatch after 200 OK fallback")
	}
}

// TestDownloader_ResumeFallback_ChangedETag tests fallback to 200 OK when remote ETag changed.
func TestDownloader_ResumeFallback_ChangedETag(t *testing.T) {
	toolsDir := t.TempDir()

	newContent := []byte("new-version-content-after-update-on-server-7.2")
	sum := sha256.Sum256(newContent)
	expectedSHA := hex.EncodeToString(sum[:])

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// If-Range holds stale "v1-etag", so server rejects range and returns 200 with new "v2-etag"
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(newContent)))
		w.Header().Set("ETag", `"v2-etag"`)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(newContent)
	}))
	defer server.Close()

	partPath := filepath.Join(toolsDir, fmt.Sprintf(".%s-download.part", domainbinaries.IDFFmpeg))
	metaPath := filepath.Join(toolsDir, fmt.Sprintf(".%s-download.part.meta", domainbinaries.IDFFmpeg))

	if err := os.WriteFile(partPath, []byte("old-partial-bytes"), 0644); err != nil {
		t.Fatal(err)
	}
	meta := downloadPartMeta{
		ETag:           `"v1-etag"`,
		TotalBytes:     100,
		ExpectedSHA256: expectedSHA,
		URL:            server.URL + "/ffmpeg",
		LastUpdated:    time.Now().UTC(),
	}
	if err := savePartMeta(metaPath, meta); err != nil {
		t.Fatal(err)
	}

	setupTestDownloadSpec(t, domainbinaries.IDFFmpeg, domainbinaries.DownloadSpec{
		URL:           server.URL + "/ffmpeg",
		ArchiveFormat: domainbinaries.ArchiveFormatRaw,
		SHA256:        expectedSHA,
	})

	svc := New(newMemoryRepo(), toolsDir, slog.New(slog.DiscardHandler))
	_, err := svc.Download(context.Background(), domainbinaries.IDFFmpeg)
	if err != nil {
		t.Fatalf("Download failed: %v", err)
	}

	targetPath := filepath.Join(toolsDir, domainbinaries.Registry[domainbinaries.IDFFmpeg].ExecutableName())
	gotBytes, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("failed to read installed binary: %v", err)
	}
	if !bytes.Equal(gotBytes, newContent) {
		t.Errorf("expected new content after ETag change, got %q", string(gotBytes))
	}
}

// TestDownloader_ResumeFallback_416RangeNotSatisfiable tests handling of HTTP 416 by clearing
// .part/.part.meta and restarting cleanly from byte 0.
func TestDownloader_ResumeFallback_416RangeNotSatisfiable(t *testing.T) {
	toolsDir := t.TempDir()

	content := []byte("clean-content-after-416-retry")
	sum := sha256.Sum256(content)
	expectedSHA := hex.EncodeToString(sum[:])

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") != "" {
			w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
			return
		}
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(content)))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(content)
	}))
	defer server.Close()

	partPath := filepath.Join(toolsDir, fmt.Sprintf(".%s-download.part", domainbinaries.IDFFmpeg))
	metaPath := filepath.Join(toolsDir, fmt.Sprintf(".%s-download.part.meta", domainbinaries.IDFFmpeg))

	// Invalid offset: 9999 bytes when file is small
	if err := os.WriteFile(partPath, bytes.Repeat([]byte{0xFF}, 9999), 0644); err != nil {
		t.Fatal(err)
	}
	meta := downloadPartMeta{
		ETag:           `"some-etag"`,
		TotalBytes:     20000,
		ExpectedSHA256: expectedSHA,
		URL:            server.URL + "/ffmpeg",
		LastUpdated:    time.Now().UTC(),
	}
	if err := savePartMeta(metaPath, meta); err != nil {
		t.Fatal(err)
	}

	setupTestDownloadSpec(t, domainbinaries.IDFFmpeg, domainbinaries.DownloadSpec{
		URL:           server.URL + "/ffmpeg",
		ArchiveFormat: domainbinaries.ArchiveFormatRaw,
		SHA256:        expectedSHA,
	})

	svc := New(newMemoryRepo(), toolsDir, slog.New(slog.DiscardHandler))
	_, err := svc.Download(context.Background(), domainbinaries.IDFFmpeg)
	if err != nil {
		t.Fatalf("Download failed: %v", err)
	}

	targetPath := filepath.Join(toolsDir, domainbinaries.Registry[domainbinaries.IDFFmpeg].ExecutableName())
	gotBytes, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("failed to read installed binary: %v", err)
	}
	if !bytes.Equal(gotBytes, content) {
		t.Errorf("binary content mismatch after 416 fallback recovery")
	}
}

// TestDownloader_SHA256_CorruptionRejection tests that corrupted bytes trigger CodeIntegrity
// and clean up all temporary files.
func TestDownloader_SHA256_CorruptionRejection(t *testing.T) {
	toolsDir := t.TempDir()

	validContent := []byte("legitimate-binary-bytes")
	sum := sha256.Sum256(validContent)
	expectedSHA := hex.EncodeToString(sum[:])

	// Server serves corrupted payload
	corrupted := []byte("malicious-corrupted-bytes")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(corrupted)))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(corrupted)
	}))
	defer server.Close()

	setupTestDownloadSpec(t, domainbinaries.IDFFmpeg, domainbinaries.DownloadSpec{
		URL:           server.URL + "/ffmpeg",
		ArchiveFormat: domainbinaries.ArchiveFormatRaw,
		SHA256:        expectedSHA,
	})

	svc := New(newMemoryRepo(), toolsDir, slog.New(slog.DiscardHandler))
	_, err := svc.Download(context.Background(), domainbinaries.IDFFmpeg)
	if err == nil {
		t.Fatalf("expected error for corrupted SHA-256, got nil")
	}
	if !apperr.IsCode(err, apperr.CodeIntegrity) {
		t.Fatalf("expected CodeIntegrity error, got %v", err)
	}

	partPath := filepath.Join(toolsDir, fmt.Sprintf(".%s-download.part", domainbinaries.IDFFmpeg))
	metaPath := filepath.Join(toolsDir, fmt.Sprintf(".%s-download.part.meta", domainbinaries.IDFFmpeg))
	if _, err := os.Stat(partPath); !os.IsNotExist(err) {
		t.Errorf("temporary .part file was not cleaned up on checksum failure")
	}
	if _, err := os.Stat(metaPath); !os.IsNotExist(err) {
		t.Errorf("temporary .part.meta file was not cleaned up on checksum failure")
	}
}

// TestDownloader_RetryBackoff_TransientErrors tests that transient HTTP 503 errors
// trigger retry backoff using WithRetry.
func TestDownloader_RetryBackoff_TransientErrors(t *testing.T) {
	toolsDir := t.TempDir()

	content := []byte("success-after-retries")
	sum := sha256.Sum256(content)
	expectedSHA := hex.EncodeToString(sum[:])

	var attempts int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := atomic.AddInt32(&attempts, 1)
		if n <= 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(content)))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(content)
	}))
	defer server.Close()

	setupTestDownloadSpec(t, domainbinaries.IDFFmpeg, domainbinaries.DownloadSpec{
		URL:           server.URL + "/ffmpeg",
		ArchiveFormat: domainbinaries.ArchiveFormatRaw,
		SHA256:        expectedSHA,
	})

	svc := New(newMemoryRepo(), toolsDir, slog.New(slog.DiscardHandler),
		WithRetry(3, 15*time.Millisecond),
	)

	_, err := svc.Download(context.Background(), domainbinaries.IDFFmpeg)
	if err != nil {
		t.Fatalf("Download failed: %v", err)
	}

	if atomic.LoadInt32(&attempts) != 3 {
		t.Errorf("expected exactly 3 attempts, got %d", attempts)
	}
}

// TestDownloader_CancellationCleanup tests that cancelling the download context cleans up .part and .part.meta.
func TestDownloader_CancellationCleanup(t *testing.T) {
	toolsDir := t.TempDir()

	hangCh := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "10000")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		select {
		case <-hangCh:
		case <-r.Context().Done():
		}
	}))
	defer server.Close()
	defer close(hangCh)

	setupTestDownloadSpec(t, domainbinaries.IDFFmpeg, domainbinaries.DownloadSpec{
		URL:           server.URL + "/ffmpeg",
		ArchiveFormat: domainbinaries.ArchiveFormatRaw,
		SHA256:        "abc123",
	})

	svc := New(newMemoryRepo(), toolsDir, slog.New(slog.DiscardHandler))

	ctx, cancel := context.WithCancel(context.Background())
	// Cancel quickly after download initiates
	time.AfterFunc(50*time.Millisecond, cancel)

	_, err := svc.Download(ctx, domainbinaries.IDFFmpeg)
	if err == nil {
		t.Fatalf("expected cancellation error, got nil")
	}

	partPath := filepath.Join(toolsDir, fmt.Sprintf(".%s-download.part", domainbinaries.IDFFmpeg))
	metaPath := filepath.Join(toolsDir, fmt.Sprintf(".%s-download.part.meta", domainbinaries.IDFFmpeg))
	if _, err := os.Stat(partPath); !os.IsNotExist(err) {
		t.Errorf(".part file was not cleaned up on cancellation")
	}
	if _, err := os.Stat(metaPath); !os.IsNotExist(err) {
		t.Errorf(".part.meta file was not cleaned up on cancellation")
	}
}

// fakePublisher captures published events for verification.
type fakePublisher struct {
	mu     sync.Mutex
	events map[string][]any
}

func newFakePublisher() *fakePublisher {
	return &fakePublisher{events: make(map[string][]any)}
}

func (p *fakePublisher) Publish(event string, data any) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.events[event] = append(p.events[event], data)
}

// TestDownloader_RealtimeEventBroadcast tests broadcasting of EventBinariesProgress.
func TestDownloader_RealtimeEventBroadcast(t *testing.T) {
	toolsDir := t.TempDir()

	content := []byte("ffmpeg-broadcast-test-binary")
	sum := sha256.Sum256(content)
	expectedSHA := hex.EncodeToString(sum[:])

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(content)))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(content)
	}))
	defer server.Close()

	setupTestDownloadSpec(t, domainbinaries.IDFFmpeg, domainbinaries.DownloadSpec{
		URL:           server.URL + "/ffmpeg",
		ArchiveFormat: domainbinaries.ArchiveFormatRaw,
		SHA256:        expectedSHA,
	})

	pub := newFakePublisher()
	svc := New(newMemoryRepo(), toolsDir, slog.New(slog.DiscardHandler),
		WithEventPublisher(pub),
	)

	_, err := svc.Download(context.Background(), domainbinaries.IDFFmpeg)
	if err != nil {
		t.Fatalf("Download failed: %v", err)
	}

	pub.mu.Lock()
	binEvents := pub.events[contracts.EventBinariesProgress]
	pub.mu.Unlock()

	if len(binEvents) == 0 {
		t.Fatalf("expected EventBinariesProgress events to be published, got 0")
	}

	// Verify the final event reached 100%
	last := binEvents[len(binEvents)-1].(contracts.BinaryProgress)
	if last.Percent != 100 {
		t.Errorf("expected final progress 100%%, got %v", last.Percent)
	}
	if last.ID != domainbinaries.IDFFmpeg {
		t.Errorf("expected binary ID %s, got %s", domainbinaries.IDFFmpeg, last.ID)
	}
}
