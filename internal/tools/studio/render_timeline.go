package studio

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"app/internal/apperr"
	"app/internal/contracts"
	domainbinaries "app/internal/domain/binaries"
	domainstudio "app/internal/domain/studio"
	"app/internal/engine"
	"app/internal/fsutil"
	"app/internal/progress"
	"app/internal/runner"
	"app/internal/service/media"
)

type resolvedTimelineClip struct {
	trackIndex int
	track      domainstudio.Track
	clip       domainstudio.TimelineClip
	inputIndex int
	isImage    bool
	hasAudio   bool
}

// NewRenderTimelineNode builds tool.render_timeline for full FFmpeg filter_complex rendering with xfade, loudnorm, and Vietnamese subtitle burn-in.
func NewRenderTimelineNode(opts Options) engine.Node {
	opts.StudioConfig = withStudioDefaults(opts.StudioConfig)
	defLUFS := fmt.Sprintf("%.1f", opts.StudioConfig.ExportLoudnormLUFS)
	defFontSize := opts.StudioConfig.ExportSubtitleFontSize
	defCRF := opts.StudioConfig.FFmpegCRF
	defPreset := opts.StudioConfig.FFmpegPreset

	spec := engine.Spec{
		Type:        NodeTypeRenderTimeline,
		Provider:    "studio",
		Label:       "Timeline FFmpeg Renderer",
		Description: "Renders a multi-track EditTimeline into an H.264/AAC MP4 using FFmpeg filter_complex with xfade transitions, -14 LUFS loudness normalization, and optional UTF-8 Vietnamese subtitle burn-in.",
		Inputs: []engine.Port{
			{Name: "timeline_json", Label: "EditTimeline JSON", Type: engine.PortText},
		},
		Outputs: []engine.Port{
			{Name: "video", Label: "Rendered MP4 Video", Type: engine.PortVideo},
		},
		Params: []engine.Param{
			{Name: "output_dir", Label: "Output Directory", Kind: engine.ParamText, Role: engine.ParamRoleOutputDir},
			{Name: "filename", Label: "Output Filename", Kind: engine.ParamText, Default: "timeline_final.mp4"},
			{Name: "burn_subtitles", Label: "Burn Subtitles into Video", Kind: engine.ParamToggle, Default: "false"},
			{Name: "subtitle_font_size", Label: "Subtitle Font Size", Kind: engine.ParamInteger, Min: 8, Max: 96, Default: strconv.Itoa(defFontSize)},
			{Name: "loudnorm_lufs", Label: "Loudnorm Target (LUFS)", Kind: engine.ParamText, Default: defLUFS},
			{Name: "crf", Label: "FFmpeg CRF (0-51)", Kind: engine.ParamInteger, Min: 0, Max: 51, Default: strconv.Itoa(defCRF)},
			{Name: "preset", Label: "FFmpeg Preset", Kind: engine.ParamSelect, Options: []string{"ultrafast", "superfast", "veryfast", "faster", "fast", "medium", "slow", "slower", "veryslow"}, Default: defPreset},
		},
	}

	return engine.Typed(spec, func(ctx context.Context, in contracts.RenderTimelineInput) (contracts.RenderTimelineOutput, error) {
		trimmed := strings.TrimSpace(in.TimelineJSON)
		if trimmed == "" {
			return contracts.RenderTimelineOutput{}, apperr.Validation("timeline_json is required")
		}
		var tl domainstudio.EditTimeline
		if err := json.Unmarshal([]byte(trimmed), &tl); err != nil {
			return contracts.RenderTimelineOutput{}, apperr.Validation("invalid timeline_json: " + err.Error())
		}
		if err := tl.Validate(); err != nil {
			return contracts.RenderTimelineOutput{}, err
		}

		outDir, err := resolveNodeOutputDir(ctx, opts, in.OutputDir, filepath.Join("studio", "exports"))
		if err != nil {
			return contracts.RenderTimelineOutput{}, err
		}

		baseName := sanitizeExportFileBase(in.Filename, "timeline_final", ".mp4")
		outPath := filepath.Join(outDir, baseName)
		if err := engine.CheckFile(ctx, outPath, true); err != nil {
			return contracts.RenderTimelineOutput{}, err
		}

		fontSize := in.SubtitleFontSize
		if fontSize <= 0 {
			fontSize = opts.StudioConfig.ExportSubtitleFontSize
		}
		if fontSize < 8 || fontSize > 96 {
			return contracts.RenderTimelineOutput{}, apperr.Validation("subtitle_font_size must be between 8 and 96")
		}

		lufs := in.LoudnormLUFS
		if lufs == 0 {
			lufs = opts.StudioConfig.ExportLoudnormLUFS
		}
		if math.IsNaN(lufs) || math.IsInf(lufs, 0) || lufs < -36 || lufs > -5 {
			return contracts.RenderTimelineOutput{}, apperr.Validation("loudnorm_lufs must be between -36 and -5")
		}

		crf := opts.StudioConfig.FFmpegCRF
		if in.CRF > 0 {
			crf = in.CRF
		}
		if crf < 0 || crf > 51 {
			return contracts.RenderTimelineOutput{}, apperr.Validation("crf must be between 0 and 51")
		}
		preset := strings.TrimSpace(in.Preset)
		if preset == "" {
			preset = opts.StudioConfig.FFmpegPreset
		}

		ffmpegExe, err := resolveBinary(ctx, opts.Binaries, domainbinaries.IDFFmpeg)
		if err != nil {
			return contracts.RenderTimelineOutput{}, err
		}
		var ffprobeExe string
		if opts.Binaries != nil {
			if exe, _, _, rerr := opts.Binaries.Resolve(ctx, domainbinaries.IDFFprobe); rerr == nil {
				ffprobeExe = strings.TrimSpace(exe)
			}
		}

		progress.Report(ctx, progress.Update{Percent: 5, Message: "Validating timeline media sources…", Key: "studio.progress.render_prepare"})

		// Resolve and FileGuard-check all referenced media files first.
		var resolved []resolvedTimelineClip
		inputIdx := 0
		for ti, tr := range tl.Tracks {
			for _, cl := range tr.Clips {
				if strings.TrimSpace(cl.FilePath) == "" {
					return contracts.RenderTimelineOutput{}, apperr.Validation(fmt.Sprintf("clip %q requires file_path for rendering", cl.ID))
				}
				p, rerr := resolveInputFile(ctx, opts, cl.FilePath)
				if rerr != nil {
					return contracts.RenderTimelineOutput{}, rerr
				}
				if _, serr := os.Stat(p); serr != nil {
					return contracts.RenderTimelineOutput{}, apperr.Validation(fmt.Sprintf("timeline media file not found: %s", filepath.Base(p)))
				}
				cl.FilePath = p
				isImg := isImageExtension(p)
				hasAudio := !isImg
				if !isImg && ffprobeExe != "" {
					probeCtx, cancel := context.WithTimeout(ctx, opts.StudioConfig.FFprobeTimeout)
					if pOut, perr := probeWithFFprobe(probeCtx, ffprobeExe, p); perr == nil {
						hasAudio = pOut.HasAudio
					}
					cancel()
				}
				resolved = append(resolved, resolvedTimelineClip{
					trackIndex: ti,
					track:      tr,
					clip:       cl,
					inputIndex: inputIdx,
					isImage:    isImg,
					hasAudio:   hasAudio,
				})
				inputIdx++
			}
		}

		if len(resolved) == 0 && len(tl.Subtitles) == 0 {
			return contracts.RenderTimelineOutput{}, apperr.Validation("timeline has no clips or subtitles to render")
		}

		var srtPath, assPath string
		if len(tl.Subtitles) > 0 {
			stem := strings.TrimSuffix(baseName, filepath.Ext(baseName))
			srtPath = filepath.Join(outDir, stem+".srt")
			assPath = filepath.Join(outDir, stem+".ass")
			if err := engine.CheckFile(ctx, srtPath, true); err != nil {
				return contracts.RenderTimelineOutput{}, err
			}
			if err := engine.CheckFile(ctx, assPath, true); err != nil {
				return contracts.RenderTimelineOutput{}, err
			}
			if err := fsutil.WriteFileAtomic(srtPath, []byte(formatSRT(tl.Subtitles)), 0o644); err != nil {
				return contracts.RenderTimelineOutput{}, apperr.Internal(err)
			}
			if err := fsutil.WriteFileAtomic(assPath, []byte(formatASS(tl.Subtitles, tl.Width, tl.Height, fontSize)), 0o644); err != nil {
				return contracts.RenderTimelineOutput{}, apperr.Internal(err)
			}
		}

		tmpOut := outPath + ".tmp.mp4"
		defer func() { _ = os.Remove(tmpOut) }()

		burnPath := ""
		if in.BurnSubtitles && assPath != "" {
			burnPath = assPath
		}
		args, expectedDurSec := buildRenderTimelineArgs(tl, resolved, burnPath, lufs, preset, crf, tmpOut)

		runCtx, cancelRun := context.WithTimeout(ctx, opts.StudioConfig.FFmpegTimeout)
		defer cancelRun()
		parser := &pipeProgressParser{totalDurationUs: int64(expectedDurSec * 1_000_000)}
		res, err := runner.RunWithOptions(runCtx, ffmpegExe, args, runner.RunOptions{
			ProgressParser: parser,
			OnProgress: func(pct float64, msg string) {
				progress.Report(ctx, progress.Update{
					Percent: pct,
					Message: msg,
					Key:     "studio.progress.render_timeline",
				})
			},
		})
		if err != nil {
			detail := strings.TrimSpace(resStderr(res))
			if detail == "" {
				detail = err.Error()
			}
			return contracts.RenderTimelineOutput{}, apperr.Unavailable("ffmpeg timeline render failed: " + detail)
		}

		if err := os.Rename(tmpOut, outPath); err != nil {
			return contracts.RenderTimelineOutput{}, apperr.Internal(err)
		}

		finalDurSec := expectedDurSec
		if ffprobeExe != "" {
			probeCtx, cancelProbe := context.WithTimeout(ctx, opts.StudioConfig.FFprobeTimeout)
			if pOut, perr := probeWithFFprobe(probeCtx, ffprobeExe, outPath); perr == nil && pOut.DurationSec > 0 {
				finalDurSec = pOut.DurationSec
			}
			cancelProbe()
		}

		var sizeBytes int64
		if st, serr := os.Stat(outPath); serr == nil {
			sizeBytes = st.Size()
		}

		assetID := ""
		if opts.Registrar != nil {
			item, rerr := opts.Registrar.RegisterAsset(context.Background(), media.RegisterAssetParams{
				Kind:           "video",
				SourceNode:     NodeTypeRenderTimeline,
				FilePath:       outPath,
				RegisteredRoot: outDir,
				MIMEType:       "video/mp4",
				SizeBytes:      sizeBytes,
				Width:          tl.Width,
				Height:         tl.Height,
				DurationSec:    finalDurSec,
			})
			if rerr == nil && item != nil {
				assetID = item.ID
			}
		}

		progress.Report(ctx, progress.Update{Percent: 100, Message: "Timeline rendered", Key: "studio.progress.done"})

		return contracts.RenderTimelineOutput{
			Video:       engine.RefFor(outPath, engine.MediaVideo),
			OutputPath:  outPath,
			AssetID:     assetID,
			SRTPath:     srtPath,
			ASSPath:     assPath,
			DurationSec: finalDurSec,
		}, nil
	})
}

