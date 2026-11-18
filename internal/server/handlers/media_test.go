package handlers

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"app/internal/apperr"
	"app/internal/security"
	"app/internal/server/middleware"
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

func setupTestAsset(t *testing.T, mediaDir, filename string, data []byte) string {
	t.Helper()
	p := filepath.Join(mediaDir, filename)
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatalf("write test asset %s: %v", filename, err)
	}
	return p
}

func makeExpiredTicket(secret []byte, assetID, purpose string) string {
	claims := security.TicketClaims{
		AssetID:   assetID,
		Purpose:   purpose,
		ExpiresAt: time.Now().Unix() - 60,
		Nonce:     "1234567890abcdef",
	}
	payloadBytes, _ := json.Marshal(claims)
	payloadPart := base64.RawURLEncoding.EncodeToString(payloadBytes)

	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(payloadPart))
	sigPart := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))

	return payloadPart + "." + sigPart
}

func TestMediaDownloadRangeRequest(t *testing.T) {
	mediaDir := t.TempDir()
	data := []byte("0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ!@#$%^&*()_+-=[]{}|;':,./<>?`~0123456789")
	setupTestAsset(t, mediaDir, "sample.mp4", data)

	secret := []byte("media-signing-secret-key-32bytes")
	signer := security.NewTicketSigner(secret)
	assetID := "sample.mp4"

	ticket, err := signer.SignTicket(assetID, security.PurposeDownload, 10*time.Minute)
	if err != nil {
		t.Fatalf("sign ticket failed: %v", err)
	}

	handler := MediaDownload(mediaDir, WithTicketSigner(signer))

	// Sub-test 1: Middle range [10-25] -> 16 bytes
	req1 := httptest.NewRequest(http.MethodGet, "/api/media/download?asset_id="+url.QueryEscape(assetID)+"&ticket="+url.QueryEscape(ticket), nil)
	req1.Header.Set("Range", "bytes=10-25")
	rec1 := httptest.NewRecorder()
	handler.ServeHTTP(rec1, req1)

	if rec1.Code != http.StatusPartialContent {
		t.Fatalf("expected 206 Partial Content, got %d (%s)", rec1.Code, rec1.Body.String())
	}
	expectedCR1 := fmt.Sprintf("bytes 10-25/%d", len(data))
	if rec1.Header().Get("Content-Range") != expectedCR1 {
		t.Fatalf("expected Content-Range %s, got %q", expectedCR1, rec1.Header().Get("Content-Range"))
	}
	expectedBody1 := data[10:26]
	if !bytes.Equal(rec1.Body.Bytes(), expectedBody1) {
		t.Fatalf("body mismatch for range 10-25:\nwant: %s\ngot:  %s", expectedBody1, rec1.Body.Bytes())
	}

	// Sub-test 2: Open-ended range [50-] -> from 50 to end
	req2 := httptest.NewRequest(http.MethodGet, "/api/media/download?asset_id="+url.QueryEscape(assetID)+"&ticket="+url.QueryEscape(ticket), nil)
	req2.Header.Set("Range", "bytes=50-")
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusPartialContent {
		t.Fatalf("expected 206 Partial Content, got %d", rec2.Code)
	}
	expectedCR2 := fmt.Sprintf("bytes 50-%d/%d", len(data)-1, len(data))
	if rec2.Header().Get("Content-Range") != expectedCR2 {
		t.Fatalf("expected Content-Range %s, got %q", expectedCR2, rec2.Header().Get("Content-Range"))
	}
	expectedBody2 := data[50:]
	if !bytes.Equal(rec2.Body.Bytes(), expectedBody2) {
		t.Fatalf("body mismatch for range 50-:\nwant: %s\ngot:  %s", expectedBody2, rec2.Body.Bytes())
	}

	// Sub-test 3: Suffix range [-20] -> last 20 bytes
	req3 := httptest.NewRequest(http.MethodGet, "/api/media/download?asset_id="+url.QueryEscape(assetID)+"&ticket="+url.QueryEscape(ticket), nil)
	req3.Header.Set("Range", "bytes=-20")
	rec3 := httptest.NewRecorder()
	handler.ServeHTTP(rec3, req3)

	if rec3.Code != http.StatusPartialContent {
		t.Fatalf("expected 206 Partial Content, got %d", rec3.Code)
	}
	expectedCR3 := fmt.Sprintf("bytes %d-%d/%d", len(data)-20, len(data)-1, len(data))
	if rec3.Header().Get("Content-Range") != expectedCR3 {
		t.Fatalf("expected Content-Range %s, got %q", expectedCR3, rec3.Header().Get("Content-Range"))
	}
	expectedBody3 := data[len(data)-20:]
	if !bytes.Equal(rec3.Body.Bytes(), expectedBody3) {
		t.Fatalf("body mismatch for range -20:\nwant: %s\ngot:  %s", expectedBody3, rec3.Body.Bytes())
	}
}

