// Package binaries provides use cases for resolving, probing, downloading, and managing external CLI tools.
package binaries

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"app/internal/apperr"
	"app/internal/config"
	"app/internal/contracts"
	domainbinaries "app/internal/domain/binaries"
	"app/internal/lookup"
	"app/internal/progress"
	"app/internal/runner"
	"app/internal/safego"
)

// EventPublisher broadcasts realtime events to connected clients.
type EventPublisher interface {
	Publish(event string, data any)
}

// Service coordinates binary inspection, path overrides, and automated downloads.
type Service struct {
	repo     domainbinaries.Repository
	toolsDir string
	logger   *slog.Logger
	client   *http.Client
	// probeTimeout bounds one version probe (config binaries.probe_timeout).
	probeTimeout time.Duration
	dlSlot       chan struct{} // capacity 1: serialises downloads into toolsDir
	publisher    EventPublisher
	onProgress   func(contracts.BinaryProgress)
	cfg          *config.Config
	retryMax     *int
	retryDelay   *time.Duration
	manifest     map[domainbinaries.BinaryID]domainbinaries.BinarySpec
}

// DefaultProbeTimeout is the config default (binaries.probe_timeout), for standalone use.
func DefaultProbeTimeout() time.Duration {
	return config.Default(config.ModeDesktop).Binaries.ProbeTimeout
}

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

// WithHTTPClient sets the client used for downloads (the app's shared client); nil keeps a
// default client.
func WithHTTPClient(c *http.Client) Option {
	return func(s *Service) {
		if c != nil {
			s.client = c
		}
	}
}

// WithEventPublisher sets the event publisher used to broadcast EventBinariesProgress.
func WithEventPublisher(pub EventPublisher) Option {
	return func(s *Service) {
		s.publisher = pub
	}
}

// WithOnProgress registers a callback invoked whenever download progress is emitted.
func WithOnProgress(fn func(contracts.BinaryProgress)) Option {
	return func(s *Service) {
		s.onProgress = fn
	}
}

// WithConfig sets the app config for download retries and resume.
func WithConfig(c *config.Config) Option {
	return func(s *Service) {
		s.cfg = c
	}
}

// WithRetry sets custom download retry attempts and backoff delay.
func WithRetry(maxRetries int, delay time.Duration) Option {
	return func(s *Service) {
		s.retryMax = &maxRetries
		s.retryDelay = &delay
	}
}

// WithManifest injects an isolated binary specification map for this service instance,
// avoiding global domainbinaries.Registry mutations in concurrent tests.
func WithManifest(m map[domainbinaries.BinaryID]domainbinaries.BinarySpec) Option {
	return func(s *Service) {
		if len(m) == 0 {
			return
		}
		cp := make(map[domainbinaries.BinaryID]domainbinaries.BinarySpec, len(m))
		for k, v := range m {
			cp[k] = v
		}
		s.manifest = cp
	}
}

func (s *Service) spec(id domainbinaries.BinaryID) (domainbinaries.BinarySpec, bool) {
	if s.manifest != nil {
		if sp, ok := s.manifest[id]; ok {
			return sp, true
		}
	}
	sp, ok := domainbinaries.Registry[id]
	return sp, ok
}

func (s *Service) emitProgress(p contracts.BinaryProgress) {
	if s.publisher != nil {
		s.publisher.Publish(contracts.EventBinariesProgress, p)
	}
	if s.onProgress != nil {
		s.onProgress(p)
	}
}

func (s *Service) getRetryMax() int {
	if s.retryMax != nil {
		if *s.retryMax < 0 {
			return 0
		}
		return *s.retryMax
	}
	if s.cfg != nil {
		return s.cfg.BinariesDownloadRetryMax()
	}
	return config.Default(config.ModeDesktop).Binaries.DownloadRetryMax
}

