// Package platform wraps OS integrations that differ between Windows, macOS and Linux.
package platform

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// OpenDirectory reveals an existing local directory in the native file manager.
// Only absolute paths to existing directories are accepted, so the call can never be
// abused to launch programs or URLs.
func OpenDirectory(dir string) error {
	if !filepath.IsAbs(dir) {
		return fmt.Errorf("platform: path must be absolute: %s", dir)
	}
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("platform: stat %s: %w", dir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("platform: not a directory: %s", dir)
	}

	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("explorer.exe", filepath.Clean(dir))
	case "darwin":
		cmd = exec.Command("open", "--", filepath.Clean(dir))
	default:
		cmd = exec.Command("xdg-open", filepath.Clean(dir))
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("platform: open %s: %w", dir, err)
	}
	// Reap the child so it does not linger as a zombie on Unix.
	go func() { _ = cmd.Wait() }()
	return nil
}
