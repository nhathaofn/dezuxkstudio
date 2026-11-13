package watermark

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"testing"
)

// TestAdversarial_Reconstruct_EncodeCleaned validates that the watermark reconstruct pipeline
// preserves MIME type for JPEG and PNG, and cleanly transcodes WebP to lossless PNG with valid magic bytes.
func TestAdversarial_Reconstruct_EncodeCleaned(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	draw.Draw(img, img.Bounds(), &image.Uniform{C: color.RGBA{R: 100, G: 150, B: 200, A: 255}}, image.Point{}, draw.Src)

	t.Run("JPEG_Produces_Valid_JPEG_MagicBytes", func(t *testing.T) {
		out, outFmt, err := encodeCleaned(img, FormatJPEG)
		if err != nil {
			t.Fatalf("encodeCleaned failed for JPEG: %v", err)
		}
		if outFmt != FormatJPEG {
			t.Fatalf("expected FormatJPEG, got %s", outFmt)
		}
		if len(out) < 3 || out[0] != 0xFF || out[1] != 0xD8 || out[2] != 0xFF {
			t.Fatalf("corrupted JPEG header: expected 0xFF 0xD8 0xFF, got %x", out[:min(len(out), 4)])
		}
	})

	t.Run("PNG_Produces_Valid_PNG_MagicBytes", func(t *testing.T) {
		out, outFmt, err := encodeCleaned(img, FormatPNG)
		if err != nil {
			t.Fatalf("encodeCleaned failed for PNG: %v", err)
		}
		if outFmt != FormatPNG {
			t.Fatalf("expected FormatPNG, got %s", outFmt)
		}
		pngMagic := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}
		if len(out) < 8 || !bytes.Equal(out[:8], pngMagic) {
			t.Fatalf("corrupted PNG header: expected standard PNG magic bytes")
		}
	})

	t.Run("WebP_Cleanly_Transcodes_To_Lossless_PNG", func(t *testing.T) {
		out, outFmt, err := encodeCleaned(img, FormatWebP)
		if err != nil {
			t.Fatalf("encodeCleaned failed for WebP: %v", err)
		}
		if outFmt != FormatPNG {
			t.Fatalf("expected FormatPNG for WebP transcode, got %s", outFmt)
		}
		pngMagic := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}
		if len(out) < 8 || !bytes.Equal(out[:8], pngMagic) {
			t.Fatalf("transcoded WebP missing standard PNG magic header: %x", out[:min(len(out), 8)])
		}
	})

	t.Run("Unknown_Format_Falls_Back_To_PNG", func(t *testing.T) {
		out, outFmt, err := encodeCleaned(img, FormatUnknown)
		if err != nil {
			t.Fatalf("encodeCleaned failed for FormatUnknown: %v", err)
		}
		if outFmt != FormatPNG {
			t.Fatalf("expected FormatPNG for unknown format fallback, got %s", outFmt)
		}
		pngMagic := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}
		if len(out) < 8 || !bytes.Equal(out[:8], pngMagic) {
			t.Fatalf("unknown format fallback missing standard PNG magic header")
		}
	})
}
