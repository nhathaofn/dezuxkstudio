package watermark

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/draw"
	"image/jpeg"
	"image/png"
	"math"
	"slices"
	"sync"

	_ "golang.org/x/image/webp"
)

var roiPool = sync.Pool{
	New: func() any {
		b := make([]byte, 256*256*4)
		return &b
	},
}

func getROIBuffer(minBytes int) *[]byte {
	buf := roiPool.Get().(*[]byte)
	if len(*buf) < minBytes {
		*buf = make([]byte, minBytes)
	}
	return buf
}

func putROIBuffer(buf *[]byte) {
	if buf == nil {
		return
	}
	roiPool.Put(buf)
}

var fullFramePool = sync.Pool{
	New: func() any {
		b := make([]byte, 0)
		return &b
	},
}

func acquireFullFrameRGBA(rect image.Rectangle) (*image.RGBA, func()) {
	need := rect.Dx() * rect.Dy() * 4
	bufPtr := fullFramePool.Get().(*[]byte)
	if cap(*bufPtr) < need {
		*bufPtr = make([]byte, need)
	} else {
		*bufPtr = (*bufPtr)[:need]
	}
	rgba := &image.RGBA{
		Pix:    *bufPtr,
		Stride: rect.Dx() * 4,
		Rect:   rect,
	}
	return rgba, func() {
		if cap(*bufPtr) <= 150*1024*1024 {
			fullFramePool.Put(bufPtr)
		}
	}
}

// writePatch writes the reconstructed patch from roi to workingImg at patchRect.
func writePatch(dst image.Image, roi *image.RGBA, patchRect image.Rectangle) {
	switch d := dst.(type) {
	case *image.RGBA:
		for y := patchRect.Min.Y; y < patchRect.Max.Y; y++ {
			dstOff := d.PixOffset(patchRect.Min.X, y)
			srcOff := roi.PixOffset(patchRect.Min.X, y)
			copy(d.Pix[dstOff:dstOff+patchRect.Dx()*4], roi.Pix[srcOff:srcOff+patchRect.Dx()*4])
		}
	case *image.NRGBA:
		for y := patchRect.Min.Y; y < patchRect.Max.Y; y++ {
			dstOff := d.PixOffset(patchRect.Min.X, y)
			srcOff := roi.PixOffset(patchRect.Min.X, y)
			copy(d.Pix[dstOff:dstOff+patchRect.Dx()*4], roi.Pix[srcOff:srcOff+patchRect.Dx()*4])
		}
	case draw.Image:
		draw.Draw(d, patchRect, roi, patchRect.Min, draw.Src)
	}
}

// reconstructROI crops only the watermark ROI corner into a pooled buffer, executes
// unblending, contour smoothing, and bilateral denoising within that ROI, and writes
// the patch directly back onto workingImg. It avoids full-image cloning.
func (e *Engine) reconstructROI(workingImg image.Image, match matchResult) {
	bounds := workingImg.Bounds()
	sz := match.size
	wmX, wmY := match.x, match.y
	if wmX < bounds.Min.X || wmY < bounds.Min.Y || wmX+sz > bounds.Max.X || wmY+sz > bounds.Max.Y {
		return
	}

	rad := e.cfg.DenoiseRadius
	pad := rad + 2
	roi := image.Rect(wmX-pad, wmY-pad, wmX+sz+pad, wmY+sz+pad).Intersect(bounds)
	roiBytes := roi.Dx() * roi.Dy() * 4

	roiBuf := getROIBuffer(roiBytes)
	defer putROIBuffer(roiBuf)
	roiRGBA := &image.RGBA{
		Pix:    (*roiBuf)[:roiBytes],
		Stride: roi.Dx() * 4,
		Rect:   roi,
	}
	draw.Draw(roiRGBA, roi, workingImg, roi.Min, draw.Src)

	alpha := match.template.Alpha
	gain := match.gain

	// Step 1: Unblend watermark pixels in ROI
	for y := 0; y < sz; y++ {
		for x := 0; x < sz; x++ {
			a := float64(alpha[y*sz+x]) * gain
			if a < 0.002 {
				continue
			}
			if a > 0.95 {
				a = 0.95
			}
			off := roiRGBA.PixOffset(wmX+x, wmY+y)
			p := roiRGBA.Pix[off : off+4 : off+4]
			p[0] = unblend(p[0], a)
			p[1] = unblend(p[1], a)
			p[2] = unblend(p[2], a)
			p[3] = 255
		}
	}

	// Step 2: Smooth contour inside ROI
	e.smoothContourROI(roiRGBA, match)

	// Step 3: Denoise inside ROI
	e.denoiseInsideROI(roiRGBA, match, gain)

	// Step 4: Write reconstruction patch directly onto working buffer
	patchRect := image.Rect(wmX, wmY, wmX+sz, wmY+sz).Intersect(bounds)
	writePatch(workingImg, roiRGBA, patchRect)
}

