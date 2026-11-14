package mediagen

import (
	"context"
	"path/filepath"
	"strings"

	"app/internal/apperr"
	domainmedia "app/internal/domain/mediagen"
	"app/internal/engine"
)

// NewNodes returns all three unified media generation workflow nodes.
func NewNodes(opts Options) []engine.Node {
	return []engine.Node{
		NewImageNode(opts),
		NewVideoNode(opts),
		NewTTSNode(opts),
	}
}

// NewImageNode builds the tool.generate_image workflow node.
func NewImageNode(opts Options) engine.Node {
	spec := engine.Spec{
		Type:        NodeTypeImage,
		Provider:    "tool",
		Label:       "Image Generator",
		Description: "Generate or edit an image using configured API providers (empty model auto-selects default)",
		Inputs: []engine.Port{
			{Name: "prompt", Label: "Prompt", Type: engine.PortText},
			{Name: "reference_images", Label: "Reference images", Type: engine.PortImage, Multiple: true, Optional: true},
		},
		Outputs: []engine.Port{
			{Name: "image", Label: "Generated image", Type: engine.PortImage},
		},
		Params: []engine.Param{
			{Name: "model", Label: "Model (empty = Auto default)", Kind: engine.ParamText},
			{Name: "aspect_ratio", Label: "Aspect ratio", Kind: engine.ParamSelect, Default: domainmedia.DefaultImageAspectRatio, Options: domainmedia.ImageAspectRatios()},
			{Name: "resolution", Label: "Resolution", Kind: engine.ParamSelect, Default: domainmedia.DefaultImageResolution, Options: domainmedia.ImageResolutions()},
			{Name: "seed", Label: "Seed", Kind: engine.ParamInteger, Min: 0, Max: 2147483647},
			{Name: "output_dir", Label: "Output directory (empty = workspace generated folder)", Kind: engine.ParamText, Role: engine.ParamRoleOutputDir},
		},
	}

	return engine.Typed(spec, func(ctx context.Context, in ImageGenerateInput) (ImageGenerateOutput, error) {
		if opts.Service == nil {
			return ImageGenerateOutput{}, apperr.Unavailable("Media generation service is unavailable")
		}
		refs, err := checkInputRefs(ctx, opts.MediaDir, in.ReferenceImages)
		if err != nil {
			return ImageGenerateOutput{}, err
		}
		outDir, err := checkOutputDir(ctx, opts.MediaDir, in.OutputDir, "images")
		if err != nil {
			return ImageGenerateOutput{}, err
		}

		res, err := opts.Service.GenerateImage(ctx, domainmedia.ImageRequest{
			ModelRef:        in.Model,
			Prompt:          in.Prompt,
			ReferenceImages: refs,
			AspectRatio:     in.AspectRatio,
			Resolution:      in.Resolution,
			Seed:            in.Seed,
			OutputDir:       outDir,
		})
		if err != nil {
			return ImageGenerateOutput{}, err
		}
		ref := engine.RefFor(res.Path, engine.MediaImage)
		if res.MimeType != "" {
			ref.MimeType = res.MimeType
		}
		return ImageGenerateOutput{
			Image:     ref,
			Path:      res.Path,
			MimeType:  res.MimeType,
			Provider:  res.Provider,
			Model:     res.Model,
			SizeBytes: res.SizeBytes,
		}, nil
	})
}

// NewVideoNode builds the tool.generate_video workflow node.
func NewVideoNode(opts Options) engine.Node {
	spec := engine.Spec{
		Type:        NodeTypeVideo,
		Provider:    "tool",
		Label:       "Video Generator",
		Description: "Generate a video from text, start/end keyframes, or reference images using configured API providers",
		Inputs: []engine.Port{
			{Name: "prompt", Label: "Prompt", Type: engine.PortText, Optional: true},
			{Name: "start_image", Label: "Start frame image", Type: engine.PortImage, Optional: true},
			{Name: "end_image", Label: "End frame image", Type: engine.PortImage, Optional: true},
			{Name: "reference_images", Label: "Reference images", Type: engine.PortImage, Multiple: true, Optional: true},
		},
		Outputs: []engine.Port{
			{Name: "video", Label: "Generated video", Type: engine.PortVideo},
		},
		Params: []engine.Param{
			{Name: "model", Label: "Model (empty = Auto default)", Kind: engine.ParamText},
			{Name: "aspect_ratio", Label: "Aspect ratio", Kind: engine.ParamSelect, Default: domainmedia.DefaultVideoAspectRatio, Options: domainmedia.VideoAspectRatios()},
			{Name: "resolution", Label: "Resolution", Kind: engine.ParamSelect, Default: domainmedia.DefaultVideoResolution, Options: domainmedia.VideoResolutions()},
			{Name: "duration", Label: "Duration (seconds)", Kind: engine.ParamInteger, Min: 0, Max: 60, Default: "0"},
			{Name: "generate_audio", Label: "Generate native audio (when supported)", Kind: engine.ParamToggle, Default: "false"},
			{Name: "seed", Label: "Seed", Kind: engine.ParamInteger, Min: 0, Max: 2147483647},
			{Name: "output_dir", Label: "Output directory (empty = workspace generated folder)", Kind: engine.ParamText, Role: engine.ParamRoleOutputDir},
		},
	}

	return engine.Typed(spec, func(ctx context.Context, in VideoGenerateInput) (VideoGenerateOutput, error) {
		if opts.Service == nil {
			return VideoGenerateOutput{}, apperr.Unavailable("Media generation service is unavailable")
		}
		startImg, err := checkSingleRef(ctx, opts.MediaDir, in.StartImage.MediaRef)
		if err != nil {
			return VideoGenerateOutput{}, err
		}
		endImg, err := checkSingleRef(ctx, opts.MediaDir, in.EndImage.MediaRef)
		if err != nil {
			return VideoGenerateOutput{}, err
		}
		refs, err := checkInputRefs(ctx, opts.MediaDir, in.ReferenceImages)
		if err != nil {
			return VideoGenerateOutput{}, err
		}
		outDir, err := checkOutputDir(ctx, opts.MediaDir, in.OutputDir, "videos")
		if err != nil {
			return VideoGenerateOutput{}, err
		}

		res, err := opts.Service.GenerateVideo(ctx, domainmedia.VideoRequest{
			ModelRef:        in.Model,
			Prompt:          in.Prompt,
			StartImage:      startImg,
			EndImage:        endImg,
			ReferenceImages: refs,
			AspectRatio:     in.AspectRatio,
			Resolution:      in.Resolution,
			DurationSec:     in.Duration,
			GenerateAudio:   in.GenerateAudio,
			Seed:            in.Seed,
			OutputDir:       outDir,
		})
		if err != nil {
			return VideoGenerateOutput{}, err
		}
		ref := engine.RefFor(res.Path, engine.MediaVideo)
		if res.MimeType != "" {
			ref.MimeType = res.MimeType
		}
		return VideoGenerateOutput{
			Video:     ref,
			Path:      res.Path,
			MimeType:  res.MimeType,
			Provider:  res.Provider,
			Model:     res.Model,
			Duration:  res.Duration,
			SizeBytes: res.SizeBytes,
		}, nil
	})
}

