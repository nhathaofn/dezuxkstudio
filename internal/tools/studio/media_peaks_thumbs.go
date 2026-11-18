package studio

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"app/internal/apperr"
	"app/internal/config"
	"app/internal/contracts"
	domainbinaries "app/internal/domain/binaries"
	"app/internal/engine"
	"app/internal/fsutil"
	"app/internal/proc"
	"app/internal/progress"
	"app/internal/runner"
	"app/internal/safego"
)

const (
	// minPeakBins and maxPeakBins match config.Validate bounds on studio.timeline_peak_bins.
	minPeakBins = 16
	maxPeakBins = 4096

	// minThumbHeight and maxThumbHeight match config.Validate bounds on studio.timeline_thumb_height.
	minThumbHeight = 32
	maxThumbHeight = 360

	// minThumbIntervalSec prevents `fps=1/%.4f` from rounding to zero on sub-millisecond clips.
	minThumbIntervalSec = 0.05

	// spriteJPEGQuality sets FFmpeg mjpeg `-q:v` quantizer scale (2..31) for compact timeline preview strips.
	spriteJPEGQuality = "4"

	// pcmMaxStderrBytes caps retained FFmpeg diagnostic stderr while the remaining pipe is drained to io.Discard.
	pcmMaxStderrBytes = 64 * 1024

	// pcmReadBufBytes is the streaming chunk size (1024 float32 samples) when reading f32le PCM from stdout.
	pcmReadBufBytes = 4096

	// pcmSampleBytes is the IEEE-754 float32 little-endian sample width emitted by `-f f32le`.
	pcmSampleBytes = 4

	// pcmMaxValidAmplitude filters corrupt/denormalized float32 spikes above +20 dBFS when normalizing peaks.
	pcmMaxValidAmplitude = 10.0

	// peakRoundingScale rounds normalized [0..1] waveform peak bins to 3 decimal places.
	peakRoundingScale = 1000.0
)

