package binaries

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"app/internal/apperr"
	"app/internal/domain/binaries"
)

type memoryRepo struct {
	paths map[binaries.BinaryID]string
}

func newMemoryRepo() *memoryRepo {
	return &memoryRepo{paths: make(map[binaries.BinaryID]string)}
}

func (m *memoryRepo) GetCustomPath(ctx context.Context, id binaries.BinaryID) (string, error) {
	return m.paths[id], nil
}

func (m *memoryRepo) GetAllCustomPaths(ctx context.Context) (map[binaries.BinaryID]string, error) {
	cp := make(map[binaries.BinaryID]string)
	for k, v := range m.paths {
		cp[k] = v
	}
	return cp, nil
}

func (m *memoryRepo) SetCustomPath(ctx context.Context, id binaries.BinaryID, customPath string) error {
	m.paths[id] = customPath
	return nil
}

func TestResolveAndCustomPath(t *testing.T) {
	ctx := context.Background()
	repo := newMemoryRepo()
	svc := New(repo, t.TempDir(), slog.Default())

	// Initially missing or from system
	info, err := svc.Probe(ctx, binaries.IDFFmpeg)
	if err != nil {
		t.Fatalf("probe failed: %v", err)
	}
	if info.ID != binaries.IDFFmpeg {
		t.Fatalf("expected ID %s, got %s", binaries.IDFFmpeg, info.ID)
	}

	// Set invalid custom path
	info, err = svc.SetCustomPath(ctx, binaries.IDFFmpeg, "C:\\nonexistent\\path\\ffmpeg.exe")
	if err != nil {
		t.Fatalf("set custom path failed: %v", err)
	}
	if info.CustomPath != "C:\\nonexistent\\path\\ffmpeg.exe" {
		t.Fatalf("expected custom path to be recorded, got %q", info.CustomPath)
	}
}

func TestVersionExtraction(t *testing.T) {
	ffmpegSpec := binaries.Registry[binaries.IDFFmpeg]
	sampleFFmpeg := "ffmpeg version 7.1-essentials_build-www.gyan.dev Copyright (c) 2000-2024 the FFmpeg developers"
	v := ffmpegSpec.ExtractVersion(sampleFFmpeg)
	if v != "7.1-essentials_build-www.gyan.dev" {
		t.Fatalf("expected extracted ffmpeg version, got %q", v)
	}

	ffprobeSpec := binaries.Registry[binaries.IDFFprobe]
	sampleFFprobe := "ffprobe version 7.0.2 Copyright (c) 2007-2024 the FFmpeg developers"
	v = ffprobeSpec.ExtractVersion(sampleFFprobe)
	if v != "7.0.2" {
		t.Fatalf("expected extracted ffprobe version, got %q", v)
	}

	ytDlpSpec := binaries.Registry[binaries.IDYtDlp]
	sampleYtDlp := "2024.10.22"
	v = ytDlpSpec.ExtractVersion(sampleYtDlp)
	if v != "2024.10.22" {
		t.Fatalf("expected extracted yt-dlp version, got %q", v)
	}
}

func overrideDownloadSpecForTest(t *testing.T, id binaries.BinaryID, dl binaries.DownloadSpec) {
	t.Helper()
	orig := binaries.Registry[id]
	spec := orig
	spec.Downloads = map[string]binaries.DownloadSpec{
		fmt.Sprintf("%s_%s", runtime.GOOS, runtime.GOARCH): dl,
	}
	binaries.Registry[id] = spec
	t.Cleanup(func() { binaries.Registry[id] = orig })
}

