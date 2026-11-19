// Package logger builds the structured slog logger used across the application:
// level filtering, text/JSON output, size-based file rotation and secret redaction.
package logger

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// LogFileName is the active log file inside the log directory.
const LogFileName = "app.log"

// Options configures New.
type Options struct {
	Level        string // debug | info | warn | error
	Format       string // text | json
	Dir          string // empty disables file output
	MaxSizeBytes int64
	MaxBackups   int
	Stdout       bool
}

// Logger owns the slog.Logger and the file it writes to.
type Logger struct {
	*slog.Logger
	file *RotatingWriter
}

// New creates a logger. At least one sink (file or stdout) must be enabled.
func New(o Options) (*Logger, error) {
	level, err := ParseLevel(o.Level)
	if err != nil {
		return nil, err
	}

	var sinks []io.Writer
	var rw *RotatingWriter
	if o.Dir != "" {
		rw, err = NewRotatingWriter(filepath.Join(o.Dir, LogFileName), o.MaxSizeBytes, o.MaxBackups)
		if err != nil {
			return nil, err
		}
		sinks = append(sinks, rw)
	}
	if o.Stdout {
		sinks = append(sinks, os.Stdout)
	}
	if len(sinks) == 0 {
		return nil, errors.New("logger: no output enabled (set a log dir or enable stdout)")
	}

	opts := &slog.HandlerOptions{Level: level, ReplaceAttr: redact}
	out := io.MultiWriter(sinks...)

	var h slog.Handler
	switch o.Format {
	case "json":
		h = slog.NewJSONHandler(out, opts)
	case "text":
		h = slog.NewTextHandler(out, opts)
	default:
		if rw != nil {
			_ = rw.Close()
		}
		return nil, fmt.Errorf("logger: unknown format %q", o.Format)
	}
	return &Logger{Logger: slog.New(h), file: rw}, nil
}

// Fallback returns a stderr logger for use before configuration is available.
func Fallback() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{ReplaceAttr: redact}))
}

// Close flushes and closes the log file, if any.
func (l *Logger) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	return l.file.Close()
}

// ParseLevel converts a configuration level name to slog.Level.
func ParseLevel(s string) (slog.Level, error) {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	}
	return 0, fmt.Errorf("logger: unknown level %q", s)
}

// sensitiveKeys are attribute-name fragments whose values are never written to logs.
var sensitiveKeys = []string{"token", "secret", "password", "authorization", "api_key", "apikey", "cookie"}

var (
	reBearerSecret = regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9._~+/=-]{4,}`)
	rePrefixedKey  = regexp.MustCompile(`\b(?:sk-(?:ant-|proj-)?[A-Za-z0-9_-]{8,}|AIza[0-9A-Za-z_-]{20,})\b`)
	reJSONSecret   = regexp.MustCompile(`(?i)("(?:api_key|apiKey|private_key|client_secret|access_token|refresh_token|auth_token|token|secret|password)"\s*:\s*")((?:[^"\\]|\\.)*)(")`)
	reQuerySecret  = regexp.MustCompile(`(?i)([?&](?:key|api_key|token|ticket|access_token)=)([^&\s"']+)`)
	reHeaderSecret = regexp.MustCompile(`(?i)\b(x-goog-api-key|x-api-key)\s*([:=])\s*([^\s,;]+)`)
)

// RedactString masks embedded secrets, API keys, bearer tokens, JSON credentials,
// and signed ticket/key query parameters from arbitrary log or crash strings.
func RedactString(s string) string {
	if s == "" {
		return ""
	}
	s = reBearerSecret.ReplaceAllString(s, "Bearer [REDACTED]")
	s = rePrefixedKey.ReplaceAllString(s, "[REDACTED]")
	s = reJSONSecret.ReplaceAllString(s, "${1}[REDACTED]${3}")
	s = reQuerySecret.ReplaceAllString(s, "${1}[REDACTED]")
	s = reHeaderSecret.ReplaceAllString(s, "${1}${2}[REDACTED]")
	return s
}

func redact(_ []string, a slog.Attr) slog.Attr {
	key := strings.ToLower(a.Key)
	for _, s := range sensitiveKeys {
		if strings.Contains(key, s) {
			return slog.String(a.Key, "[REDACTED]")
		}
	}
	switch a.Value.Kind() {
	case slog.KindString:
		return slog.String(a.Key, RedactString(a.Value.String()))
	case slog.KindAny:
		if err, ok := a.Value.Any().(error); ok && err != nil {
			return slog.String(a.Key, RedactString(err.Error()))
		}
	}
	return a
}
