package studio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
)

var silenceDurationRe = regexp.MustCompile(`silence_duration:\s*([0-9]+(?:\.[0-9]+)?)`)

type ffprobeRaw struct {
	Format struct {
		FormatName string `json:"format_name"`
		Duration   string `json:"duration"`
	} `json:"format"`
	Streams []struct {
		CodecType    string `json:"codec_type"`
		CodecName    string `json:"codec_name"`
		Width        int    `json:"width"`
		Height       int    `json:"height"`
		RFrameRate   string `json:"r_frame_rate"`
		AvgFrameRate string `json:"avg_frame_rate"`
		Duration     string `json:"duration"`
	} `json:"streams"`
}

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
				out.SilentRatio = detectSilenceRatio(silCtx, ffmpegExe, resolvedPath, out.DurationSec, opts.StudioConfig)
				stopSil()
				cancelSil()
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
	args := []string{
		"-v", "quiet",
		"-print_format", "json",
		"-show_format",
		"-show_streams",
		filePath,
	}
	res, err := runner.RunWithOptions(ctx, ffprobeExe, args, runner.RunOptions{})
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return contracts.ProbeMediaOutput{}, err
		}
		return contracts.ProbeMediaOutput{}, apperr.Wrap(apperr.CodeIntegrity, "Media file could not be inspected or is corrupted", err).
			WithMsg("errors.studio.media_corrupt", nil)
	}

	var raw ffprobeRaw
	if err := json.Unmarshal([]byte(res.Stdout), &raw); err != nil {
		return contracts.ProbeMediaOutput{}, apperr.Wrap(apperr.CodeIntegrity, "Media probe output was unreadable or corrupted", err).
			WithMsg("errors.studio.media_corrupt", nil)
	}

	out := contracts.ProbeMediaOutput{
		FormatName: raw.Format.FormatName,
	}
	if d, err := strconv.ParseFloat(strings.TrimSpace(raw.Format.Duration), 64); err == nil && d > 0 {
		out.DurationSec = d
	}

	for _, st := range raw.Streams {
		switch st.CodecType {
		case "video":
			if out.VideoCodec == "" {
				out.VideoCodec = st.CodecName
			}
			if out.Width == 0 && st.Width > 0 {
				out.Width = st.Width
				out.Height = st.Height
			}
			if out.FPS == 0 {
				out.FPS = parseFFprobeRate(st.AvgFrameRate)
				if out.FPS == 0 {
					out.FPS = parseFFprobeRate(st.RFrameRate)
				}
			}
			if out.DurationSec == 0 {
				if d, err := strconv.ParseFloat(strings.TrimSpace(st.Duration), 64); err == nil && d > 0 {
					out.DurationSec = d
				}
			}
		case "audio":
			out.HasAudio = true
			if out.AudioCodec == "" {
				out.AudioCodec = st.CodecName
			}
			if out.DurationSec == 0 {
				if d, err := strconv.ParseFloat(strings.TrimSpace(st.Duration), 64); err == nil && d > 0 {
					out.DurationSec = d
				}
			}
		}
	}
	if len(raw.Streams) == 0 && out.FormatName == "" && out.DurationSec == 0 {
		return contracts.ProbeMediaOutput{}, apperr.New(apperr.CodeIntegrity, "Media file contains no readable streams").
			WithMsg("errors.studio.media_corrupt", nil)
	}
	out.ProbeJSON = marshalProbeSummary(out)
	return out, nil
}

func detectSilenceRatio(ctx context.Context, ffmpegExe, filePath string, totalDur float64, cfg ...config.StudioConfig) float64 {
	if totalDur <= 0 {
		return 0
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
	if err != nil || res == nil {
		return 0
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
		return 0
	}
	if ratio > 1 {
		return 1
	}
	return ratio
}

func parseFFprobeRate(raw string) float64 {
	s := strings.TrimSpace(raw)
	if s == "" || s == "0/0" {
		return 0
	}
	if numStr, denStr, ok := strings.Cut(s, "/"); ok {
		num, err1 := strconv.ParseFloat(numStr, 64)
		den, err2 := strconv.ParseFloat(denStr, 64)
		if err1 == nil && err2 == nil && den > 0 {
			return num / den
		}
		return 0
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || f <= 0 {
		return 0
	}
	return f
}
