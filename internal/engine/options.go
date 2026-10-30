package engine

import (
	"context"
	"fmt"
)

// Option is one choice of a select setting.
type Option struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// OptionSource lists the choices a Param's OptionsFrom names.
type OptionSource func(ctx context.Context, selected map[string]string) ([]Option, error)

// RegisterOptions adds option sources.
func (r *Registry) RegisterOptions(sources map[string]OptionSource) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for name, source := range sources {
		if name == "" || source == nil {
			return fmt.Errorf("engine: empty option source %q", name)
		}
		if _, exists := r.options[name]; exists {
			return fmt.Errorf("engine: option source %q registered twice", name)
		}
		r.options[name] = source
	}
	return nil
}

// Options lists the choices of an option source.
func (r *Registry) Options(ctx context.Context, source string, selected map[string]string) ([]Option, error) {
	r.mu.RLock()
	fn, ok := r.options[source]
	r.mu.RUnlock()
	if !ok {
		return nil, Invalid(fmt.Sprintf("Không có nguồn lựa chọn %q.", source))
	}
	if selected == nil {
		selected = map[string]string{}
	}
	return fn(ctx, selected)
}

// Check reports a setting whose OptionsFrom no provider resolves.
func (r *Registry) Check() error {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, n := range r.nodes {
		for _, p := range n.Spec().Params {
			if p.OptionsFrom == "" {
				continue
			}
			if _, ok := r.options[p.OptionsFrom]; !ok {
				return fmt.Errorf("engine: node %s setting %s reads options from unknown source %q", n.Spec().Type, p.Name, p.OptionsFrom)
			}
		}
	}
	return nil
}
