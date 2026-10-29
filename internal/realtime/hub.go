// Package realtime implements the WebSocket action/event hub: authenticated sessions,
// typed action dispatch with bounded concurrency, per-action timeout, client-driven
// cancellation, progress events and server-initiated broadcasts.
package realtime

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"app/internal/apperr"
	"app/internal/contracts"
	"app/internal/security"
)

// Options configures a Hub. Every field is required unless noted.
type Options struct {
	AuthToken            string // required unless AllowAnonymous
	AllowAnonymous       bool   // only for loopback-only servers
	AllowedOrigins       []string
	MaxConcurrentActions int
	ActionTimeout        time.Duration
	QueueTimeout         time.Duration
	ClientBufferSize     int
	MaxMessageBytes      int64
	PingInterval         time.Duration
	WriteTimeout         time.Duration
	DeclaredActions      []string
	DeclaredEvents       []string
	Logger               *slog.Logger
}

func (o Options) validate() error {
	var errs []error
	if o.AuthToken == "" && !o.AllowAnonymous {
		errs = append(errs, errors.New("auth token is required"))
	}
	if o.MaxConcurrentActions <= 0 || o.ClientBufferSize <= 0 || o.MaxMessageBytes <= 0 {
		errs = append(errs, errors.New("limits must be > 0"))
	}
	if o.ActionTimeout <= 0 || o.QueueTimeout <= 0 || o.PingInterval <= 0 || o.WriteTimeout <= 0 {
		errs = append(errs, errors.New("timeouts must be > 0"))
	}
	if o.Logger == nil {
		errs = append(errs, errors.New("logger is required"))
	}
	return errors.Join(errs...)
}

// Hub owns every session and the action registry.
type Hub struct {
	opts     Options
	log      *slog.Logger
	registry *Registry
	events   map[string]bool
	sem      chan struct{}

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu       sync.RWMutex
	sessions map[*session]struct{}
	closed   bool
}

// New validates options and creates a hub.
func New(o Options) (*Hub, error) {
	if err := o.validate(); err != nil {
		return nil, fmt.Errorf("realtime: %w", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	events := make(map[string]bool, len(o.DeclaredEvents))
	for _, e := range o.DeclaredEvents {
		events[e] = true
	}
	return &Hub{
		opts:     o,
		log:      o.Logger,
		registry: NewRegistry(o.DeclaredActions),
		events:   events,
		sem:      make(chan struct{}, o.MaxConcurrentActions),
		ctx:      ctx,
		cancel:   cancel,
		sessions: map[*session]struct{}{},
	}, nil
}

// Mount registers feature modules; any undeclared or duplicate action fails.
func (h *Hub) Mount(modules ...Module) error {
	for _, m := range modules {
		m.Register(h.registry)
		if err := h.registry.takeErrors(); err != nil {
			return fmt.Errorf("realtime: module %s: %w", m.Name(), err)
		}
	}
	return nil
}

// VerifyComplete fails if a declared action has no handler.
func (h *Hub) VerifyComplete() error {
	if missing := h.registry.Missing(); len(missing) > 0 {
		return fmt.Errorf("realtime: declared actions without handler: %s", strings.Join(missing, ", "))
	}
	return nil
}

// Actions returns the registered action names.
func (h *Hub) Actions() []string { return h.registry.Names() }

// Publish broadcasts a declared event to every session (implements events.Publisher).
func (h *Hub) Publish(event string, data any) {
	if !h.events[event] {
		h.log.Error("realtime: publish of undeclared event dropped", "event", event)
		return
	}
	for _, s := range h.snapshot() {
		s.emit(event, "", data)
	}
}

// ClientCount returns the number of connected sessions.
func (h *Hub) ClientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.sessions)
}

func (h *Hub) snapshot() []*session {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]*session, 0, len(h.sessions))
	for s := range h.sessions {
		out = append(out, s)
	}
	return out
}

