// Package watermark provides the watermark remover node for the engine.
// It supports single-image and multi-threaded batch processing with zero-hardcode configuration.
package watermark

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"app/internal/engine"
	"app/internal/fsutil"
	"app/internal/progress"
	"app/pkg/watermark"
)

// NodeType is the unique identifier for the watermark remover node.
const NodeType = "tool.watermark_remover"

// Options configures default directories for the node.
type Options struct {
	MediaDir  string // Application media storage directory
	OutputDir string // Default folder to save cleaned images
	Templates []watermark.Template
}

// FlexibleImagePort accepts either an engine.MediaRef object or a plain string path.
type FlexibleImagePort struct {
	engine.MediaRef
}

func (f *FlexibleImagePort) UnmarshalJSON(data []byte) error {
	if len(data) == 0 || string(data) == "null" {
		return nil
	}
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		if s != "" {
			f.MediaRef = engine.RefFor(s, engine.MediaImage)
		}
		return nil
	}
	return json.Unmarshal(data, &f.MediaRef)
}

// FlexibleImagesPort accepts a string, []string, MediaRef, or []MediaRef.
type FlexibleImagesPort []engine.MediaRef

func (f *FlexibleImagesPort) UnmarshalJSON(data []byte) error {
	if len(data) == 0 || string(data) == "null" {
		return nil
	}

	// Try single string
	var singleStr string
	if err := json.Unmarshal(data, &singleStr); err == nil {
		if singleStr != "" {
			*f = []engine.MediaRef{engine.RefFor(singleStr, engine.MediaImage)}
		}
		return nil
	}

	// Try single MediaRef
	var singleRef engine.MediaRef
	if err := json.Unmarshal(data, &singleRef); err == nil && singleRef.LocalURL != "" {
		*f = []engine.MediaRef{singleRef}
		return nil
	}

	// Try array of strings
	var strArr []string
	if err := json.Unmarshal(data, &strArr); err == nil {
		refs := make([]engine.MediaRef, 0, len(strArr))
		for _, s := range strArr {
			if s != "" {
				refs = append(refs, engine.RefFor(s, engine.MediaImage))
			}
		}
		*f = refs
		return nil
	}

	// Try array of MediaRefs
	var refArr []engine.MediaRef
	if err := json.Unmarshal(data, &refArr); err == nil {
		*f = refArr
		return nil
	}

	return fmt.Errorf("cannot decode image list: %s", string(data))
}

// NodeInput is the input payload for the watermark remover node.
type NodeInput struct {
	Image     FlexibleImagePort  `json:"image,omitempty"`
	Images    FlexibleImagesPort `json:"images,omitempty"`
	InPlace   bool               `json:"inplace,omitempty"`
	OutputDir string             `json:"output_dir,omitempty"`
	Workers   int                `json:"workers,omitempty"`
	Corner    string             `json:"corner,omitempty"`
	MinScore  float64            `json:"min_score,omitempty"`
	MinGain   float64            `json:"min_gain,omitempty"`
	MaxGain   float64            `json:"max_gain,omitempty"`
}

// ItemResult provides execution details for each processed image.
type ItemResult struct {
	Path     string  `json:"path"`
	OutPath  string  `json:"out_path"`
	Detected bool    `json:"detected"`
	Score    float64 `json:"score"`
	Size     int     `json:"size,omitempty"`
	Error    string  `json:"error,omitempty"`
}

// NodeOutput is the output payload produced by the watermark remover node.
type NodeOutput struct {
	Image         engine.MediaRef   `json:"image"`
	Images        []engine.MediaRef `json:"images"`
	Detected      bool              `json:"detected"`
	TotalCount    int               `json:"total_count"`
	DetectedCount int               `json:"detected_count"`
	Results       []ItemResult      `json:"results,omitempty"`
}

