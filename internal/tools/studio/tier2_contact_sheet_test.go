package studio

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"app/internal/config"
	"app/internal/contracts"
	domainbinaries "app/internal/domain/binaries"
	domainstudio "app/internal/domain/studio"
)

type tier2FakeBinaries struct {
	paths map[domainbinaries.BinaryID]string
}

func (f *tier2FakeBinaries) Resolve(_ context.Context, id domainbinaries.BinaryID) (string, domainbinaries.Source, string, error) {
	p, ok := f.paths[id]
	if !ok {
		return "", domainbinaries.SourceNone, "", os.ErrNotExist
	}
	return p, domainbinaries.SourceCustom, p, nil
}

func TestParseVideoSignalsOutput(t *testing.T) {
	stderr := strings.Join([]string{
		"[blackdetect @ 0x1] black_start:0 black_end:0.25 black_duration:0.25",
		"[freezedetect @ 0x2] lavfi.freezedetect.freeze_start: 1.50",
		"[freezedetect @ 0x2] lavfi.freezedetect.freeze_duration: 0.80",
		"[freezedetect @ 0x2] lavfi.freezedetect.freeze_end: 2.30",
		"[freezedetect @ 0x2] lavfi.freezedetect.freeze_start: 4.50",
	}, "\n")
	stdout := strings.Join([]string{
		"frame:10   pts:2400    pts_time:1.000",
		"lavfi.scene_score=0.680",
		"frame:72   pts:17280   pts_time:3.000",
	}, "\n")

	sig := parseVideoSignalsOutput(stdout, stderr, 5.0)
	if len(sig.Blacks) != 1 || sig.Blacks[0].EndSec != 0.25 {
		t.Fatalf("unexpected blacks: %+v", sig.Blacks)
	}
	if len(sig.Freezes) != 2 || sig.Freezes[0].DurationSec != 0.8 || sig.Freezes[1].EndSec != 5.0 {
		t.Fatalf("unexpected freezes (including EOF close): %+v", sig.Freezes)
	}
	if len(sig.Cuts) != 2 || sig.Cuts[0].TimeSec != 1.0 || sig.Cuts[0].Score != 0.68 || sig.Cuts[1].TimeSec != 3.0 {
		t.Fatalf("unexpected cuts: %+v", sig.Cuts)
	}
}

func TestComposeContactSheetJPEGAndCaching(t *testing.T) {
	img1 := image.NewRGBA(image.Rect(0, 0, 640, 360))
	for y := 0; y < 360; y++ {
		for x := 0; x < 640; x++ {
			img1.Set(x, y, color.RGBA{R: 40, G: 120, B: 200, A: 255})
		}
	}
	tiles := []contactSheetTile{
		{Img: img1, TimeSec: 0.12, Signals: []string{"BLACK"}},
		{Img: img1, TimeSec: 1.50, Signals: []string{"CUT", "FREEZE"}},
		{Img: img1, TimeSec: 3.00},
		{Img: img1, TimeSec: 4.50},
	}

	raw, err := composeContactSheetJPEG(tiles, 1000, 85)
	if err != nil {
		t.Fatalf("composeContactSheetJPEG: %v", err)
	}
	cfg, fmtName, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil || fmtName != "jpeg" {
		t.Fatalf("expected valid JPEG, got fmt=%q err=%v", fmtName, err)
	}
	if cfg.Width > 1000 || cfg.Height > 1000 {
		t.Fatalf("contact sheet exceeded maxEdgePx=1000: %dx%d", cfg.Width, cfg.Height)
	}

	// Signal sidecar cache hit and invalidation when file size changes
	tmp := t.TempDir()
	vidPath := filepath.Join(tmp, "shot_01.mp4")
	if err := os.WriteFile(vidPath, []byte("abc"), 0o644); err != nil {
		t.Fatal(err)
	}
	st, _ := os.Stat(vidPath)
	cachePath := filepath.Join(tmp, "shot_01_signals.json")
	filterA := "blackdetect=d=0.100:pix_th=0.100"
	filterB := "blackdetect=d=0.100:pix_th=0.980"
	if err := writeCachedSignals(cachePath, filterA, st.Size(), st.ModTime().UnixNano(), contracts.VideoSignals{
		DurationSec: 4.0,
		Cuts:        []contracts.VideoSignalCut{{TimeSec: 2.0}},
	}); err != nil {
		t.Fatal(err)
	}
	if got, ok := readCachedSignals(context.Background(), cachePath, filterA, st.Size(), st.ModTime().UnixNano()); !ok || len(got.Cuts) != 1 {
		t.Fatalf("expected cache hit, got ok=%v sig=%+v", ok, got)
	}
	if _, ok := readCachedSignals(context.Background(), cachePath, filterB, st.Size(), st.ModTime().UnixNano()); ok {
		t.Fatal("expected cache miss when filter changes")
	}
	if _, ok := readCachedSignals(context.Background(), cachePath, filterA, st.Size()+10, st.ModTime().UnixNano()); ok {
		t.Fatal("expected cache miss when file size changes")
	}
}

