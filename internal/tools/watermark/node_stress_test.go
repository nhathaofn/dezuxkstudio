package watermark_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"app/internal/tools/watermark"
	pkgwatermark "app/pkg/watermark"
)

func createWatermarkedImageForStress(t *testing.T, dir, filename string) (string, []byte) {
	t.Helper()
	w, h := 400, 400
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	rng := rand.New(rand.NewPCG(42, 84))
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
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("failed to encode png: %v", err)
	}

	p := filepath.Join(dir, filename)
	data := buf.Bytes()
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatalf("failed to write watermarked png: %v", err)
	}
	return p, data
}

func createCleanImageForStress(t *testing.T, dir, filename string) (string, []byte) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 200, 200))
	draw.Draw(img, img.Bounds(), &image.Uniform{C: color.RGBA{R: 50, G: 150, B: 200, A: 255}}, image.Point{}, draw.Src)
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("failed to encode clean png: %v", err)
	}
	p := filepath.Join(dir, filename)
	data := buf.Bytes()
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatalf("failed to write clean png: %v", err)
	}
	return p, data
}

// TestAdversarial_Node_InPlace_MixedBatchStress simulates a batch with:
// - Watermarked files that succeed
// - Watermarked files that are locked by another process (must fail gracefully and remain untouched)
// - Clean files (unmarked) that must not be modified
// - Verification of zero lingering .tmp-* files
func TestAdversarial_Node_InPlace_MixedBatchStress(t *testing.T) {
	tempDir := t.TempDir()

	const numFiles = 10
	type fileMeta struct {
		path      string
		origBytes []byte
		isMarked  bool
		isLocked  bool
		lockFile  *os.File
	}

	files := make([]fileMeta, numFiles)
	rawPaths := make([]string, numFiles)

	for i := 0; i < numFiles; i++ {
		name := fmt.Sprintf("img_%02d.png", i)
		if i%3 == 0 {
			// Clean unmarked image
			p, data := createCleanImageForStress(t, tempDir, name)
			files[i] = fileMeta{path: p, origBytes: data, isMarked: false}
		} else {
			// Watermarked image
			p, data := createWatermarkedImageForStress(t, tempDir, name)
			files[i] = fileMeta{path: p, origBytes: data, isMarked: true}
		}
		rawPaths[i] = files[i].path
	}

	// Lock files 1 and 4 (which are watermarked)
	for _, idx := range []int{1, 4} {
		f, err := os.OpenFile(files[idx].path, os.O_RDWR, 0)
		if err != nil {
			t.Fatalf("failed to lock file %d: %v", idx, err)
		}
		files[idx].isLocked = true
		files[idx].lockFile = f
	}

	// Defer unlocking
	defer func() {
		for _, f := range files {
			if f.lockFile != nil {
				_ = f.lockFile.Close()
			}
		}
	}()

	node := watermark.NewNode(watermark.Options{})
	inputMap := map[string]interface{}{
		"images":  rawPaths,
		"inplace": true,
		"workers": 4,
	}
	inputBytes, _ := json.Marshal(inputMap)

	outRaw, err := node.Run(context.Background(), json.RawMessage(inputBytes))
	if err != nil {
		t.Fatalf("node.Run returned unexpected top-level error: %v", err)
	}

	// Unlock files so we can inspect them
	for i := range files {
		if files[i].lockFile != nil {
			_ = files[i].lockFile.Close()
			files[i].lockFile = nil
		}
	}

	outBytes, _ := json.Marshal(outRaw)
	var out watermark.NodeOutput
	if err := json.Unmarshal(outBytes, &out); err != nil {
		t.Fatalf("failed to unmarshal output: %v", err)
	}

	if out.TotalCount != numFiles {
		t.Fatalf("expected TotalCount=%d, got %d", numFiles, out.TotalCount)
	}
	if len(out.Results) != numFiles {
		t.Fatalf("expected len(Results)=%d, got %d", numFiles, len(out.Results))
	}

	for i, f := range files {
		currentData, err := os.ReadFile(f.path)
		if err != nil {
			t.Fatalf("failed to read file %s: %v", f.path, err)
		}

		res := out.Results[i]

		if f.isLocked {
			// 1. Must report an error
			if res.Error == "" {
				t.Fatalf("file %d was locked, but result reported no error: %+v", i, res)
			}
			// 2. Must be 100% byte-for-byte identical to original
			if !bytes.Equal(currentData, f.origBytes) {
				t.Fatalf("locked file %d was corrupted or truncated: got len %d, want len %d",
					i, len(currentData), len(f.origBytes))
			}
		} else if !f.isMarked {
			// Clean file: watermark was not detected, file should not be modified
			if res.Detected {
				t.Fatalf("unmarked file %d was reported as detected", i)
			}
			if !bytes.Equal(currentData, f.origBytes) {
				t.Fatalf("clean file %d was unexpectedly modified", i)
			}
		} else {
			// Unlocked watermarked file: must have succeeded
			if res.Error != "" {
				t.Fatalf("file %d had unexpected error: %s", i, res.Error)
			}
			if !res.Detected {
				t.Fatalf("file %d expected detected=true", i)
			}
			// Verify watermark was indeed removed
			detected, _, _, err := pkgwatermark.Detect(currentData)
			if err != nil {
				t.Fatalf("detect on cleaned file %d failed: %v", i, err)
			}
			if detected {
				t.Fatalf("file %d still has watermark after processing", i)
			}
		}
	}

	// Verify zero lingering temporary dot-files in directory
	entries, err := os.ReadDir(tempDir)
	if err != nil {
		t.Fatalf("failed to read dir: %v", err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") {
			t.Fatalf("lingering temporary dot-file found: %s", entry.Name())
		}
	}
}

