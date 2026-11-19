package studio

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"app/internal/apperr"
	"app/internal/config"
	"app/internal/contracts"
	domainbinaries "app/internal/domain/binaries"
	"app/internal/engine"
	"app/internal/progress"
	"app/internal/runner"
	"app/internal/tools/mediautil"
)

var silenceDurationRe = regexp.MustCompile(`silence_duration:\s*([0-9]+(?:\.[0-9]+)?)`)

// NewProbeMediaNode builds the tool.probe_media workflow node backed by binaries.Service (IDFFprobe).
func NewProbeMediaNode(opts Options) engine.Node {
	opts.StudioConfig = withStudioDefaults(opts.StudioConfig)
	spec := engine.Spec{
		Type:        NodeTypeProbeMedia,
		Provider:    studioProviderID,
		Label:       "Media Inspector (FFprobe)",
		Description: "Inspect duration, dimensions, frame rate, codecs, audio presence, and silence ratio of a video, audio, or image file via FFprobe",
		Inputs: []engine.Port{
			{Name: "media", Label: "Video or image file", Type: engine.PortMedia, Optional: true},
			{Name: "audio", Label: "Audio file", Type: engine.PortAudio, Optional: true},
		},
		Outputs: []engine.Port{
			{Name: "probe_json", Label: "Media probe JSON", Type: engine.PortText},
		},
	}

	return engine.Typed(spec, func(ctx context.Context, in contracts.ProbeMediaInput) (contracts.ProbeMediaOutput, error) {
		rawTarget := strings.TrimSpace(in.Media.LocalURL)
		allowed := inputKindAnyMedia
		if rawTarget == "" {
			rawTarget = strings.TrimSpace(in.Audio.LocalURL)
			allowed = inputKindAudio
		}
		if rawTarget == "" {
			return contracts.ProbeMediaOutput{}, apperr.Validation("media or audio input path is required").
				WithMsg("errors.studio.media_required", nil)
		}

		resolvedPath, err := resolveMediaInput(ctx, opts, rawTarget, allowed)
		if err != nil {
			return contracts.ProbeMediaOutput{}, err
		}

		ffprobeExe, err := resolveBinary(ctx, opts.Binaries, domainbinaries.IDFFprobe)
		if err != nil {
			return contracts.ProbeMediaOutput{}, err
		}

		progress.Report(ctx, progress.Update{
			Percent: 15,
			Message: "Inspecting media streams…",
			Key:     "studio.progress.probe_media",
		})

		probeCtx, cancelProbe := context.WithTimeout(ctx, opts.StudioConfig.FFprobeTimeout)
		stopProbe := progress.KeepAlive(ctx)
		out, err := probeWithFFprobe(probeCtx, ffprobeExe, resolvedPath)
		stopProbe()
		cancelProbe()
		if err != nil {
			return contracts.ProbeMediaOutput{}, err
		}

		if !out.HasAudio {
			out.SilentRatio = 1.0
		} else if out.DurationSec > 0 {
			if ffmpegExe, ferr := resolveBinary(ctx, opts.Binaries, domainbinaries.IDFFmpeg); ferr == nil {
				silCtx, cancelSil := context.WithTimeout(ctx, opts.StudioConfig.FFmpegTimeout)
				stopSil := progress.KeepAlive(ctx)
				ratio, sErr := detectSilenceRatio(silCtx, ffmpegExe, resolvedPath, out.DurationSec, opts.StudioConfig)
				stopSil()
				cancelSil()
				if sErr != nil {
					if ctx.Err() != nil {
						return contracts.ProbeMediaOutput{}, sErr
					}
					msg := fmt.Sprintf("Audio silence detection unavailable: %v", sErr)
					out.Warnings = append(out.Warnings, msg)
					if opts.Logger != nil {
						opts.Logger.WarnContext(ctx, "studio: silencedetect failed during probe", "path", filepath.Base(resolvedPath), "error", sErr)
					}
				}
				out.SilentRatio = ratio
			}
		}

		out.ProbeJSON = marshalProbeSummary(out)
		progress.Report(ctx, progress.Update{
			Percent: 100,
			Message: "Media inspection complete",
			Key:     "studio.progress.done",
		})
		return out, nil
	})
}

func marshalProbeSummary(out contracts.ProbeMediaOutput) string {
	b, err := json.Marshal(map[string]any{
		"duration_sec": out.DurationSec,
		"width":        out.Width,
		"height":       out.Height,
		"fps":          out.FPS,
		"has_audio":    out.HasAudio,
		"silent_ratio": out.SilentRatio,
		"video_codec":  out.VideoCodec,
		"audio_codec":  out.AudioCodec,
		"format_name":  out.FormatName,
	})
	if err != nil {
		return "{}"
	}
	return string(b)
}

func probeWithFFprobe(ctx context.Context, ffprobeExe, filePath string) (contracts.ProbeMediaOutput, error) {
	info, err := mediautil.ProbeFile(ctx, ffprobeExe, filePath)
	if err != nil {
		return contracts.ProbeMediaOutput{}, err
	}
	out := contracts.ProbeMediaOutput{
		FormatName:  info.FormatName,
		VideoCodec:  info.VideoCodec,
		AudioCodec:  info.AudioCodec,
		HasAudio:    info.HasAudio,
		Width:       info.Width,
		Height:      info.Height,
		FPS:         info.FPS,
		DurationSec: info.DurationSec,
	}
	out.ProbeJSON = marshalProbeSummary(out)
	return out, nil
}

func detectSilenceRatio(ctx context.Context, ffmpegExe, filePath string, totalDur float64, cfg ...config.StudioConfig) (float64, error) {
	if totalDur <= 0 {
		return 0, nil
	}
	var rawCfg config.StudioConfig
	if len(cfg) > 0 {
		rawCfg = cfg[0]
	}
	sCfg := withStudioDefaults(rawCfg)
	filter := fmt.Sprintf("silencedetect=noise=%s:d=%.2f", sCfg.SilenceDetectNoiseDB, sCfg.SilenceDetectMinSec)
	args := []string{
		"-hide_banner",
		"-nostats",
		"-i", filePath,
		"-af", filter,
		"-f", "null",
		"-",
	}
	res, err := runner.RunWithOptions(ctx, ffmpegExe, args, runner.RunOptions{})
	if err != nil {
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		return 0, apperr.Wrap(apperr.CodeUnavailable, "FFmpeg silencedetect failed", err)
	}
	if res == nil || res.ExitCode != 0 {
		return 0, apperr.Unavailable("FFmpeg silencedetect exited non-zero")
	}
	var silentTotal float64
	for _, m := range silenceDurationRe.FindAllStringSubmatch(res.Stderr, -1) {
		if len(m) > 1 {
			if d, perr := strconv.ParseFloat(m[1], 64); perr == nil && d > 0 {
				silentTotal += d
			}
		}
	}
	ratio := silentTotal / totalDur
	if ratio < 0 {
		return 0, nil
	}
	if ratio > 1 {
		return 1, nil
	}
	return ratio, nil
}
