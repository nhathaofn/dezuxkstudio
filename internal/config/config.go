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
	Binaries    BinariesConfig
	Providers   ProvidersConfig
	Network     NetworkConfig
	Desktop     DesktopConfig
	Agent       AgentConfig
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
	LongActionTimeout    time.Duration
	QueueTimeout         time.Duration
	ClientBufferSize     int
	MaxMessageBytes      int64
	PingInterval         time.Duration
	WriteTimeout         time.Duration
	// KeepAliveInterval is how often an action waiting on bounded external work (CLI sign-in,
	// handshake) re-arms its idle timeout; it must stay below action_timeout.
	KeepAliveInterval time.Duration
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

// NetworkConfig tunes the shared outbound HTTP client (update checks, tool downloads).
type NetworkConfig struct {
	IdleConnTimeout     time.Duration
	MaxIdleConns        int
	MaxIdleConnsPerHost int
}

// ProvidersConfig configures API provider credential checks.
type ProvidersConfig struct {
	TestTimeout         time.Duration // max duration of a provider connectivity test or model discovery
	CheckOnStartup      bool          // test every saved key in the background when the app starts
	AutoCheck           bool          // check a key (alive? which models?) in the background when it is saved, changed or activated
	CheckConcurrency    int           // max start-up key tests running at once
	DefaultMaxWorkers   int           // generation requests per provider and capability running at once, unless set per provider
	DefaultRPM          int           // requests per minute per provider and capability (0 = unlimited), unless set per provider
	DefaultRequestGapMs int           // minimum pause between two requests to a provider (0 = none), unless set per provider
}

// BinariesConfig configures external tool (ffmpeg, yt-dlp...) inspection.
type BinariesConfig struct {
	ProbeTimeout time.Duration // max duration of `<tool> -version`; a cold start under antivirus can take seconds
}

// UpdateConfig configures release update checks.
type UpdateConfig struct {
	FeedURL        string
	Timeout        time.Duration
	CheckOnStartup bool
}

// AgentConfig tunes the external AI agent integration (timeouts, caches, chat context size).
type AgentConfig struct {
	RestoreConnection   bool          // reconnect the agent left connected when the app last ran
	TurnTimeout         time.Duration // max silence (no streamed output or tool activity) in one chat turn
	InitializeTimeout   time.Duration // max wait for an ACP agent handshake
	VersionProbeTimeout time.Duration // max duration of `<cli> --version`
	AuthProbeTimeout    time.Duration // max duration of a CLI auth status check
	LoginTimeout        time.Duration // max duration of an interactive sign-in (browser OAuth)
	ModelCatalogTimeout time.Duration // max duration of reading a CLI's model catalog
	InterruptWait       time.Duration // how long a cancelled turn waits for the CLI to report what it produced
	StatusCacheTTL      time.Duration // how long CLI auth/version probe results are reused
	AutoInstallCLI      bool          // download the agent CLIs into the app's own folder when they are missing
	AutoUpdateCLI       bool          // keep the app's own agent CLIs on the newest official release
	CLIUpdateInterval   time.Duration // how often to look for a newer agent CLI release
	ApprovalTTL         time.Duration // pending approvals expire after this long
	HistoryLimit        int           // messages returned to the chat UI
	ContextMessages     int           // past user/assistant messages replayed into a new session
	ContextMessageChars int           // per-message cap (runes) when replaying history
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
			LongActionTimeout:    15 * time.Minute,
			QueueTimeout:         5 * time.Second,
			ClientBufferSize:     256,
			MaxMessageBytes:      1 << 20,
			PingInterval:         30 * time.Second,
			WriteTimeout:         5 * time.Second,
			KeepAliveInterval:    5 * time.Second,
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
		Binaries:    BinariesConfig{ProbeTimeout: 15 * time.Second},
		Providers: ProvidersConfig{TestTimeout: 15 * time.Second, CheckOnStartup: true, AutoCheck: true, CheckConcurrency: 4,
			DefaultMaxWorkers: 2, DefaultRPM: 0, DefaultRequestGapMs: 0}, // ArcReel _CONNECTIVITY_CHECK_TIMEOUT
		Network: NetworkConfig{IdleConnTimeout: 90 * time.Second, MaxIdleConns: 100, MaxIdleConnsPerHost: 10},
		Update: UpdateConfig{
			FeedURL:        buildinfo.UpdateFeedURL,
			Timeout:        10 * time.Second,
			CheckOnStartup: true,
		},
		Agent: AgentConfig{
			RestoreConnection:   true,
			TurnTimeout:         10 * time.Minute,
			InitializeTimeout:   15 * time.Second,
			VersionProbeTimeout: 5 * time.Second, // Node-based CLI shims cold-start slowly on Windows
			AuthProbeTimeout:    10 * time.Second,
			LoginTimeout:        3 * time.Minute,
			ModelCatalogTimeout: 30 * time.Second,
			InterruptWait:       5 * time.Second,
			StatusCacheTTL:      5 * time.Minute,
			AutoInstallCLI:      true,
			AutoUpdateCLI:       true,
			CLIUpdateInterval:   6 * time.Hour,
			ApprovalTTL:         30 * time.Minute,
			HistoryLimit:        200,
			ContextMessages:     10,
			ContextMessageChars: 1200,
		},
		Desktop: DesktopConfig{
			Width:        0, // 0 = auto (80% of primary screen)
			Height:       0, // 0 = auto (80% of primary screen)
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
