package studio

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/draw"
	_ "image/jpeg"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"strconv"

	_ "golang.org/x/image/webp"

	"app/internal/apperr"
	"app/internal/contracts"
	"app/internal/engine"
	"app/internal/fsutil"
	"app/internal/progress"
)

const (
	// defaultGridSplitAxis is the default storyboard split grid count (2x2 panels).
	defaultGridSplitAxis = 2
)

// NewGridSplitNode returns tool.grid_split to crop a multi-panel storyboard sheet into separate frame images.
func NewGridSplitNode(opts Options) engine.Node {
	opts.StudioConfig = withStudioDefaults(opts.StudioConfig)
	maxGrid := opts.StudioConfig.GridSplitMaxCount
	maxMargin := opts.StudioConfig.GridSplitMaxMarginPx
	maxGutter := opts.StudioConfig.GridSplitMaxGutterPx

	spec := engine.Spec{
		Type:        NodeTypeGridSplit,
		Provider:    studioProviderID,
		Label:       "Grid Storyboard Splitter",
		Description: "Splits a composite 2x2 or RxC storyboard grid image into clean individual panel images with optional margins, gutters, and interactive split weights.",
		Inputs: []engine.Port{
			{Name: "image_path", Label: "Grid Image", Type: engine.PortImage},
		},
		Outputs: []engine.Port{
			{Name: "images", Label: "Panel Images", Type: engine.PortImage, Multiple: true},
		},
		Params: []engine.Param{
			{Name: "rows", Label: "Rows", Kind: engine.ParamInteger, Min: 1, Max: maxGrid, Default: strconv.Itoa(defaultGridSplitAxis)},
			{Name: "cols", Label: "Columns", Kind: engine.ParamInteger, Min: 1, Max: maxGrid, Default: strconv.Itoa(defaultGridSplitAxis)},
			{Name: "margin_px", Label: "Outer Margin (px)", Kind: engine.ParamInteger, Min: 0, Max: maxMargin, Default: "0"},
			{Name: "gutter_px", Label: "Panel Gutter (px)", Kind: engine.ParamInteger, Min: 0, Max: maxGutter, Default: "0"},
			{Name: "row_weights", Label: "Row Weights (CSV)", Kind: engine.ParamText},
			{Name: "col_weights", Label: "Column Weights (CSV)", Kind: engine.ParamText},
			{Name: "output_dir", Label: "Output Folder", Kind: engine.ParamText, Role: engine.ParamRoleOutputDir},
		},
	}

	return engine.Typed(spec, func(ctx context.Context, in contracts.GridSplitInput) (contracts.GridSplitOutput, error) {
		imgPath, err := resolveMediaInput(ctx, opts, in.ImagePath.LocalURL, inputKindImage)
		if err != nil {
			return contracts.GridSplitOutput{}, err
		}
		if imgPath == "" {
			return contracts.GridSplitOutput{}, apperr.Validation("image_path is required").
				WithMsg("errors.studio.image_required", nil)
		}

		if in.Rows < 0 || in.Rows > maxGrid || in.Cols < 0 || in.Cols > maxGrid {
			return contracts.GridSplitOutput{}, apperr.Validation(fmt.Sprintf("rows and cols must be between 1 and %d", maxGrid)).
				WithMsg("errors.studio.invalid_grid_count", map[string]any{"min": 1, "max": maxGrid})
		}
		if len(in.RowWeights) > maxGrid || len(in.ColWeights) > maxGrid {
			return contracts.GridSplitOutput{}, apperr.Validation(fmt.Sprintf("grid weights cannot exceed %d entries", maxGrid)).
				WithMsg("errors.studio.invalid_grid_count", map[string]any{"min": 1, "max": maxGrid})
		}
		for _, w := range in.RowWeights {
			if math.IsNaN(w) || math.IsInf(w, 0) || w <= 0 {
				return contracts.GridSplitOutput{}, apperr.Validation("row_weights must contain positive finite numbers").
					WithMsg("errors.studio.invalid_grid_weights", nil)
			}
		}
		for _, w := range in.ColWeights {
			if math.IsNaN(w) || math.IsInf(w, 0) || w <= 0 {
				return contracts.GridSplitOutput{}, apperr.Validation("col_weights must contain positive finite numbers").
					WithMsg("errors.studio.invalid_grid_weights", nil)
			}
		}
		if in.MarginPx < 0 || in.MarginPx > maxMargin {
			return contracts.GridSplitOutput{}, apperr.Validation(fmt.Sprintf("margin_px must be between 0 and %d", maxMargin)).
				WithMsg("errors.studio.invalid_grid_margin", map[string]any{"max": maxMargin})
		}
		if in.GutterPx < 0 || in.GutterPx > maxGutter {
			return contracts.GridSplitOutput{}, apperr.Validation(fmt.Sprintf("gutter_px must be between 0 and %d", maxGutter)).
				WithMsg("errors.studio.invalid_grid_gutter", map[string]any{"max": maxGutter})
		}

		rows := in.Rows
		if rows == 0 {
			rows = defaultGridSplitAxis
		}
		if len(in.RowWeights) > 0 {
			rows = len(in.RowWeights)
		}
		cols := in.Cols
		if cols == 0 {
			cols = defaultGridSplitAxis
		}
		if len(in.ColWeights) > 0 {
			cols = len(in.ColWeights)
		}

		data, err := os.ReadFile(imgPath)
		if err != nil {
			if os.IsNotExist(err) {
				return contracts.GridSplitOutput{}, apperr.NotFound("Grid source image was not found").
					WithMsg("errors.studio.media_not_found", nil)
			}
			return contracts.GridSplitOutput{}, ioUnavailableErr("Failed to read grid source image", err)
		}
		srcImg, _, err := image.Decode(bytes.NewReader(data))
		if err != nil {
			return contracts.GridSplitOutput{}, apperr.Wrap(apperr.CodeIntegrity, "Unsupported or corrupted grid image", err).
				WithMsg("errors.studio.media_corrupt", nil)
		}
		b := srcImg.Bounds()
		usableW := b.Dx() - 2*in.MarginPx - (cols-1)*in.GutterPx
		usableH := b.Dy() - 2*in.MarginPx - (rows-1)*in.GutterPx
		if usableW < cols || usableH < rows {
			return contracts.GridSplitOutput{}, apperr.Validation("margin/gutter leaves insufficient image dimensions for requested grid").
				WithMsg("errors.studio.grid_dimensions_too_small", nil)
		}

		outDir, err := resolveNodeOutputDir(ctx, opts, in.OutputDir, filepath.Join("studio", "grid_panels"))
		if err != nil {
			return contracts.GridSplitOutput{}, err
		}

		colStarts, colWidths := splitAxisSpans(b.Min.X+in.MarginPx, usableW, cols, in.GutterPx, in.ColWeights)
		rowStarts, rowHeights := splitAxisSpans(b.Min.Y+in.MarginPx, usableH, rows, in.GutterPx, in.RowWeights)

		total := rows * cols
		out := contracts.GridSplitOutput{
			Images:    make([]engine.MediaRef, 0, total),
			Panels:    make([]contracts.GridPanel, 0, total),
			OutputDir: outDir,
		}
		idx := 0
		for r := 0; r < rows; r++ {
			for c := 0; c < cols; c++ {
				if err := ctx.Err(); err != nil {
					return contracts.GridSplitOutput{}, err
				}
				idx++
				progress.Report(ctx, progress.Update{
					Percent: float64(idx-1) / float64(max(1, total)) * 90.0,
					Message: fmt.Sprintf("Splitting grid panel %d/%d…", idx, total),
					Key:     "studio.progress.grid_split",
					Params: map[string]string{
						"current": strconv.Itoa(idx),
						"total":   strconv.Itoa(total),
					},
				})

				rect := image.Rect(colStarts[c], rowStarts[r], colStarts[c]+colWidths[c], rowStarts[r]+rowHeights[r])
				sub := image.NewRGBA(image.Rect(0, 0, rect.Dx(), rect.Dy()))
				draw.Draw(sub, sub.Bounds(), srcImg, rect.Min, draw.Src)

				var buf bytes.Buffer
				if err := png.Encode(&buf, sub); err != nil {
					return contracts.GridSplitOutput{}, ioUnavailableErr("Failed to encode cropped panel PNG", err)
				}
				outPath := filepath.Join(outDir, uniqueExportFilename(fmt.Sprintf("panel_r%d_c%d", r+1, c+1), ".png"))
				if err := checkStudioFile(ctx, outPath, true); err != nil {
					return contracts.GridSplitOutput{}, err
				}
				if err := fsutil.WriteFileAtomic(outPath, buf.Bytes(), 0o600); err != nil {
					return contracts.GridSplitOutput{}, ioUnavailableErr("Failed to write cropped panel image", err)
				}
				assetID := registerStudioAsset(ctx, opts, "image", NodeTypeGridSplit, outPath, outDir)
				out.Images = append(out.Images, engine.RefFor(outPath, engine.MediaImage))
				out.Panels = append(out.Panels, contracts.GridPanel{
					Index:   idx,
					Row:     r + 1,
					Col:     c + 1,
					Path:    outPath,
					Width:   rect.Dx(),
					Height:  rect.Dy(),
					AssetID: assetID,
				})
			}
		}
		progress.Report(ctx, progress.Update{Percent: 100, Message: "Grid panels split", Key: "studio.progress.done"})
		return out, nil
	})
}

func splitAxisSpans(origin, usable, count, gutter int, weights []float64) (starts, sizes []int) {
	starts = make([]int, count)
	sizes = make([]int, count)
	norm := make([]float64, count)
	sum := 0.0
	for i := 0; i < count; i++ {
		w := 1.0
		if i < len(weights) && weights[i] > 0 {
			w = weights[i]
		}
		norm[i] = w
		sum += w
	}
	pos := origin
	rem := usable
	for i := 0; i < count; i++ {
		starts[i] = pos
		var sz int
		if i == count-1 {
			sz = rem
		} else {
			sz = int(math.Round(float64(usable) * (norm[i] / sum)))
			if sz < 1 {
				sz = 1
			}
			if rem-sz < (count - 1 - i) {
				sz = rem - (count - 1 - i)
			}
		}
		sizes[i] = sz
		rem -= sz
		pos += sz + gutter
	}
	return starts, sizes
}
