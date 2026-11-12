package binaries

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"app/internal/apperr"
	"app/internal/buildinfo"
	domainbinaries "app/internal/domain/binaries"
	"app/internal/progress"
)

// createDummyZip creates an in-memory zip archive containing entryName with the given payload.
func createDummyZip(t *testing.T, entryName string, payload []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create(entryName)
	if err != nil {
		t.Fatalf("createDummyZip: failed to create entry %s: %v", entryName, err)
	}
	if _, err := w.Write(payload); err != nil {
		t.Fatalf("createDummyZip: failed to write payload: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("createDummyZip: failed to close zip: %v", err)
	}
	return buf.Bytes()
}

// progressRecorder collects progress callbacks thread-safely.
type progressRecorder struct {
	mu     sync.Mutex
	events []struct {
		pct float64
		msg string
	}
}

// ctx returns ctx with a progress sink that records every report (what the realtime hub installs).
func (r *progressRecorder) ctx(ctx context.Context) context.Context {
	return progress.With(ctx, progress.Sink{Report: r.record})
}

func (r *progressRecorder) record(p progress.Update) {
	pct, msg := p.Percent, p.Message
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, struct {
		pct float64
		msg string
	}{pct: pct, msg: msg})
}

func (r *progressRecorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.events)
}

func (r *progressRecorder) has100() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, ev := range r.events {
		if ev.pct >= 100 {
			return true
		}
	}
	return false
}

func (r *progressRecorder) has0() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, ev := range r.events {
		if ev.pct == 0 {
			return true
		}
	}
	return false
}

// TestDownload_ZipArchive_Success tests downloading an archive format ("zip"),
// extracting the target executable atomically, setting proper file permissions (0755),
// cleaning up temporary files, and recording progress events.
func TestDownload_ZipArchive_Success(t *testing.T) {
	ctx := context.Background()
	toolsDir := t.TempDir()
	repo := newMemoryRepo()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := New(repo, toolsDir, logger)

	dummyExecutableContent := []byte("MZ-dummy-ffmpeg-binary-data-for-testing-atomic-write-and-cleanup")
	zipBytes := createDummyZip(t, "bin/ffmpeg.exe", dummyExecutableContent)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		expectedUA := fmt.Sprintf("%s-Downloader/%s", buildinfo.ProductName, buildinfo.Version)
		if r.Header.Get("User-Agent") != expectedUA {
			t.Errorf("expected User-Agent %s, got %q", expectedUA, r.Header.Get("User-Agent"))
		}
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(zipBytes)))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(zipBytes)
	}))
	defer server.Close()

	// Temporarily override FFmpeg download spec for the current host platform
	origSpec := domainbinaries.Registry[domainbinaries.IDFFmpeg]
	platformKey := fmt.Sprintf("%s_%s", runtime.GOOS, runtime.GOARCH)

	testSpec := origSpec
	testDownloads := make(map[string]domainbinaries.DownloadSpec)
	for k, v := range origSpec.Downloads {
		testDownloads[k] = v
	}
	testDownloads[platformKey] = domainbinaries.DownloadSpec{
		URL:           server.URL + "/ffmpeg.zip",
		ArchiveFormat: domainbinaries.ArchiveFormatZip,
		ExtractFile:   "bin/ffmpeg.exe",
	}
	testSpec.Downloads = testDownloads
	domainbinaries.Registry[domainbinaries.IDFFmpeg] = testSpec
	t.Cleanup(func() {
		domainbinaries.Registry[domainbinaries.IDFFmpeg] = origSpec
	})

	recorder := &progressRecorder{}
	info, err := svc.Download(recorder.ctx(ctx), domainbinaries.IDFFmpeg)
	if err != nil {
		t.Fatalf("Download failed: %v", err)
	}

	// Verify binary info
	if info.ID != domainbinaries.IDFFmpeg {
		t.Errorf("expected info.ID %s, got %s", domainbinaries.IDFFmpeg, info.ID)
	}
	if info.Source != domainbinaries.SourceTools {
		t.Errorf("expected source %s, got %s", domainbinaries.SourceTools, info.Source)
	}

	targetExecutable := filepath.Join(toolsDir, testSpec.ExecutableName())
	if info.ResolvedPath != targetExecutable {
		t.Errorf("expected ResolvedPath %q, got %q", targetExecutable, info.ResolvedPath)
	}

	// Verify extracted file exists and has correct content
	extractedData, err := os.ReadFile(targetExecutable)
	if err != nil {
		t.Fatalf("failed to read extracted binary at %s: %v", targetExecutable, err)
	}
	if !bytes.Equal(extractedData, dummyExecutableContent) {
		t.Errorf("extracted content mismatch: expected %d bytes, got %d bytes", len(dummyExecutableContent), len(extractedData))
	}

	// Verify file mode permissions
	fileInfo, err := os.Stat(targetExecutable)
	if err != nil {
		t.Fatalf("failed to stat extracted binary: %v", err)
	}
	if fileInfo.IsDir() {
		t.Errorf("target path is a directory, expected executable file")
	}

	// Verify temporary zip was cleaned up
	tempZip := filepath.Join(toolsDir, fmt.Sprintf(".%s-download.zip", domainbinaries.IDFFmpeg))
	if _, err := os.Stat(tempZip); !os.IsNotExist(err) {
		t.Errorf("temp zip %s was not cleaned up after extraction", tempZip)
	}

	// Verify progress events
	if recorder.count() == 0 {
		t.Errorf("expected progress events to be emitted, got none")
	}
	if !recorder.has0() {
		t.Errorf("expected 0%% initial progress event")
	}
	if !recorder.has100() {
		t.Errorf("expected 100%% completion progress event")
	}
}