func TestMediaDownloadExpiredTicket(t *testing.T) {
	mediaDir := t.TempDir()
	setupTestAsset(t, mediaDir, "sample.png", makeTestPNG(t))

	secret := []byte("media-signing-secret-key-32bytes")
	signer := security.NewTicketSigner(secret)
	assetID := "sample.png"

	expiredTicket := makeExpiredTicket(secret, assetID, security.PurposeDownload)

	handler := MediaDownload(mediaDir, WithTicketSigner(signer))

	req := httptest.NewRequest(http.MethodGet, "/api/media/download?asset_id="+url.QueryEscape(assetID)+"&ticket="+url.QueryEscape(expiredTicket), nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// Per requirement: Reject expired or invalid tickets with 401/403
	if rec.Code != http.StatusUnauthorized && rec.Code != http.StatusForbidden {
		t.Fatalf("expected 401 or 403 for expired ticket, got %d (%s)", rec.Code, rec.Body.String())
	}
}

func TestMediaDownloadTamperedTicket(t *testing.T) {
	mediaDir := t.TempDir()
	setupTestAsset(t, mediaDir, "sample.png", makeTestPNG(t))

	secret := []byte("media-signing-secret-key-32bytes")
	signer := security.NewTicketSigner(secret)
	assetID := "sample.png"

	validTicket, err := signer.SignTicket(assetID, security.PurposePreview, 10*time.Minute)
	if err != nil {
		t.Fatalf("sign ticket failed: %v", err)
	}

	handler := MediaDownload(mediaDir, WithTicketSigner(signer))

	// Test 1: Tampered signature
	parts := strings.Split(validTicket, ".")
	tamperedSig := parts[1][:len(parts[1])-2] + "XX"
	tamperedTicket := parts[0] + "." + tamperedSig

	req1 := httptest.NewRequest(http.MethodGet, "/api/media/download?asset_id="+url.QueryEscape(assetID)+"&ticket="+url.QueryEscape(tamperedTicket), nil)
	rec1 := httptest.NewRecorder()
	handler.ServeHTTP(rec1, req1)

	if rec1.Code != http.StatusUnauthorized && rec1.Code != http.StatusForbidden {
		t.Fatalf("expected 401 or 403 for tampered ticket signature, got %d (%s)", rec1.Code, rec1.Body.String())
	}

	// Test 2: Tampered payload (mismatched asset_id in claims)
	tamperedPayload := base64.RawURLEncoding.EncodeToString([]byte(`{"asset_id":"evil","purpose":"preview","exp":9999999999,"nonce":"1234"}`))
	tamperedPayloadTicket := tamperedPayload + "." + parts[1]

	req2 := httptest.NewRequest(http.MethodGet, "/api/media/download?asset_id="+url.QueryEscape(assetID)+"&ticket="+url.QueryEscape(tamperedPayloadTicket), nil)
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusUnauthorized && rec2.Code != http.StatusForbidden {
		t.Fatalf("expected 401 or 403 for tampered payload, got %d (%s)", rec2.Code, rec2.Body.String())
	}
}

func TestMediaDownloadTokenQueryParameterRejected(t *testing.T) {
	mediaDir := t.TempDir()
	setupTestAsset(t, mediaDir, "sample.png", makeTestPNG(t))

	secret := []byte("media-signing-secret-key-32bytes")
	signer := security.NewTicketSigner(secret)
	assetID := "sample.png"

	ticket, _ := signer.SignTicket(assetID, security.PurposeDownload, time.Hour)

	handler := MediaDownload(mediaDir, WithTicketSigner(signer), WithAuth("master-secret", false))

	// Rejection case 1: ?token= with asset_id
	req1 := httptest.NewRequest(http.MethodGet, "/api/media/download?asset_id="+url.QueryEscape(assetID)+"&token=master-secret", nil)
	rec1 := httptest.NewRecorder()
	handler.ServeHTTP(rec1, req1)

	if rec1.Code < 400 {
		t.Fatalf("expected rejection for ?token= query parameter, got %d", rec1.Code)
	}

	// Rejection case 2: ?token= combined with valid ticket
	req2 := httptest.NewRequest(http.MethodGet, "/api/media/download?asset_id="+url.QueryEscape(assetID)+"&ticket="+url.QueryEscape(ticket)+"&token=leak", nil)
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)

	if rec2.Code < 400 {
		t.Fatalf("expected rejection for ?token= query even with valid ticket, got %d", rec2.Code)
	}

	// Rejection case 3: ?token= combined with path
	req3 := httptest.NewRequest(http.MethodGet, "/api/media/download?path=sample.png&token=master-secret", nil)
	rec3 := httptest.NewRecorder()
	handler.ServeHTTP(rec3, req3)

	if rec3.Code < 400 {
		t.Fatalf("expected rejection for ?token= query on path download, got %d", rec3.Code)
	}
}

