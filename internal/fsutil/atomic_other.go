//go:build !windows

package fsutil

import "os"

func renameWithRetry(oldPath, newPath string) error {
	return os.Rename(oldPath, newPath)
}
