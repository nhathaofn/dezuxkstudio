package server

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"app/internal/config"
	"app/internal/realtime"
)

// Server coordinates network listener and HTTP/WebSocket services.
type Server struct {
	cfg        *config.Config
	hub        *realtime.Hub
	httpServer *http.Server
	listener   net.Listener
	actualPort int
	startTime  time.Time
}

// NewServer constructs the core server with dynamic port support.
func NewServer(cfg *config.Config, hub *realtime.Hub) *Server {
	return &Server{
		cfg:       cfg,
		hub:       hub,
		startTime: time.Now(),
	}
}

// Start opens the network listener and serves HTTP & WebSocket traffic.
func (s *Server) Start(ctx context.Context) error {
	host := s.cfg.ServerHost
	if host == "" {
		host = "127.0.0.1"
	}

	addr := fmt.Sprintf("%s:%d", host, s.cfg.ServerPort)
	listener, err := net.Listen("tcp", addr)
	if err != nil && s.cfg.ServerPort != 0 {
		slog.Warn("[Server] Preferred port busy, falling back to dynamic port", "preferred_port", s.cfg.ServerPort, "error", err)
		addr = fmt.Sprintf("%s:0", host)
		listener, err = net.Listen("tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("failed to bind server to %s: %w", addr, err)
	}

	s.listener = listener
	s.actualPort = listener.Addr().(*net.TCPAddr).Port

	router := BuildRouter(s.hub, s.startTime, func() int { return s.actualPort }, s.GetWSURL, s.cfg.AuthToken)

	s.httpServer = &http.Server{
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	slog.Info("[Server] Core listening dynamically", "addr", listener.Addr().String(), "ws_url", s.GetWSURL())

	go func() {
		if err := s.httpServer.Serve(listener); err != nil && err != http.ErrServerClosed {
			slog.Error("[Server] HTTP Serve error", "error", err)
		}
	}()

	return nil
}

// GetActualPort returns the real port bound by OS.
func (s *Server) GetActualPort() int {
	return s.actualPort
}

// GetWSURL returns the dynamic active WebSocket URL.
func (s *Server) GetWSURL() string {
	host := s.cfg.ServerHost
	if host == "" || host == "0.0.0.0" {
		host = "127.0.0.1"
	}
	return fmt.Sprintf("ws://%s:%d/ws", host, s.actualPort)
}

// Stop gracefully shuts down the server.
func (s *Server) Stop(ctx context.Context) error {
	if s.httpServer != nil {
		return s.httpServer.Shutdown(ctx)
	}
	return nil
}
