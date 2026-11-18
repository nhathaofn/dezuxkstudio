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

	"app/internal/config"
	"app/internal/contracts"
	domainbinaries "app/internal/domain/binaries"
	domainstudio "app/internal/domain/studio"
	"app/internal/engine"
	"app/internal/progress"
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
	if len(nodes) != 16 {
		t.Fatalf("expected 16 studio nodes, got %d", len(nodes))
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
