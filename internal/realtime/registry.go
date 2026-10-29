package realtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"

	"app/internal/apperr"
)

// HandlerFunc executes an action with its raw JSON payload.
type HandlerFunc func(ctx context.Context, req *Request, payload json.RawMessage) (any, error)

// Module groups the handlers of one feature. Register must only call Handle.
type Module interface {
	Name() string
	Register(r *Registry)
}

// Registry maps declared action names to handlers.
type Registry struct {
	mu       sync.RWMutex
	declared map[string]bool
	handlers map[string]HandlerFunc
	errs     []error
}

// NewRegistry creates a registry that only accepts the declared action names.
func NewRegistry(declared []string) *Registry {
	d := make(map[string]bool, len(declared))
	for _, n := range declared {
		d[n] = true
	}
	return &Registry{declared: d, handlers: map[string]HandlerFunc{}}
}

func (r *Registry) add(name string, h HandlerFunc) {
	r.mu.Lock()
	defer r.mu.Unlock()
	switch {
	case !r.declared[name]:
		r.errs = append(r.errs, fmt.Errorf("action %q is not declared in contracts", name))
	case r.handlers[name] != nil:
		r.errs = append(r.errs, fmt.Errorf("action %q registered twice", name))
	default:
		r.handlers[name] = h
	}
}

// Lookup returns the handler for name.
func (r *Registry) Lookup(name string) (HandlerFunc, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	h, ok := r.handlers[name]
	return h, ok
}

// Names returns registered action names, sorted.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.handlers))
	for n := range r.handlers {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Missing returns declared actions without a handler, sorted.
func (r *Registry) Missing() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []string
	for n := range r.declared {
		if r.handlers[n] == nil {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}

func (r *Registry) takeErrors() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	err := errors.Join(r.errs...)
	r.errs = nil
	return err
}

// Validator is implemented by payloads that check their own invariants.
type Validator interface {
	Validate() error
}

// Handle registers a typed handler. The payload is decoded strictly (unknown fields
// are rejected) and validated before fn runs.
func Handle[P, R any](r *Registry, name string, fn func(ctx context.Context, req *Request, p P) (R, error)) {
	r.add(name, func(ctx context.Context, req *Request, raw json.RawMessage) (any, error) {
		var p P
		if err := decodePayload(raw, &p); err != nil {
			return nil, apperr.Wrap(apperr.CodeValidation, "invalid payload: "+err.Error(), err)
		}
		if v, ok := any(p).(Validator); ok {
			if err := v.Validate(); err != nil {
				var ae *apperr.Error
				if errors.As(err, &ae) {
					return nil, err
				}
				return nil, apperr.Wrap(apperr.CodeValidation, err.Error(), err)
			}
		}
		return fn(ctx, req, p)
	})
}

func decodePayload(raw json.RawMessage, dst any) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil
	}
	if rm, ok := dst.(*json.RawMessage); ok {
		*rm = append(json.RawMessage(nil), trimmed...)
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	if dec.More() {
		return errors.New("trailing data after payload")
	}
	return nil
}
