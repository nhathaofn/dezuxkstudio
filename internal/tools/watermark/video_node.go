package watermark

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"app/internal/apperr"
	"app/internal/config"
	"app/internal/contracts"
	domainbinaries "app/internal/domain/binaries"
	"app/internal/engine"
	"app/internal/progress"
	"app/internal/runner"
	"app/internal/service/media"
)

// NodeTypeVideo is the unique identifier for the sibling MP4 video watermark remover node.
const NodeTypeVideo = contracts.NodeTypeVideoWMRemove

const (
	// minVideoWatermarkDim is the minimum H.264 encodeable video dimension floor in pixels.
	minVideoWatermarkDim = 64
	// minVideoCropPx and videoCropEvenAlign keep cropped frame dimensions positive and yuv420p-even.
	minVideoCropPx     = 2
	videoCropEvenAlign = 2
	// videoMicrosecondsPerSec converts seconds to FFmpeg `-progress` microsecond counters.
	videoMicrosecondsPerSec = 1_000_000.0
)

var (
	videoRemovalModes = []string{"delogo", "crop"}
	videoCropEdges    = []string{"bottom", "top", "left", "right"}
)

// NewVideoRemoveNode is an alias for NewVideoNode returning tool.video_watermark_remove.
func NewVideoRemoveNode(opts Options) engine.Node {
	return NewVideoNode(opts)
}

func resolveWatermarkSettings(opts Options) config.WatermarkConfig {
	if opts.WatermarkConfig != (config.WatermarkConfig{}) {
		return config.ResolveWatermarkConfig(opts.WatermarkConfig)
	}
	if opts.Config != nil {
		return opts.Config.WatermarkOrDefault()
	}
	return config.ResolveWatermarkConfig(config.WatermarkConfig{})
}

