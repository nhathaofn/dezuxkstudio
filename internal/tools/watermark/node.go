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
	"app/pkg/watermark"
)

// NodeType is the unique identifier for the watermark remover node.
const NodeType = "tool.watermark_remover"

// Options configures default directories for the node.
type Options struct {
	MediaDir  string // Application media storage directory
	OutputDir string // Default folder to save cleaned images
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

	return fmt.Errorf("không thể giải mã danh sách ảnh: %s", string(data))
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
		Description: "Tự động phát hiện và xóa watermark Google Flow / Imagen / Gemini khỏi ảnh (Hỗ trợ xử lý đa luồng)",
		Inputs: []engine.Port{
			{Name: "image", Label: "Ảnh đơn (Tùy chọn)", Type: engine.PortImage, Multiple: false, Optional: true},
			{Name: "images", Label: "Danh sách ảnh đa luồng", Type: engine.PortImage, Multiple: true, Optional: true},
		},
		Outputs: []engine.Port{
			{Name: "image", Label: "Ảnh sau khi xóa (Đơn)", Type: engine.PortImage, Multiple: false},
			{Name: "images", Label: "Danh sách ảnh sau khi xóa (Batch)", Type: engine.PortImage, Multiple: true},
		},
		Params: []engine.Param{
			{Name: "inplace", Label: "Ghi đè file gốc", Kind: engine.ParamToggle},
			{Name: "output_dir", Label: "Thư mục xuất", Kind: engine.ParamText},
			{Name: "workers", Label: "Số luồng CPU (0 = Tự động)", Kind: engine.ParamInteger, Min: 0, Max: 64},
			{Name: "corner", Label: "Vị trí quét (bottom-right / bottom-left / top-right / top-left / any)", Kind: engine.ParamSelect},
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
			return NodeOutput{}, engine.Invalid("Cổng 'image' hoặc 'images' của Watermark Remover không nhận được đường dẫn file")
		}

		cfg := watermark.Config{
			Corner:   watermark.Corner(in.Corner),
			Workers:  in.Workers,
			MinScore: in.MinScore,
			MinGain:  in.MinGain,
			MaxGain:  in.MaxGain,
		}
		eng := watermark.New(cfg)

		resolvedPaths := make([]string, len(rawPaths))
		items := make([]watermark.BatchItem, 0, len(rawPaths))
		itemIdxMap := make(map[string]int)
		readErrors := make([]error, len(rawPaths))

		for i, p := range rawPaths {
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

		batchResults := eng.ProcessBatch(ctx, items, in.Workers)
		batchResultMap := make(map[int]watermark.BatchResult, len(batchResults))
		for _, br := range batchResults {
			if idx, ok := itemIdxMap[br.ID]; ok {
				batchResultMap[idx] = br
			}
		}

		itemResults := make([]ItemResult, len(rawPaths))
		outMediaRefs := make([]engine.MediaRef, len(rawPaths))
		detectedCount := 0

		targetDir := in.OutputDir
		if targetDir == "" {
			targetDir = opts.OutputDir
		}

		for i, rawPath := range rawPaths {
			resPath := resolvedPaths[i]
			outPath := resPath

			if readErr := readErrors[i]; readErr != nil {
				itemResults[i] = ItemResult{
					Path:  rawPath,
					Error: fmt.Sprintf("đọc file thất bại: %v", readErr),
				}
				outMediaRefs[i] = engine.RefFor(outPath, engine.MediaImage)
				continue
			}

			br := batchResultMap[i]
			if br.Err != nil {
				itemResults[i] = ItemResult{
					Path:  rawPath,
					Error: fmt.Sprintf("xử lý thất bại: %v", br.Err),
				}
				outMediaRefs[i] = engine.RefFor(outPath, engine.MediaImage)
				continue
			}

			if br.Detected {
				detectedCount++
				if in.InPlace {
					outPath = resPath
				} else {
					destDir := targetDir
					if destDir == "" {
						destDir = filepath.Dir(resPath)
					}
					if err := os.MkdirAll(destDir, 0o755); err != nil {
						itemResults[i] = ItemResult{
							Path:     rawPath,
							Detected: true,
							Score:    br.Score,
							Size:     br.Info.Size,
							Error:    fmt.Sprintf("tạo thư mục %q thất bại: %v", destDir, err),
						}
						outMediaRefs[i] = engine.RefFor(outPath, engine.MediaImage)
						continue
					}
					ext := filepath.Ext(resPath)
					base := strings.TrimSuffix(filepath.Base(resPath), ext)
					outPath = filepath.Join(destDir, base+"_nowm"+ext)
				}

				if err := os.WriteFile(outPath, br.Output, 0o644); err != nil {
					itemResults[i] = ItemResult{
						Path:     rawPath,
						Detected: true,
						Score:    br.Score,
						Size:     br.Info.Size,
						Error:    fmt.Sprintf("ghi file %q thất bại: %v", outPath, err),
					}
					outMediaRefs[i] = engine.RefFor(outPath, engine.MediaImage)
					continue
				}
			}

			itemResults[i] = ItemResult{
				Path:     rawPath,
				OutPath:  outPath,
				Detected: br.Detected,
				Score:    br.Score,
				Size:     br.Info.Size,
			}
			outMediaRefs[i] = engine.RefFor(outPath, engine.MediaImage)
		}

		var primaryRef engine.MediaRef
		if len(outMediaRefs) > 0 {
			primaryRef = outMediaRefs[0]
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

	trimmed := strings.TrimPrefix(p, "/")
	trimmed = strings.TrimPrefix(trimmed, "\\")
	if strings.HasPrefix(trimmed, "media/") || strings.HasPrefix(trimmed, "media\\") {
		trimmed = strings.TrimPrefix(trimmed, "media/")
		trimmed = strings.TrimPrefix(trimmed, "media\\")
		if mediaDir != "" {
			return filepath.Join(mediaDir, trimmed)
		}
	}

	if mediaDir != "" {
		return filepath.Join(mediaDir, cleaned)
	}
	return cleaned
}