func (e *Engine) reconstructWatermark(rgba *image.RGBA, match matchResult) {
	e.reconstructROI(rgba, match)
}

func (e *Engine) smoothContourROI(roiRGBA *image.RGBA, match matchResult) {
	sz := match.size
	alpha := match.template.Alpha
	at := func(x, y int) float64 {
		if x < 0 || y < 0 || x >= sz || y >= sz {
			return 0
		}
		return float64(alpha[y*sz+x])
	}

	roi := roiRGBA.Rect
	srcBuf := getROIBuffer(roi.Dx() * roi.Dy() * 4)
	defer putROIBuffer(srcBuf)
	src := &image.RGBA{
		Pix:    (*srcBuf)[:roi.Dx()*roi.Dy()*4],
		Stride: roi.Dx() * 4,
		Rect:   roi,
	}
	copy(src.Pix, roiRGBA.Pix)

	const pad = 2
	var r, g, b [25]uint8
	for y := 0; y < sz; y++ {
		for x := 0; x < sz; x++ {
			gx, gy := at(x+1, y)-at(x-1, y), at(x, y+1)-at(x, y-1)
			if gx*gx+gy*gy < 0.004 {
				continue
			}
			n := 0
			for dy := -pad; dy <= pad; dy++ {
				for dx := -pad; dx <= pad; dx++ {
					pt := image.Pt(match.x+x+dx, match.y+y+dy)
					if !pt.In(roi) {
						continue
					}
					off := src.PixOffset(pt.X, pt.Y)
					p := src.Pix[off : off+4 : off+4]
					r[n], g[n], b[n] = p[0], p[1], p[2]
					n++
				}
			}
			if n > 0 {
				off := roiRGBA.PixOffset(match.x+x, match.y+y)
				p := roiRGBA.Pix[off : off+4 : off+4]
				p[0] = medianSlice(r[:n])
				p[1] = medianSlice(g[:n])
				p[2] = medianSlice(b[:n])
				p[3] = 255
			}
		}
	}
}

func (e *Engine) smoothContour(rgba *image.RGBA, match matchResult) {
	e.smoothContourROI(rgba, match)
}

