package studio

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"app/internal/apperr"
	"app/internal/config"
	"app/internal/contracts"
	domainbinaries "app/internal/domain/binaries"
	domainmedia "app/internal/domain/mediagen"
	"app/internal/engine"
	"app/internal/progress"
	"app/internal/runner"
)

const (
	// maxDubSpeedUpRatio caps voiceover time-compression to 4x so speech remains intelligible.
	maxDubSpeedUpRatio = 4.0

	// maxDubOriginalVolume caps original-track gain to 2.0 (200% linear amplitude, matching timeline clip max).
	maxDubOriginalVolume = 2.0

	// atempoMaxFactor and atempoMinFactor are FFmpeg's documented per-stage `atempo` filter bounds [0.5, 2.0].
	atempoMaxFactor = 2.0
	atempoMinFactor = 0.5

	// duckCompRatio, duckCompAttackMs, and duckCompReleaseMs are standard broadcast dialogue sidechain-ducking envelope constants.
	duckCompRatio     = 6
	duckCompAttackMs  = 20
	duckCompReleaseMs = 250

	// microsecondsPerSecond converts seconds to FFmpeg microsecond duration units.
	microsecondsPerSecond = 1_000_000.0
)

var dubOverflowModes = []string{"freeze_last_frame", "speed_up", "trim"}

// NewTTSDubMuxNode returns tool.tts_dub_mux for synthesizing dub voiceover (when needed) and muxing it onto video with overflow & ducking control.
func NewTTSDubMuxNode(opts Options) engine.Node {
	opts.StudioConfig = withStudioDefaults(opts.StudioConfig)

	spec := engine.Spec{
		Type:        NodeTypeTTSDubMux,
		Provider:    studioProviderID,
		Label:       "TTS Dubbing & Audio Mux",
		Description: "Synthesizes dialogue/narration audio (or uses an existing audio file) and muxes it onto a video shot with overflow alignment and optional original audio ducking.",
		Inputs: []engine.Port{
			{Name: "video_path", Label: "Video Clip", Type: engine.PortVideo},
			{Name: "audio_path", Label: "Dub Audio", Type: engine.PortAudio, Optional: true},
		},
		Outputs: []engine.Port{
			{Name: "video", Label: "Dubbed Video", Type: engine.PortVideo},
		},
		Params: []engine.Param{
			{Name: "text", Label: "Dialogue / Narration Text", Kind: engine.ParamText},
			{Name: "model_ref", Label: "TTS Model", Kind: engine.ParamText},
			{Name: "voice", Label: "Voice ID", Kind: engine.ParamText},
			{Name: "language", Label: "Language", Kind: engine.ParamText},
			{Name: "overflow_mode", Label: "Audio Overflow Mode", Kind: engine.ParamSelect, Options: dubOverflowModes, Default: "freeze_last_frame"},
			{Name: "duck_original", Label: "Duck Original Audio", Kind: engine.ParamToggle, Default: "true"},
			{Name: "original_volume", Label: "Original Audio Volume (0..2)", Kind: engine.ParamText, Default: fmt.Sprintf("%.2f", opts.StudioConfig.TimelineAudioDuckOrigVol)},
			{Name: "output_dir", Label: "Output Folder", Kind: engine.ParamText, Role: engine.ParamRoleOutputDir},
		},
	}

	return engine.Typed(spec, func(ctx context.Context, in contracts.TTSDubMuxInput) (contracts.TTSDubMuxOutput, error) {
		vidPath, err := resolveMediaInput(ctx, opts, in.VideoPath.LocalURL, inputKindVideo)
		if err != nil {
			return contracts.TTSDubMuxOutput{}, err
		}
		if vidPath == "" {
			return contracts.TTSDubMuxOutput{}, apperr.Validation("video_path is required").
				WithMsg("errors.studio.video_required", nil)
		}

		mode := strings.ToLower(strings.TrimSpace(in.OverflowMode))
		if mode == "" {
			mode = "freeze_last_frame"
		}
		if !slices.Contains(dubOverflowModes, mode) {
			return contracts.TTSDubMuxOutput{}, apperr.Validation(fmt.Sprintf("unsupported overflow_mode %q", mode)).
				WithMsg("errors.studio.invalid_overflow_mode", map[string]any{"mode": mode})
		}
		if math.IsNaN(in.OriginalVolume) || math.IsInf(in.OriginalVolume, 0) || in.OriginalVolume < 0 || in.OriginalVolume > maxDubOriginalVolume {
			return contracts.TTSDubMuxOutput{}, apperr.Validation("original_volume must be between 0 and 2").
				WithMsg("errors.studio.invalid_volume", nil)
		}

		outDir, err := resolveNodeOutputDir(ctx, opts, in.OutputDir, filepath.Join("studio", "dubs"))
		if err != nil {
			return contracts.TTSDubMuxOutput{}, err
		}

		audioPath, err := resolveMediaInput(ctx, opts, in.AudioPath.LocalURL, inputKindAudio)
		if err != nil {
			return contracts.TTSDubMuxOutput{}, err
		}
		if audioPath == "" {
			text := strings.TrimSpace(in.Text)
			if text == "" {
				return contracts.TTSDubMuxOutput{}, apperr.Validation("either audio_path or text is required for tts_dub_mux").
					WithMsg("errors.studio.dub_audio_or_text_required", nil)
			}
			if opts.TTSGen == nil {
				return contracts.TTSDubMuxOutput{}, apperr.Unavailable("TTS generator service is unavailable").
					WithMsg("errors.studio.tts_service_unavailable", nil)
			}
			progress.Report(ctx, progress.Update{
				Percent: 5,
				Message: "Synthesizing dub voiceover…",
				Key:     "studio.progress.tts_synthesize",
			})
			ttsRes, err := opts.TTSGen.GenerateTTS(ctx, domainmedia.TTSRequest{
				ModelRef:  in.ModelRef,
				Text:      text,
				Voice:     in.Voice,
				Language:  in.Language,
				OutputDir: outDir,
			})
			if err != nil {
				return contracts.TTSDubMuxOutput{}, err
			}
			audioPath = ttsRes.Path
		}

		ffmpegExe, err := resolveBinary(ctx, opts.Binaries, domainbinaries.IDFFmpeg)
		if err != nil {
			return contracts.TTSDubMuxOutput{}, err
		}
		ffprobeExe, err := resolveBinary(ctx, opts.Binaries, domainbinaries.IDFFprobe)
		if err != nil {
			return contracts.TTSDubMuxOutput{}, err
		}

		probeCtx, cancelProbe := context.WithTimeout(ctx, opts.StudioConfig.FFprobeTimeout)
		stopProbe := progress.KeepAlive(ctx)
		vInfo, err := probeWithFFprobe(probeCtx, ffprobeExe, vidPath)
		if err == nil {
			var aInfo contracts.ProbeMediaOutput
			aInfo, err = probeWithFFprobe(probeCtx, ffprobeExe, audioPath)
			stopProbe()
			cancelProbe()
			if err != nil {
				return contracts.TTSDubMuxOutput{}, err
			}
			return runDubMuxEncode(ctx, opts, in, mode, ffmpegExe, vidPath, audioPath, outDir, vInfo, aInfo)
		}
		stopProbe()
		cancelProbe()
		return contracts.TTSDubMuxOutput{}, err
	})
}

