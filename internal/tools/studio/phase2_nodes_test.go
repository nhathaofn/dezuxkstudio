package studio

import (
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"app/internal/apperr"
	"app/internal/contracts"
	domainbinaries "app/internal/domain/binaries"
	domainmedia "app/internal/domain/mediagen"
	domainprovider "app/internal/domain/provider"
	"app/internal/engine"
	"app/internal/service/agent"
)

type stubPhase2Media struct {
	lastImageReq   domainmedia.ImageRequest
	lastTTSReq     domainmedia.TTSRequest
	lastLipSyncReq domainmedia.LipSyncRequest
	lastTextReq    domainmedia.TextRequest
	outDir         string
}

func (s *stubPhase2Media) GenerateImage(_ context.Context, req domainmedia.ImageRequest) (domainmedia.Result, error) {
	s.lastImageReq = req
	out := filepath.Join(s.outDir, "mask_edited.png")
	writeSolidPNG(out, 16, 16, color.RGBA{R: 10, G: 200, B: 50, A: 255})
	return domainmedia.Result{Path: out, MimeType: "image/png", Provider: "openai", Model: "gpt-image-1"}, nil
}

func (s *stubPhase2Media) GenerateTTS(_ context.Context, req domainmedia.TTSRequest) (domainmedia.Result, error) {
	s.lastTTSReq = req
	out := filepath.Join(s.outDir, "tts_gen.mp3")
	_ = os.WriteFile(out, []byte("ID3TTS"), 0o600)
	return domainmedia.Result{Path: out, MimeType: "audio/mpeg", Provider: "openai", Model: "tts-1"}, nil
}

func (s *stubPhase2Media) GenerateLipSync(_ context.Context, req domainmedia.LipSyncRequest) (domainmedia.Result, error) {
	s.lastLipSyncReq = req
	out := filepath.Join(s.outDir, "lipsync_out.mp4")
	_ = os.WriteFile(out, []byte("MP4LIPSYNC"), 0o600)
	return domainmedia.Result{Path: out, MimeType: "video/mp4", Provider: "kling", Model: "kling-lip-sync"}, nil
}

func (s *stubPhase2Media) GenerateText(_ context.Context, req domainmedia.TextRequest) (domainmedia.TextResult, error) {
	s.lastTextReq = req
	payload := json.RawMessage(`{"verdict":"pass","overall_score":9.4,"summary":"Crisp framing and sharp details","issues":[{"severity":"info","category":"continuity","message":"Character jacket matches reference"}]}`)
	return domainmedia.TextResult{Text: string(payload), ParsedJSON: payload, Provider: "gemini", Model: "gemini-2.5-flash"}, nil
}

type stubPreflightDeps struct {
	block    bool
	freeRate bool
}

func (s *stubPreflightDeps) CheckCapability(_ context.Context, _ domainprovider.Capability, _ string) (string, string, error) {
	if s.block {
		return "", "", errors.New("no enabled video credential configured")
	}
	return "openai", "sora-2", nil
}

func (s *stubPreflightDeps) EstimateUnitPriceUSD(_ context.Context, _, _, _, _ string) (float64, bool, error) {
	if s.freeRate {
		return 0.0, false, nil
	}
	return 0.10, false, nil
}

func writeSolidPNG(path string, w, h int, c color.RGBA) {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, c)
		}
	}
	f, _ := os.Create(path)
	_ = png.Encode(f, img)
	_ = f.Close()
}

