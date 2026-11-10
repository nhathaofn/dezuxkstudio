package watermark_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"image/png"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"app/internal/engine"
	"app/internal/tools/watermark"
	pkgwatermark "app/pkg/watermark"
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

func createWatermarkedPNG(t *testing.T, dir, filename string) string {
	t.Helper()
	w, h := 600, 600
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	rng := rand.New(rand.NewPCG(1, 2))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			n := rng.IntN(40)
			img.SetRGBA(x, y, color.RGBA{R: uint8(60 + (x*80)/w + n), G: uint8(90 + (y*60)/h + n), B: uint8(120 + n), A: 255})
		}
	}

	tmpls := pkgwatermark.DefaultTemplates()
	if len(tmpls) == 0 {
		t.Fatal("no default templates available")
	}
	tmpl := tmpls[0] // 48px
	sz := tmpl.Size
	// Place at bottom-right corner
	x0, y0 := w-75-sz, h-75-sz
	gain := 0.65
	for y := 0; y < sz; y++ {
		for x := 0; x < sz; x++ {
			a := gain * float64(tmpl.Alpha[y*sz+x])
			c := img.RGBAAt(x0+x, y0+y)
			blend := func(v uint8) uint8 { return uint8(math.Round(a*255 + (1-a)*float64(v))) }
			img.SetRGBA(x0+x, y0+y, color.RGBA{R: blend(c.R), G: blend(c.G), B: blend(c.B), A: 255})
		}
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("failed to encode png: %v", err)
	}

	p := filepath.Join(dir, filename)
	if err := os.WriteFile(p, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("failed to write test watermarked png: %v", err)
	}
	return p
}

func TestWatermarkRemoverNode_InPlaceAtomic(t *testing.T) {
	tempDir := t.TempDir()
	testImg := createWatermarkedPNG(t, tempDir, "marked_photo.png")

	// Get initial file info
	fiBefore, err := os.Stat(testImg)
	if err != nil {
		t.Fatalf("stat failed: %v", err)
	}
	origPerm := fiBefore.Mode().Perm()

	// Verify the original image is detected as watermarked
	beforeBytes, err := os.ReadFile(testImg)
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	detectedBefore, _, _, err := pkgwatermark.Detect(beforeBytes)
	if err != nil {
		t.Fatalf("detect before error: %v", err)
	}
	if !detectedBefore {
		t.Fatalf("expected watermark to be detected in test image before processing")
	}

	node := watermark.NewNode(watermark.Options{})
	inputJSON := fmt.Sprintf(`{
		"image": "%s",
		"inplace": true
	}`, filepath.ToSlash(testImg))

	outRaw, err := node.Run(context.Background(), json.RawMessage(inputJSON))
	if err != nil {
		t.Fatalf("node run failed: %v", err)
	}

	outBytes, _ := json.Marshal(outRaw)
	var out watermark.NodeOutput
	if err := json.Unmarshal(outBytes, &out); err != nil {
		t.Fatalf("unmarshal output failed: %v", err)
	}

	if out.DetectedCount != 1 {
		t.Fatalf("expected detected_count=1, got %d", out.DetectedCount)
	}
	if len(out.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(out.Results))
	}
	res := out.Results[0]
	if !res.Detected {
		t.Fatalf("expected result detected=true")
	}
	if res.OutPath != testImg {
		t.Fatalf("expected OutPath to be %q, got %q", testImg, res.OutPath)
	}

	// Verify destination file now has the watermark removed
	afterBytes, err := os.ReadFile(testImg)
	if err != nil {
		t.Fatalf("read after failed: %v", err)
	}
	detectedAfter, _, _, err := pkgwatermark.Detect(afterBytes)
	if err != nil {
		t.Fatalf("detect after error: %v", err)
	}
	if detectedAfter {
		t.Fatalf("expected watermark to be removed from overwritten file")
	}

	// Verify permissions are preserved
	fiAfter, err := os.Stat(testImg)
	if err != nil {
		t.Fatalf("stat after failed: %v", err)
	}
	if fiAfter.Mode().Perm() != origPerm {
		t.Fatalf("file permission changed: got %v, want %v", fiAfter.Mode().Perm(), origPerm)
	}

	// Verify no temporary files left behind
	entries, err := os.ReadDir(tempDir)
	if err != nil {
		t.Fatalf("readdir failed: %v", err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") {
			t.Fatalf("temporary dot-file left behind in directory: %s", entry.Name())
		}
	}
}

