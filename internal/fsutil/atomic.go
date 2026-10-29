// Package fsutil contains crash-safe filesystem helpers.
package fsutil

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// WriteFileAtomic writes data to path so that readers observe either the old or
// the new content, never a partial file.
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	return WriteAtomic(path, perm, func(w io.Writer) error {
		_, err := io.Copy(w, bytes.NewReader(data))
		return err
	})
}

// WriteAtomic streams content produced by write into a temp file in the target
// directory, fsyncs it, then renames it over path. On any error the temp file is removed.
func WriteAtomic(path string, perm os.FileMode, write func(w io.Writer) error) (err error) {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("fsutil: create temp in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	defer func() {
		if err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmpName)
		}
	}()

	if err = write(tmp); err != nil {
		return fmt.Errorf("fsutil: write %s: %w", path, err)
	}
	if err = tmp.Sync(); err != nil {
		return fmt.Errorf("fsutil: sync %s: %w", path, err)
	}
	if err = tmp.Close(); err != nil {
		return fmt.Errorf("fsutil: close %s: %w", path, err)
	}
	if err = os.Chmod(tmpName, perm); err != nil {
		return fmt.Errorf("fsutil: chmod %s: %w", path, err)
	}
	if err = os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("fsutil: rename to %s: %w", path, err)
	}
	return nil
}
