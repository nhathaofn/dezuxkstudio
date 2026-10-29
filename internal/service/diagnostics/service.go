// Package diagnostics builds support bundles: build/runtime info, redacted
// configuration, logs and crash reports zipped into one file the user can attach
// to a bug report.
package diagnostics

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"app/internal/apperr"
	"app/internal/contracts"
	"app/internal/fsutil"
)

const (
	bundlePrefix = "diagnostics-"
	bundleExt    = ".zip"
	bundlePerm   = 0o600
)

// Options configures the service.
type Options struct {
	OutputDir string                   // where bundles are written
	LogDir    string                   // collected recursively (*.log*)
	Keep      int                      // bundles retained after export
	Config    func() map[string]string // redacted configuration
	Info      func() contracts.SystemInfo
	Now       func() time.Time
}

// Service exports diagnostic bundles.
type Service struct{ o Options }

// New validates options and creates the service.
func New(o Options) (*Service, error) {
	if o.OutputDir == "" || o.LogDir == "" || o.Keep <= 0 || o.Config == nil || o.Info == nil {
		return nil, errors.New("diagnostics: incomplete options")
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	return &Service{o: o}, nil
}

// Progress receives export progress (0-100).
type Progress func(percent float64, message string)

// Export writes a new bundle atomically and prunes old ones. It honours ctx cancellation.
func (s *Service) Export(ctx context.Context, progress Progress) (contracts.DiagnosticsBundle, error) {
	if progress == nil {
		progress = func(float64, string) {}
	}
	logs, err := s.collectLogs()
	if err != nil {
		return contracts.DiagnosticsBundle{}, apperr.Wrap(apperr.CodeUnavailable, "could not read log directory", err)
	}

	now := s.o.Now().UTC()
	name := bundlePrefix + now.Format("20060102T150405.000000000Z") + bundleExt
	path := filepath.Join(s.o.OutputDir, name)
	if err := os.MkdirAll(s.o.OutputDir, 0o700); err != nil {
		return contracts.DiagnosticsBundle{}, apperr.Internal(err)
	}

	total := float64(len(logs) + 2)
	err = fsutil.WriteAtomic(path, bundlePerm, func(w io.Writer) error {
		zw := zip.NewWriter(w)
		progress(0, "manifest")
		if err := writeJSON(zw, "manifest.json", s.o.Info()); err != nil {
			return err
		}
		progress(100/total, "configuration")
		if err := writeJSON(zw, "config.json", s.o.Config()); err != nil {
			return err
		}
		for i, rel := range logs {
			if err := ctx.Err(); err != nil {
				return err
			}
			progress(100*float64(i+2)/total, rel)
			if err := copyFile(zw, filepath.Join(s.o.LogDir, rel), "logs/"+filepath.ToSlash(rel)); err != nil {
				return err
			}
		}
		return zw.Close()
	})
	if err != nil {
		if ctx.Err() != nil {
			return contracts.DiagnosticsBundle{}, ctx.Err()
		}
		return contracts.DiagnosticsBundle{}, apperr.Wrap(apperr.CodeInternal, "could not write diagnostics bundle", err)
	}
	progress(100, "done")

	info, err := os.Stat(path)
	if err != nil {
		return contracts.DiagnosticsBundle{}, apperr.Internal(err)
	}
	s.prune()
	return contracts.DiagnosticsBundle{Path: path, SizeBytes: info.Size(), CreatedAt: now.UnixMilli()}, nil
}

func (s *Service) collectLogs() ([]string, error) {
	var out []string
	err := filepath.WalkDir(s.o.LogDir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return filepath.SkipAll
			}
			return err
		}
		if d.IsDir() || !strings.Contains(d.Name(), ".log") {
			return nil
		}
		rel, err := filepath.Rel(s.o.LogDir, p)
		if err != nil {
			return err
		}
		out = append(out, rel)
		return nil
	})
	sort.Strings(out)
	return out, err
}

// prune keeps the newest Keep bundles (names sort chronologically).
func (s *Service) prune() {
	entries, err := os.ReadDir(s.o.OutputDir)
	if err != nil {
		return
	}
	var bundles []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), bundlePrefix) && strings.HasSuffix(e.Name(), bundleExt) {
			bundles = append(bundles, e.Name())
		}
	}
	sort.Strings(bundles)
	for len(bundles) > s.o.Keep {
		_ = os.Remove(filepath.Join(s.o.OutputDir, bundles[0]))
		bundles = bundles[1:]
	}
}

func writeJSON(zw *zip.Writer, name string, v any) error {
	w, err := zw.Create(name)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func copyFile(zw *zip.Writer, src, name string) error {
	f, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open %s: %w", src, err)
	}
	defer f.Close()
	w, err := zw.Create(name)
	if err != nil {
		return err
	}
	_, err = io.Copy(w, f)
	return err
}
