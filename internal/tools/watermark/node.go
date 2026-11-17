// Package watermark provides the watermark remover node for the engine.
// It supports single-image and multi-threaded batch processing with zero-hardcode configuration.
package watermark

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"

	"app/internal/apperr"
	"app/internal/config"
	"app/internal/contracts"
	"app/internal/engine"
	"app/internal/fsutil"
	"app/internal/progress"
	"app/internal/safego"
	"app/internal/service/media"
	"app/pkg/watermark"
)

// NodeType is the unique identifier for the watermark remover node.
const NodeType = contracts.NodeTypeWatermarkRemover

// MediaRegistrar registers newly created or copied media assets into the library.
type MediaRegistrar interface {
	RegisterAsset(ctx context.Context, params media.RegisterAssetParams) (*contracts.MediaAssetItem, error)
}

// Options configures default directories for the node.
type Options struct {
	MediaDir      string // Application media storage directory
	MediaDirFunc  func() string
	OutputDir     string // Default folder to save cleaned images
	OutputDirFunc func() string
	Templates     []watermark.Template
	Registrar     MediaRegistrar
	Config        *config.Config
}

func resolveMediaDir(opts Options) string {
	if opts.MediaDirFunc != nil {
		return opts.MediaDirFunc()
	}
	return opts.MediaDir
}

func resolveOutputDir(opts Options) string {
	if opts.OutputDirFunc != nil {
		return opts.OutputDirFunc()
	}
	return opts.OutputDir
}

// RemoverImage accepts either an engine.MediaRef object or a plain string path.
type RemoverImage = engine.ImageInputRef

// RemoverImages accepts a string, []string, MediaRef, or []MediaRef.
type RemoverImages = engine.ImageInputRefs

// RemoverInput is the input payload for the watermark remover node.
type RemoverInput = contracts.RemoverInput

// RemoverItem provides execution details for each processed image.
type RemoverItem = contracts.RemoverItem