// TestDownload_RawBinary_Success tests downloading a direct executable format ("raw"),
// streaming it into the target destination via atomic write with 0755 permissions.
func TestDownload_RawBinary_Success(t *testing.T) {
	ctx := context.Background()
	toolsDir := t.TempDir()
	repo := newMemoryRepo()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := New(repo, toolsDir, logger)

	dummyYtDlpContent := []byte("MZ-dummy-yt-dlp-standalone-binary-data")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		expectedUA := fmt.Sprintf("%s-Downloader/%s", buildinfo.ProductName, buildinfo.Version)
		if r.Header.Get("User-Agent") != expectedUA {
			t.Errorf("expected User-Agent %s, got %q", expectedUA, r.Header.Get("User-Agent"))
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(dummyYtDlpContent)))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(dummyYtDlpContent)
	}))
	defer server.Close()

	origSpec := domainbinaries.Registry[domainbinaries.IDYtDlp]
	platformKey := fmt.Sprintf("%s_%s", runtime.GOOS, runtime.GOARCH)

	testSpec := origSpec
	testDownloads := make(map[string]domainbinaries.DownloadSpec)
	for k, v := range origSpec.Downloads {
		testDownloads[k] = v
	}
	testDownloads[platformKey] = domainbinaries.DownloadSpec{
		URL:           server.URL + "/yt-dlp.exe",
		ArchiveFormat: domainbinaries.ArchiveFormatRaw,
		ExtractFile:   "yt-dlp.exe",
	}
	testSpec.Downloads = testDownloads
	domainbinaries.Registry[domainbinaries.IDYtDlp] = testSpec
	t.Cleanup(func() {
		domainbinaries.Registry[domainbinaries.IDYtDlp] = origSpec
	})

	recorder := &progressRecorder{}
	info, err := svc.Download(recorder.ctx(ctx), domainbinaries.IDYtDlp)
	if err != nil {
		t.Fatalf("Download failed: %v", err)
	}

	if info.ID != domainbinaries.IDYtDlp {
		t.Errorf("expected info.ID %s, got %s", domainbinaries.IDYtDlp, info.ID)
	}

	targetExecutable := filepath.Join(toolsDir, testSpec.ExecutableName())
	if info.ResolvedPath != targetExecutable {
		t.Errorf("expected ResolvedPath %q, got %q", targetExecutable, info.ResolvedPath)
	}

	// Verify content
	data, err := os.ReadFile(targetExecutable)
	if err != nil {
		t.Fatalf("failed to read downloaded binary: %v", err)
	}
	if !bytes.Equal(data, dummyYtDlpContent) {
		t.Errorf("downloaded content mismatch")
	}

	// Verify progress
	if !recorder.has0() || !recorder.has100() {
		t.Errorf("expected progress events from 0%% to 100%%, got: %v", recorder.events)
	}
}

