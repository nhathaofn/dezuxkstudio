package diagnostics

import (
	"archive/zip"
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"app/internal/contracts"
)

func newService(t *testing.T, keep int) (*Service, string, string) {
	t.Helper()
	out, logs := t.TempDir(), t.TempDir()
	for _, name := range []string{"app.log", "app.log.1", "crash_1.log", "ignore.txt"} {
		if err := os.WriteFile(filepath.Join(logs, name), []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	tick := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s, err := New(Options{
		OutputDir: out,
		LogDir:    logs,
		Keep:      keep,
		Config:    func() map[string]string { return map[string]string{"server.auth_token": "[REDACTED]"} },
		Info:      func() contracts.SystemInfo { return contracts.SystemInfo{Mode: "desktop"} },
		Now:       func() time.Time { tick = tick.Add(time.Second); return tick },
	})
	if err != nil {
		t.Fatal(err)
	}
	return s, out, logs
}

func TestExportBundlesLogsAndMetadata(t *testing.T) {
	s, _, _ := newService(t, 3)
	var last float64
	bundle, err := s.Export(context.Background(), func(p float64, _ string) {
		if p < last {
			t.Errorf("progress went backwards: %v → %v", last, p)
		}
		last = p
	})
	if err != nil {
		t.Fatal(err)
	}
	if last != 100 || bundle.SizeBytes == 0 {
		t.Fatalf("bundle=%+v last=%v", bundle, last)
	}

	zr, err := zip.OpenReader(bundle.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	var names []string
	for _, f := range zr.File {
		names = append(names, f.Name)
	}
	sort.Strings(names)
	want := []string{"config.json", "logs/app.log", "logs/app.log.1", "logs/crash_1.log", "manifest.json"}
	if len(names) != len(want) {
		t.Fatalf("entries = %v", names)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("entries = %v", names)
		}
	}
}

func TestExportPrunesOldBundles(t *testing.T) {
	s, out, _ := newService(t, 2)
	for i := 0; i < 4; i++ {
		if _, err := s.Export(context.Background(), nil); err != nil {
			t.Fatal(err)
		}
	}
	matches, _ := filepath.Glob(filepath.Join(out, bundlePrefix+"*"+bundleExt))
	if len(matches) != 2 {
		t.Fatalf("bundles = %v", matches)
	}
}

func TestExportHonoursCancellation(t *testing.T) {
	s, out, _ := newService(t, 2)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Export(ctx, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	entries, _ := os.ReadDir(out)
	if len(entries) != 0 {
		t.Fatalf("cancelled export left files: %v", entries)
	}
}
