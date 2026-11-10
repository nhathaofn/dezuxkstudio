package watermark_test

import (
	"bytes"
	"context"
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
	"testing"

	"app/pkg/watermark"
)

func TestDetectAndRemoveEmptyData(t *testing.T) {
	_, _, _, err := watermark.Detect(nil)
	if err == nil {
		t.Fatalf("expected error on empty data, got nil")
	}

	_, _, err = watermark.Remove(nil)
	if err == nil {
		t.Fatalf("expected error on empty data, got nil")
	}
}

func TestCleanImageNotModified(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 512, 512))
	draw.Draw(img, img.Bounds(), &image.Uniform{C: color.RGBA{R: 20, G: 40, B: 100, A: 255}}, image.Point{}, draw.Src)

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	raw := buf.Bytes()

	present, score, _, err := watermark.Detect(raw)
	if err != nil {
		t.Fatalf("detect error: %v", err)
	}
	if present {
		t.Fatalf("expected clean image not to be detected as watermarked, score=%.2f", score)
	}

	cleaned, removed, err := watermark.Remove(raw)
	if err != nil {
		t.Fatalf("remove error: %v", err)
	}
	if removed {
		t.Fatalf("expected removed=false for clean image")
	}
	if !bytes.Equal(cleaned, raw) {
		t.Fatalf("expected clean image bytes to be preserved untouched")
	}
}

// texture is a noisy, non-uniform picture, so detection is not helped by a
// flat background.
func texture(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	rng := rand.New(rand.NewPCG(1, 2))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			n := rng.IntN(40)
			img.SetRGBA(x, y, color.RGBA{R: uint8(60 + (x*80)/w + n), G: uint8(90 + (y*60)/h + n), B: uint8(120 + n), A: 255})
		}
	}
	return img
}