// NewMediaPeaksThumbsNode builds tool.media_peaks_thumbs for lightweight timeline thumbnail strips and PCM waveform peaks.
func NewMediaPeaksThumbsNode(opts Options) engine.Node {
	opts.StudioConfig = withStudioDefaults(opts.StudioConfig)
	defInterval := fmt.Sprintf("%.1f", opts.StudioConfig.TimelineThumbIntervalSec)
	defBins := opts.StudioConfig.TimelinePeakBins

	spec := engine.Spec{
		Type:        NodeTypeMediaPeaksThumbs,
		Provider:    studioProviderID,
		Label:       "Media Peaks & Thumbnail Strip",
		Description: "Extracts an evenly sampled thumbnail sprite sheet and PCM f32le audio waveform peaks via FFmpeg for smooth browser timeline scrubbing.",
		Inputs: []engine.Port{
			{Name: "media", Label: "Video or audio file", Type: engine.PortMedia, Optional: true},
			{Name: "audio", Label: "Audio file", Type: engine.PortAudio, Optional: true},
		},
		Outputs: []engine.Port{
			{Name: "peaks_thumbs_json", Label: "Peaks & Thumbnail Metadata JSON", Type: engine.PortText},
		},
		Params: []engine.Param{
			{Name: "thumb_interval_sec", Label: "Thumbnail Interval (sec)", Kind: engine.ParamText, Default: defInterval},
			{Name: "peak_bins", Label: "Waveform Peak Bins", Kind: engine.ParamInteger, Min: minPeakBins, Max: maxPeakBins, Default: strconv.Itoa(defBins)},
			{Name: "thumb_height", Label: "Thumbnail Height (px)", Kind: engine.ParamInteger, Min: minThumbHeight, Max: maxThumbHeight, Default: strconv.Itoa(opts.StudioConfig.TimelineThumbHeight)},
			{Name: "output_dir", Label: "Output Directory", Kind: engine.ParamText, Role: engine.ParamRoleOutputDir},
		},
	}

	return engine.Typed(spec, func(ctx context.Context, in contracts.MediaPeaksThumbsInput) (contracts.MediaPeaksThumbsOutput, error) {
		rawTarget := strings.TrimSpace(in.Media.LocalURL)
		allowed := inputKindAnyMedia
		if rawTarget == "" {
			rawTarget = strings.TrimSpace(in.Audio.LocalURL)
			allowed = inputKindAudio
		}
		if rawTarget == "" {
			return contracts.MediaPeaksThumbsOutput{}, apperr.Validation("media or audio input path is required").
				WithMsg("errors.studio.media_required", nil)
		}

		if math.IsNaN(in.ThumbIntervalSec) || math.IsInf(in.ThumbIntervalSec, 0) || in.ThumbIntervalSec < 0 {
			return contracts.MediaPeaksThumbsOutput{}, apperr.Validation("thumb_interval_sec must be a non-negative finite number").
				WithMsg("errors.studio.invalid_thumb_interval", nil)
		}
		if in.PeakBins < 0 || (in.PeakBins > 0 && (in.PeakBins < minPeakBins || in.PeakBins > maxPeakBins)) {
			return contracts.MediaPeaksThumbsOutput{}, apperr.Validation(fmt.Sprintf("peak_bins must be between %d and %d", minPeakBins, maxPeakBins)).
				WithMsg("errors.studio.invalid_peak_bins", map[string]any{"min": minPeakBins, "max": maxPeakBins})
		}
		if in.ThumbHeight < 0 || (in.ThumbHeight > 0 && (in.ThumbHeight < minThumbHeight || in.ThumbHeight > maxThumbHeight)) {
			return contracts.MediaPeaksThumbsOutput{}, apperr.Validation(fmt.Sprintf("thumb_height must be between %d and %d", minThumbHeight, maxThumbHeight)).
				WithMsg("errors.studio.invalid_thumb_height", map[string]any{"min": minThumbHeight, "max": maxThumbHeight})
		}

		resolvedPath, err := resolveMediaInput(ctx, opts, rawTarget, allowed)
		if err != nil {
			return contracts.MediaPeaksThumbsOutput{}, err
		}
		outDir, err := resolveNodeOutputDir(ctx, opts, in.OutputDir, filepath.Join("studio", "thumbs"))
		if err != nil {
			return contracts.MediaPeaksThumbsOutput{}, err
		}

		interval := in.ThumbIntervalSec
		if interval <= 0 {
			interval = opts.StudioConfig.TimelineThumbIntervalSec
		}
		bins := in.PeakBins
		if bins == 0 {
			bins = defBins
		}
		thumbH := in.ThumbHeight
		if thumbH == 0 {
			thumbH = opts.StudioConfig.TimelineThumbHeight
		}

		ffmpegExe, err := resolveBinary(ctx, opts.Binaries, domainbinaries.IDFFmpeg)
		if err != nil {
			return contracts.MediaPeaksThumbsOutput{}, err
		}
		ffprobeExe, err := resolveBinary(ctx, opts.Binaries, domainbinaries.IDFFprobe)
		if err != nil {
			return contracts.MediaPeaksThumbsOutput{}, err
		}

		probeCtx, cancelProbe := context.WithTimeout(ctx, opts.StudioConfig.FFprobeTimeout)
		stopProbe := progress.KeepAlive(ctx)
		info, err := probeWithFFprobe(probeCtx, ffprobeExe, resolvedPath)
		stopProbe()
		cancelProbe()
		if err != nil {
			return contracts.MediaPeaksThumbsOutput{}, err
		}
		durSec := info.DurationSec
		if durSec <= 0 {
			durSec = float64(opts.StudioConfig.DefaultShotDurationSec)
		}
		durationMs := int64(math.Round(durSec * 1000))

		progress.Report(ctx, progress.Update{Percent: 20, Message: "Extracting timeline thumbnails & waveform…", Key: "studio.progress.peaks_thumbs"})

		baseStem := strings.TrimSuffix(filepath.Base(resolvedPath), filepath.Ext(resolvedPath))
		if baseStem == "" {
			baseStem = "clip"
		}

		var spritePath, spriteAssetID string
		thumbCount := 0
		if info.Width > 0 && info.Height > 0 {
			maxStripThumbs := opts.StudioConfig.TimelineMaxStripThumbs
			effInterval := math.Max(minThumbIntervalSec, interval)
			thumbCount = max(1, int(math.Ceil(durSec/effInterval)))
			if thumbCount > maxStripThumbs {
				thumbCount = maxStripThumbs
				effInterval = math.Max(minThumbIntervalSec, durSec/float64(maxStripThumbs))
			}
			interval = effInterval

			spritePath = filepath.Join(outDir, uniqueExportFilename(baseStem+"_sprite", ".jpg"))
			if err := checkStudioFile(ctx, spritePath, true); err != nil {
				return contracts.MediaPeaksThumbsOutput{}, err
			}
			tmpSprite, terr := createTempMediaFile(outDir, ".tmp-sprite-", ".jpg")
			if terr != nil {
				return contracts.MediaPeaksThumbsOutput{}, terr
			}
			vf := fmt.Sprintf("fps=1/%.4f,scale=-2:%d,tile=%dx1", effInterval, thumbH, thumbCount)
			ffCtx, cancelFF := context.WithTimeout(ctx, opts.StudioConfig.FFmpegTimeout)
			stopFF := progress.KeepAlive(ctx)
			res, ferr := runner.RunWithOptions(ffCtx, ffmpegExe, []string{
				"-y", "-hide_banner", "-v", "error",
				"-i", resolvedPath,
				"-vf", vf,
				"-frames:v", "1",
				"-q:v", spriteJPEGQuality,
				tmpSprite,
			}, runner.RunOptions{})
			stopFF()
			cancelFF()
			if ferr != nil || res == nil || res.ExitCode != 0 {
				_ = os.Remove(tmpSprite)
				if ctx.Err() != nil {
					return contracts.MediaPeaksThumbsOutput{}, ctx.Err()
				}
				return contracts.MediaPeaksThumbsOutput{}, ioUnavailableErr("FFmpeg failed to extract timeline thumbnail strip", ferr)
			}
			if infoStat, serr := os.Stat(tmpSprite); serr == nil && infoStat.Size() > 0 {
				if cerr := commitTempMediaFile(tmpSprite, spritePath); cerr != nil {
					return contracts.MediaPeaksThumbsOutput{}, cerr
				}
				spriteAssetID = registerStudioAsset(ctx, opts, "image", NodeTypeMediaPeaksThumbs, spritePath, outDir)
			} else {
				_ = os.Remove(tmpSprite)
				spritePath = ""
			}
		}

		var peaks []float64
		if info.HasAudio {
			progress.Report(ctx, progress.Update{Percent: 60, Message: "Computing PCM audio waveform peaks…", Key: "studio.progress.peaks_waveform"})
			ffCtx, cancelPCM := context.WithTimeout(ctx, opts.StudioConfig.FFmpegTimeout)
			stopPCM := progress.KeepAlive(ctx)
			peaks, err = extractAudioPeaksPCM(ffCtx, ffmpegExe, resolvedPath, bins, opts.StudioConfig)
			stopPCM()
			cancelPCM()
			if err != nil {
				return contracts.MediaPeaksThumbsOutput{}, err
			}
		} else {
			peaks = make([]float64, bins)
		}

		peaksPath := filepath.Join(outDir, uniqueExportFilename(baseStem+"_peaks", ".json"))
		if err := checkStudioFile(ctx, peaksPath, true); err != nil {
			return contracts.MediaPeaksThumbsOutput{}, err
		}

		metaObj := map[string]any{
			"duration_ms":        durationMs,
			"sprite_sheet_path":  spritePath,
			"sprite_asset_id":    spriteAssetID,
			"thumb_count":        thumbCount,
			"thumb_interval_sec": interval,
			"peaks":              peaks,
		}
		metaBytes, err := json.MarshalIndent(metaObj, "", "  ")
		if err != nil {
			return contracts.MediaPeaksThumbsOutput{}, apperr.Internal(err)
		}
		if err := fsutil.WriteFileAtomic(peaksPath, append(metaBytes, '\n'), 0o600); err != nil {
			return contracts.MediaPeaksThumbsOutput{}, ioUnavailableErr("Failed to write timeline waveform peaks cache", err)
		}

		progress.Report(ctx, progress.Update{Percent: 100, Message: "Timeline peaks & thumbnails ready", Key: "studio.progress.done"})

		return contracts.MediaPeaksThumbsOutput{
			PeaksThumbsJSON:  string(metaBytes),
			SpriteSheetPath:  spritePath,
			SpriteAssetID:    spriteAssetID,
			PeaksJSONPath:    peaksPath,
			Peaks:            peaks,
			ThumbCount:       thumbCount,
			ThumbIntervalSec: interval,
			DurationMs:       durationMs,
		}, nil
	})
}