func buildRenderTimelineArgs(
	tl domainstudio.EditTimeline,
	resolved []resolvedTimelineClip,
	assBurnPath string,
	lufs float64,
	preset string,
	crf int,
	outPath string,
) (args []string, totalDurSec float64) {
	args = []string{"-y", "-hide_banner", "-progress", "pipe:1", "-nostats"}

	for _, rc := range resolved {
		if rc.isImage {
			durSec := float64(rc.clip.TimelineSpanMs()) / 1000.0
			args = append(args, "-loop", "1", "-t", fmt.Sprintf("%.3f", durSec), "-i", rc.clip.FilePath)
		} else {
			args = append(args, "-i", rc.clip.FilePath)
		}
	}

	var videoClips []resolvedTimelineClip
	var audioClips []resolvedTimelineClip
	anyAudioSolo := false
	for _, tr := range tl.Tracks {
		if tr.Kind != domainstudio.TrackVideo && tr.Kind != domainstudio.TrackText && tr.Solo {
			anyAudioSolo = true
			break
		}
	}

	for _, rc := range resolved {
		switch rc.track.Kind {
		case domainstudio.TrackVideo:
			if !rc.track.Muted {
				videoClips = append(videoClips, rc)
				if rc.hasAudio && !anyAudioSolo {
					audioClips = append(audioClips, rc)
				}
			}
		case domainstudio.TrackAudioDialogue, domainstudio.TrackAudioNarration, domainstudio.TrackAudioBGM, domainstudio.TrackAudioSFX:
			if !rc.track.Muted && (!anyAudioSolo || rc.track.Solo) {
				audioClips = append(audioClips, rc)
			}
		}
	}

	slices.SortStableFunc(videoClips, func(a, b resolvedTimelineClip) int {
		if a.clip.TimelineStartMs != b.clip.TimelineStartMs {
			if a.clip.TimelineStartMs < b.clip.TimelineStartMs {
				return -1
			}
			return 1
		}
		return strings.Compare(a.clip.ID, b.clip.ID)
	})

	totalDurSec = math.Max(1.0, float64(tl.DurationMs())/1000.0)
	var fc []string

	// 1. Build normalized video segments and sequence/xfade chain.
	vOutLabel := "[vbase]"
	if len(videoClips) == 0 {
		fc = append(fc, fmt.Sprintf("color=c=black:s=%dx%d:r=%d:d=%.3f,settb=AVTB,format=yuv420p[vbase]", tl.Width, tl.Height, tl.FPS, totalDurSec))
	} else {
		for i, rc := range videoClips {
			durSec := float64(rc.clip.TimelineSpanMs()) / 1000.0
			inSec := float64(rc.clip.SourceInMs) / 1000.0
			outSec := float64(rc.clip.SourceOutMs) / 1000.0
			spd := rc.clip.Speed
			if spd <= 0 {
				spd = 1.0
			}
			var seg string
			if rc.isImage {
				seg = fmt.Sprintf("[%d:v]scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2,setsar=1,fps=%d%s,settb=AVTB,format=yuv420p[vseg%d]",
					rc.inputIndex, tl.Width, tl.Height, tl.Width, tl.Height, tl.FPS, kenBurnsFilter(rc.clip.KenBurnsPreset, tl.Width, tl.Height, tl.FPS, durSec), i)
			} else {
				seg = fmt.Sprintf("[%d:v]trim=start=%.3f:end=%.3f,setpts=(PTS-STARTPTS)/%.4f,scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2,setsar=1,fps=%d,settb=AVTB,format=yuv420p[vseg%d]",
					rc.inputIndex, inSec, outSec, spd, tl.Width, tl.Height, tl.Width, tl.Height, tl.FPS, i)
			}
			fc = append(fc, seg)
		}

		curLabel := "[vseg0]"
		var accumSec float64
		first := videoClips[0]
		firstDurSec := float64(first.clip.TimelineSpanMs()) / 1000.0
		if first.clip.TimelineStartMs > 0 {
			gapSec := float64(first.clip.TimelineStartMs) / 1000.0
			fc = append(fc,
				fmt.Sprintf("color=c=black:s=%dx%d:r=%d:d=%.3f,settb=AVTB,format=yuv420p[vgap0]", tl.Width, tl.Height, tl.FPS, gapSec),
				"[vgap0][vseg0]concat=n=2:v=1:a=0[vc0]",
			)
			curLabel = "[vc0]"
			accumSec = gapSec + firstDurSec
		} else {
			accumSec = firstDurSec
		}
		if first.clip.TransitionIn == domainstudio.TransitionFade && first.clip.TransitionMs > 0 {
			d := math.Min(float64(first.clip.TransitionMs)/1000.0, firstDurSec/2)
			nextLbl := "[vfade0]"
			fc = append(fc, fmt.Sprintf("%sfade=t=in:st=0:d=%.3f%s", curLabel, d, nextLbl))
			curLabel = nextLbl
		}

		prevEndMs := first.clip.TimelineEndMs()
		prevDurSec := firstDurSec

		for i := 1; i < len(videoClips); i++ {
			cur := videoClips[i]
			curDurSec := float64(cur.clip.TimelineSpanMs()) / 1000.0
			segLabel := fmt.Sprintf("[vseg%d]", i)
			nextLabel := fmt.Sprintf("[vchain%d]", i)

			overlapMs := prevEndMs - cur.clip.TimelineStartMs
			if overlapMs > 40 && cur.clip.TransitionIn != domainstudio.TransitionNone {
				overlapSec := math.Min(float64(overlapMs)/1000.0, math.Min(prevDurSec, curDurSec)-0.04)
				if overlapSec > 0.02 {
					offsetSec := math.Max(0, accumSec-overlapSec)
					xf := ffmpegXfadeName(cur.clip.TransitionIn)
					fc = append(fc, fmt.Sprintf("%s%sxfade=transition=%s:duration=%.3f:offset=%.3f%s",
						curLabel, segLabel, xf, overlapSec, offsetSec, nextLabel))
					curLabel = nextLabel
					accumSec = accumSec + curDurSec - overlapSec
					prevEndMs = max(prevEndMs, cur.clip.TimelineEndMs())
					prevDurSec = curDurSec
					continue
				}
			}

			if cur.clip.TimelineStartMs == prevEndMs &&
				(cur.clip.TransitionIn == domainstudio.TransitionCrossfade || cur.clip.TransitionIn == domainstudio.TransitionWipeLeft || cur.clip.TransitionIn == domainstudio.TransitionSlideLeft) &&
				cur.clip.TransitionMs > 0 {
				overlapSec := math.Min(float64(cur.clip.TransitionMs)/1000.0, math.Min(prevDurSec/3, curDurSec/3))
				if overlapSec > 0.04 {
					offsetSec := math.Max(0, accumSec-overlapSec)
					xf := ffmpegXfadeName(cur.clip.TransitionIn)
					fc = append(fc, fmt.Sprintf("%s%sxfade=transition=%s:duration=%.3f:offset=%.3f%s",
						curLabel, segLabel, xf, overlapSec, offsetSec, nextLabel))
					curLabel = nextLabel
					accumSec = accumSec + curDurSec - overlapSec
					prevEndMs = cur.clip.TimelineEndMs()
					prevDurSec = curDurSec
					continue
				}
			}

			if cur.clip.TransitionIn == domainstudio.TransitionFade && cur.clip.TransitionMs > 0 {
				d := math.Min(float64(cur.clip.TransitionMs)/1000.0, curDurSec/2)
				fadedLbl := fmt.Sprintf("[vfaded%d]", i)
				fc = append(fc, fmt.Sprintf("%sfade=t=in:st=0:d=%.3f%s", segLabel, d, fadedLbl))
				segLabel = fadedLbl
			}

			if cur.clip.TimelineStartMs > prevEndMs {
				gapSec := float64(cur.clip.TimelineStartMs-prevEndMs) / 1000.0
				gapLbl := fmt.Sprintf("[vgap%d]", i)
				fc = append(fc,
					fmt.Sprintf("color=c=black:s=%dx%d:r=%d:d=%.3f,settb=AVTB,format=yuv420p%s", tl.Width, tl.Height, tl.FPS, gapSec, gapLbl),
					fmt.Sprintf("%s%s%sconcat=n=3:v=1:a=0%s", curLabel, gapLbl, segLabel, nextLabel),
				)
				accumSec += gapSec + curDurSec
			} else {
				fc = append(fc, fmt.Sprintf("%s%sconcat=n=2:v=1:a=0%s", curLabel, segLabel, nextLabel))
				accumSec += curDurSec
			}
			curLabel = nextLabel
			prevEndMs = max(prevEndMs, cur.clip.TimelineEndMs())
			prevDurSec = curDurSec
		}
		vOutLabel = curLabel
		totalDurSec = math.Max(totalDurSec, accumSec)
	}

	if assBurnPath != "" {
		fc = append(fc, fmt.Sprintf("%sass='%s'[vout]", vOutLabel, escapeFFmpegFilterPath(assBurnPath)))
		vOutLabel = "[vout]"
	}

	// 2. Build multi-track audio mix + loudnorm (-14 LUFS default).
	aOutLabel := "[aout]"
	if len(audioClips) == 0 {
		fc = append(fc, fmt.Sprintf("anullsrc=r=48000:cl=stereo,atrim=duration=%.3f,loudnorm=I=%.1f:TP=-1.5:LRA=11[aout]", totalDurSec, lufs))
	} else {
		var mixLabels []string
		for i, rc := range audioClips {
			inSec := float64(rc.clip.SourceInMs) / 1000.0
			outSec := float64(rc.clip.SourceOutMs) / 1000.0
			spd := rc.clip.Speed
			if spd <= 0 {
				spd = 1.0
			}
			gain := rc.track.Volume * rc.clip.Volume
			if gain < 0 {
				gain = 0
			}
			filters := []string{
				fmt.Sprintf("atrim=start=%.3f:end=%.3f", inSec, outSec),
				"asetpts=PTS-STARTPTS",
			}
			if math.Abs(spd-1.0) > 0.001 {
				filters = append(filters, buildAtempoChain(spd)...)
			}
			filters = append(filters,
				fmt.Sprintf("volume=%.3f", gain),
				"aformat=sample_rates=48000:channel_layouts=stereo",
			)
			if rc.clip.TimelineStartMs > 0 {
				filters = append(filters, fmt.Sprintf("adelay=%d|%d", rc.clip.TimelineStartMs, rc.clip.TimelineStartMs))
			}
			lbl := fmt.Sprintf("[aud%d]", i)
			fc = append(fc, fmt.Sprintf("[%d:a]%s%s", rc.inputIndex, strings.Join(filters, ","), lbl))
			mixLabels = append(mixLabels, lbl)
		}
		if len(mixLabels) == 1 {
			fc = append(fc, fmt.Sprintf("%sloudnorm=I=%.1f:TP=-1.5:LRA=11[aout]", mixLabels[0], lufs))
		} else {
			fc = append(fc, fmt.Sprintf("%samix=inputs=%d:duration=longest:dropout_transition=0:normalize=0,loudnorm=I=%.1f:TP=-1.5:LRA=11[aout]",
				strings.Join(mixLabels, ""), len(mixLabels), lufs))
		}
	}

	args = append(args,
		"-filter_complex", strings.Join(fc, ";"),
		"-map", vOutLabel,
		"-map", aOutLabel,
		"-c:v", "libx264",
		"-preset", preset,
		"-crf", strconv.Itoa(crf),
		"-pix_fmt", "yuv420p",
		"-c:a", "aac",
		"-b:a", "192k",
		"-movflags", "+faststart",
		outPath,
	)
	return args, totalDurSec
}

