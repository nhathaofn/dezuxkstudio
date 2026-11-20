package studio

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"app/internal/apperr"
	"app/internal/config"
	"app/internal/contracts"
	domainbinaries "app/internal/domain/binaries"
	"app/internal/fsutil"
	"app/internal/progress"
	"app/internal/runner"
)

const (
	// signalsSchemaVersion invalidates stale cached signal sidecar JSONs when signal extraction changes.
	signalsSchemaVersion = 2
)

var (
	blackDetectLineRe = regexp.MustCompile(`black_start:([0-9]+(?:\.[0-9]+)?)\s+black_end:([0-9]+(?:\.[0-9]+)?)\s+black_duration:([0-9]+(?:\.[0-9]+)?)`)
	freezeStartRe     = regexp.MustCompile(`freeze_start:\s*([0-9]+(?:\.[0-9]+)?)`)
	freezeDurRe       = regexp.MustCompile(`freeze_duration:\s*([0-9]+(?:\.[0-9]+)?)`)
	freezeEndRe       = regexp.MustCompile(`freeze_end:\s*([0-9]+(?:\.[0-9]+)?)`)
	ptsTimeRe         = regexp.MustCompile(`pts_time:([0-9]+(?:\.[0-9]+)?)`)
	sceneScoreRe      = regexp.MustCompile(`lavfi\.scene_score=([0-9]+(?:\.[0-9]+)?)`)
)

type cachedVideoSignalsFile struct {
	SchemaVersion int                    `json:"schema_version"`
	Filter        string                 `json:"filter,omitempty"`
	FileSize      int64                  `json:"file_size"`
	MTimeUnixNano int64                  `json:"mtime_unix_nano"`
	Signals       contracts.VideoSignals `json:"signals"`
}

func analyzeVideoSignals(ctx context.Context, opts Options, vidPath, cacheDir string) (contracts.VideoSignals, error) {
	cfg := withStudioDefaults(opts.StudioConfig)
	vf := fmt.Sprintf(
		"blackdetect=d=%.3f:pix_th=%.3f,freezedetect=n=%s:d=%.3f,select='gt(scene,%.3f)',metadata=print:file=-",
		cfg.SignalBlackMinSec,
		cfg.SignalBlackPixelThresh,
		cfg.SignalFreezeNoiseDB,
		cfg.SignalFreezeMinSec,
		cfg.SignalSceneCutThresh,
	)
	st, statErr := os.Stat(vidPath)
	var cachePath string
	if cacheDir != "" && statErr == nil {
		stem := strings.TrimSuffix(filepath.Base(vidPath), filepath.Ext(vidPath))
		cachePath = filepath.Join(cacheDir, stem+"_signals.json")
		if cached, ok := readCachedSignals(ctx, cachePath, vf, st.Size(), st.ModTime().UnixNano()); ok {
			cached.Cached = true
			return cached, nil
		}
	}

	var probeInfo contracts.ProbeMediaOutput
	if ffprobeExe, perr := resolveBinary(ctx, opts.Binaries, domainbinaries.IDFFprobe); perr == nil {
		probeCtx, cancel := context.WithTimeout(ctx, cfg.FFprobeTimeout)
		stop := progress.KeepAlive(ctx)
		info, ierr := probeWithFFprobe(probeCtx, ffprobeExe, vidPath)
		stop()
		cancel()
		if ierr != nil && ctx.Err() != nil {
			return contracts.VideoSignals{}, ctx.Err()
		}
		if ierr == nil {
			probeInfo = info
		}
	}

	ffmpegExe, err := resolveBinary(ctx, opts.Binaries, domainbinaries.IDFFmpeg)
	if err != nil {
		return contracts.VideoSignals{}, err
	}

	args := []string{
		"-hide_banner",
		"-nostats",
		"-i", vidPath,
		"-an",
		"-vf", vf,
		"-f", "null",
		"-",
	}

	ffCtx, cancelFF := context.WithTimeout(ctx, cfg.FFmpegTimeout)
	defer cancelFF()
	stop := progress.KeepAlive(ctx)
	res, runErr := runner.RunWithOptions(ffCtx, ffmpegExe, args, runner.RunOptions{})
	stop()
	if runErr != nil || res == nil || res.ExitCode != 0 {
		if ctx.Err() != nil {
			return contracts.VideoSignals{}, ctx.Err()
		}
		return contracts.VideoSignals{}, ioUnavailableErr("FFmpeg video signal analysis failed", runErr)
	}

	signals := parseVideoSignalsOutput(res.Stdout, res.Stderr, probeInfo.DurationSec)
	signals.Width = probeInfo.Width
	signals.Height = probeInfo.Height
	signals.FPS = probeInfo.FPS

	if cachePath != "" && statErr == nil {
		if cerr := checkStudioFile(ctx, cachePath, true); cerr == nil {
			_ = writeCachedSignals(cachePath, vf, st.Size(), st.ModTime().UnixNano(), signals)
		}
	}
	return signals, nil
}

