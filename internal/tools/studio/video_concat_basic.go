package studio

import (
	"context"
	"crypto/rand"
	"encoding/hex"
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
	"app/internal/progress"
	"app/internal/runner"
	"app/internal/service/media"
)

type manifestClip struct {
	Path        string  `json:"path"`
	DurationSec float64 `json:"duration_sec,omitempty"`
}

type pipeProgressParser struct {
	totalDurationUs int64
}

func (p *pipeProgressParser) ParseLine(line string) (float64, string, bool) {
	k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
	if !ok {
		return 0, "", false
	}
	if k != "out_time_us" && k != "out_time_ms" {
		return 0, "", false
	}
	us, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
	if err != nil || us < 0 {
		return 0, "", false
	}
	pct := 10.0
	if p.totalDurationUs > 0 {
		pct = float64(us) / float64(p.totalDurationUs) * 100.0
		if pct < 5 {
			pct = 5
		}
		if pct > 98 {
			pct = 98
		}
	}
	sec := float64(us) / 1_000_000.0
	return pct, fmt.Sprintf("Rendering assembled video (%.1fs)…", sec), true
}

// NewVideoConcatBasicNode builds the tool.video_concat_basic workflow node for MVP MP4 assembly.
func NewVideoConcatBasicNode(opts Options) engine.Node {
	opts.StudioConfig = withStudioDefaults(opts.StudioConfig)
	defaultRes := opts.StudioConfig.DefaultResolution
	defaultFPS := opts.StudioConfig.DefaultFPS

	spec := engine.Spec{
		Type:        NodeTypeVideoConcatBasic,
		Provider:    "tool",
		Label:       "Basic Video Assembler",
		Description: "Normalize resolution/frame rate, concatenate video clips, optionally mix a narration audio track, and export an assembled MP4 via FFmpeg",
		Inputs: []engine.Port{
			{Name: "clips_manifest_json", Label: "Clips manifest JSON (array of paths or objects)", Type: engine.PortText},
		},
		Outputs: []engine.Port{
			{Name: "video", Label: "Assembled video", Type: engine.PortVideo},
		},
		Params: []engine.Param{
			{Name: "output_dir", Label: "Output directory (empty = workspace media/videos)", Kind: engine.ParamText, Role: engine.ParamRoleOutputDir},
			{Name: "narration_audio_path", Label: "Optional narration/voiceover audio path", Kind: engine.ParamText},
			{Name: "resolution", Label: "Target resolution (WxH)", Kind: engine.ParamText, Default: defaultRes},
			{Name: "fps", Label: "Target frame rate (FPS)", Kind: engine.ParamInteger, Min: 1, Max: 120, Default: strconv.Itoa(defaultFPS)},
		},
	}

	return engine.Typed(spec, func(ctx context.Context, in contracts.VideoConcatBasicInput) (contracts.VideoConcatBasicOutput, error) {
		clips, err := parseClipsManifest(in.ClipsManifestJSON)
		if err != nil {
			return contracts.VideoConcatBasicOutput{}, err
		}
		if len(clips) == 0 {
			return contracts.VideoConcatBasicOutput{}, apperr.Validation("clips_manifest_json must contain at least one video clip")
		}

		targetRes := strings.TrimSpace(in.Resolution)
		if targetRes == "" {
			targetRes = defaultRes
		}
		width, height, err := parseResolutionWxH(targetRes, opts.StudioConfig.MaxVideoDimension)
		if err != nil {
			return contracts.VideoConcatBasicOutput{}, err
		}
		fps := in.FPS
		if fps <= 0 {
			fps = defaultFPS
		}
		if fps < 1 || fps > 120 {
			return contracts.VideoConcatBasicOutput{}, apperr.Validation("fps must be between 1 and 120")
		}

		// Resolve and guard all input clips.
		for i := range clips {
			resolved, rerr := resolveInputFile(ctx, opts, clips[i].Path)
			if rerr != nil {
				return contracts.VideoConcatBasicOutput{}, rerr
			}
			if _, serr := os.Stat(resolved); serr != nil {
				return contracts.VideoConcatBasicOutput{}, apperr.Validation(fmt.Sprintf("clip not accessible: %s", filepath.Base(resolved)))
			}
			clips[i].Path = resolved
		}

		narrationPath := ""
		if strings.TrimSpace(in.NarrationAudioPath) != "" {
			narrationPath, err = resolveInputFile(ctx, opts, in.NarrationAudioPath)
			if err != nil {
				return contracts.VideoConcatBasicOutput{}, err
			}
			if _, serr := os.Stat(narrationPath); serr != nil {
				return contracts.VideoConcatBasicOutput{}, apperr.Validation(fmt.Sprintf("narration audio not accessible: %s", filepath.Base(narrationPath)))
			}
		}

		outDir, err := resolveConcatOutputDir(ctx, opts, in.OutputDir)
		if err != nil {
			return contracts.VideoConcatBasicOutput{}, err
		}
		outPath := filepath.Join(outDir, uniqueExportFilename("studio_assembly", ".mp4"))
		if err := engine.CheckFile(ctx, outPath, true); err != nil {
			return contracts.VideoConcatBasicOutput{}, err
		}

		ffmpegExe, err := resolveBinary(ctx, opts.Binaries, domainbinaries.IDFFmpeg)
		if err != nil {
			return contracts.VideoConcatBasicOutput{}, err
		}
		ffprobeExe, _, _, _ := opts.Binaries.Resolve(ctx, domainbinaries.IDFFprobe)

		progress.Report(ctx, progress.Update{
			Percent: 5,
			Message: fmt.Sprintf("Inspecting %d video clip(s)…", len(clips)),
			Key:     "studio.progress.concat_probe",
		})

		clipHasAudio := make([]bool, len(clips))
		var totalDurSec float64
		for i := range clips {
			dur := clips[i].DurationSec
			if strings.TrimSpace(ffprobeExe) != "" {
				if pOut, perr := probeWithFFprobe(ctx, ffprobeExe, clips[i].Path); perr == nil {
					clipHasAudio[i] = pOut.HasAudio
					if pOut.DurationSec > 0 {
						dur = pOut.DurationSec
					}
				}
			}
			if dur <= 0 {
				dur = float64(opts.StudioConfig.DefaultShotDurationSec)
			}
			clips[i].DurationSec = dur
			totalDurSec += dur
		}

		if err := os.MkdirAll(outDir, 0o755); err != nil {
			return contracts.VideoConcatBasicOutput{}, apperr.Internal(err)
		}
		tmpOut := outPath + ".tmp.mp4"
		defer func() { _ = os.Remove(tmpOut) }()

		args := buildFFmpegConcatArgs(clips, clipHasAudio, narrationPath, width, height, fps, opts.StudioConfig, tmpOut)
		runCtx, cancel := context.WithTimeout(ctx, opts.StudioConfig.FFmpegTimeout)
		defer cancel()

		parser := &pipeProgressParser{totalDurationUs: int64(totalDurSec * 1_000_000)}
		res, err := runner.RunWithOptions(runCtx, ffmpegExe, args, runner.RunOptions{
			ProgressParser: parser,
			OnProgress: func(pct float64, msg string) {
				progress.Report(ctx, progress.Update{
					Percent: pct,
					Message: msg,
					Key:     "studio.progress.concat_render",
				})
			},
		})
		if err != nil {
			detail := strings.TrimSpace(resStderr(res))
			if detail == "" {
				detail = err.Error()
			}
			return contracts.VideoConcatBasicOutput{}, apperr.Unavailable("ffmpeg assembly failed: " + detail)
		}

		if err := os.Rename(tmpOut, outPath); err != nil {
			return contracts.VideoConcatBasicOutput{}, apperr.Internal(err)
		}

		if strings.TrimSpace(ffprobeExe) != "" {
			if pOut, perr := probeWithFFprobe(ctx, ffprobeExe, outPath); perr == nil && pOut.DurationSec > 0 {
				totalDurSec = pOut.DurationSec
			}
		}

		var sizeBytes int64
		if st, serr := os.Stat(outPath); serr == nil {
			sizeBytes = st.Size()
		}

		assetID := ""
		if opts.Registrar != nil {
			item, rerr := opts.Registrar.RegisterAsset(context.Background(), media.RegisterAssetParams{
				Kind:           "video",
				SourceNode:     NodeTypeVideoConcatBasic,
				FilePath:       outPath,
				RegisteredRoot: outDir,
				MIMEType:       "video/mp4",
				SizeBytes:      sizeBytes,
				Width:          width,
				Height:         height,
				DurationSec:    totalDurSec,
			})
			if rerr == nil && item != nil {
				assetID = item.ID
			}
		}

		progress.Report(ctx, progress.Update{
			Percent: 100,
			Message: "Video assembled",
			Key:     "studio.progress.done",
		})

		return contracts.VideoConcatBasicOutput{
			Video:       engine.RefFor(outPath, engine.MediaVideo),
			OutputPath:  outPath,
			AssetID:     assetID,
			DurationSec: totalDurSec,
			ClipCount:   len(clips),
		}, nil
	})
}

