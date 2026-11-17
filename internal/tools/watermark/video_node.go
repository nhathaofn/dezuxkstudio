package watermark

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"app/internal/apperr"
	"app/internal/config"
	"app/internal/contracts"
	domainbinaries "app/internal/domain/binaries"
	"app/internal/engine"
	"app/internal/runner"
	"app/internal/service/media"
)

// NodeTypeVideo is the unique identifier for the sibling MP4 video watermark remover node.
const NodeTypeVideo = contracts.NodeTypeVideoWMRemove

type videoWatermarkNode struct {
	opts Options
}

// NewVideoNode returns tool.video_watermark_remove, keeping tool.watermark_remover spec untouched.
func NewVideoNode(opts Options) engine.Node {
	return &videoWatermarkNode{opts: opts}
}

func (n *videoWatermarkNode) Spec() engine.Spec {
	return engine.Spec{
		Type:        NodeTypeVideo,
		Provider:    "watermark",
		Label:       "Video Watermark Remover",
		Description: "Removes burned-in corner logos or watermark strips from MP4 videos via FFmpeg delogo or clean edge crop while preserving original audio.",
		Inputs: []engine.Port{
			{Name: "video_path", Label: "Source Video", Type: engine.PortVideo},
		},
		Outputs: []engine.Port{
			{Name: "video", Label: "Clean Video", Type: engine.PortVideo},
		},
		Params: []engine.Param{
			{Name: "mode", Label: "Removal Mode", Kind: engine.ParamSelect, Options: []string{"delogo", "crop"}, Default: "delogo"},
			{Name: "x", Label: "Logo X (px)", Kind: engine.ParamInteger, Min: 0, Max: 8192, Default: "0"},
			{Name: "y", Label: "Logo Y (px)", Kind: engine.ParamInteger, Min: 0, Max: 8192, Default: "0"},
			{Name: "width", Label: "Logo Width (px)", Kind: engine.ParamInteger, Min: 1, Max: 4096, Default: "160"},
			{Name: "height", Label: "Logo Height (px)", Kind: engine.ParamInteger, Min: 1, Max: 4096, Default: "64"},
			{Name: "crop_edge", Label: "Crop Edge", Kind: engine.ParamSelect, Options: []string{"bottom", "top", "left", "right"}, Default: "bottom"},
			{Name: "crop_px", Label: "Crop Size (px)", Kind: engine.ParamInteger, Min: 2, Max: 2048, Default: "64"},
			{Name: "output_dir", Label: "Output Folder", Kind: engine.ParamText, Role: engine.ParamRoleOutputDir},
		},
	}
}

