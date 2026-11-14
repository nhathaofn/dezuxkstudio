// Package mediagen exposes image, video and TTS generation as typed engine workflow nodes
// usable both from standalone UI test tabs and by the AI agent via MCP workflow tools.
package mediagen

import (
	"context"

	"app/internal/contracts"
	domainmedia "app/internal/domain/mediagen"
	"app/internal/engine"
)

const (
	NodeTypeImage = contracts.NodeTypeImageGenerate
	NodeTypeVideo = contracts.NodeTypeVideoGenerate
	NodeTypeTTS   = contracts.NodeTypeTTSGenerate
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
type ImageRef = engine.ImageInputRef

// ImageRefs accepts either a slice of strings/MediaRefs or a single string/MediaRef.
type ImageRefs = engine.ImageInputRefs

// ImageGenerateInput is the merged input+params contract for tool.generate_image.
type ImageGenerateInput = contracts.ImageGenerateInput

// ImageGenerateOutput is the typed result returned by tool.generate_image.
type ImageGenerateOutput = contracts.ImageGenerateOutput

// VideoGenerateInput is the merged input+params contract for tool.generate_video.
type VideoGenerateInput = contracts.VideoGenerateInput

// VideoGenerateOutput is the typed result returned by tool.generate_video.
type VideoGenerateOutput = contracts.VideoGenerateOutput

// TTSGenerateInput is the merged input+params contract for tool.generate_tts.
type TTSGenerateInput = contracts.TTSGenerateInput

// TTSGenerateOutput is the typed result returned by tool.generate_tts.
type TTSGenerateOutput = contracts.TTSGenerateOutput
