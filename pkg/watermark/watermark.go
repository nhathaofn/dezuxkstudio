// Package watermark provides dynamic multi-scale watermark detection and
// reconstruction for Google Flow / Imagen / Gemini and custom watermark templates.
// It is fully thread-safe, zero-hardcode, and supports parallel batch processing.
package watermark

import (
	"bytes"
	"cmp"
	"context"
	_ "embed"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	_ "image/jpeg"
	"image/png"
	"math"
	"runtime"
	"slices"
	"sync"

	_ "golang.org/x/image/webp"
)

//go:embed assets/bg_48.png
var bg48Data []byte

//go:embed assets/bg_96.png
var bg96Data []byte

// Corner specifies which corner(s) to scan for watermarks.
type Corner string

const (
	CornerBottomRight Corner = "bottom-right"
	CornerBottomLeft  Corner = "bottom-left"
	CornerTopRight    Corner = "top-right"
	CornerTopLeft     Corner = "top-left"
	CornerAny         Corner = "any"
)

// Info holds details about the detected watermark location and size.
type Info struct {
	Size     int             `json:"size"`
	Position image.Rectangle `json:"position"`
}

// Template holds the alpha map and statistical invariants of a watermark mark.
type Template struct {
	Size      int       `json:"size"`
	Alpha     []float32 `json:"alpha"`
	SumAlpha  float64   `json:"sum_alpha"`
	DenAlpha  float64   `json:"den_alpha"`
	MinMargin int       `json:"min_margin"`
	MaxMargin int       `json:"max_margin"`
}

// NewTemplate extracts statistical alpha metrics from an image.Image.
func NewTemplate(img image.Image, minMargin, maxMargin int) Template {
	b := img.Bounds()
	sz := b.Dx()
	alpha := make([]float32, sz*sz)
	var sum float64
	for y := 0; y < sz; y++ {
		for x := 0; x < sz; x++ {
			c := color.RGBAModel.Convert(img.At(b.Min.X+x, b.Min.Y+y)).(color.RGBA)
			a := float32(max(c.R, max(c.G, c.B))) / 255.0
			alpha[y*sz+x] = a
			sum += float64(a)
		}
	}
	mean := sum / float64(len(alpha))
	var sumSq float64
	for _, a := range alpha {
		d := float64(a) - mean
		sumSq += d * d
	}
	return Template{
		Size:      sz,
		Alpha:     alpha,
		SumAlpha:  sum,
		DenAlpha:  math.Sqrt(sumSq),
		MinMargin: minMargin,
		MaxMargin: maxMargin,
	}
}

// NewTemplateFromBytes decodes PNG image bytes into a Template.
func NewTemplateFromBytes(data []byte, minMargin, maxMargin int) (Template, error) {
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return Template{}, fmt.Errorf("watermark: decode template bytes: %w", err)
	}
	return NewTemplate(img, minMargin, maxMargin), nil
}

var (
	defaultTemplatesOnce sync.Once
	defaultTemplatesList []Template
)

// DefaultTemplates returns the calibrated Google Flow / Imagen 48px and 96px templates.
func DefaultTemplates() []Template {
	defaultTemplatesOnce.Do(func() {
		for _, src := range []struct {
			data     []byte
			min, max int
		}{{bg48Data, 56, 104}, {bg96Data, 112, 208}} {
			tmpl, err := NewTemplateFromBytes(src.data, src.min, src.max)
			if err != nil {
				panic(fmt.Sprintf("watermark: embedded template decode failed: %v", err))
			}
			defaultTemplatesList = append(defaultTemplatesList, tmpl)
		}
	})
	return defaultTemplatesList
}

// Config controls watermark detection, reconstruction, and concurrency settings.
type Config struct {
	Corner        Corner     `json:"corner,omitempty"`
	MinScore      float64    `json:"min_score,omitempty"`
	MaxCandidates int        `json:"max_candidates,omitempty"`
	MinGain       float64    `json:"min_gain,omitempty"`
	MaxGain       float64    `json:"max_gain,omitempty"`
	MaxCoreGap    float64    `json:"max_core_gap,omitempty"`
	FlatSpread    float64    `json:"flat_spread,omitempty"`
	DenoiseRadius int        `json:"denoise_radius,omitempty"`
	SpaceSigma    float64    `json:"space_sigma,omitempty"`
	RangeSigma    float64    `json:"range_sigma,omitempty"`
	DenoiseScale  float64    `json:"denoise_scale,omitempty"`
	Workers       int        `json:"workers,omitempty"`
	Templates     []Template `json:"-"`
}

