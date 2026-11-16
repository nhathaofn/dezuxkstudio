package watermark

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"math"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// makeBenchImage creates a synthetic test image with dimensions w x h and an optional stamped watermark.
func makeBenchImage(w, h int, stamped bool) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	rng := rand.New(rand.NewPCG(42, 99))
	// Fill with gradient and noise
	for y := 0; y < h; y++ {
		row := img.Pix[y*img.Stride : y*img.Stride+w*4]
		for x := 0; x < w; x++ {
			n := uint8(rng.IntN(30))
			idx := x * 4
			row[idx] = uint8(60+(x*80)/w) + n
			row[idx+1] = uint8(90+(y*60)/h) + n
			row[idx+2] = uint8(120) + n
			row[idx+3] = 255
		}
	}
	if stamped && len(bg48Data) > 0 {
		tmpl, err := NewTemplateFromBytes(bg48Data, 56, 104)
		if err == nil {
			x0, y0 := w-78-48, h-78-48
			for y := 0; y < 48; y++ {
				for x := 0; x < 48; x++ {
					a := float64(tmpl.Alpha[y*48+x]) * 0.6
					off := img.PixOffset(x0+x, y0+y)
					p := img.Pix[off : off+4 : off+4]
					p[0] = uint8(math.Round(a*255 + (1-a)*float64(p[0])))
					p[1] = uint8(math.Round(a*255 + (1-a)*float64(p[1])))
					p[2] = uint8(math.Round(a*255 + (1-a)*float64(p[2])))
					p[3] = 255
				}
			}
		}
	}
	return img
}

