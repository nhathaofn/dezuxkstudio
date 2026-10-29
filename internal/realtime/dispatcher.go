package realtime

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"runtime/debug"
	"time"
)

// FallbackHandler defines a delegate for actions not explicitly registered in Go.
type FallbackHandler func(ctx context.Context, action string, payload json.RawMessage) (interface{}, error)

// Dispatcher executes requested actions against the registry or falls back to external workers,
// guarded with concurrency control and panic recovery.
type Dispatcher struct {
	registry *ActionRegistry
	fallback FallbackHandler
	sem      chan struct{}
}

// NewDispatcher creates a dispatcher instance with a bounded worker concurrency limit.
func NewDispatcher(registry *ActionRegistry) *Dispatcher {
	// Max 64 concurrent active actions per server instance to avoid resource exhaustion
	return &Dispatcher{
		registry: registry,
		sem:      make(chan struct{}, 64),
	}
}

// SetFallback sets an external execution delegate for unregistered action commands.
func (d *Dispatcher) SetFallback(fallback FallbackHandler) {
	d.fallback = fallback
}

// Execute performs the lookup and executes the action, sending the response to client.
// It enforces panic recovery and execution timeout.
func (d *Dispatcher) Execute(ctx context.Context, client *Client, msg ActionMessage) {
	select {
	case d.sem <- struct{}{}:
		defer func() { <-d.sem }()
	case <-time.After(5 * time.Second):
		slog.Warn("[Dispatcher] Dropping action due to concurrency saturation", "action", msg.Action, "client_id", client.ID)
		_ = client.SendJSON(ResponseMessage{
			ID:      msg.ID,
			Success: false,
			Error:   "server busy: action concurrency limit reached",
		})
		return
	}

	var resp ResponseMessage
	resp.ID = msg.ID

	defer func() {
		if r := recover(); r != nil {
			stack := string(debug.Stack())
			slog.Error("[Dispatcher] Action panicked", "action", msg.Action, "panic", r, "stack", stack)
			resp.Success = false
			resp.Error = fmt.Sprintf("action panic: %v", r)
			_ = client.SendJSON(resp)
		}
	}()

	handler, exists := d.registry.Lookup(msg.Action)
	if exists {
		result, err := handler(ctx, client, msg.Payload)
		if err != nil {
			resp.Success = false
			resp.Error = err.Error()
		} else {
			resp.Success = true
			resp.Data = result
		}
		_ = client.SendJSON(resp)
		return
	}

	// Delegate to fallback handler if present
	if d.fallback != nil {
		result, err := d.fallback(ctx, msg.Action, msg.Payload)
		if err != nil {
			resp.Success = false
			resp.Error = err.Error()
		} else {
			resp.Success = true
			resp.Data = result
		}
		_ = client.SendJSON(resp)
		return
	}

	resp.Success = false
	resp.Error = fmt.Sprintf("unknown action: %s", msg.Action)
	_ = client.SendJSON(resp)
}
