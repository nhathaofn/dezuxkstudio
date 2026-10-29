package realtime

import (
	"context"
	"encoding/json"
	"sync"
)

// ActionHandler is the function signature for executing a client action.
type ActionHandler func(ctx context.Context, client *Client, payload json.RawMessage) (interface{}, error)

// ActionRegistry maintains thread-safe mapping between action names and handlers.
type ActionRegistry struct {
	mu       sync.RWMutex
	handlers map[string]ActionHandler
}

// NewActionRegistry creates an empty registry.
func NewActionRegistry() *ActionRegistry {
	return &ActionRegistry{
		handlers: make(map[string]ActionHandler),
	}
}

// Register adds or updates an action handler.
func (r *ActionRegistry) Register(action string, handler ActionHandler) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.handlers[action] = handler
}

// Lookup retrieves the handler for a given action name.
func (r *ActionRegistry) Lookup(action string) (ActionHandler, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	h, exists := r.handlers[action]
	return h, exists
}