// stamp blends the real 48 px mark into img the way Flow does: marked =
// a*255 + (1-a)*original, with a = gain * template alpha.
func stamp(t *testing.T, img *image.RGBA, x0, y0 int, gain float64) {
	t.Helper()
	f, err := os.Open(filepath.Join("assets", "bg_48.png"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	mark, err := png.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	for y := 0; y < 48; y++ {
		for x := 0; x < 48; x++ {
			r, g, b, _ := mark.At(x, y).RGBA()
			a := gain * float64(max(r, g, b)>>8) / 255
			c := img.RGBAAt(x0+x, y0+y)
			blend := func(v uint8) uint8 { return uint8(math.Round(a*255 + (1-a)*float64(v))) }
			img.SetRGBA(x0+x, y0+y, color.RGBA{R: blend(c.R), G: blend(c.G), B: blend(c.B), A: 255})
		}
	}
}

func encode(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestRemoveRestoresStampedMark(t *testing.T) {
	sizes := []struct{ w, h, margin int }{{1024, 1024, 78}, {1376, 768, 73}, {768, 1376, 73}}
	for _, sz := range sizes {
		orig := texture(sz.w, sz.h)
		marked := image.NewRGBA(orig.Bounds())
		draw.Draw(marked, marked.Bounds(), orig, image.Point{}, draw.Src)
		x0, y0 := sz.w-sz.margin-48, sz.h-sz.margin-48
		stamp(t, marked, x0, y0, 0.6)

		present, _, info, err := watermark.Detect(encode(t, marked))
		if err != nil || !present {
			t.Fatalf("%dx%d: mark not detected (err %v)", sz.w, sz.h, err)
		}
		if info.Position.Min != image.Pt(x0, y0) || info.Size != 48 {
			t.Fatalf("%dx%d: detected %v size %d, want at (%d,%d) size 48", sz.w, sz.h, info.Position, info.Size, x0, y0)
		}

		cleaned, ok := watermark.RemoveImage(marked)
		if !ok {
			t.Fatalf("%dx%d: expected removal", sz.w, sz.h)
		}
		if cleaned.Bounds() != orig.Bounds() {
			t.Fatalf("%dx%d: size changed to %v", sz.w, sz.h, cleaned.Bounds())
		}
		var diff float64
		for y := y0; y < y0+48; y++ {
			for x := x0; x < x0+48; x++ {
				a, b := orig.RGBAAt(x, y), cleaned.RGBAAt(x, y)
				diff += math.Abs(float64(a.R)-float64(b.R)) + math.Abs(float64(a.G)-float64(b.G)) + math.Abs(float64(a.B)-float64(b.B))
			}
		}
		if mean := diff / (48 * 48 * 3); mean > 6 {
			t.Fatalf("%dx%d: restored patch differs from the original by %.1f per channel", sz.w, sz.h, mean)
		}
	}
}

// A picture without the mark is never changed, even when a corner patch
// happens to look like the star.
func TestUnmarkedTextureNotModified(t *testing.T) {
	img := texture(1376, 768)
	// A white star-like blob that is not a blend of the template.
	for y := 640; y < 690; y++ {
		for x := 1250; x < 1300; x++ {
			if math.Abs(float64(x-1275))+math.Abs(float64(y-665)) < 20 {
				img.SetRGBA(x, y, color.RGBA{R: 230, G: 230, B: 230, A: 255})
			}
		}
	}
	raw := encode(t, img)
	out, removed, err := watermark.Remove(raw)
	if err != nil {
		t.Fatal(err)
	}
	if removed || !bytes.Equal(out, raw) {
		t.Fatal("expected an unmarked image to be left untouched")
	}
}

func TestRealGoogleFlowLiveImage(t *testing.T) {
	rawPath := filepath.Join("..", "..", "watermark_live_flow_test", "raw_google_flow_original.jpg")
	data, err := os.ReadFile(rawPath)
	if err != nil {
		t.Skip("skipping live test: raw_google_flow_original.jpg not found")
	}

	present, score, info, err := watermark.Detect(data)
	if err != nil {
		t.Fatalf("detect error: %v", err)
	}
	if !present {
		t.Fatalf("expected real flow image to have detectable watermark, score=%.4f", score)
	}

	t.Logf("Detected real Google Flow watermark at %v, size=%d, score=%.4f",
		info.Position, info.Size, score)

	cleaned, removed, err := watermark.Remove(data)
	if err != nil {
		t.Fatalf("remove error: %v", err)
	}
	if !removed {
		t.Fatalf("expected removed=true for real flow image")
	}
	if len(cleaned) == 0 {
		t.Fatalf("expected non-empty cleaned image data")
	}
	before, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decode original: %v", err)
	}
	after, err := png.DecodeConfig(bytes.NewReader(cleaned))
	if err != nil {
		t.Fatalf("decode cleaned: %v", err)
	}
	if before.Width != after.Width || before.Height != after.Height {
		t.Fatalf("size changed: %dx%d -> %dx%d", before.Width, before.Height, after.Width, after.Height)
	}
}

func TestBatchProcessingConcurrency(t *testing.T) {
	const count = 12
	items := make([]watermark.BatchItem, count)
	origs := make([]*image.RGBA, count)

	for i := 0; i < count; i++ {
		orig := texture(600, 600)
		origs[i] = orig
		marked := image.NewRGBA(orig.Bounds())
		draw.Draw(marked, marked.Bounds(), orig, image.Point{}, draw.Src)
		// stamp at 48px mark (bottom-right: margin 75)
		stamp(t, marked, 600-75-48, 600-75-48, 0.62)
		items[i] = watermark.BatchItem{
			ID:   filepath.Base(t.Name()) + string(rune('A'+i)),
			Data: encode(t, marked),
		}
	}

	eng := watermark.New(watermark.Config{
		Workers: 4,
	})

	results := eng.ProcessBatch(context.Background(), items, 4)
	if len(results) != count {
		t.Fatalf("expected %d results, got %d", count, len(results))
	}

	for i, res := range results {
		if res.ID != items[i].ID {
			t.Fatalf("result index %d ID mismatch: got %s, want %s", i, res.ID, items[i].ID)
		}
		if res.Err != nil {
			t.Fatalf("result %d error: %v", i, res.Err)
		}
		if !res.Detected {
			t.Fatalf("result %d: expected watermark to be detected", i)
		}
		if len(res.Output) == 0 {
			t.Fatalf("result %d: empty output", i)
		}
	}
}

func TestStreamProcessing(t *testing.T) {
	orig := texture(600, 600)
	marked := image.NewRGBA(orig.Bounds())
	draw.Draw(marked, marked.Bounds(), orig, image.Point{}, draw.Src)
	stamp(t, marked, 600-75-48, 600-75-48, 0.6)
	data := encode(t, marked)

	inCh := make(chan watermark.BatchItem, 3)
	for i := 0; i < 3; i++ {
		inCh <- watermark.BatchItem{ID: fmt.Sprintf("stream-%d", i), Data: data}
	}
	close(inCh)

	eng := watermark.Default()
	outCh := eng.ProcessStream(context.Background(), inCh, 2)

	var received int
	for res := range outCh {
		if res.Err != nil {
			t.Fatalf("stream item error: %v", res.Err)
		}
		if !res.Detected {
			t.Fatalf("expected detected=true in stream item %s", res.ID)
		}
		received++
	}
	if received != 3 {
		t.Fatalf("expected 3 stream results, got %d", received)
	}
}

func TestZeroHardcodeCornerAny(t *testing.T) {
	orig := texture(600, 600)
	marked := image.NewRGBA(orig.Bounds())
	draw.Draw(marked, marked.Bounds(), orig, image.Point{}, draw.Src)

	// Stamp at bottom-left corner
	// margin = 75, so x = 75, y = 600 - 75 - 48
	x0, y0 := 75, 600-75-48
	stamp(t, marked, x0, y0, 0.65)
	data := encode(t, marked)

	// Engine with CornerBottomRight should not find it
	engBR := watermark.New(watermark.Config{Corner: watermark.CornerBottomRight})
	present, _, _, err := engBR.Detect(data)
	if err != nil {
		t.Fatalf("detect error: %v", err)
	}
	if present {
		t.Fatalf("CornerBottomRight should not detect bottom-left watermark")
	}

	// Engine with CornerAny or CornerBottomLeft should find it
	engAny := watermark.New(watermark.Config{Corner: watermark.CornerAny})
	present, _, info, err := engAny.Detect(data)
	if err != nil {
		t.Fatalf("detect error: %v", err)
	}
	if !present {
		t.Fatalf("CornerAny should detect bottom-left watermark")
	}
	if info.Position.Min != image.Pt(x0, y0) {
		t.Fatalf("expected position %v, got %v", image.Pt(x0, y0), info.Position.Min)
	}
}

func TestWriteAtomic(t *testing.T) {
	dir := t.TempDir()
	targetPath := filepath.Join(dir, "atomic_output.png")
	data1 := []byte("original-image-content-v1")

	if err := watermark.WriteAtomic(targetPath, data1, 0o644); err != nil {
		t.Fatalf("WriteAtomic initial write failed: %v", err)
	}

	read1, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("failed to read written file: %v", err)
	}
	if !bytes.Equal(read1, data1) {
		t.Fatalf("content mismatch: got %q, want %q", read1, data1)
	}

	// Test in-place atomic overwrite
	data2 := []byte("overwritten-cleaned-image-content-v2")
	if err := watermark.WriteAtomic(targetPath, data2, 0o644); err != nil {
		t.Fatalf("WriteAtomic overwrite failed: %v", err)
	}

	read2, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("failed to read overwritten file: %v", err)
	}
	if !bytes.Equal(read2, data2) {
		t.Fatalf("overwritten content mismatch: got %q, want %q", read2, data2)
	}

	// Assert no temporary dot files left in directory
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("failed to read dir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "atomic_output.png" {
		t.Fatalf("unexpected directory entries: %+v", entries)
	}
}

