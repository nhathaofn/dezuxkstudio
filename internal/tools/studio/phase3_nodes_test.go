package studio

import (
	"archive/zip"
	"context"
	"encoding/binary"
	"encoding/json"
	"encoding/xml"
	"errors"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"app/internal/apperr"
	"app/internal/config"
	"app/internal/contracts"
	domainbinaries "app/internal/domain/binaries"
	domainstudio "app/internal/domain/studio"
	"app/internal/engine"
	"app/internal/progress"
	"app/internal/service/agent"
)

func writePhase3FakeBinaries(t *testing.T, dir string, pcmBytes []byte) (ffprobeExe, ffmpegExe, argsLog string) {
	t.Helper()
	argsLog = filepath.Join(dir, "ffmpeg_p3_args.log")

	// Build native fake binaries inside build/.gotmp (trusted under Windows Smart App Control
	// and avoids cmd.exe pipe-splitting on filter_complex arguments containing '|').
	tmpRoot := filepath.Join("..", "..", "..", "build", ".gotmp")
	if err := os.MkdirAll(tmpRoot, 0o755); err != nil {
		tmpRoot = dir
	}
	binDir, err := os.MkdirTemp(tmpRoot, "p3fake-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(binDir) })

	probeJSONPath := filepath.Join(binDir, "ffprobe_p3.json")
	pcmPath := filepath.Join(binDir, "sample.f32le")
	argsLogPtrPath := filepath.Join(binDir, "args_log.txt")
	probePayload := `{"format":{"format_name":"mov,mp4,m4a","duration":"5.000"},"streams":[{"codec_type":"video","codec_name":"h264","width":1280,"height":720,"avg_frame_rate":"24/1"},{"codec_type":"audio","codec_name":"aac"}]}`
	if err := os.WriteFile(probeJSONPath, []byte(probePayload), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pcmPath, pcmBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(argsLogPtrPath, []byte(argsLog), 0o600); err != nil {
		t.Fatal(err)
	}

	srcPath := filepath.Join(binDir, "main.go")
	const fakeSrc = `package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	exeDir := filepath.Dir(os.Args[0])
	base := strings.ToLower(filepath.Base(os.Args[0]))
	if strings.HasPrefix(base, "ffprobe") {
		b, err := os.ReadFile(filepath.Join(exeDir, "ffprobe_p3.json"))
		if err != nil {
			os.Exit(2)
		}
		_, _ = os.Stdout.Write(b)
		return
	}
	if logPtr, err := os.ReadFile(filepath.Join(exeDir, "args_log.txt")); err == nil {
		logPath := strings.TrimSpace(string(logPtr))
		if f, oerr := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600); oerr == nil {
			for _, a := range os.Args[1:] {
				_, _ = fmt.Fprintln(f, a)
			}
			_ = f.Close()
		}
	}
	if len(os.Args) < 2 {
		return
	}
	last := os.Args[len(os.Args)-1]
	if last == "pipe:1" {
		b, err := os.ReadFile(filepath.Join(exeDir, "sample.f32le"))
		if err != nil {
			os.Exit(3)
		}
		_, _ = os.Stdout.Write(b)
		return
	}
	_, _ = fmt.Fprintln(os.Stdout, "out_time_us=2500000")
	if last != "-" && last != "NUL" && last != "/dev/null" {
		_ = os.WriteFile(last, []byte("FAKEMEDIA\n"), 0o600)
	}
}
`
	if err := os.WriteFile(srcPath, []byte(fakeSrc), 0o600); err != nil {
		t.Fatal(err)
	}
	ext := ""
	if runtime.GOOS == "windows" {
		ext = ".exe"
	}
	ffprobeExe = filepath.Join(binDir, "ffprobe"+ext)
	ffmpegExe = filepath.Join(binDir, "ffmpeg"+ext)
	cmd := exec.Command("go", "build", "-o", ffmpegExe, srcPath)
	if out, berr := cmd.CombinedOutput(); berr != nil {
		t.Fatalf("build fake ffmpeg: %v\n%s", berr, string(out))
	}
	binBytes, err := os.ReadFile(ffmpegExe)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ffprobeExe, binBytes, 0o755); err != nil {
		t.Fatal(err)
	}
	return ffprobeExe, ffmpegExe, argsLog
}

func makeTestPCMBytes() []byte {
	// 320 float32 samples: first half quiet (0.1), second half loud (0.8)
	pcm := make([]byte, 320*4)
	for i := 0; i < 320; i++ {
		val := float32(0.1)
		if i >= 160 {
			val = float32(0.8)
		}
		binary.LittleEndian.PutUint32(pcm[i*4:(i+1)*4], math.Float32bits(val))
	}
	return pcm
}

func TestPhase3ValidateSpecs(t *testing.T) {
	nodes := NewNodes(Options{})
	if len(nodes) != 17 {
		t.Fatalf("expected 17 studio nodes, got %d", len(nodes))
	}
	for _, n := range nodes {
		if err := engine.ValidateSpec(n.Spec()); err != nil {
			t.Fatalf("spec invalid for %s: %v", n.Spec().Type, err)
		}
	}
}

func TestPhase3TimelineAutoAssemblePeaksRenderAndNLEExport(t *testing.T) {
	tmp := t.TempDir()
	projDir := filepath.Join(tmp, "proj", "ep01")
	_ = os.MkdirAll(filepath.Join(projDir, "media", "videos"), 0o755)
	_ = os.MkdirAll(filepath.Join(projDir, "media", "tts"), 0o755)
	_ = os.MkdirAll(filepath.Join(projDir, "exports"), 0o755)

	vid1 := filepath.Join(projDir, "media", "videos", "shot1.mp4")
	vid2 := filepath.Join(projDir, "media", "videos", "shot2.mp4")
	aud1 := filepath.Join(projDir, "media", "tts", "shot1.wav")
	bgm := filepath.Join(projDir, "media", "tts", "bgm.mp3")
	for _, p := range []string{vid1, vid2, aud1, bgm} {
		if err := os.WriteFile(p, []byte("MEDIA_PAYLOAD_BYTES"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	ffprobeExe, ffmpegExe, argsLog := writePhase3FakeBinaries(t, tmp, makeTestPCMBytes())
	reg := &stubMediaRegistrar{}
	opts := Options{
		Binaries: &stubBinaryResolver{paths: map[domainbinaries.BinaryID]string{
			domainbinaries.IDFFprobe: ffprobeExe,
			domainbinaries.IDFFmpeg:  ffmpegExe,
		}},
		Registrar:    reg,
		StudioConfig: config.Default(config.ModeDesktop).Studio,
		MediaDir:     tmp,
		WorkspaceDir: tmp,
	}

	// 1. tool.timeline_auto_assemble
	shotsPkg := domainstudio.ShotPromptPackage{
		EpisodeTitle: "Đêm Mưa",
		Shots: []domainstudio.Shot{
			{
				ID:            "s1",
				Seq:           1,
				Title:         "Cảnh Mở Đầu",
				DurationSec:   5,
				VideoPath:     vid1,
				VideoAssetID:  "a-vid1",
				AudioPath:     aud1,
				AudioAssetID:  "a-aud1",
				NarrationText: "Đêm nay trời đổ mưa tầm tã ở quận Tư.",
			},
			{
				ID:          "s2",
				Seq:         2,
				Title:       "Đối Thoại",
				DurationSec: 5,
				VideoPath:   vid2,
				Dialogues: []domainstudio.DialogueLine{
					{Speaker: "Minh", Line: "Chìa khóa đây, giữ cẩn thận nhé!"},
				},
			},
		},
	}
	shotsBytes, _ := json.Marshal(shotsPkg)
	transMs := int64(400)
	bgmVol := 0.25
	assemRaw, err := NewTimelineAutoAssembleNode(opts).Run(context.Background(), mustJSON(t, contracts.TimelineAutoAssembleInput{
		ShotsJSON:         string(shotsBytes),
		Resolution:        "1280x720",
		FPS:               24,
		TransitionDefault: "crossfade",
		TransitionMs:      &transMs,
		BGMPath:           bgm,
		BGMVolume:         &bgmVol,
		ProjectDir:        projDir,
	}))
	if err != nil {
		t.Fatalf("timeline_auto_assemble: %v", err)
	}
	assemOut := assemRaw.(contracts.TimelineAutoAssembleOutput)
	if assemOut.TrackCount != 3 || assemOut.SubtitleCount != 2 || assemOut.TimelinePath == "" {
		t.Fatalf("unexpected assemble output: %+v", assemOut)
	}
	if _, serr := os.Stat(assemOut.TimelinePath); serr != nil {
		t.Fatalf("expected timeline.json on disk: %v", serr)
	}

	// 2. tool.media_peaks_thumbs (verifies real f32le PCM peak computation + FileGuard)
	denyCtx := engine.WithFileGuard(context.Background(), func(p string, _ bool) error {
		if strings.Contains(p, "shot1.mp4") {
			return errors.New("server denied read")
		}
		return nil
	})
	if _, gerr := NewMediaPeaksThumbsNode(opts).Run(denyCtx, mustJSON(t, contracts.MediaPeaksThumbsInput{
		Media: engine.ImageInputRef{MediaRef: engine.RefFor(vid1, engine.MediaVideo)},
	})); gerr == nil || !strings.Contains(gerr.Error(), "server denied read") {
		t.Fatalf("expected FileGuard rejection on media_peaks_thumbs, got %v", gerr)
	}

	peaksRaw, err := NewMediaPeaksThumbsNode(opts).Run(context.Background(), mustJSON(t, contracts.MediaPeaksThumbsInput{
		Media:     engine.ImageInputRef{MediaRef: engine.RefFor(vid1, engine.MediaVideo)},
		PeakBins:  16,
		OutputDir: filepath.Join(projDir, "thumbs"),
	}))
	if err != nil {
		t.Fatalf("media_peaks_thumbs: %v", err)
	}
	peaksOut := peaksRaw.(contracts.MediaPeaksThumbsOutput)
	if len(peaksOut.Peaks) != 16 || peaksOut.SpriteSheetPath == "" || peaksOut.PeaksJSONPath == "" {
		t.Fatalf("unexpected peaks output: %+v", peaksOut)
	}
	if peaksOut.Peaks[0] >= peaksOut.Peaks[15] || peaksOut.Peaks[15] != 1.0 {
		t.Fatalf("expected second half of PCM peaks louder and normalized to 1.0, got %v", peaksOut.Peaks)
	}

	// 3. tool.render_timeline (xfade + loudnorm -14.0 + Vietnamese UTF-8 .srt/.ass burn-in + progress)
	var pcts []float64
	pCtx := progress.With(context.Background(), progress.Sink{
		Report: func(u progress.Update) {
			pcts = append(pcts, u.Percent)
		},
	})
	renderRaw, err := NewRenderTimelineNode(opts).Run(pCtx, mustJSON(t, contracts.RenderTimelineInput{
		TimelineJSON:     assemOut.TimelineJSON,
		OutputDir:        filepath.Join(projDir, "exports"),
		Filename:         "ep01_final.mp4",
		BurnSubtitles:    true,
		SubtitleFontSize: 28,
		LoudnormLUFS:     -14.0,
	}))
	if err != nil {
		t.Fatalf("render_timeline: %v", err)
	}
	renderOut := renderRaw.(contracts.RenderTimelineOutput)
	if renderOut.OutputPath == "" || renderOut.SRTPath == "" || renderOut.ASSPath == "" || len(pcts) < 2 {
		t.Fatalf("unexpected render output: %+v pcts=%v", renderOut, pcts)
	}
	srtBytes, _ := os.ReadFile(renderOut.SRTPath)
	assBytes, _ := os.ReadFile(renderOut.ASSPath)
	if !strings.Contains(string(srtBytes), "Đêm nay trời đổ mưa tầm tã ở quận Tư.") ||
		!strings.Contains(string(assBytes), "Chìa khóa đây, giữ cẩn thận nhé!") {
		t.Fatalf("Vietnamese subtitles missing/corrupted:\nSRT:\n%s\nASS:\n%s", string(srtBytes), string(assBytes))
	}
	argsBytes, _ := os.ReadFile(argsLog)
	argsText := string(argsBytes)
	if !strings.Contains(argsText, "xfade=") || !strings.Contains(argsText, "loudnorm=I=-14.0") || !strings.Contains(argsText, "ass=") {
		t.Fatalf("expected xfade, loudnorm=-14.0, and ass= in ffmpeg filter_complex, got:\n%s", argsText)
	}

	// 4. tool.export_nle_draft: CapCut zip, FCPXML, OTIO, and zip size limit enforcement
	incTrue := true
	capcutRaw, err := NewExportNLEDraftNode(opts).Run(context.Background(), mustJSON(t, contracts.ExportNLEDraftInput{
		TimelineJSON:     assemOut.TimelineJSON,
		Format:           "capcut",
		ProjectName:      "Đêm Mưa Tập 1",
		OutputDir:        filepath.Join(projDir, "exports"),
		IncludeMediaCopy: &incTrue,
	}))
	if err != nil {
		t.Fatalf("export_nle_draft capcut: %v", err)
	}
	capcutOut := capcutRaw.(contracts.ExportNLEDraftOutput)
	if !strings.HasSuffix(capcutOut.ExportPath, ".zip") || capcutOut.FileCount < 5 {
		t.Fatalf("unexpected capcut zip output: %+v", capcutOut)
	}
	zr, zerr := zip.OpenReader(capcutOut.ExportPath)
	if zerr != nil {
		t.Fatalf("open capcut zip: %v", zerr)
	}
	var hasDraftContent, hasMeta, hasMaterial bool
	for _, zf := range zr.File {
		if strings.HasSuffix(zf.Name, "/draft_content.json") {
			hasDraftContent = true
		}
		if strings.HasSuffix(zf.Name, "/draft_meta_info.json") {
			hasMeta = true
		}
		if strings.Contains(zf.Name, "/materials/") {
			hasMaterial = true
		}
	}
	_ = zr.Close()
	if !hasDraftContent || !hasMeta || !hasMaterial {
		t.Fatalf("capcut zip missing expected entries: content=%v meta=%v mat=%v", hasDraftContent, hasMeta, hasMaterial)
	}

	// Enforce ExportMaxZipBytes limit
	tinyCapOpts := opts
	tinyCapOpts.StudioConfig.ExportMaxZipBytes = 32
	if _, err := NewExportNLEDraftNode(tinyCapOpts).Run(context.Background(), mustJSON(t, contracts.ExportNLEDraftInput{
		TimelineJSON:     assemOut.TimelineJSON,
		Format:           "capcut",
		OutputDir:        filepath.Join(projDir, "exports"),
		IncludeMediaCopy: &incTrue,
	})); err == nil || !strings.Contains(err.Error(), "export_max_zip_bytes") {
		t.Fatalf("expected export_max_zip_bytes error, got %v", err)
	}

	// FCPXML export
	fcpxRaw, err := NewExportNLEDraftNode(opts).Run(context.Background(), mustJSON(t, contracts.ExportNLEDraftInput{
		TimelineJSON: assemOut.TimelineJSON,
		Format:       "fcpxml",
		ProjectName:  "Đêm Mưa",
		OutputDir:    filepath.Join(projDir, "exports"),
	}))
	if err != nil {
		t.Fatalf("export_nle_draft fcpxml: %v", err)
	}
	fcpxBytes, _ := os.ReadFile(fcpxRaw.(contracts.ExportNLEDraftOutput).ExportPath)
	var parsedXML any
	if xerr := xml.Unmarshal(fcpxBytes, &parsedXML); xerr != nil || !strings.Contains(string(fcpxBytes), `<fcpxml version="1.10">`) {
		t.Fatalf("invalid FCPXML output: err=%v\n%s", xerr, string(fcpxBytes))
	}

	// OTIO export
	otioRaw, err := NewExportNLEDraftNode(opts).Run(context.Background(), mustJSON(t, contracts.ExportNLEDraftInput{
		TimelineJSON: assemOut.TimelineJSON,
		Format:       "otio",
		ProjectName:  "Đêm Mưa",
		OutputDir:    filepath.Join(projDir, "exports"),
	}))
	if err != nil {
		t.Fatalf("export_nle_draft otio: %v", err)
	}
	otioBytes, _ := os.ReadFile(otioRaw.(contracts.ExportNLEDraftOutput).ExportPath)
	if !strings.Contains(string(otioBytes), `"OTIO_SCHEMA": "Timeline.1"`) {
		t.Fatalf("invalid OTIO output: %s", string(otioBytes))
	}

	// 5. TrackVideo.Muted mutes video track audio while preserving video frames (no black fallback)
	var mutedTL domainstudio.EditTimeline
	if err := json.Unmarshal([]byte(assemOut.TimelineJSON), &mutedTL); err != nil {
		t.Fatal(err)
	}
	mutedTL.Tracks[0].Muted = true
	mutedBytes, _ := json.Marshal(mutedTL)
	_ = os.Remove(argsLog)
	if _, err := NewRenderTimelineNode(opts).Run(context.Background(), mustJSON(t, contracts.RenderTimelineInput{
		TimelineJSON: string(mutedBytes),
		OutputDir:    filepath.Join(projDir, "exports"),
		Filename:     "ep01_muted_video_audio.mp4",
	})); err != nil {
		t.Fatalf("render_timeline with muted video audio: %v", err)
	}
	mutedArgsBytes, _ := os.ReadFile(argsLog)
	mutedArgsText := string(mutedArgsBytes)
	if strings.Contains(mutedArgsText, "color=c=black") {
		t.Fatalf("expected muted TrackVideo to keep video visuals rather than falling back to black screen:\n%s", mutedArgsText)
	}
	if !strings.Contains(mutedArgsText, "[0:a]") && strings.Contains(mutedArgsText, "amix") {
		// ok: 0:a omitted
	}
	if strings.Contains(mutedArgsText, "[0:a]atrim") {
		t.Fatalf("expected muted TrackVideo input 0 audio to be excluded from audio mix:\n%s", mutedArgsText)
	}
}

func TestPhase3RenderTimeShiftExplicitCRFEmptyCuesAndEmptyShots(t *testing.T) {
	// 1. formatSRT emits contiguous 1..N cue indices when blank cues exist
	srt := formatSRT([]domainstudio.SubtitleCue{
		{ID: "c1", StartMs: 0, EndMs: 1000, Text: "Line one"},
		{ID: "c_blank", StartMs: 1000, EndMs: 2000, Text: "   "},
		{ID: "c2", StartMs: 2000, EndMs: 3500, Text: "Line two", Speaker: "An"},
	})
	if !strings.Contains(srt, "1\n00:00:00,000 --> 00:00:01,000\nLine one\n\n2\n00:00:02,000 --> 00:00:03,500\nAn: Line two") {
		t.Fatalf("expected contiguous 1, 2 indices skipping blank cue, got:\n%s", srt)
	}
	if strings.Contains(srt, "\n3\n") {
		t.Fatalf("unexpected skipped gap index 3 in SRT:\n%s", srt)
	}

	// 2. parseShotsForAssemble accepts {"shots":[]} cleanly without error
	shots, err := parseShotsForAssemble(`{"shots":[]}`)
	if err != nil || len(shots) != 0 {
		t.Fatalf("expected empty shots slice with nil error for {\"shots\":[]}, got shots=%v err=%v", shots, err)
	}

	tmp := t.TempDir()
	projDir := filepath.Join(tmp, "proj")
	mediaDir := filepath.Join(projDir, "media")
	exportDir := filepath.Join(projDir, "exports")
	_ = os.MkdirAll(mediaDir, 0o755)
	_ = os.MkdirAll(exportDir, 0o755)
	vid1 := filepath.Join(mediaDir, "v1.mp4")
	vid2 := filepath.Join(mediaDir, "v2.mp4")
	aud := filepath.Join(mediaDir, "a2.wav")
	for _, p := range []string{vid1, vid2, aud} {
		if werr := os.WriteFile(p, []byte("FAKE"), 0o600); werr != nil {
			t.Fatal(werr)
		}
	}

	ffprobeExe, ffmpegExe, argsLog := writePhase3FakeBinaries(t, tmp, makeTestPCMBytes())
	opts := Options{
		Binaries: &stubBinaryResolver{paths: map[domainbinaries.BinaryID]string{
			domainbinaries.IDFFprobe: ffprobeExe,
			domainbinaries.IDFFmpeg:  ffmpegExe,
		}},
		StudioConfig: config.Default(config.ModeDesktop).Studio,
	}

	_, aerr := NewTimelineAutoAssembleNode(opts).Run(context.Background(), mustJSON(t, contracts.TimelineAutoAssembleInput{
		EpisodeID:  "ep-empty",
		ShotsJSON:  `{"shots":[]}`,
		ProjectDir: projDir,
	}))
	if aerr == nil || !strings.Contains(aerr.Error(), "contains no shots with video_path") {
		t.Fatalf("expected clean 'no shots with video_path' validation (not parse error) for {\"shots\":[]}, got %v", aerr)
	}

	// 3. Abutting crossfade (500ms) shifts subsequent audio adelay & subtitles to 4500ms AND preserves explicit CRF=0
	tl := domainstudio.EditTimeline{
		Version: 1,
		Width:   1280,
		Height:  720,
		FPS:     24,
		Tracks: []domainstudio.Track{
			{
				ID:     "v1",
				Kind:   domainstudio.TrackVideo,
				Volume: 1,
				Clips: []domainstudio.TimelineClip{
					{ID: "vc1", FilePath: vid1, SourceInMs: 0, SourceOutMs: 5000, TimelineStartMs: 0, Speed: 1, Volume: 1},
					{ID: "vc2", FilePath: vid2, SourceInMs: 0, SourceOutMs: 5000, TimelineStartMs: 5000, Speed: 1, TransitionIn: domainstudio.TransitionCrossfade, TransitionMs: 500, Volume: 1},
				},
			},
			{
				ID:     "a1",
				Kind:   domainstudio.TrackAudioDialogue,
				Volume: 1,
				Clips: []domainstudio.TimelineClip{
					{ID: "ac2", FilePath: aud, SourceInMs: 0, SourceOutMs: 2000, TimelineStartMs: 5000, Speed: 1, Volume: 1},
				},
			},
		},
		Subtitles: []domainstudio.SubtitleCue{
			{ID: "sub1", StartMs: 5000, EndMs: 7000, Text: "Synced after xfade"},
		},
	}
	zeroCRF := 0
	outRaw, rerr := NewRenderTimelineNode(opts).Run(context.Background(), mustJSON(t, contracts.RenderTimelineInput{
		TimelineJSON: string(mustJSON(t, tl)),
		OutputDir:    exportDir,
		Filename:     "crf0_sync.mp4",
		CRF:          &zeroCRF,
	}))
	if rerr != nil {
		t.Fatalf("render_timeline CRF=0 failed: %v", rerr)
	}
	out := outRaw.(contracts.RenderTimelineOutput)
	argsBytes, _ := os.ReadFile(argsLog)
	argsText := string(argsBytes)
	if !strings.Contains(argsText, "-crf\n0\n") && !strings.Contains(strings.ReplaceAll(argsText, "\r\n", "\n"), "-crf\n0\n") {
		t.Fatalf("expected explicit -crf 0 in ffmpeg args, got:\n%s", argsText)
	}
	if !strings.Contains(argsText, "adelay=4500|4500") {
		t.Fatalf("expected audio adelay shifted to 4500|4500 after 500ms abutting xfade, got:\n%s", argsText)
	}
	srtBytes, _ := os.ReadFile(out.SRTPath)
	if !strings.Contains(string(srtBytes), "00:00:04,500 --> 00:00:06,500") {
		t.Fatalf("expected shifted SRT timestamp 00:00:04,500 --> 00:00:06,500, got:\n%s", string(srtBytes))
	}
}

func TestPhase3PCMStderrOver64KiBNoDeadlockAndMicroIntervalFloor(t *testing.T) {
	tmp := t.TempDir()
	tmpRoot := filepath.Join("..", "..", "..", "build", ".gotmp")
	if err := os.MkdirAll(tmpRoot, 0o755); err != nil {
		tmpRoot = tmp
	}
	binDir, err := os.MkdirTemp(tmpRoot, "p3noisy-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(binDir) })

	pcmBytes := makeTestPCMBytes()
	pcmPath := filepath.Join(binDir, "sample.f32le")
	argsLog := filepath.Join(tmp, "noisy_args.log")
	argsLogPtr := filepath.Join(binDir, "args_log.txt")
	probePath := filepath.Join(binDir, "ffprobe.json")
	probePayload := `{"format":{"format_name":"mov,mp4","duration":"0.020"},"streams":[{"codec_type":"video","codec_name":"h264","width":640,"height":360,"avg_frame_rate":"24/1"},{"codec_type":"audio","codec_name":"aac"}]}`
	_ = os.WriteFile(pcmPath, pcmBytes, 0o600)
	_ = os.WriteFile(argsLogPtr, []byte(argsLog), 0o600)
	_ = os.WriteFile(probePath, []byte(probePayload), 0o600)

	// Child writes 256 KiB (> 64 KiB OS pipe buffer) to stderr while emitting PCM on stdout!
	srcPath := filepath.Join(binDir, "main.go")
	const noisySrc = `package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	exeDir := filepath.Dir(os.Args[0])
	base := strings.ToLower(filepath.Base(os.Args[0]))
	if strings.HasPrefix(base, "ffprobe") {
		b, _ := os.ReadFile(filepath.Join(exeDir, "ffprobe.json"))
		_, _ = os.Stdout.Write(b)
		return
	}
	if logPtr, err := os.ReadFile(filepath.Join(exeDir, "args_log.txt")); err == nil {
		if f, oerr := os.OpenFile(strings.TrimSpace(string(logPtr)), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600); oerr == nil {
			for _, a := range os.Args[1:] {
				_, _ = fmt.Fprintln(f, a)
			}
			_ = f.Close()
		}
	}
	last := os.Args[len(os.Args)-1]
	if last == "pipe:1" {
		noise := bytes.Repeat([]byte("W"), 256*1024)
		_, _ = os.Stderr.Write(noise)
		b, _ := os.ReadFile(filepath.Join(exeDir, "sample.f32le"))
		_, _ = os.Stdout.Write(b)
		_, _ = os.Stderr.Write(noise)
		return
	}
	_ = os.WriteFile(last, []byte("JPG"), 0o600)
}
`
	if err := os.WriteFile(srcPath, []byte(noisySrc), 0o600); err != nil {
		t.Fatal(err)
	}
	ext := ""
	if runtime.GOOS == "windows" {
		ext = ".exe"
	}
	ffmpegExe := filepath.Join(binDir, "ffmpeg"+ext)
	ffprobeExe := filepath.Join(binDir, "ffprobe"+ext)
	if out, berr := exec.Command("go", "build", "-o", ffmpegExe, srcPath).CombinedOutput(); berr != nil {
		t.Fatalf("build noisy ffmpeg: %v\n%s", berr, string(out))
	}
	binData, _ := os.ReadFile(ffmpegExe)
	_ = os.WriteFile(ffprobeExe, binData, 0o755)

	vid := filepath.Join(tmp, "tiny.mp4")
	_ = os.WriteFile(vid, []byte("MP4"), 0o600)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cfg := config.Default(config.ModeDesktop).Studio
	cfg.TimelineMaxStripThumbs = 100
	node := NewMediaPeaksThumbsNode(Options{
		MediaDir:     tmp,
		WorkspaceDir: tmp,
		StudioConfig: cfg,
		Binaries: &stubBinaryResolver{paths: map[domainbinaries.BinaryID]string{
			domainbinaries.IDFFprobe: ffprobeExe,
			domainbinaries.IDFFmpeg:  ffmpegExe,
		}},
	})
	rawOut, err := node.Run(ctx, mustJSON(t, contracts.MediaPeaksThumbsInput{
		Media:            engine.InputRef(vid, engine.MediaVideo),
		ThumbIntervalSec: 0.0001,
		PeakBins:         16,
		OutputDir:        tmp,
	}))
	if err != nil {
		t.Fatalf("media_peaks_thumbs deadlocked or failed on >256KiB stderr: %v", err)
	}
	out := rawOut.(contracts.MediaPeaksThumbsOutput)
	if len(out.Peaks) != 16 || out.Peaks[15] != 1.0 {
		t.Fatalf("unexpected PCM peaks after heavy stderr drain: %v", out.Peaks)
	}
	argsBytes, _ := os.ReadFile(argsLog)
	if strings.Contains(string(argsBytes), "fps=1/0.0000") || !strings.Contains(string(argsBytes), "fps=1/0.0500") {
		t.Fatalf("expected minThumbIntervalSec floor (fps=1/0.0500), got:\n%s", string(argsBytes))
	}
}

func TestPhase3RealAccessPolicyGuardsAndNLESecretExfiltrationBlock(t *testing.T) {
	base := t.TempDir()
	wsDir := filepath.Join(base, "ws")
	projDir := filepath.Join(wsDir, "projects")
	mediaDir := filepath.Join(base, "media")
	outsideDir := t.TempDir()
	for _, d := range []string{wsDir, projDir, mediaDir} {
		_ = os.MkdirAll(d, 0o755)
	}

	insideVid := filepath.Join(mediaDir, "ok.mp4")
	outsideVid := filepath.Join(outsideDir, "outside.mp4")
	insideEnv := filepath.Join(wsDir, ".env")
	insideSecretJSON := filepath.Join(projDir, "secrets.json")
	_ = os.WriteFile(insideVid, []byte("MP4"), 0o600)
	_ = os.WriteFile(outsideVid, []byte("OUTSIDE"), 0o600)
	_ = os.WriteFile(insideEnv, []byte("API_KEY=topsecret"), 0o600)
	_ = os.WriteFile(insideSecretJSON, []byte(`{"token":"secret"}`), 0o600)

	policy := agent.NewAccessPolicy(filepath.Join(base, "data"), wsDir, mediaDir, filepath.Join(base, "profiles"), nil)
	policy.SetProjectsDir(projDir)
	guardedCtx := engine.WithFileGuard(context.Background(), policy.CheckTrustedFile)

	opts := Options{
		MediaDir:     mediaDir,
		WorkspaceDir: wsDir,
		StudioConfig: config.Default(config.ModeDesktop).Studio,
	}

	// 1. timeline_auto_assemble blocks JSON-embedded outside video_path with CodeUnauthorized
	outsideShots, _ := json.Marshal(domainstudio.ShotPromptPackage{
		Shots: []domainstudio.Shot{{ID: "s1", Seq: 1, DurationSec: 5, VideoPath: outsideVid}},
	})
	if _, err := NewTimelineAutoAssembleNode(opts).Run(guardedCtx, mustJSON(t, contracts.TimelineAutoAssembleInput{
		ShotsJSON: string(outsideShots),
	})); err == nil || !apperr.IsCode(err, apperr.CodeUnauthorized) {
		t.Fatalf("expected CodeUnauthorized on JSON-embedded outside shot video_path, got %v", err)
	}

	// 2. video_concat_basic blocks JSON-embedded outside clip path with CodeUnauthorized
	if _, err := NewVideoConcatBasicNode(opts).Run(guardedCtx, mustJSON(t, contracts.VideoConcatBasicInput{
		ClipsManifestJSON: `["` + filepath.ToSlash(outsideVid) + `"]`,
	})); err == nil || !apperr.IsCode(err, apperr.CodeUnauthorized) {
		t.Fatalf("expected CodeUnauthorized on JSON-embedded outside concat clip, got %v", err)
	}

	// 3. export_nle_draft (include_media_copy: true) blocks .env and secrets.json exfiltration
	// both WITH AccessPolicy (CodeUnauthorized) AND WITHOUT FileGuard (CodeValidation unsupported_media_ext)!
	incTrue := true
	for _, badPath := range []string{insideEnv, insideSecretJSON, outsideVid} {
		tl := domainstudio.EditTimeline{
			Version: 1, Width: 1280, Height: 720, FPS: 24,
			Tracks: []domainstudio.Track{{
				ID: "v1", Kind: domainstudio.TrackVideo, Volume: 1,
				Clips: []domainstudio.TimelineClip{{
					ID: "c1", FilePath: badPath, SourceInMs: 0, SourceOutMs: 2000, Speed: 1, Volume: 1,
				}},
			}},
		}
		tlBytes, _ := json.Marshal(tl)
		if _, err := NewExportNLEDraftNode(opts).Run(guardedCtx, mustJSON(t, contracts.ExportNLEDraftInput{
			TimelineJSON:     string(tlBytes),
			Format:           "capcut",
			OutputDir:        projDir,
			IncludeMediaCopy: &incTrue,
		})); err == nil {
			t.Fatalf("expected guarded export_nle_draft to reject %s", badPath)
		}
		if badPath != outsideVid {
			if _, err := NewExportNLEDraftNode(opts).Run(context.Background(), mustJSON(t, contracts.ExportNLEDraftInput{
				TimelineJSON:     string(tlBytes),
				Format:           "capcut",
				OutputDir:        projDir,
				IncludeMediaCopy: &incTrue,
			})); err == nil || !apperr.IsCode(err, apperr.CodeValidation) {
				t.Fatalf("expected unguarded export_nle_draft to reject non-media file %s with CodeValidation, got %v", badPath, err)
			}
		}
	}
}

func TestPhase3SpecDefaultsExplicitZeroBGMOverwriteDedupeAndOverlapShift(t *testing.T) {
	tmp := t.TempDir()
	projDir := filepath.Join(tmp, "projects", "ep01")
	exportDir := filepath.Join(projDir, "exports")
	_ = os.MkdirAll(exportDir, 0o755)

	vid1 := filepath.Join(tmp, "s1.mp4")
	vid2 := filepath.Join(tmp, "s2.mp4")
	bgm := filepath.Join(tmp, "bgm.mp3")
	for _, p := range []string{vid1, vid2, bgm} {
		_ = os.WriteFile(p, []byte("MEDIA"), 0o600)
	}

	ffprobeExe, ffmpegExe, argsLog := writePhase3FakeBinaries(t, tmp, makeTestPCMBytes())
	customCfg := config.Default(config.ModeDesktop).Studio
	customCfg.TimelineDefaultTransitionMs = 450
	customCfg.TimelineDefaultBGMVolume = 0.33
	customCfg.ExportSubtitleFontFamily = "Noto Sans"
	customCfg.FFmpegVideoCodec = "libx265"
	customCfg.FFmpegAudioSampleRate = 44100
	customCfg.FFmpegAudioBitrate = "256k"

	opts := Options{
		MediaDir:     tmp,
		WorkspaceDir: tmp,
		StudioConfig: customCfg,
		Binaries: &stubBinaryResolver{paths: map[domainbinaries.BinaryID]string{
			domainbinaries.IDFFprobe: ffprobeExe,
			domainbinaries.IDFFmpeg:  ffmpegExe,
		}},
	}

	shotsBytes, _ := json.Marshal(domainstudio.ShotPromptPackage{
		Shots: []domainstudio.Shot{
			{ID: "s1", Seq: 1, DurationSec: 5, VideoPath: vid1, NarrationText: "Một"},
			{ID: "s2", Seq: 2, DurationSec: 5, VideoPath: vid2, NarrationText: "Hai"},
		},
	})

	// 1. Omitted transition_ms & bgm_volume use StudioConfig defaults (450ms & 0.33)
	rawDef, err := NewTimelineAutoAssembleNode(opts).Run(context.Background(), json.RawMessage(`{
		"shots_json": `+strconvQuote(string(shotsBytes))+`,
		"bgm_path": `+strconvQuote(bgm)+`
	}`))
	if err != nil {
		t.Fatalf("assemble defaults: %v", err)
	}
	var tlDef domainstudio.EditTimeline
	_ = json.Unmarshal([]byte(rawDef.(contracts.TimelineAutoAssembleOutput).TimelineJSON), &tlDef)
	if tlDef.Tracks[0].Clips[1].TransitionMs != 450 {
		t.Fatalf("expected omitted transition_ms to default to 450ms, got %d", tlDef.Tracks[0].Clips[1].TransitionMs)
	}
	if math.Abs(tlDef.Tracks[len(tlDef.Tracks)-1].Volume-0.33) > 0.001 {
		t.Fatalf("expected omitted bgm_volume to default to 0.33, got %v", tlDef.Tracks[len(tlDef.Tracks)-1].Volume)
	}

	// 2. Explicit bgm_volume: 0 and transition_ms: 0 are preserved (NOT overwritten with defaults!)
	rawZero, err := NewTimelineAutoAssembleNode(opts).Run(context.Background(), json.RawMessage(`{
		"shots_json": `+strconvQuote(string(shotsBytes))+`,
		"transition_ms": 0,
		"bgm_path": `+strconvQuote(bgm)+`,
		"bgm_volume": 0
	}`))
	if err != nil {
		t.Fatalf("assemble explicit zero: %v", err)
	}
	var tlZero domainstudio.EditTimeline
	_ = json.Unmarshal([]byte(rawZero.(contracts.TimelineAutoAssembleOutput).TimelineJSON), &tlZero)
	if tlZero.Tracks[0].Clips[1].TransitionMs != 0 || tlZero.Tracks[len(tlZero.Tracks)-1].Volume != 0 || !tlZero.Tracks[len(tlZero.Tracks)-1].Muted {
		t.Fatalf("expected explicit 0 preserved for transition_ms & bgm_volume, got clip1=%+v bgmTrack=%+v",
			tlZero.Tracks[0].Clips[1], tlZero.Tracks[len(tlZero.Tracks)-1])
	}

	// 3. render_timeline overwrite=false deduplicates output filename; custom StudioConfig propagates to FFmpeg & ASS;
	// overlapping TransitionNone shifts subsequent subtitles accurately!
	overlapTL := domainstudio.EditTimeline{
		Version: 1, Width: 1280, Height: 720, FPS: 24,
		Tracks: []domainstudio.Track{{
			ID: "v1", Kind: domainstudio.TrackVideo, Volume: 1,
			Clips: []domainstudio.TimelineClip{
				{ID: "c1", FilePath: vid1, SourceInMs: 0, SourceOutMs: 5000, TimelineStartMs: 0, Speed: 1, Volume: 1},
				// Overlaps by 1000ms with TransitionNone -> concat=n=2 pushes end from 9000ms to 10000ms (+1000ms shift!)
				{ID: "c2", FilePath: vid2, SourceInMs: 0, SourceOutMs: 5000, TimelineStartMs: 4000, Speed: 1, TransitionIn: domainstudio.TransitionNone, Volume: 1},
			},
		}},
		Subtitles: []domainstudio.SubtitleCue{
			{ID: "sub-after", StartMs: 5000, EndMs: 6000, Text: "Shifted by non-xfade overlap"},
		},
	}
	overlapJSON := string(mustJSON(t, overlapTL))
	_ = os.Remove(argsLog)
	r1Raw, err := NewRenderTimelineNode(opts).Run(context.Background(), mustJSON(t, contracts.RenderTimelineInput{
		TimelineJSON: overlapJSON,
		OutputDir:    exportDir,
		Filename:     "dup_check.mp4",
		Overwrite:    false,
	}))
	if err != nil {
		t.Fatalf("render 1: %v", err)
	}
	r1 := r1Raw.(contracts.RenderTimelineOutput)
	r2Raw, err := NewRenderTimelineNode(opts).Run(context.Background(), mustJSON(t, contracts.RenderTimelineInput{
		TimelineJSON: overlapJSON,
		OutputDir:    exportDir,
		Filename:     "dup_check.mp4",
		Overwrite:    false,
	}))
	if err != nil {
		t.Fatalf("render 2: %v", err)
	}
	r2 := r2Raw.(contracts.RenderTimelineOutput)
	if r1.OutputPath == r2.OutputPath || !strings.HasSuffix(r2.OutputPath, "dup_check_1.mp4") {
		t.Fatalf("expected overwrite=false to deduplicate dup_check.mp4 -> dup_check_1.mp4, got r1=%s r2=%s", r1.OutputPath, r2.OutputPath)
	}

	assBytes, _ := os.ReadFile(r1.ASSPath)
	if !strings.Contains(string(assBytes), "Style: Default,Noto Sans,") {
		t.Fatalf("expected custom ExportSubtitleFontFamily 'Noto Sans' in ASS, got:\n%s", string(assBytes))
	}
	srtBytes, _ := os.ReadFile(r1.SRTPath)
	if !strings.Contains(string(srtBytes), "00:00:06,000 --> 00:00:07,000") {
		t.Fatalf("expected +1000ms shift on subtitle after overlapping TransitionNone concat, got:\n%s", string(srtBytes))
	}
	argsBytes, _ := os.ReadFile(argsLog)
	argsStr := string(argsBytes)
	if !strings.Contains(argsStr, "libx265") || !strings.Contains(argsStr, "sample_rates=44100") || !strings.Contains(argsStr, "256k") {
		t.Fatalf("expected custom StudioConfig FFmpeg codec/sample_rate/bitrate in args, got:\n%s", argsStr)
	}

	// 4. export_nle_draft default project_name uses neutral "studio_timeline"
	expRaw, err := NewExportNLEDraftNode(opts).Run(context.Background(), mustJSON(t, contracts.ExportNLEDraftInput{
		TimelineJSON: overlapJSON,
		Format:       "otio",
		OutputDir:    exportDir,
	}))
	if err != nil {
		t.Fatalf("export default project_name: %v", err)
	}
	expPath := expRaw.(contracts.ExportNLEDraftOutput).ExportPath
	if !strings.Contains(filepath.Base(expPath), "studio-timeline") || strings.Contains(filepath.Base(expPath), "dezuxk") {
		t.Fatalf("expected neutral studio-timeline slug in exported filename, got %s", expPath)
	}
}

func TestStudioToolsHardeningFixes(t *testing.T) {
	// 1. P0-3 Ken Burns zoompan omits -loop 1 and pre-zoompan fps=, while KenBurnsNone keeps both.
	tl := domainstudio.EditTimeline{
		Version: 1, Width: 1280, Height: 720, FPS: 24,
		Tracks: []domainstudio.Track{{
			ID: "v1", Kind: domainstudio.TrackVideo, Volume: 1,
			Clips: []domainstudio.TimelineClip{
				{ID: "kb", FilePath: "C:\\media\\still_kb.png", TimelineStartMs: 0, SourceInMs: 0, SourceOutMs: 3000, KenBurnsPreset: domainstudio.KenBurnsZoomIn},
				{ID: "st", FilePath: "C:\\media\\still_static.png", TimelineStartMs: 3000, SourceInMs: 0, SourceOutMs: 2000, KenBurnsPreset: domainstudio.KenBurnsNone},
			},
		}},
	}
	resolved := []resolvedTimelineClip{
		{track: tl.Tracks[0], clip: tl.Tracks[0].Clips[0], inputIndex: 0, isImage: true},
		{track: tl.Tracks[0], clip: tl.Tracks[0].Clips[1], inputIndex: 1, isImage: true},
	}
	args, _, _ := buildRenderTimelineArgs(tl, resolved, `C:\sub dir\my'sub[1].ass`, -14.0, "medium", 20, config.StudioConfig{}, "out.mp4")
	argsJoined := strings.Join(args, " ")
	if strings.Contains(argsJoined, "-loop 1 -t 3.000 -i C:\\media\\still_kb.png") {
		t.Fatalf("expected KenBurnsZoomIn still image NOT to pass -loop 1, got:\n%s", argsJoined)
	}
	if !strings.Contains(argsJoined, "-loop 1 -t 2.000 -i C:\\media\\still_static.png") {
		t.Fatalf("expected KenBurnsNone still image to keep -loop 1, got:\n%s", argsJoined)
	}
	if strings.Contains(argsJoined, "setsar=1,fps=24,zoompan=") {
		t.Fatalf("expected no fps= before zoompan filter, got:\n%s", argsJoined)
	}
	if !strings.Contains(argsJoined, "setsar=1,zoompan=") {
		t.Fatalf("expected zoompan directly after setsar=1, got:\n%s", argsJoined)
	}
	if !strings.Contains(argsJoined, `ass=filename=C\:/sub\ dir/my\'sub\[1\].ass`) {
		t.Fatalf("unexpected ass=filename escaping, got:\n%s", argsJoined)
	}

	// 2. UNC path URI conversion
	if got := filePathToFileURI(`\\nas01\studio share\shot 1.mp4`); got != "file://nas01/studio%20share/shot%201.mp4" {
		t.Fatalf("UNC filePathToFileURI = %q", got)
	}

	// 3. Batching >24 clips uses FFmpeg concat demuxer, MediaPeaksThumbs uses multi-row grid & zip.Store for CapCut media.
	tmp := t.TempDir()
	ffprobeExe, ffmpegExe, argsLog := writePhase3FakeBinaries(t, tmp, make([]byte, 800))
	clips := make([]manifestClip, 26)
	var manifestObjs []map[string]any
	for i := range clips {
		p := filepath.Join(tmp, "clip"+string(rune('a'+i%26))+".mp4")
		_ = os.WriteFile(p, []byte("MP4BYTES"), 0o600)
		clips[i] = manifestClip{Path: p, DurationSec: 1.0}
		manifestObjs = append(manifestObjs, map[string]any{"path": p, "duration_sec": 1.0})
	}
	opts := Options{
		MediaDir: tmp,
		Binaries: &stubBinaryResolver{paths: map[domainbinaries.BinaryID]string{
			domainbinaries.IDFFprobe: ffprobeExe,
			domainbinaries.IDFFmpeg:  ffmpegExe,
		}},
	}
	if _, err := NewVideoConcatBasicNode(opts).Run(context.Background(), mustJSON(t, contracts.VideoConcatBasicInput{
		ClipsManifestJSON: string(mustJSON(t, manifestObjs)),
		OutputDir:         tmp,
	})); err != nil {
		t.Fatalf("batched video concat failed: %v", err)
	}
	concatArgsRaw, _ := os.ReadFile(argsLog)
	concatFlat := strings.ReplaceAll(strings.ReplaceAll(string(concatArgsRaw), "\r\n", " "), "\n", " ")
	if !strings.Contains(concatFlat, "-f concat -safe 0 -i") {
		t.Fatalf("expected >24 clip concat to batch via -f concat, got:\n%s", concatFlat)
	}

	// MediaPeaksThumbs grid tile
	_ = os.Remove(argsLog)
	ptRaw, err := NewMediaPeaksThumbsNode(opts).Run(context.Background(), mustJSON(t, contracts.MediaPeaksThumbsInput{
		Media:            engine.InputRef(clips[0].Path, engine.MediaVideo),
		ThumbIntervalSec: 0.25,
		OutputDir:        tmp,
	}))
	if err != nil {
		t.Fatalf("media_peaks_thumbs failed: %v", err)
	}
	ptOut := ptRaw.(contracts.MediaPeaksThumbsOutput)
	if ptOut.SpriteCols != 10 || ptOut.SpriteRows != 2 || ptOut.ThumbCount != 20 {
		t.Fatalf("expected 10x2 grid for 20 capped thumbs (6s / 0.25s), got cols=%d rows=%d count=%d", ptOut.SpriteCols, ptOut.SpriteRows, ptOut.ThumbCount)
	}

	// CapCut export uses zip.Store on media assets
	ccTL := domainstudio.EditTimeline{
		Version: 1, Width: 1280, Height: 720, FPS: 24,
		Tracks: []domainstudio.Track{{
			ID: "v1", Kind: domainstudio.TrackVideo, Volume: 1,
			Clips: []domainstudio.TimelineClip{{ID: "c1", FilePath: clips[0].Path, TimelineStartMs: 0, SourceInMs: 0, SourceOutMs: 1000}},
		}},
	}
	incMedia := true
	ccRaw, err := NewExportNLEDraftNode(opts).Run(context.Background(), mustJSON(t, contracts.ExportNLEDraftInput{
		TimelineJSON:     string(mustJSON(t, ccTL)),
		Format:           "capcut",
		IncludeMediaCopy: &incMedia,
		OutputDir:        tmp,
	}))
	if err != nil {
		t.Fatalf("capcut export failed: %v", err)
	}
	zr, err := zip.OpenReader(ccRaw.(contracts.ExportNLEDraftOutput).ExportPath)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	for _, f := range zr.File {
		if strings.HasSuffix(f.Name, ".mp4") && f.Method != zip.Store {
			t.Fatalf("expected zip.Store for precompressed mp4 %q, got method=%d", f.Name, f.Method)
		}
	}
}
