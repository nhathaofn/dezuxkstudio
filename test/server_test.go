package test

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"app/internal/config"
	"app/internal/realtime"
	"app/internal/server"
)

func TestServer_LifecycleDynamicPort(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.ServerHost = "127.0.0.1"
	cfg.ServerPort = 0 // Dynamic binding

	hub := realtime.NewHub(cfg.AllowedOrigins, cfg.AuthToken)
	srv := server.NewServer(cfg, hub)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := srv.Start(ctx); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}

	actualPort := srv.GetActualPort()
	if actualPort <= 0 {
		t.Fatalf("expected positive actual port, got %d", actualPort)
	}

	// Verify health endpoint
	healthURL := fmt.Sprintf("http://%s:%d/health", cfg.ServerHost, actualPort)
	resp, err := http.Get(healthURL)
	if err != nil {
		t.Fatalf("failed to query health endpoint: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	// Graceful shutdown
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer shutdownCancel()

	if err := srv.Stop(shutdownCtx); err != nil {
		t.Errorf("error during server shutdown: %v", err)
	}
}
