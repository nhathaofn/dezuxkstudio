package system

import (
	"errors"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"app/internal/apperr"
	"app/internal/buildinfo"
	"app/internal/config"
	"app/internal/contracts"
	"app/internal/paths"
)

func testPaths(t *testing.T) paths.Paths {
	t.Helper()
	base := t.TempDir()
	return paths.Paths{
		Config: filepath.Join(base, "config"),
		Data:   filepath.Join(base, "data"),
		Logs:   filepath.Join(base, "logs"),
	}
}

func TestNewAndInfo(t *testing.T) {
	p := testPaths(t)
	for _, mode := range []config.Mode{config.ModeDesktop, config.ModeClient, config.ModeServer} {
		t.Run(string(mode), func(t *testing.T) {
			svc := New(mode, p, func(string) error { return nil })
			if svc == nil {
				t.Fatal("New returned nil")
			}
			svc.started = time.Now().Add(-3 * time.Second)

			info := svc.Info()
			if info.Mode != mode {
				t.Fatalf("Mode = %q, want %q", info.Mode, mode)
			}
			wantBuild := contracts.NewBuildInfo(buildinfo.Get())
			if info.Build != wantBuild {
				t.Fatalf("Build = %+v, want %+v", info.Build, wantBuild)
			}
			if info.Runtime.NumCPU != runtime.NumCPU() || info.Runtime.NumCPU <= 0 {
				t.Fatalf("NumCPU = %d, want %d (> 0)", info.Runtime.NumCPU, runtime.NumCPU())
			}
			if info.Runtime.NumGoroutine < 1 {
				t.Fatalf("NumGoroutine = %d, want >= 1", info.Runtime.NumGoroutine)
			}
			if info.Runtime.AllocMB <= 0 || info.Runtime.SysMB <= 0 {
				t.Fatalf("unexpected memory metrics: AllocMB=%v SysMB=%v", info.Runtime.AllocMB, info.Runtime.SysMB)
			}
			if info.Runtime.UptimeSeconds < 2 {
				t.Fatalf("UptimeSeconds = %d, want >= 2", info.Runtime.UptimeSeconds)
			}
			wantPaths := contracts.PathsInfo{
				Config:      p.Config,
				Data:        p.Data,
				Logs:        p.Logs,
				Diagnostics: p.Diagnostics(),
			}
			if info.Paths != wantPaths {
				t.Fatalf("Paths = %+v, want %+v", info.Paths, wantPaths)
			}
		})
	}
}

func TestOpenFolder_ValidTargets(t *testing.T) {
	p := testPaths(t)
	cases := []struct {
		target contracts.FolderTarget
		want   string
	}{
		{contracts.FolderLogs, p.Logs},
		{contracts.FolderData, p.Data},
		{contracts.FolderConfig, p.Config},
		{contracts.FolderDiagnostics, p.Diagnostics()},
		{contracts.FolderAgentCustomizations, p.AgentCustomizations()},
	}
	if len(cases) != len(contracts.FolderTargets()) {
		t.Fatalf("test cases (%d) do not cover all FolderTargets (%d)", len(cases), len(contracts.FolderTargets()))
	}

	for _, tc := range cases {
		t.Run(string(tc.target), func(t *testing.T) {
			var opened []string
			svc := New(config.ModeDesktop, p, func(dir string) error {
				opened = append(opened, dir)
				return nil
			})
			if err := svc.OpenFolder(tc.target); err != nil {
				t.Fatalf("OpenFolder(%q) unexpected error: %v", tc.target, err)
			}
			if len(opened) != 1 || opened[0] != tc.want {
				t.Fatalf("opened = %v, want [%q]", opened, tc.want)
			}
		})
	}
}

func TestOpenFolder_InvalidTarget(t *testing.T) {
	p := testPaths(t)
	for _, target := range []contracts.FolderTarget{"", "unknown", "backups"} {
		called := false
		svc := New(config.ModeDesktop, p, func(string) error {
			called = true
			return nil
		})
		err := svc.OpenFolder(target)
		if !apperr.IsCode(err, apperr.CodeValidation) {
			t.Fatalf("OpenFolder(%q) err = %v, want %s", target, err, apperr.CodeValidation)
		}
		if called {
			t.Fatalf("OpenFolder(%q) must not call opener on invalid target", target)
		}
	}
}

func TestOpenFolder_OpenerError(t *testing.T) {
	p := testPaths(t)
	boom := errors.New("shell open failed")
	svc := New(config.ModeDesktop, p, func(string) error {
		return boom
	})
	err := svc.OpenFolder(contracts.FolderLogs)
	if !apperr.IsCode(err, apperr.CodeUnavailable) {
		t.Fatalf("OpenFolder err = %v, want %s", err, apperr.CodeUnavailable)
	}
	if !errors.Is(err, boom) {
		t.Fatalf("OpenFolder err = %v, expected wrapped %v", err, boom)
	}
}