func readCachedSignals(ctx context.Context, cachePath, filter string, size, mtimeNano int64) (contracts.VideoSignals, bool) {
	if err := checkStudioFile(ctx, cachePath, false); err != nil {
		return contracts.VideoSignals{}, false
	}
	b, err := os.ReadFile(cachePath)
	if err != nil {
		return contracts.VideoSignals{}, false
	}
	var parsed cachedVideoSignalsFile
	if json.Unmarshal(b, &parsed) != nil {
		return contracts.VideoSignals{}, false
	}
	if parsed.SchemaVersion != signalsSchemaVersion || parsed.Filter != filter || parsed.FileSize != size || parsed.MTimeUnixNano != mtimeNano {
		return contracts.VideoSignals{}, false
	}
	if parsed.Signals.Blacks == nil {
		parsed.Signals.Blacks = []contracts.VideoSignalBlack{}
	}
	if parsed.Signals.Freezes == nil {
		parsed.Signals.Freezes = []contracts.VideoSignalFreeze{}
	}
	if parsed.Signals.Cuts == nil {
		parsed.Signals.Cuts = []contracts.VideoSignalCut{}
	}
	return parsed.Signals, true
}

func writeCachedSignals(cachePath, filter string, size, mtimeNano int64, sig contracts.VideoSignals) error {
	if err := os.MkdirAll(filepath.Dir(cachePath), 0o750); err != nil {
		return err
	}
	sig.Cached = false
	payload := cachedVideoSignalsFile{
		SchemaVersion: signalsSchemaVersion,
		Filter:        filter,
		FileSize:      size,
		MTimeUnixNano: mtimeNano,
		Signals:       sig,
	}
	b, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	return fsutil.WriteFileAtomic(cachePath, append(b, '\n'), 0o600)
}

// parseVideoSignalsOutput extracts BLACK, FREEZE, and CUT signals from FFmpeg stdout/stderr output.
func parseVideoSignalsOutput(stdout, stderr string, durationSec float64) contracts.VideoSignals {
	out := contracts.VideoSignals{
		DurationSec: durationSec,
		Blacks:      []contracts.VideoSignalBlack{},
		Freezes:     []contracts.VideoSignalFreeze{},
		Cuts:        []contracts.VideoSignalCut{},
	}

	var openFreezeStart *float64
	for _, line := range strings.Split(stderr, "\n") {
		if m := blackDetectLineRe.FindStringSubmatch(line); len(m) == 4 {
			st, err1 := strconv.ParseFloat(m[1], 64)
			en, err2 := strconv.ParseFloat(m[2], 64)
			du, err3 := strconv.ParseFloat(m[3], 64)
			if err1 == nil && err2 == nil && err3 == nil {
				out.Blacks = append(out.Blacks, contracts.VideoSignalBlack{
					StartSec:    roundSignalSec(st),
					EndSec:      roundSignalSec(en),
					DurationSec: roundSignalSec(du),
				})
			}
		}
		if m := freezeStartRe.FindStringSubmatch(line); len(m) == 2 {
			if v, err := strconv.ParseFloat(m[1], 64); err == nil {
				cp := v
				openFreezeStart = &cp
			}
		}
		if m := freezeEndRe.FindStringSubmatch(line); len(m) == 2 && openFreezeStart != nil {
			if endVal, err := strconv.ParseFloat(m[1], 64); err == nil {
				dur := math.Max(0, endVal-*openFreezeStart)
				out.Freezes = append(out.Freezes, contracts.VideoSignalFreeze{
					StartSec:    roundSignalSec(*openFreezeStart),
					EndSec:      roundSignalSec(endVal),
					DurationSec: roundSignalSec(dur),
				})
				openFreezeStart = nil
			}
		} else if m := freezeDurRe.FindStringSubmatch(line); len(m) == 2 && openFreezeStart != nil {
			if durVal, err := strconv.ParseFloat(m[1], 64); err == nil {
				out.Freezes = append(out.Freezes, contracts.VideoSignalFreeze{
					StartSec:    roundSignalSec(*openFreezeStart),
					EndSec:      roundSignalSec(*openFreezeStart + durVal),
					DurationSec: roundSignalSec(durVal),
				})
				openFreezeStart = nil
			}
		}
	}
	if openFreezeStart != nil && durationSec > *openFreezeStart {
		out.Freezes = append(out.Freezes, contracts.VideoSignalFreeze{
			StartSec:    roundSignalSec(*openFreezeStart),
			EndSec:      roundSignalSec(durationSec),
			DurationSec: roundSignalSec(durationSec - *openFreezeStart),
		})
	}

	var pendingCutIdx = -1
	for _, line := range strings.Split(stdout, "\n") {
		if m := ptsTimeRe.FindStringSubmatch(line); len(m) == 2 {
			if ts, err := strconv.ParseFloat(m[1], 64); err == nil && ts >= 0 {
				out.Cuts = append(out.Cuts, contracts.VideoSignalCut{TimeSec: roundSignalSec(ts)})
				pendingCutIdx = len(out.Cuts) - 1
			}
			continue
		}
		if pendingCutIdx >= 0 {
			if m := sceneScoreRe.FindStringSubmatch(line); len(m) == 2 {
				if sc, err := strconv.ParseFloat(m[1], 64); err == nil {
					out.Cuts[pendingCutIdx].Score = math.Round(sc*1000) / 1000
				}
				pendingCutIdx = -1
			}
		}
	}

	slices.SortStableFunc(out.Cuts, func(a, b contracts.VideoSignalCut) int {
		if a.TimeSec < b.TimeSec {
			return -1
		}
		if a.TimeSec > b.TimeSec {
			return 1
		}
		return 0
	})
	return out
}

func roundSignalSec(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return math.Round(v*1000) / 1000
}

// ensureUnused imports kept clean
var _ = apperr.CodeUnavailable
var _ = config.ModeDesktop