func TestVideoContactSheetNodeEndToEndWithFakeBinaries(t *testing.T) {
	tmp := t.TempDir()
	outDir := filepath.Join(tmp, "thumbs")
	vidPath := filepath.Join(tmp, "clip.mp4")
	_ = os.WriteFile(vidPath, []byte("fake-mp4"), 0o644)

	// Prepare a valid 16x16 PNG sample to copy on frame extraction calls
	samplePNG := filepath.Join(tmp, "sample.png")
	im := image.NewRGBA(image.Rect(0, 0, 16, 16))
	f, _ := os.Create(samplePNG)
	_ = png.Encode(f, im)
	_ = f.Close()

	probeJSON := filepath.Join(tmp, "probe.json")
	_ = os.WriteFile(probeJSON, []byte(`{"format":{"duration":"4.0","format_name":"mp4"},"streams":[{"codec_type":"video","codec_name":"h264","width":640,"height":360,"r_frame_rate":"24/1"}]}`), 0o644)

	ffprobeExe := filepath.Join(tmp, "ffprobe.bat")
	_ = os.WriteFile(ffprobeExe, []byte("@echo off\r\ntype \""+probeJSON+"\"\r\n"), 0o755)

	ffmpegExe := filepath.Join(tmp, "ffmpeg.cmd")
	ffmpegBat := "@echo off\r\n" +
		":loop\r\n" +
		"if \"%~2\"==\"\" goto done\r\n" +
		"shift\r\n" +
		"goto loop\r\n" +
		":done\r\n" +
		"set \"LAST=%~1\"\r\n" +
		"if \"%LAST%\"==\"-\" (\r\n" +
		"  echo frame:12 pts:1200 pts_time:1.250\r\n" +
		"  echo [blackdetect @ 0] black_start:0 black_end:0.20 black_duration:0.20 1>&2\r\n" +
		"  exit /b 0\r\n" +
		")\r\n" +
		"copy /Y \"" + samplePNG + "\" \"%LAST%\" >nul\r\n"
	_ = os.WriteFile(ffmpegExe, []byte(ffmpegBat), 0o755)

	binRes := &tier2FakeBinaries{paths: map[domainbinaries.BinaryID]string{
		domainbinaries.IDFFmpeg:  ffmpegExe,
		domainbinaries.IDFFprobe: ffprobeExe,
	}}
	cfg := config.Default(config.ModeDesktop).Studio
	cfg.ContactSheetFramesPerSheet = 2
	cfg.ContactSheetMaxFramesPerShot = 4
	cfg.ContactSheetMaxEdgePx = 800

	node := NewVideoContactSheetNode(Options{
		Binaries:     binRes,
		StudioConfig: cfg,
		MediaDir:     tmp,
		WorkspaceDir: tmp,
	})
	rawIn, _ := json.Marshal(contracts.VideoContactSheetInput{
		VideoPath: contracts.VideoContactSheetInput{}.VideoPath,
		OutputDir: outDir,
	})
	_ = rawIn
	resAny, err := node.Run(context.Background(), mustMarshalJSON(t, map[string]any{
		"video_path": vidPath,
		"output_dir": outDir,
	}))
	if err != nil {
		t.Fatalf("VideoContactSheetNode.Run: %v", err)
	}
	out := resAny.(contracts.VideoContactSheetOutput)
	if len(out.SheetPaths) != 2 {
		t.Fatalf("expected 2 sheets (4 frames / 2 per sheet), got %d (%v)", len(out.SheetPaths), out.SheetPaths)
	}
	if len(out.Frames) != 4 || len(out.Signals.Blacks) != 1 || len(out.Signals.Cuts) != 1 {
		t.Fatalf("unexpected contact sheet output: frames=%d signals=%+v", len(out.Frames), out.Signals)
	}
	for _, p := range out.SheetPaths {
		if _, serr := os.Stat(p); serr != nil {
			t.Fatalf("sheet missing on disk: %s (%v)", p, serr)
		}
	}
}