func writePhase2FakeBinaries(t *testing.T, dir string) (ffprobeExe, ffmpegExe, argsLog string) {
	t.Helper()
	argsLog = filepath.Join(dir, "ffmpeg_args.log")
	probeJSONPath := filepath.Join(dir, "ffprobe_out.json")
	probePayload := `{"format":{"format_name":"mov,mp4,m4a","duration":"6.000"},"streams":[{"codec_type":"video","codec_name":"h264","width":1280,"height":720,"avg_frame_rate":"24/1"},{"codec_type":"audio","codec_name":"aac"}]}`
	if err := os.WriteFile(probeJSONPath, []byte(probePayload), 0o600); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		ffprobeExe = filepath.Join(dir, "ffprobe.cmd")
		ffprobeScript := "@echo off\r\ntype \"" + probeJSONPath + "\"\r\n"
		if err := os.WriteFile(ffprobeExe, []byte(ffprobeScript), 0o700); err != nil {
			t.Fatal(err)
		}
		ffmpegExe = filepath.Join(dir, "ffmpeg.cmd")
		// Record invocation args and create last argument path with non-empty bytes
		ffmpegScript := "@echo off\r\n" +
			"echo %*>> \"" + argsLog + "\"\r\n" +
			"setlocal enabledelayedexpansion\r\n" +
			"set \"last=\"\r\n" +
			":loop\r\n" +
			"if \"%~1\"==\"\" goto done\r\n" +
			"set \"last=%~1\"\r\n" +
			"shift\r\n" +
			"goto loop\r\n" +
			":done\r\n" +
			"if not \"%last%\"==\"-\" if not \"%last%\"==\"NUL\" (\r\n" +
			"  echo FAKEMEDIA> \"%last%\"\r\n" +
			")\r\n"
		if err := os.WriteFile(ffmpegExe, []byte(ffmpegScript), 0o700); err != nil {
			t.Fatal(err)
		}
		return ffprobeExe, ffmpegExe, argsLog
	}

	ffprobeExe = filepath.Join(dir, "ffprobe")
	ffprobeScript := "#!/bin/sh\necho '{\"format\":{\"format_name\":\"mp4\",\"duration\":\"6.000\"},\"streams\":[{\"codec_type\":\"video\",\"codec_name\":\"h264\",\"width\":1280,\"height\":720,\"avg_frame_rate\":\"24/1\"},{\"codec_type\":\"audio\",\"codec_name\":\"aac\"}]}'\n"
	if err := os.WriteFile(ffprobeExe, []byte(ffprobeScript), 0o755); err != nil {
		t.Fatal(err)
	}
	ffmpegExe = filepath.Join(dir, "ffmpeg")
	ffmpegScript := "#!/bin/sh\necho \"$@\" >> \"" + argsLog + "\"\nlast=\"\"\nfor a in \"$@\"; do last=\"$a\"; done\nif [ \"$last\" != \"-\" ] && [ \"$last\" != \"/dev/null\" ]; then echo FAKEMEDIA > \"$last\"; fi\n"
	if err := os.WriteFile(ffmpegExe, []byte(ffmpegScript), 0o755); err != nil {
		t.Fatal(err)
	}
	return ffprobeExe, ffmpegExe, argsLog
}

