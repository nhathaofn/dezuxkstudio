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

	"app/internal/apperr"
	"app/internal/config"
	"app/internal/contracts"
	domainbinaries "app/internal/domain/binaries"
	"app/internal/engine"
	"app/internal/progress"
	"app/internal/service/agent"
	"app/internal/tools/watermark"
)

type fakeVideoBins struct {
	ffmpeg  string
	ffprobe string
}

func (f *fakeVideoBins) Resolve(_ context.Context, id domainbinaries.BinaryID) (string, domainbinaries.Source, string, error) {
	if id == domainbinaries.IDFFmpeg && f.ffmpeg != "" {
		return f.ffmpeg, domainbinaries.SourceCustom, f.ffmpeg, nil
	}
	if id == domainbinaries.IDFFprobe && f.ffprobe != "" {
		return f.ffprobe, domainbinaries.SourceCustom, f.ffprobe, nil
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
		VideoPath: engine.InputRef(src, engine.MediaVideo), Mode: "delogo", X: 10, Y: 20, Width: 100, Height: 40, OutputDir: dir,
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
		VideoPath: engine.InputRef(src, engine.MediaVideo), Mode: "delogo", X: 12, Y: 16, Width: 120, Height: 48, OutputDir: dir,
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
		VideoPath: engine.InputRef(src, engine.MediaVideo), Mode: "crop", CropEdge: "bottom", CropPx: 40, OutputDir: dir,
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

func TestVideoWatermarkRemoveDefaultsCustomConfigRunGraphAndServerGuard(t *testing.T) {
	base := t.TempDir()
	wsDir := filepath.Join(base, "ws")
	mediaDir := filepath.Join(base, "media")
	outsideDir := t.TempDir()
	_ = os.MkdirAll(wsDir, 0o755)
	_ = os.MkdirAll(mediaDir, 0o755)

	src := filepath.Join(mediaDir, "clip.mp4")
	outsideVid := filepath.Join(outsideDir, "secret.mp4")
	insideEnv := filepath.Join(mediaDir, ".env")
	_ = os.WriteFile(src, []byte("MP4SRC"), 0o600)
	_ = os.WriteFile(outsideVid, []byte("OUTSIDE"), 0o600)
	_ = os.WriteFile(insideEnv, []byte("SECRET=1"), 0o600)

	ffmpegExe, logPath := writeFakeVideoFFmpeg(t, base)
	probeJSON := `{"format":{"duration":"4.25"},"streams":[{"codec_type":"video","width":1920,"height":1080}]}`
	probeJSONPath := filepath.Join(base, "probe.json")
	_ = os.WriteFile(probeJSONPath, []byte(probeJSON), 0o600)
	var ffprobeExe string
	if runtime.GOOS == "windows" {
		ffprobeExe = filepath.Join(base, "ffprobe.cmd")
		_ = os.WriteFile(ffprobeExe, []byte("@echo off\r\ntype \""+probeJSONPath+"\"\r\n"), 0o700)
	} else {
		ffprobeExe = filepath.Join(base, "ffprobe.sh")
		_ = os.WriteFile(ffprobeExe, []byte("#!/bin/sh\ncat \""+probeJSONPath+"\"\n"), 0o755)
	}

	wmCfg := config.WatermarkConfig{
		VideoDefaultWidth:  220,
		VideoDefaultHeight: 88,
		VideoDefaultCropPx: 96,
	}
	node := watermark.NewVideoRemoveNode(watermark.Options{
		MediaDir:        mediaDir,
		WatermarkConfig: wmCfg,
		Binaries:        &fakeVideoBins{ffmpeg: ffmpegExe, ffprobe: ffprobeExe},
	})

	// 1. Real AccessPolicy guard blocks outside video_path with CodeUnauthorized AND rejects .env inside mediaDir
	policy := agent.NewAccessPolicy(filepath.Join(base, "data"), wsDir, mediaDir, filepath.Join(base, "profiles"), nil)
	guardedCtx := engine.WithFileGuard(context.Background(), policy.CheckTrustedFile)
	if _, err := node.Run(guardedCtx, mustJSON(t, map[string]any{"video_path": outsideVid})); err == nil || !apperr.IsCode(err, apperr.CodeUnauthorized) {
		t.Fatalf("expected CodeUnauthorized for outside video_path, got %v", err)
	}
	if _, err := node.Run(context.Background(), mustJSON(t, map[string]any{"video_path": insideEnv})); err == nil || !apperr.IsCode(err, apperr.CodeValidation) {
		t.Fatalf("expected CodeValidation for non-video .env input, got %v", err)
	}

	// 2. Omitted width/height/crop_px apply WatermarkConfig defaults AND populate Width/Height/DurationSec
	rawDelogo, err := node.Run(guardedCtx, json.RawMessage(`{"video_path":`+mustJSONString(t, src)+`}`))
	if err != nil {
		t.Fatalf("minimal delogo run: %v", err)
	}
	outDelogo := rawDelogo.(contracts.VideoWatermarkRemoveOutput)
	if outDelogo.Width != 1920 || outDelogo.Height != 1080 || outDelogo.DurationSec != 4.25 {
		t.Fatalf("expected probed Width/Height/DurationSec on output, got %+v", outDelogo)
	}

	// 3. RunGraph chains PortVideo output -> downstream PortVideo input cleanly
	reg := engine.NewRegistry()
	if err := reg.Register(node); err != nil {
		t.Fatal(err)
	}
	res, err := reg.RunGraph(guardedCtx, engine.Graph{
		Nodes: []engine.GraphNode{
			{ID: "wm1", Type: watermark.NodeTypeVideo, Params: mustJSON(t, map[string]any{"video_path": src})},
			{ID: "wm2", Type: watermark.NodeTypeVideo, Params: mustJSON(t, map[string]any{"mode": "crop"})},
		},
		Edges: []engine.Edge{
			{From: engine.Endpoint{Node: "wm1", Port: "video"}, To: engine.Endpoint{Node: "wm2", Port: "video_path"}},
		},
	})
	if err != nil || !res.Done() {
		t.Fatalf("RunGraph(video_watermark_remove -> video_watermark_remove) failed: err=%v res=%+v", err, res.Nodes)
	}

	argsBytes, _ := os.ReadFile(logPath)
	argsStr := string(argsBytes)
	if !strings.Contains(argsStr, "delogo=x=0:y=0:w=220:h=88") || !strings.Contains(argsStr, "crop=iw:ih-96") {
		t.Fatalf("expected custom WatermarkConfig defaults (220x88, crop 96) in FFmpeg args, got:\n%s", argsStr)
	}
}

func mustJSONString(t *testing.T, s string) string {
	t.Helper()
	return string(mustJSON(t, s))
}

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
