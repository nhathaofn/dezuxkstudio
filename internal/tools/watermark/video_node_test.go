package watermark_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"app/internal/contracts"
	domainbinaries "app/internal/domain/binaries"
	"app/internal/engine"
	"app/internal/progress"
	"app/internal/tools/watermark"
)

type fakeVideoBins struct {
	ffmpeg string
}

func (f *fakeVideoBins) Resolve(_ context.Context, id domainbinaries.BinaryID) (string, domainbinaries.Source, string, error) {
	if id == domainbinaries.IDFFmpeg {
		return f.ffmpeg, domainbinaries.SourceCustom, f.ffmpeg, nil
	}
	return "", "", "", errors.New("binary not found")
}

func writeFakeVideoFFmpeg(t *testing.T, dir string) (exePath, logPath string) {
	t.Helper()
	logPath = filepath.Join(dir, "vid_wm_args.log")
	if runtime.GOOS == "windows" {
		exePath = filepath.Join(dir, "ffmpeg.cmd")
		script := "@echo off\r\n" +
			"echo %*>> \"" + logPath + "\"\r\n" +
			"setlocal enabledelayedexpansion\r\n" +
			"set \"last=\"\r\n" +
			":loop\r\n" +
			"if \"%~1\"==\"\" goto done\r\n" +
			"set \"last=%~1\"\r\n" +
			"shift\r\n" +
			"goto loop\r\n" +
			":done\r\n" +
			"echo CLEANMP4> \"%last%\"\r\n"
		if err := os.WriteFile(exePath, []byte(script), 0o700); err != nil {
			t.Fatal(err)
		}
		return exePath, logPath
	}
	exePath = filepath.Join(dir, "ffmpeg")
	script := "#!/bin/sh\necho \"$@\" >> \"" + logPath + "\"\nlast=\"\"\nfor a in \"$@\"; do last=\"$a\"; done\necho CLEANMP4 > \"$last\"\n"
	if err := os.WriteFile(exePath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return exePath, logPath
}

func TestVideoWatermarkRemoveNodeSpecAndSiblingPreservation(t *testing.T) {
	imgSpec := watermark.NewNode(watermark.Options{}).Spec()
	vidSpec := watermark.NewVideoNode(watermark.Options{}).Spec()
	if err := engine.ValidateSpec(imgSpec); err != nil {
		t.Fatalf("ValidateSpec(image watermark): %v", err)
	}
	if err := engine.ValidateSpec(vidSpec); err != nil {
		t.Fatalf("ValidateSpec(video watermark): %v", err)
	}
	if imgSpec.Type != "tool.watermark_remover" || vidSpec.Type != "tool.video_watermark_remove" {
		t.Fatalf("unexpected specs: img=%s vid=%s", imgSpec.Type, vidSpec.Type)
	}
}

func TestVideoWatermarkRemoveDelogoCropAndGuardSanitization(t *testing.T) {
	dir := t.TempDir()
	secretParent := filepath.Join(dir, "private-client-folder")
	_ = os.MkdirAll(secretParent, 0o750)
	src := filepath.Join(secretParent, "clip.mp4")
	_ = os.WriteFile(src, []byte("MP4SRC"), 0o600)

	ffmpegExe, logPath := writeFakeVideoFFmpeg(t, dir)
	node := watermark.NewVideoNode(watermark.Options{
		MediaDir: dir,
		Binaries: &fakeVideoBins{ffmpeg: ffmpegExe},
	})

	// FileGuard denial must sanitize parent folder name out of error
	deniedCtx := engine.WithFileGuard(context.Background(), func(p string, _ bool) error {
		return errors.New("cannot read " + p)
	})
	_, err := node.Run(deniedCtx, mustJSON(t, contracts.VideoWatermarkRemoveInput{
		VideoPath: src, Mode: "delogo", X: 10, Y: 20, Width: 100, Height: 40, OutputDir: dir,
	}))
	if err == nil || strings.Contains(err.Error(), "private-client-folder") {
		t.Fatalf("expected sanitized FileGuard error hiding parent dir, got: %v", err)
	}

	// Delogo success preserving audio & reporting -progress pipe:1
	var updates []progress.Update
	pCtx := progress.With(context.Background(), progress.Sink{
		Report: func(u progress.Update) { updates = append(updates, u) },
	})
	rawOut, err := node.Run(pCtx, mustJSON(t, contracts.VideoWatermarkRemoveInput{
		VideoPath: src, Mode: "delogo", X: 12, Y: 16, Width: 120, Height: 48, OutputDir: dir,
	}))
	if err != nil {
		t.Fatalf("delogo run: %v", err)
	}
	out := rawOut.(contracts.VideoWatermarkRemoveOutput)
	if out.Path == "" {
		t.Fatal("expected output path")
	}
	if len(updates) == 0 {
		t.Fatal("expected progress.Report updates during tool.video_watermark_remove")
	}

	// Crop mode
	_, err = node.Run(context.Background(), mustJSON(t, contracts.VideoWatermarkRemoveInput{
		VideoPath: src, Mode: "crop", CropEdge: "bottom", CropPx: 40, OutputDir: dir,
	}))
	if err != nil {
		t.Fatalf("crop run: %v", err)
	}

	argsBytes, _ := os.ReadFile(logPath)
	argsStr := string(argsBytes)
	if !strings.Contains(argsStr, "delogo=x=12:y=16:w=120:h=48") || !strings.Contains(argsStr, "-c:a copy") || !strings.Contains(argsStr, "-progress pipe:1") || !strings.Contains(argsStr, "crop=iw:ih-40") {
		t.Fatalf("unexpected ffmpeg filter args:\n%s", argsStr)
	}
}

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