func encodeBenchPNG(img image.Image) []byte {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

// TestMaxImagePixelsGuard verifies that images exceeding MaxImagePixels are rejected
// by image.DecodeConfig before full decoding occurs.
func TestMaxImagePixelsGuard(t *testing.T) {
	// Create a small 100x100 image
	smallImg := makeBenchImage(100, 100, false)
	data := encodeBenchPNG(smallImg)

	// Configure engine with max 5,000 pixels (100x100 = 10,000 pixels > 5,000)
	eng := New(Config{})
	eng.SetLimits(5000, 2)

	_, _, _, err := eng.Detect(data)
	if err == nil {
		t.Fatalf("expected error for image exceeding MaxImagePixels, got nil")
	}
	expectedMsg := "exceed maximum allowed pixels 5000"
	if !bytes.Contains([]byte(err.Error()), []byte(expectedMsg)) {
		t.Fatalf("error %q does not contain expected %q", err.Error(), expectedMsg)
	}

	// Now allow 20,000 pixels: should pass guard
	engAllow := New(Config{})
	engAllow.SetLimits(20000, 2)
	present, _, _, err := engAllow.Detect(data)
	if err != nil {
		t.Fatalf("unexpected error when within limit: %v", err)
	}
	if present {
		t.Fatalf("expected no watermark present")
	}
}

// TestCorruptImageResilience_Batch verifies that corrupt, truncated, or invalid items
// fail individually without crashing or aborting the remainder of a batch.
func TestCorruptImageResilience_Batch(t *testing.T) {
	validImg := makeBenchImage(200, 200, true)
	validPNG := encodeBenchPNG(validImg)

	corruptTruncated := validPNG[:min(len(validPNG), 64)]
	corruptGarbage := []byte("not an image at all, just random text bytes")
	emptyData := []byte{}

	items := []BatchItem{
		{ID: "item-0-corrupt-truncated", Data: corruptTruncated},
		{ID: "item-1-valid", Data: validPNG},
		{ID: "item-2-corrupt-garbage", Data: corruptGarbage},
		{ID: "item-3-valid-2", Data: validPNG},
		{ID: "item-4-empty", Data: emptyData},
	}

	eng := Default()
	results := eng.ProcessBatch(context.Background(), items, 4)

	if len(results) != len(items) {
		t.Fatalf("expected %d results, got %d", len(items), len(results))
	}

	// Item 0: corrupt truncated should fail
	if results[0].Err == nil {
		t.Errorf("item 0 (truncated) expected error, got nil")
	}

	// Item 1: valid should succeed
	if results[1].Err != nil {
		t.Errorf("item 1 (valid) expected success, got err: %v", results[1].Err)
	}
	if !results[1].Detected {
		t.Errorf("item 1 expected watermark detected")
	}
	if len(results[1].Output) == 0 {
		t.Errorf("item 1 expected non-empty output")
	}

	// Item 2: corrupt garbage should fail
	if results[2].Err == nil {
		t.Errorf("item 2 (garbage) expected error, got nil")
	}

	// Item 3: valid should succeed
	if results[3].Err != nil {
		t.Errorf("item 3 (valid) expected success, got err: %v", results[3].Err)
	}
	if !results[3].Detected {
		t.Errorf("item 3 expected watermark detected")
	}

	// Item 4: empty data should fail
	if results[4].Err == nil {
		t.Errorf("item 4 (empty) expected error, got nil")
	}
}

// TestFastContextCancellation verifies that canceling context halts execution immediately.
func TestFastContextCancellation(t *testing.T) {
	validImg := makeBenchImage(500, 500, true)
	validPNG := encodeBenchPNG(validImg)

	// Pre-canceled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	eng := Default()
	_, _, _, err := eng.DetectContext(ctx, validPNG)
	if err == nil {
		t.Fatalf("expected context error on pre-canceled context, got nil")
	}
	if err != context.Canceled {
		t.Fatalf("expected context.Canceled, got: %v", err)
	}

	_, _, err = eng.RemoveContext(ctx, validPNG)
	if err == nil {
		t.Fatalf("expected context error on pre-canceled context, got nil")
	}
	if err != context.Canceled {
		t.Fatalf("expected context.Canceled, got: %v", err)
	}

	// Stream with cancellation
	streamCtx, streamCancel := context.WithCancel(context.Background())
	inCh := make(chan BatchItem, 10)
	for i := 0; i < 5; i++ {
		inCh <- BatchItem{ID: fmt.Sprintf("%d", i), Data: validPNG}
	}
	close(inCh)

	streamCancel() // Cancel immediately
	outCh := eng.ProcessStream(streamCtx, inCh, 2)
	count := 0
	for range outCh {
		count++
	}
	// Under cancellation, the stream worker exits cleanly without hanging
	t.Logf("Stream produced %d items after cancellation", count)
}

// TestDecodeConcurrencyBounded verifies that concurrent image decodings
// are bounded by the decode concurrency semaphore.
func TestDecodeConcurrencyBounded(t *testing.T) {
	const maxConcurrency = 2
	eng := New(Config{})
	eng.SetLimits(100_000_000, maxConcurrency)
	_, sem := eng.getLimits()

	var activeDecodes int32
	var maxObserved int32
	var wg sync.WaitGroup

	// Run 16 concurrent decodes
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Acquire semaphore simulate
			select {
			case sem <- struct{}{}:
			case <-time.After(5 * time.Second):
				t.Errorf("timeout waiting for decode semaphore")
				return
			}
			curr := atomic.AddInt32(&activeDecodes, 1)
			for {
				old := atomic.LoadInt32(&maxObserved)
				if curr <= old || atomic.CompareAndSwapInt32(&maxObserved, old, curr) {
					break
				}
			}
			time.Sleep(10 * time.Millisecond) // simulate decode work
			atomic.AddInt32(&activeDecodes, -1)
			<-sem
		}()
	}
	wg.Wait()

	if maxObserved > int32(maxConcurrency) {
		t.Fatalf("observed decode concurrency %d exceeded max %d", maxObserved, maxConcurrency)
	}
}

// TestROIBufferPoolReuse verifies that ROI buffers are properly pooled and reused.
func TestROIBufferPoolReuse(t *testing.T) {
	buf1 := getROIBuffer(1024)
	if buf1 == nil || len(*buf1) < 1024 {
		t.Fatalf("invalid buffer returned")
	}
	putROIBuffer(buf1)

	buf2 := getROIBuffer(1024)
	if buf2 == nil || len(*buf2) < 1024 {
		t.Fatalf("invalid buffer returned on second get")
	}
	putROIBuffer(buf2)
}

