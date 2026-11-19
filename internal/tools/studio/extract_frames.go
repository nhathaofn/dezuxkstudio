package studio

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"app/internal/apperr"
	"app/internal/config"
	"app/internal/contracts"
	domainbinaries "app/internal/domain/binaries"
	"app/internal/engine"
	"app/internal/progress"
	"app/internal/runner"
)

var extractFrameModes = []string{"last", "first", "uniform", "timestamps"}

// NewExtractFramesNode returns tool.extract_frames for extracting the last, first, or sample frames from video clips.
func NewExtractFramesNode(opts Options) engine.Node {
	opts.StudioConfig = withStudioDefaults(opts.StudioConfig)
	defCount := opts.StudioConfig.ExtractFrameDefaultCount
	maxCount := opts.StudioConfig.ExtractFrameMaxCount

	spec := engine.Spec{
		Type:        NodeTypeExtractFrames,
		Provider:    studioProviderID,
		Label:       "Extract Video Frames",
		Description: "Extracts the last frame (for shot continuity keyframes), first frame, or evenly spaced timestamps from an MP4 clip using FFmpeg.",
		Inputs: []engine.Port{
			{Name: "video_path", Label: "Source Video", Type: engine.PortVideo},
		},
		Outputs: []engine.Port{
			{Name: "last_frame", Label: "Extracted Frame", Type: engine.PortImage},
		},
		Params: []engine.Param{
			{Name: "mode", Label: "Extraction Mode", Kind: engine.ParamSelect, Options: extractFrameModes, Default: "last"},
			{Name: "count", Label: "Frame Count (uniform)", Kind: engine.ParamInteger, Min: 1, Max: maxCount, Default: strconv.Itoa(defCount)},
			{Name: "timestamps_csv", Label: "Timestamps CSV (sec)", Kind: engine.ParamText},
			{Name: "output_dir", Label: "Output Folder", Kind: engine.ParamText, Role: engine.ParamRoleOutputDir},
		},
	}

	return engine.Typed(spec, func(ctx context.Context, in contracts.ExtractFramesInput) (contracts.ExtractFramesOutput, error) {
		vidPath, err := resolveMediaInput(ctx, opts, in.VideoPath.LocalURL, inputKindVideo)
		if err != nil {
			return contracts.ExtractFramesOutput{}, err
		}
		if vidPath == "" {
			return contracts.ExtractFramesOutput{}, apperr.Validation("video_path is required").
				WithMsg("errors.studio.video_required", nil)
		}

		mode := strings.ToLower(strings.TrimSpace(in.Mode))
		if mode == "" {
			mode = "last"
		}
		if !slices.Contains(extractFrameModes, mode) {
			return contracts.ExtractFramesOutput{}, apperr.Validation(fmt.Sprintf("unsupported extract_frames mode %q", mode)).
				WithMsg("errors.studio.invalid_mode", map[string]any{"mode": mode})
		}
		if in.Count < 0 || in.Count > maxCount {
			return contracts.ExtractFramesOutput{}, apperr.Validation(fmt.Sprintf("count must be between 1 and %d", maxCount)).
				WithMsg("errors.studio.invalid_frame_count", map[string]any{"min": 1, "max": maxCount})
		}
		if len(in.TimestampsSec) > maxCount {
			return contracts.ExtractFramesOutput{}, apperr.Validation(fmt.Sprintf("timestamps_sec cannot exceed %d entries", maxCount)).
				WithMsg("errors.studio.invalid_frame_count", map[string]any{"min": 1, "max": maxCount})
		}
		for _, ts := range in.TimestampsSec {
			if math.IsNaN(ts) || math.IsInf(ts, 0) {
				return contracts.ExtractFramesOutput{}, apperr.Validation("timestamps_sec must contain finite numbers").
					WithMsg("errors.studio.invalid_timestamp", nil)
			}
		}

		ffmpegExe, err := resolveBinary(ctx, opts.Binaries, domainbinaries.IDFFmpeg)
		if err != nil {
			return contracts.ExtractFramesOutput{}, err
		}
		ffprobeExe, err := resolveBinary(ctx, opts.Binaries, domainbinaries.IDFFprobe)
		if err != nil {
			return contracts.ExtractFramesOutput{}, err
		}
		outDir, err := resolveNodeOutputDir(ctx, opts, in.OutputDir, filepath.Join("studio", "frames"))
		if err != nil {
			return contracts.ExtractFramesOutput{}, err
		}

		probeCtx, cancelProbe := context.WithTimeout(ctx, opts.StudioConfig.FFprobeTimeout)
		stopProbe := progress.KeepAlive(ctx)
		info, err := probeWithFFprobe(probeCtx, ffprobeExe, vidPath)
		stopProbe()
		cancelProbe()
		if err != nil {
			return contracts.ExtractFramesOutput{}, err
		}

		timestamps, err := resolveFrameTimestamps(in, info.DurationSec, opts.StudioConfig)
		if err != nil {
			return contracts.ExtractFramesOutput{}, err
		}
		out := contracts.ExtractFramesOutput{
			Frames:    make([]contracts.ExtractedFrame, 0, len(timestamps)),
			OutputDir: outDir,
		}

		ffCtx, cancelFF := context.WithTimeout(ctx, opts.StudioConfig.FFmpegTimeout)
		defer cancelFF()

		type stagedFrame struct {
			tmpPath string
			outPath string
			tsSec   float64
		}
		total := len(timestamps)
		staged := make([]stagedFrame, 0, total)
		defer func() {
			for _, sf := range staged {
				if sf.tmpPath != "" {
					_ = os.Remove(sf.tmpPath)
				}
			}
		}()

		for i, ts := range timestamps {
			if err := ctx.Err(); err != nil {
				return contracts.ExtractFramesOutput{}, err
			}
			progress.Report(ctx, progress.Update{
				Percent: float64(i) / float64(max(1, total)) * 90.0,
				Message: fmt.Sprintf("Extracting frame %d/%d…", i+1, total),
				Key:     "studio.progress.extract_frames",
				Params: map[string]string{
					"current": strconv.Itoa(i + 1),
					"total":   strconv.Itoa(total),
				},
			})
			outPath := filepath.Join(outDir, uniqueExportFilename(fmt.Sprintf("frame_%02d", i+1), ".png"))
			if err := checkStudioFile(ctx, outPath, true); err != nil {
				return contracts.ExtractFramesOutput{}, err
			}
			tmpOut, terr := createTempMediaFile(outDir, ".tmp-frame-", ".png")
			if terr != nil {
				return contracts.ExtractFramesOutput{}, terr
			}
			var args []string
			if ts < 0 {
				args = []string{"-y", "-sseof", fmt.Sprintf("-%.2f", opts.StudioConfig.ExtractFrameEOFOffsetSec), "-i", vidPath, "-frames:v", "1", tmpOut}
			} else {
				args = []string{"-y", "-ss", fmt.Sprintf("%.3f", ts), "-i", vidPath, "-frames:v", "1", tmpOut}
			}
			stopRun := progress.KeepAlive(ctx)
			res, runErr := runner.Run(ffCtx, ffmpegExe, args...)
			stopRun()
			if runErr != nil || res == nil || res.ExitCode != 0 {
				_ = os.Remove(tmpOut)
				if ctx.Err() != nil {
					return contracts.ExtractFramesOutput{}, ctx.Err()
				}
				return contracts.ExtractFramesOutput{}, ioUnavailableErr("FFmpeg failed to extract video frame", runErr)
			}
			staged = append(staged, stagedFrame{
				tmpPath: tmpOut,
				outPath: outPath,
				tsSec:   math.Max(0, ts),
			})
		}

		if err := ctx.Err(); err != nil {
			return contracts.ExtractFramesOutput{}, err
		}
		for i := range staged {
			sf := &staged[i]
			if cerr := commitTempMediaFile(sf.tmpPath, sf.outPath); cerr != nil {
				return contracts.ExtractFramesOutput{}, cerr
			}
			sf.tmpPath = ""
			assetID := registerStudioAsset(ctx, opts, "image", NodeTypeExtractFrames, sf.outPath, outDir)
			out.Frames = append(out.Frames, contracts.ExtractedFrame{
				Path:         sf.outPath,
				TimestampSec: sf.tsSec,
				Width:        info.Width,
				Height:       info.Height,
				AssetID:      assetID,
			})
		}
		if len(out.Frames) > 0 {
			lastPath := out.Frames[len(out.Frames)-1].Path
			out.LastFramePath = lastPath
			out.LastFrame = engine.RefFor(lastPath, engine.MediaImage)
		}
		progress.Report(ctx, progress.Update{Percent: 100, Message: "Frames extracted", Key: "studio.progress.done"})
		return out, nil
	})
}

