// Package studio implements DezuxkStudio's AI film production workflow nodes
// shared identically between the /studio UI and the built-in AI agent.
package studio

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"app/internal/apperr"
	"app/internal/config"
	"app/internal/contracts"
	domainbinaries "app/internal/domain/binaries"
	domainmedia "app/internal/domain/mediagen"
	domainprovider "app/internal/domain/provider"
	"app/internal/engine"
	"app/internal/fsutil"
	"app/internal/paths"
	"app/internal/service/media"
	"app/internal/tools/mediautil"
)

const (
	NodeTypeScriptNormalize   = contracts.NodeTypeScriptNormalize
	NodeTypeExtractAssets     = contracts.NodeTypeExtractAssets
	NodeTypeAuthorShotPrompts = contracts.NodeTypeAuthorShotPrompts
	NodeTypeProbeMedia        = contracts.NodeTypeProbeMedia
	NodeTypeVideoConcatBasic  = contracts.NodeTypeVideoConcatBasic
	NodeTypeBatchPreflight    = contracts.NodeTypeBatchPreflight
	NodeTypeGridSplit         = contracts.NodeTypeGridSplit
	NodeTypeMaskEdit          = contracts.NodeTypeMaskEdit
	NodeTypeExtractFrames     = contracts.NodeTypeExtractFrames
	NodeTypeTTSDubMux         = contracts.NodeTypeTTSDubMux
	NodeTypeLipSyncGenerate   = contracts.NodeTypeLipSyncGenerate
	NodeTypeQCReview          = contracts.NodeTypeQCReview
	NodeTypeTimelineAutoAssem = contracts.NodeTypeTimelineAutoAssem
	NodeTypeMediaPeaksThumbs  = contracts.NodeTypeMediaPeaksThumbs
	NodeTypeRenderTimeline    = contracts.NodeTypeRenderTimeline
	NodeTypeExportNLEDraft    = contracts.NodeTypeExportNLEDraft
	NodeTypeVideoContactSheet = contracts.NodeTypeVideoContactSheet
)

const (
	// studioProviderID is the canonical engine.Spec.Provider identifier shared by all Film Studio nodes.
	studioProviderID = "studio"

	// minVideoDimension is the minimum H.264/FFmpeg encodeable frame dimension in pixels (4x 16px macroblocks).
	minVideoDimension = 64

	// yuv420MacroblockAlign rounds video dimensions down to an even pixel boundary required by yuv420p chroma subsampling.
	yuv420MacroblockAlign = 2
)

type inputMediaKind uint8

const (
	inputKindImage inputMediaKind = 1 << iota
	inputKindVideo
	inputKindAudio
	inputKindSubtitle
	inputKindAnyMedia    = inputKindImage | inputKindVideo | inputKindAudio
	inputKindMediaOrSubs = inputKindAnyMedia | inputKindSubtitle
)

// TextGenerator executes structured LLM calls (satisfied by *service/mediagen.Service).
type TextGenerator interface {
	GenerateText(ctx context.Context, req domainmedia.TextRequest) (domainmedia.TextResult, error)
}

// ImageGenerator generates or edits images (satisfied by *service/mediagen.Service).
type ImageGenerator interface {
	GenerateImage(ctx context.Context, req domainmedia.ImageRequest) (domainmedia.Result, error)
}

// TTSGenerator synthesizes speech audio (satisfied by *service/mediagen.Service).
type TTSGenerator interface {
	GenerateTTS(ctx context.Context, req domainmedia.TTSRequest) (domainmedia.Result, error)
}

// LipSyncGenerator generates lip-synced videos (satisfied by *service/mediagen.Service).
type LipSyncGenerator interface {
	GenerateLipSync(ctx context.Context, req domainmedia.LipSyncRequest) (domainmedia.Result, error)
}

// ProviderChecker verifies provider & model readiness before running batches.
type ProviderChecker interface {
	CheckCapability(ctx context.Context, capability domainprovider.Capability, modelRef string) (providerID, modelID string, err error)
}