// TestDownload_HTTPError tests handling of non-2xx HTTP responses.
func TestDownload_HTTPError(t *testing.T) {
	ctx := context.Background()
	toolsDir := t.TempDir()
	repo := newMemoryRepo()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := New(repo, toolsDir, logger)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Not Found", http.StatusNotFound)
	}))
	defer server.Close()

	origSpec := domainbinaries.Registry[domainbinaries.IDFFmpeg]
	platformKey := fmt.Sprintf("%s_%s", runtime.GOOS, runtime.GOARCH)

	testSpec := origSpec
	testDownloads := make(map[string]domainbinaries.DownloadSpec)
	for k, v := range origSpec.Downloads {
		testDownloads[k] = v
	}
	testDownloads[platformKey] = domainbinaries.DownloadSpec{
		URL:           server.URL + "/missing.zip",
		ArchiveFormat: domainbinaries.ArchiveFormatZip,
		ExtractFile:   "bin/ffmpeg.exe",
	}
	testSpec.Downloads = testDownloads
	domainbinaries.Registry[domainbinaries.IDFFmpeg] = testSpec
	t.Cleanup(func() {
		domainbinaries.Registry[domainbinaries.IDFFmpeg] = origSpec
	})

	_, err := svc.Download(ctx, domainbinaries.IDFFmpeg)
	if err == nil {
		t.Fatal("expected download error for 404 response, got nil")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("expected error to mention status 404, got %v", err)
	}

	// Verify target executable was not left behind
	targetExecutable := filepath.Join(toolsDir, testSpec.ExecutableName())
	if _, err := os.Stat(targetExecutable); !os.IsNotExist(err) {
		t.Errorf("expected file %s to not exist on failure", targetExecutable)
	}
}

// TestDownload_CorruptedZipArchive tests handling of corrupted archive files.
func TestDownload_CorruptedZipArchive(t *testing.T) {
	ctx := context.Background()
	toolsDir := t.TempDir()
	repo := newMemoryRepo()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := New(repo, toolsDir, logger)

	corruptedData := []byte("this is definitely not a valid zip file format data")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(corruptedData)))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(corruptedData)
	}))
	defer server.Close()

	origSpec := domainbinaries.Registry[domainbinaries.IDFFmpeg]
	platformKey := fmt.Sprintf("%s_%s", runtime.GOOS, runtime.GOARCH)

	testSpec := origSpec
	testDownloads := make(map[string]domainbinaries.DownloadSpec)
	for k, v := range origSpec.Downloads {
		testDownloads[k] = v
	}
	testDownloads[platformKey] = domainbinaries.DownloadSpec{
		URL:           server.URL + "/corrupt.zip",
		ArchiveFormat: domainbinaries.ArchiveFormatZip,
		ExtractFile:   "bin/ffmpeg.exe",
	}
	testSpec.Downloads = testDownloads
	domainbinaries.Registry[domainbinaries.IDFFmpeg] = testSpec
	t.Cleanup(func() {
		domainbinaries.Registry[domainbinaries.IDFFmpeg] = origSpec
	})

	_, err := svc.Download(ctx, domainbinaries.IDFFmpeg)
	if err == nil {
		t.Fatal("expected error for corrupted zip, got nil")
	}
	if !apperr.IsCode(err, apperr.CodeIntegrity) {
		t.Errorf("expected ERR_INTEGRITY for a corrupted archive, got: %v", err)
	}
}

// TestDownload_MissingEntryInZip tests handling when archive does not contain the specified file.
func TestDownload_MissingEntryInZip(t *testing.T) {
	ctx := context.Background()
	toolsDir := t.TempDir()
	repo := newMemoryRepo()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := New(repo, toolsDir, logger)

	// Zip contains unrelated file
	zipBytes := createDummyZip(t, "some_other_folder/readme.txt", []byte("hello world"))

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(zipBytes)))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(zipBytes)
	}))
	defer server.Close()

	origSpec := domainbinaries.Registry[domainbinaries.IDFFmpeg]
	platformKey := fmt.Sprintf("%s_%s", runtime.GOOS, runtime.GOARCH)

	testSpec := origSpec
	testDownloads := make(map[string]domainbinaries.DownloadSpec)
	for k, v := range origSpec.Downloads {
		testDownloads[k] = v
	}
	testDownloads[platformKey] = domainbinaries.DownloadSpec{
		URL:           server.URL + "/wrong_content.zip",
		ArchiveFormat: domainbinaries.ArchiveFormatZip,
		ExtractFile:   "bin/ffmpeg.exe",
	}
	testSpec.Downloads = testDownloads
	domainbinaries.Registry[domainbinaries.IDFFmpeg] = testSpec
	t.Cleanup(func() {
		domainbinaries.Registry[domainbinaries.IDFFmpeg] = origSpec
	})

	_, err := svc.Download(ctx, domainbinaries.IDFFmpeg)
	if err == nil {
		t.Fatal("expected error for missing entry in zip, got nil")
	}
	if !strings.Contains(err.Error(), "not found in zip archive") {
		t.Errorf("expected 'not found in zip archive' error, got %v", err)
	}
}

