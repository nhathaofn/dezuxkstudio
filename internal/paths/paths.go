// Package paths resolves OS-conventional per-user directories for an application.
//
//	             Windows                      macOS                                   Linux
//	Config       %APPDATA%\<id>               ~/Library/Application Support/<id>      $XDG_CONFIG_HOME/<id>
//	Data         %LOCALAPPDATA%\<id>\data     ~/Library/Application Support/<id>/data $XDG_DATA_HOME/<id>
//	Logs         %LOCALAPPDATA%\<id>\logs     ~/Library/Logs/<id>                     $XDG_STATE_HOME/<id>/logs
//
// Nothing is ever written next to the executable or into a temp directory.
package paths

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

const dirPerm = 0o700

// Paths holds the resolved application directories.
type Paths struct {
	Config string `json:"config"`
	Data   string `json:"data"`
	Logs   string `json:"logs"`
}

// Overrides lets configuration relocate the data and log directories.
type Overrides struct {
	DataDir string
	LogDir  string
}

// Resolve computes the directories for appID without touching the filesystem.
func Resolve(appID string, o Overrides) (Paths, error) {
	if appID == "" {
		return Paths{}, errors.New("paths: app id must not be empty")
	}
	base, err := resolveBase(appID)
	if err != nil {
		return Paths{}, err
	}
	if o.DataDir != "" {
		if base.Data, err = filepath.Abs(o.DataDir); err != nil {
			return Paths{}, fmt.Errorf("paths: data dir: %w", err)
		}
	}
	if o.LogDir != "" {
		if base.Logs, err = filepath.Abs(o.LogDir); err != nil {
			return Paths{}, fmt.Errorf("paths: log dir: %w", err)
		}
	}
	return base, nil
}

// ConfigDir returns the per-user config directory for appID (used before full config is known).
func ConfigDir(appID string) (string, error) {
	p, err := resolveBase(appID)
	return p.Config, err
}

// Ensure creates every directory with owner-only permissions.
func (p Paths) Ensure() error {
	for _, dir := range []string{p.Config, p.Data, p.Logs, p.Backups(), p.Diagnostics()} {
		if err := os.MkdirAll(dir, dirPerm); err != nil {
			return fmt.Errorf("paths: create %s: %w", dir, err)
		}
	}
	return nil
}

// Backups is where database snapshots are kept before schema migrations.
func (p Paths) Backups() string { return filepath.Join(p.Data, "backups") }

// Diagnostics is where exported diagnostic bundles are written.
func (p Paths) Diagnostics() string { return filepath.Join(p.Data, "diagnostics") }

func resolveBase(appID string) (Paths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Paths{}, fmt.Errorf("paths: home dir: %w", err)
	}
	configRoot, err := os.UserConfigDir()
	if err != nil {
		return Paths{}, fmt.Errorf("paths: config dir: %w", err)
	}

	switch runtime.GOOS {
	case "windows":
		local, err := os.UserCacheDir() // %LOCALAPPDATA% on Windows
		if err != nil {
			return Paths{}, fmt.Errorf("paths: local app data: %w", err)
		}
		root := filepath.Join(local, appID)
		return Paths{
			Config: filepath.Join(configRoot, appID),
			Data:   filepath.Join(root, "data"),
			Logs:   filepath.Join(root, "logs"),
		}, nil
	case "darwin":
		root := filepath.Join(configRoot, appID)
		return Paths{
			Config: root,
			Data:   filepath.Join(root, "data"),
			Logs:   filepath.Join(home, "Library", "Logs", appID),
		}, nil
	default:
		return Paths{
			Config: filepath.Join(configRoot, appID),
			Data:   filepath.Join(xdg("XDG_DATA_HOME", home, ".local", "share"), appID),
			Logs:   filepath.Join(xdg("XDG_STATE_HOME", home, ".local", "state"), appID, "logs"),
		}, nil
	}
}

func xdg(env, home string, fallback ...string) string {
	if v := os.Getenv(env); v != "" && filepath.IsAbs(v) {
		return v
	}
	return filepath.Join(append([]string{home}, fallback...)...)
}