func parseClipsManifest(raw string) ([]manifestClip, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, apperr.Validation("clips_manifest_json is required")
	}

	var rawItems []json.RawMessage
	if err := json.Unmarshal([]byte(trimmed), &rawItems); err != nil {
		var wrapper struct {
			Clips []json.RawMessage `json:"clips"`
		}
		if werr := json.Unmarshal([]byte(trimmed), &wrapper); werr != nil || wrapper.Clips == nil {
			return nil, apperr.Validation("clips_manifest_json must be a JSON array of paths/objects or {\"clips\":[...]}")
		}
		rawItems = wrapper.Clips
	}

	out := make([]manifestClip, 0, len(rawItems))
	for _, elem := range rawItems {
		var pathStr string
		if err := json.Unmarshal(elem, &pathStr); err == nil {
			if p := strings.TrimSpace(pathStr); p != "" {
				out = append(out, manifestClip{Path: p})
			}
			continue
		}
		var obj manifestClip
		if err := json.Unmarshal(elem, &obj); err != nil {
			return nil, apperr.Validation("clips_manifest_json entries must be path strings or {\"path\":...} objects")
		}
		if p := strings.TrimSpace(obj.Path); p != "" {
			obj.Path = p
			out = append(out, obj)
		}
	}
	return out, nil
}