// NewVideoNode returns tool.video_watermark_remove, keeping tool.watermark_remover spec untouched.
func NewVideoNode(opts Options) engine.Node {
	studioCfg := config.ResolveStudioConfig(opts.StudioConfig)
	wmCfg := resolveWatermarkSettings(opts)

	maxDim := studioCfg.MaxVideoDimension
	if maxDim < minVideoWatermarkDim {
		maxDim = config.ResolveStudioConfig(config.StudioConfig{}).MaxVideoDimension
	}
	maxLogoDim := max(minVideoWatermarkDim, maxDim/2)
	maxCrop := max(minVideoCropPx, maxDim/4)

	defW := min(wmCfg.VideoDefaultWidth, maxLogoDim)
	defH := min(wmCfg.VideoDefaultHeight, maxLogoDim)
	defCrop := min(wmCfg.VideoDefaultCropPx, maxCrop)

	spec := engine.Spec{
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
			{Name: "mode", Label: "Removal Mode", Kind: engine.ParamSelect, Options: videoRemovalModes, Default: "delogo"},
			{Name: "x", Label: "Logo X (px)", Kind: engine.ParamInteger, Min: 0, Max: maxDim, Default: "0"},
			{Name: "y", Label: "Logo Y (px)", Kind: engine.ParamInteger, Min: 0, Max: maxDim, Default: "0"},
			{Name: "width", Label: "Logo Width (px)", Kind: engine.ParamInteger, Min: 1, Max: maxLogoDim, Default: strconv.Itoa(defW)},
			{Name: "height", Label: "Logo Height (px)", Kind: engine.ParamInteger, Min: 1, Max: maxLogoDim, Default: strconv.Itoa(defH)},
			{Name: "crop_edge", Label: "Crop Edge", Kind: engine.ParamSelect, Options: videoCropEdges, Default: "bottom"},
			{Name: "crop_px", Label: "Crop Size (px)", Kind: engine.ParamInteger, Min: minVideoCropPx, Max: maxCrop, Default: strconv.Itoa(defCrop)},
			{Name: "output_dir", Label: "Output Folder", Kind: engine.ParamText, Role: engine.ParamRoleOutputDir},
		},
	}

	return engine.Typed(spec, func(ctx context.Context, in contracts.VideoWatermarkRemoveInput) (contracts.VideoWatermarkRemoveOutput, error) {
		vidPath, err := resolveVideoInputPath(ctx, opts, in.VideoPath.LocalURL)
		if err != nil {
			return contracts.VideoWatermarkRemoveOutput{}, err
		}

		if opts.Binaries == nil {
			return contracts.VideoWatermarkRemoveOutput{}, apperr.Unavailable("Binary resolver unavailable for FFmpeg").
				WithMsg("errors.watermark.binary_unavailable", nil)
		}
		ffmpegExe, _, _, err := opts.Binaries.Resolve(ctx, domainbinaries.IDFFmpeg)
		if err != nil || strings.TrimSpace(ffmpegExe) == "" {
			return contracts.VideoWatermarkRemoveOutput{}, apperr.Unavailable("ffmpeg is not installed").
				WithMsg("errors.watermark.ffmpeg_missing", nil)
		}

		cleanOutDir, err := resolveVideoOutputDir(ctx, opts, in.OutputDir)
		if err != nil {
			return contracts.VideoWatermarkRemoveOutput{}, err
		}

		vf, err := buildVideoWatermarkFilter(in, defW, defH, defCrop, maxDim, maxLogoDim, maxCrop)
		if err != nil {
			return contracts.VideoWatermarkRemoveOutput{}, err
		}

		outName := fmt.Sprintf("clean_%d.mp4", time.Now().UnixNano())
		outPath := filepath.Join(cleanOutDir, outName)
		if err := checkWatermarkFile(ctx, outPath, true); err != nil {
			return contracts.VideoWatermarkRemoveOutput{}, err
		}
		if err := os.MkdirAll(cleanOutDir, 0o750); err != nil {
			return contracts.VideoWatermarkRemoveOutput{}, apperr.New(apperr.CodeUnavailable, sanitizePathError(cleanOutDir, err)).
				WithMsg("errors.watermark.io_failed", nil)
		}

		tmpFile, terr := os.CreateTemp(cleanOutDir, ".tmp-clean-*.mp4")
		if terr != nil {
			return contracts.VideoWatermarkRemoveOutput{}, apperr.New(apperr.CodeUnavailable, "Failed to allocate temporary video output file").
				WithMsg("errors.watermark.io_failed", nil)
		}
		tmpOut := tmpFile.Name()
		_ = tmpFile.Close()
		defer func() { _ = os.Remove(tmpOut) }()

		srcMeta := probeVideoMetadata(ctx, opts, vidPath)
		totalUs := int64(srcMeta.durationSec * videoMicrosecondsPerSec)

		ffCtx, cancel := context.WithTimeout(ctx, studioCfg.FFmpegTimeout)
		defer cancel()
		progress.Report(ctx, progress.Update{
			Percent: 5,
			Message: "Removing video watermark…",
			Key:     "watermark.progress.video",
			Params:  map[string]string{"sec": "0.0"},
		})
		args := []string{
			"-y", "-i", vidPath,
			"-vf", vf,
			"-c:v", studioCfg.FFmpegVideoCodec, "-preset", studioCfg.FFmpegPreset, "-crf", strconv.Itoa(studioCfg.FFmpegCRF), "-pix_fmt", studioCfg.FFmpegPixelFormat,
			"-c:a", "copy", "-movflags", "+faststart", "-progress", "pipe:1", "-nostats", tmpOut,
		}
		runOpts := runner.FFmpegPipeProgressOptions(ctx, totalUs, "watermark.progress.video", "Cleaning video watermark")
		res, err := runner.RunWithOptions(ffCtx, ffmpegExe, args, runOpts)
		if err != nil || res == nil || res.ExitCode != 0 {
			if ctx.Err() != nil {
				return contracts.VideoWatermarkRemoveOutput{}, ctx.Err()
			}
			return contracts.VideoWatermarkRemoveOutput{}, apperr.New(apperr.CodeUnavailable, "FFmpeg video watermark removal failed").
				WithMsg("errors.watermark.ffmpeg_failed", nil)
		}
		if rerr := os.Rename(tmpOut, outPath); rerr != nil {
			return contracts.VideoWatermarkRemoveOutput{}, apperr.New(apperr.CodeUnavailable, "Failed to finalize cleaned video file").
				WithMsg("errors.watermark.io_failed", nil)
		}
		progress.Report(ctx, progress.Update{Percent: 100, Message: "Video watermark removed", Key: "watermark.progress.done"})

		outMeta := probeVideoMetadata(ctx, opts, outPath)
		if outMeta.durationSec <= 0 {
			outMeta.durationSec = srcMeta.durationSec
		}
		if outMeta.width <= 0 {
			outMeta.width = srcMeta.width
			outMeta.height = srcMeta.height
		}

		var assetID string
		if opts.Registrar != nil {
			item, rerr := opts.Registrar.RegisterAsset(ctx, media.RegisterAssetParams{
				Kind:           "video",
				SourceNode:     NodeTypeVideo,
				FilePath:       outPath,
				RegisteredRoot: cleanOutDir,
				MIMEType:       "video/mp4",
				Width:          outMeta.width,
				Height:         outMeta.height,
				DurationSec:    outMeta.durationSec,
			})
			if rerr != nil {
				if opts.Logger != nil {
					opts.Logger.WarnContext(ctx, "watermark: failed to register cleaned video asset",
						"source_node", NodeTypeVideo,
						"error", rerr,
					)
				}
			} else if item != nil {
				assetID = item.ID
			}
		}
		return contracts.VideoWatermarkRemoveOutput{
			Video:       engine.RefFor(outPath, engine.MediaVideo),
			Path:        outPath,
			AssetID:     assetID,
			Width:       outMeta.width,
			Height:      outMeta.height,
			DurationSec: outMeta.durationSec,
		}, nil
	})
}