// NewNode builds an engine.Node for watermark detection and reconstruction with multi-threading.
func NewNode(opts Options) engine.Node {
	spec := engine.Spec{
		Type:        NodeType,
		Provider:    "tool",
		Label:       "Watermark Remover",
		Description: "Automatically detect and remove Google Flow / Imagen / Gemini watermarks from images (Multi-threaded)",
		Inputs: []engine.Port{
			{Name: "image", Label: "Single image (Optional)", Type: engine.PortImage, Multiple: false, Optional: true},
			{Name: "images", Label: "Batch images (Multi-threaded)", Type: engine.PortImage, Multiple: true, Optional: true},
		},
		Outputs: []engine.Port{
			{Name: "image", Label: "Cleaned image (Single)", Type: engine.PortImage, Multiple: false},
			{Name: "images", Label: "Cleaned images (Batch)", Type: engine.PortImage, Multiple: true},
		},
		Params: []engine.Param{
			{Name: "inplace", Label: "Overwrite original file", Kind: engine.ParamToggle, Default: "false", Destructive: true},
			{Name: "output_dir", Label: "Output directory (empty = next to each source image)", Kind: engine.ParamText, Role: engine.ParamRoleOutputDir},
			{Name: "workers", Label: "CPU worker threads (0 = Auto)", Kind: engine.ParamInteger, Min: 0, Max: 64, Default: "0"},
			{
				Name: "corner", Label: "Scan corner", Kind: engine.ParamSelect,
				Options: []string{
					string(watermark.CornerBottomRight), string(watermark.CornerBottomLeft),
					string(watermark.CornerTopRight), string(watermark.CornerTopLeft), string(watermark.CornerAny),
				},
				Default: string(watermark.CornerBottomRight),
			},
		},
	}

	return engine.Typed(spec, func(ctx context.Context, in NodeInput) (NodeOutput, error) {
		var rawPaths []string
		for _, ref := range in.Images {
			if ref.LocalURL != "" && !slices.Contains(rawPaths, ref.LocalURL) {
				rawPaths = append(rawPaths, ref.LocalURL)
			}
		}
		if in.Image.LocalURL != "" && !slices.Contains(rawPaths, in.Image.LocalURL) {
			rawPaths = append([]string{in.Image.LocalURL}, rawPaths...)
		}

		if len(rawPaths) == 0 {
			return NodeOutput{}, engine.Invalid("Watermark Remover input port 'image' or 'images' received no file path")
		}

		cfg := watermark.Config{
			Corner:    watermark.Corner(in.Corner),
			Workers:   in.Workers,
			MinScore:  in.MinScore,
			MinGain:   in.MinGain,
			MaxGain:   in.MaxGain,
			Templates: opts.Templates,
		}
		eng := watermark.New(cfg)

		if ctx.Err() != nil {
			return NodeOutput{}, ctx.Err()
		}

		resolvedPaths := make([]string, len(rawPaths))
		items := make([]watermark.BatchItem, 0, len(rawPaths))
		itemIdxMap := make(map[string]int)
		readErrors := make([]error, len(rawPaths))

		for i, p := range rawPaths {
			if ctx.Err() != nil {
				return NodeOutput{}, ctx.Err()
			}
			resPath := resolveFilePath(p, opts.MediaDir)
			resolvedPaths[i] = resPath
			data, err := os.ReadFile(resPath)
			if err != nil {
				readErrors[i] = err
				continue
			}
			itemID := strconv.Itoa(i)
			items = append(items, watermark.BatchItem{ID: itemID, Data: data})
			itemIdxMap[itemID] = i
		}

		batchResults := eng.ProcessBatchProgress(ctx, items, in.Workers, func(done, total int) {
			progress.Report(ctx, progress.Update{
				Percent: float64(done) * 100 / float64(total),
				Message: fmt.Sprintf("Processed %d / %d images", done, total),
				Key:     "workflow.progress.items",
				Params:  map[string]string{"done": strconv.Itoa(done), "total": strconv.Itoa(total)},
			})
		})
		if ctx.Err() != nil {
			return NodeOutput{}, ctx.Err()
		}
		batchResultMap := make(map[int]watermark.BatchResult, len(batchResults))
		for _, br := range batchResults {
			if idx, ok := itemIdxMap[br.ID]; ok {
				batchResultMap[idx] = br
			}
		}

		itemResults := make([]ItemResult, len(rawPaths))
		outMediaRefs := make([]engine.MediaRef, 0, len(rawPaths))
		detectedCount := 0
		failedCount := 0
		var firstErr string

		targetDir := in.OutputDir
		if targetDir == "" {
			targetDir = opts.OutputDir
		}

		for i, rawPath := range rawPaths {
			resPath := resolvedPaths[i]
			outPath := resPath

			if readErr := readErrors[i]; readErr != nil {
				failedCount++
				if firstErr == "" {
					firstErr = fmt.Sprintf("failed to read %s: %v", rawPath, readErr)
				}
				itemResults[i] = ItemResult{
					Path:  rawPath,
					Error: fmt.Sprintf("failed to read file: %v", readErr),
				}
				continue
			}

			br := batchResultMap[i]
			if br.Err != nil {
				failedCount++
				if firstErr == "" {
					firstErr = fmt.Sprintf("processing %s: %v", rawPath, br.Err)
				}
				itemResults[i] = ItemResult{
					Path:  rawPath,
					Error: fmt.Sprintf("processing failed: %v", br.Err),
				}
				continue
			}

			if br.Detected {
				detectedCount++
				ext := filepath.Ext(resPath)
				base := strings.TrimSuffix(filepath.Base(resPath), ext)

				// Determine output extension according to output format:
				// WebP converted to standard PNG must use .png extension.
				outExt := ext
				if br.OutputFormat == watermark.FormatPNG && (strings.EqualFold(ext, ".webp") || strings.EqualFold(ext, "")) {
					outExt = ".png"
				} else if br.OutputFormat == watermark.FormatJPEG && !strings.EqualFold(ext, ".jpg") && !strings.EqualFold(ext, ".jpeg") {
					outExt = ".jpg"
				}

				if in.InPlace {
					if outExt != ext {
						outPath = filepath.Join(filepath.Dir(resPath), base+outExt)
					} else {
						outPath = resPath
					}
				} else {
					destDir := targetDir
					if destDir == "" {
						destDir = filepath.Dir(resPath)
					}
					if err := os.MkdirAll(destDir, 0o755); err != nil {
						failedCount++
						if firstErr == "" {
							firstErr = fmt.Sprintf("failed to create directory %q: %v", destDir, err)
						}
						itemResults[i] = ItemResult{
							Path:     rawPath,
							Detected: true,
							Score:    br.Score,
							Size:     br.Info.Size,
							Error:    fmt.Sprintf("failed to create directory %q: %v", destDir, err),
						}
						continue
					}
					outPath = filepath.Join(destDir, base+"_nowm"+outExt)
				}

				perm := os.FileMode(0o644)
				if in.InPlace {
					if fi, err := os.Stat(resPath); err == nil {
						perm = fi.Mode().Perm()
					}
				}

				if err := fsutil.WriteFileAtomic(outPath, br.Output, perm); err != nil {
					failedCount++
					if firstErr == "" {
						firstErr = fmt.Sprintf("failed to write file %q: %v", outPath, err)
					}
					itemResults[i] = ItemResult{
						Path:     rawPath,
						Detected: true,
						Score:    br.Score,
						Size:     br.Info.Size,
						Error:    fmt.Sprintf("failed to write file %q: %v", outPath, err),
					}
					continue
				}
				if in.InPlace && outPath != resPath {
					_ = os.Remove(resPath)
				}
			}

			itemResults[i] = ItemResult{
				Path:     rawPath,
				OutPath:  outPath,
				Detected: br.Detected,
				Score:    br.Score,
				Size:     br.Info.Size,
			}
			outMediaRefs = append(outMediaRefs, engine.RefFor(outPath, engine.MediaImage))
		}

		var primaryRef engine.MediaRef
		if len(outMediaRefs) > 0 {
			primaryRef = outMediaRefs[0]
		}

		if failedCount == len(rawPaths) && len(rawPaths) > 0 {
			return NodeOutput{
				Image:         primaryRef,
				Images:        outMediaRefs,
				Detected:      detectedCount > 0,
				TotalCount:    len(rawPaths),
				DetectedCount: detectedCount,
				Results:       itemResults,
			}, fmt.Errorf("watermark removal failed for all %d images: %s", len(rawPaths), firstErr)
		}

		return NodeOutput{
			Image:         primaryRef,
			Images:        outMediaRefs,
			Detected:      detectedCount > 0,
			TotalCount:    len(rawPaths),
			DetectedCount: detectedCount,
			Results:       itemResults,
		}, nil
	})
}

func resolveFilePath(p, mediaDir string) string {
	cleaned := filepath.Clean(p)
	if filepath.IsAbs(cleaned) {
		return cleaned
	}

	target := cleaned
	trimmed := strings.TrimPrefix(p, "/")
	trimmed = strings.TrimPrefix(trimmed, "\\")
	if strings.HasPrefix(trimmed, "media/") || strings.HasPrefix(trimmed, "media\\") {
		trimmed = strings.TrimPrefix(trimmed, "media/")
		trimmed = strings.TrimPrefix(trimmed, "media\\")
		target = filepath.Clean(trimmed)
	}

	if mediaDir != "" {
		joined := filepath.Join(mediaDir, target)
		rel, err := filepath.Rel(mediaDir, joined)
		if err != nil || rel == ".." || strings.HasPrefix(rel, "..") {
			return ""
		}
		return joined
	}
	return cleaned
}
