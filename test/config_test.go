package test

import (
	"os"
	"testing"

	"app/internal/config"
)

func TestDefaultConfig(t *testing.T) {
	cfg := config.DefaultConfig()
	if cfg.Mode != config.ModeAllInOne {
		t.Errorf("expected ModeAllInOne, got %v", cfg.Mode)
	}
	if cfg.ServerPort != 0 {
		t.Errorf("expected ServerPort to default to 0 (dynamic), got %d", cfg.ServerPort)
	}
	if cfg.WindowWidth <= 0 || cfg.WindowHeight <= 0 {
		t.Errorf("expected positive window dimensions, got %dx%d", cfg.WindowWidth, cfg.WindowHeight)
	}
	if len(cfg.AllowedOrigins) == 0 {
		t.Errorf("expected default allowed origins to be non-empty")
	}
}

func TestLoadFromFlags(t *testing.T) {
	cfg := config.DefaultConfig()
	args := []string{
		"--host=0.0.0.0",
		"--port=8888",
		"--mode=server",
		"--width=1280",
		"--height=800",
		"--auth-token=secret123",
		"--log-level=debug",
		"--log-format=json",
		"--log-file=/custom/app.log",
	}
	if err := config.LoadFromFlags(args, cfg); err != nil {
		t.Fatalf("unexpected error parsing flags: %v", err)
	}

	if cfg.ServerHost != "0.0.0.0" {
		t.Errorf("expected host 0.0.0.0, got %s", cfg.ServerHost)
	}
	if cfg.ServerPort != 8888 {
		t.Errorf("expected port 8888, got %d", cfg.ServerPort)
	}
	if cfg.Mode != config.ModeServer {
		t.Errorf("expected mode server, got %s", cfg.Mode)
	}
	if cfg.WindowWidth != 1280 || cfg.WindowHeight != 800 {
		t.Errorf("expected 1280x800, got %dx%d", cfg.WindowWidth, cfg.WindowHeight)
	}
	if cfg.AuthToken != "secret123" {
		t.Errorf("expected auth token secret123, got %s", cfg.AuthToken)
	}
	if cfg.LogLevel != "debug" {
		t.Errorf("expected log level debug, got %s", cfg.LogLevel)
	}
	if cfg.LogFormat != "json" {
		t.Errorf("expected log format json, got %s", cfg.LogFormat)
	}
	if cfg.LogFile != "/custom/app.log" {
		t.Errorf("expected log file /custom/app.log, got %s", cfg.LogFile)
	}
}

func TestLoadFromEnv(t *testing.T) {
	cfg := config.DefaultConfig()
	_ = os.Setenv("SERVER_HOST", "192.168.1.50")
	_ = os.Setenv("SERVER_PORT", "7777")
	_ = os.Setenv("WINDOW_WIDTH", "1440")
	_ = os.Setenv("AUTH_TOKEN", "envtoken")
	_ = os.Setenv("LOG_LEVEL", "warn")
	_ = os.Setenv("LOG_FORMAT", "json")
	_ = os.Setenv("LOG_FILE", "/env/path/app.log")
	defer func() {
		_ = os.Unsetenv("SERVER_HOST")
		_ = os.Unsetenv("SERVER_PORT")
		_ = os.Unsetenv("WINDOW_WIDTH")
		_ = os.Unsetenv("AUTH_TOKEN")
		_ = os.Unsetenv("LOG_LEVEL")
		_ = os.Unsetenv("LOG_FORMAT")
		_ = os.Unsetenv("LOG_FILE")
	}()

	config.LoadFromEnv(cfg)

	if cfg.ServerHost != "192.168.1.50" {
		t.Errorf("expected 192.168.1.50, got %s", cfg.ServerHost)
	}
	if cfg.ServerPort != 7777 {
		t.Errorf("expected 7777, got %d", cfg.ServerPort)
	}
	if cfg.WindowWidth != 1440 {
		t.Errorf("expected 1440, got %d", cfg.WindowWidth)
	}
	if cfg.AuthToken != "envtoken" {
		t.Errorf("expected envtoken, got %s", cfg.AuthToken)
	}
	if cfg.LogLevel != "warn" {
		t.Errorf("expected warn, got %s", cfg.LogLevel)
	}
	if cfg.LogFormat != "json" {
		t.Errorf("expected json, got %s", cfg.LogFormat)
	}
	if cfg.LogFile != "/env/path/app.log" {
		t.Errorf("expected /env/path/app.log, got %s", cfg.LogFile)
	}
}
