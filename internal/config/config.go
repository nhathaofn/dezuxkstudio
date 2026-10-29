// Package config defines the typed runtime configuration and loads it from layered sources.
//
// Precedence (lowest → highest): defaults → config.json → environment → CLI flags.
// Every option is declared exactly once in fields.go; its file key, environment
// variable and flag name are all derived from that declaration.
package config

import (
	"fmt"
	"strings"
	"time"

	"app/internal/buildinfo"
)

// Mode selects which components run in the process.
type Mode string

const (
	// ModeDesktop runs the GUI with an embedded loopback core server (all-in-one).
	ModeDesktop Mode = "desktop"
	// ModeClient runs the GUI only and connects to a remote core server.
	ModeClient Mode = "client"
	// ModeServer runs the headless core server only.
	ModeServer Mode = "server"
)

// Modes lists every valid mode.
func Modes() []Mode { return []Mode{ModeDesktop, ModeClient, ModeServer} }

// ParseMode accepts canonical names and the legacy aliases used by earlier builds.
func ParseMode(s string) (Mode, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "desktop", "all-in-one", "dezuxkstudio":
		return ModeDesktop, nil
	case "client", "dezuxk-client":
		return ModeClient, nil
	case "server", "dezuxk-server":
		return ModeServer, nil
	}
	return "", fmt.Errorf("unknown mode %q (allowed: desktop, client, server)", s)
}

// Config is the complete runtime configuration.
type Config struct {
	Mode        Mode
	Server      ServerConfig
	Client      ClientConfig
	Realtime    RealtimeConfig
	Log         LogConfig
	Storage     StorageConfig
	Diagnostics DiagnosticsConfig
	Update      UpdateConfig
	Desktop     DesktopConfig
}

// ServerConfig configures the HTTP/WebSocket core server.
type ServerConfig struct {
	Host                  string
	Port                  int
	AuthToken             string
	AuthTokenFile         string
	AllowedOrigins        []string
	ReadHeaderTimeout     time.Duration
	IdleTimeout           time.Duration
	ShutdownTimeout       time.Duration
	EnableBroadcast       bool
	MaxBroadcastBodyBytes int64
}

// ClientConfig configures client mode (GUI connected to a remote server).
type ClientConfig struct {
	RemoteURL string
	AuthToken string
}

// RealtimeConfig tunes the action dispatcher and WebSocket sessions.
type RealtimeConfig struct {
	MaxConcurrentActions int
	ActionTimeout        time.Duration
	QueueTimeout         time.Duration
	ClientBufferSize     int
	MaxMessageBytes      int64
	PingInterval         time.Duration
	WriteTimeout         time.Duration
}

// LogConfig configures structured logging.
type LogConfig struct {
	Level      string
	Format     string
	Dir        string
	MaxSizeMB  int
	MaxBackups int
	Stdout     bool
}

// StorageConfig configures the local database.
type StorageConfig struct {
	DataDir       string
	DBFile        string
	BackupsToKeep int
	BusyTimeout   time.Duration
}

// DiagnosticsConfig configures diagnostic bundle exports.
type DiagnosticsConfig struct {
	Keep int
}

// UpdateConfig configures release update checks.
type UpdateConfig struct {
	FeedURL        string
	Timeout        time.Duration
	CheckOnStartup bool
}

// DesktopConfig configures the native window and desktop startup.
type DesktopConfig struct {
	Width        int
	Height       int
	MinWidth     int
	MinHeight    int
	Title        string
	Frameless    bool
	StartHidden  bool
	Background   string
	ReadyTimeout time.Duration
}

// DesktopOrigins are the Origin headers sent by the Wails webview in production:
// macOS/Linux use wails://wails, Windows (WebView2) uses http(s)://wails.localhost.
func DesktopOrigins() []string {
	return []string{"wails://wails", "http://wails.localhost", "https://wails.localhost"}
}

// DevOrigins are added in `wails dev` builds: the webview then loads the app from the
// Wails dev server (Windows: http://wails.localhost:34115) and the browser preview
// uses http://localhost:<port>.
func DevOrigins() []string {
	return []string{"wails://wails:*", "http://wails.localhost:*", "https://wails.localhost:*", "http://localhost:*", "http://127.0.0.1:*"}
}

// Default returns the baseline configuration for mode.
func Default(mode Mode) *Config {
	origins := DesktopOrigins()
	if buildinfo.IsDev() {
		origins = append(origins, DevOrigins()...)
	}

	return &Config{
		Mode: mode,
		Server: ServerConfig{
			Host:                  "127.0.0.1",
			Port:                  0, // OS-assigned free port
			AllowedOrigins:        origins,
			ReadHeaderTimeout:     10 * time.Second,
			IdleTimeout:           120 * time.Second,
			ShutdownTimeout:       5 * time.Second,
			EnableBroadcast:       false,
			MaxBroadcastBodyBytes: 1 << 20,
		},
		Realtime: RealtimeConfig{
			MaxConcurrentActions: 64,
			ActionTimeout:        30 * time.Second,
			QueueTimeout:         5 * time.Second,
			ClientBufferSize:     256,
			MaxMessageBytes:      1 << 20,
			PingInterval:         30 * time.Second,
			WriteTimeout:         5 * time.Second,
		},
		Log: LogConfig{
			Level:      "info",
			Format:     "text",
			MaxSizeMB:  10,
			MaxBackups: 3,
			Stdout:     mode == ModeServer || buildinfo.IsDev(),
		},
		Storage: StorageConfig{
			DBFile:        "app.db",
			BackupsToKeep: 5,
			BusyTimeout:   5 * time.Second,
		},
		Diagnostics: DiagnosticsConfig{Keep: 5},
		Update: UpdateConfig{
			FeedURL:        buildinfo.UpdateFeedURL,
			Timeout:        10 * time.Second,
			CheckOnStartup: true,
		},
		Desktop: DesktopConfig{
			Width:        1024,
			Height:       680,
			MinWidth:     720,
			MinHeight:    480,
			Background:   "#0f1117",
			ReadyTimeout: 10 * time.Second,
		},
	}
}

// WindowTitle returns the configured title or "<product> v<version>".
func (c *Config) WindowTitle() string {
	if c.Desktop.Title != "" {
		return c.Desktop.Title
	}
	return fmt.Sprintf("%s v%s", buildinfo.ProductName, buildinfo.Version)
}

// LogMaxSizeBytes converts the configured megabytes to bytes.
func (c *Config) LogMaxSizeBytes() int64 { return int64(c.Log.MaxSizeMB) << 20 }

// Redacted returns every option as "key → value" with secrets masked, for diagnostics.
func (c *Config) Redacted() map[string]string {
	out := make(map[string]string)
	for _, f := range fields(c) {
		v := f.val.String()
		if f.secret && v != "" {
			v = "[REDACTED]"
		}
		out[f.key] = v
	}
	return out
}