func TestPhase2BatchPreflightAndMaskEdit(t *testing.T) {
	tmp := t.TempDir()
	mediaStub := &stubPhase2Media{outDir: tmp}
	opts := Options{
		ImageGen:        mediaStub,
		ProviderChecker: &stubPreflightDeps{},
		CostEstimator:   &stubPreflightDeps{},
		MediaDir:        tmp,
	}

	preOutRaw, err := NewBatchPreflightNode(opts).Run(context.Background(), mustJSON(t, contracts.BatchPreflightInput{
		Capability:   "video",
		ItemCount:    4,
		UnitsPerItem: 5,
	}))
	if err != nil {
		t.Fatalf("batch_preflight: %v", err)
	}
	preOut := preOutRaw.(contracts.BatchPreflightOutput)
	if !preOut.Ready || preOut.EstimatedUSD != 2.0 || preOut.Unit != "second" {
		t.Fatalf("unexpected preflight output: %+v", preOut)
	}

	// Blocker case
	blockedOpts := opts
	blockedOpts.ProviderChecker = &stubPreflightDeps{block: true}
	blockedRaw, err := NewBatchPreflightNode(blockedOpts).Run(context.Background(), mustJSON(t, contracts.BatchPreflightInput{Capability: "video", ItemCount: 2}))
	if err != nil || blockedRaw.(contracts.BatchPreflightOutput).Ready {
		t.Fatalf("expected Ready=false on credential blocker, got %+v err=%v", blockedRaw, err)
	}

	// MaskEdit passes MaskPath and checks FileGuard
	baseImg := filepath.Join(tmp, "shot.png")
	maskImg := filepath.Join(tmp, "mask.png")
	writeSolidPNG(baseImg, 16, 16, color.RGBA{R: 255, A: 255})
	writeSolidPNG(maskImg, 16, 16, color.RGBA{R: 255, G: 255, B: 255, A: 255})

	guardCtx := engine.WithFileGuard(context.Background(), func(p string, _ bool) error {
		if strings.Contains(p, "mask.png") {
			return errors.New("denied mask read")
		}
		return nil
	})
	if _, err := NewMaskEditNode(opts).Run(guardCtx, mustJSON(t, contracts.MaskEditInput{
		ImagePath: engine.InputRef(baseImg, engine.MediaImage),
		MaskPath:  engine.InputRef(maskImg, engine.MediaImage),
		Prompt:    "add hat",
		OutputDir: tmp,
	})); err == nil {
		t.Fatal("expected FileGuard to reject denied mask path")
	}

	mOutRaw, err := NewMaskEditNode(opts).Run(context.Background(), mustJSON(t, contracts.MaskEditInput{
		ImagePath: engine.InputRef(baseImg, engine.MediaImage),
		MaskPath:  engine.InputRef(maskImg, engine.MediaImage),
		Prompt:    "change coat to red",
		OutputDir: tmp,
	}))
	if err != nil {
		t.Fatalf("mask_edit: %v", err)
	}
	mOut := mOutRaw.(contracts.MaskEditOutput)
	if mediaStub.lastImageReq.MaskPath != maskImg || len(mediaStub.lastImageReq.ReferenceImages) != 1 || mOut.Path == "" {
		t.Fatalf("mask_edit did not forward MaskPath cleanly: req=%+v out=%+v", mediaStub.lastImageReq, mOut)
	}
}

func TestPhase2GridSplitAsymmetricCropsAndGuard(t *testing.T) {
	tmp := t.TempDir()
	wsDir := filepath.Join(tmp, "ws")
	mediaDir := filepath.Join(tmp, "media")
	outsideDir := t.TempDir()
	_ = os.MkdirAll(wsDir, 0o755)
	_ = os.MkdirAll(mediaDir, 0o755)

	gridPath := filepath.Join(mediaDir, "grid.png")
	outsideGrid := filepath.Join(outsideDir, "outside.png")
	// Construct a 100x80 image where left-top=Red, right-top=Green, left-bottom=Blue, right-bottom=Yellow
	img := image.NewRGBA(image.Rect(0, 0, 100, 80))
	for y := 0; y < 80; y++ {
		for x := 0; x < 100; x++ {
			var c color.RGBA
			switch {
			case x < 40 && y < 30:
				c = color.RGBA{R: 255, A: 255}
			case x >= 40 && y < 30:
				c = color.RGBA{G: 255, A: 255}
			case x < 40 && y >= 30:
				c = color.RGBA{B: 255, A: 255}
			default:
				c = color.RGBA{R: 255, G: 255, B: 255, A: 255}
			}
			img.SetRGBA(x, y, c)
		}
	}
	f, _ := os.Create(gridPath)
	_ = png.Encode(f, img)
	_ = f.Close()
	writeSolidPNG(outsideGrid, 40, 40, color.RGBA{R: 255, A: 255})

	policy := agent.NewAccessPolicy(filepath.Join(tmp, "data"), wsDir, mediaDir, filepath.Join(tmp, "profiles"), nil)
	guardedCtx := engine.WithFileGuard(context.Background(), policy.CheckTrustedFile)
	node := NewGridSplitNode(Options{MediaDir: mediaDir, WorkspaceDir: wsDir})

	if _, err := node.Run(guardedCtx, mustJSON(t, contracts.GridSplitInput{
		ImagePath: engine.InputRef(outsideGrid, engine.MediaImage),
		OutputDir: mediaDir,
	})); err == nil || !apperr.IsCode(err, apperr.CodeUnauthorized) {
		t.Fatalf("expected AccessPolicy unauthorized error on outside image_path, got %v", err)
	}
	if _, err := node.Run(guardedCtx, mustJSON(t, contracts.GridSplitInput{
		ImagePath: engine.InputRef(gridPath, engine.MediaImage),
		OutputDir: outsideDir,
	})); err == nil || !apperr.IsCode(err, apperr.CodeUnauthorized) {
		t.Fatalf("expected AccessPolicy unauthorized error on outside output_dir, got %v", err)
	}

	outRaw, err := node.Run(guardedCtx, json.RawMessage(`{
		"image_path": `+strconvQuote(gridPath)+`,
		"rows": 2,
		"cols": 2,
		"row_weights": "30, 50",
		"col_weights": "40, 60",
		"output_dir": `+strconvQuote(mediaDir)+`
	}`))
	if err != nil {
		t.Fatalf("grid_split: %v", err)
	}
	out := outRaw.(contracts.GridSplitOutput)
	if len(out.Panels) != 4 || len(out.Images) != 4 || out.Panels[0].Width != 40 || out.Panels[0].Height != 30 || out.Panels[3].Width != 60 || out.Panels[3].Height != 50 {
		t.Fatalf("unexpected panel geometries: panels=%+v images=%+v", out.Panels, out.Images)
	}
	f0, _ := os.Open(out.Panels[0].Path)
	p0, _ := png.Decode(f0)
	_ = f0.Close()
	r, g, b, _ := p0.At(5, 5).RGBA()
	if r == 0 || g != 0 || b != 0 {
		t.Fatalf("panel 0 top-left should be red, got r=%d g=%d b=%d", r, g, b)
	}
}

func strconvQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func TestPhase2ExtractFramesTTSDubMuxLipSyncAndQCReview(t *testing.T) {
	tmp := t.TempDir()
	wsDir := filepath.Join(tmp, "ws")
	stagingDir := filepath.Join(tmp, "staging")
	_ = os.MkdirAll(wsDir, 0o755)
	_ = os.MkdirAll(stagingDir, 0o755)

	ffprobeExe, ffmpegExe, argsLog := writePhase2FakeBinaries(t, tmp)
	vidPath := filepath.Join(tmp, "shot.mp4")
	audPath := filepath.Join(tmp, "voice.mp3")
	refPath := filepath.Join(tmp, "char.png")
	_ = os.WriteFile(vidPath, []byte("MP4SRC"), 0o600)
	_ = os.WriteFile(audPath, []byte("MP3SRC"), 0o600)
	writeSolidPNG(refPath, 16, 16, color.RGBA{B: 255, A: 255})

	mediaStub := &stubPhase2Media{outDir: tmp}
	opts := Options{
		TextGen:    mediaStub,
		ImageGen:   mediaStub,
		TTSGen:     mediaStub,
		LipSyncGen: mediaStub,
		Binaries: &stubBinaryResolver{paths: map[domainbinaries.BinaryID]string{
			domainbinaries.IDFFprobe: ffprobeExe,
			domainbinaries.IDFFmpeg:  ffmpegExe,
		}},
		MediaDir:     tmp,
		WorkspaceDir: wsDir,
		StagingDir:   stagingDir,
	}

	// Extract last frame
	efRaw, err := NewExtractFramesNode(opts).Run(context.Background(), mustJSON(t, contracts.ExtractFramesInput{
		VideoPath: engine.InputRef(vidPath, engine.MediaVideo), Mode: "last", OutputDir: tmp,
	}))
	if err != nil {
		t.Fatalf("extract_frames: %v", err)
	}
	efOut := efRaw.(contracts.ExtractFramesOutput)
	if len(efOut.Frames) != 1 || efOut.LastFramePath == "" {
		t.Fatalf("unexpected extract_frames output: %+v", efOut)
	}

	// TTS Dub Mux (synthesizing TTS + sidechaincompress ducking)
	duckTrue := true
	dubRaw, err := NewTTSDubMuxNode(opts).Run(context.Background(), mustJSON(t, contracts.TTSDubMuxInput{
		VideoPath: engine.InputRef(vidPath, engine.MediaVideo), Text: "Xin chao moi nguoi", Voice: "nova", DuckOriginal: &duckTrue, OutputDir: tmp,
	}))
	if err != nil {
		t.Fatalf("tts_dub_mux: %v", err)
	}
	dubOut := dubRaw.(contracts.TTSDubMuxOutput)
	if dubOut.Path == "" || mediaStub.lastTTSReq.Voice != "nova" {
		t.Fatalf("unexpected tts_dub_mux output: %+v", dubOut)
	}
	logBytes, _ := os.ReadFile(argsLog)
	if !strings.Contains(string(logBytes), "sidechaincompress") {
		t.Fatalf("expected sidechaincompress filter in ffmpeg args, got: %s", string(logBytes))
	}

	// LipSync Generate
	lsRaw, err := NewLipSyncGenerateNode(opts).Run(context.Background(), mustJSON(t, contracts.LipSyncGenerateInput{
		VideoPath: engine.InputRef(vidPath, engine.MediaVideo), AudioPath: engine.InputRef(audPath, engine.MediaAudio), FaceIndex: 1, OutputDir: tmp,
	}))
	if err != nil {
		t.Fatalf("lipsync_generate: %v", err)
	}
	if lsRaw.(contracts.LipSyncGenerateOutput).Path == "" || mediaStub.lastLipSyncReq.FaceIndex != 1 {
		t.Fatalf("unexpected lipsync output: %+v", lsRaw)
	}

	// Extract frames using timestamps_csv + verify FileGuard runs before os.Stat
	efCSVRaw, err := NewExtractFramesNode(opts).Run(context.Background(), mustJSON(t, contracts.ExtractFramesInput{
		VideoPath: engine.InputRef(vidPath, engine.MediaVideo), Mode: "timestamps", TimestampsCSV: "1.0, 3.5", OutputDir: tmp,
	}))
	if err != nil {
		t.Fatalf("extract_frames timestamps_csv: %v", err)
	}
	if len(efCSVRaw.(contracts.ExtractFramesOutput).Frames) != 2 {
		t.Fatalf("expected 2 frames from timestamps_csv, got %+v", efCSVRaw)
	}
	denyReadCtx := engine.WithFileGuard(context.Background(), func(string, bool) error {
		return errors.New("server mode denied read")
	})
	if _, err := NewExtractFramesNode(opts).Run(denyReadCtx, mustJSON(t, contracts.ExtractFramesInput{
		VideoPath: engine.InputRef(filepath.Join(tmp, "nonexistent_should_still_guard_first.mp4"), engine.MediaVideo), Mode: "last", OutputDir: tmp,
	})); err == nil || !strings.Contains(err.Error(), "server mode denied read") {
		t.Fatalf("expected CheckFile to reject guarded path before stat, got: %v", err)
	}

	// QC Review multimodal on video + reference image
	qcRaw, err := NewQCReviewNode(opts).Run(context.Background(), mustJSON(t, contracts.QCReviewInput{
		MediaPath:      engine.InputRef(vidPath, engine.MediaVideo),
		TargetKind:     "video",
		ReferencePaths: engine.InputRefs([]string{refPath}, engine.MediaImage),
		PromptExpected: "boy in blue jacket",
		SampleCount:    2,
	}))
	if err != nil {
		t.Fatalf("qc_review: %v", err)
	}
	qcOut := qcRaw.(contracts.QCReviewOutput)
	if qcOut.Verdict != "pass" || qcOut.OverallScore < 9.0 || len(mediaStub.lastTextReq.ImageInputs) != 3 || qcOut.Report == "" {
		t.Fatalf("unexpected qc_review output (%+v), imageInputs=%v", qcOut, mediaStub.lastTextReq.ImageInputs)
	}
	// Ensure zero repo CWD pollution and zero leftover run-* staging directories
	if _, serr := os.Stat("projects"); !os.IsNotExist(serr) {
		t.Fatalf("unexpected 'projects' directory created in package CWD: %v", serr)
	}
	entries, _ := os.ReadDir(filepath.Join(stagingDir, "qc_samples"))
	if len(entries) != 0 {
		t.Fatalf("expected temporary qc_samples/run-* directories cleaned up, found %d", len(entries))
	}
}