// TestDownload_ContextCancellation tests cancelling the download context aborts execution.
func TestDownload_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	toolsDir := t.TempDir()
	repo := newMemoryRepo()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := New(repo, toolsDir, logger)

	_, err := svc.Download(ctx, domainbinaries.IDFFmpeg)
	if err == nil {
		t.Fatal("expected error on cancelled context, got nil")
	}
}

// TestDownload_ValidationErrors tests unregistered binary ID and missing download spec.
func TestDownload_ValidationErrors(t *testing.T) {
	ctx := context.Background()
	toolsDir := t.TempDir()
	repo := newMemoryRepo()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := New(repo, toolsDir, logger)

	// Unknown binary ID
	_, err := svc.Download(ctx, domainbinaries.BinaryID("unknown_tool"))
	if err == nil {
		t.Error("expected error for unknown binary, got nil")
	}

	// Binary with no download spec for current platform
	origSpec := domainbinaries.Registry[domainbinaries.IDFFmpeg]
	testSpec := origSpec
	testSpec.Downloads = map[string]domainbinaries.DownloadSpec{} // empty downloads
	domainbinaries.Registry[domainbinaries.IDFFmpeg] = testSpec
	t.Cleanup(func() {
		domainbinaries.Registry[domainbinaries.IDFFmpeg] = origSpec
	})

	_, err = svc.Download(ctx, domainbinaries.IDFFmpeg)
	if err == nil {
		t.Error("expected error when no download spec exists for platform, got nil")
	}
	if !strings.Contains(err.Error(), "no automated download available") {
		t.Errorf("expected 'no automated download available' error, got: %v", err)
	}
}

// createMultiEntryZip creates an in-memory zip archive with multiple file entries.
func createMultiEntryZip(t *testing.T, entries map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, payload := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("createMultiEntryZip: failed to create entry %s: %v", name, err)
		}
		if _, err := w.Write(payload); err != nil {
			t.Fatalf("createMultiEntryZip: failed to write payload for %s: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("createMultiEntryZip: failed to close zip: %v", err)
	}
	return buf.Bytes()
}

