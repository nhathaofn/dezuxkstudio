package watermark

import (
	"bytes"
	"cmp"
	"fmt"
	"image"
	"image/color"
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
	for _, tmpl := range e.cfg.Templates {
		sz := tmpl.Size
		regions := getScanRegions(w, h, tmpl, e.cfg.Corner)
		if len(regions) == 0 {
			continue
		}

		for _, reg := range regions {
			patchW, patchH := w-reg.minX, h-reg.minY
			if patchW <= 0 || patchH <= 0 {
				continue
			}

			luma := make([]float64, patchW*patchH)
			for y := 0; y < patchH; y++ {
				for x := 0; x < patchW; x++ {
					c := color.RGBAModel.Convert(img.At(bounds.Min.X+reg.minX+x, bounds.Min.Y+reg.minY+y)).(color.RGBA)
					luma[y*patchW+x] = 0.299*float64(c.R) + 0.587*float64(c.G) + 0.114*float64(c.B)
				}
			}

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

// blendGain finds how strongly the template was blended into this image.
func (e *Engine) blendGain(img image.Image, match matchResult) (gain float64, ok bool) {
	sz := match.size
	alpha := match.template.Alpha
	residual := func(gain float64) float64 {
		luma := make([]float64, sz*sz)
		for y := 0; y < sz; y++ {
			for x := 0; x < sz; x++ {
				a := math.Min(float64(alpha[y*sz+x])*gain, 0.95)
				c := color.RGBAModel.Convert(img.At(match.x+x, match.y+y)).(color.RGBA)
				v := (float64(c.R) + float64(c.G) + float64(c.B)) / 3.0
				luma[y*sz+x] = (v - a*255.0) / (1.0 - a)
			}
		}
		var sum float64
		for y := 0; y < sz-1; y++ {
			for x := 0; x < sz-1; x++ {
				i := y*sz + x
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

func (e *Engine) coreGap(img image.Image, match matchResult, gain float64) (gap, spread float64, ok bool) {
	sz := match.size
	var peak float32
	for _, a := range match.template.Alpha {
		peak = max(peak, a)
	}
	var core, around, aroundSq, nCore, nAround float64
	for y := 0; y < sz; y++ {
		for x := 0; x < sz; x++ {
			ta := match.template.Alpha[y*sz+x]
			c := color.RGBAModel.Convert(img.At(match.x+x, match.y+y)).(color.RGBA)
			v := 0.299*float64(c.R) + 0.587*float64(c.G) + 0.114*float64(c.B)
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
	}
	if nCore == 0 || nAround == 0 {
		return 0, 0, false
	}
	mean := around / nAround
	return core/nCore - mean, math.Sqrt(math.Max(0, aroundSq/nAround-mean*mean)), true
}

func (e *Engine) refineGainOnFlat(img image.Image, match matchResult, gain float64) float64 {
	gap := func(g float64) float64 {
		v, _, _ := e.coreGap(img, match, g)
		return v
	}
	if _, spread, ok := e.coreGap(img, match, gain); !ok || spread > e.cfg.FlatSpread {
		return gain
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
