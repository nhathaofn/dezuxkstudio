// Package system implements host/runtime introspection and OS integration use cases.
package system

import (
	"fmt"
	"runtime"
	"sync"
	"time"

	"app/internal/apperr"
	"app/internal/buildinfo"
	"app/internal/config"
	"app/internal/contracts"
	"app/internal/paths"
)

const bytesPerMB = 1 << 20

// WorkspaceTreeFunc resolves the active workspace tree dynamically.
type WorkspaceTreeFunc func() paths.WorkspaceTree

// Option configures Service.
type Option func(*Service)

// WithWorkspaceTreeFunc sets the dynamic workspace tree resolver.
func WithWorkspaceTreeFunc(fn WorkspaceTreeFunc) Option {
	return func(s *Service) {
		s.treeFn = fn
	}
}

// Service exposes system information and folder shortcuts.
type Service struct {
	mode    config.Mode
	paths   paths.Paths
	started time.Time
	open    func(dir string) error

	mu     sync.RWMutex
	treeFn WorkspaceTreeFunc
}

// New creates the service; open reveals a directory in the OS file manager.
func New(mode config.Mode, p paths.Paths, open func(dir string) error, opts ...Option) *Service {
	s := &Service{mode: mode, paths: p, started: time.Now(), open: open}
	for _, opt := range opts {
		if opt != nil {
			opt(s)
		}
	}
	return s
}

// SetWorkspaceTreeFunc updates the dynamic workspace tree resolver.
func (s *Service) SetWorkspaceTreeFunc(fn WorkspaceTreeFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.treeFn = fn
}

func (s *Service) workspaceTree() paths.WorkspaceTree {
	s.mu.RLock()
	fn := s.treeFn
	s.mu.RUnlock()
	if fn != nil {
		tree := fn()
		if tree.Root != "" {
			return tree
		}
	}
	return paths.ResolveWorkspaceTree(s.paths, "", "")
}

// Info returns build, runtime and directory information.
func (s *Service) Info() contracts.SystemInfo {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	tree := s.workspaceTree()
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
			Config:                 s.paths.Config,
			Data:                   s.paths.Data,
			Logs:                   s.paths.Logs,
			Diagnostics:            s.paths.Diagnostics(),
			WorkspaceRoot:          tree.Root,
			SuggestedWorkspaceRoot: paths.SuggestedWorkspaceRoot(buildinfo.ProductName),
			MediaDir:               tree.MediaDir,
			WatermarkDir:           tree.WatermarkDir,
			AgentWorkspace:         tree.WorkspaceDir,
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
	case contracts.FolderAgentCustomizations:
		dir = s.paths.AgentCustomizations()
	case contracts.FolderWorkspaceRoot:
		dir = s.workspaceTree().Root
	default:
		return apperr.Validation(fmt.Sprintf("unknown folder target %q", target))
	}
	if err := s.open(dir); err != nil {
		return apperr.Wrap(apperr.CodeUnavailable, "could not open folder", err)
	}
	return nil
}