func TestPhase2RunGraphMediaPortWiringAndSpecDefaults(t *testing.T) {
	tmp := t.TempDir()
	wsDir := filepath.Join(tmp, "ws")
	stagingDir := filepath.Join(tmp, "staging")
	_ = os.MkdirAll(wsDir, 0o755)
	_ = os.MkdirAll(stagingDir, 0o755)

	ffprobeExe, ffmpegExe, argsLog := writePhase2FakeBinaries(t, tmp)
	// Override fake ffmpeg on extract_frames/dub so extracted .png is a valid PNG for downstream grid_split!
	validPNG := filepath.Join(tmp, "valid_frame.png")
	writeSolidPNG(validPNG, 64, 64, color.RGBA{R: 200, G: 80, B: 40, A: 255})
	if runtime.GOOS == "windows" {
		ffmpegScript := "@echo off\r\n" +
			"echo %*>> \"" + argsLog + "\"\r\n" +
			"setlocal enabledelayedexpansion\r\n" +
			"set \"last=\"\r\n" +
			":loop\r\n" +
			"if \"%~1\"==\"\" goto done\r\n" +
			"set \"last=%~1\"\r\n" +
			"shift\r\n" +
			"goto loop\r\n" +
			":done\r\n" +
			"if \"%last%\"==\"-\" exit /b 0\r\n" +
			"copy /Y \"" + validPNG + "\" \"%last%\" >NUL\r\n" +
			"echo out_time_us=2000000\r\n" +
			"echo progress=end\r\n"
		_ = os.WriteFile(ffmpegExe, []byte(ffmpegScript), 0o700)
	} else {
		ffmpegScript := "#!/bin/sh\necho \"$@\" >> \"" + argsLog + "\"\nfor last; do :; done\nif [ \"$last\" = \"-\" ]; then exit 0; fi\ncp \"" + validPNG + "\" \"$last\"\necho \"out_time_us=2000000\"\necho \"progress=end\"\n"
		_ = os.WriteFile(ffmpegExe, []byte(ffmpegScript), 0o755)
	}

	vidPath := filepath.Join(tmp, "source.mp4")
	audPath := filepath.Join(tmp, "dialog.mp3")
	maskPath := filepath.Join(tmp, "mask.png")
	_ = os.WriteFile(vidPath, []byte("MP4"), 0o600)
	_ = os.WriteFile(audPath, []byte("MP3"), 0o600)
	writeSolidPNG(maskPath, 64, 64, color.RGBA{R: 255, G: 255, B: 255, A: 255})

	mediaStub := &stubPhase2Media{outDir: tmp}
	freePre := &stubPreflightDeps{freeRate: true}
	opts := Options{
		TextGen:         mediaStub,
		ImageGen:        mediaStub,
		TTSGen:          mediaStub,
		LipSyncGen:      mediaStub,
		ProviderChecker: freePre,
		CostEstimator:   freePre,
		Binaries: &stubBinaryResolver{paths: map[domainbinaries.BinaryID]string{
			domainbinaries.IDFFprobe: ffprobeExe,
			domainbinaries.IDFFmpeg:  ffmpegExe,
		}},
		MediaDir:     tmp,
		WorkspaceDir: wsDir,
		StagingDir:   stagingDir,
	}

	reg := engine.NewRegistry()
	if err := reg.Register(NewNodes(opts)...); err != nil {
		t.Fatal(err)
	}

	// 1. Minimal "{}" input on batch_preflight applies Spec defaults AND honors $0.00 free catalog rate!
	preRaw, err := reg.Run(context.Background(), NodeTypeBatchPreflight, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("batch_preflight {}: %v", err)
	}
	preOut := preRaw.(contracts.BatchPreflightOutput)
	if !preOut.Ready || preOut.UnitPriceUSD != 0 || preOut.EstimatedUSD != 0 || preOut.Warnings == nil || preOut.Blockers == nil || preOut.BlockerItems == nil {
		t.Fatalf("expected $0.00 free rate and non-nil slices on {}, got %+v", preOut)
	}

	// 2. Full Phase 2 media-port DAG:
	// extract_frames.last_frame -> grid_split.image_path -> qc_review.reference_paths
	// extract_frames.last_frame -> mask_edit.image_path -> qc_review.media_path
	// tts_dub_mux (DuckOriginal omitted -> Spec default true!) -> lipsync_generate.video_path
	_ = os.Remove(argsLog)
	graphRes, err := reg.RunGraph(context.Background(), engine.Graph{
		Nodes: []engine.GraphNode{
			{
				ID:     "ef",
				Type:   NodeTypeExtractFrames,
				Params: mustJSON(t, map[string]any{"video_path": vidPath, "mode": "last", "output_dir": tmp}),
			},
			{
				ID:     "split",
				Type:   NodeTypeGridSplit,
				Params: mustJSON(t, map[string]any{"rows": 2, "cols": 2, "output_dir": tmp}),
			},
			{
				ID:     "mask",
				Type:   NodeTypeMaskEdit,
				Params: mustJSON(t, map[string]any{"mask_path": maskPath, "prompt": "clean jacket", "output_dir": tmp}),
			},
			{
				ID:     "qc",
				Type:   NodeTypeQCReview,
				Params: mustJSON(t, map[string]any{"prompt_expected": "clean jacket"}),
			},
			{
				ID:     "dub",
				Type:   NodeTypeTTSDubMux,
				Params: mustJSON(t, map[string]any{"video_path": vidPath, "audio_path": audPath, "output_dir": tmp}),
			},
			{
				ID:     "ls",
				Type:   NodeTypeLipSyncGenerate,
				Params: mustJSON(t, map[string]any{"audio_path": audPath, "output_dir": tmp}),
			},
		},
		Edges: []engine.Edge{
			{From: engine.Endpoint{Node: "ef", Port: "last_frame"}, To: engine.Endpoint{Node: "split", Port: "image_path"}},
			{From: engine.Endpoint{Node: "ef", Port: "last_frame"}, To: engine.Endpoint{Node: "mask", Port: "image_path"}},
			{From: engine.Endpoint{Node: "mask", Port: "image"}, To: engine.Endpoint{Node: "qc", Port: "media_path"}},
			{From: engine.Endpoint{Node: "split", Port: "images"}, To: engine.Endpoint{Node: "qc", Port: "reference_paths"}},
			{From: engine.Endpoint{Node: "dub", Port: "video"}, To: engine.Endpoint{Node: "ls", Port: "video_path"}},
		},
	})
	if err != nil || !graphRes.Done() {
		t.Fatalf("Phase 2 RunGraph failed: err=%v nodes=%+v", err, graphRes.Nodes)
	}

	// Verify DuckOriginal omitted in dub node defaulted to true (sidechaincompress present in FFmpeg args)
	argsBytes, _ := os.ReadFile(argsLog)
	if !strings.Contains(string(argsBytes), "sidechaincompress") {
		t.Fatalf("expected omitted duck_original to default to true (sidechaincompress), got:\n%s", string(argsBytes))
	}
}