// TestAdversarial_Node_InPlace_PermissionPreservation verifies that original
// permissions are strictly preserved during in-place overwrite.
func TestAdversarial_Node_InPlace_PermissionPreservation(t *testing.T) {
	tempDir := t.TempDir()
	p, _ := createWatermarkedImageForStress(t, tempDir, "perm_test.png")

	// Set permission to 0o600
	if err := os.Chmod(p, 0o600); err != nil {
		t.Fatalf("failed to chmod: %v", err)
	}
	fiBefore, err := os.Stat(p)
	if err != nil {
		t.Fatalf("stat failed: %v", err)
	}
	expectedPerm := fiBefore.Mode().Perm()

	node := watermark.NewNode(watermark.Options{})
	inputMap := map[string]interface{}{
		"image":   p,
		"inplace": true,
	}
	inBytes, _ := json.Marshal(inputMap)
	_, err = node.Run(context.Background(), json.RawMessage(inBytes))
	if err != nil {
		t.Fatalf("node run failed: %v", err)
	}

	fiAfter, err := os.Stat(p)
	if err != nil {
		t.Fatalf("stat after failed: %v", err)
	}
	if fiAfter.Mode().Perm() != expectedPerm {
		t.Fatalf("permissions mismatch: got %v, want %v", fiAfter.Mode().Perm(), expectedPerm)
	}
}

// TestAdversarial_Node_InPlace_ContextCancellation tests behavior when
// context is cancelled midway through a large batch.
func TestAdversarial_Node_InPlace_ContextCancellation(t *testing.T) {
	tempDir := t.TempDir()
	const count = 16
	filePaths := make([]string, count)
	origHashes := make(map[string][32]byte)

	for i := 0; i < count; i++ {
		p, data := createWatermarkedImageForStress(t, tempDir, fmt.Sprintf("cancel_%02d.png", i))
		filePaths[i] = p
		origHashes[p] = sha256.Sum256(data)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	node := watermark.NewNode(watermark.Options{})
	inputMap := map[string]interface{}{
		"images":  filePaths,
		"inplace": true,
		"workers": 1, // Slow down to ensure cancellation interrupts midway
	}
	inBytes, _ := json.Marshal(inputMap)

	// Run with timeout context
	_, _ = node.Run(ctx, json.RawMessage(inBytes))

	// Every file must either be:
	// - 100% unmodified original bytes (hash matches original)
	// - OR valid completed PNG with no watermark (valid PNG header & decode)
	// NEVER an empty 0-byte or corrupt partial file!
	for _, p := range filePaths {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("failed to read file %s: %v", p, err)
		}
		if len(data) == 0 {
			t.Fatalf("file %s is empty (0 bytes) after context cancellation!", p)
		}

		h := sha256.Sum256(data)
		if h != origHashes[p] {
			// Must be a valid decoded PNG
			_, err := png.Decode(bytes.NewReader(data))
			if err != nil {
				t.Fatalf("file %s corrupted by cancellation: %v", p, err)
			}
		}
	}

	// Verify zero lingering .tmp-* files
	entries, err := os.ReadDir(tempDir)
	if err != nil {
		t.Fatalf("failed to read dir: %v", err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") {
			t.Fatalf("lingering temp dot-file left behind after cancellation: %s", entry.Name())
		}
	}
}
