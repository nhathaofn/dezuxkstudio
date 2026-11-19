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

	"app/internal/config"
	domainstudio "app/internal/domain/studio"
	"app/internal/fsutil"
	"app/internal/runner"
)

const (
	// maxDirectFFmpegClips caps input count per FFmpeg process to bound simultaneous decoders.
	maxDirectFFmpegClips = 24

	// maxFFmpegArgvBytes stays safely under Windows' 32,767-char CreateProcessW command line limit.
	maxFFmpegArgvBytes = 16 * 1024
)

func ffmpegArgvBytes(args []string) int {
	total := 0
	for _, a := range args {
		total += len(a) + 1
	}
	return total
}

func needsFFmpegBatching(args []string, clipCount int) bool {
	return clipCount > maxDirectFFmpegClips || ffmpegArgvBytes(args) > maxFFmpegArgvBytes
}

func writeFFmpegConcatList(listPath string, segPaths []string) error {
	var b strings.Builder
	for _, p := range segPaths {
		slashed := filepath.ToSlash(p)
		escaped := strings.ReplaceAll(slashed, "'", `'\''`)
		fmt.Fprintf(&b, "file '%s'\n", escaped)
	}
	if err := fsutil.WriteFileAtomic(listPath, []byte(b.String()), 0o600); err != nil {
		return ioUnavailableErr("Failed to write FFmpeg concat segment list", err)
	}
	return nil
}

func runConcatBatch(
	ctx context.Context,
	ffmpegExe, outDir string,
	clips []manifestClip,
	hasAudio []bool,
	narrationPath string,
	width, height, fps int,
	cfg config.StudioConfig,
	totalDurSec float64,
	tmpOut string,
) error {
	cfg = withStudioDefaults(cfg)
	directArgs := buildFFmpegConcatArgs(clips, hasAudio, narrationPath, width, height, fps, cfg, tmpOut)
	if !needsFFmpegBatching(directArgs, len(clips)) {
		runCtx, cancel := context.WithTimeout(ctx, cfg.FFmpegTimeout)
		defer cancel()
		runOpts := runner.FFmpegPipeProgressOptions(ctx, int64(totalDurSec*microsecondsPerSecond), "studio.progress.concat_render", "Rendering assembled video")
		res, err := runner.RunWithOptions(runCtx, ffmpegExe, directArgs, runOpts)
		if err != nil || res == nil || res.ExitCode != 0 {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return ioUnavailableErr("FFmpeg video assembly failed", err)
		}
		return nil
	}

	var segPaths []string
	var listPath string
	defer func() {
		for _, p := range segPaths {
			_ = os.Remove(p)
		}
		if listPath != "" {
			_ = os.Remove(listPath)
		}
	}()

	chunkSize := maxDirectFFmpegClips
	for start := 0; start < len(clips); {
		if err := ctx.Err(); err != nil {
			return err
		}
		end := min(len(clips), start+chunkSize)
		segOut, terr := createTempMediaFile(outDir, ".tmp-cseg-", ".mp4")
		if terr != nil {
			return terr
		}
		segPaths = append(segPaths, segOut)

		var chunkArgs []string
		for {
			chunkArgs = buildFFmpegConcatArgs(clips[start:end], hasAudio[start:end], "", width, height, fps, cfg, segOut)
			if end-start <= 1 || ffmpegArgvBytes(chunkArgs) <= maxFFmpegArgvBytes {
				break
			}
			end = start + max(1, (end-start)/2)
		}

		var chunkDur float64
		for _, c := range clips[start:end] {
			chunkDur += c.DurationSec
		}
		runCtx, cancel := context.WithTimeout(ctx, cfg.FFmpegTimeout)
		runOpts := runner.FFmpegPipeProgressOptions(ctx, int64(chunkDur*microsecondsPerSecond), "studio.progress.concat_render", "Rendering intermediate video segment")
		res, err := runner.RunWithOptions(runCtx, ffmpegExe, chunkArgs, runOpts)
		cancel()
		if err != nil || res == nil || res.ExitCode != 0 {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return ioUnavailableErr("FFmpeg batched video concat segment failed", err)
		}
		start = end
	}

	var terr error
	listPath, terr = createTempMediaFile(outDir, ".tmp-clist-", ".txt")
	if terr != nil {
		return terr
	}
	if err := writeFFmpegConcatList(listPath, segPaths); err != nil {
		return err
	}

	var finalArgs []string
	if narrationPath != "" {
		fc := fmt.Sprintf("[0:a]aformat=sample_rates=%d:channel_layouts=stereo,asetpts=PTS-STARTPTS[acat];[1:a]aformat=sample_rates=%d:channel_layouts=stereo,asetpts=PTS-STARTPTS[anar];[acat][anar]amix=inputs=2:duration=first:normalize=0[aout]",
			cfg.FFmpegAudioSampleRate, cfg.FFmpegAudioSampleRate)
		finalArgs = []string{
			"-y", "-hide_banner", "-progress", "pipe:1", "-nostats",
			"-f", "concat", "-safe", "0", "-i", listPath,
			"-i", narrationPath,
			"-filter_complex", fc,
			"-map", "0:v", "-c:v", "copy",
			"-map", "[aout]", "-c:a", cfg.FFmpegAudioCodec, "-b:a", cfg.FFmpegAudioBitrate,
			"-movflags", "+faststart", tmpOut,
		}
	} else {
		finalArgs = []string{
			"-y", "-hide_banner", "-progress", "pipe:1", "-nostats",
			"-f", "concat", "-safe", "0", "-i", listPath,
			"-c", "copy", "-movflags", "+faststart", tmpOut,
		}
	}

	runCtx, cancel := context.WithTimeout(ctx, cfg.FFmpegTimeout)
	defer cancel()
	runOpts := runner.FFmpegPipeProgressOptions(ctx, int64(totalDurSec*microsecondsPerSecond), "studio.progress.concat_render", "Finalizing assembled video")
	res, err := runner.RunWithOptions(runCtx, ffmpegExe, finalArgs, runOpts)
	if err != nil || res == nil || res.ExitCode != 0 {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return ioUnavailableErr("FFmpeg batched video concat finalize failed", err)
	}
	return nil
}