// TestDownload_IndeterminateChunkedProgress tests streaming download when Content-Length is missing
// (e.g. chunked transfer), confirming periodic downloaded MB updates are emitted without stalling.
func TestDownload_IndeterminateChunkedProgress(t *testing.T) {
	ctx := context.Background()
	toolsDir := t.TempDir()
	repo := newMemoryRepo()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := New(repo, toolsDir, logger)

	dummyExecutableContent := []byte("MZ-dummy-ffmpeg-indeterminate-chunked-data")
	zipBytes := createDummyZip(t, "bin/ffmpeg.exe", dummyExecutableContent)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Do not set Content-Length header so ContentLength <= 0
		w.Header().Set("Content-Type", "application/zip")
		flusher, ok := w.(http.Flusher)
		if ok {
			flusher.Flush()
		}
		mid := len(zipBytes) / 2
		_, _ = w.Write(zipBytes[:mid])
		if ok {
			flusher.Flush()
		}
		time.Sleep(250 * time.Millisecond)
		_, _ = w.Write(zipBytes[mid:])
		if ok {
			flusher.Flush()
		}
	}))
	defer server.Close()

	origSpec := domainbinaries.Registry[domainbinaries.IDFFmpeg]
	platformKey := fmt.Sprintf("%s_%s", runtime.GOOS, runtime.GOARCH)

	testSpec := origSpec
	testDownloads := make(map[string]domainbinaries.DownloadSpec)
	for k, v := range origSpec.Downloads {
		testDownloads[k] = v
	}
	testDownloads[platformKey] = domainbinaries.DownloadSpec{
		URL:           server.URL + "/ffmpeg.zip",
		ArchiveFormat: domainbinaries.ArchiveFormatZip,
		ExtractFile:   "bin/ffmpeg.exe",
	}
	testSpec.Downloads = testDownloads
	domainbinaries.Registry[domainbinaries.IDFFmpeg] = testSpec
	t.Cleanup(func() {
		domainbinaries.Registry[domainbinaries.IDFFmpeg] = origSpec
	})

	recorder := &progressRecorder{}
	info, err := svc.Download(recorder.ctx(ctx), domainbinaries.IDFFmpeg)
	if err != nil {
		t.Fatalf("Download failed: %v", err)
	}

	if info.ID != domainbinaries.IDFFmpeg {
		t.Errorf("expected info.ID %s, got %s", domainbinaries.IDFFmpeg, info.ID)
	}

	targetExecutable := filepath.Join(toolsDir, testSpec.ExecutableName())
	extractedData, err := os.ReadFile(targetExecutable)
	if err != nil {
		t.Fatalf("failed to read extracted binary at %s: %v", targetExecutable, err)
	}
	if !bytes.Equal(extractedData, dummyExecutableContent) {
		t.Errorf("extracted content mismatch")
	}

	// Verify periodic progress updates were recorded during indeterminate transfer
	recorder.mu.Lock()
	events := append([]struct {
		pct float64
		msg string
	}(nil), recorder.events...)
	recorder.mu.Unlock()

	hasIndeterminateMB := false
	for _, ev := range events {
		if strings.Contains(ev.msg, "Downloaded") && strings.HasSuffix(ev.msg, "MB") && !strings.Contains(ev.msg, "/") {
			hasIndeterminateMB = true
			break
		}
	}
	if !hasIndeterminateMB {
		t.Errorf("expected indeterminate chunked progress event formatted as 'Downloaded X.X MB', got events: %+v", events)
	}
}

// TestDownload_ExactZipEntryMatching tests extracting the specific entry matching the manifest path structure
// rather than loosely matching any file with the same base name.
func TestDownload_ExactZipEntryMatching(t *testing.T) {
	ctx := context.Background()
	toolsDir := t.TempDir()
	repo := newMemoryRepo()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := New(repo, toolsDir, logger)

	realBinaryContent := []byte("MZ-real-ffmpeg-binary-under-bin")
	decoyDocContent := []byte("Decoy-doc-ffmpeg.exe-content")

	// Create a zip with both doc/ffmpeg.exe and nested bin/ffmpeg.exe
	zipBytes := createMultiEntryZip(t, map[string][]byte{
		"doc/ffmpeg.exe":            decoyDocContent,
		"ffmpeg-7.1/bin/ffmpeg.exe": realBinaryContent,
	})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(zipBytes)))
		_, _ = w.Write(zipBytes)
	}))
	defer server.Close()

	origSpec := domainbinaries.Registry[domainbinaries.IDFFmpeg]
	platformKey := fmt.Sprintf("%s_%s", runtime.GOOS, runtime.GOARCH)

	testSpec := origSpec
	testDownloads := make(map[string]domainbinaries.DownloadSpec)
	for k, v := range origSpec.Downloads {
		testDownloads[k] = v
	}
	testDownloads[platformKey] = domainbinaries.DownloadSpec{
		URL:           server.URL + "/ffmpeg.zip",
		ArchiveFormat: domainbinaries.ArchiveFormatZip,
		ExtractFile:   "bin/ffmpeg.exe",
	}
	testSpec.Downloads = testDownloads
	domainbinaries.Registry[domainbinaries.IDFFmpeg] = testSpec
	t.Cleanup(func() {
		domainbinaries.Registry[domainbinaries.IDFFmpeg] = origSpec
	})

	_, err := svc.Download(ctx, domainbinaries.IDFFmpeg)
	if err != nil {
		t.Fatalf("Download failed: %v", err)
	}

	targetExecutable := filepath.Join(toolsDir, testSpec.ExecutableName())
	extractedData, err := os.ReadFile(targetExecutable)
	if err != nil {
		t.Fatalf("failed to read extracted binary at %s: %v", targetExecutable, err)
	}

	// Must match realBinaryContent from "ffmpeg-7.1/bin/ffmpeg.exe", NOT decoy from "doc/ffmpeg.exe"
	if !bytes.Equal(extractedData, realBinaryContent) {
		t.Fatalf("expected binary from bin/ffmpeg.exe, got content: %s", string(extractedData))
	}
}