func (n *videoWatermarkNode) Run(ctx context.Context, raw json.RawMessage) (any, error) {
	var in contracts.VideoWatermarkRemoveInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, apperr.Validation("invalid video_watermark_remove input").WithDetails(map[string]any{"cause": err.Error()})
	}
	trimmed := strings.TrimSpace(in.VideoPath)
	if trimmed == "" {
		return nil, apperr.Validation("video_path is required")
	}
	mediaDir := resolveMediaDir(n.opts)
	vidPath := engine.ResolveMediaPath(trimmed, mediaDir)
	if vidPath == "" {
		return nil, apperr.Validation("video_path is invalid or escapes workspace")
	}
	if err := engine.CheckFile(ctx, vidPath, false); err != nil {
		return nil, apperr.New(apperr.CodeValidation, sanitizePathError(vidPath, err))
	}
	if _, err := os.Stat(vidPath); err != nil {
		return nil, apperr.New(apperr.CodeValidation, sanitizePathError(vidPath, err))
	}

	if n.opts.Binaries == nil {
		return nil, apperr.Unavailable("Binary resolver unavailable for FFmpeg")
	}
	ffmpegExe, _, _, err := n.opts.Binaries.Resolve(ctx, domainbinaries.IDFFmpeg)
	if err != nil || strings.TrimSpace(ffmpegExe) == "" {
		return nil, apperr.Unavailable("ffmpeg is not installed")
	}

	outDir := strings.TrimSpace(in.OutputDir)
	if outDir == "" {
		outDir = resolveOutputDir(n.opts)
	}
	if outDir == "" && mediaDir != "" {
		outDir = filepath.Join(mediaDir, "watermark")
	}
	if outDir == "" {
		outDir = os.TempDir()
	}
	cleanOutDir := filepath.Clean(outDir)
	if !filepath.IsAbs(cleanOutDir) && mediaDir != "" {
		cleanOutDir = filepath.Join(mediaDir, cleanOutDir)
	}
	outName := fmt.Sprintf("clean_%d%s", time.Now().UnixNano(), ".mp4")
	outPath := filepath.Join(cleanOutDir, outName)
	if err := engine.CheckFile(ctx, outPath, true); err != nil {
		return nil, apperr.New(apperr.CodeValidation, sanitizePathError(outPath, err))
	}
	if err := os.MkdirAll(cleanOutDir, 0o750); err != nil {
		return nil, apperr.New(apperr.CodeInternal, sanitizePathError(cleanOutDir, err))
	}

	studioCfg := n.opts.StudioConfig
	defStudio := config.Default(config.ModeDesktop).Studio
	if studioCfg.FFmpegPreset == "" {
		studioCfg.FFmpegPreset = defStudio.FFmpegPreset
	}
	if studioCfg.FFmpegCRF <= 0 {
		studioCfg.FFmpegCRF = defStudio.FFmpegCRF
	}
	if studioCfg.FFmpegTimeout <= 0 {
		studioCfg.FFmpegTimeout = defStudio.FFmpegTimeout
	}

	vf, err := buildVideoWatermarkFilter(in)
	if err != nil {
		return nil, err
	}

	ffCtx, cancel := context.WithTimeout(ctx, studioCfg.FFmpegTimeout)
	defer cancel()
	args := []string{
		"-y", "-i", vidPath,
		"-vf", vf,
		"-c:v", "libx264", "-preset", studioCfg.FFmpegPreset, "-crf", strconv.Itoa(studioCfg.FFmpegCRF), "-pix_fmt", "yuv420p",
		"-c:a", "copy", "-movflags", "+faststart", outPath,
	}
	res, err := runner.Run(ffCtx, ffmpegExe, args...)
	if err != nil {
		return nil, apperr.New(apperr.CodeUnavailable, sanitizePathError(vidPath, err))
	}
	if res.ExitCode != 0 {
		return nil, apperr.Unavailable("FFmpeg video watermark removal failed: " + strings.TrimSpace(res.Stderr))
	}

	var assetID string
	if n.opts.Registrar != nil {
		if item, rerr := n.opts.Registrar.RegisterAsset(ctx, media.RegisterAssetParams{
			Kind:           "video",
			SourceNode:     NodeTypeVideo,
			FilePath:       outPath,
			RegisteredRoot: cleanOutDir,
		}); rerr == nil && item != nil {
			assetID = item.ID
		}
	}
	return contracts.VideoWatermarkRemoveOutput{
		Video:   engine.RefFor(outPath, engine.MediaVideo),
		Path:    outPath,
		AssetID: assetID,
	}, nil
}

func buildVideoWatermarkFilter(in contracts.VideoWatermarkRemoveInput) (string, error) {
	mode := strings.ToLower(strings.TrimSpace(in.Mode))
	if mode == "" {
		mode = "delogo"
	}
	if mode == "crop" {
		px := in.CropPx
		if px < 2 {
			return "", apperr.Validation("crop_px must be >= 2 for crop mode")
		}
		// Keep even pixel dimensions for H.264 yuv420p compatibility
		if px%2 != 0 {
			px++
		}
		edge := strings.ToLower(strings.TrimSpace(in.CropEdge))
		switch edge {
		case "", "bottom":
			return fmt.Sprintf("crop=iw:ih-%d:0:0,pad=iw:ih+%d:0:0:black", px, px), nil
		case "top":
			return fmt.Sprintf("crop=iw:ih-%d:0:%d,pad=iw:ih+%d:0:%d:black", px, px, px, px), nil
		case "left":
			return fmt.Sprintf("crop=iw-%d:ih:%d:0", px, px), nil
		case "right":
			return fmt.Sprintf("crop=iw-%d:ih:0:0", px), nil
		default:
			return "", apperr.Validation("invalid crop_edge: must be bottom, top, left, or right")
		}
	}
	if in.X < 0 || in.Y < 0 || in.Width <= 0 || in.Height <= 0 {
		return "", apperr.Validation("delogo mode requires non-negative x,y and positive width,height")
	}
	return fmt.Sprintf("delogo=x=%d:y=%d:w=%d:h=%d", in.X, in.Y, in.Width, in.Height), nil
}