func TestWatermarkRemoverNode_InPlaceAtomicWriteFailurePreservesOriginal(t *testing.T) {
	tempDir := t.TempDir()
	testImg := createWatermarkedPNG(t, tempDir, "locked_image.png")
	origBytes, _ := os.ReadFile(testImg)

	// Lock the destination file so rename will fail on Windows
	f, err := os.OpenFile(testImg, os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("open file lock failed: %v", err)
	}

	node := watermark.NewNode(watermark.Options{})
	inputJSON := fmt.Sprintf(`{
		"image": "%s",
		"inplace": true
	}`, filepath.ToSlash(testImg))

	outRaw, err := node.Run(context.Background(), json.RawMessage(inputJSON))
	_ = f.Close() // unlock

	if err == nil {
		t.Fatalf("expected node run to return execution error when all items fail, got nil")
	}

	outBytes, _ := json.Marshal(outRaw)
	var out watermark.NodeOutput
	_ = json.Unmarshal(outBytes, &out)

	if len(out.Results) != 1 || out.Results[0].Error == "" {
		t.Fatalf("expected result with write error, got %+v", out.Results)
	}

	// Verify original file is not corrupted or truncated
	afterBytes, err := os.ReadFile(testImg)
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	if !bytes.Equal(afterBytes, origBytes) {
		t.Fatalf("original file was corrupted during failed atomic write")
	}
}

func TestWatermarkRemoverNode_PathTraversalBlocked(t *testing.T) {
	parentDir := t.TempDir()
	mediaDir := filepath.Join(parentDir, "media")
	if err := os.MkdirAll(mediaDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Create a secret file in parentDir outside mediaDir
	secretPath := filepath.Join(parentDir, "secret.png")
	if err := os.WriteFile(secretPath, []byte("sensitive-host-content"), 0o644); err != nil {
		t.Fatal(err)
	}

	node := watermark.NewNode(watermark.Options{
		MediaDir:  mediaDir,
		OutputDir: mediaDir,
	})

	traversalPaths := []string{
		"../../secret.png",
		"/media/../../secret.png",
		"media/../../secret.png",
		"../secret.png",
	}

	for _, p := range traversalPaths {
		inputJSON := fmt.Sprintf(`{"image": %q}`, p)
		outRaw, err := node.Run(context.Background(), json.RawMessage(inputJSON))
		if err == nil {
			t.Fatalf("path %q: expected error when traversal path fails", p)
		}

		outBytes, _ := json.Marshal(outRaw)
		var out watermark.NodeOutput
		if err := json.Unmarshal(outBytes, &out); err != nil {
			t.Fatalf("unmarshal node output: %v", err)
		}

		if len(out.Results) != 1 {
			t.Fatalf("path %q: expected 1 result, got %d", p, len(out.Results))
		}
		if !strings.Contains(out.Results[0].Error, "failed to read file") {
			t.Fatalf("path %q: expected error containing 'failed to read file', got %q", p, out.Results[0].Error)
		}
	}
}

func createWatermarkedJPEG(t *testing.T, dir, filename string) string {
	t.Helper()
	w, h := 600, 600
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	rng := rand.New(rand.NewPCG(1, 2))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			n := rng.IntN(40)
			img.SetRGBA(x, y, color.RGBA{R: uint8(60 + (x*80)/w + n), G: uint8(90 + (y*60)/h + n), B: uint8(120 + n), A: 255})
		}
	}

	tmpls := pkgwatermark.DefaultTemplates()
	if len(tmpls) == 0 {
		t.Fatal("no default templates available")
	}
	tmpl := tmpls[0] // 48px
	sz := tmpl.Size
	x0, y0 := w-75-sz, h-75-sz
	gain := 0.65
	for y := 0; y < sz; y++ {
		for x := 0; x < sz; x++ {
			a := gain * float64(tmpl.Alpha[y*sz+x])
			c := img.RGBAAt(x0+x, y0+y)
			blend := func(v uint8) uint8 { return uint8(math.Round(a*255 + (1-a)*float64(v))) }
			img.SetRGBA(x0+x, y0+y, color.RGBA{R: blend(c.R), G: blend(c.G), B: blend(c.B), A: 255})
		}
	}

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatalf("failed to encode jpeg: %v", err)
	}

	p := filepath.Join(dir, filename)
	if err := os.WriteFile(p, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("failed to write test watermarked jpeg: %v", err)
	}
	return p
}