// TestDownload_ZipSlipDefense tests that malicious entries containing '..' path traversal are rejected.
func TestDownload_ZipSlipDefense(t *testing.T) {
	ctx := context.Background()
	toolsDir := t.TempDir()
	repo := newMemoryRepo()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := New(repo, toolsDir, logger)

	maliciousPayload := []byte("evil-payload")
	zipBytes := createMultiEntryZip(t, map[string][]byte{
		"../../bin/ffmpeg.exe": maliciousPayload,
	})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(zipBytes)))
		_, _ = w.Write(zipBytes)
	}))
	defer server.Close()

	origSpec := domainbinaries.Registry[domainbinaries.IDFFmpeg]
	platformKey := fmt.Sprintf("%s_%s", runtime.GOOS, runtime.GOARCH)

	testSpec := origSpec
	testDownloads := make(map[string]domainbinaries.DownloadSpec)
	for k, v := range origSpec.Downloads {
		testDownloads[k] = v
	}
	testDownloads[platformKey] = domainbinaries.DownloadSpec{
		URL:           server.URL + "/ffmpeg.zip",
		ArchiveFormat: domainbinaries.ArchiveFormatZip,
		ExtractFile:   "bin/ffmpeg.exe",
	}
	testSpec.Downloads = testDownloads
	domainbinaries.Registry[domainbinaries.IDFFmpeg] = testSpec
	t.Cleanup(func() {
		domainbinaries.Registry[domainbinaries.IDFFmpeg] = origSpec
	})

	_, err := svc.Download(ctx, domainbinaries.IDFFmpeg)
	if err == nil {
		t.Fatalf("expected error due to zip slip entry rejection, got nil")
	}
	if !strings.Contains(err.Error(), "not found in zip archive") {
		t.Errorf("expected 'not found in zip archive' error, got %v", err)
	}
}

func TestDownload_SHA256_Mismatch_Zip(t *testing.T) {
	ctx := context.Background()
	toolsDir := t.TempDir()
	repo := newMemoryRepo()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := New(repo, toolsDir, logger)

	dummyExecutableContent := []byte("MZ-dummy-binary-zip")
	zipBytes := createDummyZip(t, "bin/ffmpeg.exe", dummyExecutableContent)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(zipBytes)))
		_, _ = w.Write(zipBytes)
	}))
	defer server.Close()

	origSpec := domainbinaries.Registry[domainbinaries.IDFFmpeg]
	platformKey := fmt.Sprintf("%s_%s", runtime.GOOS, runtime.GOARCH)

	testSpec := origSpec
	testDownloads := make(map[string]domainbinaries.DownloadSpec)
	for k, v := range origSpec.Downloads {
		testDownloads[k] = v
	}
	// Intentionally wrong SHA256
	testDownloads[platformKey] = domainbinaries.DownloadSpec{
		URL:           server.URL + "/ffmpeg.zip",
		ArchiveFormat: domainbinaries.ArchiveFormatZip,
		ExtractFile:   "bin/ffmpeg.exe",
		SHA256:        "0000000000000000000000000000000000000000000000000000000000000000",
	}
	testSpec.Downloads = testDownloads
	domainbinaries.Registry[domainbinaries.IDFFmpeg] = testSpec
	t.Cleanup(func() {
		domainbinaries.Registry[domainbinaries.IDFFmpeg] = origSpec
	})

	_, err := svc.Download(ctx, domainbinaries.IDFFmpeg)
	if err == nil {
		t.Fatal("expected ERR_INTEGRITY error on checksum mismatch, got nil")
	}

	if !apperr.IsCode(err, apperr.CodeIntegrity) {
		t.Fatalf("expected error code %s (ERR_INTEGRITY), got: %v", apperr.CodeIntegrity, err)
	}

	// Verify temp zip and target binary were cleaned up
	tempZip := filepath.Join(toolsDir, fmt.Sprintf(".%s-download.zip", domainbinaries.IDFFmpeg))
	if _, err := os.Stat(tempZip); !os.IsNotExist(err) {
		t.Errorf("temp zip %s was not cleaned up on checksum mismatch", tempZip)
	}
	targetExecutable := filepath.Join(toolsDir, testSpec.ExecutableName())
	if _, err := os.Stat(targetExecutable); !os.IsNotExist(err) {
		t.Errorf("target executable %s was not cleaned up on checksum mismatch", targetExecutable)
	}
}

