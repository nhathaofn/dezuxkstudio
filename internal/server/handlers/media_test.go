package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func makeTestPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

func multipartUploadRequest(t *testing.T, field, filename string, data []byte) *http.Request {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile(field, filename)
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := fw.Write(data); err != nil {
		t.Fatalf("write form file: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/media/upload", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return req
}

func TestMediaUploadAndDownloadRoundTrip(t *testing.T) {
	mediaDir := t.TempDir()
	pngData := makeTestPNG(t)

	uploadHandler := MediaUpload(mediaDir, 0)
	req := multipartUploadRequest(t, "file", `../../nested/evil\photo.html`, pngData)
	rec := httptest.NewRecorder()
	uploadHandler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("upload status=%d body=%s", rec.Code, rec.Body.String())
	}

	var resp mediaUploadResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Name != "photo.png" {
		t.Fatalf("expected sanitized canonical name photo.png, got %q", resp.Name)
	}
	if !strings.Contains(filepath.ToSlash(resp.Path), "/staging/") {
		t.Fatalf("expected staging subdir path, got %q", resp.Path)
	}
	diskBytes, err := os.ReadFile(resp.Path)
	if err != nil {
		t.Fatalf("read staged file: %v", err)
	}
	if !bytes.Equal(diskBytes, pngData) {
		t.Fatal("staged bytes mismatch")
	}

	// Download using exact staged path and relative path inside mediaDir.
	downloadHandler := MediaDownload(mediaDir)
	for _, qPath := range []string{resp.Path, strings.TrimPrefix(filepath.ToSlash(strings.TrimPrefix(resp.Path, mediaDir)), "/")} {
		dlReq := httptest.NewRequest(http.MethodGet, "/api/media/download?path="+url.QueryEscape(qPath), nil)
		dlRec := httptest.NewRecorder()
		downloadHandler.ServeHTTP(dlRec, dlReq)
		if dlRec.Code != http.StatusOK {
			t.Fatalf("download %q status=%d body=%s", qPath, dlRec.Code, dlRec.Body.String())
		}
		if dlRec.Header().Get("Content-Type") != "image/png" {
			t.Fatalf("content-type = %q", dlRec.Header().Get("Content-Type"))
		}
		if dlRec.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Fatalf("missing nosniff header")
		}
		if !strings.Contains(dlRec.Header().Get("Content-Disposition"), "photo.png") {
			t.Fatalf("content-disposition = %q", dlRec.Header().Get("Content-Disposition"))
		}
		if !bytes.Equal(dlRec.Body.Bytes(), pngData) {
			t.Fatal("downloaded bytes mismatch")
		}
	}

	// Video and audio outputs saved in mediaDir must be served with media MIME types.
	for name, wantMIME := range map[string]string{
		"generated/videos/clip.mp4": "video/mp4",
		"generated/tts/speech.wav":  "audio/wav",
		"generated/tts/speech.mp3":  "audio/mpeg",
	} {
		full := filepath.Join(mediaDir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("test-media-bytes"), 0o600); err != nil {
			t.Fatal(err)
		}
		dlReq := httptest.NewRequest(http.MethodGet, "/api/media/download?path="+url.QueryEscape(name), nil)
		dlRec := httptest.NewRecorder()
		downloadHandler.ServeHTTP(dlRec, dlReq)
		if dlRec.Code != http.StatusOK {
			t.Fatalf("%s: status=%d body=%s", name, dlRec.Code, dlRec.Body.String())
		}
		if got := dlRec.Header().Get("Content-Type"); got != wantMIME {
			t.Errorf("%s: Content-Type=%q, want %q", name, got, wantMIME)
		}
	}
}

func TestMediaUploadRejectsInvalidInputs(t *testing.T) {
	mediaDir := t.TempDir()

	// Reject unsupported MIME type (e.g. HTML disguised as PNG).
	h := MediaUpload(mediaDir, 4096)
	req := multipartUploadRequest(t, "file", "xss.png", []byte("<html><script>alert(1)</script></html>"))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("html payload: expected 415, got %d (%s)", rec.Code, rec.Body.String())
	}

	// Reject oversized upload cleanly and avoid leaving partial directories.
	oversized := bytes.Repeat([]byte("A"), 8192)
	reqBig := multipartUploadRequest(t, "file", "big.png", oversized)
	recBig := httptest.NewRecorder()
	h.ServeHTTP(recBig, reqBig)
	if recBig.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized upload: expected 413, got %d", recBig.Code)
	}

	// Reject empty upload.
	reqEmpty := multipartUploadRequest(t, "file", "empty.png", nil)
	recEmpty := httptest.NewRecorder()
	h.ServeHTTP(recEmpty, reqEmpty)
	if recEmpty.Code != http.StatusBadRequest {
		t.Fatalf("empty upload: expected 400, got %d", recEmpty.Code)
	}
}

