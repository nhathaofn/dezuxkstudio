// Package watermark provides dynamic multi-scale watermark detection and
// reconstruction for Google Flow / Imagen / Gemini and custom watermark templates.
// It is fully thread-safe, zero-hardcode, and supports parallel batch processing.
package watermark

import (
	"bytes"
	_ "embed"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"runtime"
	"sync"
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

// ImageFormat specifies the detected or exported image format.
type ImageFormat string

const (
	FormatPNG     ImageFormat = "png"
	FormatJPEG    ImageFormat = "jpeg"
	FormatWebP    ImageFormat = "webp"
	FormatUnknown ImageFormat = "unknown"
)

// BatchResult represents the outcome of processing a BatchItem.
type BatchResult struct {
	ID           string      `json:"id"`
	Output       []byte      `json:"-"`
	OutputFormat ImageFormat `json:"output_format,omitempty"`
	Detected     bool        `json:"detected"`
	Info         Info        `json:"info"`
	Score        float64     `json:"score"`
	Gain         float64     `json:"gain"`
	Err          error       `json:"err,omitempty"`
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