func TestMediaDownloadLoggerRedactsTicket(t *testing.T) {
	mediaDir := t.TempDir()
	setupTestAsset(t, mediaDir, "sample.png", makeTestPNG(t))

	secret := []byte("media-signing-secret-key-32bytes")
	signer := security.NewTicketSigner(secret)
	assetID := "sample.png"

	ticket, err := signer.SignTicket(assetID, security.PurposePreview, 10*time.Minute)
	if err != nil {
		t.Fatalf("sign ticket failed: %v", err)
	}

	var logBuf strings.Builder
	logger := slog.New(slog.NewTextHandler(&logBuf, nil))

	baseHandler := MediaDownload(mediaDir, WithTicketSigner(signer))
	loggedHandler := middleware.RequestLogger(logger)(baseHandler)

	req := httptest.NewRequest(http.MethodGet, "/api/media/download?asset_id="+url.QueryEscape(assetID)+"&ticket="+url.QueryEscape(ticket)+"&purpose=preview", nil)
	rec := httptest.NewRecorder()
	loggedHandler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("download expected 200, got %d (%s)", rec.Code, rec.Body.String())
	}

	logOutput := logBuf.String()
	if strings.Contains(logOutput, ticket) {
		t.Fatalf("logger leaked signed ticket string into logs: %s", logOutput)
	}
	if !strings.Contains(logOutput, "ticket=[REDACTED]") {
		t.Fatalf("logger did not contain redacted ticket placeholder: %s", logOutput)
	}
}

