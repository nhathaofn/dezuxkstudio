// Package system implements host/runtime introspection and OS integration use cases.
package system

import (
	"fmt"
	"runtime"
	"time"

	"app/internal/apperr"
	"app/internal/buildinfo"
	"app/internal/config"
	"app/internal/contracts"
	"app/internal/paths"
)

const bytesPerMB = 1 << 20

// Service exposes system information and folder shortcuts.
type Service struct {
	mode    config.Mode
	paths   paths.Paths
	started time.Time
	open    func(dir string) error
}

// New creates the service; open reveals a directory in the OS file manager.
func New(mode config.Mode, p paths.Paths, open func(dir string) error) *Service {
	return &Service{mode: mode, paths: p, started: time.Now(), open: open}
}

// Info returns build, runtime and directory information.
func (s *Service) Info() contracts.SystemInfo {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return contracts.SystemInfo{
		Mode:  s.mode,
		Build: contracts.NewBuildInfo(buildinfo.Get()),
		Runtime: contracts.RuntimeInfo{
			NumCPU:        runtime.NumCPU(),
			NumGoroutine:  runtime.NumGoroutine(),
			AllocMB:       float64(m.Alloc) / bytesPerMB,
			SysMB:         float64(m.Sys) / bytesPerMB,
			GCCycles:      m.NumGC,
			UptimeSeconds: int64(time.Since(s.started).Seconds()),
		},
		Paths: contracts.PathsInfo{
			Config:      s.paths.Config,
			Data:        s.paths.Data,
			Logs:        s.paths.Logs,
			Diagnostics: s.paths.Diagnostics(),
		},
	}
}

// OpenFolder reveals one of the application folders.
func (s *Service) OpenFolder(target contracts.FolderTarget) error {
	var dir string
	switch target {
	case contracts.FolderLogs:
		dir = s.paths.Logs
	case contracts.FolderData:
		dir = s.paths.Data
	case contracts.FolderConfig:
		dir = s.paths.Config
	case contracts.FolderDiagnostics:
		dir = s.paths.Diagnostics()
	default:
		return apperr.Validation(fmt.Sprintf("unknown folder target %q", target))
	}
	if err := s.open(dir); err != nil {
		return apperr.Wrap(apperr.CodeUnavailable, "could not open folder", err)
	}
	return nil
}
