// Package watermark provides the watermark remover node for the engine.
package watermark

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		f.MediaRef = engine.RefFor(s, engine.MediaImage)
		return nil
	}
	return json.Unmarshal(data, &f.MediaRef)
}

// NodeInput is the input payload for the watermark remover node.
type NodeInput struct {
	Image     FlexibleImagePort `json:"image"`
	InPlace   bool              `json:"inplace,omitempty"`
	OutputDir string            `json:"output_dir,omitempty"`
}

// NodeOutput is the output payload produced by the watermark remover node.
type NodeOutput struct {
	Image    engine.MediaRef `json:"image"`
	Detected bool            `json:"detected"`
}

// NewNode builds an engine.Node for watermark detection and reconstruction.
func NewNode(opts Options) engine.Node {
	spec := engine.Spec{
		Type:        NodeType,
		Provider:    "tool",
		Label:       "Watermark Remover",
		Description: "Tự động phát hiện và xóa watermark Google Flow / Imagen / Gemini khỏi ảnh",
		Inputs: []engine.Port{
			{Name: "image", Label: "Ảnh đầu vào", Type: engine.PortImage, Multiple: false},
		},
		Outputs: []engine.Port{
			{Name: "image", Label: "Ảnh sau khi xóa", Type: engine.PortImage, Multiple: false},
		},
		Params: []engine.Param{
			{Name: "inplace", Label: "Ghi đè file gốc", Kind: engine.ParamToggle},
			{Name: "output_dir", Label: "Thư mục xuất", Kind: engine.ParamText},
		},
	}

	return engine.Typed(spec, func(ctx context.Context, in NodeInput) (NodeOutput, error) {
		rawPath := in.Image.LocalURL
		if rawPath == "" {
			return NodeOutput{}, engine.Invalid("Cổng 'image' của Watermark Remover không nhận được đường dẫn file")
		}

		resolvedPath := resolveFilePath(rawPath, opts.MediaDir)
		data, err := os.ReadFile(resolvedPath)
		if err != nil {
			return NodeOutput{}, fmt.Errorf("watermark_remover: không thể đọc file %q: %w", resolvedPath, err)
		}

		cleaned, modified, err := watermark.Remove(data)
		if err != nil {
			return NodeOutput{}, fmt.Errorf("watermark_remover: xử lý ảnh thất bại: %w", err)
		}

		outPath := resolvedPath
		if modified {
			if in.InPlace {
				outPath = resolvedPath
			} else {
				targetDir := opts.OutputDir
				if in.OutputDir != "" {
					targetDir = in.OutputDir
				}
				if targetDir == "" {
					targetDir = filepath.Dir(resolvedPath)
				}

				if err := os.MkdirAll(targetDir, 0o755); err != nil {
					return NodeOutput{}, fmt.Errorf("watermark_remover: không thể tạo thư mục lưu %q: %w", targetDir, err)
				}

				ext := filepath.Ext(resolvedPath)
				base := strings.TrimSuffix(filepath.Base(resolvedPath), ext)
				outPath = filepath.Join(targetDir, base+"_nowm"+ext)
			}

			if err := os.WriteFile(outPath, cleaned, 0o644); err != nil {
				return NodeOutput{}, fmt.Errorf("watermark_remover: không thể lưu ảnh đã xử lý: %w", err)
			}
		}

		return NodeOutput{
			Image:    engine.RefFor(outPath, engine.MediaImage),
			Detected: modified,
		}, nil
	})
}

func resolveFilePath(p, mediaDir string) string {
	cleaned := filepath.Clean(p)
	if filepath.IsAbs(cleaned) {
		return cleaned
	}

	// Handle web-style local routes like /media/xyz.png or media/xyz.png
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