func TestMediaDownloadBearerAuthWithAssetID(t *testing.T) {
	mediaDir := t.TempDir()
	assetData := makeTestPNG(t)
	setupTestAsset(t, mediaDir, "asset123.png", assetData)

	resolver := AssetResolverFunc(func(ctx context.Context, id string) (string, string, error) {
		if id == "asset-custom-id" {
			return filepath.Join(mediaDir, "asset123.png"), mediaDir, nil
		}
		return "", "", os.ErrNotExist
	})

	handler := MediaDownload(mediaDir, WithAssetResolver(resolver), WithAuth("master-bearer-token", false))

	// Request with valid Bearer auth
	reqAuth := httptest.NewRequest(http.MethodGet, "/api/media/download?asset_id=asset-custom-id", nil)
	reqAuth.Header.Set("Authorization", "Bearer master-bearer-token")
	recAuth := httptest.NewRecorder()
	handler.ServeHTTP(recAuth, reqAuth)

	if recAuth.Code != http.StatusOK {
		t.Fatalf("expected 200 with valid Bearer auth, got %d (%s)", recAuth.Code, recAuth.Body.String())
	}
	if !bytes.Equal(recAuth.Body.Bytes(), assetData) {
		t.Fatal("content mismatch on Bearer download")
	}

	// Request with invalid Bearer auth
	reqBadAuth := httptest.NewRequest(http.MethodGet, "/api/media/download?asset_id=asset-custom-id", nil)
	reqBadAuth.Header.Set("Authorization", "Bearer invalid-token")
	recBadAuth := httptest.NewRecorder()
	handler.ServeHTTP(recBadAuth, reqBadAuth)

	if recBadAuth.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 with invalid Bearer auth, got %d", recBadAuth.Code)
	}

	// Request with missing Bearer auth
	reqNoAuth := httptest.NewRequest(http.MethodGet, "/api/media/download?asset_id=asset-custom-id", nil)
	recNoAuth := httptest.NewRecorder()
	handler.ServeHTTP(recNoAuth, reqNoAuth)

	if recNoAuth.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 with missing Bearer auth, got %d", recNoAuth.Code)
	}
}

func TestMediaDownloadPurposePreviewVsDownload(t *testing.T) {
	mediaDir := t.TempDir()
	setupTestAsset(t, mediaDir, "photo.jpg", []byte("fake-jpeg-data"))

	secret := []byte("media-signing-secret-key-32bytes")
	signer := security.NewTicketSigner(secret)
	assetID := "photo.jpg"

	previewTicket, _ := signer.SignTicket(assetID, security.PurposePreview, 10*time.Minute)
	downloadTicket, _ := signer.SignTicket(assetID, security.PurposeDownload, 10*time.Minute)

	handler := MediaDownload(mediaDir, WithTicketSigner(signer))

	// Preview ticket -> Content-Disposition: inline
	reqPrev := httptest.NewRequest(http.MethodGet, "/api/media/download?asset_id="+url.QueryEscape(assetID)+"&ticket="+url.QueryEscape(previewTicket), nil)
	recPrev := httptest.NewRecorder()
	handler.ServeHTTP(recPrev, reqPrev)

	if recPrev.Code != http.StatusOK {
		t.Fatalf("preview expected 200, got %d", recPrev.Code)
	}
	if !strings.HasPrefix(recPrev.Header().Get("Content-Disposition"), "inline") {
		t.Fatalf("preview expected inline disposition, got %q", recPrev.Header().Get("Content-Disposition"))
	}

	// Download ticket -> Content-Disposition: attachment
	reqDl := httptest.NewRequest(http.MethodGet, "/api/media/download?asset_id="+url.QueryEscape(assetID)+"&ticket="+url.QueryEscape(downloadTicket), nil)
	recDl := httptest.NewRecorder()
	handler.ServeHTTP(recDl, reqDl)

	if recDl.Code != http.StatusOK {
		t.Fatalf("download expected 200, got %d", recDl.Code)
	}
	if !strings.HasPrefix(recDl.Header().Get("Content-Disposition"), "attachment") {
		t.Fatalf("download expected attachment disposition, got %q", recDl.Header().Get("Content-Disposition"))
	}
}

type fakeAuthorizingResolver struct {
	allowedRoots []string
}

func (f *fakeAuthorizingResolver) ResolveAsset(_ context.Context, _ string) (string, string, error) {
	return "", "", errors.New("not implemented")
}

func (f *fakeAuthorizingResolver) IsPathAuthorized(target string) bool {
	cleanTarget := filepath.Clean(target)
	for _, root := range f.allowedRoots {
		cleanRoot := filepath.Clean(root)
		if strings.HasPrefix(strings.ToLower(cleanTarget), strings.ToLower(cleanRoot)) {
			return true
		}
	}
	return false
}

