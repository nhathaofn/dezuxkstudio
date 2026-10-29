// Package server runs the HTTP listener that hosts the realtime hub and auxiliary endpoints.
package server

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"

	"app/internal/safego"
)

// Options configures the listener.
type Options struct {
	Host              string
	Port              int // 0 = OS-assigned
	ReadHeaderTimeout time.Duration
	IdleTimeout       time.Duration
	Handler           http.Handler
	Logger            *slog.Logger
}

// Server is a started HTTP server bound to a concrete address.
type Server struct {
	o        Options
	http     *http.Server
	listener net.Listener
	errs     chan error
	running  atomic.Bool
}

// New validates options.
func New(o Options) (*Server, error) {
	if o.Host == "" || o.Handler == nil || o.Logger == nil || o.ReadHeaderTimeout <= 0 || o.IdleTimeout <= 0 {
		return nil, errors.New("server: incomplete options")
	}
	return &Server{o: o, errs: make(chan error, 1)}, nil
}

// Start binds the listener and serves in the background. When a fixed port is busy
// it fails rather than silently moving to another port.
func (s *Server) Start() error {
	addr := net.JoinHostPort(s.o.Host, strconv.Itoa(s.o.Port))
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("server: listen on %s: %w", addr, err)
	}
	s.listener = ln
	s.http = &http.Server{
		Handler:           s.o.Handler,
		ReadHeaderTimeout: s.o.ReadHeaderTimeout,
		IdleTimeout:       s.o.IdleTimeout,
		ErrorLog:          slog.NewLogLogger(s.o.Logger.Handler(), slog.LevelWarn),
	}
	s.running.Store(true)
	safego.Go("server.serve", func() {
		err := s.http.Serve(ln)
		s.running.Store(false)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.o.Logger.Error("server: serve failed", "error", err)
			s.errs <- err
		}
		close(s.errs)
	})
	s.o.Logger.Info("server: listening", "addr", ln.Addr().String())
	return nil
}

// Errors delivers a fatal serve error, then closes.
func (s *Server) Errors() <-chan error { return s.errs }

// Port returns the bound port.
func (s *Server) Port() int {
	if s.listener == nil {
		return 0
	}
	return s.listener.Addr().(*net.TCPAddr).Port
}

// WSURL returns the WebSocket endpoint reachable from this machine.
func (s *Server) WSURL() string {
	host := s.o.Host
	if ip := net.ParseIP(host); ip != nil && ip.IsUnspecified() {
		host = "127.0.0.1"
	}
	return fmt.Sprintf("ws://%s%s", net.JoinHostPort(host, strconv.Itoa(s.Port())), PathWS)
}

// Stop closes the listener and every remaining HTTP connection immediately.
//
// Graceful draining happens one level up: WebSocket sessions are hijacked
// connections owned by the hub, which must be closed (and drained) before Stop.
// The remaining HTTP endpoints are tiny and idempotent, and http.Server.Shutdown
// would otherwise wait up to 5s for idle-but-unused client sockets (StateNew),
// delaying application exit for no benefit.
func (s *Server) Stop() error {
	if s.http == nil {
		return nil
	}
	return s.http.Close()
}
