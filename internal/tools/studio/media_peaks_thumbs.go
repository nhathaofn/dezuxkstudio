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

// NewMediaPeaksThumbsNode builds tool.media_peaks_thumbs for lightweight timeline thumbnail strips and PCM waveform peaks.
func NewMediaPeaksThumbsNode(opts Options) engine.Node {
	opts.StudioConfig = withStudioDefaults(opts.StudioConfig)
	defInterval := fmt.Sprintf("%.1f", opts.StudioConfig.TimelineThumbIntervalSec)
	defBins := opts.StudioConfig.TimelinePeakBins

	spec := engine.Spec{
		Type:        NodeTypeMediaPeaksThumbs,
		Provider:    "studio",
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
			{Name: "peak_bins", Label: "Waveform Peak Bins", Kind: engine.ParamInteger, Min: 16, Max: 4096, Default: strconv.Itoa(defBins)},
			{Name: "thumb_height", Label: "Thumbnail Height (px)", Kind: engine.ParamInteger, Min: 32, Max: 360, Default: strconv.Itoa(opts.StudioConfig.TimelineThumbHeight)},
			{Name: "output_dir", Label: "Output Directory", Kind: engine.ParamText, Role: engine.ParamRoleOutputDir},
		},
	}

	return engine.Typed(spec, func(ctx context.Context, in contracts.MediaPeaksThumbsInput) (contracts.MediaPeaksThumbsOutput, error) {
		rawTarget := strings.TrimSpace(in.Media.LocalURL)
		if rawTarget == "" {
			rawTarget = strings.TrimSpace(in.Audio.LocalURL)
		}
		if rawTarget == "" {
			return contracts.MediaPeaksThumbsOutput{}, apperr.Validation("media or audio input path is required")
		}

		resolvedPath, err := resolveInputFile(ctx, opts, rawTarget)
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
		if bins <= 0 {
			bins = defBins
		}
		if bins < 16 || bins > 4096 {
			return contracts.MediaPeaksThumbsOutput{}, apperr.Validation("peak_bins must be between 16 and 4096")
		}
		thumbH := in.ThumbHeight
		if thumbH <= 0 {
			thumbH = opts.StudioConfig.TimelineThumbHeight
		}
		if thumbH < 32 || thumbH > 360 {
			return contracts.MediaPeaksThumbsOutput{}, apperr.Validation("thumb_height must be between 32 and 360")
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
		defer cancelProbe()
		info, err := probeWithFFprobe(probeCtx, ffprobeExe, resolvedPath)
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
			thumbCount = max(1, int(math.Ceil(durSec/interval)))
			effInterval := interval
			if thumbCount > maxStripThumbs {
				thumbCount = maxStripThumbs
				effInterval = durSec / float64(maxStripThumbs)
			}
			interval = effInterval

			spritePath = filepath.Join(outDir, uniqueExportFilename(baseStem+"_sprite", ".jpg"))
			if err := engine.CheckFile(ctx, spritePath, true); err != nil {
				return contracts.MediaPeaksThumbsOutput{}, err
			}
			vf := fmt.Sprintf("fps=1/%.4f,scale=-2:%d,tile=%dx1", effInterval, thumbH, thumbCount)
			ffCtx, cancelFF := context.WithTimeout(ctx, opts.StudioConfig.FFmpegTimeout)
			res, ferr := runner.RunWithOptions(ffCtx, ffmpegExe, []string{
				"-y", "-hide_banner", "-v", "error",
				"-i", resolvedPath,
				"-vf", vf,
				"-frames:v", "1",
				"-q:v", "4",
				spritePath,
			}, runner.RunOptions{})
			cancelFF()
			if ferr != nil {
				return contracts.MediaPeaksThumbsOutput{}, apperr.Unavailable("ffmpeg thumbnail sprite failed: " + resStderr(res))
			}
			if _, serr := os.Stat(spritePath); serr == nil {
				spriteAssetID = registerStudioAsset(ctx, opts, "image", NodeTypeMediaPeaksThumbs, spritePath, outDir)
			} else {
				spritePath = ""
			}
		}

		var peaks []float64
		if info.HasAudio {
			ffCtx, cancelPCM := context.WithTimeout(ctx, opts.StudioConfig.FFmpegTimeout)
			peaks, err = extractAudioPeaksPCM(ffCtx, ffmpegExe, resolvedPath, bins, opts.StudioConfig)
			cancelPCM()
			if err != nil {
				return contracts.MediaPeaksThumbsOutput{}, err
			}
		} else {
			peaks = make([]float64, bins)
		}

		peaksPath := filepath.Join(outDir, uniqueExportFilename(baseStem+"_peaks", ".json"))
		if err := engine.CheckFile(ctx, peaksPath, true); err != nil {
			return contracts.MediaPeaksThumbsOutput{}, err
		}

		metaObj := map[string]any{
			"source_path":        resolvedPath,
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
		if err := fsutil.WriteFileAtomic(peaksPath, append(metaBytes, '\n'), 0o644); err != nil {
			return contracts.MediaPeaksThumbsOutput{}, apperr.Internal(err)
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
		return nil, apperr.Internal(err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, apperr.Internal(err)
	}
	if err := cmd.Start(); err != nil {
		return nil, apperr.Unavailable("failed to start ffmpeg PCM peak extraction: " + err.Error())
	}

	var stderrMsg strings.Builder
	stderrDone := make(chan struct{})
	safego.Go("peaks.stderr", func() {
		defer close(stderrDone)
		b, _ := io.ReadAll(io.LimitReader(stderr, 64*1024))
		stderrMsg.Write(b)
	})

	// Cancel watcher kills the child tree cleanly when ctx expires.
	waitDone := make(chan struct{})
	safego.Go("peaks.cancel", func() {
		select {
		case <-ctx.Done():
			proc.KillTree(cmd)
		case <-waitDone:
		}
	})

	// Stream float32 samples into 80-sample (~10ms @ 8kHz) frame blocks to keep memory small.
	blockSamples := cfg.TimelineWaveBlockSamples
	var blocks []float64
	var curMax float64
	var inBlock int
	var carry [4]byte
	carryLen := 0
	buf := make([]byte, 4096)

	for {
		n, rerr := stdout.Read(buf)
		if n > 0 {
			chunk := buf[:n]
			pos := 0
			if carryLen > 0 {
				need := 4 - carryLen
				if n >= need {
					copy(carry[carryLen:], chunk[:need])
					pos = need
					sample := math.Abs(float64(math.Float32frombits(binary.LittleEndian.Uint32(carry[:]))))
					if !math.IsNaN(sample) && !math.IsInf(sample, 0) && sample <= 10.0 && sample > curMax {
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
			for pos+4 <= len(chunk) {
				sample := math.Abs(float64(math.Float32frombits(binary.LittleEndian.Uint32(chunk[pos : pos+4]))))
				if !math.IsNaN(sample) && !math.IsInf(sample, 0) && sample <= 10.0 && sample > curMax {
					curMax = sample
				}
				inBlock++
				if inBlock >= blockSamples {
					blocks = append(blocks, curMax)
					curMax = 0
					inBlock = 0
				}
				pos += 4
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
	if inBlock > 0 {
		blocks = append(blocks, curMax)
	}

	<-stderrDone
	waitErr := cmd.Wait()
	close(waitDone)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if waitErr != nil {
		return nil, apperr.Unavailable("ffmpeg PCM peak extraction failed: " + strings.TrimSpace(stderrMsg.String()))
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
			out[i] = math.Round((out[i]/globalMax)*1000) / 1000
		}
	}
	return out
}
