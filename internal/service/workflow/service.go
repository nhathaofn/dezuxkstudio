// Package workflow coordinates DezuxkStudio's node registry and DAG execution pipeline.
package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"app/internal/engine"
	"app/internal/paths"
	"app/internal/tools/watermark"
)

// Options configures the workflow service.
type Options struct {
	Paths  paths.Paths
	Logger *slog.Logger
}

// Service manages DezuxkStudio's workflow runtime and registered nodes.
type Service struct {
	registry *engine.Registry
	paths    paths.Paths
	log      *slog.Logger
}

// New creates and initializes the workflow service with built-in nodes.
func New(o Options) (*Service, error) {
	if o.Logger == nil {
		return nil, errors.New("workflow: logger is required")
	}

	reg := engine.NewRegistry()

	// Built-in feature nodes. A new feature = one entry here: the UI, workflows and the agent
	// (MCP workflow_* tools) all pick it up from its Spec, with no agent-specific code.
	nodes := []engine.Node{
		watermark.NewNode(watermark.Options{MediaDir: o.Paths.Media()}),
	}
	for _, n := range nodes {
		if err := engine.ValidateSpec(n.Spec()); err != nil {
			return nil, fmt.Errorf("workflow: incomplete node spec: %w", err)
		}
	}
	if err := reg.Register(nodes...); err != nil {
		return nil, fmt.Errorf("workflow: register nodes: %w", err)
	}

	return &Service{
		registry: reg,
		paths:    o.Paths,
		log:      o.Logger,
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
	s.log.Debug("workflow: running single node", "type", nodeType)
	return s.registry.Run(ctx, nodeType, input)
}

// RunGraph executes a complete workflow graph.
func (s *Service) RunGraph(ctx context.Context, g engine.Graph) (engine.GraphResult, error) {
	s.log.Info("workflow: running graph", "nodes_count", len(g.Nodes), "edges_count", len(g.Edges))
	return s.registry.RunGraph(ctx, g)
}