func TestMediaDownloadDynamicMediaDirAndAuthorizedRoots(t *testing.T) {
	tmpDir := t.TempDir()
	mediaDir1 := filepath.Join(tmpDir, "workspace1", "media")
	mediaDir2 := filepath.Join(tmpDir, "workspace2", "media")
	oldRootDir := filepath.Join(tmpDir, "old_workspace", "media")
	outsideDir := filepath.Join(tmpDir, "forbidden")

	for _, d := range []string{mediaDir1, mediaDir2, oldRootDir, outsideDir} {
		_ = os.MkdirAll(d, 0o755)
	}

	setupTestAsset(t, mediaDir1, "img1.png", []byte("img1-bytes"))
	setupTestAsset(t, mediaDir2, "img2.png", []byte("img2-bytes"))
	oldFile := setupTestAsset(t, oldRootDir, "old.png", []byte("old-bytes"))
	unauthorizedFile := setupTestAsset(t, outsideDir, "secret.png", []byte("secret-bytes"))

	currentMedia := mediaDir1
	mediaGetter := func() string {
		return currentMedia
	}

	authResolver := &fakeAuthorizingResolver{
		allowedRoots: []string{oldRootDir},
	}

	handler := MediaDownload(
		mediaDir1,
		WithMediaDirFunc(mediaGetter),
		WithAssetResolver(authResolver),
	)

	// 1. Download relative to mediaDir1
	req1 := httptest.NewRequest(http.MethodGet, "/api/media/download?path=img1.png", nil)
	rec1 := httptest.NewRecorder()
	handler.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusOK {
		t.Fatalf("expected 200 downloading from mediaDir1, got %d", rec1.Code)
	}

	// 2. Switch dynamic media dir to mediaDir2
	currentMedia = mediaDir2
	req2 := httptest.NewRequest(http.MethodGet, "/api/media/download?path=img2.png", nil)
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("expected 200 downloading from dynamic mediaDir2, got %d", rec2.Code)
	}

	// 3. Absolute path to oldRootDir authorized via IsPathAuthorized
	reqOld := httptest.NewRequest(http.MethodGet, "/api/media/download?path="+url.QueryEscape(oldFile), nil)
	recOld := httptest.NewRecorder()
	handler.ServeHTTP(recOld, reqOld)
	if recOld.Code != http.StatusOK {
		t.Fatalf("expected 200 for authorized old root path, got %d", recOld.Code)
	}

	// 4. Absolute path to outsideDir NOT authorized
	reqEvil := httptest.NewRequest(http.MethodGet, "/api/media/download?path="+url.QueryEscape(unauthorizedFile), nil)
	recEvil := httptest.NewRecorder()
	handler.ServeHTTP(recEvil, reqEvil)
	if recEvil.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for unauthorized path outside media dir, got %d", recEvil.Code)
	}
}

func TestMediaUploadAndStagingCleanupWithDedicatedStagingDir(t *testing.T) {
	tmpDir := t.TempDir()
	stagingDir := filepath.Join(tmpDir, "data", "staging")
	_ = os.MkdirAll(stagingDir, 0o755)

	pngData := makeTestPNG(t)
	uploadHandler := MediaUpload(stagingDir, 0)

	req := multipartUploadRequest(t, "file", "test_upload.png", pngData)
	rec := httptest.NewRecorder()
	uploadHandler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("upload status=%d body=%s", rec.Code, rec.Body.String())
	}

	var resp mediaUploadResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	// Verify file is placed inside stagingDir directly (stagingDir/<jobID>/test_upload.png)
	cleanStaging := filepath.Clean(stagingDir)
	cleanDest := filepath.Clean(resp.Path)
	if !strings.HasPrefix(strings.ToLower(cleanDest), strings.ToLower(cleanStaging)) {
		t.Fatalf("expected path inside stagingDir %s, got %s", cleanStaging, cleanDest)
	}
	// Verify no double "staging/staging"
	rel, err := filepath.Rel(cleanStaging, cleanDest)
	if err != nil || strings.HasPrefix(rel, "staging") {
		t.Fatalf("unexpected nested staging path: %s", rel)
	}

	// Test RunStagingCleanup directly on dedicated stagingDir
	now := time.Now()
	expiredJob := writeAgedStagingJob(t, stagingDir, "expired-job", "old.png", now.Add(-2*time.Hour))
	freshJob := writeAgedStagingJob(t, stagingDir, "fresh-job", "new.png", now)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		RunStagingCleanup(ctx, stagingDir, time.Hour, 15*time.Millisecond, nil)
	}()

	waitUntilRemoved(t, expiredJob)
	if _, err := os.Stat(freshJob); err != nil {
		t.Fatalf("fresh job directory must remain: %v", err)
	}
	cancel()
	<-done
}

