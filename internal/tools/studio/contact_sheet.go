package studio

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	_ "image/png"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"

	"app/internal/apperr"
	"app/internal/config"
	"app/internal/contracts"
	domainbinaries "app/internal/domain/binaries"
	"app/internal/engine"
	"app/internal/fsutil"
	"app/internal/progress"
	"app/internal/runner"
)

const (
	contactSheetTileHeaderPx = 22
	contactSheetTileGapPx    = 6
	contactSheetMinTileEdge  = 160
	contactSheetTimeDedupSec = 0.15
)

type sampledFrameTarget struct {
	TimeSec float64
	Signals []string
}

type contactSheetTile struct {
	Img        image.Image
	TimeSec    float64
	TimelineMs int64
	Signals    []string
}

// NewVideoContactSheetNode builds tool.video_contact_sheet to extract labeled contact sheets
// along with FFmpeg BLACK, FREEZE, and CUT video signals.
func NewVideoContactSheetNode(opts Options) engine.Node {
	opts.StudioConfig = withStudioDefaults(opts.StudioConfig)
	defMaxFrames := opts.StudioConfig.ContactSheetMaxFramesPerShot
	defPerSheet := opts.StudioConfig.ContactSheetFramesPerSheet
	defMaxEdge := opts.StudioConfig.ContactSheetMaxEdgePx
	defQuality := opts.StudioConfig.ContactSheetJPEGQuality

	spec := engine.Spec{
		Type:        NodeTypeVideoContactSheet,
		Provider:    studioProviderID,
		Label:       "Video Contact Sheet & Signal Inspector",
		Description: "Samples video frames around scene cuts, black frames, and freeze intervals into labeled JPEG contact sheets with machine signals (BLACK/FREEZE/CUT) for visual review.",
		Inputs: []engine.Port{
			{Name: "video_path", Label: "Source Video", Type: engine.PortVideo},
		},
		Outputs: []engine.Port{
			{Name: "sheets", Label: "Contact Sheet Images", Type: engine.PortImage, Multiple: true},
		},
		Params: []engine.Param{
			{Name: "shot_id", Label: "Shot ID", Kind: engine.ParamText},
			{Name: "source_in_ms", Label: "Source In (ms)", Kind: engine.ParamInteger, Min: 0, Max: 86400000, Default: "0"},
			{Name: "source_out_ms", Label: "Source Out (ms)", Kind: engine.ParamInteger, Min: 0, Max: 86400000, Default: "0"},
			{Name: "max_frames", Label: "Max Sampled Frames", Kind: engine.ParamInteger, Min: 1, Max: defMaxFrames, Default: strconv.Itoa(defMaxFrames)},
			{Name: "frames_per_sheet", Label: "Frames Per Sheet", Kind: engine.ParamInteger, Min: 1, Max: defPerSheet, Default: strconv.Itoa(defPerSheet)},
			{Name: "max_edge_px", Label: "Max Sheet Edge (px)", Kind: engine.ParamInteger, Min: 256, Max: opts.StudioConfig.MaxVideoDimension, Default: strconv.Itoa(defMaxEdge)},
			{Name: "jpeg_quality", Label: "JPEG Quality (1-100)", Kind: engine.ParamInteger, Min: 1, Max: 100, Default: strconv.Itoa(defQuality)},
			{Name: "include_signals", Label: "Detect BLACK/FREEZE/CUT Signals", Kind: engine.ParamToggle, Default: "true"},
			{Name: "output_dir", Label: "Output Folder", Kind: engine.ParamText, Role: engine.ParamRoleOutputDir},
		},
	}

	return engine.Typed(spec, func(ctx context.Context, in contracts.VideoContactSheetInput) (contracts.VideoContactSheetOutput, error) {
		vidPath, err := resolveMediaInput(ctx, opts, in.VideoPath.LocalURL, inputKindVideo)
		if err != nil {
			return contracts.VideoContactSheetOutput{}, err
		}
		if vidPath == "" {
			return contracts.VideoContactSheetOutput{}, apperr.Validation("video_path is required").
				WithMsg("errors.studio.video_required", nil)
		}

		maxFrames := in.MaxFrames
		if maxFrames == 0 {
			maxFrames = defMaxFrames
		}
		if maxFrames < 1 || maxFrames > defMaxFrames {
			return contracts.VideoContactSheetOutput{}, apperr.Validation(fmt.Sprintf("max_frames must be between 1 and %d", defMaxFrames)).
				WithMsg("errors.studio.invalid_frame_count", map[string]any{"min": 1, "max": defMaxFrames})
		}

		perSheet := in.FramesPerSheet
		if perSheet == 0 {
			perSheet = defPerSheet
		}
		if perSheet < 1 || perSheet > defPerSheet {
			return contracts.VideoContactSheetOutput{}, apperr.Validation(fmt.Sprintf("frames_per_sheet must be between 1 and %d", defPerSheet)).
				WithMsg("errors.studio.invalid_frame_count", map[string]any{"min": 1, "max": defPerSheet})
		}

		maxEdgePx := in.MaxEdgePx
		if maxEdgePx == 0 {
			maxEdgePx = defMaxEdge
		}
		if maxEdgePx < 256 || maxEdgePx > opts.StudioConfig.MaxVideoDimension {
			return contracts.VideoContactSheetOutput{}, apperr.Validation("max_edge_px is out of bounds").
				WithMsg("errors.studio.invalid_resolution", nil)
		}

		jpegQuality := in.JPEGQuality
		if jpegQuality == 0 {
			jpegQuality = defQuality
		}
		if jpegQuality < 1 || jpegQuality > 100 {
			return contracts.VideoContactSheetOutput{}, apperr.Validation("jpeg_quality must be between 1 and 100")
		}

		includeSignals := true
		if in.IncludeSignals != nil {
			includeSignals = *in.IncludeSignals
		}

		outDir, err := resolveNodeOutputDir(ctx, opts, in.OutputDir, filepath.Join("studio", "thumbs"))
		if err != nil {
			return contracts.VideoContactSheetOutput{}, err
		}
		ffmpegExe, err := resolveBinary(ctx, opts.Binaries, domainbinaries.IDFFmpeg)
		if err != nil {
			return contracts.VideoContactSheetOutput{}, err
		}

		progress.Report(ctx, progress.Update{
			Percent: 10,
			Message: "Analyzing video BLACK/FREEZE/CUT signals…",
			Key:     "studio.progress.contact_sheet",
			Params: map[string]string{
				"current": "0",
				"total":   "1",
			},
		})

		var signals contracts.VideoSignals
		if includeSignals {
			signals, err = analyzeVideoSignals(ctx, opts, vidPath, outDir)
			if err != nil {
				return contracts.VideoContactSheetOutput{}, err
			}
		} else {
			signals = contracts.VideoSignals{
				Blacks:  []contracts.VideoSignalBlack{},
				Freezes: []contracts.VideoSignalFreeze{},
				Cuts:    []contracts.VideoSignalCut{},
			}
			if ffprobeExe, perr := resolveBinary(ctx, opts.Binaries, domainbinaries.IDFFprobe); perr == nil {
				probeCtx, cancel := context.WithTimeout(ctx, opts.StudioConfig.FFprobeTimeout)
				stop := progress.KeepAlive(ctx)
				if info, ierr := probeWithFFprobe(probeCtx, ffprobeExe, vidPath); ierr == nil {
					signals.DurationSec = info.DurationSec
					signals.Width = info.Width
					signals.Height = info.Height
					signals.FPS = info.FPS
				}
				stop()
				cancel()
			}
		}

		targets := selectContactSheetTimestamps(signals.DurationSec, signals, maxFrames, in.SourceInMs, in.SourceOutMs, opts.StudioConfig)
		if len(targets) == 0 {
			targets = []sampledFrameTarget{{TimeSec: 0}}
		}

		tmpDir, err := os.MkdirTemp(outDir, ".tmp-sheet-*")
		if err != nil {
			return contracts.VideoContactSheetOutput{}, ioUnavailableErr("Failed to prepare temporary contact sheet directory", err)
		}
		defer func() { _ = os.RemoveAll(tmpDir) }()

		ffCtx, cancelFF := context.WithTimeout(ctx, opts.StudioConfig.FFmpegTimeout)
		defer cancelFF()

		tiles := make([]contactSheetTile, 0, len(targets))
		for i, tg := range targets {
			if err := ctx.Err(); err != nil {
				return contracts.VideoContactSheetOutput{}, err
			}
			progress.Report(ctx, progress.Update{
				Percent: 20.0 + float64(i)/float64(max(1, len(targets)))*55.0,
				Message: fmt.Sprintf("Extracting contact sheet frame %d/%d…", i+1, len(targets)),
				Key:     "studio.progress.contact_sheet",
				Params: map[string]string{
					"current": strconv.Itoa(i + 1),
					"total":   strconv.Itoa(len(targets)),
				},
			})
			frameFile := filepath.Join(tmpDir, fmt.Sprintf("f_%03d.png", i))
			args := []string{"-y", "-ss", fmt.Sprintf("%.3f", tg.TimeSec), "-i", vidPath, "-frames:v", "1", frameFile}
			stopRun := progress.KeepAlive(ctx)
			res, runErr := runner.Run(ffCtx, ffmpegExe, args...)
			stopRun()
			if runErr != nil || res == nil || res.ExitCode != 0 {
				if ctx.Err() != nil {
					return contracts.VideoContactSheetOutput{}, ctx.Err()
				}
				continue
			}
			f, oerr := os.Open(frameFile)
			if oerr != nil {
				continue
			}
			img, _, derr := image.Decode(f)
			_ = f.Close()
			if derr != nil {
				continue
			}
			relMs := int64(math.Round(tg.TimeSec*1000)) - max(0, in.SourceInMs)
			if relMs < 0 {
				relMs = 0
			}
			tiles = append(tiles, contactSheetTile{
				Img:        img,
				TimeSec:    tg.TimeSec,
				TimelineMs: relMs,
				Signals:    tg.Signals,
			})
		}

		if len(tiles) == 0 {
			return contracts.VideoContactSheetOutput{}, ioUnavailableErr("Could not decode any frames from video for contact sheet", nil)
		}

		stem := strings.TrimSuffix(filepath.Base(vidPath), filepath.Ext(vidPath))
		var sheetRefs []engine.MediaRef
		var sheetPaths []string
		var frameMeta []contracts.ContactSheetFrameMeta

		totalSheets := (len(tiles) + perSheet - 1) / perSheet
		for sIdx := 0; sIdx < totalSheets; sIdx++ {
			start := sIdx * perSheet
			end := min(len(tiles), start+perSheet)
			chunk := tiles[start:end]
			jpegBytes, cerr := composeContactSheetJPEG(chunk, maxEdgePx, jpegQuality)
			if cerr != nil {
				return contracts.VideoContactSheetOutput{}, apperr.Internal(cerr)
			}
			sheetName := uniqueExportFilename(fmt.Sprintf("%s_sheet_%02d", stem, sIdx+1), ".jpg")
			sheetPath := filepath.Join(outDir, sheetName)
			if err := checkStudioFile(ctx, sheetPath, true); err != nil {
				return contracts.VideoContactSheetOutput{}, err
			}
			if err := fsutil.WriteFileAtomic(sheetPath, jpegBytes, 0o600); err != nil {
				return contracts.VideoContactSheetOutput{}, ioUnavailableErr("Failed to save contact sheet JPEG", err)
			}
			_ = registerStudioAsset(ctx, opts, "image", NodeTypeVideoContactSheet, sheetPath, outDir)
			sheetPaths = append(sheetPaths, sheetPath)
			sheetRefs = append(sheetRefs, engine.RefFor(sheetPath, engine.MediaImage))
			for j, t := range chunk {
				frameMeta = append(frameMeta, contracts.ContactSheetFrameMeta{
					Index:      start + j,
					SheetIndex: sIdx,
					TimeSec:    roundSignalSec(t.TimeSec),
					TimelineMs: t.TimelineMs,
					Signals:    t.Signals,
				})
			}
		}

		summary := fmt.Sprintf("Generated %d contact sheet(s) (%d frames): %d black, %d freeze, %d cut signal(s)",
			len(sheetPaths), len(frameMeta), len(signals.Blacks), len(signals.Freezes), len(signals.Cuts))
		progress.Report(ctx, progress.Update{Percent: 100, Message: "Contact sheet ready", Key: "studio.progress.done"})

		return contracts.VideoContactSheetOutput{
			Sheets:     sheetRefs,
			SheetPaths: sheetPaths,
			Signals:    signals,
			Frames:     frameMeta,
			Summary:    summary,
		}, nil
	})
}