func TestMediaDownloadBlocksTraversalAndOutsidePaths(t *testing.T) {
	root := t.TempDir()
	mediaDir := filepath.Join(root, "media")
	if err := os.MkdirAll(filepath.Join(mediaDir, MediaStagingSubdir), 0o700); err != nil {
		t.Fatal(err)
	}
	outsideFile := filepath.Join(root, "secret.txt")
	if err := os.WriteFile(outsideFile, []byte("top-secret"), 0o600); err != nil {
		t.Fatal(err)
	}

	h := MediaDownload(mediaDir)
	for _, tc := range []struct {
		name string
		path string
		code int
	}{
		{"outside-abs", outsideFile, http.StatusForbidden},
		{"traversal", "../secret.txt", http.StatusForbidden},
		{"media-root-dir", mediaDir, http.StatusForbidden},
		{"missing", "staging/does-not-exist.png", http.StatusNotFound},
	} {
		req := httptest.NewRequest(http.MethodGet, "/api/media/download?path="+url.QueryEscape(tc.path), nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != tc.code {
			t.Errorf("%s: got status %d, want %d (body=%s)", tc.name, rec.Code, tc.code, rec.Body.String())
		}
	}
}

func TestCleanupStagingDirRespectsLatestFileModTime(t *testing.T) {
	staging := filepath.Join(t.TempDir(), MediaStagingSubdir)
	oldDir := filepath.Join(staging, "old-job")
	freshDir := filepath.Join(staging, "fresh-job")
	for _, d := range []string{oldDir, freshDir} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}

	now := time.Now()
	oldTime := now.Add(-48 * time.Hour)
	oldFile := filepath.Join(oldDir, "a.png")
	if err := os.WriteFile(oldFile, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	_ = os.Chtimes(oldFile, oldTime, oldTime)
	_ = os.Chtimes(oldDir, oldTime, oldTime)

	// Older input file alongside a recently produced _nowm output file must stay alive.
	freshOldInput := filepath.Join(freshDir, "b.png")
	freshOutput := filepath.Join(freshDir, "b_nowm.png")
	if err := os.WriteFile(freshOldInput, []byte("in"), 0o600); err != nil {
		t.Fatal(err)
	}
	_ = os.Chtimes(freshOldInput, oldTime, oldTime)
	_ = os.Chtimes(freshDir, oldTime, oldTime)
	if err := os.WriteFile(freshOutput, []byte("out"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := CleanupStagingDir(staging, 24*time.Hour, now); err != nil {
		t.Fatalf("cleanup failed: %v", err)
	}
	if _, err := os.Stat(oldDir); !os.IsNotExist(err) {
		t.Errorf("expired staging dir should have been removed")
	}
	if _, err := os.Stat(freshOutput); err != nil {
		t.Errorf("active staging dir with fresh file must be kept: %v", err)
	}
}

func writeAgedStagingJob(t *testing.T, stagingRoot, jobName, fileName string, modTime time.Time) string {
	t.Helper()
	jobDir := filepath.Join(stagingRoot, jobName)
	if err := os.MkdirAll(jobDir, 0o700); err != nil {
		t.Fatal(err)
	}
	filePath := filepath.Join(jobDir, fileName)
	if err := os.WriteFile(filePath, []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(filePath, modTime, modTime); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(jobDir, modTime, modTime); err != nil {
		t.Fatal(err)
	}
	return jobDir
}

func waitUntilRemoved(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("expected %s to be removed before deadline", path)
}

func TestMediaUploadFallbackForNonPositiveMaxBytes(t *testing.T) {
	mediaDir := t.TempDir()
	h := MediaUpload(mediaDir, -1)
	req := multipartUploadRequest(t, "file", "sample.png", makeTestPNG(t))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("upload with negative maxBytes fallback: status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestRunStagingCleanupUsesConfiguredTTLAndInterval(t *testing.T) {
	mediaDir := t.TempDir()
	staging := filepath.Join(mediaDir, MediaStagingSubdir)
	now := time.Now()
	expired := writeAgedStagingJob(t, staging, "expired-job", "a.png", now.Add(-2*time.Hour))
	fresh := writeAgedStagingJob(t, staging, "fresh-job", "b.png", now)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		RunStagingCleanup(ctx, mediaDir, time.Hour, 15*time.Millisecond, nil)
	}()

	waitUntilRemoved(t, expired)
	if _, err := os.Stat(fresh); err != nil {
		cancel()
		<-done
		t.Fatalf("fresh staging dir must remain: %v", err)
	}

	// Verify the periodic ticker sweeps newly expired entries at the configured interval.
	laterExpired := writeAgedStagingJob(t, staging, "later-expired", "c.png", now.Add(-2*time.Hour))
	waitUntilRemoved(t, laterExpired)

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("RunStagingCleanup did not stop on context cancellation")
	}
}

func TestRunStagingCleanupFallbackDefaults(t *testing.T) {
	mediaDir := t.TempDir()
	staging := filepath.Join(mediaDir, MediaStagingSubdir)
	now := time.Now()
	expired := writeAgedStagingJob(t, staging, "over-24h", "old.png", now.Add(-48*time.Hour))
	kept := writeAgedStagingJob(t, staging, "under-24h", "recent.png", now.Add(-12*time.Hour))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		RunStagingCleanup(ctx, mediaDir, 0, 0, nil)
	}()

	waitUntilRemoved(t, expired)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("RunStagingCleanup did not stop on context cancellation")
	}

	if _, err := os.Stat(kept); err != nil {
		t.Fatalf("staging dir younger than default 24h TTL must be kept: %v", err)
	}
}