// ServeHTTP authenticates and upgrades a WebSocket connection.
func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !h.authorized(r) {
		h.log.Warn("realtime: unauthorized connection rejected", "remote_addr", r.RemoteAddr)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		Subprotocols:   []string{contracts.ProtocolVersion},
		OriginPatterns: h.opts.AllowedOrigins,
	})
	if err != nil {
		h.log.Warn("realtime: upgrade failed", "remote_addr", r.RemoteAddr, "error", err)
		return
	}
	if conn.Subprotocol() != contracts.ProtocolVersion {
		_ = conn.Close(websocket.StatusPolicyViolation, "unsupported protocol version")
		return
	}

	s := newSession(newID(), h, conn)
	if !h.register(s) {
		_ = conn.Close(websocket.StatusGoingAway, "server shutting down")
		return
	}
	defer h.unregister(s)

	h.log.Debug("realtime: client connected", "client_id", s.id)
	s.run()
	h.log.Debug("realtime: client disconnected", "client_id", s.id)
}

func (h *Hub) authorized(r *http.Request) bool {
	if h.opts.AuthToken == "" {
		return h.opts.AllowAnonymous
	}
	token := security.SubprotocolToken(r, contracts.AuthSubprotocolPrefix)
	if token == "" {
		token = security.BearerToken(r)
	}
	return security.TokenEqual(h.opts.AuthToken, token)
}

func (h *Hub) register(s *session) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return false
	}
	h.sessions[s] = struct{}{}
	h.wg.Add(1)
	return true
}

func (h *Hub) unregister(s *session) {
	h.mu.Lock()
	delete(h.sessions, s)
	h.mu.Unlock()
	h.wg.Done()
}

func (h *Hub) acquire(ctx context.Context) bool {
	timer := time.NewTimer(h.opts.QueueTimeout)
	defer timer.Stop()
	select {
	case h.sem <- struct{}{}:
		return true
	case <-timer.C:
		return false
	case <-ctx.Done():
		return false
	}
}

func (h *Hub) release() { <-h.sem }

// execute runs a handler with panic recovery and maps the outcome to a user-safe error.
func (h *Hub) execute(ctx context.Context, req *Request, handler HandlerFunc, payload json.RawMessage) (result any, err error) {
	start := time.Now()
	defer func() {
		if r := recover(); r != nil {
			h.log.Error("realtime: action panicked", "action", req.Action, "request_id", req.ID,
				"panic", r, "stack", string(debug.Stack()))
			result, err = nil, apperr.Internal(fmt.Errorf("panic: %v", r))
		}
		if err != nil {
			err = h.classify(ctx, req, err)
		}
		h.log.Debug("realtime: action finished", "action", req.Action, "request_id", req.ID,
			"duration", time.Since(start), "error", err)
	}()
	return handler(ctx, req, payload)
}

func (h *Hub) classify(ctx context.Context, req *Request, err error) error {
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return apperr.Wrap(apperr.CodeTimeout, fmt.Sprintf("action timed out after %s", h.opts.ActionTimeout), err)
	case errors.Is(ctx.Err(), context.Canceled):
		return apperr.Wrap(apperr.CodeCancelled, "action cancelled", err)
	}
	ae := apperr.From(err)
	if ae.Code == apperr.CodeInternal {
		h.log.Error("realtime: action failed", "action", req.Action, "request_id", req.ID, "error", err)
	}
	return ae
}

// Close disconnects every session, cancels in-flight actions and waits until all
// sessions and action goroutines have finished, or until ctx expires.
func (h *Hub) Close(ctx context.Context) error {
	h.mu.Lock()
	h.closed = true
	h.mu.Unlock()
	h.cancel()
	for _, s := range h.snapshot() {
		s.close(websocket.StatusGoingAway, "server shutting down")
	}

	done := make(chan struct{})
	go func() { h.wg.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("realtime: close: %w", ctx.Err())
	}
}

func newID() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("c%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}
