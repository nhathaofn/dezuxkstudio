package config

// LoadConfig orchestrates configuration loading in strict hierarchical priority:
// 1. Defaults -> 2. Config File (if exists) -> 3. Environment Variables -> 4. CLI Flags.
func LoadConfig(args []string) (*Config, error) {
	cfg := DefaultConfig()

	// Parse temporary flag set first to identify if --config was supplied
	tempFlags := &Config{}
	_ = LoadFromFlags(args, tempFlags)

	configFilePath := ""
	if tempFlags.ConfigFile != "" {
		configFilePath = tempFlags.ConfigFile
	}

	// 1. File layer
	_ = LoadFromFile(configFilePath, cfg)

	// 2. Environment variables layer
	LoadFromEnv(cfg)

	// 3. Command line flags layer (highest priority, overrides all prior layers)
	if err := LoadFromFlags(args, cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}