// RemoverOutput is the output payload produced by the watermark remover node.
type RemoverOutput = contracts.RemoverOutput

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
			{Name: "output_dir", Label: "Output directory (empty = workspace watermark folder)", Kind: engine.ParamText, Role: engine.ParamRoleOutputDir},
			{Name: "workers", Label: "CPU worker threads (0 = Auto)", Kind: engine.ParamInteger, Min: 0, Max: 64, Default: "0"},
			{
				Name: "corner", Label: "Scan corner", Kind: engine.ParamSelect,
				Options: []string{
					string(watermark.CornerBottomRight), string(watermark.CornerBottomLeft),
					string(watermark.CornerTopRight), string(watermark.CornerTopLeft), string(watermark.CornerAny),
				},
				Default: string(watermark.CornerBottomRight),
			},
			{Name: "copy_clean", Label: "Copy clean images to destination", Kind: engine.ParamToggle, Default: "true"},
			{Name: "preserve_tree", Label: "Preserve directory structure", Kind: engine.ParamToggle, Default: "false"},
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

		maxWorkers := opts.Config.WatermarkMaxWorkers()
		maxPixels := opts.Config.WatermarkMaxImagePixels()
		decodeConcurrency := opts.Config.WatermarkDecodeConcurrency()

		workers := in.Workers
		if workers <= 0 {
			workers = maxWorkers
		}

		cfg := watermark.Config{
			Corner:    watermark.Corner(in.Corner),
			Workers:   workers,
			MinScore:  in.MinScore,
			MinGain:   in.MinGain,
			MaxGain:   in.MaxGain,
			Templates: opts.Templates,
		}
		eng := watermark.New(cfg)
		eng.SetLimits(maxPixels, decodeConcurrency)

		if ctx.Err() != nil {
			return RemoverOutput{}, ctx.Err()
		}

		mediaDir := resolveMediaDir(opts)
		targetDir := in.OutputDir
		if targetDir == "" && !in.InPlace {
			targetDir = resolveOutputDir(opts)
		}
		if targetDir != "" {
			// Anchor a relative folder exactly like the input paths, so the file guard checks the
			// same folder the node then creates and writes (never the server's working directory).
			resolved := engine.ResolveMediaPath(targetDir, mediaDir)
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
				resPath, data, err := readInput(streamCtx, p, mediaDir)
				resolvedPaths[i] = resPath
				if err != nil {
					if streamCtx.Err() != nil {
						return
					}
					itemResults[i] = RemoverItem{Path: p, Status: watermark.StatusFailed, Error: err.Error()}
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

		for br := range eng.ProcessStream(streamCtx, itemsCh, workers) {
			if ctx.Err() != nil {
				return RemoverOutput{}, ctx.Err()
			}
			i, err := strconv.Atoi(br.ID)
			if err != nil || i < 0 || i >= total {
				continue
			}
			itemResults[i], outPaths[i] = finishItem(ctx, in, targetDir, rawPaths[i], resolvedPaths[i], br, opts)
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
	resPath := engine.ResolveMediaPath(raw, mediaDir)
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

// finishItem writes one processed image (when a watermark was found) or handles CopyClean (when clean)
// and returns its result and output path ("" when the item failed).
func finishItem(ctx context.Context, in RemoverInput, targetDir, rawPath, resPath string, br watermark.BatchResult, nodeOpts ...Options) (RemoverItem, string) {
	if err := ctx.Err(); err != nil {
		return RemoverItem{
			Path:   rawPath,
			Status: watermark.StatusFailed,
			Error:  err.Error(),
		}, ""
	}

	if br.Err != nil {
		return RemoverItem{
			Path:   rawPath,
			Status: watermark.StatusFailed,
			Error:  fmt.Sprintf("processing failed: %v", br.Err),
		}, ""
	}

	taskParams := watermark.TaskParams{
		InputPath: resPath,
		Corner:    in.Corner,
		MinScore:  in.MinScore,
		MinGain:   in.MinGain,
		MaxGain:   in.MaxGain,
	}

	// Determine destDir taking PreserveTree into account when relative paths are supplied
	destDir := targetDir
	if destDir == "" {
		destDir = filepath.Dir(resPath)
	} else if in.PreserveTree != nil && *in.PreserveTree && !filepath.IsAbs(rawPath) {
		cleanRel := filepath.Clean(rawPath)
		if !strings.HasPrefix(cleanRel, "..") {
			dir := filepath.Dir(cleanRel)
			if dir != "." && dir != string(filepath.Separator) {
				destDir = filepath.Join(targetDir, dir)
			}
		}
	}

	copyClean := targetDir != ""
	if in.CopyClean != nil {
		copyClean = *in.CopyClean
	}
	if in.InPlace {
		copyClean = false
	}

	ext := filepath.Ext(resPath)
	base := strings.TrimSuffix(filepath.Base(resPath), ext)

	// Case 1: No watermark detected
	if !br.Detected {
		res := RemoverItem{
			Path:     rawPath,
			Detected: false,
			Score:    br.Score,
			Size:     br.Info.Size,
		}

		if !copyClean {
			res.Status = watermark.StatusSkipped
			res.Reason = watermark.ReasonNoWatermark
			res.OutPath = resPath
			return res, resPath
		}

		// Copy clean original image to destination folder
		if err := engine.CheckFile(ctx, destDir, true); err != nil {
			res.Status = watermark.StatusFailed
			res.Error = formatPathError("create directory", destDir, err)
			return res, ""
		}
		if err := os.MkdirAll(destDir, 0o755); err != nil {
			res.Status = watermark.StatusFailed
			res.Error = formatPathError("create directory", destDir, err)
			return res, ""
		}

		outPath, isIdempotent := watermark.ResolveOutputPath(destDir, base, "", ext, taskParams, nil)
		if isIdempotent {
			res.Status = watermark.StatusSkipped
			res.Reason = watermark.ReasonIdempotentSkip
			res.OutPath = outPath
			res.AssetID = registerWatermarkAsset(ctx, nodeOpts, destDir, outPath)
			return res, outPath
		}

		if err := engine.CheckFile(ctx, outPath, true); err != nil {
			res.Status = watermark.StatusFailed
			res.Error = formatPathError("write file", outPath, err)
			return res, ""
		}

		perm := os.FileMode(0o644)
		if fi, err := os.Stat(resPath); err == nil {
			perm = fi.Mode().Perm()
		}
		if err := watermark.CopyFileAtomicContext(ctx, resPath, outPath, perm); err != nil {
			res.Status = watermark.StatusFailed
			res.Error = formatPathError("write file", outPath, err)
			return res, ""
		}
		watermark.DefaultProvenance.Record(outPath, taskParams)
		res.Status = watermark.StatusCopied
		res.OutPath = outPath
		res.AssetID = registerWatermarkAsset(ctx, nodeOpts, destDir, outPath)
		return res, outPath
	}

	// Case 2: Watermark detected and removed
	res := RemoverItem{
		Path:     rawPath,
		Detected: true,
		Score:    br.Score,
		Size:     br.Info.Size,
	}

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
		if !strings.EqualFold(outPath, resPath) {
			if _, err := os.Stat(outPath); err == nil {
				res.Status = watermark.StatusFailed
				res.Error = fmt.Sprintf("target file %q already exists", filepath.Base(outPath))
				return res, ""
			} else if !os.IsNotExist(err) {
				res.Status = watermark.StatusFailed
				res.Error = formatPathError("inspect target", outPath, err)
				return res, ""
			}
		}
	} else {
		if err := engine.CheckFile(ctx, destDir, true); err != nil {
			res.Status = watermark.StatusFailed
			res.Error = formatPathError("create directory", destDir, err)
			return res, ""
		}
		if err := os.MkdirAll(destDir, 0o755); err != nil {
			res.Status = watermark.StatusFailed
			res.Error = formatPathError("create directory", destDir, err)
			return res, ""
		}

		var isIdempotent bool
		outPath, isIdempotent = watermark.ResolveOutputPath(destDir, base, "_nowm", outExt, taskParams, nil)
		if isIdempotent {
			res.Status = watermark.StatusSkipped
			res.Reason = watermark.ReasonIdempotentSkip
			res.OutPath = outPath
			res.AssetID = registerWatermarkAsset(ctx, nodeOpts, destDir, outPath)
			return res, outPath
		}
	}

	if err := engine.CheckFile(ctx, outPath, true); err != nil {
		res.Status = watermark.StatusFailed
		res.Error = formatPathError("write file", outPath, err)
		return res, ""
	}

	perm := os.FileMode(0o644)
	if in.InPlace {
		if fi, err := os.Stat(resPath); err == nil {
			perm = fi.Mode().Perm()
		}
	}
	if err := ctx.Err(); err != nil {
		res.Status = watermark.StatusFailed
		res.Error = err.Error()
		return res, ""
	}
	if err := fsutil.WriteFileAtomic(outPath, br.Output, perm); err != nil {
		res.Status = watermark.StatusFailed
		res.Error = formatPathError("write file", outPath, err)
		return res, ""
	}
	if in.InPlace && !strings.EqualFold(outPath, resPath) {
		_ = os.Remove(resPath)
	}

	watermark.DefaultProvenance.Record(outPath, taskParams)
	res.Status = watermark.StatusProcessed
	res.OutPath = outPath
	res.AssetID = registerWatermarkAsset(ctx, nodeOpts, destDir, outPath)
	return res, outPath
}

func registerWatermarkAsset(ctx context.Context, nodeOpts []Options, destDir, outPath string) string {
	if len(nodeOpts) == 0 || nodeOpts[0].Registrar == nil || strings.TrimSpace(outPath) == "" {
		return ""
	}
	opts := nodeOpts[0]
	root := resolveMediaDir(opts)
	if strings.TrimSpace(destDir) != "" {
		root = destDir
	}
	if root == "" {
		root = filepath.Dir(outPath)
	}
	if adder, ok := opts.Registrar.(interface{ AddAllowedRoot(string) }); ok && root != "" {
		adder.AddAllowedRoot(root)
	}
	asset, err := opts.Registrar.RegisterAsset(ctx, media.RegisterAssetParams{
		Kind:           "image",
		SourceNode:     NodeType,
		FilePath:       outPath,
		RegisteredRoot: root,
	})
	if err != nil || asset == nil {
		return ""
	}
	return asset.ID
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
