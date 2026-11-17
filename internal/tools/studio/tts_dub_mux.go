package studio

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"path/filepath"
	"strings"

	"app/internal/apperr"
	"app/internal/contracts"
	domainbinaries "app/internal/domain/binaries"
	domainmedia "app/internal/domain/mediagen"
	"app/internal/engine"
	"app/internal/progress"
	"app/internal/runner"
)

type ttsDubMuxNode struct {
	opts Options
}

// NewTTSDubMuxNode returns tool.tts_dub_mux for synthesizing dub voiceover (when needed) and muxing it onto video with overflow & ducking control.
func NewTTSDubMuxNode(opts Options) engine.Node {
	opts.StudioConfig = withStudioDefaults(opts.StudioConfig)
	return &ttsDubMuxNode{opts: opts}
}

func (n *ttsDubMuxNode) Spec() engine.Spec {
	return engine.Spec{
		Type:        NodeTypeTTSDubMux,
		Provider:    "studio",
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
			{Name: "overflow_mode", Label: "Audio Overflow Mode", Kind: engine.ParamSelect, Options: []string{"freeze_last_frame", "speed_up", "trim"}, Default: "freeze_last_frame"},
			{Name: "duck_original", Label: "Duck Original Audio", Kind: engine.ParamToggle, Default: "true"},
			{Name: "output_dir", Label: "Output Folder", Kind: engine.ParamText, Role: engine.ParamRoleOutputDir},
		},
	}
}

