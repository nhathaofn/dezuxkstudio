package studio

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"app/internal/apperr"
	"app/internal/config"
	"app/internal/contracts"
	domainbinaries "app/internal/domain/binaries"
	domainmedia "app/internal/domain/mediagen"
	domainstudio "app/internal/domain/studio"
	"app/internal/engine"
	"app/internal/paths"
	"app/internal/progress"
	"app/internal/service/media"
)

type stubTextGenerator struct {
	mu       sync.Mutex
	requests []domainmedia.TextRequest
	err      error
}

func (s *stubTextGenerator) GenerateText(_ context.Context, req domainmedia.TextRequest) (domainmedia.TextResult, error) {
	s.mu.Lock()
	s.requests = append(s.requests, req)
	s.mu.Unlock()
	if s.err != nil {
		return domainmedia.TextResult{}, s.err
	}

	switch req.JSONSchemaName {
	case "normalized_script":
		payload := json.RawMessage(`{
			"title": "Rainy Neon",
			"genre": "drama",
			"aspect_ratio": "16:9",
			"logline": "A courier finds a glowing key in a midnight diner.",
			"language": "vi",
			"characters": ["Minh"],
			"scenes": [
				{
					"code": "S01",
					"title": "Midnight Diner",
					"location": "Old Cafe",
					"time_of_day": "NIGHT",
					"summary": "Minh enters out of the rain.",
					"beats": [
						{
							"code": "",
							"description": "Minh shakes off rain by the counter.",
							"visual_hint": "Neon reflection on wet jacket",
							"camera_hint": "Medium close-up",
							"duration_sec": 0,
							"asset_tags": ["@Minh", "@OldCafe"],
							"dialogues": [{"speaker": "Minh", "line": "Cho một ly đen nóng.", "emotion": "tired"}]
						}
					]
				}
			]
		}`)
		return domainmedia.TextResult{
			Text:       string(payload),
			ParsedJSON: payload,
			Provider:   "MockLLM",
			Model:      "mock-text-1",
		}, nil

	case "asset_bible":
		payload := json.RawMessage(`{
			"style_prompt": "Neo-noir anime realism",
			"assets": [
				{
					"kind": "character",
					"name": "Minh",
					"code_tag": "minh",
					"description": "Courier in wet trench coat",
					"prompt": "3-view turnaround sheet of Minh in dark trench coat, neutral studio backdrop"
				},
				{
					"kind": "character",
					"name": "Minh Duplicate",
					"code_tag": "@Minh",
					"description": "Duplicate tag should be deduplicated",
					"prompt": "Duplicate"
				},
				{
					"kind": "scene",
					"name": "Old Cafe",
					"code_tag": "@OldCafe",
					"description": "Rain-streaked retro cafe window with amber neon",
					"prompt": "Wide establishing concept sheet of retro Asian cafe interior at night"
				}
			]
		}`)
		return domainmedia.TextResult{
			Text:       string(payload),
			ParsedJSON: payload,
			Provider:   "MockLLM",
			Model:      "mock-text-1",
		}, nil

	case "shot_prompt_package":
		payload := json.RawMessage(`{
			"episode_title": "Rainy Neon",
			"production_mode": "storyboard_i2v",
			"shots": [
				{
					"seq": 9,
					"scene_code": "S01",
					"title": "Diner Entrance",
					"shot_mode": "",
					"duration_sec": 5,
					"image_prompt": "Medium close-up of @Minh inside @OldCafe shaking rain off coat",
					"video_prompt": "Slow dolly-in as @Minh steps to counter and speaks: Cho một ly đen nóng.",
					"narration_text": "",
					"ref_asset_tags": ["minh", "@OldCafe"],
					"dialogues": [{"speaker": "Minh", "line": "Cho một ly đen nóng.", "emotion": "tired"}]
				}
			]
		}`)
		return domainmedia.TextResult{
			Text:       string(payload),
			ParsedJSON: payload,
			Provider:   "MockLLM",
			Model:      "mock-text-1",
		}, nil

	default:
		return domainmedia.TextResult{}, errors.New("unexpected schema: " + req.JSONSchemaName)
	}
}

type stubBinaryResolver struct {
	paths map[domainbinaries.BinaryID]string
}