func TestWatermarkRemoverNode_JPEGPreserved(t *testing.T) {
	tempDir := t.TempDir()
	testImg := createWatermarkedJPEG(t, tempDir, "sample_flow.jpg")

	node := watermark.NewNode(watermark.Options{OutputDir: tempDir})
	inputJSON := fmt.Sprintf(`{"image": %q}`, filepath.ToSlash(testImg))

	outRaw, err := node.Run(context.Background(), json.RawMessage(inputJSON))
	if err != nil {
		t.Fatalf("node run failed: %v", err)
	}

	outBytes, _ := json.Marshal(outRaw)
	var out watermark.NodeOutput
	if err := json.Unmarshal(outBytes, &out); err != nil {
		t.Fatalf("unmarshal output failed: %v", err)
	}

	if out.DetectedCount != 1 {
		t.Fatalf("expected detected_count=1, got %d", out.DetectedCount)
	}
	if len(out.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(out.Results))
	}
	res := out.Results[0]
	if !strings.HasSuffix(res.OutPath, ".jpg") {
		t.Errorf("expected outPath to have .jpg extension, got %s", res.OutPath)
	}

	outData, err := os.ReadFile(res.OutPath)
	if err != nil {
		t.Fatalf("read out file failed: %v", err)
	}
	if len(outData) < 3 || outData[0] != 0xFF || outData[1] != 0xD8 || outData[2] != 0xFF {
		t.Errorf("output file does not have JPEG magic bytes: %x", outData[:min(len(outData), 8)])
	}
}

func TestWatermarkRemoverNode_AllFailedReturnsError(t *testing.T) {
	node := watermark.NewNode(watermark.Options{})
	inputJSON := `{"images": [{"localUrl": "non_existent_1.png", "kind": "image"}, {"localUrl": "non_existent_2.png", "kind": "image"}]}`

	outRaw, err := node.Run(context.Background(), json.RawMessage(inputJSON))
	if err == nil {
		t.Fatalf("expected error when all items fail, got nil")
	}
	if !strings.Contains(err.Error(), "watermark removal failed for all 2 images") {
		t.Fatalf("unexpected error message: %v", err)
	}

	outBytes, _ := json.Marshal(outRaw)
	var out watermark.NodeOutput
	_ = json.Unmarshal(outBytes, &out)
	if len(out.Images) != 0 {
		t.Fatalf("expected out.Images to be empty, got: %d", len(out.Images))
	}
}

func TestWatermarkRemoverNode_PartialFailureOmitsFailedRef(t *testing.T) {
	tempDir := t.TempDir()
	validImg := createTestPNG(t, tempDir, "valid_sample.png")

	node := watermark.NewNode(watermark.Options{OutputDir: tempDir})
	inputJSON := fmt.Sprintf(`{"images": [{"localUrl": %q, "kind": "image"}, {"localUrl": "non_existent.png", "kind": "image"}]}`, filepath.ToSlash(validImg))

	outRaw, err := node.Run(context.Background(), json.RawMessage(inputJSON))
	if err != nil {
		t.Fatalf("expected partial failure to return nil error, got: %v", err)
	}

	outBytes, _ := json.Marshal(outRaw)
	var out watermark.NodeOutput
	_ = json.Unmarshal(outBytes, &out)

	if len(out.Images) != 1 {
		t.Fatalf("expected exactly 1 successful image in out.Images, got %d", len(out.Images))
	}
	if len(out.Results) != 2 {
		t.Fatalf("expected 2 items in Results, got %d", len(out.Results))
	}
	if out.Results[1].Error == "" {
		t.Errorf("expected 2nd result to report error")
	}
}

func TestWatermarkRemoverNode_ContextCanceled(t *testing.T) {
	node := watermark.NewNode(watermark.Options{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	inputJSON := `{"image": "any.png"}`
	_, err := node.Run(ctx, json.RawMessage(inputJSON))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled error, got: %v", err)
	}
}
