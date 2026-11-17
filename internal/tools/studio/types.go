// Package studio implements DezuxkStudio's AI film production workflow nodes
// shared identically between the /studio UI and the built-in AI agent.
package studio

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
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
	WorkspaceTreeFunc func() paths.WorkspaceTree
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
	}
}

func withStudioDefaults(cfg config.StudioConfig) config.StudioConfig {
	def := config.Default(config.ModeDesktop).Studio
	presetWasEmpty := strings.TrimSpace(cfg.FFmpegPreset) == ""
	if cfg.DefaultFPS <= 0 {
		cfg.DefaultFPS = def.DefaultFPS
	}
	if strings.TrimSpace(cfg.DefaultResolution) == "" {
		cfg.DefaultResolution = def.DefaultResolution
	}
	if presetWasEmpty {
		cfg.FFmpegPreset = def.FFmpegPreset
	}
	if cfg.FFmpegCRF < 0 || cfg.FFmpegCRF > 51 || (cfg.FFmpegCRF == 0 && presetWasEmpty) {
		cfg.FFmpegCRF = def.FFmpegCRF
	}
	if cfg.FFmpegTimeout <= 0 {
		cfg.FFmpegTimeout = def.FFmpegTimeout
	}
	if cfg.FFprobeTimeout <= 0 {
		cfg.FFprobeTimeout = def.FFprobeTimeout
	}
	if cfg.MaxConcurrentBatch <= 0 {
		cfg.MaxConcurrentBatch = def.MaxConcurrentBatch
	}
	if cfg.ExportMaxZipBytes <= 0 {
		cfg.ExportMaxZipBytes = def.ExportMaxZipBytes
	}
	if cfg.ExportLoudnormLUFS == 0 {
		cfg.ExportLoudnormLUFS = def.ExportLoudnormLUFS
	}
	if cfg.ExportSubtitleFontSize <= 0 {
		cfg.ExportSubtitleFontSize = def.ExportSubtitleFontSize
	}
	if cfg.TimelineThumbIntervalSec <= 0 {
		cfg.TimelineThumbIntervalSec = def.TimelineThumbIntervalSec
	}
	if cfg.TimelinePeakBins <= 0 {
		cfg.TimelinePeakBins = def.TimelinePeakBins
	}
	return cfg
}

func resolveTree(opts Options) paths.WorkspaceTree {
	if opts.WorkspaceTreeFunc != nil {
		tree := opts.WorkspaceTreeFunc()
		if tree.Root != "" || tree.MediaDir != "" || tree.ProjectsDir != "" {
			return tree
		}
	}
	return paths.WorkspaceTree{
		MediaDir:     opts.MediaDir,
		WorkspaceDir: opts.WorkspaceDir,
		ProjectsDir:  filepath.Join(opts.WorkspaceDir, "projects"),
	}
}

func resolveMediaDir(opts Options) string {
	return resolveTree(opts).MediaDir
}

func resolveProjectsDir(opts Options) string {
	t := resolveTree(opts)
	if strings.TrimSpace(t.ProjectsDir) != "" {
		return t.ProjectsDir
	}
	if strings.TrimSpace(t.WorkspaceDir) != "" {
		return filepath.Join(t.WorkspaceDir, "projects")
	}
	return t.MediaDir
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
		resolved := engine.ResolveMediaPath(trimmed, base)
		if resolved == "" {
			return "", apperr.Validation("project_dir is invalid or escapes workspace projects directory")
		}
		target = resolved
	}
	if err := engine.CheckFile(ctx, target, true); err != nil {
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
	if err := engine.CheckFile(ctx, target, true); err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return "", apperr.Internal(err)
	}
	if err := fsutil.WriteFileAtomic(target, data, 0o644); err != nil {
		return "", apperr.Internal(err)
	}
	return target, nil
}

func resolveInputFile(ctx context.Context, opts Options, raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", nil
	}
	bases := []string{resolveMediaDir(opts), resolveProjectsDir(opts), resolveTree(opts).WorkspaceDir}
	var lastGuardErr error
	var firstGuarded string
	for _, base := range bases {
		base = strings.TrimSpace(base)
		if base == "" && !filepath.IsAbs(trimmed) {
			continue
		}
		candidate := engine.ResolveMediaPath(trimmed, base)
		if candidate == "" {
			continue
		}
		if err := engine.CheckFile(ctx, candidate, false); err != nil {
			if lastGuardErr == nil {
				lastGuardErr = err
			}
			continue
		}
		if firstGuarded == "" {
			firstGuarded = candidate
		}
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}
	if firstGuarded != "" {
		return firstGuarded, nil
	}
	if lastGuardErr != nil {
		return "", lastGuardErr
	}
	return "", apperr.Validation("Input file path is invalid or escapes workspace")
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

func parseResolutionWxH(raw string) (int, int, error) {
	s := strings.ToLower(strings.TrimSpace(raw))
	parts := strings.Split(s, "x")
	if len(parts) != 2 {
		return 0, 0, apperr.Validation(fmt.Sprintf("resolution must be WxH (e.g. 1920x1080), got %q", raw))
	}
	w, err1 := strconv.Atoi(strings.TrimSpace(parts[0]))
	h, err2 := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err1 != nil || err2 != nil || w < 64 || h < 64 || w > 16384 || h > 16384 {
		return 0, 0, apperr.Validation(fmt.Sprintf("resolution out of bounds (64..16384): %q", raw))
	}
	// Ensure even dimensions for yuv420p H.264 encoding
	if w%2 != 0 {
		w--
	}
	if h%2 != 0 {
		h--
	}
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
	base := resolveProjectsDir(opts)
	if base == "" {
		base = resolveMediaDir(opts)
	}
	if p := strings.TrimSpace(rawOutDir); p != "" {
		clean := filepath.Clean(p)
		if !filepath.IsAbs(clean) {
			anchor := resolveMediaDir(opts)
			if anchor == "" {
				anchor = base
			}
			if anchor == "" {
				return "", apperr.Validation("relative output_dir requires a configured workspace or media directory")
			}
			clean = engine.ResolveMediaPath(p, anchor)
			if clean == "" {
				return "", apperr.Validation("output_dir escapes configured workspace directory")
			}
		}
		if err := engine.CheckFile(ctx, filepath.Join(clean, ".write-check"), true); err != nil {
			return "", err
		}
		if err := os.MkdirAll(clean, 0o750); err != nil {
			return "", apperr.Internal(err)
		}
		return clean, nil
	}
	if base == "" {
		return "", apperr.Validation("output_dir or configured workspace/media directory is required")
	}
	dir := filepath.Join(base, subDir)
	if err := engine.CheckFile(ctx, filepath.Join(dir, ".write-check"), true); err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", apperr.Internal(err)
	}
	return dir, nil
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
	if err != nil || item == nil {
		return ""
	}
	return item.ID
}