func kenBurnsFilter(preset domainstudio.KenBurnsPreset, w, h, fps int, durSec float64) string {
	frames := max(1, int(math.Round(durSec*float64(fps))))
	switch preset {
	case domainstudio.KenBurnsZoomIn:
		return fmt.Sprintf(",zoompan=z='min(zoom+0.0012,1.15)':x='iw/2-(iw/zoom/2)':y='ih/2-(ih/zoom/2)':d=%d:s=%dx%d:fps=%d", frames, w, h, fps)
	case domainstudio.KenBurnsZoomOut:
		return fmt.Sprintf(",zoompan=z='if(lte(zoom,1.0),1.15,max(1.0,zoom-0.0012))':x='iw/2-(iw/zoom/2)':y='ih/2-(ih/zoom/2)':d=%d:s=%dx%d:fps=%d", frames, w, h, fps)
	case domainstudio.KenBurnsPanLeft:
		return fmt.Sprintf(",zoompan=z='1.10':x='iw*0.09*(1-on/%d)':y='ih/2-(ih/zoom/2)':d=%d:s=%dx%d:fps=%d", frames, frames, w, h, fps)
	case domainstudio.KenBurnsPanRight:
		return fmt.Sprintf(",zoompan=z='1.10':x='iw*0.09*(on/%d)':y='ih/2-(ih/zoom/2)':d=%d:s=%dx%d:fps=%d", frames, frames, w, h, fps)
	default:
		return ""
	}
}

