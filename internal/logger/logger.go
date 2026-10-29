package logger

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
)

var (
	once    sync.Once
	log     *slog.Logger
	logFile *os.File
)

// ParseLevel translates string log levels into slog.Level.
func ParseLevel(lvl string) slog.Level {
	switch lvl {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// GetDefaultLogPath returns a dynamic, OS-appropriate application log path (Zero Hardcode).
func GetDefaultLogPath(appName string) string {
	configDir, err := os.UserConfigDir()
	if err != nil {
		configDir = os.TempDir()
	}
	return filepath.Join(configDir, appName, "logs", "app.log")
}

// Init initializes the global slog default logger.
// If filePath is non-empty, logs are written to both os.Stdout and the file.
func Init(level slog.Level, jsonFormat bool, filePath string) {
	var writer io.Writer = os.Stdout

	if filePath != "" {
		dir := filepath.Dir(filePath)
		if err := os.MkdirAll(dir, 0755); err == nil {
			f, err := os.OpenFile(filePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
			if err == nil {
				logFile = f
				writer = io.MultiWriter(os.Stdout, f)
			} else {
				fmt.Fprintf(os.Stderr, "[Logger] Warning: failed to open log file %s: %v\n", filePath, err)
			}
		} else {
			fmt.Fprintf(os.Stderr, "[Logger] Warning: failed to create log dir %s: %v\n", dir, err)
		}
	}

	opts := &slog.HandlerOptions{
		Level:     level,
		AddSource: false,
	}

	var handler slog.Handler
	if jsonFormat {
		handler = slog.NewJSONHandler(writer, opts)
	} else {
		handler = slog.NewTextHandler(writer, opts)
	}

	log = slog.New(handler)
	slog.SetDefault(log)
}

// Close flushes and closes any open log file safely.
func Close() {
	if logFile != nil {
		_ = logFile.Sync()
		_ = logFile.Close()
		logFile = nil
	}
}

// Get returns the structured logger.
func Get() *slog.Logger {
	once.Do(func() {
		if log == nil {
			Init(slog.LevelInfo, false, "")
		}
	})
	return log
}
