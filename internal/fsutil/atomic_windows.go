//go:build windows

package fsutil

import (
	"errors"
	"os"
	"time"

	"golang.org/x/sys/windows"
)

// renameWithRetry retries os.Rename briefly on Windows when Defender or Search Indexer
// holds a transient sharing/access lock on the target or temp file.
func renameWithRetry(oldPath, newPath string) error {
	backoffs := []time.Duration{
		15 * time.Millisecond,
		30 * time.Millisecond,
		60 * time.Millisecond,
		120 * time.Millisecond,
		240 * time.Millisecond,
	}
	var err error
	for i := 0; i <= len(backoffs); i++ {
		err = os.Rename(oldPath, newPath)
		if err == nil {
			return nil
		}
		if !errors.Is(err, windows.ERROR_SHARING_VIOLATION) && !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			return err
		}
		if i < len(backoffs) {
			time.Sleep(backoffs[i])
		}
	}
	return err
}
