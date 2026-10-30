package watermark_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"app/internal/engine"
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

func TestWatermarkRemoverNode_DirectRun(t *testing.T) {
	tempDir := t.TempDir()
	testImg := createTestPNG(t, tempDir, "sample.png")

	node := watermark.NewNode(watermark.Options{OutputDir: tempDir})
	if node.Spec().Type != watermark.NodeType {
		t.Fatalf("unexpected node type: %s", node.Spec().Type)
	}

	rawInput := json.RawMessage(`{"image": "` + filepath.ToSlash(testImg) + `"}`)
	outRaw, err := node.Run(context.Background(), rawInput)
	if err != nil {
		t.Fatalf("node run failed: %v", err)
	}

	outBytes, _ := json.Marshal(outRaw)
	var out watermark.NodeOutput
	if err := json.Unmarshal(outBytes, &out); err != nil {
		t.Fatalf("failed to unmarshal output: %v", err)
	}

	if out.Image.LocalURL == "" {
		t.Fatalf("expected non-empty LocalURL, got empty")
	}
}

func TestWatermarkRemoverNode_RelativePathWithMediaDir(t *testing.T) {
	tempMediaDir := t.TempDir()
	createTestPNG(t, tempMediaDir, "generated.png")

	node := watermark.NewNode(watermark.Options{MediaDir: tempMediaDir})

	// Pass relative path as "/media/generated.png"
	rawInput := json.RawMessage(`{"image": "/media/generated.png"}`)
	outRaw, err := node.Run(context.Background(), rawInput)
	if err != nil {
		t.Fatalf("expected node to resolve relative media path: %v", err)
	}

	outBytes, _ := json.Marshal(outRaw)
	var out watermark.NodeOutput
	_ = json.Unmarshal(outBytes, &out)
	if out.Image.LocalURL == "" {
		t.Fatalf("expected resolved output LocalURL")
	}
}

func TestWatermarkRemoverNode_InEngineDAG(t *testing.T) {
	tempDir := t.TempDir()
	testImg := createTestPNG(t, tempDir, "dag_sample.png")

	reg := engine.NewRegistry()
	wmNode := watermark.NewNode(watermark.Options{OutputDir: tempDir})
	if err := reg.Register(wmNode); err != nil {
		t.Fatalf("failed to register watermark node: %v", err)
	}

	g := engine.Graph{
		Nodes: []engine.GraphNode{
			{
				ID:     "wm1",
				Type:   watermark.NodeType,
				Params: json.RawMessage(`{"image": {"localUrl": "` + filepath.ToSlash(testImg) + `", "kind": "image"}}`),
			},
		},
	}

	res, err := reg.RunGraph(context.Background(), g)
	if err != nil {
		t.Fatalf("graph execution failed: %v", err)
	}

	if !res.Done() {
		t.Fatalf("graph run did not complete successfully: %+v", res)
	}

	runRes, ok := res.Nodes["wm1"]
	if !ok || runRes.Status != engine.RunDone {
		t.Fatalf("node wm1 failed or not found: %+v", runRes)
	}
}

func TestWatermarkRemoverNode_BatchMultiThreaded(t *testing.T) {
	tempDir := t.TempDir()
	img1 := createTestPNG(t, tempDir, "batch1.png")
	img2 := createTestPNG(t, tempDir, "batch2.png")
	img3 := createTestPNG(t, tempDir, "batch3.png")

	node := watermark.NewNode(watermark.Options{OutputDir: tempDir})

	inputJSON := fmt.Sprintf(`{
		"images": ["%s", "%s", "%s"],
		"workers": 4,
		"corner": "bottom-right"
	}`, filepath.ToSlash(img1), filepath.ToSlash(img2), filepath.ToSlash(img3))

	outRaw, err := node.Run(context.Background(), json.RawMessage(inputJSON))
	if err != nil {
		t.Fatalf("batch run failed: %v", err)
	}

	outBytes, _ := json.Marshal(outRaw)
	var out watermark.NodeOutput
	if err := json.Unmarshal(outBytes, &out); err != nil {
		t.Fatalf("unmarshal batch output: %v", err)
	}

	if out.TotalCount != 3 {
		t.Fatalf("expected total_count=3, got %d", out.TotalCount)
	}
	if len(out.Images) != 3 {
		t.Fatalf("expected 3 images in output, got %d", len(out.Images))
	}
	if len(out.Results) != 3 {
		t.Fatalf("expected 3 item results, got %d", len(out.Results))
	}
}
