package workflow_test

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"app/internal/engine"
	"app/internal/paths"
	"app/internal/service/workflow"
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

	// Verify Specs
	specs := svc.Specs(context.Background())
	if len(specs) == 0 {
		t.Fatalf("expected at least 1 registered node spec")
	}

	hasWM := false
	for _, sp := range specs {
		if sp.Type == watermark.NodeType {
			hasWM = true
			break
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

	// Verify RunGraph
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