func runRenderTimelinePass(
	ctx context.Context,
	ffmpegExe, outDir string,
	tl domainstudio.EditTimeline,
	resolved []resolvedTimelineClip,
	assBurnPath string,
	lufs float64,
	preset string,
	crf int,
	cfg config.StudioConfig,
	tmpOut string,
	directArgs []string,
	expectedDurSec float64,
) error {
	cfg = withStudioDefaults(cfg)
	if !needsFFmpegBatching(directArgs, len(resolved)) {
		runCtx, cancelRun := context.WithTimeout(ctx, cfg.FFmpegTimeout)
		defer cancelRun()
		runOpts := runner.FFmpegPipeProgressOptions(ctx, int64(expectedDurSec*microsecondsPerSecond), "studio.progress.render_timeline", "Rendering timeline")
		res, err := runner.RunWithOptions(runCtx, ffmpegExe, directArgs, runOpts)
		if err != nil || res == nil || res.ExitCode != 0 {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return ioUnavailableErr("FFmpeg timeline render failed", err)
		}
		return nil
	}

	var tempFiles []string
	defer func() {
		for _, p := range tempFiles {
			if p != "" {
				_ = os.Remove(p)
			}
		}
	}()

	var videoClips, audioClips []resolvedTimelineClip
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

	// 1. Pre-render video clips in bounded batches into intermediate MP4 segments.
	var videoSegPaths []string
	if len(videoClips) > 0 {
		var batchOriginMs int64
		for start := 0; start < len(videoClips); {
			if err := ctx.Err(); err != nil {
				return err
			}
			end := min(len(videoClips), start+maxDirectFFmpegClips)
			segPath, terr := createTempMediaFile(outDir, ".tmp-vseg-", ".mp4")
			if terr != nil {
				return terr
			}
			tempFiles = append(tempFiles, segPath)
			videoSegPaths = append(videoSegPaths, segPath)

			var segArgs []string
			var segDurSec float64
			for {
				subTL := domainstudio.EditTimeline{
					Version: tl.Version,
					FPS:     tl.FPS,
					Width:   tl.Width,
					Height:  tl.Height,
					Tracks: []domainstudio.Track{{
						ID:     "vbatch",
						Name:   "Video",
						Kind:   domainstudio.TrackVideo,
						Volume: 1.0,
					}},
				}
				subResolved := make([]resolvedTimelineClip, 0, end-start)
				for idx, orig := range videoClips[start:end] {
					shifted := orig
					shifted.inputIndex = idx
					shifted.hasAudio = false
					shifted.clip.TimelineStartMs = max(0, orig.clip.TimelineStartMs-batchOriginMs)
					subTL.Tracks[0].Clips = append(subTL.Tracks[0].Clips, shifted.clip)
					subResolved = append(subResolved, shifted)
				}
				segArgs, segDurSec, _ = buildRenderTimelineArgs(subTL, subResolved, "", lufs, preset, crf, cfg, segPath)
				if end-start <= 1 || ffmpegArgvBytes(segArgs) <= maxFFmpegArgvBytes {
					break
				}
				end = start + max(1, (end-start)/2)
			}

			var batchMaxEnd int64
			for _, rc := range videoClips[start:end] {
				if eMs := rc.clip.TimelineEndMs(); eMs > batchMaxEnd {
					batchMaxEnd = eMs
				}
			}
			batchOriginMs = batchMaxEnd

			runCtx, cancelRun := context.WithTimeout(ctx, cfg.FFmpegTimeout)
			runOpts := runner.FFmpegPipeProgressOptions(ctx, int64(segDurSec*microsecondsPerSecond), "studio.progress.render_timeline", "Rendering video segment batch")
			res, err := runner.RunWithOptions(runCtx, ffmpegExe, segArgs, runOpts)
			cancelRun()
			if err != nil || res == nil || res.ExitCode != 0 {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				return ioUnavailableErr("FFmpeg timeline batched video segment failed", err)
			}
			start = end
		}
	}

	// 2. Pre-mix audio clips in bounded batches into intermediate AAC/M4A stems.
	var audioStemPaths []string
	for start := 0; start < len(audioClips); {
		if err := ctx.Err(); err != nil {
			return err
		}
		end := min(len(audioClips), start+maxDirectFFmpegClips)
		stemPath, terr := createTempMediaFile(outDir, ".tmp-astem-", ".m4a")
		if terr != nil {
			return terr
		}
		tempFiles = append(tempFiles, stemPath)
		audioStemPaths = append(audioStemPaths, stemPath)

		var aArgs []string
		for {
			batch := audioClips[start:end]
			aArgs = []string{"-y", "-hide_banner", "-progress", "pipe:1", "-nostats"}
			var fc []string
			var mixLabels []string
			for i, rc := range batch {
				aArgs = append(aArgs, "-i", rc.clip.FilePath)
				inSec := float64(rc.clip.SourceInMs) / 1000.0
				outSec := float64(rc.clip.SourceOutMs) / 1000.0
				spd := rc.clip.Speed
				if spd <= 0 {
					spd = 1.0
				}
				gain := max(0.0, rc.track.Volume*rc.clip.Volume)
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
				if rc.clip.TimelineStartMs > 0 {
					filters = append(filters, fmt.Sprintf("adelay=%d|%d", rc.clip.TimelineStartMs, rc.clip.TimelineStartMs))
				}
				lbl := fmt.Sprintf("[aud%d]", i)
				fc = append(fc, fmt.Sprintf("[%d:a]%s%s", i, strings.Join(filters, ","), lbl))
				mixLabels = append(mixLabels, lbl)
			}
			fc = append(fc, fmt.Sprintf("%samix=inputs=%d:duration=longest:dropout_transition=0:normalize=0[aout]", strings.Join(mixLabels, ""), len(mixLabels)))
			aArgs = append(aArgs, "-filter_complex", strings.Join(fc, ";"), "-map", "[aout]", "-c:a", cfg.FFmpegAudioCodec, "-b:a", cfg.FFmpegAudioBitrate, stemPath)
			if end-start <= 1 || ffmpegArgvBytes(aArgs) <= maxFFmpegArgvBytes {
				break
			}
			end = start + max(1, (end-start)/2)
		}

		runCtx, cancelRun := context.WithTimeout(ctx, cfg.FFmpegTimeout)
		runOpts := runner.FFmpegPipeProgressOptions(ctx, int64(expectedDurSec*microsecondsPerSecond), "studio.progress.render_timeline", "Mixing timeline audio batch")
		res, err := runner.RunWithOptions(runCtx, ffmpegExe, aArgs, runOpts)
		cancelRun()
		if err != nil || res == nil || res.ExitCode != 0 {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return ioUnavailableErr("FFmpeg timeline batched audio mix failed", err)
		}
		start = end
	}

	// 3. Final pass combining concatenated video segments + mixed audio stems + optional subtitle burn-in.
	finalArgs := []string{"-y", "-hide_banner", "-progress", "pipe:1", "-nostats"}
	var fc []string
	vOutLabel := "[0:v]"
	if len(videoSegPaths) > 0 {
		vListPath, terr := createTempMediaFile(outDir, ".tmp-vlist-", ".txt")
		if terr != nil {
			return terr
		}
		tempFiles = append(tempFiles, vListPath)
		if err := writeFFmpegConcatList(vListPath, videoSegPaths); err != nil {
			return err
		}
		finalArgs = append(finalArgs, "-f", "concat", "-safe", "0", "-i", vListPath)
	} else {
		fc = append(fc, fmt.Sprintf("color=c=black:s=%dx%d:r=%d:d=%.3f,settb=AVTB,format=%s[vbase]", tl.Width, tl.Height, tl.FPS, expectedDurSec, cfg.FFmpegPixelFormat))
		vOutLabel = "[vbase]"
	}

	audioStartIdx := 0
	if len(videoSegPaths) > 0 {
		audioStartIdx = 1
	}
	if assBurnPath != "" {
		fc = append(fc, fmt.Sprintf("%sass=filename=%s[vout]", vOutLabel, escapeFFmpegFilterPath(assBurnPath)))
		vOutLabel = "[vout]"
	}

	aOutLabel := "[aout]"
	if len(audioStemPaths) == 0 {
		fc = append(fc, fmt.Sprintf("anullsrc=r=%d:cl=stereo,atrim=duration=%.3f,loudnorm=I=%.1f:TP=%.1f:LRA=%d[aout]",
			cfg.FFmpegAudioSampleRate, expectedDurSec, lufs, loudnormTruePeakDB, loudnormRangeLU))
	} else {
		var stemLabels []string
		for i, sp := range audioStemPaths {
			finalArgs = append(finalArgs, "-i", sp)
			stemLabels = append(stemLabels, fmt.Sprintf("[%d:a]", audioStartIdx+i))
		}
		if len(stemLabels) == 1 {
			fc = append(fc, fmt.Sprintf("%sloudnorm=I=%.1f:TP=%.1f:LRA=%d[aout]", stemLabels[0], lufs, loudnormTruePeakDB, loudnormRangeLU))
		} else {
			fc = append(fc, fmt.Sprintf("%samix=inputs=%d:duration=longest:dropout_transition=0:normalize=0,loudnorm=I=%.1f:TP=%.1f:LRA=%d[aout]",
				strings.Join(stemLabels, ""), len(stemLabels), lufs, loudnormTruePeakDB, loudnormRangeLU))
		}
	}

	if len(fc) > 0 {
		finalArgs = append(finalArgs, "-filter_complex", strings.Join(fc, ";"))
	}
	finalArgs = append(finalArgs, "-map", vOutLabel)
	if vOutLabel == "[0:v]" && len(videoSegPaths) > 0 && assBurnPath == "" {
		finalArgs = append(finalArgs, "-c:v", "copy")
	} else {
		finalArgs = append(finalArgs, "-c:v", cfg.FFmpegVideoCodec, "-preset", preset, "-crf", strconv.Itoa(crf), "-pix_fmt", cfg.FFmpegPixelFormat)
	}
	finalArgs = append(finalArgs, "-map", aOutLabel, "-c:a", cfg.FFmpegAudioCodec, "-b:a", cfg.FFmpegAudioBitrate, "-movflags", "+faststart", tmpOut)

	runCtx, cancelRun := context.WithTimeout(ctx, cfg.FFmpegTimeout)
	defer cancelRun()
	runOpts := runner.FFmpegPipeProgressOptions(ctx, int64(expectedDurSec*microsecondsPerSecond), "studio.progress.render_timeline", "Finalizing timeline render")
	res, err := runner.RunWithOptions(runCtx, ffmpegExe, finalArgs, runOpts)
	if err != nil || res == nil || res.ExitCode != 0 {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return ioUnavailableErr("FFmpeg batched timeline finalize failed", err)
	}
	return nil
}
