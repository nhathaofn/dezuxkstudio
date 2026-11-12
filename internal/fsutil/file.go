package fsutil

import "os"

// IsFile reports whether path exists and is a regular file (not a directory).
func IsFile(path string) bool {
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
