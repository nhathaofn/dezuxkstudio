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
	"app/internal/config"
	"app/internal/contracts"
	domainbinaries "app/internal/domain/binaries"
	domainstudio "app/internal/domain/studio"
	"app/internal/engine"
	"app/internal/fsutil"
	"app/internal/progress"
	"app/internal/service/media"
	"app/internal/tools/mediautil"
)

const (
	// minSubtitleFontSize and maxSubtitleFontSize match config.Validate bounds on studio.export_subtitle_font_size.
	minSubtitleFontSize = 8
	maxSubtitleFontSize = 96

	// minLoudnormLUFS and maxLoudnormLUFS match config.Validate bounds on studio.export_loudnorm_lufs.
	minLoudnormLUFS = -36.0
	maxLoudnormLUFS = -5.0

	// minH264CRF and maxH264CRF are libx264's documented constant-rate-factor range [0..51].
	minH264CRF = 0
	maxH264CRF = 51

	// loudnormTruePeakDB and loudnormRangeLU are EBU R128 / ITU-R BS.1770 streaming loudness envelope standards.
	loudnormTruePeakDB = -1.5
	loudnormRangeLU    = 11

	// minXfadeOverlapMs and xfadeGuardSec keep FFmpeg xfade durations strictly positive and inside clip bounds.
	minXfadeOverlapMs = 40
	xfadeGuardSec     = 0.04
	minXfadeSec       = 0.02

	// kenBurnsZoomStep, kenBurnsMaxZoom, kenBurnsPanZoom, and kenBurnsPanSpan define smooth 24..60fps still-image camera motion.
	kenBurnsZoomStep = 0.0012
	kenBurnsMaxZoom  = 1.15
	kenBurnsPanZoom  = 1.10
	kenBurnsPanSpan  = 0.09

	// assOutlinePx, assShadowPx, assMarginH, and assMarginV define readable SubStation Alpha v4+ subtitle styling.
	assOutlinePx = 2.2
	assShadowPx  = 1.0
	assMarginH   = 28
	assMarginV   = 36
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
	validPresets := config.ValidFFmpegPresets()

	spec := engine.Spec{
		Type:        NodeTypeRenderTimeline,
		Provider:    studioProviderID,
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
			{Name: "subtitle_font_size", Label: "Subtitle Font Size", Kind: engine.ParamInteger, Min: minSubtitleFontSize, Max: maxSubtitleFontSize, Default: strconv.Itoa(defFontSize)},
			{Name: "loudnorm_lufs", Label: "Loudnorm Target (LUFS)", Kind: engine.ParamText, Default: defLUFS},
			{Name: "crf", Label: "FFmpeg CRF (0-51)", Kind: engine.ParamInteger, Min: minH264CRF, Max: maxH264CRF, Default: strconv.Itoa(defCRF)},
			{Name: "preset", Label: "FFmpeg Preset", Kind: engine.ParamSelect, Options: validPresets, Default: defPreset},
			{Name: "overwrite", Label: "Overwrite Existing Output", Kind: engine.ParamToggle, Default: "false", Destructive: true},
		},
	}

	return engine.Typed(spec, func(ctx context.Context, in contracts.RenderTimelineInput) (contracts.RenderTimelineOutput, error) {
		trimmed := strings.TrimSpace(in.TimelineJSON)
		if trimmed == "" {
			return contracts.RenderTimelineOutput{}, apperr.Validation("timeline_json is required").
				WithMsg("errors.studio.timeline_required", nil)
		}
		var tl domainstudio.EditTimeline
		if err := json.Unmarshal([]byte(trimmed), &tl); err != nil {
			return contracts.RenderTimelineOutput{}, apperr.Wrap(apperr.CodeValidation, "invalid timeline_json", err).
				WithMsg("errors.studio.invalid_timeline_json", nil)
		}
		if err := tl.ValidateWithMaxDim(opts.StudioConfig.MaxVideoDimension); err != nil {
			return contracts.RenderTimelineOutput{}, err
		}

		if in.SubtitleFontSize < 0 || (in.SubtitleFontSize > 0 && (in.SubtitleFontSize < minSubtitleFontSize || in.SubtitleFontSize > maxSubtitleFontSize)) {
			return contracts.RenderTimelineOutput{}, apperr.Validation(fmt.Sprintf("subtitle_font_size must be between %d and %d", minSubtitleFontSize, maxSubtitleFontSize)).
				WithMsg("errors.studio.invalid_subtitle_font_size", map[string]any{"min": minSubtitleFontSize, "max": maxSubtitleFontSize})
		}
		fontSize := in.SubtitleFontSize
		if fontSize == 0 {
			fontSize = opts.StudioConfig.ExportSubtitleFontSize
		}

		lufs := in.LoudnormLUFS
		if lufs == 0 {
			lufs = opts.StudioConfig.ExportLoudnormLUFS
		}
		if math.IsNaN(lufs) || math.IsInf(lufs, 0) || lufs < minLoudnormLUFS || lufs > maxLoudnormLUFS {
			return contracts.RenderTimelineOutput{}, apperr.Validation(fmt.Sprintf("loudnorm_lufs must be between %.0f and %.0f", minLoudnormLUFS, maxLoudnormLUFS)).
				WithMsg("errors.studio.invalid_loudnorm_lufs", nil)
		}

		crf := opts.StudioConfig.FFmpegCRF
		if in.CRF != nil {
			crf = *in.CRF
		}
		if crf < minH264CRF || crf > maxH264CRF {
			return contracts.RenderTimelineOutput{}, apperr.Validation(fmt.Sprintf("crf must be between %d and %d", minH264CRF, maxH264CRF)).
				WithMsg("errors.studio.invalid_crf", nil)
		}
		preset := strings.TrimSpace(in.Preset)
		if preset == "" {
			preset = opts.StudioConfig.FFmpegPreset
		}
		if !slices.Contains(validPresets, preset) {
			return contracts.RenderTimelineOutput{}, apperr.Validation(fmt.Sprintf("unsupported ffmpeg preset %q", preset)).
				WithMsg("errors.studio.invalid_preset", map[string]any{"preset": preset})
		}

		outDir, err := resolveNodeOutputDir(ctx, opts, in.OutputDir, filepath.Join("studio", "exports"))
		if err != nil {
			return contracts.RenderTimelineOutput{}, err
		}

		baseName := sanitizeExportFileBase(in.Filename, "timeline_final", ".mp4")
		outPath := deduplicateOutputPath(filepath.Join(outDir, baseName), in.Overwrite)
		if err := checkStudioFile(ctx, outPath, true); err != nil {
			return contracts.RenderTimelineOutput{}, err
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

		totalClips := 0
		for _, tr := range tl.Tracks {
			totalClips += len(tr.Clips)
		}
		progress.Report(ctx, progress.Update{
			Percent: 5,
			Message: "Validating timeline media sources…",
			Key:     "studio.progress.render_prepare",
			Params: map[string]string{
				"current": "0",
				"total":   strconv.Itoa(totalClips),
			},
		})

		var resolved []resolvedTimelineClip
		inputIdx := 0
		for ti, tr := range tl.Tracks {
			allowedKind := inputKindVideo | inputKindImage
			if tr.Kind != domainstudio.TrackVideo {
				allowedKind = inputKindAudio
			}
			for _, cl := range tr.Clips {
				if err := ctx.Err(); err != nil {
					return contracts.RenderTimelineOutput{}, err
				}
				if strings.TrimSpace(cl.FilePath) == "" {
					return contracts.RenderTimelineOutput{}, apperr.Validation(fmt.Sprintf("clip %q requires file_path for rendering", cl.ID)).
						WithMsg("errors.studio.clip_path_required", nil)
				}
				p, rerr := resolveMediaInput(ctx, opts, cl.FilePath, allowedKind)
				if rerr != nil {
					return contracts.RenderTimelineOutput{}, rerr
				}
				if _, serr := os.Stat(p); serr != nil {
					return contracts.RenderTimelineOutput{}, apperr.NotFound(fmt.Sprintf("timeline media file not found: %s", filepath.Base(p))).
						WithMsg("errors.studio.media_not_found", nil)
				}
				cl.FilePath = p
				isImg := mediautil.IsSupportedImageExt(p)
				hasAudio := !isImg
				if !isImg && ffprobeExe != "" {
					probeCtx, cancel := context.WithTimeout(ctx, opts.StudioConfig.FFprobeTimeout)
					stop := progress.KeepAlive(ctx)
					pOut, perr := probeWithFFprobe(probeCtx, ffprobeExe, p)
					stop()
					cancel()
					if perr == nil {
						hasAudio = pOut.HasAudio
					}
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
				progress.Report(ctx, progress.Update{
					Percent: 5.0 + float64(inputIdx)/float64(max(1, totalClips))*5.0,
					Message: fmt.Sprintf("Validated timeline clip %d/%d…", inputIdx, totalClips),
					Key:     "studio.progress.render_prepare",
					Params: map[string]string{
						"current": strconv.Itoa(inputIdx),
						"total":   strconv.Itoa(totalClips),
					},
				})
			}
		}

		if len(resolved) == 0 && len(tl.Subtitles) == 0 {
			return contracts.RenderTimelineOutput{}, apperr.Validation("timeline has no clips or subtitles to render").
				WithMsg("errors.studio.empty_timeline", nil)
		}

		var srtPath, assPath, tmpSrt, tmpAss string
		burnPath := ""
		if len(tl.Subtitles) > 0 {
			stem := strings.TrimSuffix(filepath.Base(outPath), filepath.Ext(outPath))
			srtPath = deduplicateOutputPath(filepath.Join(outDir, stem+".srt"), in.Overwrite)
			assPath = deduplicateOutputPath(filepath.Join(outDir, stem+".ass"), in.Overwrite)
			if err := checkStudioFile(ctx, srtPath, true); err != nil {
				return contracts.RenderTimelineOutput{}, err
			}
			if err := checkStudioFile(ctx, assPath, true); err != nil {
				return contracts.RenderTimelineOutput{}, err
			}
		}

		tmpOut, err := createTempMediaFile(outDir, ".tmp-render-", ".mp4")
		if err != nil {
			return contracts.RenderTimelineOutput{}, err
		}
		defer func() {
			_ = os.Remove(tmpOut)
			if tmpSrt != "" {
				_ = os.Remove(tmpSrt)
			}
			if tmpAss != "" {
				_ = os.Remove(tmpAss)
			}
		}()

		var expectedDurSec float64
		var shifts []renderTimeShift
		// Compute filter shifts & expected duration first so shifted subtitles match the rendered video timeline.
		args, expectedDurSec, shifts := buildRenderTimelineArgs(tl, resolved, "", lufs, preset, crf, opts.StudioConfig, tmpOut)

		if len(tl.Subtitles) > 0 {
			shiftedSubs := shiftSubtitleCues(tl.Subtitles, shifts)
			var terr error
			tmpSrt, terr = createTempMediaFile(outDir, ".tmp-render-", ".srt")
			if terr != nil {
				return contracts.RenderTimelineOutput{}, terr
			}
			if err := fsutil.WriteFileAtomic(tmpSrt, []byte(formatSRT(shiftedSubs)), 0o600); err != nil {
				return contracts.RenderTimelineOutput{}, ioUnavailableErr("Failed to write temporary rendered SRT subtitle file", err)
			}
			tmpAss, terr = createTempMediaFile(outDir, ".tmp-render-", ".ass")
			if terr != nil {
				return contracts.RenderTimelineOutput{}, terr
			}
			assContent := formatASS(shiftedSubs, tl.Width, tl.Height, fontSize, opts.StudioConfig.ExportSubtitleFontFamily)
			if err := fsutil.WriteFileAtomic(tmpAss, []byte(assContent), 0o600); err != nil {
				return contracts.RenderTimelineOutput{}, ioUnavailableErr("Failed to write temporary rendered ASS subtitle file", err)
			}
			if in.BurnSubtitles {
				burnPath = tmpAss
				args, expectedDurSec, shifts = buildRenderTimelineArgs(tl, resolved, burnPath, lufs, preset, crf, opts.StudioConfig, tmpOut)
			}
		}

		if err := runRenderTimelinePass(ctx, ffmpegExe, outDir, tl, resolved, burnPath, lufs, preset, crf, opts.StudioConfig, tmpOut, args, expectedDurSec); err != nil {
			return contracts.RenderTimelineOutput{}, err
		}

		if err := commitTempMediaFile(tmpOut, outPath); err != nil {
			return contracts.RenderTimelineOutput{}, err
		}
		if tmpSrt != "" && srtPath != "" {
			if err := commitTempMediaFile(tmpSrt, srtPath); err != nil {
				return contracts.RenderTimelineOutput{}, err
			}
			tmpSrt = ""
		}
		if tmpAss != "" && assPath != "" {
			if err := commitTempMediaFile(tmpAss, assPath); err != nil {
				return contracts.RenderTimelineOutput{}, err
			}
			tmpAss = ""
		}

		finalDurSec := expectedDurSec
		if ffprobeExe != "" {
			probeCtx, cancelProbe := context.WithTimeout(ctx, opts.StudioConfig.FFprobeTimeout)
			stopProbe := progress.KeepAlive(ctx)
			pOut, perr := probeWithFFprobe(probeCtx, ffprobeExe, outPath)
			stopProbe()
			cancelProbe()
			if perr == nil && pOut.DurationSec > 0 {
				finalDurSec = pOut.DurationSec
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
				SourceNode:     NodeTypeRenderTimeline,
				FilePath:       outPath,
				RegisteredRoot: outDir,
				MIMEType:       "video/mp4",
				SizeBytes:      sizeBytes,
				Width:          tl.Width,
				Height:         tl.Height,
				DurationSec:    finalDurSec,
			})
			if rerr != nil {
				if opts.Logger != nil {
					opts.Logger.WarnContext(ctx, "studio: failed to register rendered timeline video",
						"source_node", NodeTypeRenderTimeline,
						"error", rerr,
					)
				}
			} else if item != nil {
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

type renderTimeShift struct {
	fromTimelineMs int64
	deltaMs        int64
}

func shiftTimelineMs(shifts []renderTimeShift, timelineMs int64) int64 {
	delta := int64(0)
	for _, s := range shifts {
		if timelineMs >= s.fromTimelineMs {
			delta = s.deltaMs
		}
	}
	return max(0, timelineMs+delta)
}

func shiftSubtitleCues(cues []domainstudio.SubtitleCue, shifts []renderTimeShift) []domainstudio.SubtitleCue {
	if len(cues) == 0 || len(shifts) == 0 {
		return cues
	}
	out := make([]domainstudio.SubtitleCue, len(cues))
	for i, c := range cues {
		out[i] = c
		out[i].StartMs = shiftTimelineMs(shifts, c.StartMs)
		out[i].EndMs = max(out[i].StartMs+1, shiftTimelineMs(shifts, c.EndMs))
	}
	return out
}

func buildRenderTimelineArgs(
	tl domainstudio.EditTimeline,
	resolved []resolvedTimelineClip,
	assBurnPath string,
	lufs float64,
	preset string,
	crf int,
	cfg config.StudioConfig,
	outPath string,
) (args []string, totalDurSec float64, shifts []renderTimeShift) {
	cfg = withStudioDefaults(cfg)
	args = []string{"-y", "-hide_banner", "-progress", "pipe:1", "-nostats"}

	for _, rc := range resolved {
		if rc.isImage && rc.clip.KenBurnsPreset == domainstudio.KenBurnsNone {
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
		if tr.Kind != domainstudio.TrackText && tr.Solo {
			anyAudioSolo = true
			break
		}
	}

	for _, rc := range resolved {
		switch rc.track.Kind {
		case domainstudio.TrackVideo:
			videoClips = append(videoClips, rc)
			if rc.hasAudio && !rc.track.Muted && !rc.clip.Mute && (!anyAudioSolo || rc.track.Solo) {
				audioClips = append(audioClips, rc)
			}
		case domainstudio.TrackAudioDialogue, domainstudio.TrackAudioNarration, domainstudio.TrackAudioBGM, domainstudio.TrackAudioSFX:
			if !rc.track.Muted && !rc.clip.Mute && (!anyAudioSolo || rc.track.Solo) {
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

	vOutLabel := "[vbase]"
	if len(videoClips) == 0 {
		fc = append(fc, fmt.Sprintf("color=c=black:s=%dx%d:r=%d:d=%.3f,settb=AVTB,format=%s[vbase]", tl.Width, tl.Height, tl.FPS, totalDurSec, cfg.FFmpegPixelFormat))
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
				if rc.clip.KenBurnsPreset != domainstudio.KenBurnsNone {
					seg = fmt.Sprintf("[%d:v]scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2,setsar=1%s,settb=AVTB,format=%s[vseg%d]",
						rc.inputIndex, tl.Width, tl.Height, tl.Width, tl.Height, kenBurnsFilter(rc.clip.KenBurnsPreset, tl.Width, tl.Height, tl.FPS, durSec), cfg.FFmpegPixelFormat, i)
				} else {
					seg = fmt.Sprintf("[%d:v]scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2,setsar=1,fps=%d,settb=AVTB,format=%s[vseg%d]",
						rc.inputIndex, tl.Width, tl.Height, tl.Width, tl.Height, tl.FPS, cfg.FFmpegPixelFormat, i)
				}
			} else {
				seg = fmt.Sprintf("[%d:v]trim=start=%.3f:end=%.3f,setpts=(PTS-STARTPTS)/%.4f,scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2,setsar=1,fps=%d,settb=AVTB,format=%s[vseg%d]",
					rc.inputIndex, inSec, outSec, spd, tl.Width, tl.Height, tl.Width, tl.Height, tl.FPS, cfg.FFmpegPixelFormat, i)
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
				fmt.Sprintf("color=c=black:s=%dx%d:r=%d:d=%.3f,settb=AVTB,format=%s[vgap0]", tl.Width, tl.Height, tl.FPS, gapSec, cfg.FFmpegPixelFormat),
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
			if overlapMs > minXfadeOverlapMs && cur.clip.TransitionIn != domainstudio.TransitionNone {
				overlapSec := math.Min(float64(overlapMs)/1000.0, math.Min(prevDurSec, curDurSec)-xfadeGuardSec)
				if overlapSec > minXfadeSec {
					offsetSec := math.Max(0, accumSec-overlapSec)
					xf := ffmpegXfadeName(cur.clip.TransitionIn)
					fc = append(fc, fmt.Sprintf("%s%sxfade=transition=%s:duration=%.3f:offset=%.3f%s",
						curLabel, segLabel, xf, overlapSec, offsetSec, nextLabel))
					curLabel = nextLabel
					accumSec = accumSec + curDurSec - overlapSec
					prevEndMs = max(prevEndMs, cur.clip.TimelineEndMs())
					prevDurSec = curDurSec
					if delta := int64(math.Round(accumSec*1000)) - prevEndMs; delta != 0 {
						shifts = append(shifts, renderTimeShift{fromTimelineMs: cur.clip.TimelineStartMs, deltaMs: delta})
					}
					continue
				}
			}

			if cur.clip.TimelineStartMs == prevEndMs &&
				(cur.clip.TransitionIn == domainstudio.TransitionCrossfade || cur.clip.TransitionIn == domainstudio.TransitionWipeLeft || cur.clip.TransitionIn == domainstudio.TransitionSlideLeft) &&
				cur.clip.TransitionMs > 0 {
				overlapSec := math.Min(float64(cur.clip.TransitionMs)/1000.0, math.Min(prevDurSec/3, curDurSec/3))
				if overlapSec > xfadeGuardSec {
					offsetSec := math.Max(0, accumSec-overlapSec)
					xf := ffmpegXfadeName(cur.clip.TransitionIn)
					fc = append(fc, fmt.Sprintf("%s%sxfade=transition=%s:duration=%.3f:offset=%.3f%s",
						curLabel, segLabel, xf, overlapSec, offsetSec, nextLabel))
					curLabel = nextLabel
					accumSec = accumSec + curDurSec - overlapSec
					prevEndMs = cur.clip.TimelineEndMs()
					prevDurSec = curDurSec
					if delta := int64(math.Round(accumSec*1000)) - prevEndMs; delta != 0 {
						shifts = append(shifts, renderTimeShift{fromTimelineMs: cur.clip.TimelineStartMs, deltaMs: delta})
					}
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
					fmt.Sprintf("color=c=black:s=%dx%d:r=%d:d=%.3f,settb=AVTB,format=%s%s", tl.Width, tl.Height, tl.FPS, gapSec, cfg.FFmpegPixelFormat, gapLbl),
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
			if delta := int64(math.Round(accumSec*1000)) - prevEndMs; delta != 0 {
				shifts = append(shifts, renderTimeShift{fromTimelineMs: cur.clip.TimelineStartMs, deltaMs: delta})
			}
		}
		vOutLabel = curLabel
		totalDurSec = math.Max(1.0, accumSec)
	}

	if len(videoClips) > 0 {
		for _, sub := range shiftSubtitleCues(tl.Subtitles, shifts) {
			if endSec := float64(sub.EndMs) / 1000.0; endSec > totalDurSec {
				totalDurSec = endSec
			}
		}
		for _, rc := range audioClips {
			startMs := shiftTimelineMs(shifts, rc.clip.TimelineStartMs)
			if endSec := float64(startMs+rc.clip.TimelineSpanMs()) / 1000.0; endSec > totalDurSec {
				totalDurSec = endSec
			}
		}
	}

	if assBurnPath != "" {
		fc = append(fc, fmt.Sprintf("%sass=filename=%s[vout]", vOutLabel, escapeFFmpegFilterPath(assBurnPath)))
		vOutLabel = "[vout]"
	}

	aOutLabel := "[aout]"
	if len(audioClips) == 0 {
		fc = append(fc, fmt.Sprintf("anullsrc=r=%d:cl=stereo,atrim=duration=%.3f,loudnorm=I=%.1f:TP=%.1f:LRA=%d[aout]",
			cfg.FFmpegAudioSampleRate, totalDurSec, lufs, loudnormTruePeakDB, loudnormRangeLU))
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
				fmt.Sprintf("aformat=sample_rates=%d:channel_layouts=stereo", cfg.FFmpegAudioSampleRate),
			)
			delayMs := shiftTimelineMs(shifts, rc.clip.TimelineStartMs)
			if delayMs > 0 {
				filters = append(filters, fmt.Sprintf("adelay=%d|%d", delayMs, delayMs))
			}
			lbl := fmt.Sprintf("[aud%d]", i)
			fc = append(fc, fmt.Sprintf("[%d:a]%s%s", rc.inputIndex, strings.Join(filters, ","), lbl))
			mixLabels = append(mixLabels, lbl)
		}
		if len(mixLabels) == 1 {
			fc = append(fc, fmt.Sprintf("%sloudnorm=I=%.1f:TP=%.1f:LRA=%d[aout]",
				mixLabels[0], lufs, loudnormTruePeakDB, loudnormRangeLU))
		} else {
			fc = append(fc, fmt.Sprintf("%samix=inputs=%d:duration=longest:dropout_transition=0:normalize=0,loudnorm=I=%.1f:TP=%.1f:LRA=%d[aout]",
				strings.Join(mixLabels, ""), len(mixLabels), lufs, loudnormTruePeakDB, loudnormRangeLU))
		}
	}

	args = append(args,
		"-filter_complex", strings.Join(fc, ";"),
		"-map", vOutLabel,
		"-map", aOutLabel,
		"-c:v", cfg.FFmpegVideoCodec,
		"-preset", preset,
		"-crf", strconv.Itoa(crf),
		"-pix_fmt", cfg.FFmpegPixelFormat,
		"-c:a", cfg.FFmpegAudioCodec,
		"-b:a", cfg.FFmpegAudioBitrate,
		"-movflags", "+faststart",
		outPath,
	)
	return args, totalDurSec, shifts
}

func kenBurnsFilter(preset domainstudio.KenBurnsPreset, w, h, fps int, durSec float64) string {
	frames := max(1, int(math.Round(durSec*float64(fps))))
	switch preset {
	case domainstudio.KenBurnsZoomIn:
		return fmt.Sprintf(",zoompan=z='min(zoom+%.4f,%.2f)':x='iw/2-(iw/zoom/2)':y='ih/2-(ih/zoom/2)':d=%d:s=%dx%d:fps=%d",
			kenBurnsZoomStep, kenBurnsMaxZoom, frames, w, h, fps)
	case domainstudio.KenBurnsZoomOut:
		return fmt.Sprintf(",zoompan=z='if(lte(zoom,1.0),%.2f,max(1.0,zoom-%.4f))':x='iw/2-(iw/zoom/2)':y='ih/2-(ih/zoom/2)':d=%d:s=%dx%d:fps=%d",
			kenBurnsMaxZoom, kenBurnsZoomStep, frames, w, h, fps)
	case domainstudio.KenBurnsPanLeft:
		return fmt.Sprintf(",zoompan=z='%.2f':x='iw*%.2f*(1-on/%d)':y='ih/2-(ih/zoom/2)':d=%d:s=%dx%d:fps=%d",
			kenBurnsPanZoom, kenBurnsPanSpan, frames, frames, w, h, fps)
	case domainstudio.KenBurnsPanRight:
		return fmt.Sprintf(",zoompan=z='%.2f':x='iw*%.2f*(on/%d)':y='ih/2-(ih/zoom/2)':d=%d:s=%dx%d:fps=%d",
			kenBurnsPanZoom, kenBurnsPanSpan, frames, frames, w, h, fps)
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

// escapeFFmpegFilterPath applies FFmpeg's two-level filtergraph escaping:
// first for the filter option parser (\, :, '), then for the filtergraph parser (\, ', [, ], ,, ;, space).
func escapeFFmpegFilterPath(p string) string {
	slashed := filepath.ToSlash(p)
	optEscaped := strings.NewReplacer(
		`\`, `\\`,
		`:`, `\:`,
		`'`, `\'`,
	).Replace(slashed)
	return strings.NewReplacer(
		`\`, `\\`,
		`'`, `\'`,
		`[`, `\[`,
		`]`, `\]`,
		`,`, `\,`,
		`;`, `\;`,
		` `, `\ `,
	).Replace(optEscaped)
}

func formatSRT(cues []domainstudio.SubtitleCue) string {
	var b strings.Builder
	seq := 0
	for _, c := range cues {
		text := strings.TrimSpace(c.Text)
		if text == "" {
			continue
		}
		seq++
		if c.Speaker != "" && !strings.HasPrefix(text, c.Speaker+":") {
			text = c.Speaker + ": " + text
		}
		fmt.Fprintf(&b, "%d\n%s --> %s\n%s\n\n", seq, formatSRTTimestamp(c.StartMs), formatSRTTimestamp(c.EndMs), text)
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

func formatASS(cues []domainstudio.SubtitleCue, width, height, fontSize int, fontFamily string) string {
	font := strings.ReplaceAll(strings.TrimSpace(fontFamily), ",", " ")
	if font == "" {
		font = withStudioDefaults(config.StudioConfig{}).ExportSubtitleFontFamily
	}
	var b strings.Builder
	fmt.Fprintf(&b, "[Script Info]\nScriptType: v4.00+\nWrapStyle: 0\nScaledBorderAndShadow: yes\nPlayResX: %d\nPlayResY: %d\n\n", width, height)
	b.WriteString("[V4+ Styles]\n")
	b.WriteString("Format: Name, Fontname, Fontsize, PrimaryColour, SecondaryColour, OutlineColour, BackColour, Bold, Italic, Underline, StrikeOut, ScaleX, ScaleY, Spacing, Angle, BorderStyle, Outline, Shadow, Alignment, MarginL, MarginR, MarginV, Encoding\n")
	fmt.Fprintf(&b, "Style: Default,%s,%d,&H00FFFFFF,&H000000FF,&H00000000,&H80000000,-1,0,0,0,100,100,0,0,1,%.1f,%.1f,2,%d,%d,%d,1\n",
		font, fontSize, assOutlinePx, assShadowPx, assMarginH, assMarginH, assMarginV)
	fmt.Fprintf(&b, "Style: Top,%s,%d,&H00FFFFFF,&H000000FF,&H00000000,&H80000000,-1,0,0,0,100,100,0,0,1,%.1f,%.1f,8,%d,%d,%d,1\n\n",
		font, fontSize, assOutlinePx, assShadowPx, assMarginH, assMarginH, assMarginV)
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