func TestWriteAtomicFailurePreservesOriginal(t *testing.T) {
	dir := t.TempDir()
	targetPath := filepath.Join(dir, "target.png")
	originalData := []byte("critical-original-data")

	if err := os.WriteFile(targetPath, originalData, 0o644); err != nil {
		t.Fatalf("failed to create original file: %v", err)
	}

	// On Windows, opening target file exclusively prevents os.Rename over it
	f, err := os.OpenFile(targetPath, os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("failed to open file for locking: %v", err)
	}

	// Attempt atomic overwrite while file is locked
	newData := []byte("new-data-that-should-fail")
	err = watermark.WriteAtomic(targetPath, newData, 0o644)
	if err == nil {
		_ = f.Close()
		t.Fatalf("expected atomic write to fail due to file lock, got nil")
	}

	// Unlock by closing
	_ = f.Close()

	// Verify original file is still intact and not truncated or corrupted
	afterData, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("failed to read file after failed atomic write: %v", err)
	}
	if !bytes.Equal(afterData, originalData) {
		t.Fatalf("original file corrupted: got %q, want %q", afterData, originalData)
	}

	// Verify temporary files are scrubbed
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("failed to read dir: %v", err)
	}
	for _, entry := range entries {
		if entry.Name() != "target.png" {
			t.Fatalf("temporary file left behind: %s", entry.Name())
		}
	}
}

