package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"app/internal/config"
	"app/internal/logger"
	"app/internal/realtime"
	"app/internal/server"
)

func main() {
	cfg, err := config.LoadConfig(os.Args[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "[dezuxk-server] Failed to load config: %v\n", err)
		os.Exit(1)
	}

	logger.Init(logger.ParseLevel(cfg.LogLevel), cfg.LogFormat == "json", cfg.LogFile)
	defer logger.Close()

	cfg.Mode = config.ModeServer

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	hub := realtime.NewHub(cfg.AllowedOrigins, cfg.AuthToken)
	srv := server.NewServer(cfg, hub)
	if err := srv.Start(ctx); err != nil {
		slog.Error("[dezuxk-server] Server start error", "error", err)
		os.Exit(1)
	}

	slog.Info(fmt.Sprintf("[dezuxk-server] Headless Server started at %s. Press CTRL+C to stop.", srv.GetWSURL()))

	<-ctx.Done()

	slog.Info("[dezuxk-server] Shutting down...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_ = srv.Stop(shutdownCtx)
	slog.Info("[dezuxk-server] Server stopped.")
}
