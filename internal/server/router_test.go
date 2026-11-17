package server

import (
	"bytes"
	"encoding/json"
	"image"
	"image/png"
	"io"
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

	"app/internal/realtime"
	"app/internal/server/handlers"
)

func uploadReq(t *testing.T, origin, token string) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	_ = png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 4, 4)))

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", "sample.png")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write(buf.Bytes()); err != nil {
		t.Fatal(err)
	}
	_ = mw.Close()

	req := httptest.NewRequest(http.MethodPost, PathMediaUpload, &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return req
}

func TestRouterMediaAuthAndCORS(t *testing.T) {
	hub, err := realtime.New(realtime.Options{
		AuthToken:            "top-secret",
		MaxConcurrentActions: 2,
		ClientBufferSize:     16,
		MaxMessageBytes:      1 << 20,
		ActionTimeout:        time.Second,
		LongActionTimeout:    time.Second,
		QueueTimeout:         time.Second,
		PingInterval:         time.Second,
		WriteTimeout:         time.Second,
		KeepAliveInterval:    100 * time.Millisecond,
		Logger:               slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = hub.Close(t.Context()) }()

	mediaDir := t.TempDir()
	h := NewRouter(RouterOptions{
		Hub:            hub,
		Mode:           "server",
		Version:        "test",
		StartedAt:      time.Now(),
		AuthToken:      "top-secret",
		AllowedOrigins: []string{"http://localhost:*"},
		MediaDir:       mediaDir,
		WSURL:          func() string { return "ws://127.0.0.1:8080/ws" },
		Timeouts:       handlers.Timeouts{Action: time.Second, LongAction: time.Second, PingInterval: time.Second},
	})

	// Preflight from allowed origin succeeds even before auth header check.
	pre := httptest.NewRequest(http.MethodOptions, PathMediaUpload, nil)
	pre.Header.Set("Origin", "http://localhost:5173")
	preRec := httptest.NewRecorder()
	h.ServeHTTP(preRec, pre)
	if preRec.Code != http.StatusNoContent {
		t.Fatalf("preflight expected 204, got %d", preRec.Code)
	}

	// Disallowed origin fails with 403 before upload handler.
	evilRec := httptest.NewRecorder()
	h.ServeHTTP(evilRec, uploadReq(t, "https://evil.example", "top-secret"))
	if evilRec.Code != http.StatusForbidden {
		t.Fatalf("disallowed origin expected 403, got %d", evilRec.Code)
	}

	// Missing token fails with 401.
	unauthRec := httptest.NewRecorder()
	h.ServeHTTP(unauthRec, uploadReq(t, "http://localhost:5173", ""))
	if unauthRec.Code != http.StatusUnauthorized {
		t.Fatalf("missing token expected 401, got %d", unauthRec.Code)
	}

	// Valid token uploads and allows download.
	okRec := httptest.NewRecorder()
	h.ServeHTTP(okRec, uploadReq(t, "http://localhost:5173", "top-secret"))
	if okRec.Code != http.StatusOK {
		t.Fatalf("authorized upload expected 200, got %d (%s)", okRec.Code, okRec.Body.String())
	}
	var up struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(okRec.Body.Bytes(), &up); err != nil {
		t.Fatal(err)
	}

	dlUnauth := httptest.NewRequest(http.MethodGet, PathMediaDownload+"?path="+url.QueryEscape(up.Path), nil)
	dlUnauthRec := httptest.NewRecorder()
	h.ServeHTTP(dlUnauthRec, dlUnauth)
	if dlUnauthRec.Code != http.StatusUnauthorized {
		t.Fatalf("download without token expected 401, got %d", dlUnauthRec.Code)
	}

	dlAuth := httptest.NewRequest(http.MethodGet, PathMediaDownload+"?path="+url.QueryEscape(up.Path), nil)
	dlAuth.Header.Set("Authorization", "Bearer top-secret")
	dlAuth.Header.Set("Origin", "http://localhost:5173")
	dlAuthRec := httptest.NewRecorder()
	h.ServeHTTP(dlAuthRec, dlAuth)
	if dlAuthRec.Code != http.StatusOK {
		t.Fatalf("authorized download expected 200, got %d", dlAuthRec.Code)
	}
}

func TestRouterDynamicMediaDirAndStagingDir(t *testing.T) {
	hub, err := realtime.New(realtime.Options{
		AuthToken:            "secret",
		MaxConcurrentActions: 2,
		ClientBufferSize:     16,
		MaxMessageBytes:      1 << 20,
		ActionTimeout:        time.Second,
		LongActionTimeout:    time.Second,
		QueueTimeout:         time.Second,
		PingInterval:         time.Second,
		WriteTimeout:         time.Second,
		KeepAliveInterval:    100 * time.Millisecond,
		Logger:               slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = hub.Close(t.Context()) }()

	tmpDir := t.TempDir()
	stagingDir := filepath.Join(tmpDir, "data", "staging")
	mediaDir1 := filepath.Join(tmpDir, "workspace1", "media")
	mediaDir2 := filepath.Join(tmpDir, "workspace2", "media")

	_ = os.MkdirAll(stagingDir, 0o755)
	_ = os.MkdirAll(mediaDir1, 0o755)
	_ = os.MkdirAll(mediaDir2, 0o755)

	currentMedia := mediaDir1
	h := NewRouter(RouterOptions{
		Hub:            hub,
		Mode:           "server",
		Version:        "test",
		StartedAt:      time.Now(),
		AuthToken:      "secret",
		AllowedOrigins: []string{"*"},
		StagingDir:     stagingDir,
		MediaDirFunc: func() string {
			return currentMedia
		},
		WSURL:    func() string { return "ws://127.0.0.1:8080/ws" },
		Timeouts: handlers.Timeouts{Action: time.Second, LongAction: time.Second, PingInterval: time.Second},
	})

	// 1. Upload must be placed into stagingDir
	okRec := httptest.NewRecorder()
	h.ServeHTTP(okRec, uploadReq(t, "http://localhost:5173", "secret"))
	if okRec.Code != http.StatusOK {
		t.Fatalf("upload failed: %d (%s)", okRec.Code, okRec.Body.String())
	}
	var up struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(okRec.Body.Bytes(), &up); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(filepath.Clean(up.Path), filepath.Clean(stagingDir)) {
		t.Fatalf("expected uploaded file to reside in stagingDir %s, got %s", stagingDir, up.Path)
	}

	// 2. Download from current media dir (mediaDir1)
	file1 := filepath.Join(mediaDir1, "sample1.png")
	var buf bytes.Buffer
	_ = png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 2, 2)))
	_ = os.WriteFile(file1, buf.Bytes(), 0o644)

	dlReq1 := httptest.NewRequest(http.MethodGet, PathMediaDownload+"?path=sample1.png", nil)
	dlReq1.Header.Set("Authorization", "Bearer secret")
	rec1 := httptest.NewRecorder()
	h.ServeHTTP(rec1, dlReq1)
	if rec1.Code != http.StatusOK {
		t.Fatalf("download from mediaDir1 failed: %d", rec1.Code)
	}

	// 3. Switch dynamic media dir to mediaDir2
	currentMedia = mediaDir2
	file2 := filepath.Join(mediaDir2, "sample2.png")
	_ = os.WriteFile(file2, buf.Bytes(), 0o644)

	dlReq2 := httptest.NewRequest(http.MethodGet, PathMediaDownload+"?path=sample2.png", nil)
	dlReq2.Header.Set("Authorization", "Bearer secret")
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, dlReq2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("download from dynamic mediaDir2 failed: %d", rec2.Code)
	}
}