func (e *Engine) denoiseInsideROI(roiRGBA *image.RGBA, match matchResult, gain float64) {
	sz := match.size
	rad := e.cfg.DenoiseRadius
	roi := roiRGBA.Rect

	srcBuf := getROIBuffer(roi.Dx() * roi.Dy() * 4)
	defer putROIBuffer(srcBuf)
	src := &image.RGBA{
		Pix:    (*srcBuf)[:roi.Dx()*roi.Dy()*4],
		Stride: roi.Dx() * 4,
		Rect:   roi,
	}
	copy(src.Pix, roiRGBA.Pix)

	spaceSigma := e.cfg.SpaceSigma
	rangeSigma := e.cfg.RangeSigma
	denoiseScale := e.cfg.DenoiseScale
	invSpaceDen := 1.0 / (2 * spaceSigma * spaceSigma)
	invRangeDen := 1.0 / (3 * 2 * rangeSigma * rangeSigma)

	side := 2*rad + 1
	var spaceTermsBuf [121]float64
	var spaceTerms []float64
	if side*side <= len(spaceTermsBuf) {
		spaceTerms = spaceTermsBuf[:side*side]
	} else {
		fBuf := getFloatBuffer(side * side)
		defer putFloatBuffer(fBuf)
		spaceTerms = *fBuf
	}

	for dy := -rad; dy <= rad; dy++ {
		for dx := -rad; dx <= rad; dx++ {
			spaceTerms[(dy+rad)*side+(dx+rad)] = -float64(dx*dx+dy*dy) * invSpaceDen
		}
	}

	for y := 0; y < sz; y++ {
		for x := 0; x < sz; x++ {
			a := math.Min(float64(match.template.Alpha[y*sz+x])*gain, 0.95)
			w := math.Min(1, a/(1.0-a)*denoiseScale)
			if w < 0.02 {
				continue
			}
			px, py := match.x+x, match.y+y
			cOff := src.PixOffset(px, py)
			cp := src.Pix[cOff : cOff+4 : cOff+4]
			cR, cG, cB := float64(cp[0]), float64(cp[1]), float64(cp[2])
			var sr, sg, sb, sw float64
			for dy := -rad; dy <= rad; dy++ {
				rowIdx := (dy + rad) * side
				qy := py + dy
				if qy < roi.Min.Y || qy >= roi.Max.Y {
					continue
				}
				for dx := -rad; dx <= rad; dx++ {
					qx := px + dx
					if qx < roi.Min.X || qx >= roi.Max.X {
						continue
					}
					nOff := src.PixOffset(qx, qy)
					np := src.Pix[nOff : nOff+4 : nOff+4]
					nR, nG, nB := float64(np[0]), float64(np[1]), float64(np[2])
					dr, dg, db := nR-cR, nG-cG, nB-cB
					k := math.Exp(spaceTerms[rowIdx+(dx+rad)] - (dr*dr+dg*dg+db*db)*invRangeDen)
					sr += k * nR
					sg += k * nG
					sb += k * nB
					sw += k
				}
			}
			dOff := roiRGBA.PixOffset(px, py)
			dp := roiRGBA.Pix[dOff : dOff+4 : dOff+4]
			dp[0] = clamp8((1-w)*cR + w*sr/sw)
			dp[1] = clamp8((1-w)*cG + w*sg/sw)
			dp[2] = clamp8((1-w)*cB + w*sb/sw)
			dp[3] = 255
		}
	}
}

func (e *Engine) denoiseInside(rgba *image.RGBA, match matchResult, gain float64) {
	e.denoiseInsideROI(rgba, match, gain)
}

func unblend(c uint8, a float64) uint8 {
	return clamp8((float64(c) - a*255.0) / (1.0 - a))
}

func clamp8(v float64) uint8 {
	return uint8(math.Round(math.Max(0, math.Min(255, v))))
}

func medianSlice(v []uint8) uint8 {
	if len(v) == 0 {
		return 0
	}
	slices.Sort(v)
	return v[len(v)/2]
}

func median(v [25]uint8) uint8 {
	return medianSlice(v[:])
}

// SniffFormat inspects magic bytes to identify JPEG, PNG, or WebP format.
func SniffFormat(data []byte) ImageFormat {
	if len(data) >= 3 && data[0] == 0xFF && data[1] == 0xD8 && data[2] == 0xFF {
		return FormatJPEG
	}
	if len(data) >= 8 && bytes.Equal(data[:8], []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}) {
		return FormatPNG
	}
	if len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP" {
		return FormatWebP
	}
	return FormatUnknown
}

func encodeCleaned(img image.Image, inFormat ImageFormat) ([]byte, ImageFormat, error) {
	var buf bytes.Buffer
	switch inFormat {
	case FormatJPEG:
		if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 95}); err != nil {
			return nil, FormatJPEG, fmt.Errorf("watermark: encode cleaned jpeg: %w", err)
		}
		return buf.Bytes(), FormatJPEG, nil
	case FormatWebP:
		// Go stdlib and x/image have no pure-Go WebP encoder without CGo.
		// Export cleanly to standard PNG (zero-cgo pure-Go).
		if err := png.Encode(&buf, img); err != nil {
			return nil, FormatPNG, fmt.Errorf("watermark: export cleaned webp to png: %w", err)
		}
		return buf.Bytes(), FormatPNG, nil
	case FormatPNG, FormatUnknown:
		fallthrough
	default:
		if err := png.Encode(&buf, img); err != nil {
			return nil, FormatPNG, fmt.Errorf("watermark: encode cleaned png: %w", err)
		}
		return buf.Bytes(), FormatPNG, nil
	}
}

// RemoveContext detects and removes the watermark, supporting context cancellation.
func (e *Engine) RemoveContext(ctx context.Context, data []byte) (output []byte, removed bool, err error) {
	out, rem, _, _, _, _, err := e.removeInternal(ctx, data)
	return out, rem, err
}

