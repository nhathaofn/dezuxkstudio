package workflow_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"app/internal/domain/mediagen"
	"app/internal/engine"
	"app/internal/paths"
	"app/internal/progress"
	"app/internal/service/workflow"
	mediagennodes "app/internal/tools/mediagen"
	studionodes "app/internal/tools/studio"
	"app/internal/tools/watermark"
)

func createTestPNG(t *testing.T, dir, filename string) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 100, 100))
	draw.Draw(img, img.Bounds(), &image.Uniform{C: color.RGBA{R: 200, G: 100, B: 50, A: 255}}, image.Point{}, draw.Src)
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("failed to encode png: %v", err)
	}

	p := filepath.Join(dir, filename)
	if err := os.WriteFile(p, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("failed to write test png: %v", err)
	}
	return p
}

func TestWorkflowService_SpecsAndExecution(t *testing.T) {
	tempDir := t.TempDir()
	appPaths := paths.Paths{Data: tempDir}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	svc, err := workflow.New(workflow.Options{
		Paths:  appPaths,
		Logger: logger,
	})
	if err != nil {
		t.Fatalf("failed to create workflow service: %v", err)
	}

	specs := svc.Specs(context.Background())
	if len(specs) == 0 {
		t.Fatalf("expected at least 1 registered node spec, got 0")
	}

	hasWM := false
	for _, sp := range specs {
		if sp.Type == watermark.NodeType {
			hasWM = true
		}
	}
	if !hasWM {
		t.Fatalf("watermark node spec %s not found in registry", watermark.NodeType)
	}

	// Verify RunNode
	testImg := createTestPNG(t, tempDir, "svc_test.png")
	rawIn := json.RawMessage(`{"image": "` + filepath.ToSlash(testImg) + `"}`)
	out, err := svc.RunNode(context.Background(), watermark.NodeType, rawIn)
	if err != nil {
		t.Fatalf("RunNode failed: %v", err)
	}
	if out == nil {
		t.Fatalf("expected non-nil output")
	}

	// Verify RunGraph single node
	graph := engine.Graph{
		Nodes: []engine.GraphNode{
			{
				ID:     "n1",
				Type:   watermark.NodeType,
				Params: rawIn,
			},
		},
	}
	res, err := svc.RunGraph(context.Background(), graph)
	if err != nil {
		t.Fatalf("RunGraph failed: %v", err)
	}
	if !res.Done() {
		t.Fatalf("expected graph run done: %+v", res)
	}
}