func extractAudioPeaksPCM(ctx context.Context, ffmpegExe, mediaPath string, bins int, cfg config.StudioConfig) ([]float64, error) {
	cfg = withStudioDefaults(cfg)
	args := []string{
		"-hide_banner", "-v", "error",
		"-i", mediaPath,
		"-vn", "-ac", "1", "-ar", strconv.Itoa(cfg.TimelineWaveSampleRate),
		"-f", "f32le", "pipe:1",
	}
	cmd := proc.Command(context.Background(), ffmpegExe, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, ioUnavailableErr("Failed to open FFmpeg PCM stdout pipe", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, ioUnavailableErr("Failed to open FFmpeg PCM stderr pipe", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, ioUnavailableErr("Failed to start FFmpeg PCM peak extraction", err)
	}

	var stderrMsg strings.Builder
	stderrDone := make(chan struct{})
	safego.Go("peaks.stderr", func() {
		defer close(stderrDone)
		b, _ := io.ReadAll(io.LimitReader(stderr, pcmMaxStderrBytes))
		_, _ = io.Copy(io.Discard, stderr)
		stderrMsg.Write(b)
	})

	pcmDone := make(chan struct{})
	waitCh := make(chan error, 1)
	safego.Go("peaks.wait", func() {
		<-pcmDone
		<-stderrDone
		waitCh <- cmd.Wait()
	})

	blockSamples := cfg.TimelineWaveBlockSamples
	var blocks []float64
	var curMax float64
	var inBlock int
	var carry [pcmSampleBytes]byte
	carryLen := 0
	buf := make([]byte, pcmReadBufBytes)

	// Kill child tree if ctx cancels while stdout is blocking on Read.
	stopCancel := make(chan struct{})
	safego.Go("peaks.cancel", func() {
		select {
		case <-ctx.Done():
			proc.KillTree(cmd)
		case <-stopCancel:
		}
	})

	for {
		n, rerr := stdout.Read(buf)
		if n > 0 {
			chunk := buf[:n]
			pos := 0
			if carryLen > 0 {
				need := pcmSampleBytes - carryLen
				if n >= need {
					copy(carry[carryLen:], chunk[:need])
					pos = need
					sample := math.Abs(float64(math.Float32frombits(binary.LittleEndian.Uint32(carry[:]))))
					if !math.IsNaN(sample) && !math.IsInf(sample, 0) && sample <= pcmMaxValidAmplitude && sample > curMax {
						curMax = sample
					}
					inBlock++
					if inBlock >= blockSamples {
						blocks = append(blocks, curMax)
						curMax = 0
						inBlock = 0
					}
					carryLen = 0
				} else {
					copy(carry[carryLen:], chunk)
					carryLen += n
					pos = n
				}
			}
			for pos+pcmSampleBytes <= len(chunk) {
				sample := math.Abs(float64(math.Float32frombits(binary.LittleEndian.Uint32(chunk[pos : pos+pcmSampleBytes]))))
				if !math.IsNaN(sample) && !math.IsInf(sample, 0) && sample <= pcmMaxValidAmplitude && sample > curMax {
					curMax = sample
				}
				inBlock++
				if inBlock >= blockSamples {
					blocks = append(blocks, curMax)
					curMax = 0
					inBlock = 0
				}
				pos += pcmSampleBytes
			}
			if rem := len(chunk) - pos; rem > 0 {
				copy(carry[:], chunk[pos:])
				carryLen = rem
			}
		}
		if rerr != nil {
			break
		}
	}
	_, _ = io.Copy(io.Discard, stdout)
	close(pcmDone)

	waitErr := <-waitCh
	close(stopCancel)

	if inBlock > 0 {
		blocks = append(blocks, curMax)
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if waitErr != nil {
		if detail := strings.TrimSpace(stderrMsg.String()); detail != "" {
			return nil, ioUnavailableErr("FFmpeg PCM peak extraction failed", fmt.Errorf("%w: %s", waitErr, detail))
		}
		return nil, ioUnavailableErr("FFmpeg PCM peak extraction failed", waitErr)
	}

	return resamplePeaks(blocks, bins), nil
}

func resamplePeaks(blocks []float64, bins int) []float64 {
	out := make([]float64, bins)
	if len(blocks) == 0 {
		return out
	}
	globalMax := 0.0
	for i := 0; i < bins; i++ {
		start := i * len(blocks) / bins
		end := (i + 1) * len(blocks) / bins
		if end <= start {
			end = min(len(blocks), start+1)
		}
		peak := 0.0
		for j := start; j < end; j++ {
			if blocks[j] > peak {
				peak = blocks[j]
			}
		}
		out[i] = peak
		if peak > globalMax {
			globalMax = peak
		}
	}
	if globalMax > 0 {
		for i := range out {
			out[i] = math.Round((out[i]/globalMax)*peakRoundingScale) / peakRoundingScale
		}
	}
	return out
}
