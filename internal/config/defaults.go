package config

// DefaultConfig provides baseline default settings.
// Port 0 signifies dynamic allocation during runtime startup.
func DefaultConfig() *Config {
	return &Config{
		ConfigFile:        "",
		Mode:              ModeAllInOne,
		ServerHost:        "127.0.0.1",
		ServerPort:        0, // Dynamic OS-allocated port
		RemoteServerURL:   "",
		AllowedOrigins:    []string{"wails://*", "http://wails.localhost*", "http://localhost:*", "http://127.0.0.1:*"},
		AuthToken:         "",
		LogLevel:          "info",
		LogFormat:         "text",
		LogFile:           "",
		WindowWidth:       1024,
		WindowHeight:      680,
		WindowTitle:       "",
		WindowFrameless:   false,
		WindowStartHidden: false,
	}
}
