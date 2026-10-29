package test

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"app/internal/logger"
)

func TestLogger_FileOutput(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "logtest_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	logFilePath := filepath.Join(tempDir, "sub", "test.log")
	logger.Init(slog.LevelInfo, false, logFilePath)
	defer logger.Close()

	slog.Info("test message for logger", "key", "val")
	logger.Close()

	content, err := os.ReadFile(logFilePath)
	if err != nil {
		t.Fatalf("failed to read created log file: %v", err)
	}

	if !strings.Contains(string(content), "test message for logger") {
		t.Errorf("expected log file to contain test message, got: %s", string(content))
	}
}
