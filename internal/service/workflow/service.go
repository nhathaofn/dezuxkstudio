// Package workflow coordinates DezuxkStudio's node registry and DAG execution pipeline.
package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"app/internal/config"
	"app/internal/contracts"
	"app/internal/engine"
	"app/internal/paths"
	mediagennodes "app/internal/tools/mediagen"
	providernodes "app/internal/tools/provider"
	studionodes "app/internal/tools/studio"
	"app/internal/tools/watermark"
)

// Options configures the workflow service.
type Options struct {
	Paths  paths.Paths
	Logger *slog.Logger
	// Providers backs the API-provider nodes (nil leaves them out, e.g. in tests).
	Providers providernodes.Service
	// MediaGen backs the unified image/video/TTS generation nodes (nil leaves them out in minimal tests).
	MediaGen mediagennodes.MediaService
	// TextGen backs the LLM text/screenplay studio nodes (optional; falls back to MediaGen when it implements TextGenerator).
	TextGen studionodes.TextGenerator
	// Binaries resolves managed external tools such as ffmpeg/ffprobe for media inspection & concat nodes.
	Binaries studionodes.BinaryResolver
	// ProviderChecker & CostEstimator back tool.batch_preflight (optional; nil omits provider/pricing checks in minimal tests).
	ProviderChecker studionodes.ProviderChecker
	CostEstimator   studionodes.CostEstimator
	// StudioConfig supplies default FPS, resolution, presets, and timeouts for studio nodes.
	StudioConfig config.StudioConfig
	// Config provides full runtime settings (including WatermarkConfig bounds) to workflow nodes.
	Config *config.Config
	// Registrar auto-registers newly created or cleaned media assets into the library.
	Registrar watermark.MediaRegistrar
	// WorkspaceTreeFunc provides dynamic workspace paths.
	WorkspaceTreeFunc func() paths.WorkspaceTree
}

// Service manages DezuxkStudio's workflow runtime and registered nodes.
type Service struct {
	registry  *engine.Registry
	templates []contracts.WorkflowTemplate
	paths     paths.Paths
	log       *slog.Logger
}