func checkWatermarkFile(ctx context.Context, path string, write bool) error {
	clean := filepath.Clean(strings.TrimSpace(path))
	if clean == "" || !filepath.IsAbs(clean) {
		return apperr.Validation("file path must resolve to an absolute workspace path").
			WithMsg("errors.watermark.invalid_path", nil)
	}
	err := engine.CheckFile(ctx, clean, write)
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	code := apperr.CodeUnauthorized
	var appErr *apperr.Error
	if errors.As(err, &appErr) && appErr.Code != "" {
		code = appErr.Code
	}
	return apperr.New(code, sanitizePathError(clean, err)).
		WithMsg("errors.watermark.access_denied", nil)
}

func isSupportedVideoExt(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".mp4", ".mov", ".webm", ".mkv", ".avi", ".m4v", ".mpeg", ".mpg", ".ts":
		return true
	default:
		return false
	}
}

func resolveVideoInputPath(ctx context.Context, opts Options, raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", apperr.Validation("video_path is required").
			WithMsg("errors.watermark.video_required", nil)
	}
	mediaDir := strings.TrimSpace(resolveMediaDir(opts))
	var roots []string
	if mediaDir != "" {
		roots = append(roots, mediaDir)
	}
	if opts.WorkspaceTreeFunc != nil {
		tree := opts.WorkspaceTreeFunc()
		for _, r := range []string{tree.MediaDir, tree.ProjectsDir, tree.WorkspaceDir} {
			if sr := strings.TrimSpace(r); sr != "" && !slices.Contains(roots, sr) {
				roots = append(roots, sr)
			}
		}
	}

	var candidates []string
	if strings.HasPrefix(filepath.ToSlash(trimmed), "/media/") {
		if mediaDir != "" && filepath.IsAbs(mediaDir) {
			if c := engine.ResolveMediaPath(trimmed, mediaDir); c != "" && filepath.IsAbs(c) {
				candidates = append(candidates, c)
			}
		}
	} else if filepath.IsAbs(filepath.Clean(trimmed)) {
		candidates = append(candidates, filepath.Clean(trimmed))
	} else {
		for _, root := range roots {
			if filepath.IsAbs(root) {
				if c := engine.ResolveMediaPath(trimmed, root); c != "" && filepath.IsAbs(c) {
					candidates = append(candidates, c)
				}
			}
		}
	}
	if len(candidates) == 0 {
		return "", apperr.Validation("video_path is invalid or escapes workspace").
			WithMsg("errors.watermark.invalid_path", nil)
	}

	var lastGuardErr error
	var firstGuarded string
	for _, cand := range candidates {
		if err := checkWatermarkFile(ctx, cand, false); err != nil {
			if lastGuardErr == nil {
				lastGuardErr = err
			}
			continue
		}
		if !isSupportedVideoExt(cand) {
			return "", apperr.Validation("video_path must reference a supported video file").
				WithMsg("errors.watermark.unsupported_video_ext", nil)
		}
		if info, err := os.Stat(cand); err == nil {
			if info.IsDir() {
				return "", apperr.Validation("video_path must reference a regular file, not a directory").
					WithMsg("errors.watermark.input_not_file", nil)
			}
			return cand, nil
		}
		if firstGuarded == "" {
			firstGuarded = cand
		}
	}
	if firstGuarded != "" {
		if _, err := os.Stat(firstGuarded); err != nil {
			return "", apperr.New(apperr.CodeNotFound, sanitizePathError(firstGuarded, err)).
				WithMsg("errors.watermark.video_not_found", nil)
		}
		return firstGuarded, nil
	}
	if lastGuardErr != nil {
		return "", lastGuardErr
	}
	return "", apperr.Validation("video_path is invalid or escapes workspace").
		WithMsg("errors.watermark.invalid_path", nil)
}

