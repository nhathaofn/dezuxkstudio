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
