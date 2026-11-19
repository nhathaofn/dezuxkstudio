package studio

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
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
	"app/internal/service/media"
)

type manifestClip struct {
	Path        string  `json:"path"`
	DurationSec float64 `json:"duration_sec,omitempty"`
}

// NewVideoConcatBasicNode builds the tool.video_concat_basic workflow node for MVP MP4 assembly.
func NewVideoConcatBasicNode(opts Options) engine.Node {
	opts.StudioConfig = withStudioDefaults(opts.StudioConfig)
	defaultRes := opts.StudioConfig.DefaultResolution
	defaultFPS := opts.StudioConfig.DefaultFPS

	spec := engine.Spec{
		Type:        NodeTypeVideoConcatBasic,
		Provider:    studioProviderID,
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
			{Name: "fps", Label: "Target frame rate (FPS)", Kind: engine.ParamInteger, Min: minTimelineFPS, Max: maxTimelineFPS, Default: strconv.Itoa(defaultFPS)},
		},
	}

	return engine.Typed(spec, func(ctx context.Context, in contracts.VideoConcatBasicInput) (contracts.VideoConcatBasicOutput, error) {
		clips, err := parseClipsManifest(in.ClipsManifestJSON)
		if err != nil {
			return contracts.VideoConcatBasicOutput{}, err
		}
		if len(clips) == 0 {
			return contracts.VideoConcatBasicOutput{}, apperr.Validation("clips_manifest_json must contain at least one video clip").
				WithMsg("errors.studio.clips_required", nil)
		}

		targetRes := strings.TrimSpace(in.Resolution)
		if targetRes == "" {
			targetRes = defaultRes
		}
		width, height, err := parseResolutionWxH(targetRes, opts.StudioConfig.MaxVideoDimension)
		if err != nil {
			return contracts.VideoConcatBasicOutput{}, err
		}
		if in.FPS < 0 || (in.FPS > 0 && (in.FPS < minTimelineFPS || in.FPS > maxTimelineFPS)) {
			return contracts.VideoConcatBasicOutput{}, apperr.Validation(fmt.Sprintf("fps must be between %d and %d", minTimelineFPS, maxTimelineFPS)).
				WithMsg("errors.studio.invalid_fps", map[string]any{"min": minTimelineFPS, "max": maxTimelineFPS})
		}
		fps := in.FPS
		if fps == 0 {
			fps = defaultFPS
		}

		for i := range clips {
			if err := ctx.Err(); err != nil {
				return contracts.VideoConcatBasicOutput{}, err
			}
			if math.IsNaN(clips[i].DurationSec) || math.IsInf(clips[i].DurationSec, 0) || clips[i].DurationSec < 0 {
				return contracts.VideoConcatBasicOutput{}, apperr.Validation("clip duration_sec must be a non-negative finite number").
					WithMsg("errors.studio.invalid_duration", nil)
			}
			resolved, rerr := resolveMediaInput(ctx, opts, clips[i].Path, inputKindVideo)
			if rerr != nil {
				return contracts.VideoConcatBasicOutput{}, rerr
			}
			if _, serr := os.Stat(resolved); serr != nil {
				return contracts.VideoConcatBasicOutput{}, apperr.NotFound(fmt.Sprintf("clip not accessible: %s", filepath.Base(resolved))).
					WithMsg("errors.studio.media_not_found", nil)
			}
			clips[i].Path = resolved
		}

		narrationPath := ""
		if strings.TrimSpace(in.NarrationAudioPath) != "" {
			narrationPath, err = resolveMediaInput(ctx, opts, in.NarrationAudioPath, inputKindAudio)
			if err != nil {
				return contracts.VideoConcatBasicOutput{}, err
			}
			if _, serr := os.Stat(narrationPath); serr != nil {
				return contracts.VideoConcatBasicOutput{}, apperr.NotFound(fmt.Sprintf("narration audio not accessible: %s", filepath.Base(narrationPath))).
					WithMsg("errors.studio.media_not_found", nil)
			}
		}

		outDir, err := resolveConcatOutputDir(ctx, opts, in.OutputDir)
		if err != nil {
			return contracts.VideoConcatBasicOutput{}, err
		}
		outPath := filepath.Join(outDir, uniqueExportFilename("studio_assembly", ".mp4"))
		if err := checkStudioFile(ctx, outPath, true); err != nil {
			return contracts.VideoConcatBasicOutput{}, err
		}

		ffmpegExe, err := resolveBinary(ctx, opts.Binaries, domainbinaries.IDFFmpeg)
		if err != nil {
			return contracts.VideoConcatBasicOutput{}, err
		}
		var ffprobeExe string
		if opts.Binaries != nil {
			if exe, _, _, rerr := opts.Binaries.Resolve(ctx, domainbinaries.IDFFprobe); rerr == nil {
				ffprobeExe = strings.TrimSpace(exe)
			}
		}

		progress.Report(ctx, progress.Update{
			Percent: 5,
			Message: fmt.Sprintf("Inspecting %d video clip(s)…", len(clips)),
			Key:     "studio.progress.concat_probe",
			Params: map[string]string{
				"count": strconv.Itoa(len(clips)),
			},
		})

		clipHasAudio := make([]bool, len(clips))
		var totalDurSec float64
		for i := range clips {
			if err := ctx.Err(); err != nil {
				return contracts.VideoConcatBasicOutput{}, err
			}
			dur := clips[i].DurationSec
			if ffprobeExe != "" {
				probeCtx, cancelProbe := context.WithTimeout(ctx, opts.StudioConfig.FFprobeTimeout)
				stopProbe := progress.KeepAlive(ctx)
				pOut, perr := probeWithFFprobe(probeCtx, ffprobeExe, clips[i].Path)
				stopProbe()
				cancelProbe()
				if perr == nil {
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

		if err := os.MkdirAll(outDir, 0o750); err != nil {
			return contracts.VideoConcatBasicOutput{}, ioUnavailableErr("Failed to create video assembly output directory", err)
		}
		tmpOut, err := createTempMediaFile(outDir, ".tmp-concat-", ".mp4")
		if err != nil {
			return contracts.VideoConcatBasicOutput{}, err
		}
		defer func() { _ = os.Remove(tmpOut) }()

		if err := runConcatBatch(ctx, ffmpegExe, outDir, clips, clipHasAudio, narrationPath, width, height, fps, opts.StudioConfig, totalDurSec, tmpOut); err != nil {
			return contracts.VideoConcatBasicOutput{}, err
		}

		if err := commitTempMediaFile(tmpOut, outPath); err != nil {
			return contracts.VideoConcatBasicOutput{}, err
		}

		if ffprobeExe != "" {
			probeCtx, cancelProbe := context.WithTimeout(ctx, opts.StudioConfig.FFprobeTimeout)
			stopProbe := progress.KeepAlive(ctx)
			pOut, perr := probeWithFFprobe(probeCtx, ffprobeExe, outPath)
			stopProbe()
			cancelProbe()
			if perr == nil && pOut.DurationSec > 0 {
				totalDurSec = pOut.DurationSec
			}
		}

		var sizeBytes int64
		if st, serr := os.Stat(outPath); serr == nil {
			sizeBytes = st.Size()
		}

		assetID := ""
		if opts.Registrar != nil {
			item, rerr := opts.Registrar.RegisterAsset(ctx, media.RegisterAssetParams{
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
			if rerr != nil {
				if opts.Logger != nil {
					opts.Logger.WarnContext(ctx, "studio: failed to register assembled video",
						"source_node", NodeTypeVideoConcatBasic,
						"error", rerr,
					)
				}
			} else if item != nil {
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
		return nil, apperr.Validation("clips_manifest_json is required").
			WithMsg("errors.studio.clips_required", nil)
	}

	var rawItems []json.RawMessage
	if err := json.Unmarshal([]byte(trimmed), &rawItems); err != nil {
		var wrapper struct {
			Clips []json.RawMessage `json:"clips"`
		}
		if werr := json.Unmarshal([]byte(trimmed), &wrapper); werr != nil || wrapper.Clips == nil {
			return nil, apperr.Validation("clips_manifest_json must be a JSON array of paths/objects or {\"clips\":[...]}").
				WithMsg("errors.studio.invalid_clips_manifest", nil)
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
			return nil, apperr.Validation("clips_manifest_json entries must be path strings or {\"path\":...} objects").
				WithMsg("errors.studio.invalid_clips_manifest", nil)
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
	mediaDir := strings.TrimSpace(tree.MediaDir)
	trimmed := strings.TrimSpace(rawOutDir)
	var target string
	switch {
	case trimmed != "":
		if filepath.IsAbs(filepath.Clean(trimmed)) {
			target = filepath.Clean(trimmed)
		} else {
			var anchor string
			if strings.HasPrefix(filepath.ToSlash(trimmed), "/media/") {
				anchor = mediaDir
			} else {
				for _, cand := range []string{mediaDir, tree.WorkspaceDir, resolveProjectsDir(opts)} {
					if c := strings.TrimSpace(cand); c != "" && filepath.IsAbs(c) {
						anchor = c
						break
					}
				}
			}
			if anchor == "" {
				return "", apperr.Validation("relative output_dir requires a configured workspace or media directory").
					WithMsg("errors.studio.output_dir_required", nil)
			}
			target = engine.ResolveMediaPath(trimmed, anchor)
		}
	case strings.TrimSpace(tree.VideosDir) != "" && filepath.IsAbs(strings.TrimSpace(tree.VideosDir)):
		target = filepath.Clean(strings.TrimSpace(tree.VideosDir))
	case mediaDir != "" && filepath.IsAbs(mediaDir):
		target = filepath.Join(mediaDir, "videos")
	default:
		return "", apperr.Validation("output_dir is required when no workspace media directory is configured").
			WithMsg("errors.studio.output_dir_required", nil)
	}
	if target == "" || !filepath.IsAbs(target) {
		return "", apperr.Validation("output_dir is invalid or escapes workspace").
			WithMsg("errors.studio.output_dir_escape", nil)
	}
	if err := checkStudioFile(ctx, filepath.Join(target, ".write-check"), true); err != nil {
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