func ffmpegXfadeName(t domainstudio.TransitionType) string {
	switch t {
	case domainstudio.TransitionFade:
		return "fadeblack"
	case domainstudio.TransitionWipeLeft:
		return "wipeleft"
	case domainstudio.TransitionSlideLeft:
		return "slideleft"
	default:
		return "fade"
	}
}

func escapeFFmpegFilterPath(p string) string {
	slashed := filepath.ToSlash(p)
	replacer := strings.NewReplacer(
		`\`, `\\`,
		`:`, `\:`,
		`'`, `\'`,
		`[`, `\[`,
		`]`, `\]`,
	)
	return replacer.Replace(slashed)
}

func formatSRT(cues []domainstudio.SubtitleCue) string {
	var b strings.Builder
	for i, c := range cues {
		text := strings.TrimSpace(c.Text)
		if text == "" {
			continue
		}
		if c.Speaker != "" && !strings.HasPrefix(text, c.Speaker+":") {
			text = c.Speaker + ": " + text
		}
		fmt.Fprintf(&b, "%d\n%s --> %s\n%s\n\n", i+1, formatSRTTimestamp(c.StartMs), formatSRTTimestamp(c.EndMs), text)
	}
	return b.String()
}

func formatSRTTimestamp(ms int64) string {
	if ms < 0 {
		ms = 0
	}
	hrs := ms / 3_600_000
	rem := ms % 3_600_000
	mins := rem / 60_000
	rem %= 60_000
	secs := rem / 1000
	millis := rem % 1000
	return fmt.Sprintf("%02d:%02d:%02d,%03d", hrs, mins, secs, millis)
}