func (c *Config) applyDefaults() {
	if c.Corner == "" {
		c.Corner = CornerBottomRight
	}
	if c.MinScore <= 0 {
		c.MinScore = 0.2
	}
	if c.MaxCandidates <= 0 {
		c.MaxCandidates = 256
	}
	if c.MinGain <= 0 {
		c.MinGain = 0.4
	}
	if c.MaxGain <= 0 {
		c.MaxGain = 0.9
	}
	if c.MaxCoreGap <= 0 {
		c.MaxCoreGap = 45.0
	}
	if c.FlatSpread <= 0 {
		c.FlatSpread = 8.0
	}
	if c.DenoiseRadius <= 0 {
		c.DenoiseRadius = 3
	}
	if c.SpaceSigma <= 0 {
		c.SpaceSigma = 1.5
	}
	if c.RangeSigma <= 0 {
		c.RangeSigma = 14.0
	}
	if c.DenoiseScale <= 0 {
		c.DenoiseScale = 4.0
	}
	if c.Workers <= 0 {
		c.Workers = runtime.NumCPU()
	}
	if len(c.Templates) == 0 {
		c.Templates = DefaultTemplates()
	}
}

// Engine encapsulates detection and removal execution with configurable rules.
type Engine struct {
	cfg Config
}

// New creates an Engine instance with custom zero-hardcode configuration.
func New(cfg Config) *Engine {
	cfg.applyDefaults()
	return &Engine{cfg: cfg}
}

var (
	defaultEngineOnce sync.Once
	defaultEngineInst *Engine
)

// Default returns the shared default Engine configured for Google Flow.
func Default() *Engine {
	defaultEngineOnce.Do(func() {
		defaultEngineInst = New(Config{})
	})
	return defaultEngineInst
}

// BatchItem represents a single unit of work in a multi-threaded batch.
type BatchItem struct {
	ID   string
	Data []byte
}

// BatchResult represents the outcome of processing a BatchItem.
type BatchResult struct {
	ID       string  `json:"id"`
	Output   []byte  `json:"-"`
	Detected bool    `json:"detected"`
	Info     Info    `json:"info"`
	Score    float64 `json:"score"`
	Gain     float64 `json:"gain"`
	Err      error   `json:"err,omitempty"`
}

type matchResult struct {
	found    bool
	size     int
	x        int
	y        int
	score    float64
	gain     float64
	template Template
}

type scanRegion struct {
	minX, maxX int
	minY, maxY int
}

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
			c := rgba.RGBAAt(wmX+x, wmY+y)
			rgba.SetRGBA(wmX+x, wmY+y, color.RGBA{R: unblend(c.R, a), G: unblend(c.G, a), B: unblend(c.B, a), A: 255})
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
	src := image.NewRGBA(rgba.Bounds())
	draw.Draw(src, src.Bounds(), rgba, rgba.Bounds().Min, draw.Src)
	var r, g, b [25]uint8
	for y := 0; y < sz; y++ {
		for x := 0; x < sz; x++ {
			gx, gy := at(x+1, y)-at(x-1, y), at(x, y+1)-at(x, y-1)
			if gx*gx+gy*gy < 0.004 {
				continue
			}
			n := 0
			for dy := -2; dy <= 2; dy++ {
				for dx := -2; dx <= 2; dx++ {
					c := src.RGBAAt(match.x+x+dx, match.y+y+dy)
					r[n], g[n], b[n] = c.R, c.G, c.B
					n++
				}
			}
			rgba.SetRGBA(match.x+x, match.y+y, color.RGBA{R: median(r), G: median(g), B: median(b), A: 255})
		}
	}
}

func (e *Engine) denoiseInside(rgba *image.RGBA, match matchResult, gain float64) {
	sz := match.size
	src := image.NewRGBA(rgba.Bounds())
	draw.Draw(src, src.Bounds(), rgba, rgba.Bounds().Min, draw.Src)
	b := rgba.Bounds()
	rad := e.cfg.DenoiseRadius
	spaceSigma := e.cfg.SpaceSigma
	rangeSigma := e.cfg.RangeSigma
	denoiseScale := e.cfg.DenoiseScale

	for y := 0; y < sz; y++ {
		for x := 0; x < sz; x++ {
			a := math.Min(float64(match.template.Alpha[y*sz+x])*gain, 0.95)
			w := math.Min(1, a/(1.0-a)*denoiseScale)
			if w < 0.02 {
				continue
			}
			px, py := match.x+x, match.y+y
			c := src.RGBAAt(px, py)
			var sr, sg, sb, sw float64
			for dy := -rad; dy <= rad; dy++ {
				for dx := -rad; dx <= rad; dx++ {
					q := image.Pt(px+dx, py+dy)
					if !q.In(b) {
						continue
					}
					n := src.RGBAAt(q.X, q.Y)
					dr, dg, db := float64(n.R)-float64(c.R), float64(n.G)-float64(c.G), float64(n.B)-float64(c.B)
					k := math.Exp(-float64(dx*dx+dy*dy)/(2*spaceSigma*spaceSigma) - (dr*dr+dg*dg+db*db)/(3*2*rangeSigma*rangeSigma))
					sr += k * float64(n.R)
					sg += k * float64(n.G)
					sb += k * float64(n.B)
					sw += k
				}
			}
			mix := func(v uint8, f float64) uint8 { return clamp8((1-w)*float64(v) + w*f/sw) }
			rgba.SetRGBA(px, py, color.RGBA{R: mix(c.R, sr), G: mix(c.G, sg), B: mix(c.B, sb), A: 255})
		}
	}
}