func runDubMuxEncode(
	ctx context.Context,
	opts Options,
	in contracts.TTSDubMuxInput,
	mode, ffmpegExe, vidPath, audioPath, outDir string,
	vInfo, aInfo contracts.ProbeMediaOutput,
) (contracts.TTSDubMuxOutput, error) {
	outPath := filepath.Join(outDir, uniqueExportFilename("dubbed", ".mp4"))
	if err := checkStudioFile(ctx, outPath, true); err != nil {
		return contracts.TTSDubMuxOutput{}, err
	}
	tmpOut, err := createTempMediaFile(outDir, ".tmp-dub-", ".mp4")
	if err != nil {
		return contracts.TTSDubMuxOutput{}, err
	}

	in.OverflowMode = mode
	args, targetDur, adjRatio := buildDubMuxArgs(in, opts.StudioConfig, vidPath, audioPath, tmpOut, vInfo, aInfo)

	ffCtx, cancelFF := context.WithTimeout(ctx, opts.StudioConfig.FFmpegTimeout)
	defer cancelFF()
	progress.Report(ctx, progress.Update{Percent: 15, Message: "Muxing dubbed audio onto video…", Key: "studio.progress.tts_dub_mux"})
	runOpts := runner.FFmpegPipeProgressOptions(ctx, int64(targetDur*microsecondsPerSecond), "studio.progress.tts_dub_mux", "Muxing dubbed audio")
	res, err := runner.RunWithOptions(ffCtx, ffmpegExe, args, runOpts)
	if err != nil || res == nil || res.ExitCode != 0 {
		_ = os.Remove(tmpOut)
		if ctx.Err() != nil {
			return contracts.TTSDubMuxOutput{}, ctx.Err()
		}
		return contracts.TTSDubMuxOutput{}, ioUnavailableErr("FFmpeg failed to mux dubbed audio onto video", err)
	}
	if cerr := commitTempMediaFile(tmpOut, outPath); cerr != nil {
		return contracts.TTSDubMuxOutput{}, cerr
	}
	progress.Report(ctx, progress.Update{Percent: 100, Message: "Dubbing & mux ready", Key: "studio.progress.done"})

	assetID := registerStudioAsset(ctx, opts, "video", NodeTypeTTSDubMux, outPath, outDir)
	return contracts.TTSDubMuxOutput{
		Video:         engine.RefFor(outPath, engine.MediaVideo),
		Path:          outPath,
		AudioPath:     audioPath,
		AssetID:       assetID,
		DurationSec:   targetDur,
		AdjustedRatio: adjRatio,
	}, nil
}