func resolveFrameTimestamps(in contracts.ExtractFramesInput, dur float64, sCfg config.StudioConfig) ([]float64, error) {
	sCfg = withStudioDefaults(sCfg)
	endPad := sCfg.ExtractFrameEndPadSec
	lastPad := sCfg.ExtractFrameLastPadSec
	defCount := sCfg.ExtractFrameDefaultCount
	maxCount := sCfg.ExtractFrameMaxCount

	mode := strings.ToLower(strings.TrimSpace(in.Mode))
	if mode == "" {
		mode = "last"
	}
	switch mode {
	case "first":
		return []float64{0}, nil
	case "timestamps":
		rawStamps := append([]float64(nil), in.TimestampsSec...)
		if len(rawStamps) == 0 && strings.TrimSpace(in.TimestampsCSV) != "" {
			for _, part := range strings.Split(in.TimestampsCSV, ",") {
				trimmed := strings.TrimSpace(part)
				if trimmed == "" {
					continue
				}
				v, err := strconv.ParseFloat(trimmed, 64)
				if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
					return nil, apperr.Validation("timestamps_csv contains an invalid timestamp").
						WithMsg("errors.studio.invalid_timestamp", nil)
				}
				rawStamps = append(rawStamps, v)
			}
		}
		if len(rawStamps) > maxCount {
			return nil, apperr.Validation(fmt.Sprintf("timestamps cannot exceed %d entries", maxCount)).
				WithMsg("errors.studio.invalid_frame_count", map[string]any{"min": 1, "max": maxCount})
		}
		if len(rawStamps) == 0 {
			return []float64{0}, nil
		}
		out := make([]float64, 0, len(rawStamps))
		for _, t := range rawStamps {
			if t < 0 {
				t = 0
			}
			if dur > endPad && t > dur-endPad {
				t = dur - endPad
			}
			out = append(out, t)
		}
		return out, nil
	case "uniform":
		count := in.Count
		if count == 0 {
			count = defCount
		}
		if dur <= endPad*2 {
			return []float64{0}, nil
		}
		out := make([]float64, 0, count)
		step := dur / float64(count+1)
		for i := 1; i <= count; i++ {
			out = append(out, math.Min(dur-endPad, step*float64(i)))
		}
		return out, nil
	default: // "last"
		if dur <= 0 {
			return []float64{-1}, nil
		}
		ts := dur - lastPad
		if ts < 0 {
			ts = 0
		}
		return []float64{ts}, nil
	}
}
