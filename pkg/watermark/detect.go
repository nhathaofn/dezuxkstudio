package watermark

import (
	"bytes"
	"cmp"
	"fmt"
	"image"
	_ "image/jpeg"
	"math"
	"slices"

	_ "golang.org/x/image/webp"
)

// getScanRegions returns all rectangular search boundaries based on Corner mode.
func getScanRegions(w, h int, tmpl Template, corner Corner) []scanRegion {
	sz := tmpl.Size
	var regions []scanRegion

	addRegion := func(c Corner) {
		var minX, maxX, minY, maxY int
		switch c {
		case CornerBottomRight:
			minX, maxX = w-tmpl.MaxMargin-sz, w-tmpl.MinMargin-sz
			minY, maxY = h-tmpl.MaxMargin-sz, h-tmpl.MinMargin-sz
		case CornerBottomLeft:
			minX, maxX = tmpl.MinMargin, tmpl.MaxMargin
			minY, maxY = h-tmpl.MaxMargin-sz, h-tmpl.MinMargin-sz
		case CornerTopRight:
			minX, maxX = w-tmpl.MaxMargin-sz, w-tmpl.MinMargin-sz
			minY, maxY = tmpl.MinMargin, tmpl.MaxMargin
		case CornerTopLeft:
			minX, maxX = tmpl.MinMargin, tmpl.MaxMargin
			minY, maxY = tmpl.MinMargin, tmpl.MaxMargin
		default:
			return
		}

		minX = max(0, minX)
		maxX = min(w-sz, maxX)
		minY = max(0, minY)
		maxY = min(h-sz, maxY)

		if minX <= maxX && minY <= maxY {
			regions = append(regions, scanRegion{minX: minX, maxX: maxX, minY: minY, maxY: maxY})
		}
	}

	if corner == CornerAny {
		addRegion(CornerBottomRight)
		addRegion(CornerBottomLeft)
		addRegion(CornerTopRight)
		addRegion(CornerTopLeft)
	} else {
		addRegion(corner)
	}
	return regions
}

// findWatermark scans the image regions using normalized cross-correlation and verifies blend gain.
func (e *Engine) findWatermark(img image.Image) matchResult {
	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()

	best := matchResult{}
	var lumaBuf []float64
	for _, tmpl := range e.cfg.Templates {
		sz := tmpl.Size
		regions := getScanRegions(w, h, tmpl, e.cfg.Corner)
		if len(regions) == 0 {
			continue
		}

		for _, reg := range regions {
			// Only extract the search rectangle touched by (reg.min..reg.max) + sz, avoiding whole-image conversion.
			patchW, patchH := reg.maxX+sz-reg.minX, reg.maxY+sz-reg.minY
			if patchW <= 0 || patchH <= 0 {
				continue
			}

			need := patchW * patchH
			if cap(lumaBuf) < need {
				lumaBuf = make([]float64, need)
			}
			luma := lumaBuf[:need]
			extractLumaRegion(img, bounds.Min.X+reg.minX, bounds.Min.Y+reg.minY, patchW, patchH, luma, true)

			n := float64(sz * sz)
			var candidates []matchResult
			for y := reg.minY; y <= reg.maxY; y++ {
				for x := reg.minX; x <= reg.maxX; x++ {
					var sum, sumSq, prod float64
					for dy := 0; dy < sz; dy++ {
						row := luma[(y-reg.minY+dy)*patchW+x-reg.minX:]
						ta := tmpl.Alpha[dy*sz:]
						for dx := 0; dx < sz; dx++ {
							v := row[dx]
							sum += v
							sumSq += v * v
							prod += v * float64(ta[dx])
						}
					}
					den := sumSq - sum*sum/n
					if den <= 0 {
						continue
					}
					c := (prod - sum/n*tmpl.SumAlpha) / (math.Sqrt(den) * tmpl.DenAlpha)
					if c >= e.cfg.MinScore {
						candidates = append(candidates, matchResult{
							size:     sz,
							x:        bounds.Min.X + x,
							y:        bounds.Min.Y + y,
							score:    c,
							template: tmpl,
						})
					}
				}
			}

			slices.SortFunc(candidates, func(a, b matchResult) int { return cmp.Compare(b.score, a.score) })
			for _, c := range candidates[:min(len(candidates), e.cfg.MaxCandidates)] {
				if c.score <= best.score {
					break
				}
				if gain, ok := e.blendGain(img, c); ok && e.fitsSurroundings(img, c, gain) {
					c.gain, c.found = e.refineGainOnFlat(img, c, gain), true
					best = c
					break
				}
			}
		}
	}
	return best
}

