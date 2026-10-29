package config

import (
	"flag"
	"strings"
)

// LoadFromFlags parses CLI arguments dynamically to override configuration.
func LoadFromFlags(args []string, cfg *Config) error {
	fs := flag.NewFlagSet("app", flag.ContinueOnError)
	configFlag := fs.String("config", cfg.ConfigFile, "Path to custom JSON configuration file")
	modeFlag := fs.String("mode", string(cfg.Mode), "Run mode: all-in-one, client, or server")
	hostFlag := fs.String("host", cfg.ServerHost, "Server host address")
	portFlag := fs.Int("port", cfg.ServerPort, "Server port (0 for auto-select)")
	remoteFlag := fs.String("remote-url", cfg.RemoteServerURL, "Remote WebSocket URL when running in client mode")
	originsFlag := fs.String("allowed-origins", strings.Join(cfg.AllowedOrigins, ","), "Comma-separated allowed WebSocket origins")
	authTokenFlag := fs.String("auth-token", cfg.AuthToken, "Security token / API Key required for incoming connections")
	logLevelFlag := fs.String("log-level", cfg.LogLevel, "Logging level: debug, info, warn, error")
	logFormatFlag := fs.String("log-format", cfg.LogFormat, "Logging output format: text, json")
	logFileFlag := fs.String("log-file", cfg.LogFile, "Path to output log file (optional, writes to stdout + file)")

	widthFlag := fs.Int("width", cfg.WindowWidth, "Initial window width in pixels")
	heightFlag := fs.Int("height", cfg.WindowHeight, "Initial window height in pixels")
	titleFlag := fs.String("title", cfg.WindowTitle, "Custom window title override")
	framelessFlag := fs.Bool("frameless", cfg.WindowFrameless, "Enable frameless window mode")
	hiddenFlag := fs.Bool("hidden", cfg.WindowStartHidden, "Start desktop window hidden")

	if err := fs.Parse(args); err != nil {
		return err
	}

	if *configFlag != "" {
		cfg.ConfigFile = *configFlag
	}
	cfg.Mode = AppMode(*modeFlag)
	cfg.ServerHost = *hostFlag
	cfg.ServerPort = *portFlag
	cfg.RemoteServerURL = *remoteFlag
	if *originsFlag != "" {
		cfg.AllowedOrigins = splitAndTrim(*originsFlag, ",")
	}
	cfg.AuthToken = *authTokenFlag
	cfg.LogLevel = *logLevelFlag
	cfg.LogFormat = *logFormatFlag
	cfg.LogFile = *logFileFlag

	cfg.WindowWidth = *widthFlag
	cfg.WindowHeight = *heightFlag
	cfg.WindowTitle = *titleFlag
	cfg.WindowFrameless = *framelessFlag
	cfg.WindowStartHidden = *hiddenFlag

	return nil
}