func (s *stubBinaryResolver) Resolve(_ context.Context, id domainbinaries.BinaryID) (string, domainbinaries.Source, string, error) {
	if p, ok := s.paths[id]; ok {
		return p, domainbinaries.SourceCustom, p, nil
	}
	return "", domainbinaries.SourceNone, "", nil
}

type stubMediaRegistrar struct {
	mu    sync.Mutex
	calls []media.RegisterAssetParams
}

func (s *stubMediaRegistrar) RegisterAsset(_ context.Context, params media.RegisterAssetParams) (*contracts.MediaAssetItem, error) {
	s.mu.Lock()
	s.calls = append(s.calls, params)
	s.mu.Unlock()
	return &contracts.MediaAssetItem{
		ID:         "asset-concat-1",
		Kind:       params.Kind,
		SourceNode: params.SourceNode,
		FilePath:   params.FilePath,
	}, nil
}

func writeFakeFFmpegPair(t *testing.T, dir string, withAudio bool) (ffprobeExe, ffmpegExe string) {
	t.Helper()
	streamsJSON := `[
		{"codec_type":"video","codec_name":"h264","width":1280,"height":720,"r_frame_rate":"24/1","avg_frame_rate":"24/1","duration":"6.0"}`
	if withAudio {
		streamsJSON += `,
		{"codec_type":"audio","codec_name":"aac","duration":"6.0"}`
	}
	streamsJSON += `]`
	probePayload := `{"format":{"format_name":"mov,mp4,m4a,3gp,3g2,mj2","duration":"6.0"},"streams":` + streamsJSON + `}`

	probeJSONPath := filepath.Join(dir, "ffprobe_out.json")
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
		ffmpegScript := "@echo off\r\n" +
			":loop\r\n" +
			"if \"%~2\"==\"\" goto done\r\n" +
			"shift\r\n" +
			"goto loop\r\n" +
			":done\r\n" +
			"set \"LAST=%~1\"\r\n" +
			"if \"%LAST%\"==\"-\" (\r\n" +
			"  echo [silencedetect @ 000] silence_duration: 1.500 1>&2\r\n" +
			"  exit /b 0\r\n" +
			")\r\n" +
			"echo out_time_us=3000000\r\n" +
			"echo progress=end\r\n" +
			"echo fake-mp4> \"%LAST%\"\r\n" +
			"exit /b 0\r\n"
		if err := os.WriteFile(ffmpegExe, []byte(ffmpegScript), 0o700); err != nil {
			t.Fatal(err)
		}
		return ffprobeExe, ffmpegExe
	}

	ffprobeExe = filepath.Join(dir, "ffprobe.sh")
	if err := os.WriteFile(ffprobeExe, []byte("#!/bin/sh\ncat \""+probeJSONPath+"\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	ffmpegExe = filepath.Join(dir, "ffmpeg.sh")
	ffmpegSh := "#!/bin/sh\nfor last; do :; done\nif [ \"$last\" = \"-\" ]; then\n  echo \"[silencedetect @ 000] silence_duration: 1.500\" >&2\n  exit 0\nfi\necho \"out_time_us=3000000\"\necho \"progress=end\"\nprintf 'fake-mp4' > \"$last\"\n"
	if err := os.WriteFile(ffmpegExe, []byte(ffmpegSh), 0o755); err != nil {
		t.Fatal(err)
	}
	return ffprobeExe, ffmpegExe
}

func TestNewNodesValidateSpecs(t *testing.T) {
	nodes := NewNodes(Options{})
	if len(nodes) != 17 {
		t.Fatalf("expected 17 studio nodes, got %d", len(nodes))
	}
	reg := engine.NewRegistry()
	for _, n := range nodes {
		if err := engine.ValidateSpec(n.Spec()); err != nil {
			t.Fatalf("ValidateSpec(%s): %v", n.Spec().Type, err)
		}
		if err := reg.Register(n); err != nil {
			t.Fatalf("Register(%s): %v", n.Spec().Type, err)
		}
	}
	if err := reg.Check(); err != nil {
		t.Fatalf("reg.Check(): %v", err)
	}
}

func TestScriptNormalizeAssetsShotsAndGraph(t *testing.T) {
	wsDir := t.TempDir()
	projRoot := filepath.Join(wsDir, "projects")
	epDir := filepath.Join(projRoot, "rainy-neon", "episodes", "ep01")
	if err := os.MkdirAll(epDir, 0o755); err != nil {
		t.Fatal(err)
	}

	textGen := &stubTextGenerator{}
	opts := Options{
		TextGen:      textGen,
		WorkspaceDir: wsDir,
		MediaDir:     filepath.Join(wsDir, "media"),
		WorkspaceTreeFunc: func() paths.WorkspaceTree {
			return paths.WorkspaceTree{
				Root:         wsDir,
				WorkspaceDir: wsDir,
				ProjectsDir:  projRoot,
				MediaDir:     filepath.Join(wsDir, "media"),
				VideosDir:    filepath.Join(wsDir, "media", "videos"),
			}
		},
	}

	reg := engine.NewRegistry()
	if err := reg.Register(NewNodes(opts)...); err != nil {
		t.Fatal(err)
	}

	// FileGuard denying writes outside epDir should block project_dir outside allowed root.
	outsideDir := t.TempDir()
	guardedCtx := engine.WithFileGuard(context.Background(), func(path string, write bool) error {
		clean := filepath.Clean(path)
		if !strings.HasPrefix(strings.ToLower(clean), strings.ToLower(filepath.Clean(projRoot))) {
			return apperr.New(apperr.CodeUnauthorized, "outside projects root")
		}
		return nil
	})

	_, err := reg.Run(guardedCtx, NodeTypeScriptNormalize, mustJSON(t, map[string]any{
		"raw_script":  "EXT. DINER - NIGHT",
		"project_dir": outsideDir,
	}))
	if err == nil || !apperr.IsCode(err, apperr.CodeUnauthorized) {
		t.Fatalf("expected FileGuard unauthorized error for outside project_dir, got %v", err)
	}

	// Full DAG: script_normalize -> extract_assets -> author_shot_prompts
	res, err := reg.RunGraph(guardedCtx, engine.Graph{
		Nodes: []engine.GraphNode{
			{
				ID:   "norm",
				Type: NodeTypeScriptNormalize,
				Params: mustJSON(t, map[string]any{
					"raw_script":   "Rainy midnight scene",
					"genre":        "drama",
					"aspect_ratio": "16:9",
					"project_dir":  filepath.Join("rainy-neon", "episodes", "ep01"),
				}),
			},
			{
				ID:   "assets",
				Type: NodeTypeExtractAssets,
				Params: mustJSON(t, map[string]any{
					"style_prompt": "Neo-noir",
					"project_dir":  filepath.Join("rainy-neon", "episodes", "ep01"),
				}),
			},
			{
				ID:   "shots",
				Type: NodeTypeAuthorShotPrompts,
				Params: mustJSON(t, map[string]any{
					"production_mode": "storyboard_i2v",
					"project_dir":     filepath.Join("rainy-neon", "episodes", "ep01"),
				}),
			},
		},
		Edges: []engine.Edge{
			{From: engine.Endpoint{Node: "norm", Port: "normalized_script_json"}, To: engine.Endpoint{Node: "assets", Port: "normalized_script_json"}},
			{From: engine.Endpoint{Node: "norm", Port: "normalized_script_json"}, To: engine.Endpoint{Node: "shots", Port: "normalized_script_json"}},
			{From: engine.Endpoint{Node: "assets", Port: "asset_bible_json"}, To: engine.Endpoint{Node: "shots", Port: "asset_bible_json"}},
		},
	})
	if err != nil {
		t.Fatalf("RunGraph: %v", err)
	}
	if !res.Done() {
		t.Fatalf("expected all graph nodes done, got %+v", res.Nodes)
	}

	var normOut contracts.ScriptNormalizeOutput
	if err := json.Unmarshal(res.Nodes["norm"].Output, &normOut); err != nil {
		t.Fatal(err)
	}
	if normOut.SceneCount != 1 || normOut.BeatCount != 1 {
		t.Fatalf("unexpected script counts: %+v", normOut)
	}
	if _, err := os.Stat(filepath.Join(epDir, "script", "normalized_script.json")); err != nil {
		t.Fatalf("expected normalized_script.json artifact: %v", err)
	}

	var assetsOut contracts.ExtractAssetsOutput
	if err := json.Unmarshal(res.Nodes["assets"].Output, &assetsOut); err != nil {
		t.Fatal(err)
	}
	if assetsOut.AssetCount != 2 {
		t.Fatalf("expected 2 deduplicated assets, got %d (%s)", assetsOut.AssetCount, assetsOut.AssetBibleJSON)
	}
	var bible domainstudio.AssetBiblePackage
	if err := json.Unmarshal([]byte(assetsOut.AssetBibleJSON), &bible); err != nil {
		t.Fatal(err)
	}
	if bible.Assets[0].CodeTag != "@minh" {
		t.Fatalf("expected normalized @tag '@minh', got %q", bible.Assets[0].CodeTag)
	}

	var shotsOut contracts.AuthorShotPromptsOutput
	if err := json.Unmarshal(res.Nodes["shots"].Output, &shotsOut); err != nil {
		t.Fatal(err)
	}
	if shotsOut.ShotCount != 1 {
		t.Fatalf("expected 1 shot, got %d", shotsOut.ShotCount)
	}
	var shotPkg domainstudio.ShotPromptPackage
	if err := json.Unmarshal([]byte(shotsOut.ShotsJSON), &shotPkg); err != nil {
		t.Fatal(err)
	}
	if shotPkg.Shots[0].Seq != 1 || shotPkg.Shots[0].ShotMode != domainstudio.ShotModeSingle {
		t.Fatalf("unexpected normalized shot: %+v", shotPkg.Shots[0])
	}
	if _, err := os.Stat(filepath.Join(epDir, "storyboard", "shots.json")); err != nil {
		t.Fatalf("expected shots.json artifact: %v", err)
	}
}

func TestProbeMediaAndVideoConcatBasic(t *testing.T) {
	wsDir := t.TempDir()
	mediaDir := filepath.Join(wsDir, "media")
	videosDir := filepath.Join(mediaDir, "videos")
	binDir := t.TempDir()
	if err := os.MkdirAll(videosDir, 0o755); err != nil {
		t.Fatal(err)
	}

	clip1 := filepath.Join(videosDir, "clip1.mp4")
	clip2 := filepath.Join(videosDir, "clip2.mp4")
	audioFile := filepath.Join(mediaDir, "voice.wav")
	for _, f := range []string{clip1, clip2, audioFile} {
		if err := os.WriteFile(f, []byte("dummy"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	ffprobeExe, ffmpegExe := writeFakeFFmpegPair(t, binDir, true)
	registrar := &stubMediaRegistrar{}
	cfg := config.Default(config.ModeDesktop).Studio

	opts := Options{
		Binaries: &stubBinaryResolver{
			paths: map[domainbinaries.BinaryID]string{
				domainbinaries.IDFFprobe: ffprobeExe,
				domainbinaries.IDFFmpeg:  ffmpegExe,
			},
		},
		Registrar:    registrar,
		StudioConfig: cfg,
		MediaDir:     mediaDir,
		WorkspaceDir: wsDir,
		WorkspaceTreeFunc: func() paths.WorkspaceTree {
			return paths.WorkspaceTree{
				Root:         wsDir,
				WorkspaceDir: wsDir,
				ProjectsDir:  filepath.Join(wsDir, "projects"),
				MediaDir:     mediaDir,
				VideosDir:    videosDir,
			}
		},
	}

	reg := engine.NewRegistry()
	if err := reg.Register(NewNodes(opts)...); err != nil {
		t.Fatal(err)
	}

	// Test probe_media with video media ref + audio port ref
	rawProbe, err := reg.Run(context.Background(), NodeTypeProbeMedia, mustJSON(t, map[string]any{
		"media": map[string]any{"kind": "video", "localUrl": "/media/videos/clip1.mp4"},
	}))
	if err != nil {
		t.Fatalf("Run(probe_media): %v", err)
	}
	probeOut := rawProbe.(contracts.ProbeMediaOutput)
	if probeOut.DurationSec != 6.0 || probeOut.Width != 1280 || probeOut.Height != 720 || probeOut.FPS != 24 || !probeOut.HasAudio || probeOut.VideoCodec != "h264" {
		t.Fatalf("unexpected probe output: %+v", probeOut)
	}
	if probeOut.SilentRatio < 0.24 || probeOut.SilentRatio > 0.26 {
		t.Fatalf("expected silent_ratio ~0.25 (1.5/6.0), got %v", probeOut.SilentRatio)
	}

	rawAudioProbe, err := reg.Run(context.Background(), NodeTypeProbeMedia, mustJSON(t, map[string]any{
		"audio": map[string]any{"kind": "audio", "localUrl": "voice.wav"},
	}))
	if err != nil {
		t.Fatalf("Run(probe_media audio): %v", err)
	}
	if !rawAudioProbe.(contracts.ProbeMediaOutput).HasAudio {
		t.Fatal("expected audio probe HasAudio=true")
	}

	// Test video_concat_basic with progress sink + MediaRegistrar
	var progressUpdates []progress.Update
	var pMu sync.Mutex
	pCtx := progress.With(context.Background(), progress.Sink{
		Report: func(u progress.Update) {
			pMu.Lock()
			progressUpdates = append(progressUpdates, u)
			pMu.Unlock()
		},
	})

	manifest := `["/media/videos/clip1.mp4", {"path": "/media/videos/clip2.mp4", "duration_sec": 6.0}]`
	rawConcat, err := reg.Run(pCtx, NodeTypeVideoConcatBasic, mustJSON(t, map[string]any{
		"clips_manifest_json":  manifest,
		"narration_audio_path": "/media/voice.wav",
		"resolution":           "1280x720",
		"fps":                  24,
	}))
	if err != nil {
		t.Fatalf("Run(video_concat_basic): %v", err)
	}
	concatOut := rawConcat.(contracts.VideoConcatBasicOutput)
	if concatOut.ClipCount != 2 || concatOut.AssetID != "asset-concat-1" || concatOut.OutputPath == "" {
		t.Fatalf("unexpected concat output: %+v", concatOut)
	}
	if _, err := os.Stat(concatOut.OutputPath); err != nil {
		t.Fatalf("expected assembled output file at %q: %v", concatOut.OutputPath, err)
	}

	pMu.Lock()
	gotProgress := len(progressUpdates)
	pMu.Unlock()
	if gotProgress < 3 {
		t.Fatalf("expected multiple progress updates (initial, ffmpeg pipe, done), got %d", gotProgress)
	}
	if len(registrar.calls) != 1 || registrar.calls[0].SourceNode != NodeTypeVideoConcatBasic || registrar.calls[0].Kind != "video" {
		t.Fatalf("unexpected registrar calls: %+v", registrar.calls)
	}

	// Missing binary should return unavailable error
	noBinReg := engine.NewRegistry()
	_ = noBinReg.Register(NewNodes(Options{MediaDir: mediaDir, WorkspaceDir: wsDir})...)
	_, err = noBinReg.Run(context.Background(), NodeTypeProbeMedia, mustJSON(t, map[string]any{
		"media": map[string]any{"kind": "video", "localUrl": clip1},
	}))
	if err == nil || !apperr.IsCode(err, apperr.CodeUnavailable) {
		t.Fatalf("expected unavailable error when Binaries resolver nil, got %v", err)
	}
}

func TestDirectNodeConstructorsApplyStudioDefaults(t *testing.T) {
	mediaDir := t.TempDir()
	binDir := t.TempDir()
	clip := filepath.Join(mediaDir, "clip.mp4")
	if err := os.WriteFile(clip, []byte("mp4"), 0o600); err != nil {
		t.Fatal(err)
	}
	ffprobeExe, ffmpegExe := writeFakeFFmpegPair(t, binDir, true)
	directOpts := Options{
		Binaries: &stubBinaryResolver{
			paths: map[domainbinaries.BinaryID]string{
				domainbinaries.IDFFprobe: ffprobeExe,
				domainbinaries.IDFFmpeg:  ffmpegExe,
			},
		},
		MediaDir: mediaDir,
	}

	reg := engine.NewRegistry()
	if err := reg.Register(NewProbeMediaNode(directOpts), NewVideoConcatBasicNode(directOpts)); err != nil {
		t.Fatal(err)
	}

	probeRaw, err := reg.Run(context.Background(), NodeTypeProbeMedia, mustJSON(t, map[string]any{
		"media": map[string]any{"kind": "video", "localUrl": clip},
	}))
	if err != nil {
		t.Fatalf("direct NewProbeMediaNode with empty StudioConfig failed: %v", err)
	}
	if probeRaw.(contracts.ProbeMediaOutput).DurationSec != 6.0 {
		t.Fatalf("unexpected probe duration: %+v", probeRaw)
	}

	concatRaw, err := reg.Run(context.Background(), NodeTypeVideoConcatBasic, mustJSON(t, map[string]any{
		"clips_manifest_json": []string{clip},
	}))
	if err != nil {
		t.Fatalf("direct NewVideoConcatBasicNode with empty StudioConfig failed: %v", err)
	}
	if concatRaw.(contracts.VideoConcatBasicOutput).ClipCount != 1 {
		t.Fatalf("unexpected concat output: %+v", concatRaw)
	}
}

func TestAuthorShotPromptsDefaultDurationOverrideAndEmptyResolveTree(t *testing.T) {
	// Empty Options must never resolve a relative "projects" directory in process CWD.
	tree := resolveTree(Options{})
	if tree.ProjectsDir != "" || resolveProjectsDir(Options{}) != "" {
		t.Fatalf("expected empty ProjectsDir for empty Options, got tree=%+v projects=%q", tree, resolveProjectsDir(Options{}))
	}

	// Verify Spec.Provider is unified across all 16 studio nodes.
	for _, n := range NewNodes(Options{}) {
		if n.Spec().Provider != "studio" {
			t.Fatalf("expected Provider=\"studio\" on %s, got %q", n.Spec().Type, n.Spec().Provider)
		}
	}

	cfg := config.Default(config.ModeDesktop).Studio
	cfg.DefaultShotDurationSec = 6
	cfg.MaxShotDurationSec = 12
	stub := &zeroDurShotStubTextGen{}
	node := NewAuthorShotPromptsNode(Options{
		TextGen:      stub,
		StudioConfig: cfg,
	})

	// 1. Uses StudioConfig.DefaultShotDurationSec (6s) when default_duration_sec omitted
	raw1, err := node.Run(context.Background(), json.RawMessage(`{"normalized_script_json":{"title":"Ep","genre":"drama","aspect_ratio":"16:9","logline":"L","scenes":[]}}`))
	if err != nil {
		t.Fatalf("author_shot_prompts default duration: %v", err)
	}
	var pkg1 domainstudio.ShotPromptPackage
	if err := json.Unmarshal([]byte(raw1.(contracts.AuthorShotPromptsOutput).ShotsJSON), &pkg1); err != nil {
		t.Fatal(err)
	}
	if len(pkg1.Shots) != 2 || pkg1.Shots[0].DurationSec != 6 || pkg1.Shots[1].DurationSec != 12 {
		t.Fatalf("expected shot0=6s (config default) and shot1=12s (clamped to MaxShotDurationSec), got %+v", pkg1.Shots)
	}

	// 2. Explicit default_duration_sec param overrides StudioConfig default
	raw2, err := node.Run(context.Background(), json.RawMessage(`{"normalized_script_json":{"title":"Ep","genre":"drama","aspect_ratio":"16:9","logline":"L","scenes":[]},"default_duration_sec":9}`))
	if err != nil {
		t.Fatalf("author_shot_prompts override duration: %v", err)
	}
	var pkg2 domainstudio.ShotPromptPackage
	if err := json.Unmarshal([]byte(raw2.(contracts.AuthorShotPromptsOutput).ShotsJSON), &pkg2); err != nil {
		t.Fatal(err)
	}
	if pkg2.Shots[0].DurationSec != 9 {
		t.Fatalf("expected shot0=9s from default_duration_sec param, got %+v", pkg2.Shots[0])
	}

	// 3. Out-of-bounds default_duration_sec rejected
	if _, err := node.Run(context.Background(), json.RawMessage(`{"normalized_script_json":"{}","default_duration_sec":99}`)); err == nil {
		t.Fatal("expected out-of-bounds default_duration_sec to fail validation")
	}
}

type zeroDurShotStubTextGen struct{}

func (z *zeroDurShotStubTextGen) GenerateText(_ context.Context, _ domainmedia.TextRequest) (domainmedia.TextResult, error) {
	payload := json.RawMessage(`{
		"episode_title":"Ep",
		"production_mode":"storyboard_i2v",
		"shots":[
			{"seq":1,"scene_code":"S01","title":"A","shot_mode":"single","duration_sec":0,"image_prompt":"img","video_prompt":"vid"},
			{"seq":2,"scene_code":"S01","title":"B","shot_mode":"single","duration_sec":40,"image_prompt":"img2","video_prompt":"vid2"}
		]
	}`)
	return domainmedia.TextResult{Text: string(payload), ParsedJSON: payload, Provider: "stub", Model: "m1"}, nil
}

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