// NewTTSNode builds the tool.generate_tts workflow node.
func NewTTSNode(opts Options) engine.Node {
	spec := engine.Spec{
		Type:        NodeTypeTTS,
		Provider:    "tool",
		Label:       "Speech Generator (TTS)",
		Description: "Synthesize speech audio from text using configured API providers (empty model auto-selects default)",
		Inputs: []engine.Port{
			{Name: "text", Label: "Text to speak", Type: engine.PortText},
		},
		Outputs: []engine.Port{
			{Name: "audio", Label: "Generated audio", Type: engine.PortAudio},
		},
		Params: []engine.Param{
			{Name: "model", Label: "Model (empty = Auto default)", Kind: engine.ParamText},
			{Name: "voice", Label: "Voice ID (empty = provider default)", Kind: engine.ParamText},
			{Name: "format", Label: "Audio format", Kind: engine.ParamSelect, Default: domainmedia.DefaultTTSFormat, Options: domainmedia.TTSFormats()},
			{Name: "speed", Label: "Speed (0.25 - 4.0, default 1.0)", Kind: engine.ParamText, Default: "1.0"},
			{Name: "language", Label: "Language hint (optional)", Kind: engine.ParamText},
			{Name: "output_dir", Label: "Output directory (empty = workspace generated folder)", Kind: engine.ParamText, Role: engine.ParamRoleOutputDir},
		},
	}

	return engine.Typed(spec, func(ctx context.Context, in TTSGenerateInput) (TTSGenerateOutput, error) {
		if opts.Service == nil {
			return TTSGenerateOutput{}, apperr.Unavailable("Media generation service is unavailable")
		}
		outDir, err := checkOutputDir(ctx, opts.MediaDir, in.OutputDir, "tts")
		if err != nil {
			return TTSGenerateOutput{}, err
		}

		res, err := opts.Service.GenerateTTS(ctx, domainmedia.TTSRequest{
			ModelRef:  in.Model,
			Text:      in.Text,
			Voice:     in.Voice,
			Format:    in.Format,
			Speed:     in.Speed,
			Language:  in.Language,
			OutputDir: outDir,
		})
		if err != nil {
			return TTSGenerateOutput{}, err
		}
		ref := engine.RefFor(res.Path, engine.MediaAudio)
		if res.MimeType != "" {
			ref.MimeType = res.MimeType
		}
		return TTSGenerateOutput{
			Audio:     ref,
			Path:      res.Path,
			MimeType:  res.MimeType,
			Provider:  res.Provider,
			Model:     res.Model,
			SizeBytes: res.SizeBytes,
		}, nil
	})
}

func checkSingleRef(ctx context.Context, mediaDir string, ref engine.MediaRef) (string, error) {
	if strings.TrimSpace(ref.LocalURL) == "" {
		return "", nil
	}
	resolved := engine.ResolveMediaPath(ref.LocalURL, mediaDir)
	if resolved == "" {
		return "", apperr.Validation("Input path is outside the workspace media directory")
	}
	if err := engine.CheckFile(ctx, resolved, false); err != nil {
		return "", err
	}
	return resolved, nil
}

func checkInputRefs(ctx context.Context, mediaDir string, refs []engine.MediaRef) ([]string, error) {
	if len(refs) == 0 {
		return nil, nil
	}
	out := make([]string, 0, len(refs))
	for _, r := range refs {
		resolved, err := checkSingleRef(ctx, mediaDir, r)
		if err != nil {
			return nil, err
		}
		if resolved != "" {
			out = append(out, resolved)
		}
	}
	return out, nil
}

func checkOutputDir(ctx context.Context, mediaDir, rawDir, sub string) (string, error) {
	trimmed := strings.TrimSpace(rawDir)
	var target string
	if trimmed == "" {
		if mediaDir == "" {
			return "", nil
		}
		target = filepath.Join(mediaDir, "generated", sub)
	} else {
		target = engine.ResolveMediaPath(trimmed, mediaDir)
		if target == "" {
			return "", apperr.Validation("Output directory is outside the workspace media directory")
		}
	}
	if err := engine.CheckFile(ctx, target, true); err != nil {
		return "", err
	}
	return target, nil
}
