package studio

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/draw"
	_ "image/jpeg"
	"image/png"
	"math"
	"os"
	"path/filepath"

	_ "golang.org/x/image/webp"

	"app/internal/apperr"
	"app/internal/contracts"
	"app/internal/engine"
	"app/internal/fsutil"
)

type gridSplitNode struct {
	opts Options
}

// NewGridSplitNode returns tool.grid_split to crop a multi-panel storyboard sheet into separate frame images.
func NewGridSplitNode(opts Options) engine.Node {
	opts.StudioConfig = withStudioDefaults(opts.StudioConfig)
	return &gridSplitNode{opts: opts}
}

func (n *gridSplitNode) Spec() engine.Spec {
	maxGrid := n.opts.StudioConfig.GridSplitMaxCount
	if maxGrid < 1 {
		maxGrid = 8
	}
	return engine.Spec{
		Type:        NodeTypeGridSplit,
		Provider:    "studio",
		Label:       "Grid Storyboard Splitter",
		Description: "Splits a composite 2x2 or RxC storyboard grid image into clean individual panel images with optional margins, gutters, and interactive split weights.",
		Inputs: []engine.Port{
			{Name: "image_path", Label: "Grid Image", Type: engine.PortImage},
		},
		Outputs: []engine.Port{
			{Name: "panels", Label: "Panel Images", Type: engine.PortImage, Multiple: true},
		},
		Params: []engine.Param{
			{Name: "rows", Label: "Rows", Kind: engine.ParamInteger, Min: 1, Max: maxGrid, Default: "2"},
			{Name: "cols", Label: "Columns", Kind: engine.ParamInteger, Min: 1, Max: maxGrid, Default: "2"},
			{Name: "margin_px", Label: "Outer Margin (px)", Kind: engine.ParamInteger, Min: 0, Max: 512, Default: "0"},
			{Name: "gutter_px", Label: "Panel Gutter (px)", Kind: engine.ParamInteger, Min: 0, Max: 256, Default: "0"},
			{Name: "output_dir", Label: "Output Folder", Kind: engine.ParamText, Role: engine.ParamRoleOutputDir},
		},
	}
}

func (n *gridSplitNode) Run(ctx context.Context, raw json.RawMessage) (any, error) {
	var in contracts.GridSplitInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, apperr.Validation("invalid grid_split input").WithDetails(map[string]any{"cause": err.Error()})
	}
	imgPath, err := resolveInputFile(ctx, n.opts, in.ImagePath)
	if err != nil {
		return nil, err
	}
	if imgPath == "" {
		return nil, apperr.Validation("image_path is required")
	}

	maxGrid := n.opts.StudioConfig.GridSplitMaxCount
	rows := clampInt(in.Rows, 2, 1, maxGrid)
	cols := clampInt(in.Cols, 2, 1, maxGrid)
	if len(in.RowWeights) > 0 {
		rows = clampInt(len(in.RowWeights), rows, 1, maxGrid)
	}
	if len(in.ColWeights) > 0 {
		cols = clampInt(len(in.ColWeights), cols, 1, maxGrid)
	}
	margin := max(0, in.MarginPx)
	gutter := max(0, in.GutterPx)

	data, err := os.ReadFile(imgPath)
	if err != nil {
		return nil, apperr.Wrap(apperr.CodeValidation, "cannot read grid image", err)
	}
	srcImg, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, apperr.Wrap(apperr.CodeValidation, "unsupported or corrupt image format", err)
	}
	b := srcImg.Bounds()
	usableW := b.Dx() - 2*margin - (cols-1)*gutter
	usableH := b.Dy() - 2*margin - (rows-1)*gutter
	if usableW < cols || usableH < rows {
		return nil, apperr.Validation("margin/gutter leaves insufficient image dimensions for requested grid")
	}

	outDir, err := resolveNodeOutputDir(ctx, n.opts, in.OutputDir, filepath.Join("studio", "grid_panels"))
	if err != nil {
		return nil, err
	}

	colStarts, colWidths := splitAxisSpans(b.Min.X+margin, usableW, cols, gutter, in.ColWeights)
	rowStarts, rowHeights := splitAxisSpans(b.Min.Y+margin, usableH, rows, gutter, in.RowWeights)

	out := contracts.GridSplitOutput{
		Panels:    make([]contracts.GridPanel, 0, rows*cols),
		OutputDir: outDir,
	}
	idx := 0
	for r := 0; r < rows; r++ {
		for c := 0; c < cols; c++ {
			idx++
			rect := image.Rect(colStarts[c], rowStarts[r], colStarts[c]+colWidths[c], rowStarts[r]+rowHeights[r])
			sub := image.NewRGBA(image.Rect(0, 0, rect.Dx(), rect.Dy()))
			draw.Draw(sub, sub.Bounds(), srcImg, rect.Min, draw.Src)

			var buf bytes.Buffer
			if err := png.Encode(&buf, sub); err != nil {
				return nil, apperr.Internal(err)
			}
			outPath := filepath.Join(outDir, uniqueExportFilename(fmt.Sprintf("panel_r%d_c%d", r+1, c+1), ".png"))
			if err := engine.CheckFile(ctx, outPath, true); err != nil {
				return nil, err
			}
			if err := fsutil.WriteFileAtomic(outPath, buf.Bytes(), 0o600); err != nil {
				return nil, apperr.Internal(err)
			}
			assetID := registerStudioAsset(ctx, n.opts, "image", NodeTypeGridSplit, outPath, outDir)
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
	return out, nil
}

func clampInt(val, def, minV, maxV int) int {
	if val == 0 {
		return def
	}
	if val < minV {
		return minV
	}
	if val > maxV {
		return maxV
	}
	return val
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