func selectContactSheetTimestamps(durSec float64, sig contracts.VideoSignals, maxFrames int, sourceInMs, sourceOutMs int64, cfg config.StudioConfig) []sampledFrameTarget {
	cfg = withStudioDefaults(cfg)
	if durSec <= 0 {
		return []sampledFrameTarget{{TimeSec: 0}}
	}
	endPad := cfg.ExtractFrameEndPadSec
	winStart := 0.0
	if sourceInMs > 0 {
		winStart = math.Min(durSec, float64(sourceInMs)/1000.0)
	}
	winEnd := math.Max(winStart, durSec-endPad)
	if sourceOutMs > sourceInMs {
		winEnd = math.Min(durSec-endPad, float64(sourceOutMs)/1000.0)
	}
	if winEnd < winStart {
		winEnd = winStart
	}

	signalTagFor := func(ts float64) []string {
		var tags []string
		for _, b := range sig.Blacks {
			if ts >= b.StartSec-contactSheetTimeDedupSec && ts <= b.EndSec+contactSheetTimeDedupSec {
				tags = append(tags, "BLACK")
				break
			}
		}
		for _, f := range sig.Freezes {
			if ts >= f.StartSec-contactSheetTimeDedupSec && ts <= f.EndSec+contactSheetTimeDedupSec {
				tags = append(tags, "FREEZE")
				break
			}
		}
		for _, c := range sig.Cuts {
			if math.Abs(ts-c.TimeSec) <= contactSheetTimeDedupSec {
				tags = append(tags, "CUT")
				break
			}
		}
		return tags
	}

	var candidates []float64
	for _, b := range sig.Blacks {
		mid := (b.StartSec + b.EndSec) / 2.0
		if mid >= winStart && mid <= winEnd {
			candidates = append(candidates, mid)
		}
	}
	for _, f := range sig.Freezes {
		mid := (f.StartSec + f.EndSec) / 2.0
		if mid >= winStart && mid <= winEnd {
			candidates = append(candidates, mid)
		}
	}
	for _, c := range sig.Cuts {
		if c.TimeSec >= winStart && c.TimeSec <= winEnd {
			candidates = append(candidates, c.TimeSec)
		}
	}

	// Add uniform samples across the active window.
	if maxFrames == 1 {
		candidates = append(candidates, winStart)
	} else {
		span := winEnd - winStart
		for i := 0; i < maxFrames; i++ {
			ts := winStart
			if span > 0 {
				ts = winStart + span*float64(i)/float64(maxFrames-1)
			}
			candidates = append(candidates, ts)
		}
	}

	slices.Sort(candidates)
	var targets []sampledFrameTarget
	for _, ts := range candidates {
		ts = roundSignalSec(math.Max(winStart, math.Min(winEnd, ts)))
		if len(targets) > 0 && math.Abs(ts-targets[len(targets)-1].TimeSec) < contactSheetTimeDedupSec {
			// Merge signal tags onto the existing close frame.
			merged := append(targets[len(targets)-1].Signals, signalTagFor(ts)...)
			targets[len(targets)-1].Signals = dedupStrings(merged)
			continue
		}
		targets = append(targets, sampledFrameTarget{
			TimeSec: ts,
			Signals: signalTagFor(ts),
		})
	}
	if len(targets) > maxFrames {
		// Prioritize signal-annotated frames + evenly spaced frames up to maxFrames.
		var prioritized []sampledFrameTarget
		var plain []sampledFrameTarget
		for _, t := range targets {
			if len(t.Signals) > 0 {
				prioritized = append(prioritized, t)
			} else {
				plain = append(plain, t)
			}
		}
		if len(prioritized) >= maxFrames {
			targets = prioritized[:maxFrames]
		} else {
			need := maxFrames - len(prioritized)
			if need >= len(plain) {
				targets = append(prioritized, plain...)
			} else {
				step := float64(len(plain)) / float64(need)
				for i := 0; i < need; i++ {
					idx := min(len(plain)-1, int(math.Floor(float64(i)*step)))
					prioritized = append(prioritized, plain[idx])
				}
				targets = prioritized
			}
		}
		slices.SortStableFunc(targets, func(a, b sampledFrameTarget) int {
			if a.TimeSec < b.TimeSec {
				return -1
			}
			if a.TimeSec > b.TimeSec {
				return 1
			}
			return 0
		})
	}
	return targets
}

func dedupStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(in))
	var out []string
	for _, s := range in {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func composeContactSheetJPEG(tiles []contactSheetTile, maxEdgePx, jpegQuality int) ([]byte, error) {
	n := len(tiles)
	if n == 0 {
		return nil, fmt.Errorf("no tiles to compose")
	}
	cols := min(4, max(1, int(math.Ceil(math.Sqrt(float64(n))))))
	rows := (n + cols - 1) / cols

	srcB := tiles[0].Img.Bounds()
	srcW := max(1, srcB.Dx())
	srcH := max(1, srcB.Dy())

	availW := maxEdgePx - (cols+1)*contactSheetTileGapPx
	availH := maxEdgePx - (rows+1)*contactSheetTileGapPx - rows*contactSheetTileHeaderPx
	maxTileW := max(contactSheetMinTileEdge, availW/cols)
	maxTileH := max(contactSheetMinTileEdge, availH/rows)

	scale := math.Min(float64(maxTileW)/float64(srcW), float64(maxTileH)/float64(srcH))
	if scale <= 0 || scale > 1.0 {
		scale = math.Min(1.0, math.Min(float64(maxTileW)/float64(srcW), float64(maxTileH)/float64(srcH)))
	}
	tileW := max(1, int(math.Round(float64(srcW)*scale)))
	tileH := max(1, int(math.Round(float64(srcH)*scale)))

	sheetW := cols*tileW + (cols+1)*contactSheetTileGapPx
	sheetH := rows*(tileH+contactSheetTileHeaderPx) + (rows+1)*contactSheetTileGapPx
	if sheetW > maxEdgePx || sheetH > maxEdgePx {
		fit := math.Min(float64(maxEdgePx)/float64(sheetW), float64(maxEdgePx)/float64(sheetH))
		tileW = max(64, int(math.Floor(float64(tileW)*fit)))
		tileH = max(64, int(math.Floor(float64(tileH)*fit)))
		sheetW = cols*tileW + (cols+1)*contactSheetTileGapPx
		sheetH = rows*(tileH+contactSheetTileHeaderPx) + (rows+1)*contactSheetTileGapPx
	}

	canvas := image.NewRGBA(image.Rect(0, 0, sheetW, sheetH))
	draw.Draw(canvas, canvas.Bounds(), &image.Uniform{C: color.RGBA{R: 18, G: 20, B: 26, A: 255}}, image.Point{}, draw.Src)

	for i, tile := range tiles {
		r := i / cols
		c := i % cols
		x0 := contactSheetTileGapPx + c*(tileW+contactSheetTileGapPx)
		y0 := contactSheetTileGapPx + r*(tileH+contactSheetTileHeaderPx+contactSheetTileGapPx)

		// Header bar
		headerRect := image.Rect(x0, y0, x0+tileW, y0+contactSheetTileHeaderPx)
		draw.Draw(canvas, headerRect, &image.Uniform{C: color.RGBA{R: 32, G: 36, B: 46, A: 255}}, image.Point{}, draw.Src)

		// Scaled video frame
		frameRect := image.Rect(x0, y0+contactSheetTileHeaderPx, x0+tileW, y0+contactSheetTileHeaderPx+tileH)
		xdraw.BiLinear.Scale(canvas, frameRect, tile.Img, tile.Img.Bounds(), draw.Over, nil)

		// Timestamp label
		label := fmt.Sprintf("#%d %.2fs", i+1, tile.TimeSec)
		drawSheetText(canvas, x0+6, y0+15, label, color.RGBA{R: 238, G: 242, B: 248, A: 255})

		// Signal badges on right side of header
		bx := x0 + tileW - 4
		for bi := len(tile.Signals) - 1; bi >= 0; bi-- {
			sig := tile.Signals[bi]
			bw := len(sig)*7 + 8
			if bx-bw < x0+len(label)*7+12 {
				break
			}
			badgeRect := image.Rect(bx-bw, y0+3, bx, y0+contactSheetTileHeaderPx-3)
			draw.Draw(canvas, badgeRect, &image.Uniform{C: signalBadgeColor(sig)}, image.Point{}, draw.Src)
			drawSheetText(canvas, bx-bw+4, y0+15, sig, color.RGBA{R: 255, G: 255, B: 255, A: 255})
			bx -= bw + 4
		}
	}

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, canvas, &jpeg.Options{Quality: jpegQuality}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func signalBadgeColor(sig string) color.RGBA {
	switch sig {
	case "BLACK":
		return color.RGBA{R: 196, G: 44, B: 44, A: 255}
	case "FREEZE":
		return color.RGBA{R: 214, G: 122, B: 24, A: 255}
	case "CUT":
		return color.RGBA{R: 34, G: 132, B: 212, A: 255}
	default:
		return color.RGBA{R: 88, G: 96, B: 112, A: 255}
	}
}

func drawSheetText(dst *image.RGBA, x, y int, text string, col color.Color) {
	d := &font.Drawer{
		Dst:  dst,
		Src:  image.NewUniform(col),
		Face: basicfont.Face7x13,
		Dot:  fixed.P(x, y),
	}
	d.DrawString(text)
}