func TestTimelineAutoAssembleOverrunPolicies(t *testing.T) {
	tmp := t.TempDir()
	vid1 := filepath.Join(tmp, "s1.mp4")
	aud1 := filepath.Join(tmp, "s1.wav")
	vid2 := filepath.Join(tmp, "s2.mp4")
	aud2 := filepath.Join(tmp, "s2.wav")
	for _, p := range []string{vid1, aud1, vid2, aud2} {
		_ = os.WriteFile(p, []byte("media"), 0o644)
	}

	// Fake ffprobe returning 4.0s for videos and 5.2s for s1.wav (2.0s for s2.wav)
	ffprobeExe := filepath.Join(tmp, "ffprobe.bat")
	bat := "@echo off\r\n" +
		"echo %* | findstr /I \"s1.wav\" >nul\r\n" +
		"if not errorlevel 1 (\r\n" +
		"  echo {\"format\":{\"duration\":\"5.2\",\"format_name\":\"wav\"},\"streams\":[{\"codec_type\":\"audio\",\"codec_name\":\"pcm_s16le\"}]}\r\n" +
		"  exit /b 0\r\n" +
		")\r\n" +
		"echo %* | findstr /I \"s2.wav\" >nul\r\n" +
		"if not errorlevel 1 (\r\n" +
		"  echo {\"format\":{\"duration\":\"2.0\",\"format_name\":\"wav\"},\"streams\":[{\"codec_type\":\"audio\",\"codec_name\":\"pcm_s16le\"}]}\r\n" +
		"  exit /b 0\r\n" +
		")\r\n" +
		"echo {\"format\":{\"duration\":\"4.0\",\"format_name\":\"mp4\"},\"streams\":[{\"codec_type\":\"video\",\"codec_name\":\"h264\",\"width\":1280,\"height\":720,\"r_frame_rate\":\"24/1\"}]}\r\n"
	_ = os.WriteFile(ffprobeExe, []byte(bat), 0o755)

	samplePNG := filepath.Join(tmp, "hold.png")
	im := image.NewRGBA(image.Rect(0, 0, 16, 16))
	f, _ := os.Create(samplePNG)
	_ = png.Encode(f, im)
	_ = f.Close()
	ffmpegExe := filepath.Join(tmp, "ffmpeg.cmd")
	_ = os.WriteFile(ffmpegExe, []byte("@echo off\r\n:loop\r\nif \"%~2\"==\"\" goto done\r\nshift\r\ngoto loop\r\n:done\r\ncopy /Y \""+samplePNG+"\" \"%~1\" >nul\r\n"), 0o755)

	shotsBytes, _ := json.Marshal([]domainstudio.Shot{
		{ID: "s1", Seq: 1, Title: "Shot 1", DurationSec: 4, VideoPath: vid1, AudioPath: aud1, NarrationText: "Line 1"},
		{ID: "s2", Seq: 2, Title: "Shot 2", DurationSec: 4, VideoPath: vid2, AudioPath: aud2, NarrationText: "Line 2"},
	})
	cfg := config.Default(config.ModeDesktop).Studio
	cfg.TimelineDefaultTransitionMs = 0
	opts := Options{
		Binaries: &tier2FakeBinaries{paths: map[domainbinaries.BinaryID]string{
			domainbinaries.IDFFprobe: ffprobeExe,
			domainbinaries.IDFFmpeg:  ffmpegExe,
		}},
		StudioConfig: cfg,
		MediaDir:     tmp,
		WorkspaceDir: tmp,
	}
	node := NewTimelineAutoAssembleNode(opts)

	// 1. Policy: warn -> emits narration_overrun + narration_source_collision
	resWarn, err := node.Run(context.Background(), mustMarshalJSON(t, map[string]any{
		"shots_json":     string(shotsBytes),
		"overrun_policy": "warn",
		"project_dir":    tmp,
	}))
	if err != nil {
		t.Fatalf("warn policy failed: %v", err)
	}
	outWarn := resWarn.(contracts.TimelineAutoAssembleOutput)
	if len(outWarn.Issues) < 2 {
		t.Fatalf("expected overrun + collision issues under warn policy, got %+v", outWarn.Issues)
	}

	// 2. Policy: speed_fit with max_speed_fit_ratio=1.35 -> 5.2 / 4.0 = 1.30 <= 1.35 -> resolved cleanly!
	resFit, err := node.Run(context.Background(), mustMarshalJSON(t, map[string]any{
		"shots_json":          string(shotsBytes),
		"overrun_policy":      "speed_fit",
		"max_speed_fit_ratio": 1.35,
		"project_dir":         tmp,
	}))
	if err != nil {
		t.Fatalf("speed_fit policy failed: %v", err)
	}
	outFit := resFit.(contracts.TimelineAutoAssembleOutput)
	var tlFit domainstudio.EditTimeline
	_ = json.Unmarshal([]byte(outFit.TimelineJSON), &tlFit)
	if tlFit.Tracks[1].Clips[0].Speed < 1.29 || tlFit.Tracks[1].Clips[0].Speed > 1.31 {
		t.Fatalf("expected speed ~1.30, got %v", tlFit.Tracks[1].Clips[0].Speed)
	}
	for _, iss := range outFit.Issues {
		if !iss.Resolved {
			t.Fatalf("expected all issues resolved when within max_speed_fit_ratio, got %+v", iss)
		}
	}

	// 3. Policy: freeze_last_frame -> inserts hold image clip and pushes s2 cursor to 5200ms
	resFreeze, err := node.Run(context.Background(), mustMarshalJSON(t, map[string]any{
		"shots_json":     string(shotsBytes),
		"overrun_policy": "freeze_last_frame",
		"project_dir":    tmp,
	}))
	if err != nil {
		t.Fatalf("freeze_last_frame policy failed: %v", err)
	}
	outFreeze := resFreeze.(contracts.TimelineAutoAssembleOutput)
	var tlFreeze domainstudio.EditTimeline
	_ = json.Unmarshal([]byte(outFreeze.TimelineJSON), &tlFreeze)
	if len(tlFreeze.Tracks[0].Clips) != 3 {
		t.Fatalf("expected 3 video clips (s1 + s1-hold + s2), got %d", len(tlFreeze.Tracks[0].Clips))
	}
	if tlFreeze.Tracks[0].Clips[2].TimelineStartMs != 5200 {
		t.Fatalf("expected s2 pushed to 5200ms after hold, got %d", tlFreeze.Tracks[0].Clips[2].TimelineStartMs)
	}

	// 4. Policy: freeze_last_frame with active xfade transition (400ms) keeps speech non-overlapping
	transMs := int64(400)
	resFreezeXfade, err := node.Run(context.Background(), mustMarshalJSON(t, map[string]any{
		"shots_json":     string(shotsBytes),
		"overrun_policy": "freeze_last_frame",
		"transition_ms":  transMs,
		"project_dir":    tmp,
	}))
	if err != nil {
		t.Fatalf("freeze_last_frame with transition failed: %v", err)
	}
	outXfade := resFreezeXfade.(contracts.TimelineAutoAssembleOutput)
	for _, iss := range outXfade.Issues {
		if iss.Code == "narration_source_collision" {
			t.Fatalf("unexpected speech collision with transition+freeze: %+v", iss)
		}
	}
	var tlXfade domainstudio.EditTimeline
	_ = json.Unmarshal([]byte(outXfade.TimelineJSON), &tlXfade)
	if len(tlXfade.Tracks[1].Clips) != 2 || tlXfade.Tracks[1].Clips[1].TimelineStartMs != 5200 {
		t.Fatalf("expected aud2 delayed past aud1 end (5200ms), got %+v", tlXfade.Tracks[1].Clips)
	}
}

func mustMarshalJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