func TestPhase2QCReviewFailsOnCorruptMediaAndPopulatesFFprobeJSON(t *testing.T) {
	tmp := t.TempDir()
	wsDir := filepath.Join(tmp, "ws")
	_ = os.MkdirAll(wsDir, 0o755)

	// 1. Corrupt image with Binaries == nil fails technical QC instead of false-pass 9.0
	corruptImg := filepath.Join(tmp, "corrupt.png")
	_ = os.WriteFile(corruptImg, []byte("NOT_A_VALID_PNG"), 0o600)
	qcNoBin := NewQCReviewNode(Options{MediaDir: tmp, WorkspaceDir: wsDir})
	rawCorruptImg, err := qcNoBin.Run(context.Background(), mustJSON(t, contracts.QCReviewInput{
		MediaPath:  engine.InputRef(corruptImg, engine.MediaImage),
		SkipVision: true,
	}))
	if err != nil {
		t.Fatalf("qc_review corrupt image: %v", err)
	}
	outCorruptImg := rawCorruptImg.(contracts.QCReviewOutput)
	if outCorruptImg.Verdict != "fail" || outCorruptImg.OverallScore > 4.0 || len(outCorruptImg.Issues) == 0 {
		t.Fatalf("expected Verdict=fail on corrupt image, got %+v", outCorruptImg)
	}

	// 2. Failing ffprobe on corrupt/zero-byte video with SkipVision:true fails QC instead of false-pass 9.0
	failProbe := filepath.Join(tmp, "ffprobe_fail.cmd")
	if runtime.GOOS == "windows" {
		_ = os.WriteFile(failProbe, []byte("@echo off\r\necho invalid data found 1>&2\r\nexit /b 1\r\n"), 0o700)
	} else {
		failProbe = filepath.Join(tmp, "ffprobe_fail.sh")
		_ = os.WriteFile(failProbe, []byte("#!/bin/sh\necho 'invalid data found' >&2\nexit 1\n"), 0o755)
	}
	corruptVid := filepath.Join(tmp, "zero.mp4")
	_ = os.WriteFile(corruptVid, nil, 0o600)
	qcFailProbe := NewQCReviewNode(Options{
		MediaDir:     tmp,
		WorkspaceDir: wsDir,
		Binaries:     &stubBinaryResolver{paths: map[domainbinaries.BinaryID]string{domainbinaries.IDFFprobe: failProbe}},
	})
	rawCorruptVid, err := qcFailProbe.Run(context.Background(), mustJSON(t, contracts.QCReviewInput{
		MediaPath:  engine.InputRef(corruptVid, engine.MediaVideo),
		SkipVision: true,
	}))
	if err != nil {
		t.Fatalf("qc_review corrupt video: %v", err)
	}
	outCorruptVid := rawCorruptVid.(contracts.QCReviewOutput)
	if outCorruptVid.Verdict != "fail" || len(outCorruptVid.Issues) == 0 || outCorruptVid.SampleFrames == nil {
		t.Fatalf("expected Verdict=fail and non-nil SampleFrames on corrupt video, got %+v", outCorruptVid)
	}

	// 3. Valid ffprobe populates non-empty FFprobeJSON without leaking host directory path
	ffprobeExe, ffmpegExe, _ := writePhase2FakeBinaries(t, tmp)
	validVid := filepath.Join(tmp, "clean.mp4")
	_ = os.WriteFile(validVid, []byte("MP4"), 0o600)
	qcValid := NewQCReviewNode(Options{
		MediaDir:     tmp,
		WorkspaceDir: wsDir,
		Binaries: &stubBinaryResolver{paths: map[domainbinaries.BinaryID]string{
			domainbinaries.IDFFprobe: ffprobeExe,
			domainbinaries.IDFFmpeg:  ffmpegExe,
		}},
	})
	rawValid, err := qcValid.Run(context.Background(), mustJSON(t, contracts.QCReviewInput{
		MediaPath:  engine.InputRef(validVid, engine.MediaVideo),
		SkipVision: true,
	}))
	if err != nil {
		t.Fatalf("qc_review valid video: %v", err)
	}
	outValid := rawValid.(contracts.QCReviewOutput)
	if outValid.Verdict != "pass" || outValid.FFprobeJSON == "" || strings.Contains(outValid.FFprobeJSON, filepath.Base(tmp)) {
		t.Fatalf("expected Verdict=pass and clean FFprobeJSON, got %+v", outValid)
	}
}