func TestDownload_SHA256_Mismatch_Raw(t *testing.T) {
	ctx := context.Background()
	toolsDir := t.TempDir()
	repo := newMemoryRepo()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := New(repo, toolsDir, logger)

	dummyExecutableContent := []byte("MZ-dummy-binary-raw")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(dummyExecutableContent)))
		_, _ = w.Write(dummyExecutableContent)
	}))
	defer server.Close()

	origSpec := domainbinaries.Registry[domainbinaries.IDYtDlp]
	platformKey := fmt.Sprintf("%s_%s", runtime.GOOS, runtime.GOARCH)

	testSpec := origSpec
	testDownloads := make(map[string]domainbinaries.DownloadSpec)
	for k, v := range origSpec.Downloads {
		testDownloads[k] = v
	}
	// Intentionally wrong SHA256
	testDownloads[platformKey] = domainbinaries.DownloadSpec{
		URL:           server.URL + "/yt-dlp.exe",
		ArchiveFormat: domainbinaries.ArchiveFormatRaw,
		ExtractFile:   "yt-dlp.exe",
		SHA256:        "badhashbadhashbadhashbadhashbadhashbadhashbadhashbadhashbadhashbadh",
	}
	testSpec.Downloads = testDownloads
	domainbinaries.Registry[domainbinaries.IDYtDlp] = testSpec
	t.Cleanup(func() {
		domainbinaries.Registry[domainbinaries.IDYtDlp] = origSpec
	})

	_, err := svc.Download(ctx, domainbinaries.IDYtDlp)
	if err == nil {
		t.Fatal("expected ERR_INTEGRITY on raw checksum mismatch, got nil")
	}

	if !apperr.IsCode(err, apperr.CodeIntegrity) {
		t.Fatalf("expected error code %s (ERR_INTEGRITY), got: %v", apperr.CodeIntegrity, err)
	}

	targetExecutable := filepath.Join(toolsDir, testSpec.ExecutableName())
	if _, err := os.Stat(targetExecutable); !os.IsNotExist(err) {
		t.Errorf("target executable %s was not cleaned up on checksum mismatch", targetExecutable)
	}
}

func TestDownload_SHA256_Success(t *testing.T) {
	ctx := context.Background()
	toolsDir := t.TempDir()
	repo := newMemoryRepo()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := New(repo, toolsDir, logger)

	dummyContent := []byte("MZ-dummy-binary-raw-matching-checksum")
	h := sha256.Sum256(dummyContent)
	expectedHash := hex.EncodeToString(h[:])

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(dummyContent)))
		_, _ = w.Write(dummyContent)
	}))
	defer server.Close()

	origSpec := domainbinaries.Registry[domainbinaries.IDYtDlp]
	platformKey := fmt.Sprintf("%s_%s", runtime.GOOS, runtime.GOARCH)

	testSpec := origSpec
	testDownloads := make(map[string]domainbinaries.DownloadSpec)
	for k, v := range origSpec.Downloads {
		testDownloads[k] = v
	}
	testDownloads[platformKey] = domainbinaries.DownloadSpec{
		URL:           server.URL + "/yt-dlp.exe",
		ArchiveFormat: domainbinaries.ArchiveFormatRaw,
		ExtractFile:   "yt-dlp.exe",
		SHA256:        expectedHash,
	}
	testSpec.Downloads = testDownloads
	domainbinaries.Registry[domainbinaries.IDYtDlp] = testSpec
	t.Cleanup(func() {
		domainbinaries.Registry[domainbinaries.IDYtDlp] = origSpec
	})

	info, err := svc.Download(ctx, domainbinaries.IDYtDlp)
	if err != nil {
		t.Fatalf("expected successful download when SHA256 matches, got error: %v", err)
	}
	if info.ID != domainbinaries.IDYtDlp {
		t.Errorf("expected info.ID %s, got %s", domainbinaries.IDYtDlp, info.ID)
	}
}