func TestDownloadErrorHygiene(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	t.Run("tools dir creation failure", func(t *testing.T) {
		blocker := filepath.Join(t.TempDir(), "blocker")
		if err := os.WriteFile(blocker, []byte("not-a-dir"), 0o644); err != nil {
			t.Fatal(err)
		}
		toolsDir := filepath.Join(blocker, "tools")
		overrideDownloadSpecForTest(t, binaries.IDFFmpeg, binaries.DownloadSpec{
			URL:           "https://example.invalid/ffmpeg.zip",
			ArchiveFormat: binaries.ArchiveFormatZip,
			ExtractFile:   "bin/ffmpeg.exe",
		})

		svc := New(newMemoryRepo(), toolsDir, logger)
		_, err := svc.Download(context.Background(), binaries.IDFFmpeg)
		if !apperr.IsCode(err, apperr.CodeUnavailable) {
			t.Fatalf("want ERR_UNAVAILABLE, got %v", err)
		}
		var ae *apperr.Error
		if !errors.As(err, &ae) {
			t.Fatalf("want *apperr.Error, got %T", err)
		}
		if ae.Message != "could not prepare tools directory" {
			t.Fatalf("unexpected message: %q", ae.Message)
		}
		if strings.Contains(ae.Message, blocker) || strings.Contains(ae.Message, toolsDir) {
			t.Fatalf("message leaked local path: %q", ae.Message)
		}
	})

	t.Run("invalid request url", func(t *testing.T) {
		toolsDir := t.TempDir()
		overrideDownloadSpecForTest(t, binaries.IDFFmpeg, binaries.DownloadSpec{
			URL:           "://invalid-url",
			ArchiveFormat: binaries.ArchiveFormatZip,
			ExtractFile:   "bin/ffmpeg.exe",
		})

		svc := New(newMemoryRepo(), toolsDir, logger)
		_, err := svc.Download(context.Background(), binaries.IDFFmpeg)
		if !apperr.IsCode(err, apperr.CodeUnavailable) {
			t.Fatalf("want ERR_UNAVAILABLE, got %v", err)
		}
		var ae *apperr.Error
		if !errors.As(err, &ae) {
			t.Fatalf("want *apperr.Error, got %T", err)
		}
		if ae.Message != "could not create download request" {
			t.Fatalf("unexpected message: %q", ae.Message)
		}
	})

	t.Run("temp zip creation failure", func(t *testing.T) {
		toolsDir := t.TempDir()
		tempZipDir := filepath.Join(toolsDir, fmt.Sprintf(".%s-download.zip", binaries.IDFFmpeg))
		if err := os.MkdirAll(tempZipDir, 0o755); err != nil {
			t.Fatal(err)
		}

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("zip-bytes"))
		}))
		defer srv.Close()

		overrideDownloadSpecForTest(t, binaries.IDFFmpeg, binaries.DownloadSpec{
			URL:           srv.URL + "/ffmpeg.zip",
			ArchiveFormat: binaries.ArchiveFormatZip,
			ExtractFile:   "bin/ffmpeg.exe",
		})

		svc := New(newMemoryRepo(), toolsDir, logger)
		_, err := svc.Download(context.Background(), binaries.IDFFmpeg)
		if !apperr.IsCode(err, apperr.CodeUnavailable) {
			t.Fatalf("want ERR_UNAVAILABLE, got %v", err)
		}
		var ae *apperr.Error
		if !errors.As(err, &ae) {
			t.Fatalf("want *apperr.Error, got %T", err)
		}
		if ae.Message != "could not create temporary download file" {
			t.Fatalf("unexpected message: %q", ae.Message)
		}
		if strings.Contains(ae.Message, toolsDir) || strings.Contains(ae.Message, tempZipDir) {
			t.Fatalf("message leaked local path: %q", ae.Message)
		}
	})

	t.Run("unsupported archive format", func(t *testing.T) {
		toolsDir := t.TempDir()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("archive-bytes"))
		}))
		defer srv.Close()

		overrideDownloadSpecForTest(t, binaries.IDFFmpeg, binaries.DownloadSpec{
			URL:           srv.URL + "/ffmpeg.tar.gz",
			ArchiveFormat: "tar.gz",
			ExtractFile:   "bin/ffmpeg.exe",
		})

		svc := New(newMemoryRepo(), toolsDir, logger)
		_, err := svc.Download(context.Background(), binaries.IDFFmpeg)
		if !apperr.IsCode(err, apperr.CodeBadRequest) {
			t.Fatalf("want ERR_BAD_REQUEST, got %v", err)
		}
		var ae *apperr.Error
		if !errors.As(err, &ae) {
			t.Fatalf("want *apperr.Error, got %T", err)
		}
		if ae.Message != `unsupported archive format "tar.gz"` {
			t.Fatalf("unexpected message: %q", ae.Message)
		}
		if strings.Contains(ae.Message, toolsDir) {
			t.Fatalf("message leaked local path: %q", ae.Message)
		}
	})
}