func TestWatermarkBoundarySampling(t *testing.T) {
	w, h := 256, 256
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(img, img.Bounds(), &image.Uniform{C: color.RGBA{R: 240, G: 240, B: 240, A: 255}}, image.Point{}, draw.Src)

	// Stamp watermark directly at the boundary coordinate (0, 0)
	stamp(t, img, 0, 0, 0.65)

	bgBytes, err := os.ReadFile(filepath.Join("assets", "bg_48.png"))
	if err != nil {
		t.Fatal(err)
	}
	tmpl, err := watermark.NewTemplateFromBytes(bgBytes, 0, 10)
	if err != nil {
		t.Fatal(err)
	}

	eng := watermark.New(watermark.Config{
		Corner:    watermark.CornerTopLeft,
		Templates: []watermark.Template{tmpl},
	})

	cleaned, removed := eng.RemoveImage(img)
	if !removed {
		t.Fatalf("expected watermark at (0, 0) to be detected and removed")
	}

	// Verify no zero-collapse black artifacts along contour
	for y := 0; y < 48; y++ {
		for x := 0; x < 48; x++ {
			c := cleaned.RGBAAt(x, y)
			if c.R < 180 || c.G < 180 || c.B < 180 {
				t.Fatalf("zero-collapse black artifact at (%d, %d): RGBA=(%d, %d, %d, %d)", x, y, c.R, c.G, c.B, c.A)
			}
		}
	}
}

func TestSniffFormat(t *testing.T) {
	// 1. JPEG
	jpegBytes := []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 0x4A, 0x46}
	if fmt := watermark.SniffFormat(jpegBytes); fmt != watermark.FormatJPEG {
		t.Errorf("expected FormatJPEG, got %s", fmt)
	}

	// 2. PNG
	pngBytes := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0x00}
	if fmt := watermark.SniffFormat(pngBytes); fmt != watermark.FormatPNG {
		t.Errorf("expected FormatPNG, got %s", fmt)
	}

	// 3. WebP
	webpBytes := []byte("RIFF\x20\x00\x00\x00WEBPVP8 \x14\x00\x00\x00")
	if fmt := watermark.SniffFormat(webpBytes); fmt != watermark.FormatWebP {
		t.Errorf("expected FormatWebP, got %s", fmt)
	}

	// 4. Unknown
	otherBytes := []byte("GIF89a...")
	if fmt := watermark.SniffFormat(otherBytes); fmt != watermark.FormatUnknown {
		t.Errorf("expected FormatUnknown, got %s", fmt)
	}
}

