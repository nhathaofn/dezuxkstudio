package config

import (
	"os"
	"strconv"
	"strings"
)

// LoadFromEnv overrides configuration values from environment variables dynamically.
func LoadFromEnv(cfg *Config) {
	if val := os.Getenv("CONFIG_FILE"); val != "" {
		cfg.ConfigFile = val
	}
	if val := os.Getenv("APP_MODE"); val != "" {
		cfg.Mode = AppMode(val)
	}
	if val := os.Getenv("SERVER_HOST"); val != "" {
		cfg.ServerHost = val
	}
	if val := os.Getenv("SERVER_PORT"); val != "" {
		if p, err := strconv.Atoi(val); err == nil {
			cfg.ServerPort = p
		}
	}
	if val := os.Getenv("REMOTE_SERVER_URL"); val != "" {
		cfg.RemoteServerURL = val
	}
	if val := os.Getenv("ALLOWED_ORIGINS"); val != "" {
		cfg.AllowedOrigins = splitAndTrim(val, ",")
	}
	if val := os.Getenv("AUTH_TOKEN"); val != "" {
		cfg.AuthToken = val
	}
	if val := os.Getenv("LOG_LEVEL"); val != "" {
		cfg.LogLevel = val
	}
	if val := os.Getenv("LOG_FORMAT"); val != "" {
		cfg.LogFormat = val
	}
	if val := os.Getenv("LOG_FILE"); val != "" {
		cfg.LogFile = val
	}
	if val := os.Getenv("WINDOW_WIDTH"); val != "" {
		if w, err := strconv.Atoi(val); err == nil {
			cfg.WindowWidth = w
		}
	}
	if val := os.Getenv("WINDOW_HEIGHT"); val != "" {
		if h, err := strconv.Atoi(val); err == nil {
			cfg.WindowHeight = h
		}
	}
	if val := os.Getenv("WINDOW_TITLE"); val != "" {
		cfg.WindowTitle = val
	}
	if val := os.Getenv("WINDOW_FRAMELESS"); val != "" {
		cfg.WindowFrameless = val == "true" || val == "1"
	}
	if val := os.Getenv("WINDOW_START_HIDDEN"); val != "" {
		cfg.WindowStartHidden = val == "true" || val == "1"
	}
}

func splitAndTrim(s, sep string) []string {
	parts := strings.Split(s, sep)
	var res []string
	for _, p := range parts {
		trimmed := strings.TrimSpace(p)
		if trimmed != "" {
			res = append(res, trimmed)
		}
	}
	return res
}
