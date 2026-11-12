// Package binaries provides use cases for resolving, probing, downloading, and managing external CLI tools.
package binaries

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"app/internal/apperr"
	domainbinaries "app/internal/domain/binaries"
	"app/internal/runner"
)

// Service coordinates binary inspection, path overrides, and automated downloads.
type Service struct {
	repo     domainbinaries.Repository
	toolsDir string
	logger   *slog.Logger
	client   *http.Client
	// probeTimeout bounds one version probe (config binaries.probe_timeout).
	probeTimeout time.Duration
	dlSlot       chan struct{} // capacity 1: serialises downloads into toolsDir
}

// DefaultProbeTimeout mirrors config.Default().Binaries.ProbeTimeout for standalone use (tests, tools).
const DefaultProbeTimeout = 15 * time.Second

// Option customises a Service.
type Option func(*Service)

// WithProbeTimeout sets the version probe timeout; values <= 0 keep DefaultProbeTimeout.
func WithProbeTimeout(d time.Duration) Option {
	return func(s *Service) {
		if d > 0 {
			s.probeTimeout = d
		}
	}
}

// New creates a new binaries Service.
func New(repo domainbinaries.Repository, toolsDir string, logger *slog.Logger, opts ...Option) *Service {
	s := &Service{
		repo:         repo,
		toolsDir:     toolsDir,
		logger:       logger,
		client:       &http.Client{},
		probeTimeout: DefaultProbeTimeout,
		dlSlot:       make(chan struct{}, 1),
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// List inspects all registered binaries and returns their status.
func (s *Service) List(ctx context.Context) ([]domainbinaries.BinaryInfo, error) {
	allIDs := domainbinaries.AllIDs()
	infos := make([]domainbinaries.BinaryInfo, 0, len(allIDs))
	for _, id := range allIDs {
		info, err := s.Probe(ctx, id)
		if err != nil {
			s.logger.WarnContext(ctx, "failed to probe binary", "id", id, "err", err)
			spec := domainbinaries.Registry[id]
			_, canDownload := spec.CurrentDownloadSpec()
			infos = append(infos, domainbinaries.BinaryInfo{
				ID:          id,
				Name:        spec.Name,
				Description: spec.Description,
				Executable:  spec.ExecutableName(),
				Status:      domainbinaries.StatusInvalid,
				CanDownload: canDownload,
			})
			continue
		}
		infos = append(infos, info)
	}
	return infos, nil
}

// Get returns the status of a single binary.
func (s *Service) Get(ctx context.Context, id domainbinaries.BinaryID) (domainbinaries.BinaryInfo, error) {
	return s.Probe(ctx, id)
}

// SetCustomPath sets or clears the custom path for a binary and returns the updated status.
func (s *Service) SetCustomPath(ctx context.Context, id domainbinaries.BinaryID, customPath string) (domainbinaries.BinaryInfo, error) {
	if err := domainbinaries.ValidateID(id); err != nil {
		return domainbinaries.BinaryInfo{}, err
	}
	customPath = strings.TrimSpace(customPath)
	if err := s.repo.SetCustomPath(ctx, id, customPath); err != nil {
		return domainbinaries.BinaryInfo{}, err
	}
	return s.Probe(ctx, id)
}

// Resolve identifies the executable location and source for a binary.
func (s *Service) Resolve(ctx context.Context, id domainbinaries.BinaryID) (resolvedPath string, source domainbinaries.Source, customPath string, err error) {
	spec, ok := domainbinaries.Registry[id]
	if !ok {
		return "", domainbinaries.SourceNone, "", apperr.Validation(fmt.Sprintf("unknown binary %q", id))
	}

	// 1. Custom path configured by user
	customPath, err = s.repo.GetCustomPath(ctx, id)
	if err != nil {
		return "", domainbinaries.SourceNone, "", err
	}
	if customPath != "" {
		if fileExists(customPath) {
			return customPath, domainbinaries.SourceCustom, customPath, nil
		}
		// If specified custom path does not exist, note it but continue fallbacks
	}

	// 2. Application tools directory (%LOCALAPPDATA%/.../data/tools)
	if s.toolsDir != "" {
		toolsPath := filepath.Join(s.toolsDir, spec.ExecutableName())
		if fileExists(toolsPath) {
			return toolsPath, domainbinaries.SourceTools, customPath, nil
		}
	}

	// 3. Portable tools directory next to the executable
	if exe, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exe)
		portableTools := filepath.Join(exeDir, "tools", spec.ExecutableName())
		if fileExists(portableTools) {
			return portableTools, domainbinaries.SourceTools, customPath, nil
		}
	}

	// 4. System PATH
	if sysPath, err := exec.LookPath(spec.ExecutableBase); err == nil {
		return sysPath, domainbinaries.SourceSystem, customPath, nil
	}
	if sysPath, err := exec.LookPath(spec.ExecutableName()); err == nil {
		return sysPath, domainbinaries.SourceSystem, customPath, nil
	}

	return "", domainbinaries.SourceNone, customPath, nil
}

// Probe executes the binary with probe arguments to detect version and verify execution.
func (s *Service) Probe(ctx context.Context, id domainbinaries.BinaryID) (domainbinaries.BinaryInfo, error) {
	spec, ok := domainbinaries.Registry[id]
	if !ok {
		return domainbinaries.BinaryInfo{}, apperr.Validation(fmt.Sprintf("unknown binary %q", id))
	}

	_, canDownload := spec.CurrentDownloadSpec()
	resolvedPath, source, customPath, err := s.Resolve(ctx, id)
	if err != nil {
		return domainbinaries.BinaryInfo{}, err
	}

	info := domainbinaries.BinaryInfo{
		ID:           id,
		Name:         spec.Name,
		Description:  spec.Description,
		Executable:   spec.ExecutableName(),
		ResolvedPath: resolvedPath,
		CustomPath:   customPath,
		Source:       source,
		CanDownload:  canDownload,
	}

	if resolvedPath == "" {
		info.Status = domainbinaries.StatusMissing
		return info, nil
	}

	probeCtx, cancel := context.WithTimeout(ctx, s.probeTimeout)
	defer cancel()

	res, err := runner.RunWithOptions(probeCtx, resolvedPath, spec.ProbeArgs, runner.RunOptions{})
	if err != nil {
		s.logger.WarnContext(ctx, "binary probe execution failed", "id", id, "path", resolvedPath, "err", err)
		info.Status = domainbinaries.StatusInvalid
		return info, nil
	}

	output := res.Stdout + "\n" + res.Stderr
	version := spec.ExtractVersion(output)
	if version != "" {
		info.Version = version
		info.Status = domainbinaries.StatusAvailable
	} else if res.ExitCode == 0 {
		info.Status = domainbinaries.StatusAvailable
	} else {
		info.Status = domainbinaries.StatusInvalid
	}

	return info, nil
}

func fileExists(p string) bool {
	info, err := os.Stat(p)
	if err != nil {
		return false
	}
	return !info.IsDir()
}