func (s *Service) getRetryDelay() time.Duration {
	if s.retryDelay != nil {
		if *s.retryDelay <= 0 {
			return 0
		}
		return *s.retryDelay
	}
	if s.cfg != nil {
		return s.cfg.BinariesDownloadRetryDelay()
	}
	return config.Default(config.ModeDesktop).Binaries.DownloadRetryDelay
}

func (s *Service) isResumeEnabled() bool {
	if s.cfg != nil {
		return s.cfg.BinariesDownloadResume()
	}
	return config.Default(config.ModeDesktop).Binaries.DownloadResume
}

// New creates a new binaries Service.
func New(repo domainbinaries.Repository, toolsDir string, logger *slog.Logger, opts ...Option) *Service {
	s := &Service{
		repo:         repo,
		toolsDir:     toolsDir,
		logger:       logger,
		client:       &http.Client{},
		probeTimeout: DefaultProbeTimeout(),
		dlSlot:       make(chan struct{}, 1),
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// List inspects all registered binaries concurrently and returns their status in canonical order.
func (s *Service) List(ctx context.Context) ([]domainbinaries.BinaryInfo, error) {
	defer progress.KeepAlive(ctx)()
	allIDs := domainbinaries.AllIDs()
	infos := make([]domainbinaries.BinaryInfo, len(allIDs))
	var wg sync.WaitGroup
	for i, id := range allIDs {
		idx := i
		binID := id
		wg.Add(1)
		safego.Go("binaries.list.probe", func() {
			defer wg.Done()
			info, err := s.Probe(ctx, binID)
			if err != nil {
				s.logger.WarnContext(ctx, "failed to probe binary", "id", binID, "error", err)
				spec, _ := s.spec(binID)
				_, canDownload := spec.CurrentDownloadSpec()
				info = domainbinaries.BinaryInfo{
					ID:          binID,
					Name:        spec.Name,
					Description: spec.Description,
					Executable:  spec.ExecutableName(),
					Status:      domainbinaries.StatusInvalid,
					CanDownload: canDownload,
				}
			}
			infos[idx] = info
		})
	}
	wg.Wait()
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
	spec, ok := s.spec(id)
	if !ok {
		return "", domainbinaries.SourceNone, "", apperr.Validation(fmt.Sprintf("unknown binary %q", id))
	}

	customPath, err = s.repo.GetCustomPath(ctx, id)
	if err != nil {
		return "", domainbinaries.SourceNone, "", err
	}
	// Priority: user's custom path, app tools folder, portable tools next to the executable, PATH.
	// A custom path that no longer exists is kept (reported) but falls through to the others.
	candidates := []lookup.Candidate[domainbinaries.Source]{
		{Source: domainbinaries.SourceCustom, Find: lookup.File(customPath)},
	}
	if s.toolsDir != "" {
		candidates = append(candidates, lookup.Candidate[domainbinaries.Source]{
			Source: domainbinaries.SourceTools, Find: lookup.File(filepath.Join(s.toolsDir, spec.ExecutableName())),
		})
	}
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates, lookup.Candidate[domainbinaries.Source]{
			Source: domainbinaries.SourceTools, Find: lookup.File(filepath.Join(filepath.Dir(exe), "tools", spec.ExecutableName())),
		})
	}
	candidates = append(candidates, lookup.Candidate[domainbinaries.Source]{
		Source: domainbinaries.SourceSystem, Find: lookup.OnPath(spec.ExecutableBase, spec.ExecutableName()),
	})
	if path, src, ok := lookup.First(candidates...); ok {
		return path, src, customPath, nil
	}
	return "", domainbinaries.SourceNone, customPath, nil
}

// Probe executes the binary with probe arguments to detect version and verify execution.
func (s *Service) Probe(ctx context.Context, id domainbinaries.BinaryID) (domainbinaries.BinaryInfo, error) {
	spec, ok := s.spec(id)
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
		s.logger.WarnContext(ctx, "binary probe execution failed", "id", id, "path", resolvedPath, "error", err)
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
