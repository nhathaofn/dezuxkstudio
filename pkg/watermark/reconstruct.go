package watermark

import (
	"bytes"
	"fmt"
	"image"
	"image/draw"
	"image/jpeg"
	"image/png"
	"math"
	"slices"

	_ "golang.org/x/image/webp"
)

func (e *Engine) reconstructWatermark(rgba *image.RGBA, match matchResult) {
	bounds := rgba.Bounds()
	sz := match.size
	wmX, wmY := match.x, match.y
	if wmX < bounds.Min.X || wmY < bounds.Min.Y || wmX+sz > bounds.Max.X || wmY+sz > bounds.Max.Y {
		return
	}
	alpha := match.template.Alpha
	gain := match.gain

	for y := 0; y < sz; y++ {
		for x := 0; x < sz; x++ {
			a := float64(alpha[y*sz+x]) * gain
			if a < 0.002 {
				continue
			}
			if a > 0.95 {
				a = 0.95
			}
			off := rgba.PixOffset(wmX+x, wmY+y)
			p := rgba.Pix[off : off+4 : off+4]
			p[0] = unblend(p[0], a)
			p[1] = unblend(p[1], a)
			p[2] = unblend(p[2], a)
			p[3] = 255
		}
	}
	e.smoothContour(rgba, match)
	e.denoiseInside(rgba, match, gain)
}

func (e *Engine) smoothContour(rgba *image.RGBA, match matchResult) {
	sz := match.size
	alpha := match.template.Alpha
	at := func(x, y int) float64 {
		if x < 0 || y < 0 || x >= sz || y >= sz {
			return 0
		}
		return float64(alpha[y*sz+x])
	}
	bounds := rgba.Bounds()
	// Copy only the corner ROI + 2px neighborhood instead of cloning the full image.
	const pad = 2
	roi := image.Rect(match.x-pad, match.y-pad, match.x+sz+pad, match.y+sz+pad).Intersect(bounds)
	src := image.NewRGBA(roi)
	draw.Draw(src, roi, rgba, roi.Min, draw.Src)
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
				off := rgba.PixOffset(match.x+x, match.y+y)
				p := rgba.Pix[off : off+4 : off+4]
				p[0] = medianSlice(r[:n])
				p[1] = medianSlice(g[:n])
				p[2] = medianSlice(b[:n])
				p[3] = 255
			}
		}
	}
}

func (e *Engine) denoiseInside(rgba *image.RGBA, match matchResult, gain float64) {
	sz := match.size
	b := rgba.Bounds()
	rad := e.cfg.DenoiseRadius
	// Copy only the corner ROI + DenoiseRadius neighborhood instead of cloning the full image.
	roi := image.Rect(match.x-rad, match.y-rad, match.x+sz+rad, match.y+sz+rad).Intersect(b)
	src := image.NewRGBA(roi)
	draw.Draw(src, roi, rgba, roi.Min, draw.Src)

	spaceSigma := e.cfg.SpaceSigma
	rangeSigma := e.cfg.RangeSigma
	denoiseScale := e.cfg.DenoiseScale
	invSpaceDen := 1.0 / (2 * spaceSigma * spaceSigma)
	invRangeDen := 1.0 / (3 * 2 * rangeSigma * rangeSigma)

	// Precompute spatial term -(dx*dx + dy*dy)/(2*spaceSigma^2) for the bilateral window.
	side := 2*rad + 1
	spaceTerms := make([]float64, side*side)
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
			dOff := rgba.PixOffset(px, py)
			dp := rgba.Pix[dOff : dOff+4 : dOff+4]
			dp[0] = clamp8((1-w)*cR + w*sr/sw)
			dp[1] = clamp8((1-w)*cG + w*sg/sw)
			dp[2] = clamp8((1-w)*cB + w*sb/sw)
			dp[3] = 255
		}
	}
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

func encodeCleaned(rgba *image.RGBA, inFormat ImageFormat) ([]byte, ImageFormat, error) {
	var buf bytes.Buffer
	switch inFormat {
	case FormatJPEG:
		if err := jpeg.Encode(&buf, rgba, &jpeg.Options{Quality: 95}); err != nil {
			return nil, FormatJPEG, fmt.Errorf("watermark: encode cleaned jpeg: %w", err)
		}
		return buf.Bytes(), FormatJPEG, nil
	case FormatWebP:
		// Go stdlib and x/image have no pure-Go WebP encoder without CGo.
		// Export cleanly to standard PNG (zero-cgo pure-Go).
		if err := png.Encode(&buf, rgba); err != nil {
			return nil, FormatPNG, fmt.Errorf("watermark: export cleaned webp to png: %w", err)
		}
		return buf.Bytes(), FormatPNG, nil
	case FormatPNG, FormatUnknown:
		fallthrough
	default:
		if err := png.Encode(&buf, rgba); err != nil {
			return nil, FormatPNG, fmt.Errorf("watermark: encode cleaned png: %w", err)
		}
		return buf.Bytes(), FormatPNG, nil
	}
}

// Remove detects and removes the watermark, preserving format for JPEG and PNG, and cleanly exporting WebP to PNG.
func (e *Engine) Remove(data []byte) (output []byte, removed bool, err error) {
	out, rem, _, _, _, _, err := e.removeInternal(data)
	return out, rem, err
}

// RemoveWithFormat detects and removes the watermark, returning the output bytes and output format.
func (e *Engine) RemoveWithFormat(data []byte) (output []byte, removed bool, format ImageFormat, err error) {
	out, rem, outFmt, _, _, _, err := e.removeInternal(data)
	return out, rem, outFmt, err
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

func (e *Engine) removeInternal(data []byte) ([]byte, bool, ImageFormat, Info, float64, float64, error) {
	if len(data) == 0 {
		return nil, false, FormatUnknown, Info{}, 0, 0, fmt.Errorf("watermark: empty image data")
	}
	inFormat := SniffFormat(data)
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, false, inFormat, Info{}, 0, 0, fmt.Errorf("watermark: decode image: %w", err)
	}

	match := e.findWatermark(img)
	if !match.found {
		return data, false, inFormat, Info{}, match.score, match.gain, nil
	}

	b := img.Bounds()
	rgba, ok := img.(*image.RGBA)
	if !ok {
		rgba = image.NewRGBA(b)
		draw.Draw(rgba, b, img, b.Min, draw.Src)
	}
	e.reconstructWatermark(rgba, match)

	encodedBytes, outFormat, err := encodeCleaned(rgba, inFormat)
	if err != nil {
		return nil, false, inFormat, Info{}, 0, 0, err
	}

	info := Info{
		Size:     match.size,
		Position: image.Rect(match.x, match.y, match.x+match.size, match.y+match.size),
	}
	return encodedBytes, true, outFormat, info, match.score, match.gain, nil
}

// Remove locates and removes the watermark using the default Google Flow engine.
func Remove(data []byte) (output []byte, removed bool, err error) {
	return Default().Remove(data)
}

// RemoveWithFormat locates and removes the watermark using default settings, returning output format.
func RemoveWithFormat(data []byte) (output []byte, removed bool, format ImageFormat, err error) {
	return Default().RemoveWithFormat(data)
}

// RemoveImage removes the watermark from an in-memory image.Image using default settings.
func RemoveImage(img image.Image) (*image.RGBA, bool) {
	return Default().RemoveImage(img)
}
