package watermark

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"app/internal/engine"
	pkgwatermark "app/pkg/watermark"
)

func createCollisionTestPNG(t *testing.T, dir, filename string, r, g, b uint8) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rect(0, 0, 48, 48))
	for y := 0; y < 48; y++ {
		for x := 0; x < 48; x++ {
			img.Set(x, y, color.RGBA{R: r, G: g, B: b, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, filename)
	if err := os.WriteFile(p, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestCollision_AvoidanceDifferentSources(t *testing.T) {
	opts := Options{Provenance: pkgwatermark.NewProvenanceStore()}
	baseDir := t.TempDir()
	dirA := filepath.Join(baseDir, "sourceA")
	dirB := filepath.Join(baseDir, "sourceB")
	outDir := filepath.Join(baseDir, "output")

	srcA := createCollisionTestPNG(t, dirA, "sample.png", 100, 50, 20)
	srcB := createCollisionTestPNG(t, dirB, "sample.png", 20, 50, 100)

	brA := pkgwatermark.BatchResult{
		Detected:     true,
		Score:        0.90,
		Output:       []byte("data-from-sourceA"),
		OutputFormat: pkgwatermark.FormatPNG,
		Info:         pkgwatermark.Info{Size: 48},
	}
	inA := RemoverInput{
		OutputDir: outDir,
		Corner:    "bottom-right",
		MinScore:  0.2,
		MinGain:   0.4,
		MaxGain:   0.9,
	}
	resA, outA := finishItem(context.Background(), inA, outDir, srcA, srcA, brA, opts)
	if resA.Status != pkgwatermark.StatusProcessed {
		t.Fatalf("expected Status=processed for item A, got %s", resA.Status)
	}
	if filepath.Base(outA) != "sample_nowm.png" {
		t.Fatalf("expected sample_nowm.png for item A, got %s", filepath.Base(outA))
	}

	brB := pkgwatermark.BatchResult{
		Detected:     true,
		Score:        0.85,
		Output:       []byte("data-from-sourceB"),
		OutputFormat: pkgwatermark.FormatPNG,
		Info:         pkgwatermark.Info{Size: 48},
	}
	inB := RemoverInput{
		OutputDir: outDir,
		Corner:    "bottom-right",
		MinScore:  0.2,
		MinGain:   0.4,
		MaxGain:   0.9,
	}
	resB, outB := finishItem(context.Background(), inB, outDir, srcB, srcB, brB, opts)
	if resB.Status != pkgwatermark.StatusProcessed {
		t.Fatalf("expected Status=processed for item B, got %s", resB.Status)
	}
	if filepath.Base(outB) != "sample_nowm_1.png" {
		t.Fatalf("expected sample_nowm_1.png for collision avoidance, got %s", filepath.Base(outB))
	}

	if _, err := os.Stat(outA); err != nil {
		t.Fatalf("output file A does not exist: %v", err)
	}
	if _, err := os.Stat(outB); err != nil {
		t.Fatalf("output file B does not exist: %v", err)
	}
}

func TestCollision_IdempotentReRunSkips(t *testing.T) {
	opts := Options{Provenance: pkgwatermark.NewProvenanceStore()}
	baseDir := t.TempDir()
	srcDir := filepath.Join(baseDir, "source")
	outDir := filepath.Join(baseDir, "output")

	src := createCollisionTestPNG(t, srcDir, "repeat.png", 120, 80, 40)

	br := pkgwatermark.BatchResult{
		Detected:     true,
		Score:        0.92,
		Output:       []byte("first-run-bytes"),
		OutputFormat: pkgwatermark.FormatPNG,
		Info:         pkgwatermark.Info{Size: 48},
	}
	in := RemoverInput{
		OutputDir: outDir,
		Corner:    "bottom-right",
		MinScore:  0.2,
		MinGain:   0.4,
		MaxGain:   0.9,
	}

	// First run: writes output
	res1, out1 := finishItem(context.Background(), in, outDir, src, src, br, opts)
	if res1.Status != pkgwatermark.StatusProcessed {
		t.Fatalf("first run: expected Status=processed, got %s", res1.Status)
	}
	if filepath.Base(out1) != "repeat_nowm.png" {
		t.Fatalf("first run: expected repeat_nowm.png, got %s", filepath.Base(out1))
	}

	// Second run: exact same input path + exact same parameters
	res2, out2 := finishItem(context.Background(), in, outDir, src, src, br, opts)
	if res2.Status != pkgwatermark.StatusSkipped {
		t.Fatalf("second run: expected Status=skipped, got %s", res2.Status)
	}
	if res2.Reason != pkgwatermark.ReasonIdempotentSkip {
		t.Fatalf("second run: expected Reason=%q, got %q", pkgwatermark.ReasonIdempotentSkip, res2.Reason)
	}
	if out2 != out1 {
		t.Fatalf("second run: expected same outPath %s, got %s", out1, out2)
	}

	// Verify repeat_nowm_1.png was NOT generated
	redundantPath := filepath.Join(outDir, "repeat_nowm_1.png")
	if _, err := os.Stat(redundantPath); err == nil {
		t.Fatalf("redundant %s was generated on idempotent re-run", redundantPath)
	}
}

func TestCollision_ParameterChangeGeneratesNewOutput(t *testing.T) {
	opts := Options{Provenance: pkgwatermark.NewProvenanceStore()}
	baseDir := t.TempDir()
	srcDir := filepath.Join(baseDir, "source")
	outDir := filepath.Join(baseDir, "output")

	src := createCollisionTestPNG(t, srcDir, "param_change.png", 50, 100, 150)

	br := pkgwatermark.BatchResult{
		Detected:     true,
		Score:        0.88,
		Output:       []byte("data-corner-br"),
		OutputFormat: pkgwatermark.FormatPNG,
		Info:         pkgwatermark.Info{Size: 48},
	}
	inBR := RemoverInput{
		OutputDir: outDir,
		Corner:    "bottom-right",
		MinScore:  0.2,
		MinGain:   0.4,
		MaxGain:   0.9,
	}
	res1, out1 := finishItem(context.Background(), inBR, outDir, src, src, br, opts)
	if res1.Status != pkgwatermark.StatusProcessed || filepath.Base(out1) != "param_change_nowm.png" {
		t.Fatalf("first run failed: status=%s, out=%s", res1.Status, out1)
	}

	// Now re-run with different parameter (Corner = top-left)
	inTL := inBR
	inTL.Corner = "top-left"
	res2, out2 := finishItem(context.Background(), inTL, outDir, src, src, br, opts)
	if res2.Status != pkgwatermark.StatusProcessed {
		t.Fatalf("param change run: expected Status=processed, got %s", res2.Status)
	}
	if filepath.Base(out2) != "param_change_nowm_1.png" {
		t.Fatalf("param change run: expected param_change_nowm_1.png, got %s", filepath.Base(out2))
	}
}

func TestCollision_CopyClean(t *testing.T) {
	opts := Options{Provenance: pkgwatermark.NewProvenanceStore()}
	baseDir := t.TempDir()
	srcDir := filepath.Join(baseDir, "source")
	outDir := filepath.Join(baseDir, "output")

	src := createCollisionTestPNG(t, srcDir, "clean_image.png", 30, 60, 90)

	brClean := pkgwatermark.BatchResult{
		Detected: false,
		Score:    0.01,
	}

	// Run with CopyClean = true (default when OutputDir is non-empty)
	inCopyClean := RemoverInput{
		OutputDir: outDir,
	}
	res, outPath := finishItem(context.Background(), inCopyClean, outDir, src, src, brClean, opts)
	if res.Status != pkgwatermark.StatusCopied {
		t.Fatalf("expected Status=copied, got %s", res.Status)
	}
	if filepath.Base(outPath) != "clean_image.png" {
		t.Fatalf("expected clean_image.png in destDir, got %s", filepath.Base(outPath))
	}

	// Verify copied file actually exists and bytes match original
	origBytes, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	copiedBytes, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(origBytes, copiedBytes) {
		t.Fatalf("copied clean image data does not match original")
	}

	// Second run of clean image with identical parameters -> idempotent skip
	res2, outPath2 := finishItem(context.Background(), inCopyClean, outDir, src, src, brClean, opts)
	if res2.Status != pkgwatermark.StatusSkipped {
		t.Fatalf("expected Status=skipped on clean copy re-run, got %s", res2.Status)
	}
	if res2.Reason != pkgwatermark.ReasonIdempotentSkip {
		t.Fatalf("expected Reason=%q, got %q", pkgwatermark.ReasonIdempotentSkip, res2.Reason)
	}
	if outPath2 != outPath {
		t.Fatalf("expected same outPath %s, got %s", outPath, outPath2)
	}
}

func TestCollision_CopyCleanDisabled(t *testing.T) {
	opts := Options{Provenance: pkgwatermark.NewProvenanceStore()}
	baseDir := t.TempDir()
	srcDir := filepath.Join(baseDir, "source")
	outDir := filepath.Join(baseDir, "output")

	src := createCollisionTestPNG(t, srcDir, "ignored_clean.png", 10, 20, 30)

	brClean := pkgwatermark.BatchResult{
		Detected: false,
		Score:    0.02,
	}

	f := false
	inNoCopy := RemoverInput{
		OutputDir: outDir,
		CopyClean: &f,
	}
	res, outPath := finishItem(context.Background(), inNoCopy, outDir, src, src, brClean, opts)
	if res.Status != pkgwatermark.StatusSkipped {
		t.Fatalf("expected Status=skipped when CopyClean is disabled, got %s", res.Status)
	}
	if res.Reason != pkgwatermark.ReasonNoWatermark {
		t.Fatalf("expected Reason=%q, got %q", pkgwatermark.ReasonNoWatermark, res.Reason)
	}
	if outPath != src {
		t.Fatalf("expected original path when not copied, got %s", outPath)
	}

	// Ensure nothing was written to outDir
	destPath := filepath.Join(outDir, "ignored_clean.png")
	if _, err := os.Stat(destPath); err == nil {
		t.Fatalf("destination file should not have been created when CopyClean is false")
	}
}

func TestCollision_PreserveTree(t *testing.T) {
	opts := Options{Provenance: pkgwatermark.NewProvenanceStore()}
	baseDir := t.TempDir()
	mediaDir := filepath.Join(baseDir, "media")
	outDir := filepath.Join(baseDir, "output")

	nestedRel := filepath.Join("nature", "animals", "cat.png")
	absSrc := createCollisionTestPNG(t, filepath.Join(mediaDir, "nature", "animals"), "cat.png", 80, 160, 240)

	br := pkgwatermark.BatchResult{
		Detected:     true,
		Score:        0.94,
		Output:       []byte("cleaned-cat"),
		OutputFormat: pkgwatermark.FormatPNG,
		Info:         pkgwatermark.Info{Size: 48},
	}

	preserve := true
	in := RemoverInput{
		OutputDir:    outDir,
		PreserveTree: &preserve,
	}

	res, outPath := finishItem(context.Background(), in, outDir, nestedRel, absSrc, br, opts)
	if res.Status != pkgwatermark.StatusProcessed {
		t.Fatalf("expected Status=processed, got %s (error=%s)", res.Status, res.Error)
	}

	expectedSubPath := filepath.Join(outDir, "nature", "animals", "cat_nowm.png")
	if filepath.Clean(outPath) != filepath.Clean(expectedSubPath) {
		t.Fatalf("expected preserved tree path %s, got %s", expectedSubPath, outPath)
	}

	if _, err := os.Stat(expectedSubPath); err != nil {
		t.Fatalf("expected preserved tree output file does not exist: %v", err)
	}
}

func TestResolveFilePath_AllowsLeadingDoubleDotsFilename(t *testing.T) {
	mediaDir := filepath.Join(t.TempDir(), "media")
	got := engine.ResolveMediaPath("..foo.png", mediaDir)
	want := filepath.Join(mediaDir, "..foo.png")
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	if bad := engine.ResolveMediaPath("../escape.png", mediaDir); bad != "" {
		t.Fatalf("expected traversal outside mediaDir to return empty, got %q", bad)
	}
	if bad := engine.ResolveMediaPath("..", mediaDir); bad != "" {
		t.Fatalf("expected .. to return empty, got %q", bad)
	}
}

func TestFinishItem_InPlaceExtensionChangeRefusesExistingTarget(t *testing.T) {
	dir := t.TempDir()
	srcWebp := filepath.Join(dir, "sample.webp")
	existingPng := filepath.Join(dir, "sample.png")
	if err := os.WriteFile(srcWebp, []byte("webp-original"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(existingPng, []byte("png-precious"), 0o644); err != nil {
		t.Fatal(err)
	}

	br := pkgwatermark.BatchResult{
		Detected:     true,
		Score:        0.95,
		Output:       []byte("new-png-data"),
		OutputFormat: pkgwatermark.FormatPNG,
		Info:         pkgwatermark.Info{Size: 48},
	}
	res, outPath := finishItem(context.Background(), RemoverInput{InPlace: true}, "", srcWebp, srcWebp, br)
	if outPath != "" || !strings.Contains(res.Error, "already exists") {
		t.Fatalf("expected collision error when target .png exists, got outPath=%q res=%+v", outPath, res)
	}
	if raw, err := os.ReadFile(srcWebp); err != nil || string(raw) != "webp-original" {
		t.Fatalf("source .webp must remain intact, got %q err=%v", string(raw), err)
	}
	if raw, err := os.ReadFile(existingPng); err != nil || string(raw) != "png-precious" {
		t.Fatalf("existing .png must not be overwritten, got %q err=%v", string(raw), err)
	}
}

func TestRemoverInput_JSONMarshalUnmarshal(t *testing.T) {
	in := RemoverInput{
		Image: RemoverImage{
			MediaRef: engine.MediaRef{LocalURL: "one.png", Kind: engine.MediaImage, MimeType: "image/png"},
		},
		Images: RemoverImages{
			{LocalURL: "two.jpg", Kind: engine.MediaImage},
		},
	}
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal RemoverInput: %v", err)
	}
	var out RemoverInput
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal RemoverInput: %v", err)
	}
	if !reflect.DeepEqual(in, out) {
		t.Fatalf("roundtrip mismatch: got %+v want %+v", out, in)
	}
}