// Callers without their own callback (agent approvals, MCP tools) must still report progress
// through the context, or the action's idle timeout fires mid-run. A watermark batch reports
// once per image, so a long batch keeps the action alive.
func TestWorkflowReportsProgressThroughContext(t *testing.T) {
	tempDir := t.TempDir()
	svc, err := workflow.New(workflow.Options{
		Paths:  paths.Paths{Data: tempDir},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var updates []progress.Update
	ctx := progress.With(context.Background(), progress.Sink{Report: func(u progress.Update) {
		mu.Lock()
		updates = append(updates, u)
		mu.Unlock()
	}})

	imgs := []string{}
	for i := 0; i < 3; i++ {
		imgs = append(imgs, `"`+filepath.ToSlash(createTestPNG(t, tempDir, fmt.Sprintf("p%d.png", i)))+`"`)
	}
	raw := json.RawMessage(`{"images": [` + strings.Join(imgs, ",") + `]}`)
	if _, err := svc.RunNode(ctx, watermark.NodeType, raw); err != nil {
		t.Fatalf("RunNode: %v", err)
	}
	itemReports := 0
	for _, u := range updates {
		if u.Key == "workflow.progress.items" {
			itemReports++
		}
	}
	if itemReports != 3 {
		t.Fatalf("want one report per image (3), got %d: %+v", itemReports, updates)
	}

	updates = nil
	g := engine.Graph{Nodes: []engine.GraphNode{{ID: "n1", Type: watermark.NodeType, Params: json.RawMessage(`{"image": ` + imgs[0] + `}`)}}}
	if _, err := svc.RunGraph(ctx, g); err != nil {
		t.Fatalf("RunGraph: %v", err)
	}
	if len(updates) == 0 {
		t.Fatal("RunGraph without a callback reported nothing through the context")
	}
}

type stubMediaGen struct {
	mediaDir string
}

func (s *stubMediaGen) GenerateImage(_ context.Context, req mediagen.ImageRequest) (mediagen.Result, error) {
	return mediagen.Result{Kind: mediagen.KindImage, Path: filepath.Join(s.mediaDir, "i.png"), MimeType: "image/png", Provider: "stub", Model: "m1"}, nil
}
func (s *stubMediaGen) GenerateVideo(_ context.Context, req mediagen.VideoRequest) (mediagen.Result, error) {
	return mediagen.Result{Kind: mediagen.KindVideo, Path: filepath.Join(s.mediaDir, "v.mp4"), MimeType: "video/mp4", Provider: "stub", Model: "m2"}, nil
}
func (s *stubMediaGen) GenerateTTS(_ context.Context, req mediagen.TTSRequest) (mediagen.Result, error) {
	return mediagen.Result{Kind: mediagen.KindTTS, Path: filepath.Join(s.mediaDir, "a.wav"), MimeType: "audio/wav", Provider: "stub", Model: "m3"}, nil
}

func TestWorkflowRegistersMediaGenNodes(t *testing.T) {
	tempDir := t.TempDir()
	svc, err := workflow.New(workflow.Options{
		Paths:    paths.Paths{Data: tempDir},
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		MediaGen: &stubMediaGen{mediaDir: tempDir},
	})
	if err != nil {
		t.Fatalf("workflow.New: %v", err)
	}
	specs := svc.Specs(context.Background())
	have := map[string]bool{}
	for _, sp := range specs {
		have[sp.Type] = true
	}
	for _, want := range []string{mediagennodes.NodeTypeImage, mediagennodes.NodeTypeVideo, mediagennodes.NodeTypeTTS} {
		if !have[want] {
			t.Fatalf("missing registered node spec %q", want)
		}
	}
	if _, err := svc.RunNode(context.Background(), mediagennodes.NodeTypeImage, json.RawMessage(`{"prompt":"hello"}`)); err != nil {
		t.Fatalf("RunNode(image): %v", err)
	}
	if _, err := svc.RunNode(context.Background(), mediagennodes.NodeTypeVideo, json.RawMessage(`{"prompt":"hello"}`)); err != nil {
		t.Fatalf("RunNode(video): %v", err)
	}
	if _, err := svc.RunNode(context.Background(), mediagennodes.NodeTypeTTS, json.RawMessage(`{"text":"hello"}`)); err != nil {
		t.Fatalf("RunNode(tts): %v", err)
	}
}

func TestWorkflowWiresWorkspaceTreeFunc(t *testing.T) {
	tempDir := t.TempDir()
	workspaceRoot := filepath.Join(tempDir, "workspace")
	mediaDir := filepath.Join(workspaceRoot, "media")
	watermarkDir := filepath.Join(mediaDir, "watermark")
	_ = os.MkdirAll(watermarkDir, 0o755)

	tree := paths.WorkspaceTree{
		Root:         workspaceRoot,
		MediaDir:     mediaDir,
		ImagesDir:    filepath.Join(mediaDir, "images"),
		VideosDir:    filepath.Join(mediaDir, "videos"),
		TTSDir:       filepath.Join(mediaDir, "tts"),
		WatermarkDir: watermarkDir,
		WorkspaceDir: filepath.Join(workspaceRoot, "workspace"),
	}

	var lastReqOutputDir string
	stubGen := &stubMediaGenCapture{
		onImage: func(req mediagen.ImageRequest) {
			lastReqOutputDir = req.OutputDir
		},
	}

	svc, err := workflow.New(workflow.Options{
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		MediaGen: stubGen,
		WorkspaceTreeFunc: func() paths.WorkspaceTree {
			return tree
		},
	})
	if err != nil {
		t.Fatalf("workflow.New failed: %v", err)
	}

	// 1. Mediagen image node should output to tree.MediaDir/images by default
	_, err = svc.RunNode(context.Background(), mediagennodes.NodeTypeImage, json.RawMessage(`{"prompt":"hello dynamic"}`))
	if err != nil {
		t.Fatalf("RunNode image error: %v", err)
	}
	expectedImgDir := filepath.Join(mediaDir, "images")
	if lastReqOutputDir != expectedImgDir {
		t.Fatalf("expected mediagen output dir %s, got %s", expectedImgDir, lastReqOutputDir)
	}

	// 2. Watermark node should default target dir to tree.WatermarkDir
	srcImg := createTestPNG(t, tempDir, "wm_test.png")
	outRaw, err := svc.RunNode(context.Background(), watermark.NodeType, json.RawMessage(fmt.Sprintf(`{"image": %q}`, filepath.ToSlash(srcImg))))
	if err != nil {
		t.Fatalf("RunNode watermark error: %v", err)
	}
	outBytes, _ := json.Marshal(outRaw)
	var wmOut watermark.RemoverOutput
	_ = json.Unmarshal(outBytes, &wmOut)

	if len(wmOut.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(wmOut.Results))
	}
	actualOutDir := filepath.Dir(filepath.Clean(wmOut.Results[0].OutPath))
	if !strings.EqualFold(actualOutDir, filepath.Clean(watermarkDir)) {
		t.Fatalf("expected watermark output in %s, got %s", watermarkDir, actualOutDir)
	}
}

type stubMediaGenCapture struct {
	onImage func(req mediagen.ImageRequest)
}

func (s *stubMediaGenCapture) GenerateImage(_ context.Context, req mediagen.ImageRequest) (mediagen.Result, error) {
	if s.onImage != nil {
		s.onImage(req)
	}
	return mediagen.Result{Kind: mediagen.KindImage, Path: filepath.Join(req.OutputDir, "i.png")}, nil
}
func (s *stubMediaGenCapture) GenerateVideo(_ context.Context, req mediagen.VideoRequest) (mediagen.Result, error) {
	return mediagen.Result{Kind: mediagen.KindVideo, Path: filepath.Join(req.OutputDir, "v.mp4")}, nil
}
func (s *stubMediaGenCapture) GenerateTTS(_ context.Context, req mediagen.TTSRequest) (mediagen.Result, error) {
	return mediagen.Result{Kind: mediagen.KindTTS, Path: filepath.Join(req.OutputDir, "a.wav")}, nil
}

type stubTextGen struct{}

func (s *stubTextGen) GenerateText(_ context.Context, req mediagen.TextRequest) (mediagen.TextResult, error) {
	payload := json.RawMessage(`{"title":"Ep1","genre":"drama","aspect_ratio":"16:9","logline":"L","scenes":[{"code":"S01","title":"Open","beats":[{"code":"B01","description":"Walk in","duration_sec":5}]}]}`)
	return mediagen.TextResult{Text: string(payload), ParsedJSON: payload, Provider: "stub", Model: "text-1"}, nil
}

func TestWorkflowRegistersStudioNodes(t *testing.T) {
	tempDir := t.TempDir()
	svc, err := workflow.New(workflow.Options{
		Paths:   paths.Paths{Data: tempDir},
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		TextGen: &stubTextGen{},
	})
	if err != nil {
		t.Fatalf("workflow.New: %v", err)
	}

	have := map[string]bool{}
	for _, sp := range svc.Specs(context.Background()) {
		have[sp.Type] = true
	}
	wantNodes := []string{
		studionodes.NodeTypeScriptNormalize,
		studionodes.NodeTypeExtractAssets,
		studionodes.NodeTypeAuthorShotPrompts,
		studionodes.NodeTypeProbeMedia,
		studionodes.NodeTypeVideoConcatBasic,
	}
	for _, want := range wantNodes {
		if !have[want] {
			t.Fatalf("missing studio node spec %q", want)
		}
	}

	if _, err := svc.RunNode(context.Background(), studionodes.NodeTypeScriptNormalize, json.RawMessage(`{"raw_script":"INT. ROOM - DAY"}`)); err != nil {
		t.Fatalf("RunNode(script_normalize): %v", err)
	}
}
