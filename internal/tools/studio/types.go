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
)

// TextGenerator executes structured LLM calls (satisfied by *service/mediagen.Service).
type TextGenerator interface {
	GenerateText(ctx context.Context, req domainmedia.TextRequest) (domainmedia.TextResult, error)
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
	Binaries          BinaryResolver
	Registrar         MediaRegistrar
	StudioConfig      config.StudioConfig
	MediaDir          string
	WorkspaceDir      string
	WorkspaceTreeFunc func() paths.WorkspaceTree
}

// NewNodes returns all Phase 1 film studio engine nodes.
func NewNodes(opts Options) []engine.Node {
	opts.StudioConfig = withStudioDefaults(opts.StudioConfig)
	return []engine.Node{
		NewScriptNormalizeNode(opts),
		NewExtractAssetsNode(opts),
		NewAuthorShotPromptsNode(opts),
		NewProbeMediaNode(opts),
		NewVideoConcatBasicNode(opts),
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
	mediaDir := resolveMediaDir(opts)
	resolved := engine.ResolveMediaPath(trimmed, mediaDir)
	if resolved == "" {
		return "", apperr.Validation("Input file path is invalid or escapes workspace")
	}
	if _, err := os.Stat(resolved); err != nil {
		for _, altBase := range []string{resolveProjectsDir(opts), resolveTree(opts).WorkspaceDir} {
			altBase = strings.TrimSpace(altBase)
			if altBase == "" {
				continue
			}
			if candidate := engine.ResolveMediaPath(trimmed, altBase); candidate != "" {
				if _, cerr := os.Stat(candidate); cerr == nil {
					resolved = candidate
					break
				}
			}
		}
	}
	if err := engine.CheckFile(ctx, resolved, false); err != nil {
		return "", err
	}
	return resolved, nil
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
