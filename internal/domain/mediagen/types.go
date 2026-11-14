// Package mediagen holds the domain types and validation rules for unified
// image, video and text-to-speech generation across configured providers.
package mediagen

// Kind identifies which media capability a generation request uses.
type Kind string

const (
	KindImage Kind = "image"
	KindVideo Kind = "video"
	KindTTS   Kind = "tts"
)

const (
	DefaultImageAspectRatio = "1:1"
	DefaultImageResolution  = "1K"

	DefaultVideoAspectRatio = "16:9"
	DefaultVideoResolution  = "720p"
	DefaultVideoDurationSec = 5

	DefaultTTSFormat = "wav"
	DefaultTTSSpeed  = 1.0
)

// ImageAspectRatios returns supported image aspect ratios in UI display order.
func ImageAspectRatios() []string {
	return []string{"1:1", "16:9", "9:16", "4:3", "3:4", "3:2", "2:3", "21:9"}
}

// ImageResolutions returns supported image resolution tiers in UI display order.
func ImageResolutions() []string {
	return []string{"1K", "2K", "4K"}
}

// VideoAspectRatios returns supported video aspect ratios in UI display order.
func VideoAspectRatios() []string {
	return []string{"16:9", "9:16", "1:1", "4:3", "3:4", "21:9"}
}

// VideoResolutions returns supported video resolution tiers in UI display order.
func VideoResolutions() []string {
	return []string{"480p", "720p", "1080p"}
}

// TTSFormats returns supported speech audio container/encoding formats.
func TTSFormats() []string {
	return []string{"wav", "mp3", "opus", "aac", "flac", "pcm"}
}

// ImageRequest describes a single image generation or image-editing call.
type ImageRequest struct {
	// ModelRef identifies "provider:model" or "custom:<id>:<model>"; empty means resolve default ("auto").
	ModelRef        string
	Prompt          string
	ReferenceImages []string
	AspectRatio     string
	Resolution      string
	Seed            *int
	OutputDir       string
}

// VideoRequest describes a single text-to-video or image-to-video generation call.
type VideoRequest struct {
	ModelRef        string
	Prompt          string
	StartImage      string
	EndImage        string
	ReferenceImages []string
	AspectRatio     string
	Resolution      string
	DurationSec     int
	GenerateAudio   bool
	Seed            *int
	OutputDir       string
}

// TTSRequest describes a single text-to-speech synthesis call.
type TTSRequest struct {
	ModelRef  string
	Text      string
	Voice     string
	Format    string
	Speed     float64
	Language  string
	OutputDir string
}

// Result describes one generated media file saved on disk.
type Result struct {
	Kind      Kind    `json:"kind"`
	Path      string  `json:"path"`
	LocalURL  string  `json:"local_url"`
	MimeType  string  `json:"mime_type"`
	Provider  string  `json:"provider"`
	Model     string  `json:"model"`
	SizeBytes int64   `json:"size_bytes"`
	Duration  float64 `json:"duration,omitempty"`
	Width     int     `json:"width,omitempty"`
	Height    int     `json:"height,omitempty"`
}