func buildDubMuxArgs(
	in contracts.TTSDubMuxInput,
	cfg config.StudioConfig,
	vidPath, audioPath, outPath string,
	vInfo, aInfo contracts.ProbeMediaOutput,
) (args []string, outDur float64, ratio float64) {
	cfg = withStudioDefaults(cfg)
	preset := cfg.FFmpegPreset
	crf := cfg.FFmpegCRF
	mode := strings.ToLower(strings.TrimSpace(in.OverflowMode))
	if mode == "" {
		mode = "freeze_last_frame"
	}
	duckOriginal := true
	if in.DuckOriginal != nil {
		duckOriginal = *in.DuckOriginal
	}

	vDur := vInfo.DurationSec
	aDur := aInfo.DurationSec
	outDur = math.Max(vDur, aDur)
	if outDur <= 0 {
		outDur = float64(cfg.DefaultShotDurationSec)
	}
	ratio = 1.0

	var vFilters []string
	var aFilters []string

	if vDur > 0 && aDur > vDur+cfg.AudioOverflowToleranceSec {
		switch mode {
		case "speed_up":
			ratio = math.Min(maxDubSpeedUpRatio, aDur/vDur)
			aFilters = append(aFilters, buildAtempoChain(ratio)...)
			outDur = vDur
		case "trim":
			aFilters = append(aFilters, fmt.Sprintf("atrim=0:%.3f,asetpts=PTS-STARTPTS", vDur))
			outDur = vDur
		default: // "freeze_last_frame"
			pad := aDur - vDur
			vFilters = append(vFilters, fmt.Sprintf("tpad=stop_mode=clone:stop_duration=%.3f", pad))
			outDur = aDur
		}
	}

	dubLabel := "[1:a]"
	var fc []string
	if len(vFilters) > 0 {
		fc = append(fc, "[0:v]"+strings.Join(vFilters, ",")+"[vout]")
	}
	if len(aFilters) > 0 {
		fc = append(fc, "[1:a]"+strings.Join(aFilters, ",")+"[dub_prep]")
		dubLabel = "[dub_prep]"
	}

	origVol := in.OriginalVolume
	if origVol <= 0 {
		origVol = cfg.TimelineAudioDuckOrigVol
	}
	if vInfo.HasAudio && duckOriginal {
		fc = append(fc,
			fmt.Sprintf("[0:a]volume=%.2f[bg]", origVol),
			fmt.Sprintf("%sasplit=2[sc][dub]", dubLabel),
			fmt.Sprintf("[bg][sc]sidechaincompress=threshold=%.3f:ratio=%d:attack=%d:release=%d[ducked]",
				cfg.TimelineAudioDuckThreshold, duckCompRatio, duckCompAttackMs, duckCompReleaseMs),
			"[ducked][dub]amix=inputs=2:duration=longest:normalize=0[aout]",
		)
	} else if vInfo.HasAudio && in.OriginalVolume > 0 {
		fc = append(fc,
			fmt.Sprintf("[0:a]volume=%.2f[bg]", origVol),
			fmt.Sprintf("[bg]%samix=inputs=2:duration=longest:normalize=0[aout]", dubLabel),
		)
	}

	args = []string{"-y", "-i", vidPath, "-i", audioPath}
	if len(fc) > 0 {
		args = append(args, "-filter_complex", strings.Join(fc, ";"))
		if len(vFilters) > 0 {
			args = append(args, "-map", "[vout]", "-c:v", cfg.FFmpegVideoCodec, "-preset", preset, "-crf", fmt.Sprintf("%d", crf), "-pix_fmt", cfg.FFmpegPixelFormat)
		} else {
			args = append(args, "-map", "0:v", "-c:v", "copy")
		}
		if vInfo.HasAudio && (duckOriginal || in.OriginalVolume > 0) {
			args = append(args, "-map", "[aout]")
		} else {
			args = append(args, "-map", dubLabel)
		}
	} else {
		args = append(args, "-map", "0:v", "-map", "1:a", "-c:v", "copy")
	}

	args = append(args, "-c:a", cfg.FFmpegAudioCodec, "-b:a", cfg.FFmpegAudioBitrate, "-movflags", "+faststart", "-progress", "pipe:1", "-nostats", outPath)
	return args, outDur, ratio
}

func buildAtempoChain(factor float64) []string {
	var filters []string
	rem := factor
	for rem > atempoMaxFactor {
		filters = append(filters, "atempo=2.0")
		rem /= atempoMaxFactor
	}
	for rem < atempoMinFactor {
		filters = append(filters, "atempo=0.5")
		rem /= atempoMinFactor
	}
	filters = append(filters, fmt.Sprintf("atempo=%.4f", rem))
	return filters
}