func unblend(c uint8, a float64) uint8 {
	return clamp8((float64(c) - a*255.0) / (1.0 - a))
}

func clamp8(v float64) uint8 {
	return uint8(math.Round(math.Max(0, math.Min(255, v))))
}

func median(v [25]uint8) uint8 {
	slices.Sort(v[:])
	return v[12]
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

// Remove detects and removes the watermark, encoding the clean image to PNG.
func (e *Engine) Remove(data []byte) (output []byte, removed bool, err error) {
	if len(data) == 0 {
		return nil, false, fmt.Errorf("watermark: empty image data")
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, false, fmt.Errorf("watermark: decode image: %w", err)
	}

	cleaned, ok := e.RemoveImage(img)
	if !ok {
		return data, false, nil
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, cleaned); err != nil {
		return nil, false, fmt.Errorf("watermark: encode cleaned image: %w", err)
	}
	return buf.Bytes(), true, nil
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

func (e *Engine) removeInternal(data []byte) ([]byte, bool, Info, float64, float64, error) {
	if len(data) == 0 {
		return nil, false, Info{}, 0, 0, fmt.Errorf("watermark: empty image data")
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, false, Info{}, 0, 0, fmt.Errorf("watermark: decode image: %w", err)
	}

	match := e.findWatermark(img)
	if !match.found {
		return data, false, Info{}, match.score, match.gain, nil
	}

	b := img.Bounds()
	rgba := image.NewRGBA(b)
	draw.Draw(rgba, b, img, b.Min, draw.Src)
	e.reconstructWatermark(rgba, match)

	var buf bytes.Buffer
	if err := png.Encode(&buf, rgba); err != nil {
		return nil, false, Info{}, 0, 0, fmt.Errorf("watermark: encode cleaned image: %w", err)
	}

	info := Info{
		Size:     match.size,
		Position: image.Rect(match.x, match.y, match.x+match.size, match.y+match.size),
	}
	return buf.Bytes(), true, info, match.score, match.gain, nil
}

// ProcessBatch processes a slice of BatchItems concurrently using a thread-safe worker pool.
// Output ordering is guaranteed to match the input slice index.
func (e *Engine) ProcessBatch(ctx context.Context, items []BatchItem, workers int) []BatchResult {
	if len(items) == 0 {
		return nil
	}
	if workers <= 0 {
		workers = e.cfg.Workers
	}
	if workers > len(items) {
		workers = len(items)
	}

	results := make([]BatchResult, len(items))
	jobsCh := make(chan int, len(items))
	for i := range items {
		jobsCh <- i
	}
	close(jobsCh)

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for idx := range jobsCh {
				if ctx.Err() != nil {
					results[idx] = BatchResult{
						ID:  items[idx].ID,
						Err: ctx.Err(),
					}
					continue
				}
				out, detected, info, score, gain, err := e.removeInternal(items[idx].Data)
				results[idx] = BatchResult{
					ID:       items[idx].ID,
					Output:   out,
					Detected: detected,
					Info:     info,
					Score:    score,
					Gain:     gain,
					Err:      err,
				}
			}
		}()
	}
	wg.Wait()
	return results
}

// ProcessStream returns a stream channel receiving processed BatchResults from an input channel.
func (e *Engine) ProcessStream(ctx context.Context, in <-chan BatchItem, workers int) <-chan BatchResult {
	if workers <= 0 {
		workers = e.cfg.Workers
	}
	outCh := make(chan BatchResult, workers*2)
	var wg sync.WaitGroup

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case item, ok := <-in:
					if !ok {
						return
					}
					out, detected, info, score, gain, err := e.removeInternal(item.Data)
					res := BatchResult{
						ID:       item.ID,
						Output:   out,
						Detected: detected,
						Info:     info,
						Score:    score,
						Gain:     gain,
						Err:      err,
					}
					select {
					case <-ctx.Done():
						return
					case outCh <- res:
					}
				}
			}
		}()
	}

	go func() {
		wg.Wait()
		close(outCh)
	}()

	return outCh
}

// --- Backward-Compatible Top-Level Functions (Delegate to Default Engine) ---

// Detect checks if the image contains a watermark using the default Google Flow engine.
func Detect(data []byte) (present bool, score float64, info Info, err error) {
	return Default().Detect(data)
}

// Remove locates and removes the watermark using the default Google Flow engine.
func Remove(data []byte) (output []byte, removed bool, err error) {
	return Default().Remove(data)
}

// RemoveImage removes the watermark from an in-memory image.Image using default settings.
func RemoveImage(img image.Image) (*image.RGBA, bool) {
	return Default().RemoveImage(img)
}

// RemoveBatch processes multiple images concurrently using default settings.
func RemoveBatch(ctx context.Context, items []BatchItem, workers int) []BatchResult {
	return Default().ProcessBatch(ctx, items, workers)
}
