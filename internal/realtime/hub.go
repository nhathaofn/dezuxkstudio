package realtime

import (
	"context"
	"net/http"
	"sync"
)

// Hub coordinates connected clients, action registry, dispatcher, and broadcaster.
type Hub struct {
	mu          sync.RWMutex
	clients     map[*Client]bool
	registry    *ActionRegistry
	dispatcher  *Dispatcher
	broadcaster *Broadcaster
	upgrader    *Upgrader
}

// NewHub initializes the realtime hub with security and origins configured.
func NewHub(allowedOrigins []string, authToken string) *Hub {
	registry := NewActionRegistry()
	RegisterSystemActions(registry)

	h := &Hub{
		clients:    make(map[*Client]bool),
		registry:   registry,
		dispatcher: NewDispatcher(registry),
	}

	h.broadcaster = NewBroadcaster(h)
	h.upgrader = NewUpgrader(h, allowedOrigins, authToken)

	return h
}

// Register adds a new client.
func (h *Hub) Register(c *Client) {
	h.mu.Lock()
	h.clients[c] = true
	h.mu.Unlock()
}

// Unregister removes a client.
func (h *Hub) Unregister(c *Client) {
	h.mu.Lock()
	delete(h.clients, c)
	h.mu.Unlock()
}

// GetClients returns a snapshot of active clients.
func (h *Hub) GetClients() []*Client {
	h.mu.RLock()
	defer h.mu.RUnlock()

	result := make([]*Client, 0, len(h.clients))
	for c := range h.clients {
		result = append(result, c)
	}
	return result
}

// RegisterAction delegates action registration to the registry.
func (h *Hub) RegisterAction(action string, handler ActionHandler) {
	h.registry.Register(action, handler)
}

// SetFallbackHandler binds an external action execution delegate to the dispatcher.
func (h *Hub) SetFallbackHandler(fallback FallbackHandler) {
	h.dispatcher.SetFallback(fallback)
}

// Dispatch delegates execution of an action to the dispatcher.
func (h *Hub) Dispatch(ctx context.Context, client *Client, msg ActionMessage) {
	h.dispatcher.Execute(ctx, client, msg)
}

// Emit sends an event to a single client.
func (h *Hub) Emit(client *Client, event string, refID string, data interface{}) {
	h.broadcaster.Emit(client, event, refID, data)
}

// Broadcast sends an event to all connected clients.
func (h *Hub) Broadcast(event string, refID string, data interface{}) {
	h.broadcaster.Broadcast(event, refID, data)
}

// ServeHTTP implements http.Handler to upgrade connections.
func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.upgrader.ServeHTTP(w, r)
}