func (n *ttsDubMuxNode) Run(ctx context.Context, raw json.RawMessage) (any, error) {
	var in contracts.TTSDubMuxInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, apperr.Validation("invalid tts_dub_mux input").WithDetails(map[string]any{"cause": err.Error()})
	}
	vidPath, err := resolveInputFile(ctx, n.opts, in.VideoPath)
	if err != nil {
		return nil, err
	}
	if vidPath == "" {
		return nil, apperr.Validation("video_path is required")
	}

	outDir, err := resolveNodeOutputDir(ctx, n.opts, in.OutputDir, filepath.Join("studio", "dubs"))
	if err != nil {
		return nil, err
	}

	audioPath, err := resolveInputFile(ctx, n.opts, in.AudioPath)
	if err != nil {
		return nil, err
	}
	if audioPath == "" {
		text := strings.TrimSpace(in.Text)
		if text == "" {
			return nil, apperr.Validation("either audio_path or text is required for tts_dub_mux")
		}
		if n.opts.TTSGen == nil {
			return nil, apperr.Unavailable("TTS generator service is unavailable")
		}
		ttsRes, err := n.opts.TTSGen.GenerateTTS(ctx, domainmedia.TTSRequest{
			ModelRef:  in.ModelRef,
			Text:      text,
			Voice:     in.Voice,
			Language:  in.Language,
			OutputDir: outDir,
		})
		if err != nil {
			return nil, err
		}
		audioPath = ttsRes.Path
	}

	ffmpegExe, err := resolveBinary(ctx, n.opts.Binaries, domainbinaries.IDFFmpeg)
	if err != nil {
		return nil, err
	}
	ffprobeExe, err := resolveBinary(ctx, n.opts.Binaries, domainbinaries.IDFFprobe)
	if err != nil {
		return nil, err
	}

	probeCtx, cancelProbe := context.WithTimeout(ctx, n.opts.StudioConfig.FFprobeTimeout)
	defer cancelProbe()
	vInfo, err := probeWithFFprobe(probeCtx, ffprobeExe, vidPath)
	if err != nil {
		return nil, err
	}
	aInfo, err := probeWithFFprobe(probeCtx, ffprobeExe, audioPath)
	if err != nil {
		return nil, err
	}

	outPath := filepath.Join(outDir, uniqueExportFilename("dubbed", ".mp4"))
	if err := engine.CheckFile(ctx, outPath, true); err != nil {
		return nil, err
	}

	args, targetDur, adjRatio := buildDubMuxArgs(in, n.opts.StudioConfig.FFmpegPreset, n.opts.StudioConfig.FFmpegCRF, vidPath, audioPath, outPath, vInfo, aInfo)

	ffCtx, cancelFF := context.WithTimeout(ctx, n.opts.StudioConfig.FFmpegTimeout)
	defer cancelFF()
	progress.Report(ctx, progress.Update{Percent: 10, Message: "Muxing dubbed audio onto video…", Key: "studio.progress.tts_dub_mux"})
	parser := &pipeProgressParser{totalDurationUs: int64(targetDur * 1_000_000)}
	res, err := runner.RunWithOptions(ffCtx, ffmpegExe, args, runner.RunOptions{
		ProgressParser: parser,
		OnProgress: func(pct float64, msg string) {
			progress.Report(ctx, progress.Update{Percent: pct, Message: msg, Key: "studio.progress.tts_dub_mux"})
		},
	})
	if err != nil {
		return nil, apperr.Wrap(apperr.CodeUnavailable, "FFmpeg tts_dub_mux failed: "+resStderr(res), err)
	}
	if res.ExitCode != 0 {
		return nil, apperr.Unavailable("FFmpeg tts_dub_mux failed: " + resStderr(res))
	}
	progress.Report(ctx, progress.Update{Percent: 100, Message: "Dubbing & mux ready", Key: "studio.progress.done"})

	assetID := registerStudioAsset(ctx, n.opts, "video", NodeTypeTTSDubMux, outPath, outDir)
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
	preset string,
	crf int,
	vidPath, audioPath, outPath string,
	vInfo, aInfo contracts.ProbeMediaOutput,
) (args []string, outDur float64, ratio float64) {
	mode := strings.ToLower(strings.TrimSpace(in.OverflowMode))
	if mode == "" {
		mode = "freeze_last_frame"
	}

	vDur := vInfo.DurationSec
	aDur := aInfo.DurationSec
	outDur = math.Max(vDur, aDur)
	if outDur <= 0 {
		outDur = 5
	}
	ratio = 1.0

	var vFilters []string
	var aFilters []string

	if vDur > 0 && aDur > vDur+0.05 {
		switch mode {
		case "speed_up":
			ratio = math.Min(4.0, aDur/vDur)
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
		origVol = 0.35
	}
	if vInfo.HasAudio && in.DuckOriginal {
		fc = append(fc,
			fmt.Sprintf("[0:a]volume=%.2f[bg]", origVol),
			fmt.Sprintf("%sasplit=2[sc][dub]", dubLabel),
			"[bg][sc]sidechaincompress=threshold=0.05:ratio=6:attack=20:release=250[ducked]",
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
			args = append(args, "-map", "[vout]", "-c:v", "libx264", "-preset", preset, "-crf", fmt.Sprintf("%d", crf), "-pix_fmt", "yuv420p")
		} else {
			args = append(args, "-map", "0:v", "-c:v", "copy")
		}
		if vInfo.HasAudio && (in.DuckOriginal || in.OriginalVolume > 0) {
			args = append(args, "-map", "[aout]")
		} else {
			args = append(args, "-map", dubLabel)
		}
	} else {
		args = append(args, "-map", "0:v", "-map", "1:a", "-c:v", "copy")
	}

	args = append(args, "-c:a", "aac", "-b:a", "192k", "-movflags", "+faststart", "-progress", "pipe:1", "-nostats", outPath)
	return args, outDur, ratio
}

func buildAtempoChain(factor float64) []string {
	var filters []string
	rem := factor
	for rem > 2.0 {
		filters = append(filters, "atempo=2.0")
		rem /= 2.0
	}
	for rem < 0.5 {
		filters = append(filters, "atempo=0.5")
		rem /= 0.5
	}
	filters = append(filters, fmt.Sprintf("atempo=%.4f", rem))
	return filters
}