func resolveVideoOutputDir(ctx context.Context, opts Options, rawOutDir string) (string, error) {
	mediaDir := strings.TrimSpace(resolveMediaDir(opts))
	outDir := strings.TrimSpace(rawOutDir)
	if outDir == "" {
		outDir = strings.TrimSpace(resolveOutputDir(opts))
	}
	if outDir == "" && mediaDir != "" {
		outDir = filepath.Join(mediaDir, "watermark")
	}
	if outDir == "" {
		return "", apperr.Validation("output_dir or configured workspace media directory is required").
			WithMsg("errors.watermark.output_dir_required", nil)
	}
	var cleanOutDir string
	if filepath.IsAbs(filepath.Clean(outDir)) && !strings.HasPrefix(filepath.ToSlash(outDir), "/media/") {
		cleanOutDir = filepath.Clean(outDir)
	} else {
		anchor := mediaDir
		if anchor == "" && opts.WorkspaceTreeFunc != nil {
			tree := opts.WorkspaceTreeFunc()
			for _, cand := range []string{tree.MediaDir, tree.WorkspaceDir, tree.ProjectsDir} {
				if c := strings.TrimSpace(cand); c != "" && filepath.IsAbs(c) {
					anchor = c
					break
				}
			}
		}
		if anchor == "" || !filepath.IsAbs(anchor) {
			return "", apperr.Validation("relative output_dir requires a configured media directory").
				WithMsg("errors.watermark.output_dir_required", nil)
		}
		cleanOutDir = engine.ResolveMediaPath(outDir, anchor)
	}
	if cleanOutDir == "" || !filepath.IsAbs(cleanOutDir) {
		return "", apperr.Validation("output_dir escapes configured media directory").
			WithMsg("errors.watermark.output_dir_escape", nil)
	}
	if err := checkWatermarkFile(ctx, filepath.Join(cleanOutDir, ".write-check"), true); err != nil {
		return "", err
	}
	return cleanOutDir, nil
}