func TestStagingCleanupWhere_ProtectsGuardedEntries(t *testing.T) {
	tmpDir := t.TempDir()
	stagingDir := filepath.Join(tmpDir, "data", "staging")
	_ = os.MkdirAll(stagingDir, 0o755)

	now := time.Now()
	expiredUpload := writeAgedStagingJob(t, stagingDir, "expired-upload", "old.png", now.Add(-48*time.Hour))
	expiredMediagen := writeAgedStagingJob(t, stagingDir, "mediagen", "long_job.bin", now.Add(-48*time.Hour))
	expiredLeased := writeAgedStagingJob(t, stagingDir, "leased-job", "active.bin", now.Add(-48*time.Hour))

	err := CleanupStagingDirWhere(stagingDir, 24*time.Hour, now, func(p string) bool {
		base := filepath.Base(filepath.Clean(p))
		return base == "mediagen" || base == "leased-job"
	})
	if err != nil {
		t.Fatalf("CleanupStagingDirWhere failed: %v", err)
	}

	if _, err := os.Stat(expiredUpload); !os.IsNotExist(err) {
		t.Fatalf("expected unguarded expired upload %q to be removed", expiredUpload)
	}
	if _, err := os.Stat(expiredMediagen); err != nil {
		t.Fatalf("expected guarded mediagen dir %q to remain: %v", expiredMediagen, err)
	}
	if _, err := os.Stat(expiredLeased); err != nil {
		t.Fatalf("expected guarded leased dir %q to remain: %v", expiredLeased, err)
	}
}

func TestMediaDownloadRejectsRestrictedFilesAndHonoursFileGuard(t *testing.T) {
	mediaDir := t.TempDir()
	for _, restricted := range []string{".env", "secrets.enc", "app.db", "credentials.json", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(mediaDir, restricted), []byte("sensitive"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(mediaDir, "ok.png"), makeTestPNG(t), 0o600); err != nil {
		t.Fatal(err)
	}

	h := MediaDownload(mediaDir)
	for _, restricted := range []string{".env", "secrets.enc", "app.db", "credentials.json", "notes.txt"} {
		req := httptest.NewRequest(http.MethodGet, "/api/media/download?path="+url.QueryEscape(restricted), nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("expected 403 Forbidden for restricted download %q, got %d (%s)", restricted, rec.Code, rec.Body.String())
		}
	}

	guarded := MediaDownload(mediaDir, WithFileGuard(func(path string, _ bool) error {
		if strings.HasSuffix(path, "ok.png") {
			return apperr.New(apperr.CodeUnauthorized, "blocked by policy")
		}
		return nil
	}))
	reqGuard := httptest.NewRequest(http.MethodGet, "/api/media/download?path=ok.png", nil)
	recGuard := httptest.NewRecorder()
	guarded.ServeHTTP(recGuard, reqGuard)
	if recGuard.Code != http.StatusForbidden {
		t.Fatalf("expected 403 when WithFileGuard denies path, got %d", recGuard.Code)
	}

	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := CleanupStagingDirWhereContext(canceledCtx, mediaDir, time.Hour, time.Now(), nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled from CleanupStagingDirWhereContext, got %v", err)
	}
}
