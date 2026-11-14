package contracts

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"app/internal/engine"
)

// Built-in workflow node type identifiers.
const (
	NodeTypeWatermarkRemover = "tool.watermark_remover"
	NodeTypeImageGenerate    = "tool.generate_image"
	NodeTypeVideoGenerate    = "tool.generate_video"
	NodeTypeTTSGenerate      = "tool.generate_tts"
	NodeTypeProviderList     = "tool.provider_list"
)

// RemoverInput is the input payload for the watermark remover node.
type RemoverInput struct {
	Image     engine.ImageInputRef  `json:"image,omitempty"`
	Images    engine.ImageInputRefs `json:"images,omitempty"`
	InPlace   bool                  `json:"inplace,omitempty"`
	OutputDir string                `json:"output_dir,omitempty"`
	Workers   int                   `json:"workers,omitempty"`
	Corner    string                `json:"corner,omitempty"`
	MinScore  float64               `json:"min_score,omitempty"`
	MinGain   float64               `json:"min_gain,omitempty"`
	MaxGain   float64               `json:"max_gain,omitempty"`
}

// RemoverItem provides execution details for each processed image.
type RemoverItem struct {
	Path     string  `json:"path"`
	OutPath  string  `json:"out_path"`
	Detected bool    `json:"detected"`
	Score    float64 `json:"score"`
	Size     int     `json:"size,omitempty"`
	Error    string  `json:"error,omitempty"`
}

// RemoverOutput is the output payload produced by the watermark remover node.
type RemoverOutput struct {
	Image         engine.MediaRef   `json:"image"`
	Images        []engine.MediaRef `json:"images"`
	Detected      bool              `json:"detected"`
	TotalCount    int               `json:"total_count"`
	DetectedCount int               `json:"detected_count"`
	Results       []RemoverItem     `json:"results,omitempty"`
}

// ImageGenerateInput is the merged input+params contract for tool.generate_image.
type ImageGenerateInput struct {
	Prompt          string                `json:"prompt"`
	ReferenceImages engine.ImageInputRefs `json:"reference_images,omitempty"`
	Model           string                `json:"model,omitempty"`
	AspectRatio     string                `json:"aspect_ratio,omitempty"`
	Resolution      string                `json:"resolution,omitempty"`
	Seed            *int                  `json:"seed,omitempty"`
	OutputDir       string                `json:"output_dir,omitempty"`
}

// ImageGenerateOutput is the typed result returned by tool.generate_image.
type ImageGenerateOutput struct {
	Image     engine.MediaRef `json:"image"`
	Path      string          `json:"path"`
	MimeType  string          `json:"mimeType"`
	Provider  string          `json:"provider"`
	Model     string          `json:"model"`
	SizeBytes int64           `json:"sizeBytes"`
}

// VideoGenerateInput is the merged input+params contract for tool.generate_video.
type VideoGenerateInput struct {
	Prompt          string                `json:"prompt,omitempty"`
	StartImage      engine.ImageInputRef  `json:"start_image,omitempty"`
	EndImage        engine.ImageInputRef  `json:"end_image,omitempty"`
	ReferenceImages engine.ImageInputRefs `json:"reference_images,omitempty"`
	Model           string                `json:"model,omitempty"`
	AspectRatio     string                `json:"aspect_ratio,omitempty"`
	Resolution      string                `json:"resolution,omitempty"`
	Duration        int                   `json:"duration,omitempty"`
	GenerateAudio   bool                  `json:"generate_audio,omitempty"`
	Seed            *int                  `json:"seed,omitempty"`
	OutputDir       string                `json:"output_dir,omitempty"`
}

// VideoGenerateOutput is the typed result returned by tool.generate_video.
type VideoGenerateOutput struct {
	Video     engine.MediaRef `json:"video"`
	Path      string          `json:"path"`
	MimeType  string          `json:"mimeType"`
	Provider  string          `json:"provider"`
	Model     string          `json:"model"`
	Duration  float64         `json:"duration"`
	SizeBytes int64           `json:"sizeBytes"`
}

// TTSGenerateInput is the merged input+params contract for tool.generate_tts.
type TTSGenerateInput struct {
	Text      string  `json:"text"`
	Model     string  `json:"model,omitempty"`
	Voice     string  `json:"voice,omitempty"`
	Format    string  `json:"format,omitempty"`
	Speed     float64 `json:"speed,omitempty"`
	Language  string  `json:"language,omitempty"`
	OutputDir string  `json:"output_dir,omitempty"`
}

// UnmarshalJSON tolerates speed encoded either as JSON number (1.2) or string ("1.2").
func (in *TTSGenerateInput) UnmarshalJSON(data []byte) error {
	type raw struct {
		Text      string `json:"text"`
		Model     string `json:"model,omitempty"`
		Voice     string `json:"voice,omitempty"`
		Format    string `json:"format,omitempty"`
		Speed     any    `json:"speed,omitempty"`
		Language  string `json:"language,omitempty"`
		OutputDir string `json:"output_dir,omitempty"`
	}
	var r raw
	if err := json.Unmarshal(data, &r); err != nil {
		return err
	}
	in.Text = r.Text
	in.Model = r.Model
	in.Voice = r.Voice
	in.Format = r.Format
	in.Language = r.Language
	in.OutputDir = r.OutputDir
	switch v := r.Speed.(type) {
	case float64:
		in.Speed = v
	case string:
		if strings.TrimSpace(v) != "" {
			parsed, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
			if err != nil {
				return fmt.Errorf("invalid speed %q: %w", v, err)
			}
			in.Speed = parsed
		}
	}
	return nil
}

// TTSGenerateOutput is the typed result returned by tool.generate_tts.
type TTSGenerateOutput struct {
	Audio     engine.MediaRef `json:"audio"`
	Path      string          `json:"path"`
	MimeType  string          `json:"mimeType"`
	Provider  string          `json:"provider"`
	Model     string          `json:"model"`
	SizeBytes int64           `json:"sizeBytes"`
}