// CostEstimator resolves unit pricing in USD for preflight estimates.
type CostEstimator interface {
	EstimateUnitPriceUSD(ctx context.Context, providerID, modelID, capability, unit string) (unitPriceUSD float64, wildcardFallback bool, err error)
}

// BinaryResolver resolves managed external executables (satisfied by *service/binaries.Service).
type BinaryResolver interface {
	Resolve(ctx context.Context, id domainbinaries.BinaryID) (resolvedPath string, source domainbinaries.Source, customPath string, err error)
}

// MediaRegistrar registers newly created media files into the shared media library.
type MediaRegistrar interface {
	RegisterAsset(ctx context.Context, params media.RegisterAssetParams) (*contracts.MediaAssetItem, error)
}

// Options configures the film studio engine nodes.
type Options struct {
	TextGen           TextGenerator
	ImageGen          ImageGenerator
	TTSGen            TTSGenerator
	LipSyncGen        LipSyncGenerator
	ProviderChecker   ProviderChecker
	CostEstimator     CostEstimator
	Binaries          BinaryResolver
	Registrar         MediaRegistrar
	StudioConfig      config.StudioConfig
	MediaDir          string
	WorkspaceDir      string
	StagingDir        string
	WorkspaceTreeFunc func() paths.WorkspaceTree
	Logger            *slog.Logger
}

// NewNodes returns all Film Studio engine nodes (Phase 1 + Phase 2 + Phase 3).
func NewNodes(opts Options) []engine.Node {
	opts.StudioConfig = withStudioDefaults(opts.StudioConfig)
	return []engine.Node{
		NewScriptNormalizeNode(opts),
		NewExtractAssetsNode(opts),
		NewAuthorShotPromptsNode(opts),
		NewProbeMediaNode(opts),
		NewVideoConcatBasicNode(opts),
		NewBatchPreflightNode(opts),
		NewGridSplitNode(opts),
		NewMaskEditNode(opts),
		NewExtractFramesNode(opts),
		NewTTSDubMuxNode(opts),
		NewLipSyncGenerateNode(opts),
		NewQCReviewNode(opts),
		NewTimelineAutoAssembleNode(opts),
		NewMediaPeaksThumbsNode(opts),
		NewRenderTimelineNode(opts),
		NewExportNLEDraftNode(opts),
		NewVideoContactSheetNode(opts),
	}
}

func withStudioDefaults(cfg config.StudioConfig) config.StudioConfig {
	return config.ResolveStudioConfig(cfg)
}

func resolveTree(opts Options) paths.WorkspaceTree {
	if opts.WorkspaceTreeFunc != nil {
		tree := opts.WorkspaceTreeFunc()
		if tree.Root != "" || tree.WorkspaceDir != "" || tree.MediaDir != "" || tree.ProjectsDir != "" {
			return tree
		}
	}
	ws := strings.TrimSpace(opts.WorkspaceDir)
	md := strings.TrimSpace(opts.MediaDir)
	var projDir string
	if ws != "" {
		projDir = filepath.Join(ws, "projects")
	}
	return paths.WorkspaceTree{
		MediaDir:     md,
		WorkspaceDir: ws,
		ProjectsDir:  projDir,
	}
}

func resolveMediaDir(opts Options) string {
	return strings.TrimSpace(resolveTree(opts).MediaDir)
}

func resolveProjectsDir(opts Options) string {
	t := resolveTree(opts)
	if strings.TrimSpace(t.ProjectsDir) != "" {
		return strings.TrimSpace(t.ProjectsDir)
	}
	if strings.TrimSpace(t.WorkspaceDir) != "" {
		return filepath.Join(strings.TrimSpace(t.WorkspaceDir), "projects")
	}
	return strings.TrimSpace(t.MediaDir)
}

func resolveStagingDir(opts Options) string {
	if s := strings.TrimSpace(opts.StagingDir); s != "" {
		return s
	}
	if md := resolveMediaDir(opts); md != "" {
		return filepath.Join(md, "staging")
	}
	return ""
}

