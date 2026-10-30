package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
)

// Registry holds the node types every provider and tool contributes.
type Registry struct {
	mu      sync.RWMutex
	nodes   map[string]Node
	options map[string]OptionSource
}

func NewRegistry() *Registry {
	return &Registry{
		nodes:   make(map[string]Node),
		options: make(map[string]OptionSource),
	}
}

// Register adds nodes; a type registered twice is a wiring error.
func (r *Registry) Register(nodes ...Node) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, n := range nodes {
		t := n.Spec().Type
		if t == "" {
			return fmt.Errorf("engine: node without a type")
		}
		if _, exists := r.nodes[t]; exists {
			return fmt.Errorf("engine: node type %q registered twice", t)
		}
		r.nodes[t] = n
	}
	return nil
}

// Specs lists the registered node types, sorted by type.
func (r *Registry) Specs() []Spec {
	r.mu.RLock()
	defer r.mu.RUnlock()
	specs := make([]Spec, 0, len(r.nodes))
	for _, n := range r.nodes {
		specs = append(specs, n.Spec())
	}
	sort.Slice(specs, func(i, j int) bool { return specs[i].Type < specs[j].Type })
	return specs
}

// Run executes one node with its JSON request.
func (r *Registry) Run(ctx context.Context, nodeType string, input json.RawMessage) (any, error) {
	n, ok := r.node(nodeType)
	if !ok {
		return nil, Invalid(fmt.Sprintf("Không có node %q.", nodeType))
	}
	return n.Run(ctx, input)
}

func (r *Registry) node(nodeType string) (Node, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	n, ok := r.nodes[nodeType]
	return n, ok
}
