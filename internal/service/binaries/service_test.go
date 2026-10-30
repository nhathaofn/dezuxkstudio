package binaries

import (
	"context"
	"log/slog"
	"testing"

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