func checkStudioFile(ctx context.Context, target string, write bool) error {
	clean := filepath.Clean(strings.TrimSpace(target))
	if clean == "" || !filepath.IsAbs(clean) {
		return apperr.Validation("file path must resolve to an absolute workspace path").
			WithMsg("errors.studio.invalid_path", nil)
	}
	err := engine.CheckFile(ctx, clean, write)
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	var appErr *apperr.Error
	if errors.As(err, &appErr) {
		return appErr
	}
	return apperr.Wrap(apperr.CodeUnauthorized, "File access is restricted outside the active workspace", err).
		WithMsg("errors.studio.access_denied", nil)
}

func ioUnavailableErr(msg string, cause error) error {
	if cause == nil {
		return apperr.Unavailable(msg).WithMsg("errors.studio.io_failed", nil)
	}
	if errors.Is(cause, context.Canceled) || errors.Is(cause, context.DeadlineExceeded) {
		return cause
	}
	return apperr.Wrap(apperr.CodeUnavailable, msg, cause).WithMsg("errors.studio.io_failed", nil)
}

func resolveProjectDir(ctx context.Context, opts Options, rawDir string) (string, error) {
	trimmed := strings.TrimSpace(rawDir)
	if trimmed == "" {
		return "", nil
	}
	base := resolveProjectsDir(opts)
	cleaned := filepath.Clean(trimmed)
	var target string
	if filepath.IsAbs(cleaned) {
		target = cleaned
	} else {
		if base == "" || !filepath.IsAbs(base) {
			return "", apperr.Validation("relative project_dir requires a configured workspace projects directory").
				WithMsg("errors.studio.project_dir_required", nil)
		}
		resolved := engine.ResolveMediaPath(trimmed, base)
		if resolved == "" || !filepath.IsAbs(resolved) {
			return "", apperr.Validation("project_dir is invalid or escapes workspace projects directory").
				WithMsg("errors.studio.project_dir_escape", nil)
		}
		target = resolved
	}
	if err := checkStudioFile(ctx, target, true); err != nil {
		return "", err
	}
	return target, nil
}

func writeProjectArtifact(ctx context.Context, opts Options, projectDir, subDir, filename string, data []byte) (string, error) {
	dir, err := resolveProjectDir(ctx, opts, projectDir)
	if err != nil {
		return "", err
	}
	if dir == "" {
		return "", nil
	}
	var target string
	if strings.EqualFold(filepath.Base(dir), subDir) {
		target = filepath.Join(dir, filename)
	} else {
		target = filepath.Join(dir, subDir, filename)
	}
	if err := checkStudioFile(ctx, target, true); err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
		return "", ioUnavailableErr("Failed to prepare studio artifact directory", err)
	}
	if err := fsutil.WriteFileAtomic(target, data, 0o600); err != nil {
		return "", ioUnavailableErr("Failed to write studio artifact file", err)
	}
	return target, nil
}

func matchesInputKind(path string, allowed inputMediaKind) bool {
	if strings.TrimSpace(filepath.Ext(path)) == "" {
		return false
	}
	if allowed&inputKindImage != 0 && mediautil.IsSupportedImageExt(path) {
		return true
	}
	if allowed&inputKindVideo != 0 && mediautil.IsSupportedVideoExt(path) {
		return true
	}
	if allowed&inputKindAudio != 0 && mediautil.IsSupportedAudioSourceExt(path) {
		return true
	}
	if allowed&inputKindSubtitle != 0 && mediautil.IsSupportedSubtitleExt(path) {
		return true
	}
	return false
}

func resolveInputFile(ctx context.Context, opts Options, raw string) (string, error) {
	return resolveMediaInput(ctx, opts, raw, inputKindAnyMedia)
}

