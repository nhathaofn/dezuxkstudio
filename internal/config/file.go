package config

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// ResolveConfigFile locates candidate configuration paths dynamically.
func ResolveConfigFile(customPath string) string {
	if customPath != "" {
		if _, err := os.Stat(customPath); err == nil {
			return customPath
		}
	}

	// 1. Check relative to current working directory
	cwdCandidate := "config.json"
	if _, err := os.Stat(cwdCandidate); err == nil {
		if abs, err := filepath.Abs(cwdCandidate); err == nil {
			return abs
		}
		return cwdCandidate
	}

	// 2. Check relative to executable directory (for installed production packages)
	if exe, err := os.Executable(); err == nil {
		exeCandidate := filepath.Join(filepath.Dir(exe), "config.json")
		if _, err := os.Stat(exeCandidate); err == nil {
			return exeCandidate
		}
	}

	return ""
}

// LoadFromFile attempts to populate config from a JSON file if found.
func LoadFromFile(filePath string, cfg *Config) error {
	resolved := ResolveConfigFile(filePath)
	if resolved == "" {
		return nil
	}

	data, err := os.ReadFile(resolved)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, cfg)
}
