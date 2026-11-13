// Package mediagen exposes image, video and TTS generation as typed engine workflow nodes
// usable both from standalone UI test tabs and by the AI agent via MCP workflow tools.
package mediagen

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	domainmedia "app/internal/domain/mediagen"
	"app/internal/engine"
)

const (
	NodeTypeImage = "tool.generate_image"
	NodeTypeVideo = "tool.generate_video"
	NodeTypeTTS   = "tool.generate_tts"
)

// MediaService is satisfied by *service/mediagen.Service.
type MediaService interface {
	GenerateImage(ctx context.Context, req domainmedia.ImageRequest) (domainmedia.Result, error)
	GenerateVideo(ctx context.Context, req domainmedia.VideoRequest) (domainmedia.Result, error)
	GenerateTTS(ctx context.Context, req domainmedia.TTSRequest) (domainmedia.Result, error)
}

// Options configures the unified media generation workflow nodes.
type Options struct {
	MediaDir string
	Service  MediaService
}

// ImageRef accepts either a JSON string path or an engine.MediaRef object.
type ImageRef struct {
	engine.MediaRef
}

// UnmarshalJSON implements json.Unmarshaler.
func (r *ImageRef) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		if strings.TrimSpace(s) != "" {
			r.MediaRef = engine.RefFor(strings.TrimSpace(s), engine.MediaImage)
		}
		return nil
	}
	var ref engine.MediaRef
	if err := json.Unmarshal(data, &ref); err != nil {
		return err
	}
	if ref.LocalURL != "" && ref.Kind == "" {
		ref.Kind = engine.MediaImage
	}
	r.MediaRef = ref
	return nil
}

// ImageRefs accepts either a slice of strings/MediaRefs or a single string/MediaRef.
type ImageRefs []engine.MediaRef

// UnmarshalJSON implements json.Unmarshaler.
func (r *ImageRefs) UnmarshalJSON(data []byte) error {
	var singleStr string
	if err := json.Unmarshal(data, &singleStr); err == nil {
		if strings.TrimSpace(singleStr) != "" {
			*r = []engine.MediaRef{engine.RefFor(strings.TrimSpace(singleStr), engine.MediaImage)}
		}
		return nil
	}
	var singleRef engine.MediaRef
	if err := json.Unmarshal(data, &singleRef); err == nil && singleRef.LocalURL != "" {
		if singleRef.Kind == "" {
			singleRef.Kind = engine.MediaImage
		}
		*r = []engine.MediaRef{singleRef}
		return nil
	}
	var rawList []json.RawMessage
	if err := json.Unmarshal(data, &rawList); err != nil {
		return fmt.Errorf("expected image list or path: %w", err)
	}
	out := make([]engine.MediaRef, 0, len(rawList))
	for _, item := range rawList {
		var img ImageRef
		if err := json.Unmarshal(item, &img); err != nil {
			return err
		}
		if img.LocalURL != "" {
			out = append(out, img.MediaRef)
		}
	}
	*r = out
	return nil
}

// ImageGenerateInput is the merged input+params contract for tool.generate_image.
type ImageGenerateInput struct {
	Prompt          string    `json:"prompt"`
	ReferenceImages ImageRefs `json:"reference_images,omitempty"`
	Model           string    `json:"model,omitempty"`
	AspectRatio     string    `json:"aspect_ratio,omitempty"`
	Resolution      string    `json:"resolution,omitempty"`
	Seed            *int      `json:"seed,omitempty"`
	OutputDir       string    `json:"output_dir,omitempty"`
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
	Prompt          string    `json:"prompt,omitempty"`
	StartImage      ImageRef  `json:"start_image,omitempty"`
	EndImage        ImageRef  `json:"end_image,omitempty"`
	ReferenceImages ImageRefs `json:"reference_images,omitempty"`
	Model           string    `json:"model,omitempty"`
	AspectRatio     string    `json:"aspect_ratio,omitempty"`
	Resolution      string    `json:"resolution,omitempty"`
	Duration        int       `json:"duration,omitempty"`
	GenerateAudio   bool      `json:"generate_audio,omitempty"`
	Seed            *int      `json:"seed,omitempty"`
	OutputDir       string    `json:"output_dir,omitempty"`
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
