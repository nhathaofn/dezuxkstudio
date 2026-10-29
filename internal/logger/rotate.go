package logger

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

const filePerm = 0o600

// RotatingWriter is an io.Writer that rotates the file once it reaches maxBytes,
// keeping at most maxBackups old files named <file>.1 (newest) … <file>.N.
type RotatingWriter struct {
	mu         sync.Mutex
	path       string
	maxBytes   int64
	maxBackups int
	file       *os.File
	size       int64
}

// NewRotatingWriter opens (or creates) path for appending.
func NewRotatingWriter(path string, maxBytes int64, maxBackups int) (*RotatingWriter, error) {
	if maxBytes <= 0 {
		return nil, errors.New("logger: max size must be > 0")
	}
	if maxBackups < 0 {
		return nil, errors.New("logger: max backups must be >= 0")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("logger: create log dir: %w", err)
	}
	w := &RotatingWriter{path: path, maxBytes: maxBytes, maxBackups: maxBackups}
	if err := w.open(); err != nil {
		return nil, err
	}
	return w, nil
}

func (w *RotatingWriter) open() error {
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, filePerm)
	if err != nil {
		return fmt.Errorf("logger: open %s: %w", w.path, err)
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return fmt.Errorf("logger: stat %s: %w", w.path, err)
	}
	w.file, w.size = f, info.Size()
	return nil
}

// rotate closes the current file, shifts backups and opens a fresh file.
// On failure the writer keeps (or reopens) the current file so writes never hit a nil file.
func (w *RotatingWriter) rotate() error {
	if err := w.file.Close(); err != nil {
		return w.reopenAfter(fmt.Errorf("logger: close for rotation: %w", err))
	}
	w.file = nil

	if w.maxBackups == 0 {
		if err := os.Remove(w.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return w.reopenAfter(err)
		}
		return w.open()
	}

	_ = os.Remove(w.backupName(w.maxBackups))
	for i := w.maxBackups - 1; i >= 1; i-- {
		if err := os.Rename(w.backupName(i), w.backupName(i+1)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return w.reopenAfter(err)
		}
	}
	if err := os.Rename(w.path, w.backupName(1)); err != nil {
		return w.reopenAfter(err)
	}
	return w.open()
}

func (w *RotatingWriter) reopenAfter(cause error) error {
	if w.file == nil {
		if err := w.open(); err != nil {
			return errors.Join(cause, err)
		}
	}
	return cause
}

func (w *RotatingWriter) backupName(i int) string { return fmt.Sprintf("%s.%d", w.path, i) }

// Write implements io.Writer.
func (w *RotatingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.file == nil {
		if err := w.open(); err != nil {
			return 0, err
		}
	}
	if w.size > 0 && w.size+int64(len(p)) > w.maxBytes {
		if err := w.rotate(); err != nil {
			fmt.Fprintf(os.Stderr, "log rotation failed: %v\n", err)
		}
	}
	n, err := w.file.Write(p)
	w.size += int64(n)
	return n, err
}

// Close syncs and closes the file.
func (w *RotatingWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return nil
	}
	err := errors.Join(w.file.Sync(), w.file.Close())
	w.file = nil
	return err
}