// New creates and initializes the workflow service with built-in nodes.
func New(o Options) (*Service, error) {
	if o.Logger == nil {
		return nil, errors.New("workflow: logger is required")
	}

	reg := engine.NewRegistry()

	var mediaDirFunc func() string
	var watermarkDirFunc func() string
	if o.WorkspaceTreeFunc != nil {
		mediaDirFunc = func() string {
			return o.WorkspaceTreeFunc().MediaDir
		}
		watermarkDirFunc = func() string {
			return o.WorkspaceTreeFunc().WatermarkDir
		}
	}

	defaultMedia := ""
	defaultWorkspace := ""
	if o.Paths.Data != "" {
		defaultMedia = o.Paths.Media()
		defaultWorkspace = paths.ResolveWorkspaceTree(o.Paths, "", "").WorkspaceDir
	}

	textGen := o.TextGen
	if textGen == nil && o.MediaGen != nil {
		if tg, ok := o.MediaGen.(studionodes.TextGenerator); ok {
			textGen = tg
		}
	}

	var imgGen studionodes.ImageGenerator
	var ttsGen studionodes.TTSGenerator
	var lipSyncGen studionodes.LipSyncGenerator
	if o.MediaGen != nil {
		if ig, ok := o.MediaGen.(studionodes.ImageGenerator); ok {
			imgGen = ig
		}
		if ag, ok := o.MediaGen.(studionodes.TTSGenerator); ok {
			ttsGen = ag
		}
		if lg, ok := o.MediaGen.(studionodes.LipSyncGenerator); ok {
			lipSyncGen = lg
		}
	}

	// Built-in feature nodes. A new feature = one entry here: the UI, workflows and the agent
	// (MCP workflow_* tools) all pick it up from its Spec, with no agent-specific code.
	studioCfg := config.ResolveStudioConfig(o.StudioConfig)
	if o.Config != nil {
		studioCfg = o.Config.StudioOrDefault()
	}
	watermarkCfg := o.Config.WatermarkOrDefault()

	wmOpts := watermark.Options{
		MediaDir:          defaultMedia,
		MediaDirFunc:      mediaDirFunc,
		OutputDirFunc:     watermarkDirFunc,
		WorkspaceTreeFunc: o.WorkspaceTreeFunc,
		Registrar:         o.Registrar,
		Binaries:          o.Binaries,
		StudioConfig:      studioCfg,
		WatermarkConfig:   watermarkCfg,
		Config:            o.Config,
		Logger:            o.Logger,
	}
	nodes := []engine.Node{
		watermark.NewNode(wmOpts),
		watermark.NewVideoRemoveNode(wmOpts),
	}
	nodes = append(nodes, studionodes.NewNodes(studionodes.Options{
		TextGen:           textGen,
		ImageGen:          imgGen,
		TTSGen:            ttsGen,
		LipSyncGen:        lipSyncGen,
		ProviderChecker:   o.ProviderChecker,
		CostEstimator:     o.CostEstimator,
		Binaries:          o.Binaries,
		Registrar:         o.Registrar,
		StudioConfig:      studioCfg,
		MediaDir:          defaultMedia,
		WorkspaceDir:      defaultWorkspace,
		WorkspaceTreeFunc: o.WorkspaceTreeFunc,
		Logger:            o.Logger,
	})...)
	if o.MediaGen != nil {
		nodes = append(nodes, mediagennodes.NewNodes(mediagennodes.Options{
			MediaDir:                defaultMedia,
			MediaDirFunc:            mediaDirFunc,
			Service:                 o.MediaGen,
			MaxVideoDurationSec:     studioCfg.MaxShotDurationSec,
			DefaultVideoDurationSec: studioCfg.DefaultShotDurationSec,
		})...)
	}
	if o.Providers != nil {
		nodes = append(nodes, providernodes.Nodes(o.Providers)...)
		if err := reg.RegisterOptions(providernodes.OptionSources(o.Providers)); err != nil {
			return nil, fmt.Errorf("workflow: register option sources: %w", err)
		}
	}
	for _, n := range nodes {
		if err := engine.ValidateSpec(n.Spec()); err != nil {
			return nil, fmt.Errorf("workflow: incomplete node spec: %w", err)
		}
	}
	if err := reg.Register(nodes...); err != nil {
		return nil, fmt.Errorf("workflow: register nodes: %w", err)
	}
	if err := reg.Check(); err != nil {
		return nil, fmt.Errorf("workflow: %w", err)
	}
	templates, err := buildStudioTemplates(reg)
	if err != nil {
		return nil, fmt.Errorf("workflow: %w", err)
	}

	return &Service{
		registry:  reg,
		templates: templates,
		paths:     o.Paths,
		log:       o.Logger,
	}, nil
}

// Registry returns the underlying engine registry.
func (s *Service) Registry() *engine.Registry {
	return s.registry
}

// Specs returns metadata of all registered nodes.
func (s *Service) Specs(ctx context.Context) []engine.Spec {
	return s.registry.Specs()
}

// RunNode executes a single node by type with raw JSON input.
func (s *Service) RunNode(ctx context.Context, nodeType string, input json.RawMessage) (any, error) {
	s.log.DebugContext(ctx, "workflow: running single node", "type", nodeType)
	return s.registry.Run(ctx, nodeType, input)
}

// RunGraph executes a complete workflow graph.
func (s *Service) RunGraph(ctx context.Context, g engine.Graph) (engine.GraphResult, error) {
	s.log.InfoContext(ctx, "workflow: running graph", "nodes_count", len(g.Nodes), "edges_count", len(g.Edges))
	return s.registry.RunGraph(ctx, g)
}
