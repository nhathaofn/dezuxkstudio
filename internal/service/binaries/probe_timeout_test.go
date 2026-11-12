package binaries

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"app/internal/domain/binaries"
)

// slowTool writes a fake ffmpeg that answers after ~2s, like a cold start under antivirus scanning.
func slowTool(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if runtime.GOOS == "windows" {
		p := filepath.Join(dir, "ffmpeg.cmd")
		body := "@echo off\r\nping -n 3 127.0.0.1 >nul\r\necho ffmpeg version 7.1-test Copyright\r\n"
		if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
		return p
	}
	p := filepath.Join(dir, "ffmpeg")
	if err := os.WriteFile(p, []byte("#!/bin/sh\nsleep 2\necho 'ffmpeg version 7.1-test Copyright'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// A healthy but slow tool used to be reported "invalid" by the hard-coded 5s probe. The probe
// timeout is now configurable and the default leaves room for slow cold starts.
func TestProbeTimeoutIsConfigurable(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns a slow process")
	}
	exe := slowTool(t)
	ctx := context.Background()

	repo := newMemoryRepo()
	_ = repo.SetCustomPath(ctx, binaries.IDFFmpeg, exe)

	tight := New(repo, t.TempDir(), slog.New(slog.DiscardHandler), WithProbeTimeout(300*time.Millisecond))
	info, err := tight.Probe(ctx, binaries.IDFFmpeg)
	if err != nil {
		t.Fatal(err)
	}
	if info.Status != binaries.StatusInvalid {
		t.Fatalf("300ms probe should give up on a 2s tool, got %s", info.Status)
	}

	def := New(repo, t.TempDir(), slog.New(slog.DiscardHandler))
	info, err = def.Probe(ctx, binaries.IDFFmpeg)
	if err != nil {
		t.Fatal(err)
	}
	if info.Status != binaries.StatusAvailable {
		t.Fatalf("default probe timeout must tolerate a 2s tool, got %s (version %q)", info.Status, info.Version)
	}
}

func TestWithProbeTimeoutIgnoresNonPositive(t *testing.T) {
	s := New(newMemoryRepo(), t.TempDir(), slog.New(slog.DiscardHandler), WithProbeTimeout(0))
	if s.probeTimeout != DefaultProbeTimeout {
		t.Fatalf("got %v", s.probeTimeout)
	}
}