func resolveMediaInput(ctx context.Context, opts Options, raw string, allowed inputMediaKind) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", nil
	}
	t := resolveTree(opts)
	bases := []string{t.MediaDir, resolveProjectsDir(opts), t.WorkspaceDir}
	var lastGuardErr error
	var firstGuarded string
	for _, base := range bases {
		base = strings.TrimSpace(base)
		var candidate string
		if strings.HasPrefix(filepath.ToSlash(trimmed), "/media/") {
			if md := strings.TrimSpace(t.MediaDir); md != "" && filepath.IsAbs(md) {
				candidate = engine.ResolveMediaPath(trimmed, md)
			}
		} else if filepath.IsAbs(filepath.Clean(trimmed)) {
			candidate = filepath.Clean(trimmed)
		} else if base != "" && filepath.IsAbs(base) {
			candidate = engine.ResolveMediaPath(trimmed, base)
		}
		if candidate == "" || !filepath.IsAbs(candidate) {
			continue
		}
		if err := checkStudioFile(ctx, candidate, false); err != nil {
			if lastGuardErr == nil {
				lastGuardErr = err
			}
			continue
		}
		if !matchesInputKind(candidate, allowed) {
			return "", apperr.Validation("Input file type is not a supported media format").
				WithMsg("errors.studio.unsupported_media_ext", nil)
		}
		if info, err := os.Stat(candidate); err == nil {
			if info.IsDir() {
				return "", apperr.Validation("Input path must reference a regular file, not a directory").
					WithMsg("errors.studio.input_not_file", nil)
			}
			return candidate, nil
		}
		if firstGuarded == "" {
			firstGuarded = candidate
		}
	}
	if firstGuarded != "" {
		return firstGuarded, nil
	}
	if lastGuardErr != nil {
		return "", lastGuardErr
	}
	return "", apperr.Validation("Input file path is invalid or escapes workspace").
		WithMsg("errors.studio.invalid_path", nil)
}

func resolveBinary(ctx context.Context, resolver BinaryResolver, id domainbinaries.BinaryID) (string, error) {
	if resolver == nil {
		return "", apperr.Unavailable(fmt.Sprintf("%s binary resolver is unavailable", id)).
			WithMsg("errors.studio.binary_unavailable", map[string]any{"id": string(id)})
	}
	exe, _, _, err := resolver.Resolve(ctx, id)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(exe) == "" {
		return "", apperr.Unavailable(fmt.Sprintf("%s is not installed; install it in Settings -> External Tools", id)).
			WithMsg("errors.studio.binary_missing", map[string]any{"id": string(id)})
	}
	return exe, nil
}

func parseResolutionWxH(raw string, maxDim ...int) (int, int, error) {
	defaults := config.ResolveStudioConfig(config.StudioConfig{})
	limit := defaults.MaxVideoDimension
	if len(maxDim) > 0 && maxDim[0] >= minVideoDimension {
		limit = maxDim[0]
	}
	w, h, ok := domainmedia.ParseWxHWithMax(raw, limit)
	if !ok || w < minVideoDimension || h < minVideoDimension {
		return 0, 0, apperr.Validation(fmt.Sprintf("resolution must be WxH within %d..%d (e.g. %s)", minVideoDimension, limit, defaults.DefaultResolution)).
			WithMsg("errors.studio.invalid_resolution", map[string]any{"min": minVideoDimension, "max": limit})
	}
	w -= w % yuv420MacroblockAlign
	h -= h % yuv420MacroblockAlign
	return w, h, nil
}

func optionStrings[T ~string](vals []T) []string {
	out := make([]string, len(vals))
	for i, v := range vals {
		out[i] = string(v)
	}
	return out
}

