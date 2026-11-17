package studio

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"path/filepath"
	"strconv"
	"strings"

	"app/internal/apperr"
	"app/internal/contracts"
	domainbinaries "app/internal/domain/binaries"
	"app/internal/engine"
	"app/internal/progress"
	"app/internal/runner"
)

type extractFramesNode struct {
	opts Options
}

// NewExtractFramesNode returns tool.extract_frames for extracting the last, first, or sample frames from video clips.
func NewExtractFramesNode(opts Options) engine.Node {
	opts.StudioConfig = withStudioDefaults(opts.StudioConfig)
	return &extractFramesNode{opts: opts}
}

func (n *extractFramesNode) Spec() engine.Spec {
	return engine.Spec{
		Type:        NodeTypeExtractFrames,
		Provider:    "studio",
		Label:       "Extract Video Frames",
		Description: "Extracts the last frame (for shot continuity keyframes), first frame, or evenly spaced timestamps from an MP4 clip using FFmpeg.",
		Inputs: []engine.Port{
			{Name: "video_path", Label: "Source Video", Type: engine.PortVideo},
		},
		Outputs: []engine.Port{
			{Name: "last_frame", Label: "Extracted Frame", Type: engine.PortImage},
		},
		Params: []engine.Param{
			{Name: "mode", Label: "Extraction Mode", Kind: engine.ParamSelect, Options: []string{"last", "first", "uniform", "timestamps"}, Default: "last"},
			{Name: "count", Label: "Frame Count (uniform)", Kind: engine.ParamInteger, Min: 1, Max: 24, Default: "4"},
			{Name: "timestamps_csv", Label: "Timestamps CSV (sec)", Kind: engine.ParamText},
			{Name: "output_dir", Label: "Output Folder", Kind: engine.ParamText, Role: engine.ParamRoleOutputDir},
		},
	}
}

func (n *extractFramesNode) Run(ctx context.Context, raw json.RawMessage) (any, error) {
	var in contracts.ExtractFramesInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, apperr.Validation("invalid extract_frames input").WithDetails(map[string]any{"cause": err.Error()})
	}
	vidPath, err := resolveInputFile(ctx, n.opts, in.VideoPath)
	if err != nil {
		return nil, err
	}
	if vidPath == "" {
		return nil, apperr.Validation("video_path is required")
	}

	ffmpegExe, err := resolveBinary(ctx, n.opts.Binaries, domainbinaries.IDFFmpeg)
	if err != nil {
		return nil, err
	}
	ffprobeExe, err := resolveBinary(ctx, n.opts.Binaries, domainbinaries.IDFFprobe)
	if err != nil {
		return nil, err
	}
	outDir, err := resolveNodeOutputDir(ctx, n.opts, in.OutputDir, filepath.Join("studio", "frames"))
	if err != nil {
		return nil, err
	}

	probeCtx, cancelProbe := context.WithTimeout(ctx, n.opts.StudioConfig.FFprobeTimeout)
	defer cancelProbe()
	info, err := probeWithFFprobe(probeCtx, ffprobeExe, vidPath)
	if err != nil {
		return nil, err
	}

	timestamps := resolveFrameTimestamps(in, info.DurationSec)
	out := contracts.ExtractFramesOutput{
		Frames:    make([]contracts.ExtractedFrame, 0, len(timestamps)),
		OutputDir: outDir,
	}

	ffCtx, cancelFF := context.WithTimeout(ctx, n.opts.StudioConfig.FFmpegTimeout)
	defer cancelFF()

	for i, ts := range timestamps {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		progress.Report(ctx, progress.Update{
			Percent: float64(i) / float64(max(1, len(timestamps))) * 90.0,
			Message: fmt.Sprintf("Extracting frame %d/%d…", i+1, len(timestamps)),
			Key:     "studio.progress.extract_frames",
		})
		outPath := filepath.Join(outDir, uniqueExportFilename(fmt.Sprintf("frame_%02d", i+1), ".png"))
		if err := engine.CheckFile(ctx, outPath, true); err != nil {
			return nil, err
		}
		var args []string
		if ts < 0 {
			// Fallback when duration metadata was unavailable: seek near EOF directly
			args = []string{"-y", "-sseof", "-0.1", "-i", vidPath, "-frames:v", "1", "-q:v", "2", outPath}
		} else {
			args = []string{"-y", "-ss", fmt.Sprintf("%.3f", ts), "-i", vidPath, "-frames:v", "1", "-q:v", "2", outPath}
		}
		res, runErr := runner.Run(ffCtx, ffmpegExe, args...)
		if runErr != nil {
			return nil, apperr.Wrap(apperr.CodeUnavailable, "FFmpeg extract_frames failed: "+resStderr(res), runErr)
		}
		if res.ExitCode != 0 {
			return nil, apperr.Unavailable("FFmpeg extract_frames failed: " + resStderr(res))
		}
		assetID := registerStudioAsset(ctx, n.opts, "image", NodeTypeExtractFrames, outPath, outDir)
		out.Frames = append(out.Frames, contracts.ExtractedFrame{
			Path:         outPath,
			TimestampSec: math.Max(0, ts),
			Width:        info.Width,
			Height:       info.Height,
			AssetID:      assetID,
		})
	}
	if len(out.Frames) > 0 {
		out.LastFramePath = out.Frames[len(out.Frames)-1].Path
	}
	progress.Report(ctx, progress.Update{Percent: 100, Message: "Frames extracted", Key: "studio.progress.done"})
	return out, nil
}

func resolveFrameTimestamps(in contracts.ExtractFramesInput, dur float64) []float64 {
	mode := strings.ToLower(strings.TrimSpace(in.Mode))
	if mode == "" {
		mode = "last"
	}
	switch mode {
	case "first":
		return []float64{0}
	case "timestamps":
		rawStamps := append([]float64(nil), in.TimestampsSec...)
		if len(rawStamps) == 0 && strings.TrimSpace(in.TimestampsCSV) != "" {
			for _, part := range strings.Split(in.TimestampsCSV, ",") {
				if v, err := strconv.ParseFloat(strings.TrimSpace(part), 64); err == nil {
					rawStamps = append(rawStamps, v)
				}
			}
		}
		if len(rawStamps) == 0 {
			return []float64{0}
		}
		out := make([]float64, 0, len(rawStamps))
		for _, t := range rawStamps {
			if t < 0 {
				t = 0
			}
			if dur > 0.05 && t > dur-0.05 {
				t = dur - 0.05
			}
			out = append(out, t)
		}
		return out
	case "uniform":
		count := clampInt(in.Count, 4, 1, 24)
		if dur <= 0.1 {
			return []float64{0}
		}
		out := make([]float64, 0, count)
		step := dur / float64(count+1)
		for i := 1; i <= count; i++ {
			out = append(out, math.Min(dur-0.05, step*float64(i)))
		}
		return out
	default: // "last"
		if dur <= 0 {
			return []float64{-1}
		}
		ts := dur - 0.08
		if ts < 0 {
			ts = 0
		}
		return []float64{ts}
	}
}