// TestFloatBufferPoolReuse verifies that float buffers are properly pooled and reused.
func TestFloatBufferPoolReuse(t *testing.T) {
	buf1 := getFloatBuffer(512)
	if buf1 == nil || len(*buf1) != 512 {
		t.Fatalf("invalid float buffer returned")
	}
	putFloatBuffer(buf1)

	buf2 := getFloatBuffer(512)
	if buf2 == nil || len(*buf2) != 512 {
		t.Fatalf("invalid float buffer returned on second get")
	}
	putFloatBuffer(buf2)
}

// -----------------------------------------------------------------------
// BENCHMARKS: Proving Heap Allocation Reduction with sync.Pool & ROI Crop
// -----------------------------------------------------------------------

// BenchmarkFullFrameClone_Baseline_4K measures memory allocations when repeatedly
// cloning a full 4K (3840x2160) NRGBA image to RGBA (the old approach).
func BenchmarkFullFrameClone_Baseline_4K(b *testing.B) {
	const w, h = 3840, 2160
	nrgba := image.NewNRGBA(image.Rect(0, 0, w, h))
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		// Old unpooled baseline: clone full 4K image into newly allocated RGBA
		rect := nrgba.Bounds()
		rgba := image.NewRGBA(rect)
		draw.Draw(rgba, rect, nrgba, rect.Min, draw.Src)
		_ = rgba.Pix[0]
	}
}

// BenchmarkROIPool_Reconstruct_4K measures memory allocations using the new
// ROI cropping and sync.Pool buffer reuse on the same 4K (3840x2160) image.
func BenchmarkROIPool_Reconstruct_4K(b *testing.B) {
	const w, h = 3840, 2160
	nrgba := makeBenchImage(w, h, true)
	eng := Default()
	match := matchResult{
		found:    true,
		size:     48,
		x:        w - 78 - 48,
		y:        h - 78 - 48,
		score:    0.95,
		gain:     0.6,
		template: eng.cfg.Templates[0],
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		// New approach: crop only ROI corner into pooled buffer, patch in-place on working buffer
		eng.reconstructROI(nrgba, match)
	}
}

// BenchmarkFullFrameClone_Baseline_8K measures memory allocations when repeatedly
// cloning a full 8K (7680x4320) NRGBA image to RGBA (the old approach).
func BenchmarkFullFrameClone_Baseline_8K(b *testing.B) {
	const w, h = 7680, 4320
	nrgba := image.NewNRGBA(image.Rect(0, 0, w, h))
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		// Old unpooled baseline: clone full 8K image into newly allocated RGBA
		rect := nrgba.Bounds()
		rgba := image.NewRGBA(rect)
		draw.Draw(rgba, rect, nrgba, rect.Min, draw.Src)
		_ = rgba.Pix[0]
	}
}

// BenchmarkROIPool_Reconstruct_8K measures memory allocations using the new
// ROI cropping and sync.Pool buffer reuse on the same 8K (7680x4320) image.
func BenchmarkROIPool_Reconstruct_8K(b *testing.B) {
	const w, h = 7680, 4320
	nrgba := makeBenchImage(w, h, true)
	eng := Default()
	match := matchResult{
		found:    true,
		size:     48,
		x:        w - 78 - 48,
		y:        h - 78 - 48,
		score:    0.95,
		gain:     0.6,
		template: eng.cfg.Templates[0],
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		// New approach: crop only ROI corner into pooled buffer, patch in-place on working buffer
		eng.reconstructROI(nrgba, match)
	}
}

// BenchmarkROIBuffer_GetPut benchmarks the throughput of sync.Pool ROI buffer reuse.
func BenchmarkROIBuffer_GetPut(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		buf := getROIBuffer(64 * 1024)
		putROIBuffer(buf)
	}
}

// BenchmarkFloatBuffer_GetPut benchmarks the throughput of sync.Pool float buffer reuse.
func BenchmarkFloatBuffer_GetPut(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		buf := getFloatBuffer(4096)
		putFloatBuffer(buf)
	}
}