func resolveNodeOutputDir(ctx context.Context, opts Options, rawOutDir, subDir string) (string, error) {
	t := resolveTree(opts)
	defaultBase := resolveProjectsDir(opts)
	if defaultBase == "" {
		defaultBase = strings.TrimSpace(t.MediaDir)
	}
	if p := strings.TrimSpace(rawOutDir); p != "" {
		var clean string
		if strings.HasPrefix(filepath.ToSlash(p), "/media/") {
			md := strings.TrimSpace(t.MediaDir)
			if md == "" || !filepath.IsAbs(md) {
				return "", apperr.Validation("media output_dir requires a configured media directory").
					WithMsg("errors.studio.output_dir_required", nil)
			}
			clean = engine.ResolveMediaPath(p, md)
		} else if filepath.IsAbs(filepath.Clean(p)) {
			clean = filepath.Clean(p)
		} else {
			var anchor string
			for _, candidate := range []string{t.WorkspaceDir, t.MediaDir, defaultBase} {
				if c := strings.TrimSpace(candidate); c != "" && filepath.IsAbs(c) {
					anchor = c
					break
				}
			}
			if anchor == "" {
				return "", apperr.Validation("relative output_dir requires a configured workspace or media directory").
					WithMsg("errors.studio.output_dir_required", nil)
			}
			clean = engine.ResolveMediaPath(p, anchor)
		}
		if clean == "" || !filepath.IsAbs(clean) {
			return "", apperr.Validation("output_dir escapes configured workspace directory").
				WithMsg("errors.studio.output_dir_escape", nil)
		}
		if err := checkStudioFile(ctx, filepath.Join(clean, ".write-check"), true); err != nil {
			return "", err
		}
		if err := os.MkdirAll(clean, 0o750); err != nil {
			return "", ioUnavailableErr("Failed to create studio output directory", err)
		}
		return clean, nil
	}
	if defaultBase == "" || !filepath.IsAbs(defaultBase) {
		return "", apperr.Validation("output_dir or configured workspace/media directory is required").
			WithMsg("errors.studio.output_dir_required", nil)
	}
	dir := filepath.Join(defaultBase, subDir)
	if err := checkStudioFile(ctx, filepath.Join(dir, ".write-check"), true); err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", ioUnavailableErr("Failed to create studio output directory", err)
	}
	return dir, nil
}

func createTempMediaFile(dir, prefix, ext string) (string, error) {
	f, err := os.CreateTemp(dir, prefix+"*"+ext)
	if err != nil {
		return "", ioUnavailableErr("Failed to allocate temporary media output file", err)
	}
	name := f.Name()
	if cerr := f.Close(); cerr != nil {
		_ = os.Remove(name)
		return "", ioUnavailableErr("Failed to close temporary media output file", cerr)
	}
	return name, nil
}

func commitTempMediaFile(tmpPath, finalPath string) error {
	if err := os.Rename(tmpPath, finalPath); err != nil {
		_ = os.Remove(tmpPath)
		return ioUnavailableErr("Failed to finalize rendered media file", err)
	}
	return nil
}

func deduplicateOutputPath(path string, overwrite bool) string {
	if overwrite {
		return path
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return path
	}
	dir := filepath.Dir(path)
	ext := filepath.Ext(path)
	stem := strings.TrimSuffix(filepath.Base(path), ext)
	for i := 1; i <= 10_000; i++ {
		candidate := filepath.Join(dir, fmt.Sprintf("%s_%d%s", stem, i, ext))
		if _, err := os.Stat(candidate); os.IsNotExist(err) {
			return candidate
		}
	}
	return path
}

func registerStudioAsset(ctx context.Context, opts Options, kind, sourceNode, filePath, rootDir string) string {
	if opts.Registrar == nil || strings.TrimSpace(filePath) == "" {
		return ""
	}
	item, err := opts.Registrar.RegisterAsset(ctx, media.RegisterAssetParams{
		Kind:           kind,
		SourceNode:     sourceNode,
		FilePath:       filePath,
		RegisteredRoot: rootDir,
	})
	if err != nil {
		if opts.Logger != nil {
			opts.Logger.WarnContext(ctx, "studio: failed to register generated media asset",
				"kind", kind,
				"source_node", sourceNode,
				"error", err,
			)
		}
		return ""
	}
	if item == nil {
		return ""
	}
	return item.ID
}
