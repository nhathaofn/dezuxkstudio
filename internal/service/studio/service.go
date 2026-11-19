// Package studio orchestrates DezuxkStudio's AI film production projects,
// Hybrid SQLite <-> workspace JSON synchronization, and Phase 1-3 stage execution.
package studio

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"app/internal/config"
	"app/internal/contracts"
	domainstudio "app/internal/domain/studio"
	"app/internal/engine"
	"app/internal/service/media"
)

// WorkflowRunner executes registered engine nodes shared by UI and agent.
type WorkflowRunner interface {
	RunNode(ctx context.Context, nodeType string, input json.RawMessage) (any, error)
}

// MediaRegistrar registers newly created studio media files in media_assets and authorizes roots.
type MediaRegistrar interface {
	RegisterAsset(ctx context.Context, params media.RegisterAssetParams) (*contracts.MediaAssetItem, error)
	AddAllowedRoot(root string)
}

// TextReadinessChecker reports whether at least one LLM text provider/model is usable for PurposeText.
type TextReadinessChecker interface {
	IsTextProviderReady(ctx context.Context) bool
}

// Options configures a studio.Service instance.
type Options struct {
	Repo          domainstudio.Repository
	Workflow      WorkflowRunner
	Media         MediaRegistrar
	VideoMedia    VideoQueueMedia
	TextReadiness TextReadinessChecker
	FileGuard     engine.FileGuard
	OpenDirectory func(dir string) error
	StudioConfig  config.StudioConfig
	ProjectsDir   string
	OnEvent       func(eventName string, data any)
	Logger        *slog.Logger
}

// Service manages projects, episodes, asset bibles, shots, workspace sync, and stage execution.
type Service struct {
	repo          domainstudio.Repository
	wf            WorkflowRunner
	media         MediaRegistrar
	videoMedia    VideoQueueMedia
	textReadiness TextReadinessChecker
	fileGuard     engine.FileGuard
	openDirectory func(dir string) error
	cfg           config.StudioConfig
	onEvent       func(eventName string, data any)
	log           *slog.Logger

	mu          sync.RWMutex
	projectsDir string

	runMu      sync.Mutex
	runningEps map[string]bool

	jobMu      sync.Mutex
	activeJobs map[string]*activeJobEntry
	pollErrors map[string]int

	waitMu        sync.Mutex
	waitListeners map[chan struct{}]struct{}

	wakeCh chan struct{}
}

func New(opts Options) *Service {
	log := opts.Logger
	if log == nil {
		log = slog.Default()
	}
	cfg := config.ResolveStudioConfig(opts.StudioConfig)

	projectsDir := strings.TrimSpace(opts.ProjectsDir)
	if projectsDir != "" {
		projectsDir = filepath.Clean(projectsDir)
		if err := os.MkdirAll(projectsDir, 0o755); err != nil {
			log.WarnContext(context.Background(), "studio: failed to create initial projects directory", "error", err)
		}
	}

	return &Service{
		repo:          opts.Repo,
		wf:            opts.Workflow,
		media:         opts.Media,
		videoMedia:    opts.VideoMedia,
		textReadiness: opts.TextReadiness,
		fileGuard:     opts.FileGuard,
		openDirectory: opts.OpenDirectory,
		cfg:           cfg,
		onEvent:       opts.OnEvent,
		log:           log,
		projectsDir:   projectsDir,
		runningEps:    make(map[string]bool),
		activeJobs:    make(map[string]*activeJobEntry),
		pollErrors:    make(map[string]int),
		waitListeners: make(map[chan struct{}]struct{}),
		wakeCh:        make(chan struct{}, 1),
	}
}

// ProjectsDir returns the currently configured workspace projects root directory.
func (s *Service) ProjectsDir() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.projectsDir
}

// SetFileGuard configures the FileGuard applied to background queue workers.
func (s *Service) SetFileGuard(fn engine.FileGuard) {
	s.mu.Lock()
	s.fileGuard = fn
	s.mu.Unlock()
}

// SetProjectsDir updates the active projects root when the user changes WorkspaceRoot.
func (s *Service) SetProjectsDir(dir string) {
	trimmed := strings.TrimSpace(dir)
	if trimmed == "" {
		return
	}
	clean := filepath.Clean(trimmed)
	if err := os.MkdirAll(clean, 0o755); err != nil {
		s.log.WarnContext(context.Background(), "studio: failed to ensure updated projects directory", "error", err)
	}

	s.mu.Lock()
	s.projectsDir = clean
	s.mu.Unlock()

	if s.media != nil {
		s.media.AddAllowedRoot(clean)
	}
}

func (s *Service) emit(eventName string, data any) {
	if s.onEvent != nil {
		s.onEvent(eventName, data)
	}
}

// ListProjects lists all projects ordered by recent updates.