func formatASS(cues []domainstudio.SubtitleCue, width, height, fontSize int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[Script Info]\nScriptType: v4.00+\nWrapStyle: 0\nScaledBorderAndShadow: yes\nPlayResX: %d\nPlayResY: %d\n\n", width, height)
	b.WriteString("[V4+ Styles]\n")
	b.WriteString("Format: Name, Fontname, Fontsize, PrimaryColour, SecondaryColour, OutlineColour, BackColour, Bold, Italic, Underline, StrikeOut, ScaleX, ScaleY, Spacing, Angle, BorderStyle, Outline, Shadow, Alignment, MarginL, MarginR, MarginV, Encoding\n")
	fmt.Fprintf(&b, "Style: Default,Arial,%d,&H00FFFFFF,&H000000FF,&H00000000,&H80000000,-1,0,0,0,100,100,0,0,1,2.2,1.0,2,28,28,36,1\n", fontSize)
	fmt.Fprintf(&b, "Style: Top,Arial,%d,&H00FFFFFF,&H000000FF,&H00000000,&H80000000,-1,0,0,0,100,100,0,0,1,2.2,1.0,8,28,28,36,1\n\n", fontSize)
	b.WriteString("[Events]\nFormat: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text\n")
	for _, c := range cues {
		text := strings.TrimSpace(c.Text)
		if text == "" {
			continue
		}
		style := "Default"
		if strings.EqualFold(c.StylePreset, "top") {
			style = "Top"
		}
		escText := strings.NewReplacer("{", `\{`, "}", `\}`, "\r\n", `\N`, "\n", `\N`).Replace(text)
		fmt.Fprintf(&b, "Dialogue: 0,%s,%s,%s,%s,0,0,0,,%s\n",
			formatASSTimestamp(c.StartMs), formatASSTimestamp(c.EndMs), style, strings.ReplaceAll(c.Speaker, ",", " "), escText)
	}
	return b.String()
}