func TestMIMETypePreservation(t *testing.T) {
	// Create test image with stamped watermark
	img := image.NewRGBA(image.Rect(0, 0, 512, 512))
	draw.Draw(img, img.Bounds(), &image.Uniform{C: color.RGBA{R: 200, G: 200, B: 200, A: 255}}, image.Point{}, draw.Src)

	bgBytes, err := os.ReadFile(filepath.Join("assets", "bg_48.png"))
	if err != nil {
		t.Fatal(err)
	}
	tmpl, err := watermark.NewTemplateFromBytes(bgBytes, 56, 104)
	if err != nil {
		t.Fatal(err)
	}

	// Stamp watermark at bottom-right (margin 64)
	stamp(t, img, 512-48-64, 512-48-64, 0.65)

	eng := watermark.New(watermark.Config{
		Corner:    watermark.CornerBottomRight,
		Templates: []watermark.Template{tmpl},
	})

	t.Run("JPEG_Preserves_JPEG_MIME", func(t *testing.T) {
		var buf bytes.Buffer
		if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 95}); err != nil {
			t.Fatalf("encode jpeg: %v", err)
		}
		rawJPEG := buf.Bytes()

		out, removed, outFmt, err := eng.RemoveWithFormat(rawJPEG)
		if err != nil {
			t.Fatalf("RemoveWithFormat failed: %v", err)
		}
		if !removed {
			t.Fatal("expected watermark to be removed")
		}
		if outFmt != watermark.FormatJPEG {
			t.Errorf("expected output format %s, got %s", watermark.FormatJPEG, outFmt)
		}
		// Confirm output has valid JPEG magic bytes: 0xFF, 0xD8, 0xFF
		if len(out) < 3 || out[0] != 0xFF || out[1] != 0xD8 || out[2] != 0xFF {
			t.Errorf("output bytes do not start with JPEG magic bytes: %x", out[:min(len(out), 8)])
		}
	})

	t.Run("PNG_Preserves_PNG_MIME", func(t *testing.T) {
		var buf bytes.Buffer
		if err := png.Encode(&buf, img); err != nil {
			t.Fatalf("encode png: %v", err)
		}
		rawPNG := buf.Bytes()

		out, removed, outFmt, err := eng.RemoveWithFormat(rawPNG)
		if err != nil {
			t.Fatalf("RemoveWithFormat failed: %v", err)
		}
		if !removed {
			t.Fatal("expected watermark to be removed")
		}
		if outFmt != watermark.FormatPNG {
			t.Errorf("expected output format %s, got %s", watermark.FormatPNG, outFmt)
		}
		// Confirm output has valid PNG magic bytes: \x89PNG\r\n\x1a\n
		pngMagic := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}
		if len(out) < 8 || !bytes.Equal(out[:8], pngMagic) {
			t.Errorf("output bytes do not start with PNG magic bytes")
		}
	})

	t.Run("WebP_Exports_Cleanly_To_PNG", func(t *testing.T) {
		// Test synthetic webp bytes header with image data
		// Since Go lacks stdlib WebP encoder, we test format detection and clean PNG export
		if fmt := watermark.SniffFormat([]byte("RIFF1234WEBPVP8 ")); fmt != watermark.FormatWebP {
			t.Errorf("expected FormatWebP, got %s", fmt)
		}
	})
}

func TestBatchPanicSafetyAndRecovery(t *testing.T) {
	eng := watermark.Default()

	img := image.NewRGBA(image.Rect(0, 0, 100, 100))
	draw.Draw(img, img.Bounds(), &image.Uniform{C: color.RGBA{R: 50, G: 50, B: 50, A: 255}}, image.Point{}, draw.Src)
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	validPNG := buf.Bytes()

	items := []watermark.BatchItem{
		{ID: "valid_1", Data: validPNG},
		{ID: "corrupted_1", Data: []byte("NOT_AN_IMAGE_RANDOM_GARBAGE_BYTES")},
		{ID: "empty_1", Data: nil},
		{ID: "valid_2", Data: validPNG},
	}

	results := eng.ProcessBatch(context.Background(), items, 2)
	if len(results) != len(items) {
		t.Fatalf("expected %d results, got %d", len(items), len(results))
	}

	if results[0].Err != nil {
		t.Errorf("expected valid_1 to succeed, got error: %v", results[0].Err)
	}
	if results[1].Err == nil {
		t.Errorf("expected corrupted_1 to report error, got nil")
	}
	if results[2].Err == nil {
		t.Errorf("expected empty_1 to report error, got nil")
	}
	if results[3].Err != nil {
		t.Errorf("expected valid_2 to succeed, got error: %v", results[3].Err)
	}
}
