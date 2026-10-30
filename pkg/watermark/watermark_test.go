package watermark_test

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	_ "image/jpeg"
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
