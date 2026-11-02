package workflow_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"app/internal/engine"
	"app/internal/paths"
	"app/internal/service/workflow"
	"app/internal/tools/gateway"
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

func encodePNGBytes(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 120, 80))
	draw.Draw(img, img.Bounds(), &image.Uniform{C: color.RGBA{R: 50, G: 150, B: 250, A: 255}}, image.Point{}, draw.Src)
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("failed to encode test png: %v", err)
	}
	return buf.Bytes()
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

	// Verify Specs contain both watermark remover and gateway generator
	specs := svc.Specs(context.Background())
	if len(specs) < 2 {
		t.Fatalf("expected at least 2 registered node specs, got %d", len(specs))
	}

	hasWM := false
	hasGateway := false
	for _, sp := range specs {
		if sp.Type == watermark.NodeType {
			hasWM = true
		}
		if sp.Type == gateway.NodeType {
			hasGateway = true
		}
	}
	if !hasWM {
		t.Fatalf("watermark node spec %s not found in registry", watermark.NodeType)
	}
	if !hasGateway {
		t.Fatalf("gateway node spec %s not found in registry", gateway.NodeType)
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

func TestWorkflowService_TwoNodePipelineIntegration(t *testing.T) {
	tempDir := t.TempDir()
	appPaths := paths.Paths{Data: tempDir}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	pngBytes := encodePNGBytes(t)
	b64PNG := base64.StdEncoding.EncodeToString(pngBytes)

	// Mock Gateway Server
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/images/generations" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(gateway.GenerationResponse{
			Created:  1730000000,
			Provider: "flow",
			Data: []gateway.GenerationItem{
				{B64JSON: b64PNG},
			},
		})
	}))
	defer srv.Close()

	svc, err := workflow.New(workflow.Options{
		Paths:         appPaths,
		Logger:        logger,
		GatewayURL:    srv.URL,
		GatewayAPIKey: "pipeline-token",
		HTTPClient:    srv.Client(),
	})
	if err != nil {
		t.Fatalf("failed to create workflow service: %v", err)
	}

	// Two-node pipeline:
	// tool.ai_image_generator (output: image) -> tool.watermark_remover (input: image)
	graph := engine.Graph{
		Nodes: []engine.GraphNode{
			{
				ID:   "gen",
				Type: gateway.NodeType,
				Params: json.RawMessage(`{
					"prompt": "futuristic flying vehicle over neon city",
					"negative_prompt": "lowres, bad quality",
					"aspect_ratio": "16:9"
				}`),
			},
			{
				ID:   "clean",
				Type: watermark.NodeType,
				Params: json.RawMessage(`{
					"inplace": false
				}`),
			},
		},
		Edges: []engine.Edge{
			{
				From: engine.Endpoint{Node: "gen", Port: "image"},
				To:   engine.Endpoint{Node: "clean", Port: "image"},
			},
		},
	}

	ctx := context.Background()
	res, err := svc.RunGraph(ctx, graph)
	if err != nil {
		t.Fatalf("two-node graph execution failed: %v", err)
	}

	if !res.Done() {
		t.Fatalf("expected graph to complete, got: %+v", res)
	}

	genRun, ok := res.Nodes["gen"]
	if !ok || genRun.Status != engine.RunDone {
		t.Fatalf("expected generator node done, got: %+v", genRun)
	}

	cleanRun, ok := res.Nodes["clean"]
	if !ok || cleanRun.Status != engine.RunDone {
		t.Fatalf("expected watermark remover node done, got: %+v", cleanRun)
	}

	if len(cleanRun.Output) == 0 {
		t.Fatalf("watermark remover output must not be empty")
	}

	var wmOutput watermark.NodeOutput
	if err := json.Unmarshal(cleanRun.Output, &wmOutput); err != nil {
		t.Fatalf("failed to unmarshal watermark output: %v", err)
	}

	if wmOutput.Image.LocalURL == "" {
		t.Fatalf("expected non-empty output cleaned image path")
	}

	// Verify cleaned image file exists on disk
	if _, err := os.Stat(wmOutput.Image.LocalURL); err != nil {
		t.Fatalf("cleaned output image does not exist: %v", err)
	}
}