func formatASSTimestamp(ms int64) string {
	if ms < 0 {
		ms = 0
	}
	hrs := ms / 3_600_000
	rem := ms % 3_600_000
	mins := rem / 60_000
	rem %= 60_000
	secs := rem / 1000
	cs := (rem % 1000) / 10
	return fmt.Sprintf("%d:%02d:%02d.%02d", hrs, mins, secs, cs)
}

func sanitizeExportFileBase(raw, fallbackStem, wantExt string) string {
	cleaned := filepath.Base(strings.TrimSpace(raw))
	if cleaned == "" || cleaned == "." || cleaned == ".." {
		cleaned = fallbackStem + wantExt
	}
	var b strings.Builder
	for _, r := range cleaned {
		if strings.ContainsRune(`<>:"/\|?*`, r) || r < 32 {
			b.WriteByte('_')
		} else {
			b.WriteRune(r)
		}
	}
	out := b.String()
	if !strings.EqualFold(filepath.Ext(out), wantExt) {
		out = strings.TrimSuffix(out, filepath.Ext(out)) + wantExt
	}
	return out
}

func isImageExtension(p string) bool {
	switch strings.ToLower(filepath.Ext(p)) {
	case ".png", ".jpg", ".jpeg", ".webp", ".bmp":
		return true
	default:
		return false
	}
}