// extractLumaRegion fills dst (len >= w*h) with pixel luminance values for the rectangle [x0..x0+w, y0..y0+h)
// using direct Pix / concrete image accessors to avoid per-pixel interface boxing.
func extractLumaRegion(img image.Image, x0, y0, w, h int, dst []float64, weighted bool) {
	lumaVal := func(r, g, b uint8) float64 {
		if weighted {
			return 0.299*float64(r) + 0.587*float64(g) + 0.114*float64(b)
		}
		return (float64(r) + float64(g) + float64(b)) / 3.0
	}

	switch src := img.(type) {
	case *image.RGBA:
		for y := 0; y < h; y++ {
			off := src.PixOffset(x0, y0+y)
			row := src.Pix[off : off+w*4 : off+w*4]
			out := dst[y*w : (y+1)*w]
			for x := 0; x < w; x++ {
				p := row[x*4 : x*4+4 : x*4+4]
				out[x] = lumaVal(p[0], p[1], p[2])
			}
		}
	case *image.NRGBA:
		for y := 0; y < h; y++ {
			off := src.PixOffset(x0, y0+y)
			row := src.Pix[off : off+w*4 : off+w*4]
			out := dst[y*w : (y+1)*w]
			for x := 0; x < w; x++ {
				p := row[x*4 : x*4+4 : x*4+4]
				if p[3] == 255 {
					out[x] = lumaVal(p[0], p[1], p[2])
					continue
				}
				rr, gg, bb, _ := src.NRGBAAt(x0+x, y0+y).RGBA()
				out[x] = lumaVal(uint8(rr>>8), uint8(gg>>8), uint8(bb>>8))
			}
		}
	case *image.YCbCr:
		for y := 0; y < h; y++ {
			out := dst[y*w : (y+1)*w]
			for x := 0; x < w; x++ {
				rr, gg, bb, _ := src.YCbCrAt(x0+x, y0+y).RGBA()
				out[x] = lumaVal(uint8(rr>>8), uint8(gg>>8), uint8(bb>>8))
			}
		}
	default:
		for y := 0; y < h; y++ {
			out := dst[y*w : (y+1)*w]
			for x := 0; x < w; x++ {
				rr, gg, bb, _ := img.At(x0+x, y0+y).RGBA()
				out[x] = lumaVal(uint8(rr>>8), uint8(gg>>8), uint8(bb>>8))
			}
		}
	}
}

// blendGain finds how strongly the template was blended into this image.
func (e *Engine) blendGain(img image.Image, match matchResult) (gain float64, ok bool) {
	sz := match.size
	alpha := match.template.Alpha
	baseV := make([]float64, sz*sz)
	extractLumaRegion(img, match.x, match.y, sz, sz, baseV, false)
	luma := make([]float64, sz*sz)

	residual := func(gain float64) float64 {
		for i, v := range baseV {
			a := math.Min(float64(alpha[i])*gain, 0.95)
			luma[i] = (v - a*255.0) / (1.0 - a)
		}
		var sum float64
		for y := 0; y < sz-1; y++ {
			row := y * sz
			for x := 0; x < sz-1; x++ {
				i := row + x
				sum += (luma[i+1]-luma[i])*float64(alpha[i+1]-alpha[i]) + (luma[i+sz]-luma[i])*float64(alpha[i+sz]-alpha[i])
			}
		}
		return sum
	}

	lo, hi := e.cfg.MinGain, e.cfg.MaxGain
	if residual(lo) <= 0 || residual(hi) >= 0 {
		return 0, false
	}
	for range 24 {
		mid := (lo + hi) / 2
		if residual(mid) > 0 {
			lo = mid
		} else {
			hi = mid
		}
	}
	return (lo + hi) / 2, true
}

func (e *Engine) fitsSurroundings(img image.Image, match matchResult, gain float64) bool {
	gap, _, ok := e.coreGap(img, match, gain)
	return ok && math.Abs(gap) <= e.cfg.MaxCoreGap
}

func extractCoreGapData(img image.Image, match matchResult) (luma []float64, peak float32) {
	sz := match.size
	luma = make([]float64, sz*sz)
	extractLumaRegion(img, match.x, match.y, sz, sz, luma, true)
	for _, a := range match.template.Alpha {
		peak = max(peak, a)
	}
	return luma, peak
}

func coreGapFromLuma(luma []float64, alpha []float32, peak float32, gain float64) (gap, spread float64, ok bool) {
	var core, around, aroundSq, nCore, nAround float64
	for i, ta := range alpha {
		v := luma[i]
		switch {
		case ta >= 0.8*peak:
			a := math.Min(float64(ta)*gain, 0.95)
			core += (v - a*255.0) / (1.0 - a)
			nCore++
		case ta < 0.01:
			around += v
			aroundSq += v * v
			nAround++
		}
	}
	if nCore == 0 || nAround == 0 {
		return 0, 0, false
	}
	mean := around / nAround
	return core/nCore - mean, math.Sqrt(math.Max(0, aroundSq/nAround-mean*mean)), true
}

func (e *Engine) coreGap(img image.Image, match matchResult, gain float64) (gap, spread float64, ok bool) {
	luma, peak := extractCoreGapData(img, match)
	return coreGapFromLuma(luma, match.template.Alpha, peak, gain)
}

func (e *Engine) refineGainOnFlat(img image.Image, match matchResult, gain float64) float64 {
	luma, peak := extractCoreGapData(img, match)
	alpha := match.template.Alpha
	if _, spread, ok := coreGapFromLuma(luma, alpha, peak, gain); !ok || spread > e.cfg.FlatSpread {
		return gain
	}
	gap := func(g float64) float64 {
		v, _, _ := coreGapFromLuma(luma, alpha, peak, g)
		return v
	}
	lo, hi := e.cfg.MinGain, e.cfg.MaxGain
	if gap(lo) <= 0 || gap(hi) >= 0 {
		return gain
	}
	for range 24 {
		mid := (lo + hi) / 2
		if gap(mid) > 0 {
			lo = mid
		} else {
			hi = mid
		}
	}
	return (lo + hi) / 2
}

// Detect checks if image data contains a watermark matching the engine rules.
func (e *Engine) Detect(data []byte) (present bool, score float64, info Info, err error) {
	if len(data) == 0 {
		return false, 0, Info{}, fmt.Errorf("watermark: empty image data")
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return false, 0, Info{}, fmt.Errorf("watermark: decode image: %w", err)
	}

	match := e.findWatermark(img)
	if !match.found {
		return false, match.score, Info{}, nil
	}
	return true, match.score, Info{
		Size:     match.size,
		Position: image.Rect(match.x, match.y, match.x+match.size, match.y+match.size),
	}, nil
}

// Detect checks if the image contains a watermark using the default Google Flow engine.
func Detect(data []byte) (present bool, score float64, info Info, err error) {
	return Default().Detect(data)
}
