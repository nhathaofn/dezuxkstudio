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
	"sync"

	"app/internal/apperr"
	"app/internal/engine"
	"app/internal/fsutil"
	"app/internal/progress"
	"app/internal/safego"
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

// RemoverImage accepts either an engine.MediaRef object or a plain string path.
type RemoverImage struct {
	engine.MediaRef
}

func (f *RemoverImage) UnmarshalJSON(data []byte) error {
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

// RemoverImages accepts a string, []string, MediaRef, or []MediaRef.
type RemoverImages []engine.MediaRef

func (f *RemoverImages) UnmarshalJSON(data []byte) error {
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

// RemoverInput is the input payload for the watermark remover node.
type RemoverInput struct {
	Image     RemoverImage  `json:"image,omitempty"`
	Images    RemoverImages `json:"images,omitempty"`
	InPlace   bool          `json:"inplace,omitempty"`
	OutputDir string        `json:"output_dir,omitempty"`
	Workers   int           `json:"workers,omitempty"`
	Corner    string        `json:"corner,omitempty"`
	MinScore  float64       `json:"min_score,omitempty"`
	MinGain   float64       `json:"min_gain,omitempty"`
	MaxGain   float64       `json:"max_gain,omitempty"`
}

// RemoverItem provides execution details for each processed image.
type RemoverItem struct {
	Path     string  `json:"path"`
	OutPath  string  `json:"out_path"`
	Detected bool    `json:"detected"`
	Score    float64 `json:"score"`
	Size     int     `json:"size,omitempty"`
	Error    string  `json:"error,omitempty"`
}

// RemoverOutput is the output payload produced by the watermark remover node.
type RemoverOutput struct {
	Image         engine.MediaRef   `json:"image"`
	Images        []engine.MediaRef `json:"images"`
	Detected      bool              `json:"detected"`
	TotalCount    int               `json:"total_count"`
	DetectedCount int               `json:"detected_count"`
	Results       []RemoverItem     `json:"results,omitempty"`
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

	return engine.Typed(spec, func(ctx context.Context, in RemoverInput) (RemoverOutput, error) {
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
			return RemoverOutput{}, engine.Invalid("Watermark Remover input port 'image' or 'images' received no file path")
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
			return RemoverOutput{}, ctx.Err()
		}

		targetDir := in.OutputDir
		if targetDir == "" {
			targetDir = opts.OutputDir
		}
		if targetDir != "" {
			// Anchor a relative folder exactly like the input paths, so the file guard checks the
			// same folder the node then creates and writes (never the server's working directory).
			resolved := resolveFilePath(targetDir, opts.MediaDir)
			if resolved == "" || !filepath.IsAbs(resolved) {
				return RemoverOutput{}, engine.Invalid("output_dir must be an absolute path or a folder inside the media folder")
			}
			targetDir = resolved
		}

		total := len(rawPaths)
		resolvedPaths := make([]string, total)
		itemResults := make([]RemoverItem, total)
		outPaths := make([]string, total) // "" = no output (failed item)

		// Stream the batch: a reader feeds files one at a time into a bounded channel and every
		// result is written to disk as soon as it arrives, so memory holds only ~workers images
		// at once instead of the whole batch (inputs + outputs) until the end.
		streamCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		itemsCh := make(chan watermark.BatchItem)
		var (
			doneMu sync.Mutex
			done   int
		)
		reportDone := func() {
			doneMu.Lock()
			done++
			d := done
			doneMu.Unlock()
			progress.Report(ctx, progress.Update{
				Percent: float64(d) * 100 / float64(total),
				Message: fmt.Sprintf("Processed %d / %d images", d, total),
				Key:     "workflow.progress.items",
				Params:  map[string]string{"done": strconv.Itoa(d), "total": strconv.Itoa(total)},
			})
		}
		safego.Go("watermark.node.reader", func() {
			defer close(itemsCh)
			for i, p := range rawPaths {
				resPath, data, err := readInput(streamCtx, p, opts.MediaDir)
				resolvedPaths[i] = resPath
				if err != nil {
					if streamCtx.Err() != nil {
						return
					}
					itemResults[i] = RemoverItem{Path: p, Error: err.Error()}
					reportDone()
					continue
				}
				select {
				case <-streamCtx.Done():
					return
				case itemsCh <- watermark.BatchItem{ID: strconv.Itoa(i), Data: data}:
				}
			}
		})

		for br := range eng.ProcessStream(streamCtx, itemsCh, in.Workers) {
			i, err := strconv.Atoi(br.ID)
			if err != nil || i < 0 || i >= total {
				continue
			}
			itemResults[i], outPaths[i] = finishItem(ctx, in, targetDir, rawPaths[i], resolvedPaths[i], br)
			reportDone()
		}
		if ctx.Err() != nil {
			return RemoverOutput{}, ctx.Err()
		}

		outMediaRefs := make([]engine.MediaRef, 0, total)
		detectedCount, failedCount := 0, 0
		var firstErr string
		for i, r := range itemResults {
			if r.Detected {
				detectedCount++
			}
			if r.Error != "" || outPaths[i] == "" {
				failedCount++
				if firstErr == "" {
					firstErr = fmt.Sprintf("%s: %s", filepath.Base(rawPaths[i]), r.Error)
				}
				continue
			}
			outMediaRefs = append(outMediaRefs, engine.RefFor(outPaths[i], engine.MediaImage))
		}

		var primaryRef engine.MediaRef
		if len(outMediaRefs) > 0 {
			primaryRef = outMediaRefs[0]
		}
		out := RemoverOutput{
			Image:         primaryRef,
			Images:        outMediaRefs,
			Detected:      detectedCount > 0,
			TotalCount:    total,
			DetectedCount: detectedCount,
			Results:       itemResults,
		}
		if failedCount == total {
			return out, apperr.New(apperr.CodeUnavailable, fmt.Sprintf("watermark removal failed for all %d images: %s", total, firstErr))
		}
		return out, nil
	})
}

// readInput resolves one input path, asks the caller's file guard for read access and loads it.
func readInput(ctx context.Context, raw, mediaDir string) (string, []byte, error) {
	resPath := resolveFilePath(raw, mediaDir)
	if resPath == "" {
		return "", nil, fmt.Errorf("failed to read file: path %q is outside the media folder", filepath.Base(raw))
	}
	if err := engine.CheckFile(ctx, resPath, false); err != nil {
		return resPath, nil, fmt.Errorf("failed to read file: %s", sanitizePathError(resPath, err))
	}
	data, err := os.ReadFile(resPath)
	if err != nil {
		return resPath, nil, fmt.Errorf("failed to read file: %s", sanitizePathError(resPath, err))
	}
	return resPath, data, nil
}

// finishItem writes one processed image (when a watermark was found) and returns its result and
// output path ("" when the item failed).
func finishItem(ctx context.Context, in RemoverInput, targetDir, rawPath, resPath string, br watermark.BatchResult) (RemoverItem, string) {
	if br.Err != nil {
		return RemoverItem{Path: rawPath, Error: fmt.Sprintf("processing failed: %v", br.Err)}, ""
	}
	res := RemoverItem{Path: rawPath, Detected: br.Detected, Score: br.Score, Size: br.Info.Size}
	if !br.Detected {
		res.OutPath = resPath
		return res, resPath
	}

	ext := filepath.Ext(resPath)
	base := strings.TrimSuffix(filepath.Base(resPath), ext)
	// WebP re-encoded as PNG must use .png; anything re-encoded as JPEG must use .jpg.
	outExt := ext
	if br.OutputFormat == watermark.FormatPNG && (strings.EqualFold(ext, ".webp") || ext == "") {
		outExt = ".png"
	} else if br.OutputFormat == watermark.FormatJPEG && !strings.EqualFold(ext, ".jpg") && !strings.EqualFold(ext, ".jpeg") {
		outExt = ".jpg"
	}

	var outPath string
	if in.InPlace {
		outPath = filepath.Join(filepath.Dir(resPath), base+outExt)
	} else {
		destDir := targetDir
		if destDir == "" {
			destDir = filepath.Dir(resPath)
		}
		if err := engine.CheckFile(ctx, destDir, true); err != nil {
			res.Error = formatPathError("create directory", destDir, err)
			return res, ""
		}
		if err := os.MkdirAll(destDir, 0o755); err != nil {
			res.Error = formatPathError("create directory", destDir, err)
			return res, ""
		}
		outPath = filepath.Join(destDir, base+"_nowm"+outExt)
	}
	if in.InPlace && !strings.EqualFold(outPath, resPath) {
		if _, err := os.Stat(outPath); err == nil {
			res.Error = fmt.Sprintf("target file %q already exists", filepath.Base(outPath))
			return res, ""
		} else if !os.IsNotExist(err) {
			res.Error = formatPathError("inspect target", outPath, err)
			return res, ""
		}
	}

	if err := engine.CheckFile(ctx, outPath, true); err != nil {
		res.Error = formatPathError("write file", outPath, err)
		return res, ""
	}

	perm := os.FileMode(0o644)
	if in.InPlace {
		if fi, err := os.Stat(resPath); err == nil {
			perm = fi.Mode().Perm()
		}
	}
	if err := fsutil.WriteFileAtomic(outPath, br.Output, perm); err != nil {
		res.Error = formatPathError("write file", outPath, err)
		return res, ""
	}
	if in.InPlace && !strings.EqualFold(outPath, resPath) {
		_ = os.Remove(resPath)
	}
	res.OutPath = outPath
	return res, outPath
}

func formatPathError(action, targetPath string, err error) string {
	return fmt.Sprintf("failed to %s %q: %s", action, filepath.Base(targetPath), sanitizePathError(targetPath, err))
}

// sanitizePathError strips full local directory paths from OS, fsutil, and file-guard errors so
// user-facing error messages only name the leaf file or folder.
func sanitizePathError(targetPath string, err error) string {
	if err == nil {
		return ""
	}
	msg := replacePathWithBase(err.Error(), targetPath)
	msg = sanitizeWrappedPathErrors(msg, err)
	if targetPath != "" {
		dir := filepath.Dir(filepath.Clean(targetPath))
		if dir != "" && dir != "." && filepath.Dir(dir) != dir {
			msg = replacePathVariants(msg, dir+string(filepath.Separator), "")
			if strings.ContainsAny(dir, `/\`) {
				msg = replacePathVariants(msg, dir, "target directory")
			}
		}
	}
	return msg
}

func sanitizeWrappedPathErrors(msg string, err error) string {
	switch e := err.(type) {
	case *os.PathError:
		msg = replacePathWithBase(msg, e.Path)
	case *os.LinkError:
		msg = replacePathWithBase(msg, e.Old)
		msg = replacePathWithBase(msg, e.New)
	}
	if u, ok := err.(interface{ Unwrap() error }); ok {
		if inner := u.Unwrap(); inner != nil {
			msg = sanitizeWrappedPathErrors(msg, inner)
		}
	} else if u, ok := err.(interface{ Unwrap() []error }); ok {
		for _, inner := range u.Unwrap() {
			if inner != nil {
				msg = sanitizeWrappedPathErrors(msg, inner)
			}
		}
	}
	return msg
}

func replacePathWithBase(msg, path string) string {
	if path == "" {
		return msg
	}
	cleaned := filepath.Clean(path)
	if cleaned == "." || filepath.Dir(cleaned) == cleaned {
		return msg
	}
	base := filepath.Base(cleaned)
	if base == "" || base == "." || base == string(filepath.Separator) {
		return msg
	}
	msg = replacePathVariants(msg, path, base)
	if cleaned != path {
		msg = replacePathVariants(msg, cleaned, base)
	}
	return msg
}

func replacePathVariants(msg, raw, replacement string) string {
	if raw == "" {
		return msg
	}
	repQuoted := strings.TrimSuffix(strings.TrimPrefix(strconv.Quote(replacement), `"`), `"`)
	for _, candidate := range []string{raw, filepath.ToSlash(raw)} {
		if candidate == "" {
			continue
		}
		msg = strings.ReplaceAll(msg, candidate, replacement)
		if quoted := strings.TrimSuffix(strings.TrimPrefix(strconv.Quote(candidate), `"`), `"`); quoted != candidate {
			msg = strings.ReplaceAll(msg, quoted, repQuoted)
		}
	}
	return msg
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
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return ""
		}
		return joined
	}
	return cleaned
}
