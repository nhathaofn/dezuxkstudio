package config

// AppMode represents the operational mode of the system.
type AppMode string

const (
	ModeAllInOne AppMode = "all-in-one" // Runs Desktop Client + Embedded Go Core Server
	ModeClient   AppMode = "client"     // Thin Client only, connects to a remote server
	ModeServer   AppMode = "server"     // Headless Go Server daemon
)

// Config holds runtime configuration without any hardcoded values.
type Config struct {
	ConfigFile      string   `json:"-"`
	Mode            AppMode  `json:"mode"`
	ServerHost      string   `json:"server_host"`
	ServerPort      int      `json:"server_port"`
	RemoteServerURL string   `json:"remote_server_url"`
	AllowedOrigins  []string `json:"allowed_origins"`
	AuthToken       string   `json:"auth_token"`

	// Logging Configuration
	LogLevel  string `json:"log_level"`  // "debug", "info", "warn", "error"
	LogFormat string `json:"log_format"` // "text", "json"
	LogFile   string `json:"log_file"`   // File path for persistent disk logging

	// Window Settings (Dynamically configurable)
	WindowWidth       int    `json:"window_width"`
	WindowHeight      int    `json:"window_height"`
	WindowTitle       string `json:"window_title"`
	WindowFrameless   bool   `json:"window_frameless"`
	WindowStartHidden bool   `json:"window_start_hidden"`
}