// Remove detects and removes the watermark, preserving format for JPEG and PNG, and cleanly exporting WebP to PNG.
func (e *Engine) Remove(data []byte) (output []byte, removed bool, err error) {
	return e.RemoveContext(context.Background(), data)
}

// RemoveWithFormatContext detects and removes the watermark with context, returning output bytes and format.
func (e *Engine) RemoveWithFormatContext(ctx context.Context, data []byte) (output []byte, removed bool, format ImageFormat, err error) {
	out, rem, outFmt, _, _, _, err := e.removeInternal(ctx, data)
	return out, rem, outFmt, err
}

// RemoveWithFormat detects and removes the watermark, returning the output bytes and output format.
func (e *Engine) RemoveWithFormat(data []byte) (output []byte, removed bool, format ImageFormat, err error) {
	return e.RemoveWithFormatContext(context.Background(), data)
}

// RemoveImage removes the watermark from an in-memory image.Image.
func (e *Engine) RemoveImage(img image.Image) (*image.RGBA, bool) {
	match := e.findWatermark(img)
	if !match.found {
		return nil, false
	}

	b := img.Bounds()
	rgba := image.NewRGBA(b)
	draw.Draw(rgba, b, img, b.Min, draw.Src)

	e.reconstructWatermark(rgba, match)
	return rgba, true
}

func (e *Engine) removeInternal(ctx context.Context, data []byte) ([]byte, bool, ImageFormat, Info, float64, float64, error) {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return nil, false, FormatUnknown, Info{}, 0, 0, err
		}
	}
	img, inFormat, err := e.decodeImage(ctx, data)
	if err != nil {
		return nil, false, inFormat, Info{}, 0, 0, err
	}
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return nil, false, inFormat, Info{}, 0, 0, err
		}
	}

	match := e.findWatermarkContext(ctx, img)
	if !match.found {
		return data, false, inFormat, Info{}, match.score, match.gain, nil
	}
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return nil, false, inFormat, Info{}, 0, 0, err
		}
	}

	var workingImg image.Image = img
	var releaseFullFrame func()
	switch img.(type) {
	case *image.RGBA, *image.NRGBA:
		// Working buffer is the decoded image itself - zero full-frame allocations!
	default:
		// Format like YCbCr requires RGBA conversion; use pooled frame buffer
		rgba, rel := acquireFullFrameRGBA(img.Bounds())
		releaseFullFrame = rel
		draw.Draw(rgba, img.Bounds(), img, img.Bounds().Min, draw.Src)
		workingImg = rgba
	}
	if releaseFullFrame != nil {
		defer releaseFullFrame()
	}

	e.reconstructROI(workingImg, match)

	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return nil, false, inFormat, Info{}, 0, 0, err
		}
	}

	encodedBytes, outFormat, err := encodeCleaned(workingImg, inFormat)
	if err != nil {
		return nil, false, inFormat, Info{}, 0, 0, err
	}
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return nil, false, inFormat, Info{}, 0, 0, err
		}
	}

	info := Info{
		Size:     match.size,
		Position: image.Rect(match.x, match.y, match.x+match.size, match.y+match.size),
	}
	return encodedBytes, true, outFormat, info, match.score, match.gain, nil
}

// RemoveContext locates and removes the watermark with context using the default Google Flow engine.
func RemoveContext(ctx context.Context, data []byte) (output []byte, removed bool, err error) {
	return Default().RemoveContext(ctx, data)
}

// Remove locates and removes the watermark using the default Google Flow engine.
func Remove(data []byte) (output []byte, removed bool, err error) {
	return Default().Remove(data)
}

// RemoveWithFormatContext locates and removes the watermark with context using default settings.
func RemoveWithFormatContext(ctx context.Context, data []byte) (output []byte, removed bool, format ImageFormat, err error) {
	return Default().RemoveWithFormatContext(ctx, data)
}

// RemoveWithFormat locates and removes the watermark using default settings, returning output format.
func RemoveWithFormat(data []byte) (output []byte, removed bool, format ImageFormat, err error) {
	return Default().RemoveWithFormat(data)
}

// RemoveImage removes the watermark from an in-memory image.Image using default settings.
func RemoveImage(img image.Image) (*image.RGBA, bool) {
	return Default().RemoveImage(img)
}