func resolveConcatOutputDir(ctx context.Context, opts Options, rawOutDir string) (string, error) {
	tree := resolveTree(opts)
	mediaDir := tree.MediaDir
	trimmed := strings.TrimSpace(rawOutDir)
	var target string
	switch {
	case trimmed != "":
		resolved := engine.ResolveMediaPath(trimmed, mediaDir)
		if resolved == "" {
			return "", apperr.Validation("output_dir is invalid or escapes workspace")
		}
		target = resolved
	case strings.TrimSpace(tree.VideosDir) != "":
		target = tree.VideosDir
	case strings.TrimSpace(mediaDir) != "":
		target = filepath.Join(mediaDir, "videos")
	default:
		return "", apperr.Validation("output_dir is required when no workspace media directory is configured")
	}
	if err := engine.CheckFile(ctx, target, true); err != nil {
		return "", err
	}
	return target, nil
}

func buildFFmpegConcatArgs(
	clips []manifestClip,
	hasAudio []bool,
	narrationPath string,
	width, height, fps int,
	cfg config.StudioConfig,
	outPath string,
) []string {
	cfg = withStudioDefaults(cfg)
	preset := cfg.FFmpegPreset
	crf := cfg.FFmpegCRF
	args := []string{"-y", "-hide_banner", "-progress", "pipe:1", "-nostats"}
	for _, c := range clips {
		args = append(args, "-i", c.Path)
	}
	narIdx := -1
	if narrationPath != "" {
		narIdx = len(clips)
		args = append(args, "-i", narrationPath)
	}

	var fc strings.Builder
	var concatInputs strings.Builder
	for i, c := range clips {
		fmt.Fprintf(&fc, "[%d:v]scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2,setsar=1,fps=%d,format=%s,setpts=PTS-STARTPTS[v%d];",
			i, width, height, width, height, fps, cfg.FFmpegPixelFormat, i)
		if hasAudio[i] {
			fmt.Fprintf(&fc, "[%d:a]aformat=sample_rates=%d:channel_layouts=stereo,asetpts=PTS-STARTPTS[a%d];", i, cfg.FFmpegAudioSampleRate, i)
		} else {
			fmt.Fprintf(&fc, "anullsrc=r=%d:cl=stereo,atrim=duration=%.3f[a%d];", cfg.FFmpegAudioSampleRate, c.DurationSec, i)
		}
		fmt.Fprintf(&concatInputs, "[v%d][a%d]", i, i)
	}

	audioOutLabel := "aout"
	if narIdx >= 0 {
		fmt.Fprintf(&fc, "%sconcat=n=%d:v=1:a=1[vout][aconcat];", concatInputs.String(), len(clips))
		fmt.Fprintf(&fc, "[%d:a]aformat=sample_rates=%d:channel_layouts=stereo,asetpts=PTS-STARTPTS[anar];", narIdx, cfg.FFmpegAudioSampleRate)
		fmt.Fprintf(&fc, "[aconcat][anar]amix=inputs=2:duration=first:normalize=0[%s]", audioOutLabel)
	} else {
		fmt.Fprintf(&fc, "%sconcat=n=%d:v=1:a=1[vout][%s]", concatInputs.String(), len(clips), audioOutLabel)
	}

	args = append(args,
		"-filter_complex", fc.String(),
		"-map", "[vout]",
		"-map", "["+audioOutLabel+"]",
		"-c:v", cfg.FFmpegVideoCodec,
		"-preset", preset,
		"-crf", strconv.Itoa(crf),
		"-pix_fmt", cfg.FFmpegPixelFormat,
		"-c:a", cfg.FFmpegAudioCodec,
		"-b:a", cfg.FFmpegAudioBitrate,
		"-movflags", "+faststart",
		outPath,
	)
	return args
}

func uniqueExportFilename(prefix, ext string) string {
	var rnd [4]byte
	_, _ = rand.Read(rnd[:])
	return fmt.Sprintf("%s-%s-%s%s", prefix, time.Now().Format("20060102-150405"), hex.EncodeToString(rnd[:]), ext)
}