func buildVideoWatermarkFilter(in contracts.VideoWatermarkRemoveInput, defW, defH, defCrop, maxDim, maxLogoDim, maxCrop int) (string, error) {
	mode := strings.ToLower(strings.TrimSpace(in.Mode))
	if mode == "" {
		mode = "delogo"
	}
	if !slices.Contains(videoRemovalModes, mode) {
		return "", apperr.Validation(fmt.Sprintf("unsupported video watermark mode %q", mode)).
			WithMsg("errors.watermark.invalid_mode", map[string]any{"mode": mode})
	}
	if mode == "crop" {
		if in.CropPx < 0 || in.CropPx > maxCrop {
			return "", apperr.Validation(fmt.Sprintf("crop_px must be between %d and %d", minVideoCropPx, maxCrop)).
				WithMsg("errors.watermark.invalid_crop_px", map[string]any{"min": minVideoCropPx, "max": maxCrop})
		}
		px := in.CropPx
		if px == 0 {
			px = defCrop
		}
		if px < minVideoCropPx {
			return "", apperr.Validation(fmt.Sprintf("crop_px must be >= %d for crop mode", minVideoCropPx)).
				WithMsg("errors.watermark.invalid_crop_px", map[string]any{"min": minVideoCropPx, "max": maxCrop})
		}
		if px%videoCropEvenAlign != 0 {
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
			return "", apperr.Validation("invalid crop_edge: must be bottom, top, left, or right").
				WithMsg("errors.watermark.invalid_crop_edge", nil)
		}
	}

	w := in.Width
	if w == 0 {
		w = defW
	}
	h := in.Height
	if h == 0 {
		h = defH
	}
	if in.X < 0 || in.X > maxDim || in.Y < 0 || in.Y > maxDim {
		return "", apperr.Validation(fmt.Sprintf("delogo x and y must be between 0 and %d", maxDim)).
			WithMsg("errors.watermark.invalid_delogo_bounds", nil)
	}
	if w <= 0 || w > maxLogoDim || h <= 0 || h > maxLogoDim {
		return "", apperr.Validation(fmt.Sprintf("delogo width and height must be between 1 and %d", maxLogoDim)).
			WithMsg("errors.watermark.invalid_delogo_bounds", nil)
	}
	return fmt.Sprintf("delogo=x=%d:y=%d:w=%d:h=%d", in.X, in.Y, w, h), nil
}

type probedVideoMeta struct {
	width       int
	height      int
	durationSec float64
}

func probeVideoMetadata(ctx context.Context, opts Options, vidPath string) probedVideoMeta {
	if opts.Binaries == nil {
		return probedVideoMeta{}
	}
	ffprobeExe, _, _, err := opts.Binaries.Resolve(ctx, domainbinaries.IDFFprobe)
	if err != nil || strings.TrimSpace(ffprobeExe) == "" {
		return probedVideoMeta{}
	}
	timeout := config.ResolveStudioConfig(opts.StudioConfig).FFprobeTimeout
	pCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	stop := progress.KeepAlive(ctx)
	res, err := runner.Run(pCtx, ffprobeExe, "-v", "quiet", "-print_format", "json", "-show_format", "-show_streams", vidPath)
	stop()
	if err != nil || res == nil || res.ExitCode != 0 {
		return probedVideoMeta{}
	}
	var raw struct {
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
		Streams []struct {
			CodecType string `json:"codec_type"`
			Width     int    `json:"width"`
			Height    int    `json:"height"`
			Duration  string `json:"duration"`
		} `json:"streams"`
	}
	if json.Unmarshal([]byte(res.Stdout), &raw) != nil {
		if sec, perr := strconv.ParseFloat(strings.TrimSpace(res.Stdout), 64); perr == nil && sec > 0 {
			return probedVideoMeta{durationSec: sec}
		}
		return probedVideoMeta{}
	}
	var meta probedVideoMeta
	if sec, perr := strconv.ParseFloat(strings.TrimSpace(raw.Format.Duration), 64); perr == nil && sec > 0 {
		meta.durationSec = sec
	}
	for _, st := range raw.Streams {
		if st.CodecType == "video" {
			if meta.width == 0 && st.Width > 0 {
				meta.width = st.Width
				meta.height = st.Height
			}
			if meta.durationSec == 0 {
				if sec, perr := strconv.ParseFloat(strings.TrimSpace(st.Duration), 64); perr == nil && sec > 0 {
					meta.durationSec = sec
				}
			}
		}
	}
	return meta
}
