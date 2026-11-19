// Package studio orchestrates DezuxkStudio's AI film production projects,
// Hybrid SQLite <-> workspace JSON synchronization, and Phase 1-3 stage execution.
package studio

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"app/internal/apperr"
	"app/internal/config"
	"app/internal/contracts"
	domainmedia "app/internal/domain/mediagen"
	domainstudio "app/internal/domain/studio"
	"app/internal/engine"
	"app/internal/fsutil"
	"app/internal/progress"
	"app/internal/safego"
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

// Options configures a studio.Service instance.
type Options struct {
	Repo          domainstudio.Repository
	Workflow      WorkflowRunner
	Media         MediaRegistrar
	VideoMedia    VideoQueueMedia
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

	wakeCh chan struct{}
}

type episodeDiskMeta struct {
	Episode           domainstudio.Episode `json:"episode"`
	FinalVideoPath    string               `json:"final_video_path,omitempty"`
	FinalVideoAssetID string               `json:"final_video_asset_id,omitempty"`
}

// New constructs a Studio Service.
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
		fileGuard:     opts.FileGuard,
		openDirectory: opts.OpenDirectory,
		cfg:           cfg,
		onEvent:       opts.OnEvent,
		log:           log,
		projectsDir:   projectsDir,
		runningEps:    make(map[string]bool),
		activeJobs:    make(map[string]*activeJobEntry),
		pollErrors:    make(map[string]int),
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
func (s *Service) ListProjects(ctx context.Context) (contracts.StudioProjectListResult, error) {
	if s.repo == nil {
		return contracts.StudioProjectListResult{}, apperr.Unavailable("studio repository is unavailable").
			WithMsg("errors.studio.service_unavailable", nil)
	}
	list, err := s.repo.ListProjects(ctx)
	if err != nil {
		return contracts.StudioProjectListResult{}, err
	}
	out := make([]contracts.StudioProject, len(list))
	for i := range list {
		out[i] = projectToDTO(list[i])
	}
	return contracts.StudioProjectListResult{
		Projects:    out,
		ProjectsDir: s.ProjectsDir(),
	}, nil
}

// CreateProject creates a new project, initializes its workspace folder & JSON files,
// and creates an initial episode.
func (s *Service) CreateProject(ctx context.Context, p contracts.StudioProjectCreatePayload) (contracts.StudioProjectDetailResult, error) {
	if s.repo == nil {
		return contracts.StudioProjectDetailResult{}, apperr.Unavailable("studio repository is unavailable").
			WithMsg("errors.studio.service_unavailable", nil)
	}
	if err := p.Validate(); err != nil {
		return contracts.StudioProjectDetailResult{}, err
	}

	baseSlug := domainstudio.Slugify(p.Slug)
	if strings.TrimSpace(p.Slug) == "" {
		baseSlug = domainstudio.Slugify(p.Title)
	}
	slug, err := s.allocateUniqueSlug(ctx, baseSlug)
	if err != nil {
		return contracts.StudioProjectDetailResult{}, err
	}

	projRoot := ""
	if pDir := s.ProjectsDir(); pDir != "" {
		projRoot = filepath.Join(pDir, slug)
		if err := s.ensureGuardedProjectDirs(ctx, projRoot); err != nil {
			return contracts.StudioProjectDetailResult{}, err
		}
		if s.media != nil {
			s.media.AddAllowedRoot(projRoot)
		}
	}

	proj, err := s.repo.CreateProject(ctx, domainstudio.Project{
		Slug:               slug,
		Title:              p.Title,
		Genre:              p.Genre,
		ProductionMode:     p.ProductionMode,
		AspectRatio:        p.AspectRatio,
		StylePrompt:        p.StylePrompt,
		RootDir:            projRoot,
		AutoApproveBatches: p.AutoApproveBatches,
		DefaultTextModel:   p.DefaultTextModel,
		DefaultImageModel:  p.DefaultImageModel,
		DefaultVideoModel:  p.DefaultVideoModel,
		DefaultTTSModel:    p.DefaultTTSModel,
	})
	if err != nil {
		return contracts.StudioProjectDetailResult{}, err
	}

	s.warnDiskMirror(ctx, "project.json", s.persistProjectToDisk(ctx, proj, nil))

	epTitle := strings.TrimSpace(p.InitialEpisodeTitle)
	if epTitle == "" {
		epTitle = "Episode 1"
	}
	epDetail, err := s.createEpisodeInternal(ctx, proj, contracts.StudioEpisodeCreatePayload{
		ProjectID: proj.ID,
		Code:      "ep01",
		Title:     epTitle,
	})
	if err != nil {
		return contracts.StudioProjectDetailResult{}, err
	}

	dtoProj := projectToDTO(proj)
	s.emit(contracts.EventStudioProjectChanged, contracts.StudioProjectChangedEvent{
		ProjectID: proj.ID,
		Action:    contracts.ProjectActionCreated,
		Project:   &dtoProj,
	})

	return contracts.StudioProjectDetailResult{
		Project:       dtoProj,
		Episodes:      []contracts.StudioEpisode{epDetail.Episode},
		Assets:        []contracts.StudioAsset{},
		ActiveEpisode: &epDetail,
	}, nil
}

// GetProject fetches project metadata, episodes, assets, and first episode detail.
func (s *Service) GetProject(ctx context.Context, idOrSlug string) (contracts.StudioProjectDetailResult, error) {
	if s.repo == nil {
		return contracts.StudioProjectDetailResult{}, apperr.Unavailable("studio repository is unavailable").
			WithMsg("errors.studio.service_unavailable", nil)
	}
	proj, err := s.repo.GetProject(ctx, strings.TrimSpace(idOrSlug))
	if err != nil {
		return contracts.StudioProjectDetailResult{}, err
	}
	episodes, err := s.repo.ListEpisodes(ctx, proj.ID)
	if err != nil {
		return contracts.StudioProjectDetailResult{}, err
	}
	assets, err := s.repo.ListAssets(ctx, proj.ID)
	if err != nil {
		return contracts.StudioProjectDetailResult{}, err
	}

	epDTOs := make([]contracts.StudioEpisode, len(episodes))
	for i := range episodes {
		epDTOs[i] = episodeToDTO(episodes[i])
	}
	assetDTOs := assetsToDTO(assets)

	var activeEp *contracts.StudioEpisodeDetailResult
	if len(episodes) > 0 {
		detail, derr := s.GetEpisode(ctx, episodes[0].ID)
		if derr == nil {
			activeEp = &detail
		} else {
			s.log.WarnContext(ctx, "studio: failed to load active episode detail", "episode_id", episodes[0].ID, "error", derr)
		}
	}

	return contracts.StudioProjectDetailResult{
		Project:       projectToDTO(proj),
		Episodes:      epDTOs,
		Assets:        assetDTOs,
		ActiveEpisode: activeEp,
	}, nil
}

// UpdateProject updates mutable project settings and syncs project.json.
func (s *Service) UpdateProject(ctx context.Context, p contracts.StudioProjectUpdatePayload) (contracts.StudioProjectDetailResult, error) {
	if s.repo == nil {
		return contracts.StudioProjectDetailResult{}, apperr.Unavailable("studio repository is unavailable").
			WithMsg("errors.studio.service_unavailable", nil)
	}
	if err := p.Validate(); err != nil {
		return contracts.StudioProjectDetailResult{}, err
	}
	proj, err := s.repo.GetProject(ctx, strings.TrimSpace(p.ID))
	if err != nil {
		return contracts.StudioProjectDetailResult{}, err
	}
	if p.Title != nil {
		proj.Title = strings.TrimSpace(*p.Title)
	}
	if p.Genre != nil {
		proj.Genre = *p.Genre
	}
	if p.ProductionMode != nil {
		proj.ProductionMode = *p.ProductionMode
	}
	if p.AspectRatio != nil {
		proj.AspectRatio = strings.TrimSpace(*p.AspectRatio)
	}
	if p.StylePrompt != nil {
		proj.StylePrompt = strings.TrimSpace(*p.StylePrompt)
	}
	if p.AutoApproveBatches != nil {
		proj.AutoApproveBatches = *p.AutoApproveBatches
	}
	if p.DefaultTextModel != nil {
		proj.DefaultTextModel = strings.TrimSpace(*p.DefaultTextModel)
	}
	if p.DefaultImageModel != nil {
		proj.DefaultImageModel = strings.TrimSpace(*p.DefaultImageModel)
	}
	if p.DefaultVideoModel != nil {
		proj.DefaultVideoModel = strings.TrimSpace(*p.DefaultVideoModel)
	}
	if p.DefaultTTSModel != nil {
		proj.DefaultTTSModel = strings.TrimSpace(*p.DefaultTTSModel)
	}

	updated, err := s.repo.UpdateProject(ctx, proj)
	if err != nil {
		return contracts.StudioProjectDetailResult{}, err
	}
	assets, aerr := s.repo.ListAssets(ctx, updated.ID)
	if aerr != nil {
		s.log.WarnContext(ctx, "studio: failed to list assets after project update", "project_id", updated.ID, "error", aerr)
	}
	s.warnDiskMirror(ctx, "project.json", s.persistProjectToDisk(ctx, updated, assets))

	dtoProj := projectToDTO(updated)
	s.emit(contracts.EventStudioProjectChanged, contracts.StudioProjectChangedEvent{
		ProjectID: updated.ID,
		Action:    contracts.ProjectActionUpdated,
		Project:   &dtoProj,
	})
	return s.GetProject(ctx, updated.ID)
}

// DeleteProject removes the project from SQLite and optionally removes its folder when safely inside ProjectsDir.
func (s *Service) DeleteProject(ctx context.Context, p contracts.StudioProjectDeletePayload) error {
	if s.repo == nil {
		return apperr.Unavailable("studio repository is unavailable").
			WithMsg("errors.studio.service_unavailable", nil)
	}
	if err := p.Validate(); err != nil {
		return err
	}
	proj, err := s.repo.GetProject(ctx, strings.TrimSpace(p.ID))
	if err != nil {
		return err
	}
	if p.DeleteFiles && proj.RootDir != "" {
		if pDir := s.ProjectsDir(); pDir != "" && isDirectOrSubpath(pDir, proj.RootDir) {
			if cerr := engine.CheckFile(ctx, proj.RootDir, true); cerr != nil {
				return cerr
			}
			s.cancelEpisodeActiveJobs(proj.ID, "")
			s.waitProjectActiveJobs(proj.ID, time.Second)
			if rerr := removeDirWithRetry(proj.RootDir); rerr != nil {
				return apperr.Wrap(apperr.CodeUnavailable, "Failed to remove deleted project directory", rerr).
					WithMsg("errors.studio.io_failed", nil)
			}
		} else {
			s.cancelEpisodeActiveJobs(proj.ID, "")
			s.waitProjectActiveJobs(proj.ID, time.Second)
		}
	} else {
		s.cancelEpisodeActiveJobs(proj.ID, "")
		s.waitProjectActiveJobs(proj.ID, time.Second)
	}
	if err := s.repo.DeleteProject(ctx, proj.ID); err != nil {
		return err
	}
	s.emit(contracts.EventStudioProjectChanged, contracts.StudioProjectChangedEvent{
		ProjectID: proj.ID,
		Action:    contracts.ProjectActionDeleted,
	})
	return nil
}

// CreateEpisode creates a new episode in a project and initializes its workspace files.
func (s *Service) CreateEpisode(ctx context.Context, p contracts.StudioEpisodeCreatePayload) (contracts.StudioEpisodeDetailResult, error) {
	if s.repo == nil {
		return contracts.StudioEpisodeDetailResult{}, apperr.Unavailable("studio repository is unavailable").
			WithMsg("errors.studio.service_unavailable", nil)
	}
	if err := p.Validate(); err != nil {
		return contracts.StudioEpisodeDetailResult{}, err
	}
	proj, err := s.repo.GetProject(ctx, strings.TrimSpace(p.ProjectID))
	if err != nil {
		return contracts.StudioEpisodeDetailResult{}, err
	}
	detail, err := s.createEpisodeInternal(ctx, proj, p)
	if err != nil {
		return contracts.StudioEpisodeDetailResult{}, err
	}
	s.emit(contracts.EventStudioEpisodeUpdated, contracts.StudioEpisodeUpdatedEvent{
		ProjectID: proj.ID,
		EpisodeID: detail.Episode.ID,
		Stage:     detail.Episode.Stage,
		Reason:    "episode_created",
	})
	return detail, nil
}

func (s *Service) createEpisodeInternal(ctx context.Context, proj domainstudio.Project, p contracts.StudioEpisodeCreatePayload) (contracts.StudioEpisodeDetailResult, error) {
	existing, err := s.repo.ListEpisodes(ctx, proj.ID)
	if err != nil {
		return contracts.StudioEpisodeDetailResult{}, err
	}
	code := strings.TrimSpace(p.Code)
	if code == "" {
		code = fmt.Sprintf("ep%02d", len(existing)+1)
	}
	code = domainstudio.Slugify(code)
	used := map[string]bool{}
	for _, e := range existing {
		used[e.Code] = true
	}
	for used[code] {
		code = fmt.Sprintf("%s-%d", code, len(used)+1)
	}
	title := strings.TrimSpace(p.Title)
	if title == "" {
		title = strings.ToUpper(code)
	}

	epRoot := s.resolveEpisodeRoot(proj, code)
	var rawPath, scriptJSONPath, assetsJSONPath, shotsJSONPath, timelineJSONPath string
	if epRoot != "" {
		if err := s.ensureGuardedEpisodeDirs(ctx, epRoot); err != nil {
			return contracts.StudioEpisodeDetailResult{}, err
		}
		rawPath = filepath.Join(epRoot, "script", "raw_screenplay.txt")
		scriptJSONPath = filepath.Join(epRoot, "script", "normalized_script.json")
		assetsJSONPath = filepath.Join(epRoot, "assets", "asset_bible.json")
		shotsJSONPath = filepath.Join(epRoot, "storyboard", "shots.json")
		timelineJSONPath = filepath.Join(epRoot, "timeline", "timeline.json")
	}

	ep, err := s.repo.CreateEpisode(ctx, domainstudio.Episode{
		ProjectID:        proj.ID,
		Code:             code,
		Title:            title,
		Stage:            domainstudio.StageScript,
		RawScriptPath:    rawPath,
		ScriptJSONPath:   scriptJSONPath,
		AssetsJSONPath:   assetsJSONPath,
		ShotsJSONPath:    shotsJSONPath,
		TimelineJSONPath: timelineJSONPath,
	})
	if err != nil {
		return contracts.StudioEpisodeDetailResult{}, err
	}

	if epRoot != "" {
		s.warnDiskMirror(ctx, "episode.json", s.persistEpisodeToDisk(ctx, proj, ep, "", nil, nil, "", ""))
	}
	return s.GetEpisode(ctx, ep.ID)
}

// GetEpisode returns full episode details including script, asset bible, shots, and latest export.
func (s *Service) GetEpisode(ctx context.Context, episodeID string) (contracts.StudioEpisodeDetailResult, error) {
	if s.repo == nil {
		return contracts.StudioEpisodeDetailResult{}, apperr.Unavailable("studio repository is unavailable").
			WithMsg("errors.studio.service_unavailable", nil)
	}
	ep, err := s.repo.GetEpisode(ctx, strings.TrimSpace(episodeID))
	if err != nil {
		return contracts.StudioEpisodeDetailResult{}, err
	}
	proj, err := s.repo.GetProject(ctx, ep.ProjectID)
	if err != nil {
		return contracts.StudioEpisodeDetailResult{}, err
	}
	assets, err := s.repo.ListAssets(ctx, proj.ID)
	if err != nil {
		return contracts.StudioEpisodeDetailResult{}, err
	}
	shots, err := s.repo.ListShots(ctx, ep.ID)
	if err != nil {
		return contracts.StudioEpisodeDetailResult{}, err
	}

	epRoot := s.resolveEpisodeRoot(proj, ep.Code)
	rawScript := s.readGuardedStringFile(ctx, epRoot, ep.RawScriptPath)
	normScript := s.readGuardedNormalizedScriptDTO(ctx, epRoot, ep.ScriptJSONPath)
	var finalPath, finalAssetID string
	if meta := s.readEpisodeDiskMeta(ctx, epRoot); meta != nil {
		finalPath = meta.FinalVideoPath
		finalAssetID = meta.FinalVideoAssetID
	}

	return contracts.StudioEpisodeDetailResult{
		Project:           projectToDTO(proj),
		Episode:           episodeToDTO(ep),
		Assets:            assetsToDTO(assets),
		Shots:             shotsToDTO(shots),
		RawScript:         rawScript,
		NormalizedScript:  normScript,
		FinalVideoPath:    finalPath,
		FinalVideoAssetID: finalAssetID,
	}, nil
}

// SaveScript saves raw screenplay text and/or edited normalized script JSON for an episode.
func (s *Service) SaveScript(ctx context.Context, p contracts.StudioEpisodeSaveScriptPayload) (contracts.StudioEpisodeDetailResult, error) {
	if s.repo == nil {
		return contracts.StudioEpisodeDetailResult{}, apperr.Unavailable("studio repository is unavailable").
			WithMsg("errors.studio.service_unavailable", nil)
	}
	if err := p.Validate(); err != nil {
		return contracts.StudioEpisodeDetailResult{}, err
	}
	ep, err := s.repo.GetEpisode(ctx, strings.TrimSpace(p.EpisodeID))
	if err != nil {
		return contracts.StudioEpisodeDetailResult{}, err
	}
	proj, err := s.repo.GetProject(ctx, ep.ProjectID)
	if err != nil {
		return contracts.StudioEpisodeDetailResult{}, err
	}

	epRoot := s.resolveEpisodeRoot(proj, ep.Code)
	if epRoot != "" {
		if err := s.ensureGuardedEpisodeDirs(ctx, epRoot); err != nil {
			return contracts.StudioEpisodeDetailResult{}, err
		}
		if ep.RawScriptPath == "" {
			ep.RawScriptPath = filepath.Join(epRoot, "script", "raw_screenplay.txt")
		}
		if ep.ScriptJSONPath == "" {
			ep.ScriptJSONPath = filepath.Join(epRoot, "script", "normalized_script.json")
		}
	}

	rawText := s.readGuardedStringFile(ctx, epRoot, ep.RawScriptPath)
	if p.RawScript != nil {
		rawText = *p.RawScript
		if ep.RawScriptPath != "" {
			if err := s.writeGuardedFile(ctx, epRoot, ep.RawScriptPath, []byte(rawText), 0o644); err != nil {
				return contracts.StudioEpisodeDetailResult{}, err
			}
		}
	}

	var normDomain *domainstudio.NormalizedScript
	if p.NormalizedScript != nil {
		converted := normalizedScriptFromDTO(*p.NormalizedScript)
		normDomain = &converted
		if ep.ScriptJSONPath != "" {
			data, merr := json.MarshalIndent(converted, "", "  ")
			if merr != nil {
				return contracts.StudioEpisodeDetailResult{}, apperr.Internal(merr)
			}
			if err := s.writeGuardedFile(ctx, epRoot, ep.ScriptJSONPath, append(data, '\n'), 0o644); err != nil {
				return contracts.StudioEpisodeDetailResult{}, err
			}
		}
	}
	if p.Stage != nil {
		ep.Stage = *p.Stage
	}
	updatedEp, err := s.repo.UpdateEpisode(ctx, ep)
	if err != nil {
		return contracts.StudioEpisodeDetailResult{}, err
	}

	shots, serr := s.repo.ListShots(ctx, updatedEp.ID)
	if serr != nil {
		s.log.WarnContext(ctx, "studio: failed to list shots after script save", "episode_id", updatedEp.ID, "error", serr)
	}
	meta := s.readEpisodeDiskMeta(ctx, epRoot)
	var finalPath, finalAssetID string
	if meta != nil {
		finalPath = meta.FinalVideoPath
		finalAssetID = meta.FinalVideoAssetID
	}
	s.warnDiskMirror(ctx, "episode.json", s.persistEpisodeToDisk(ctx, proj, updatedEp, rawText, normDomain, shots, finalPath, finalAssetID))

	s.emit(contracts.EventStudioEpisodeUpdated, contracts.StudioEpisodeUpdatedEvent{
		ProjectID: proj.ID,
		EpisodeID: updatedEp.ID,
		Stage:     updatedEp.Stage,
		Reason:    "script_saved",
	})
	return s.GetEpisode(ctx, updatedEp.ID)
}

// UpsertAsset creates, updates, or deletes a project asset bible entry and syncs asset_bible.json.
func (s *Service) UpsertAsset(ctx context.Context, p contracts.StudioAssetUpsertPayload) (contracts.StudioAssetUpsertResult, error) {
	if s.repo == nil {
		return contracts.StudioAssetUpsertResult{}, apperr.Unavailable("studio repository is unavailable").
			WithMsg("errors.studio.service_unavailable", nil)
	}
	if err := p.Validate(); err != nil {
		return contracts.StudioAssetUpsertResult{}, err
	}

	if p.Delete {
		existing, err := s.repo.GetAsset(ctx, strings.TrimSpace(p.ID))
		if err != nil {
			return contracts.StudioAssetUpsertResult{}, err
		}
		if err := s.repo.DeleteAsset(ctx, existing.ID); err != nil {
			return contracts.StudioAssetUpsertResult{}, err
		}
		assets, aerr := s.repo.ListAssets(ctx, existing.ProjectID)
		if aerr != nil {
			s.log.WarnContext(ctx, "studio: failed to list assets after delete", "project_id", existing.ProjectID, "error", aerr)
		}
		if proj, perr := s.repo.GetProject(ctx, existing.ProjectID); perr == nil {
			s.warnDiskMirror(ctx, "asset_bible.json", s.persistProjectToDisk(ctx, proj, assets))
			dtoProj := projectToDTO(proj)
			s.emit(contracts.EventStudioProjectChanged, contracts.StudioProjectChangedEvent{
				ProjectID: proj.ID,
				Action:    contracts.ProjectActionUpdated,
				Project:   &dtoProj,
			})
		}
		return contracts.StudioAssetUpsertResult{Assets: assetsToDTO(assets)}, nil
	}

	proj, err := s.repo.GetProject(ctx, strings.TrimSpace(p.ProjectID))
	if err != nil {
		return contracts.StudioAssetUpsertResult{}, err
	}
	if primary := strings.TrimSpace(p.PrimaryPath); primary != "" {
		if cerr := engine.CheckFile(ctx, primary, false); cerr != nil {
			return contracts.StudioAssetUpsertResult{}, cerr
		}
	}
	kind := p.Kind
	if kind == "" {
		kind = domainstudio.AssetCharacter
	}
	saved, err := s.repo.UpsertAsset(ctx, domainstudio.AssetEntry{
		ID:             strings.TrimSpace(p.ID),
		ProjectID:      proj.ID,
		Kind:           kind,
		Name:           p.Name,
		CodeTag:        p.CodeTag,
		Description:    p.Description,
		Prompt:         p.Prompt,
		PrimaryAssetID: p.PrimaryAssetID,
		PrimaryPath:    p.PrimaryPath,
		VoiceProvider:  p.VoiceProvider,
		VoiceID:        p.VoiceID,
		VoiceStyle:     p.VoiceStyle,
	})
	if err != nil {
		return contracts.StudioAssetUpsertResult{}, err
	}
	assets, aerr := s.repo.ListAssets(ctx, proj.ID)
	if aerr != nil {
		s.log.WarnContext(ctx, "studio: failed to list assets after upsert", "project_id", proj.ID, "error", aerr)
	}
	s.warnDiskMirror(ctx, "asset_bible.json", s.persistProjectToDisk(ctx, proj, assets))

	dtoProj := projectToDTO(proj)
	s.emit(contracts.EventStudioProjectChanged, contracts.StudioProjectChangedEvent{
		ProjectID: proj.ID,
		Action:    contracts.ProjectActionUpdated,
		Project:   &dtoProj,
	})
	dtoAsset := assetToDTO(saved)
	return contracts.StudioAssetUpsertResult{Asset: &dtoAsset, Assets: assetsToDTO(assets)}, nil
}

// UpsertShot creates or updates an episode shot and syncs storyboard/shots.json.
func (s *Service) UpsertShot(ctx context.Context, p contracts.StudioShotUpsertPayload) (contracts.StudioShotUpsertResult, error) {
	if s.repo == nil {
		return contracts.StudioShotUpsertResult{}, apperr.Unavailable("studio repository is unavailable").
			WithMsg("errors.studio.service_unavailable", nil)
	}
	if err := p.Validate(); err != nil {
		return contracts.StudioShotUpsertResult{}, err
	}
	ep, err := s.repo.GetEpisode(ctx, strings.TrimSpace(p.EpisodeID))
	if err != nil {
		return contracts.StudioShotUpsertResult{}, err
	}
	proj, err := s.repo.GetProject(ctx, ep.ProjectID)
	if err != nil {
		return contracts.StudioShotUpsertResult{}, err
	}
	for _, ptr := range []*string{p.KeyframePath, p.VideoPath, p.AudioPath} {
		if ptr != nil && strings.TrimSpace(*ptr) != "" {
			if cerr := engine.CheckFile(ctx, strings.TrimSpace(*ptr), false); cerr != nil {
				return contracts.StudioShotUpsertResult{}, cerr
			}
		}
	}

	var cur domainstudio.Shot
	if strings.TrimSpace(p.ID) != "" {
		if existing, gerr := s.repo.GetShot(ctx, strings.TrimSpace(p.ID)); gerr == nil {
			cur = existing
		}
	}
	cur.EpisodeID = ep.ID
	if strings.TrimSpace(p.ID) != "" {
		cur.ID = strings.TrimSpace(p.ID)
	}
	if p.Seq != nil {
		cur.Seq = *p.Seq
	} else if cur.ID == "" {
		shots, serr := s.repo.ListShots(ctx, ep.ID)
		if serr != nil {
			s.log.WarnContext(ctx, "studio: failed to list shots for default seq", "episode_id", ep.ID, "error", serr)
		}
		cur.Seq = len(shots) + 1
	}
	if p.SceneCode != "" {
		cur.SceneCode = p.SceneCode
	}
	if p.Title != "" {
		cur.Title = p.Title
	}
	if p.ShotMode != "" {
		cur.ShotMode = p.ShotMode
	}
	if p.DurationSec != nil {
		cur.DurationSec = *p.DurationSec
	}
	if p.ImagePrompt != nil {
		cur.ImagePrompt = *p.ImagePrompt
	}
	if p.VideoPrompt != nil {
		cur.VideoPrompt = *p.VideoPrompt
	}
	if p.NarrationText != nil {
		cur.NarrationText = *p.NarrationText
	}
	if p.Dialogues != nil {
		cur.Dialogues = dialoguesFromDTO(p.Dialogues)
	}
	if p.RefAssetTags != nil {
		cur.RefAssetTags = append([]string(nil), p.RefAssetTags...)
	}
	if p.KeyframeAssetID != nil {
		cur.KeyframeAssetID = *p.KeyframeAssetID
	}
	if p.KeyframePath != nil {
		cur.KeyframePath = *p.KeyframePath
	}
	if p.VideoAssetID != nil {
		cur.VideoAssetID = *p.VideoAssetID
	}
	if p.VideoPath != nil {
		cur.VideoPath = *p.VideoPath
	}
	if p.AudioAssetID != nil {
		cur.AudioAssetID = *p.AudioAssetID
	}
	if p.AudioPath != nil {
		cur.AudioPath = *p.AudioPath
	}
	if p.Status != nil {
		cur.Status = *p.Status
	}
	if p.ErrorText != nil {
		cur.ErrorText = *p.ErrorText
	}

	if err := cur.ValidateWithLimits(s.cfg.MaxShotDurationSec, s.cfg.DefaultShotDurationSec); err != nil {
		return contracts.StudioShotUpsertResult{}, err
	}
	saved, err := s.repo.UpsertShot(ctx, cur)
	if err != nil {
		return contracts.StudioShotUpsertResult{}, err
	}
	shots, serr := s.repo.ListShots(ctx, ep.ID)
	if serr != nil {
		s.log.WarnContext(ctx, "studio: failed to list shots after upsert", "episode_id", ep.ID, "error", serr)
	}
	s.warnDiskMirror(ctx, "shots.json", s.syncEpisodeShotsToDisk(ctx, proj, ep, shots))

	s.emit(contracts.EventStudioEpisodeUpdated, contracts.StudioEpisodeUpdatedEvent{
		ProjectID: proj.ID,
		EpisodeID: ep.ID,
		Stage:     ep.Stage,
		Reason:    "shot_updated",
	})
	return contracts.StudioShotUpsertResult{Shot: shotToDTO(saved), Shots: shotsToDTO(shots)}, nil
}

// ReorderShots reorders storyboard shots in SQLite and syncs storyboard/shots.json.
func (s *Service) ReorderShots(ctx context.Context, p contracts.StudioShotReorderPayload) (contracts.StudioShotListResult, error) {
	if s.repo == nil {
		return contracts.StudioShotListResult{}, apperr.Unavailable("studio repository is unavailable").
			WithMsg("errors.studio.service_unavailable", nil)
	}
	if err := p.Validate(); err != nil {
		return contracts.StudioShotListResult{}, err
	}
	ep, err := s.repo.GetEpisode(ctx, strings.TrimSpace(p.EpisodeID))
	if err != nil {
		return contracts.StudioShotListResult{}, err
	}
	proj, err := s.repo.GetProject(ctx, ep.ProjectID)
	if err != nil {
		return contracts.StudioShotListResult{}, err
	}

	reordered, err := s.repo.ReorderShots(ctx, ep.ID, p.ShotIDs)
	if err != nil {
		return contracts.StudioShotListResult{}, err
	}
	s.warnDiskMirror(ctx, "shots.json", s.syncEpisodeShotsToDisk(ctx, proj, ep, reordered))
	s.emit(contracts.EventStudioEpisodeUpdated, contracts.StudioEpisodeUpdatedEvent{
		ProjectID: proj.ID,
		EpisodeID: ep.ID,
		Stage:     ep.Stage,
		Reason:    "shots_reordered",
	})
	return contracts.StudioShotListResult{Shots: shotsToDTO(reordered)}, nil
}

// SyncWorkspace reconciles JSON files in the projects directory (or one specific project) with SQLite.
func (s *Service) SyncWorkspace(ctx context.Context, projectID string) (*contracts.StudioProjectDetailResult, error) {
	if s.repo == nil {
		return nil, apperr.Unavailable("studio repository is unavailable").
			WithMsg("errors.studio.service_unavailable", nil)
	}
	trimmedID := strings.TrimSpace(projectID)
	if trimmedID != "" {
		proj, err := s.repo.GetProject(ctx, trimmedID)
		if err != nil {
			return nil, err
		}
		if err := s.syncSingleProjectFromDisk(ctx, proj); err != nil {
			return nil, err
		}
		detail, err := s.GetProject(ctx, proj.ID)
		return &detail, err
	}

	pDir := s.ProjectsDir()
	if pDir == "" {
		return nil, nil
	}
	if cerr := engine.CheckFile(ctx, pDir, false); cerr != nil {
		return nil, cerr
	}
	entries, err := os.ReadDir(pDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, apperr.Wrap(apperr.CodeUnavailable, "Failed to read studio projects directory", err).
			WithMsg("errors.studio.io_failed", nil)
	}

	var firstSynced *contracts.StudioProjectDetailResult
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		projRoot := filepath.Join(pDir, entry.Name())
		projFile := filepath.Join(projRoot, "project.json")
		if cerr := engine.CheckFile(ctx, projFile, false); cerr != nil {
			continue
		}
		data, rerr := os.ReadFile(projFile)
		if rerr != nil {
			continue
		}
		var diskProj domainstudio.Project
		if jerr := json.Unmarshal(data, &diskProj); jerr != nil {
			continue
		}
		if diskProj.Slug == "" {
			diskProj.Slug = domainstudio.Slugify(entry.Name())
		}
		if diskProj.Title == "" {
			diskProj.Title = entry.Name()
		}
		diskProj.RootDir = projRoot
		// Never trust auto_approve_batches from disk JSON; users must explicitly enable it in SQLite/UI.
		diskProj.AutoApproveBatches = false
		sanitizeDiskProjectEnums(&diskProj)

		existing, gerr := s.repo.GetProject(ctx, diskProj.ID)
		if gerr != nil {
			existing, gerr = s.repo.GetProject(ctx, diskProj.Slug)
		}
		if gerr != nil {
			created, cerr := s.repo.CreateProject(ctx, diskProj)
			if cerr != nil {
				s.log.WarnContext(ctx, "studio: failed to import project from disk", "slug", diskProj.Slug, "error", cerr)
				continue
			}
			existing = created
		} else {
			existing.Title = diskProj.Title
			existing.StylePrompt = diskProj.StylePrompt
			if diskProj.AspectRatio != "" {
				existing.AspectRatio = diskProj.AspectRatio
			}
			if diskProj.Genre != "" {
				existing.Genre = diskProj.Genre
			}
			if diskProj.ProductionMode != "" {
				existing.ProductionMode = diskProj.ProductionMode
			}
			if diskProj.RootDir != "" {
				existing.RootDir = diskProj.RootDir
			}
			if upd, uerr := s.repo.UpdateProject(ctx, existing); uerr == nil {
				existing = upd
			} else {
				s.log.WarnContext(ctx, "studio: failed to update synced project metadata", "project_id", existing.ID, "error", uerr)
			}
		}

		if serr := s.syncSingleProjectFromDisk(ctx, existing); serr != nil {
			s.log.WarnContext(ctx, "studio: failed to sync project workspace artifacts", "project_id", existing.ID, "error", serr)
		}
		if firstSynced == nil {
			if detail, derr := s.GetProject(ctx, existing.ID); derr == nil {
				firstSynced = &detail
			}
		}
	}

	s.emit(contracts.EventStudioProjectChanged, contracts.StudioProjectChangedEvent{
		Action: contracts.ProjectActionSynced,
	})
	return firstSynced, nil
}

// SyncAndListWorkspace reconciles disk projects/episodes with SQLite and returns the
// optionally refreshed project alongside the full project listing.
func (s *Service) SyncAndListWorkspace(ctx context.Context, projectID string) (contracts.StudioSyncWorkspaceResult, error) {
	proj, err := s.SyncWorkspace(ctx, projectID)
	if err != nil {
		return contracts.StudioSyncWorkspaceResult{}, err
	}
	list, err := s.ListProjects(ctx)
	if err != nil {
		return contracts.StudioSyncWorkspaceResult{}, err
	}
	return contracts.StudioSyncWorkspaceResult{
		Project:     proj,
		Projects:    list.Projects,
		ProjectsDir: list.ProjectsDir,
	}, nil
}

func sanitizeDiskProjectEnums(dp *domainstudio.Project) {
	if dp == nil {
		return
	}
	if dp.Genre != "" && domainstudio.ValidateGenre(dp.Genre) != nil {
		dp.Genre = ""
	}
	if dp.ProductionMode != "" && domainstudio.ValidateProductionMode(dp.ProductionMode) != nil {
		dp.ProductionMode = ""
	}
	if ar := strings.TrimSpace(dp.AspectRatio); ar != "" && !slices.Contains(domainmedia.VideoAspectRatios(), ar) {
		dp.AspectRatio = ""
	}
}

func (s *Service) sanitizeSyncedPath(ctx context.Context, baseDir, projectID, rawPath string) string {
	trimmed := strings.TrimSpace(rawPath)
	if trimmed == "" || strings.TrimSpace(baseDir) == "" {
		return ""
	}
	candidate := trimmed
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(baseDir, candidate)
	}
	clean := filepath.Clean(candidate)
	if !isWithinBaseDir(baseDir, clean) {
		s.log.WarnContext(ctx, "studio: stripped outside-workspace path during sync", "project_id", projectID)
		return ""
	}
	if err := engine.CheckFile(ctx, clean, false); err != nil {
		s.log.WarnContext(ctx, "studio: stripped untrusted file path during sync", "project_id", projectID, "error", err)
		return ""
	}
	return clean
}

func (s *Service) syncSingleProjectFromDisk(ctx context.Context, proj domainstudio.Project) error {
	projRoot := s.resolveProjectRoot(proj)
	if projRoot == "" {
		return nil
	}
	if err := engine.CheckFile(ctx, projRoot, false); err != nil {
		return err
	}
	if s.media != nil {
		s.media.AddAllowedRoot(projRoot)
	}

	// Sync project.json metadata if modified on disk (never overwriting AutoApproveBatches!).
	projJSONPath := filepath.Join(projRoot, "project.json")
	if engine.CheckFile(ctx, projJSONPath, false) == nil {
		if b, err := os.ReadFile(projJSONPath); err == nil {
			var dp domainstudio.Project
			if json.Unmarshal(b, &dp) == nil && strings.TrimSpace(dp.Title) != "" {
				sanitizeDiskProjectEnums(&dp)
				proj.Title = strings.TrimSpace(dp.Title)
				if dp.Genre != "" {
					proj.Genre = dp.Genre
				}
				if dp.ProductionMode != "" {
					proj.ProductionMode = dp.ProductionMode
				}
				if dp.AspectRatio != "" {
					proj.AspectRatio = dp.AspectRatio
				}
				proj.StylePrompt = dp.StylePrompt
				if updated, uerr := s.repo.UpdateProject(ctx, proj); uerr == nil {
					proj = updated
				} else {
					s.log.WarnContext(ctx, "studio: failed to update project from disk project.json", "project_id", proj.ID, "error", uerr)
				}
			}
		}
	}

	// Sync asset_bible.json -> studio_assets.
	biblePath := filepath.Join(projRoot, "assets", "asset_bible.json")
	if engine.CheckFile(ctx, biblePath, false) == nil {
		if b, err := os.ReadFile(biblePath); err == nil {
			var pkg domainstudio.AssetBiblePackage
			if json.Unmarshal(b, &pkg) == nil {
				existingAssets, lerr := s.repo.ListAssets(ctx, proj.ID)
				if lerr != nil {
					s.log.WarnContext(ctx, "studio: failed to list existing assets during sync", "project_id", proj.ID, "error", lerr)
				}
				byTag := make(map[string]domainstudio.AssetEntry, len(existingAssets))
				for _, ea := range existingAssets {
					byTag[strings.ToLower(ea.CodeTag)] = ea
				}
				merged := make([]domainstudio.AssetEntry, 0, len(pkg.Assets))
				for _, da := range pkg.Assets {
					da.ProjectID = proj.ID
					da.Normalize()
					hadDiskPath := strings.TrimSpace(da.PrimaryPath) != ""
					da.PrimaryPath = s.sanitizeSyncedPath(ctx, projRoot, proj.ID, da.PrimaryPath)
					if hadDiskPath && da.PrimaryPath == "" {
						da.PrimaryAssetID = ""
					}
					if prev, ok := byTag[strings.ToLower(da.CodeTag)]; ok {
						if da.ID == "" {
							da.ID = prev.ID
						}
						if !hadDiskPath && da.PrimaryPath == "" {
							da.PrimaryPath = prev.PrimaryPath
							if da.PrimaryAssetID == "" {
								da.PrimaryAssetID = prev.PrimaryAssetID
							}
						}
					}
					merged = append(merged, da)
				}
				if _, rerr := s.repo.ReplaceAssets(ctx, proj.ID, merged); rerr != nil {
					s.log.WarnContext(ctx, "studio: failed to replace synced assets", "project_id", proj.ID, "error", rerr)
				}
			}
		}
	}

	// Sync episodes/*/storyboard/shots.json & timeline/timeline.json.
	epsDir := filepath.Join(projRoot, "episodes")
	epEntries, err := os.ReadDir(epsDir)
	if err == nil {
		existingEps, lerr := s.repo.ListEpisodes(ctx, proj.ID)
		if lerr != nil {
			s.log.WarnContext(ctx, "studio: failed to list episodes during sync", "project_id", proj.ID, "error", lerr)
		}
		epByCode := make(map[string]domainstudio.Episode, len(existingEps))
		for _, ee := range existingEps {
			epByCode[ee.Code] = ee
		}
		for _, entry := range epEntries {
			if !entry.IsDir() {
				continue
			}
			epCode := domainstudio.Slugify(entry.Name())
			epRoot := filepath.Join(epsDir, entry.Name())
			if !isWithinBaseDir(projRoot, epRoot) {
				continue
			}
			ep, exists := epByCode[epCode]
			if !exists {
				created, cerr := s.repo.CreateEpisode(ctx, domainstudio.Episode{
					ProjectID:        proj.ID,
					Code:             epCode,
					Title:            strings.ToUpper(epCode),
					Stage:            domainstudio.StageScript,
					RawScriptPath:    filepath.Join(epRoot, "script", "raw_screenplay.txt"),
					ScriptJSONPath:   filepath.Join(epRoot, "script", "normalized_script.json"),
					AssetsJSONPath:   filepath.Join(epRoot, "assets", "asset_bible.json"),
					ShotsJSONPath:    filepath.Join(epRoot, "storyboard", "shots.json"),
					TimelineJSONPath: filepath.Join(epRoot, "timeline", "timeline.json"),
				})
				if cerr != nil {
					s.log.WarnContext(ctx, "studio: failed to create episode during disk sync", "project_id", proj.ID, "code", epCode, "error", cerr)
					continue
				}
				ep = created
			}
			timelinePath := filepath.Join(epRoot, "timeline", "timeline.json")
			if ep.TimelineJSONPath == "" {
				ep.TimelineJSONPath = timelinePath
				if updatedEp, uerr := s.repo.UpdateEpisode(ctx, ep); uerr == nil {
					ep = updatedEp
				} else {
					s.log.WarnContext(ctx, "studio: failed to update episode timeline path during sync", "episode_id", ep.ID, "error", uerr)
				}
			}
			shotsDiskPath := filepath.Join(epRoot, "storyboard", "shots.json")
			if engine.CheckFile(ctx, shotsDiskPath, false) == nil {
				if b, serr := os.ReadFile(shotsDiskPath); serr == nil {
					var sp domainstudio.ShotPromptPackage
					if json.Unmarshal(b, &sp) == nil {
						existingShots, lserr := s.repo.ListShots(ctx, ep.ID)
						if lserr != nil {
							s.log.WarnContext(ctx, "studio: failed to list existing shots during sync", "episode_id", ep.ID, "error", lserr)
						}
						byID := make(map[string]domainstudio.Shot, len(existingShots))
						bySeq := make(map[int]domainstudio.Shot, len(existingShots))
						for _, es := range existingShots {
							byID[es.ID] = es
							bySeq[es.Seq] = es
						}
						merged := make([]domainstudio.Shot, 0, len(sp.Shots))
						for i, ds := range sp.Shots {
							ds.EpisodeID = ep.ID
							if ds.Seq <= 0 {
								ds.Seq = i + 1
							}
							ds.NormalizeWithDefaultDuration(s.cfg.DefaultShotDurationSec)

							hadKF := strings.TrimSpace(ds.KeyframePath) != ""
							hadVid := strings.TrimSpace(ds.VideoPath) != ""
							hadAud := strings.TrimSpace(ds.AudioPath) != ""
							ds.KeyframePath = s.sanitizeSyncedPath(ctx, projRoot, proj.ID, ds.KeyframePath)
							ds.VideoPath = s.sanitizeSyncedPath(ctx, projRoot, proj.ID, ds.VideoPath)
							ds.AudioPath = s.sanitizeSyncedPath(ctx, projRoot, proj.ID, ds.AudioPath)
							if hadKF && ds.KeyframePath == "" {
								ds.KeyframeAssetID = ""
							}
							if hadVid && ds.VideoPath == "" {
								ds.VideoAssetID = ""
							}
							if hadAud && ds.AudioPath == "" {
								ds.AudioAssetID = ""
							}

							prev, ok := byID[ds.ID]
							if !ok {
								prev, ok = bySeq[ds.Seq]
							}
							if ok {
								if ds.ID == "" {
									ds.ID = prev.ID
								}
								if !hadKF && ds.KeyframePath == "" {
									ds.KeyframePath = prev.KeyframePath
									ds.KeyframeAssetID = prev.KeyframeAssetID
								}
								if !hadVid && ds.VideoPath == "" {
									ds.VideoPath = prev.VideoPath
									ds.VideoAssetID = prev.VideoAssetID
								}
								if !hadAud && ds.AudioPath == "" {
									ds.AudioPath = prev.AudioPath
									ds.AudioAssetID = prev.AudioAssetID
								}
							}
							merged = append(merged, ds)
						}
						if _, rerr := s.repo.ReplaceShots(ctx, ep.ID, merged); rerr != nil {
							s.log.WarnContext(ctx, "studio: failed to replace synced shots", "episode_id", ep.ID, "error", rerr)
						}
					}
				}
			}
			if engine.CheckFile(ctx, timelinePath, false) == nil {
				if tb, terr := os.ReadFile(timelinePath); terr == nil {
					var diskTL domainstudio.EditTimeline
					if json.Unmarshal(tb, &diskTL) == nil && len(diskTL.Tracks) > 0 {
						for ti := range diskTL.Tracks {
							filtered := make([]domainstudio.TimelineClip, 0, len(diskTL.Tracks[ti].Clips))
							for _, cl := range diskTL.Tracks[ti].Clips {
								hadFP := strings.TrimSpace(cl.FilePath) != ""
								hadSP := strings.TrimSpace(cl.SpritePath) != ""
								cl.FilePath = s.sanitizeSyncedPath(ctx, projRoot, proj.ID, cl.FilePath)
								cl.SpritePath = s.sanitizeSyncedPath(ctx, projRoot, proj.ID, cl.SpritePath)
								if hadFP && cl.FilePath == "" {
									cl.AssetID = ""
								}
								if hadSP && cl.SpritePath == "" {
									cl.SpriteAssetID = ""
								}
								if cl.FilePath == "" && cl.AssetID == "" {
									continue
								}
								filtered = append(filtered, cl)
							}
							diskTL.Tracks[ti].Clips = filtered
						}
						diskTL.Normalize()
						if verr := diskTL.ValidateWithMaxDim(s.cfg.MaxVideoDimension); verr == nil {
							diskCanon, _ := json.Marshal(diskTL)
							if curRec, rerr := s.repo.GetTimeline(ctx, ep.ID); rerr == nil {
								curCanon, _ := json.Marshal(curRec.Timeline)
								if string(diskCanon) != string(curCanon) {
									if _, serr := s.repo.SaveTimelineRevision(ctx, ep.ID, curRec.Revision, domainstudio.ActorUser, "disk sync", diskTL); serr == nil {
										s.log.DebugContext(ctx, "reconciled timeline.json from disk", "episode_id", ep.ID)
									} else {
										s.log.WarnContext(ctx, "studio: failed to save synced timeline revision", "episode_id", ep.ID, "error", serr)
									}
								}
							}
						} else {
							s.log.WarnContext(ctx, "studio: skipped invalid timeline.json during disk sync", "episode_id", ep.ID, "error", verr)
						}
					}
				}
			}
		}
	}

	dtoProj := projectToDTO(proj)
	s.emit(contracts.EventStudioProjectChanged, contracts.StudioProjectChangedEvent{
		ProjectID: proj.ID,
		Action:    contracts.ProjectActionSynced,
		Project:   &dtoProj,
	})
	return nil
}

// RunStage executes any Phase 1 stage or shot/asset batch using shared workflow nodes.
func (s *Service) RunStage(ctx context.Context, p contracts.StudioEpisodeRunStagePayload) (contracts.StudioRunStageResult, error) {
	if s.repo == nil || s.wf == nil {
		return contracts.StudioRunStageResult{}, apperr.Unavailable("studio dependencies are unavailable").
			WithMsg("errors.studio.service_unavailable", nil)
	}
	if err := p.Validate(); err != nil {
		return contracts.StudioRunStageResult{}, err
	}

	ep, err := s.repo.GetEpisode(ctx, strings.TrimSpace(p.EpisodeID))
	if err != nil {
		return contracts.StudioRunStageResult{}, err
	}

	s.runMu.Lock()
	if s.runningEps[ep.ID] {
		s.runMu.Unlock()
		return contracts.StudioRunStageResult{}, apperr.New(apperr.CodeConflict, "episode stage execution is already in progress").
			WithMsg("errors.studio.stage_in_progress", nil)
	}
	s.runningEps[ep.ID] = true
	s.runMu.Unlock()
	defer func() {
		s.runMu.Lock()
		delete(s.runningEps, ep.ID)
		s.runMu.Unlock()
	}()

	proj, err := s.repo.GetProject(ctx, ep.ProjectID)
	if err != nil {
		return contracts.StudioRunStageResult{}, err
	}
	if !proj.AutoApproveBatches && !p.Approved && isHeavyGenerationStage(p.Stage) {
		return contracts.StudioRunStageResult{}, apperr.Validation("Batch execution requires approval when auto_approve_batches is disabled").
			WithMsg("errors.studio.approval_required", nil).
			WithDetails(map[string]any{"approval_required": true, "project_id": proj.ID, "stage": string(p.Stage)})
	}
	ctx = domainmedia.ContextWithUsageMeta(ctx, domainmedia.UsageMeta{
		ProjectID: proj.ID,
		EpisodeID: ep.ID,
		Operation: string(p.Stage),
	})
	epRoot := s.resolveEpisodeRoot(proj, ep.Code)
	if epRoot != "" {
		if err := s.ensureGuardedEpisodeDirs(ctx, epRoot); err != nil {
			return contracts.StudioRunStageResult{}, err
		}
	}

	switch p.Stage {
	case contracts.StudioStageScriptNormalize:
		return s.runScriptNormalize(ctx, proj, ep, epRoot, p)
	case contracts.StudioStageExtractAssets:
		return s.runExtractAssets(ctx, proj, ep, epRoot, p)
	case contracts.StudioStageAuthorShots:
		return s.runAuthorShots(ctx, proj, ep, epRoot, p)
	case contracts.StudioStageGenerateAssets:
		return s.runGenerateAssets(ctx, proj, ep, epRoot, p)
	case contracts.StudioStageGenerateKeyframes:
		return s.runGenerateKeyframes(ctx, proj, ep, epRoot, p)
	case contracts.StudioStageGenerateVideos:
		return s.runGenerateVideos(ctx, proj, ep, epRoot, p)
	case contracts.StudioStageGenerateTTS:
		return s.runGenerateTTS(ctx, proj, ep, epRoot, p)
	case contracts.StudioStageProbeShots:
		return s.runProbeShots(ctx, proj, ep, p)
	case contracts.StudioStageConcatVideo:
		return s.runConcatVideo(ctx, proj, ep, epRoot, p)
	default:
		return contracts.StudioRunStageResult{}, apperr.Validation("unsupported stage")
	}
}

func (s *Service) runScriptNormalize(ctx context.Context, proj domainstudio.Project, ep domainstudio.Episode, epRoot string, p contracts.StudioEpisodeRunStagePayload) (contracts.StudioRunStageResult, error) {
	rawScript := strings.TrimSpace(s.readGuardedStringFile(ctx, epRoot, ep.RawScriptPath))
	if rawScript == "" {
		return contracts.StudioRunStageResult{}, apperr.Validation("raw screenplay is empty; save script text before normalizing").
			WithMsg("errors.studio.raw_script_required", nil)
	}
	model := firstNonEmpty(p.Model, proj.DefaultTextModel)
	inBytes, err := json.Marshal(contracts.ScriptNormalizeInput{
		RawScript:    rawScript,
		Genre:        string(proj.Genre),
		EpisodeTitle: ep.Title,
		AspectRatio:  proj.AspectRatio,
		Model:        model,
		ProjectDir:   epRoot,
	})
	if err != nil {
		return contracts.StudioRunStageResult{}, apperr.Internal(err)
	}
	rawOut, err := s.wf.RunNode(ctx, contracts.NodeTypeScriptNormalize, inBytes)
	if err != nil {
		return contracts.StudioRunStageResult{}, err
	}
	var out contracts.ScriptNormalizeOutput
	if err := decodeNodeOutput(rawOut, &out); err != nil {
		return contracts.StudioRunStageResult{}, err
	}
	if out.ScriptPath != "" {
		ep.ScriptJSONPath = out.ScriptPath
	} else if epRoot != "" {
		ep.ScriptJSONPath = filepath.Join(epRoot, "script", "normalized_script.json")
		if err := s.writeGuardedFile(ctx, epRoot, ep.ScriptJSONPath, []byte(out.NormalizedScriptJSON), 0o644); err != nil {
			return contracts.StudioRunStageResult{}, err
		}
	}
	if ep.Stage == domainstudio.StageScript {
		ep.Stage = domainstudio.StageAssets
	}
	if _, err := s.repo.UpdateEpisode(ctx, ep); err != nil {
		return contracts.StudioRunStageResult{}, err
	}
	detail, err := s.GetEpisode(ctx, ep.ID)
	if err != nil {
		return contracts.StudioRunStageResult{}, err
	}
	s.emit(contracts.EventStudioEpisodeUpdated, contracts.StudioEpisodeUpdatedEvent{
		ProjectID: proj.ID,
		EpisodeID: ep.ID,
		Stage:     detail.Episode.Stage,
		Reason:    "script_normalized",
	})
	return contracts.StudioRunStageResult{
		Episode:        detail,
		OutputPath:     ep.ScriptJSONPath,
		CompletedCount: out.BeatCount,
		Summary:        fmt.Sprintf("Normalized %d scenes / %d beats", out.SceneCount, out.BeatCount),
	}, nil
}

func (s *Service) runExtractAssets(ctx context.Context, proj domainstudio.Project, ep domainstudio.Episode, epRoot string, p contracts.StudioEpisodeRunStagePayload) (contracts.StudioRunStageResult, error) {
	normRaw := s.readGuardedStringFile(ctx, epRoot, ep.ScriptJSONPath)
	if strings.TrimSpace(normRaw) == "" {
		return contracts.StudioRunStageResult{}, apperr.Validation("normalized_script.json is required before extracting assets").
			WithMsg("errors.studio.normalized_script_required", nil)
	}
	model := firstNonEmpty(p.Model, proj.DefaultTextModel)
	inBytes, err := json.Marshal(contracts.ExtractAssetsInput{
		NormalizedScriptJSON: normRaw,
		StylePrompt:          proj.StylePrompt,
		Model:                model,
		ProjectDir:           epRoot,
	})
	if err != nil {
		return contracts.StudioRunStageResult{}, apperr.Internal(err)
	}
	rawOut, err := s.wf.RunNode(ctx, contracts.NodeTypeExtractAssets, inBytes)
	if err != nil {
		return contracts.StudioRunStageResult{}, err
	}
	var out contracts.ExtractAssetsOutput
	if err := decodeNodeOutput(rawOut, &out); err != nil {
		return contracts.StudioRunStageResult{}, err
	}

	var pkg domainstudio.AssetBiblePackage
	if err := json.Unmarshal([]byte(out.AssetBibleJSON), &pkg); err != nil {
		return contracts.StudioRunStageResult{}, apperr.Wrap(apperr.CodeIntegrity, "Invalid asset bible JSON output", err).
			WithMsg("errors.studio.llm_invalid_json", nil)
	}

	existingAssets, lerr := s.repo.ListAssets(ctx, proj.ID)
	if lerr != nil {
		s.log.WarnContext(ctx, "studio: failed to list existing assets before merge", "project_id", proj.ID, "error", lerr)
	}
	byTag := make(map[string]domainstudio.AssetEntry, len(existingAssets))
	for _, ea := range existingAssets {
		byTag[strings.ToLower(ea.CodeTag)] = ea
	}

	merged := make([]domainstudio.AssetEntry, 0, len(pkg.Assets))
	for _, a := range pkg.Assets {
		a.ProjectID = proj.ID
		a.Normalize()
		if !p.Force {
			if prev, ok := byTag[strings.ToLower(a.CodeTag)]; ok {
				a.ID = prev.ID
				a.PrimaryAssetID = prev.PrimaryAssetID
				a.PrimaryPath = prev.PrimaryPath
				if a.VoiceProvider == "" {
					a.VoiceProvider = prev.VoiceProvider
					a.VoiceID = prev.VoiceID
					a.VoiceStyle = prev.VoiceStyle
				}
			}
		}
		merged = append(merged, a)
	}
	savedAssets, err := s.repo.ReplaceAssets(ctx, proj.ID, merged)
	if err != nil {
		return contracts.StudioRunStageResult{}, err
	}
	if out.AssetsPath != "" {
		ep.AssetsJSONPath = out.AssetsPath
		if _, uerr := s.repo.UpdateEpisode(ctx, ep); uerr != nil {
			s.log.WarnContext(ctx, "studio: failed to update episode assets_json_path", "episode_id", ep.ID, "error", uerr)
		}
	}
	s.warnDiskMirror(ctx, "asset_bible.json", s.persistProjectToDisk(ctx, proj, savedAssets))

	detail, err := s.GetEpisode(ctx, ep.ID)
	if err != nil {
		return contracts.StudioRunStageResult{}, err
	}
	s.emit(contracts.EventStudioEpisodeUpdated, contracts.StudioEpisodeUpdatedEvent{
		ProjectID: proj.ID,
		EpisodeID: ep.ID,
		Stage:     detail.Episode.Stage,
		Reason:    "assets_extracted",
	})
	return contracts.StudioRunStageResult{
		Episode:        detail,
		OutputPath:     out.AssetsPath,
		CompletedCount: len(savedAssets),
		Summary:        fmt.Sprintf("Extracted %d assets", len(savedAssets)),
	}, nil
}

func (s *Service) runAuthorShots(ctx context.Context, proj domainstudio.Project, ep domainstudio.Episode, epRoot string, p contracts.StudioEpisodeRunStagePayload) (contracts.StudioRunStageResult, error) {
	normRaw := s.readGuardedStringFile(ctx, epRoot, ep.ScriptJSONPath)
	if strings.TrimSpace(normRaw) == "" {
		return contracts.StudioRunStageResult{}, apperr.Validation("normalized_script.json is required before authoring shots").
			WithMsg("errors.studio.normalized_script_required", nil)
	}
	assets, aerr := s.repo.ListAssets(ctx, proj.ID)
	if aerr != nil {
		s.log.WarnContext(ctx, "studio: failed to list assets for shot authoring", "project_id", proj.ID, "error", aerr)
	}
	bibleBytes, merr := json.Marshal(domainstudio.AssetBiblePackage{
		StylePrompt: proj.StylePrompt,
		Assets:      assets,
	})
	if merr != nil {
		return contracts.StudioRunStageResult{}, apperr.Internal(merr)
	}

	model := firstNonEmpty(p.Model, proj.DefaultTextModel)
	inBytes, err := json.Marshal(contracts.AuthorShotPromptsInput{
		NormalizedScriptJSON: normRaw,
		AssetBibleJSON:       string(bibleBytes),
		ProductionMode:       string(proj.ProductionMode),
		DefaultDurationSec:   s.cfg.DefaultShotDurationSec,
		StylePrompt:          proj.StylePrompt,
		Model:                model,
		ProjectDir:           epRoot,
	})
	if err != nil {
		return contracts.StudioRunStageResult{}, apperr.Internal(err)
	}
	rawOut, err := s.wf.RunNode(ctx, contracts.NodeTypeAuthorShotPrompts, inBytes)
	if err != nil {
		return contracts.StudioRunStageResult{}, err
	}
	var out contracts.AuthorShotPromptsOutput
	if err := decodeNodeOutput(rawOut, &out); err != nil {
		return contracts.StudioRunStageResult{}, err
	}

	var pkg domainstudio.ShotPromptPackage
	if err := json.Unmarshal([]byte(out.ShotsJSON), &pkg); err != nil {
		return contracts.StudioRunStageResult{}, apperr.Wrap(apperr.CodeIntegrity, "Invalid shots JSON output", err).
			WithMsg("errors.studio.llm_invalid_json", nil)
	}

	for i := range pkg.Shots {
		pkg.Shots[i].ID = ""
		pkg.Shots[i].EpisodeID = ep.ID
		pkg.Shots[i].Seq = i + 1
		pkg.Shots[i].Status = domainstudio.ShotPending
		pkg.Shots[i].NormalizeWithDefaultDuration(s.cfg.DefaultShotDurationSec)
	}
	savedShots, err := s.repo.ReplaceShots(ctx, ep.ID, pkg.Shots)
	if err != nil {
		return contracts.StudioRunStageResult{}, err
	}
	if out.ShotsPath != "" {
		ep.ShotsJSONPath = out.ShotsPath
	}
	if ep.Stage == domainstudio.StageScript || ep.Stage == domainstudio.StageAssets {
		ep.Stage = domainstudio.StageStoryboard
	}
	if _, err := s.repo.UpdateEpisode(ctx, ep); err != nil {
		return contracts.StudioRunStageResult{}, err
	}
	s.warnDiskMirror(ctx, "shots.json", s.syncEpisodeShotsToDisk(ctx, proj, ep, savedShots))

	detail, err := s.GetEpisode(ctx, ep.ID)
	if err != nil {
		return contracts.StudioRunStageResult{}, err
	}
	s.emit(contracts.EventStudioEpisodeUpdated, contracts.StudioEpisodeUpdatedEvent{
		ProjectID: proj.ID,
		EpisodeID: ep.ID,
		Stage:     detail.Episode.Stage,
		Reason:    "shots_authored",
	})
	return contracts.StudioRunStageResult{
		Episode:        detail,
		OutputPath:     out.ShotsPath,
		CompletedCount: len(savedShots),
		Summary:        fmt.Sprintf("Authored %d storyboard shots", len(savedShots)),
	}, nil
}

func (s *Service) runGenerateAssets(ctx context.Context, proj domainstudio.Project, ep domainstudio.Episode, epRoot string, p contracts.StudioEpisodeRunStagePayload) (contracts.StudioRunStageResult, error) {
	assets, err := s.repo.ListAssets(ctx, proj.ID)
	if err != nil {
		return contracts.StudioRunStageResult{}, err
	}
	filter := toSet(p.AssetIDs)
	targets := make([]domainstudio.AssetEntry, 0, len(assets))
	for _, a := range assets {
		if len(filter) > 0 && !filter[a.ID] {
			continue
		}
		if len(filter) == 0 && !p.Force && strings.TrimSpace(a.PrimaryPath) != "" {
			continue
		}
		targets = append(targets, a)
	}

	outDir := ""
	if epRoot != "" {
		outDir = filepath.Join(epRoot, "media", "assets")
		if err := s.ensureGuardedDir(ctx, outDir); err != nil {
			return contracts.StudioRunStageResult{}, err
		}
	}
	model := firstNonEmpty(p.Model, proj.DefaultImageModel)

	completed, failed := 0, 0
	var lastErr error
	for idx, asset := range targets {
		if ctx.Err() != nil {
			return contracts.StudioRunStageResult{}, ctx.Err()
		}
		s.reportBatchProgress(ctx, proj.ID, ep.ID, string(p.Stage), idx+1, len(targets), completed, failed, asset.ID, asset.Name, fmt.Sprintf("Generating asset reference: %s", asset.Name), contracts.BatchStatusRunning)

		prompt := composePrompt(proj.StylePrompt, firstNonEmpty(asset.Prompt, asset.Description, asset.Name))
		reqBytes, merr := json.Marshal(contracts.ImageGenerateInput{
			Prompt:      prompt,
			Model:       model,
			AspectRatio: proj.AspectRatio,
			Resolution:  p.Resolution,
			OutputDir:   outDir,
		})
		if merr != nil {
			failed++
			lastErr = apperr.Internal(merr)
			continue
		}
		rawOut, rerr := s.wf.RunNode(ctx, contracts.NodeTypeImageGenerate, reqBytes)
		if rerr != nil {
			failed++
			lastErr = rerr
			continue
		}
		var out contracts.ImageGenerateOutput
		if merr := decodeNodeOutput(rawOut, &out); merr != nil {
			failed++
			lastErr = merr
			continue
		}
		asset.PrimaryPath = out.Path
		if regID := s.registerMedia(ctx, "image", contracts.NodeTypeImageGenerate, out.Path, outDir, 0); regID != "" {
			asset.PrimaryAssetID = regID
		}
		if _, uerr := s.repo.UpsertAsset(ctx, asset); uerr != nil {
			failed++
			lastErr = uerr
			continue
		}
		s.recordSelectedVersion(ctx, proj.ID, ep.ID, domainstudio.TargetAsset, asset.ID, out.Path, contracts.NodeTypeImageGenerate, prompt, model)
		completed++
	}

	if allAssets, lerr := s.repo.ListAssets(ctx, proj.ID); lerr == nil {
		s.warnDiskMirror(ctx, "asset_bible.json", s.persistProjectToDisk(ctx, proj, allAssets))
	} else {
		s.log.WarnContext(ctx, "studio: failed to list assets after batch generation", "project_id", proj.ID, "error", lerr)
	}
	status := contracts.BatchStatusCompleted
	if failed > 0 && completed == 0 {
		status = contracts.BatchStatusFailed
	}
	s.reportBatchProgress(ctx, proj.ID, ep.ID, string(p.Stage), len(targets), len(targets), completed, failed, "", "", fmt.Sprintf("Assets generated: %d ok, %d failed", completed, failed), status)

	detail, err := s.GetEpisode(ctx, ep.ID)
	if err != nil {
		return contracts.StudioRunStageResult{}, err
	}
	s.emit(contracts.EventStudioEpisodeUpdated, contracts.StudioEpisodeUpdatedEvent{
		ProjectID: proj.ID,
		EpisodeID: ep.ID,
		Stage:     detail.Episode.Stage,
		Reason:    "asset_sheets_generated",
	})
	if failed > 0 && completed == 0 && lastErr != nil {
		return contracts.StudioRunStageResult{}, lastErr
	}
	return contracts.StudioRunStageResult{
		Episode:        detail,
		CompletedCount: completed,
		FailedCount:    failed,
		Summary:        fmt.Sprintf("Generated %d asset reference image(s), %d failed", completed, failed),
	}, nil
}

func (s *Service) runGenerateKeyframes(ctx context.Context, proj domainstudio.Project, ep domainstudio.Episode, epRoot string, p contracts.StudioEpisodeRunStagePayload) (contracts.StudioRunStageResult, error) {
	shots, err := s.repo.ListShots(ctx, ep.ID)
	if err != nil {
		return contracts.StudioRunStageResult{}, err
	}
	assets, aerr := s.repo.ListAssets(ctx, proj.ID)
	if aerr != nil {
		s.log.WarnContext(ctx, "studio: failed to list assets for keyframe generation", "project_id", proj.ID, "error", aerr)
	}
	tagPathMap := buildAssetPathMap(assets)

	filter := toSet(p.ShotIDs)
	targets := make([]domainstudio.Shot, 0, len(shots))
	for _, sh := range shots {
		if len(filter) > 0 && !filter[sh.ID] {
			continue
		}
		if len(filter) == 0 && !p.Force && strings.TrimSpace(sh.KeyframePath) != "" {
			continue
		}
		targets = append(targets, sh)
	}

	outDir := ""
	if epRoot != "" {
		outDir = filepath.Join(epRoot, "media", "keyframes")
		if err := s.ensureGuardedDir(ctx, outDir); err != nil {
			return contracts.StudioRunStageResult{}, err
		}
	}
	model := firstNonEmpty(p.Model, proj.DefaultImageModel)

	completed, failed := 0, 0
	var lastErr error
	for idx, shot := range targets {
		if ctx.Err() != nil {
			return contracts.StudioRunStageResult{}, ctx.Err()
		}
		s.reportBatchProgress(ctx, proj.ID, ep.ID, string(p.Stage), idx+1, len(targets), completed, failed, shot.ID, shot.Title, fmt.Sprintf("Generating keyframe #%d: %s", shot.Seq, shot.Title), contracts.BatchStatusRunning)

		refs := collectRefImages(shot.RefAssetTags, tagPathMap)
		prompt := composePrompt(proj.StylePrompt, firstNonEmpty(shot.ImagePrompt, shot.Title))
		reqBytes, merr := json.Marshal(contracts.ImageGenerateInput{
			Prompt:          prompt,
			ReferenceImages: refs,
			Model:           model,
			AspectRatio:     proj.AspectRatio,
			Resolution:      p.Resolution,
			OutputDir:       outDir,
		})
		if merr != nil {
			failed++
			lastErr = apperr.Internal(merr)
			continue
		}
		rawOut, rerr := s.wf.RunNode(ctx, contracts.NodeTypeImageGenerate, reqBytes)
		if rerr != nil {
			failed++
			lastErr = rerr
			shot.Status = domainstudio.ShotFailed
			shot.ErrorText = rerr.Error()
			if _, uerr := s.repo.UpsertShot(ctx, shot); uerr != nil {
				s.log.WarnContext(ctx, "studio: failed to persist failed shot status", "shot_id", shot.ID, "error", uerr)
			}
			continue
		}
		var out contracts.ImageGenerateOutput
		if merr := decodeNodeOutput(rawOut, &out); merr != nil {
			failed++
			lastErr = merr
			continue
		}
		shot.KeyframePath = out.Path
		if regID := s.registerMedia(ctx, "image", contracts.NodeTypeImageGenerate, out.Path, outDir, 0); regID != "" {
			shot.KeyframeAssetID = regID
		}
		if shot.Status == domainstudio.ShotPending || shot.Status == domainstudio.ShotFailed {
			shot.Status = domainstudio.ShotKeyframeReady
		}
		shot.ErrorText = ""
		if _, uerr := s.repo.UpsertShot(ctx, shot); uerr != nil {
			s.log.WarnContext(ctx, "studio: failed to persist shot keyframe metadata", "shot_id", shot.ID, "error", uerr)
			failed++
			lastErr = uerr
			continue
		}
		s.recordSelectedVersion(ctx, proj.ID, ep.ID, domainstudio.TargetKeyframe, shot.ID, out.Path, contracts.NodeTypeImageGenerate, prompt, model)
		completed++
	}

	if completed > 0 && ep.Stage == domainstudio.StageStoryboard {
		ep.Stage = domainstudio.StageVideo
		if _, uerr := s.repo.UpdateEpisode(ctx, ep); uerr != nil {
			s.log.WarnContext(ctx, "studio: failed to advance episode stage after keyframes", "episode_id", ep.ID, "error", uerr)
		}
	}
	if refreshed, lerr := s.repo.ListShots(ctx, ep.ID); lerr == nil {
		s.warnDiskMirror(ctx, "shots.json", s.syncEpisodeShotsToDisk(ctx, proj, ep, refreshed))
	} else {
		s.log.WarnContext(ctx, "studio: failed to list shots after keyframe batch", "episode_id", ep.ID, "error", lerr)
	}
	status := contracts.BatchStatusCompleted
	if failed > 0 && completed == 0 {
		status = contracts.BatchStatusFailed
	}
	s.reportBatchProgress(ctx, proj.ID, ep.ID, string(p.Stage), len(targets), len(targets), completed, failed, "", "", fmt.Sprintf("Keyframes: %d ok, %d failed", completed, failed), status)

	detail, err := s.GetEpisode(ctx, ep.ID)
	if err != nil {
		return contracts.StudioRunStageResult{}, err
	}
	s.emit(contracts.EventStudioEpisodeUpdated, contracts.StudioEpisodeUpdatedEvent{
		ProjectID: proj.ID,
		EpisodeID: ep.ID,
		Stage:     detail.Episode.Stage,
		Reason:    "keyframes_generated",
	})
	if failed > 0 && completed == 0 && lastErr != nil {
		return contracts.StudioRunStageResult{}, lastErr
	}
	return contracts.StudioRunStageResult{
		Episode:        detail,
		CompletedCount: completed,
		FailedCount:    failed,
		Summary:        fmt.Sprintf("Generated %d keyframe(s), %d failed", completed, failed),
	}, nil
}

func (s *Service) runGenerateVideos(ctx context.Context, proj domainstudio.Project, ep domainstudio.Episode, epRoot string, p contracts.StudioEpisodeRunStagePayload) (contracts.StudioRunStageResult, error) {
	shots, err := s.repo.ListShots(ctx, ep.ID)
	if err != nil {
		return contracts.StudioRunStageResult{}, err
	}
	assets, aerr := s.repo.ListAssets(ctx, proj.ID)
	if aerr != nil {
		s.log.WarnContext(ctx, "studio: failed to list assets for video generation", "project_id", proj.ID, "error", aerr)
	}
	tagPathMap := buildAssetPathMap(assets)

	filter := toSet(p.ShotIDs)
	targets := make([]domainstudio.Shot, 0, len(shots))
	for _, sh := range shots {
		if len(filter) > 0 && !filter[sh.ID] {
			continue
		}
		if len(filter) == 0 && !p.Force && strings.TrimSpace(sh.VideoPath) != "" {
			continue
		}
		targets = append(targets, sh)
	}

	outDir := ""
	if epRoot != "" {
		outDir = filepath.Join(epRoot, "media", "videos")
		if err := s.ensureGuardedDir(ctx, outDir); err != nil {
			return contracts.StudioRunStageResult{}, err
		}
	}
	model := firstNonEmpty(p.Model, proj.DefaultVideoModel)

	// Run bounded workers up to config studio.max_concurrent_batch.
	workers := 0
	if len(targets) > 0 {
		workers = max(1, s.cfg.MaxConcurrentBatch)
		if workers > len(targets) {
			workers = len(targets)
		}
	}

	var (
		stateMu   sync.Mutex
		completed int
		failed    int
		doneCount int
		lastErr   error
	)

	jobCh := make(chan domainstudio.Shot, len(targets))
	for _, sh := range targets {
		jobCh <- sh
	}
	close(jobCh)

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		safego.Go("studio.generate_videos", func() {
			defer wg.Done()
			for shot := range jobCh {
				if ctx.Err() != nil {
					stateMu.Lock()
					lastErr = ctx.Err()
					stateMu.Unlock()
					return
				}

				vidIn := contracts.VideoGenerateInput{
					Prompt:        composePrompt(proj.StylePrompt, firstNonEmpty(shot.VideoPrompt, shot.ImagePrompt, shot.Title)),
					Model:         model,
					AspectRatio:   proj.AspectRatio,
					Resolution:    p.Resolution,
					Duration:      shot.DurationSec,
					GenerateAudio: proj.Genre == domainstudio.GenreDrama,
					OutputDir:     outDir,
				}
				if proj.ProductionMode == domainstudio.ModeR2V || shot.ShotMode == domainstudio.ShotModeR2V {
					refs := collectRefImages(shot.RefAssetTags, tagPathMap)
					if len(refs) == 0 && strings.TrimSpace(shot.KeyframePath) != "" {
						refs = engine.ImageInputRefs{engine.RefFor(shot.KeyframePath, engine.MediaImage)}
					}
					vidIn.ReferenceImages = refs
				} else if strings.TrimSpace(shot.KeyframePath) != "" {
					vidIn.StartImage = engine.ImageInputRef{MediaRef: engine.RefFor(shot.KeyframePath, engine.MediaImage)}
				}

				reqBytes, merr := json.Marshal(vidIn)
				if merr != nil {
					stateMu.Lock()
					failed++
					doneCount++
					lastErr = apperr.Internal(merr)
					stateMu.Unlock()
					continue
				}
				rawOut, rerr := s.wf.RunNode(ctx, contracts.NodeTypeVideoGenerate, reqBytes)
				if rerr != nil {
					shot.Status = domainstudio.ShotFailed
					shot.ErrorText = rerr.Error()
					if _, uerr := s.repo.UpsertShot(ctx, shot); uerr != nil {
						s.log.WarnContext(ctx, "studio: failed to persist failed shot video status", "shot_id", shot.ID, "error", uerr)
					}
					stateMu.Lock()
					failed++
					doneCount++
					lastErr = rerr
					curDone, curOK, curFail := doneCount, completed, failed
					stateMu.Unlock()
					s.reportBatchProgress(ctx, proj.ID, ep.ID, string(p.Stage), curDone, len(targets), curOK, curFail, shot.ID, shot.Title, fmt.Sprintf("Shot #%d video failed", shot.Seq), contracts.BatchStatusRunning)
					continue
				}

				var out contracts.VideoGenerateOutput
				if merr := decodeNodeOutput(rawOut, &out); merr != nil {
					stateMu.Lock()
					failed++
					doneCount++
					lastErr = merr
					stateMu.Unlock()
					continue
				}

				shot.VideoPath = out.Path
				if regID := s.registerMedia(ctx, "video", contracts.NodeTypeVideoGenerate, out.Path, outDir, out.Duration); regID != "" {
					shot.VideoAssetID = regID
				}
				shot.Status = domainstudio.ShotVideoReady
				shot.ErrorText = ""
				if _, uerr := s.repo.UpsertShot(ctx, shot); uerr != nil {
					s.log.WarnContext(ctx, "studio: failed to persist shot video metadata", "shot_id", shot.ID, "error", uerr)
					stateMu.Lock()
					failed++
					doneCount++
					lastErr = uerr
					curDone, curOK, curFail := doneCount, completed, failed
					stateMu.Unlock()
					s.reportBatchProgress(ctx, proj.ID, ep.ID, string(p.Stage), curDone, len(targets), curOK, curFail, shot.ID, shot.Title, fmt.Sprintf("Shot #%d video save failed", shot.Seq), contracts.BatchStatusRunning)
					continue
				}
				s.recordSelectedVersion(ctx, proj.ID, ep.ID, domainstudio.TargetVideo, shot.ID, out.Path, contracts.NodeTypeVideoGenerate, shot.VideoPrompt, model)

				stateMu.Lock()
				completed++
				doneCount++
				curDone, curOK, curFail := doneCount, completed, failed
				stateMu.Unlock()
				s.reportBatchProgress(ctx, proj.ID, ep.ID, string(p.Stage), curDone, len(targets), curOK, curFail, shot.ID, shot.Title, fmt.Sprintf("Generated shot #%d video", shot.Seq), contracts.BatchStatusRunning)
			}
		})
	}
	wg.Wait()
	if ctx.Err() != nil {
		return contracts.StudioRunStageResult{}, ctx.Err()
	}

	if refreshed, lerr := s.repo.ListShots(ctx, ep.ID); lerr == nil {
		s.warnDiskMirror(ctx, "shots.json", s.syncEpisodeShotsToDisk(ctx, proj, ep, refreshed))
	} else {
		s.log.WarnContext(ctx, "studio: failed to list shots after video batch", "episode_id", ep.ID, "error", lerr)
	}
	if completed > 0 && ep.Stage != domainstudio.StageExport {
		ep.Stage = domainstudio.StageVideo
		if _, uerr := s.repo.UpdateEpisode(ctx, ep); uerr != nil {
			s.log.WarnContext(ctx, "studio: failed to update episode stage after videos", "episode_id", ep.ID, "error", uerr)
		}
	}

	status := contracts.BatchStatusCompleted
	if failed > 0 && completed == 0 {
		status = contracts.BatchStatusFailed
	}
	s.reportBatchProgress(ctx, proj.ID, ep.ID, string(p.Stage), len(targets), len(targets), completed, failed, "", "", fmt.Sprintf("Videos: %d ok, %d failed", completed, failed), status)

	detail, err := s.GetEpisode(ctx, ep.ID)
	if err != nil {
		return contracts.StudioRunStageResult{}, err
	}
	s.emit(contracts.EventStudioEpisodeUpdated, contracts.StudioEpisodeUpdatedEvent{
		ProjectID: proj.ID,
		EpisodeID: ep.ID,
		Stage:     detail.Episode.Stage,
		Reason:    "videos_generated",
	})
	if failed > 0 && completed == 0 && lastErr != nil {
		return contracts.StudioRunStageResult{}, lastErr
	}
	return contracts.StudioRunStageResult{
		Episode:        detail,
		CompletedCount: completed,
		FailedCount:    failed,
		Summary:        fmt.Sprintf("Generated %d shot video(s), %d failed", completed, failed),
	}, nil
}

func (s *Service) runGenerateTTS(ctx context.Context, proj domainstudio.Project, ep domainstudio.Episode, epRoot string, p contracts.StudioEpisodeRunStagePayload) (contracts.StudioRunStageResult, error) {
	shots, err := s.repo.ListShots(ctx, ep.ID)
	if err != nil {
		return contracts.StudioRunStageResult{}, err
	}
	filter := toSet(p.ShotIDs)
	targets := make([]domainstudio.Shot, 0, len(shots))
	for _, sh := range shots {
		if len(filter) > 0 && !filter[sh.ID] {
			continue
		}
		speechText := shotSpeechText(sh)
		if speechText == "" {
			continue
		}
		if len(filter) == 0 && !p.Force && strings.TrimSpace(sh.AudioPath) != "" {
			continue
		}
		targets = append(targets, sh)
	}

	outDir := ""
	if epRoot != "" {
		outDir = filepath.Join(epRoot, "media", "tts")
		if err := s.ensureGuardedDir(ctx, outDir); err != nil {
			return contracts.StudioRunStageResult{}, err
		}
	}

	assets, aerr := s.repo.ListAssets(ctx, proj.ID)
	if aerr != nil {
		s.log.WarnContext(ctx, "studio: failed to list assets for tts voice mapping", "project_id", proj.ID, "error", aerr)
	}
	voiceBySpeaker := map[string]domainstudio.AssetEntry{}
	for _, a := range assets {
		if a.Kind == domainstudio.AssetCharacter && (a.VoiceID != "" || a.VoiceProvider != "") {
			voiceBySpeaker[strings.ToLower(strings.TrimSpace(a.Name))] = a
			voiceBySpeaker[strings.ToLower(strings.TrimSpace(a.CodeTag))] = a
		}
	}

	completed, failed := 0, 0
	var lastErr error
	for idx, shot := range targets {
		if ctx.Err() != nil {
			return contracts.StudioRunStageResult{}, ctx.Err()
		}
		shotModel, shotVoice := resolveShotVoiceConfig(shot, p.Model, proj.DefaultTTSModel, p.Voice, voiceBySpeaker)
		s.reportBatchProgress(ctx, proj.ID, ep.ID, string(p.Stage), idx+1, len(targets), completed, failed, shot.ID, shot.Title, fmt.Sprintf("Synthesizing TTS for shot #%d", shot.Seq), contracts.BatchStatusRunning)
		reqBytes, merr := json.Marshal(contracts.TTSGenerateInput{
			Text:      shotSpeechText(shot),
			Model:     shotModel,
			Voice:     shotVoice,
			OutputDir: outDir,
		})
		if merr != nil {
			failed++
			lastErr = apperr.Internal(merr)
			continue
		}
		rawOut, rerr := s.wf.RunNode(ctx, contracts.NodeTypeTTSGenerate, reqBytes)
		if rerr != nil {
			failed++
			lastErr = rerr
			continue
		}
		var out contracts.TTSGenerateOutput
		if merr := decodeNodeOutput(rawOut, &out); merr != nil {
			failed++
			lastErr = merr
			continue
		}
		shot.AudioPath = out.Path
		if regID := s.registerMedia(ctx, "audio", contracts.NodeTypeTTSGenerate, out.Path, outDir, 0); regID != "" {
			shot.AudioAssetID = regID
		}
		if shot.Status == domainstudio.ShotPending || shot.Status == domainstudio.ShotKeyframeReady {
			shot.Status = domainstudio.ShotTTSReady
		}
		if _, uerr := s.repo.UpsertShot(ctx, shot); uerr != nil {
			s.log.WarnContext(ctx, "studio: failed to update shot audio metadata", "shot_id", shot.ID, "error", uerr)
			failed++
			lastErr = uerr
			continue
		}
		s.recordSelectedVersion(ctx, proj.ID, ep.ID, domainstudio.TargetDubAudio, shot.ID, out.Path, contracts.NodeTypeTTSGenerate, shotSpeechText(shot), shotModel)
		completed++
	}

	if refreshed, lerr := s.repo.ListShots(ctx, ep.ID); lerr == nil {
		s.warnDiskMirror(ctx, "shots.json", s.syncEpisodeShotsToDisk(ctx, proj, ep, refreshed))
	} else {
		s.log.WarnContext(ctx, "studio: failed to list shots after tts batch", "episode_id", ep.ID, "error", lerr)
	}
	status := contracts.BatchStatusCompleted
	if failed > 0 && completed == 0 {
		status = contracts.BatchStatusFailed
	}
	s.reportBatchProgress(ctx, proj.ID, ep.ID, string(p.Stage), len(targets), len(targets), completed, failed, "", "", fmt.Sprintf("TTS: %d ok, %d failed", completed, failed), status)

	detail, err := s.GetEpisode(ctx, ep.ID)
	if err != nil {
		return contracts.StudioRunStageResult{}, err
	}
	s.emit(contracts.EventStudioEpisodeUpdated, contracts.StudioEpisodeUpdatedEvent{
		ProjectID: proj.ID,
		EpisodeID: ep.ID,
		Stage:     detail.Episode.Stage,
		Reason:    "tts_generated",
	})
	if failed > 0 && completed == 0 && lastErr != nil {
		return contracts.StudioRunStageResult{}, lastErr
	}
	return contracts.StudioRunStageResult{
		Episode:        detail,
		CompletedCount: completed,
		FailedCount:    failed,
		Summary:        fmt.Sprintf("Synthesized %d audio clip(s), %d failed", completed, failed),
	}, nil
}

func (s *Service) runProbeShots(ctx context.Context, proj domainstudio.Project, ep domainstudio.Episode, p contracts.StudioEpisodeRunStagePayload) (contracts.StudioRunStageResult, error) {
	shots, err := s.repo.ListShots(ctx, ep.ID)
	if err != nil {
		return contracts.StudioRunStageResult{}, err
	}
	filter := toSet(p.ShotIDs)
	targets := make([]domainstudio.Shot, 0, len(shots))
	for _, sh := range shots {
		if len(filter) > 0 && !filter[sh.ID] {
			continue
		}
		if strings.TrimSpace(sh.VideoPath) == "" {
			continue
		}
		targets = append(targets, sh)
	}

	probed, failed := 0, 0
	var lastErr error
	for idx, sh := range targets {
		if ctx.Err() != nil {
			return contracts.StudioRunStageResult{}, ctx.Err()
		}
		s.reportBatchProgress(ctx, proj.ID, ep.ID, string(p.Stage), idx+1, len(targets), probed, failed, sh.ID, sh.Title, fmt.Sprintf("Probing shot #%d media", sh.Seq), contracts.BatchStatusRunning)

		inBytes, merr := json.Marshal(contracts.ProbeMediaInput{
			Media: engine.ImageInputRef{MediaRef: engine.RefFor(sh.VideoPath, engine.MediaVideo)},
		})
		if merr != nil {
			failed++
			lastErr = apperr.Internal(merr)
			continue
		}
		rawOut, rerr := s.wf.RunNode(ctx, contracts.NodeTypeProbeMedia, inBytes)
		if rerr != nil {
			failed++
			lastErr = rerr
			continue
		}
		var out contracts.ProbeMediaOutput
		if merr := decodeNodeOutput(rawOut, &out); merr != nil {
			failed++
			lastErr = merr
			continue
		}
		if out.DurationSec > 0 && int(out.DurationSec+0.5) >= domainstudio.MinShotDurationSec && int(out.DurationSec+0.5) <= s.cfg.MaxShotDurationSec {
			sh.DurationSec = int(out.DurationSec + 0.5)
			if _, uerr := s.repo.UpsertShot(ctx, sh); uerr != nil {
				s.log.WarnContext(ctx, "studio: failed to update probed shot duration", "shot_id", sh.ID, "error", uerr)
			}
		}
		probed++
	}

	status := contracts.BatchStatusCompleted
	if failed > 0 && probed == 0 {
		status = contracts.BatchStatusFailed
	}
	s.reportBatchProgress(ctx, proj.ID, ep.ID, string(p.Stage), len(targets), len(targets), probed, failed, "", "", fmt.Sprintf("Probed: %d ok, %d failed", probed, failed), status)

	detail, err := s.GetEpisode(ctx, ep.ID)
	if err != nil {
		return contracts.StudioRunStageResult{}, err
	}
	if failed > 0 && probed == 0 && lastErr != nil {
		return contracts.StudioRunStageResult{}, lastErr
	}
	return contracts.StudioRunStageResult{
		Episode:        detail,
		CompletedCount: probed,
		FailedCount:    failed,
		Summary:        fmt.Sprintf("Probed %d video shot(s)", probed),
	}, nil
}

func (s *Service) runConcatVideo(ctx context.Context, proj domainstudio.Project, ep domainstudio.Episode, epRoot string, p contracts.StudioEpisodeRunStagePayload) (contracts.StudioRunStageResult, error) {
	shots, err := s.repo.ListShots(ctx, ep.ID)
	if err != nil {
		return contracts.StudioRunStageResult{}, err
	}
	filter := toSet(p.ShotIDs)
	type clipItem struct {
		Path        string  `json:"path"`
		DurationSec float64 `json:"duration_sec,omitempty"`
	}
	clips := make([]clipItem, 0, len(shots))
	narFallback := strings.TrimSpace(p.NarrationAudioPath)
	for _, sh := range shots {
		if len(filter) > 0 && !filter[sh.ID] {
			continue
		}
		if strings.TrimSpace(sh.VideoPath) == "" {
			continue
		}
		clips = append(clips, clipItem{
			Path:        sh.VideoPath,
			DurationSec: float64(sh.DurationSec),
		})
		if narFallback == "" && proj.Genre == domainstudio.GenreNarration && strings.TrimSpace(sh.AudioPath) != "" {
			narFallback = sh.AudioPath
		}
	}
	if len(clips) == 0 {
		return contracts.StudioRunStageResult{}, apperr.Validation("no generated shot videos found to assemble").
			WithMsg("errors.studio.no_shots_to_assemble", nil)
	}

	manifestBytes, err := json.Marshal(clips)
	if err != nil {
		return contracts.StudioRunStageResult{}, apperr.Internal(err)
	}
	outDir := ""
	if epRoot != "" {
		outDir = filepath.Join(epRoot, "exports")
		if err := s.ensureGuardedDir(ctx, outDir); err != nil {
			return contracts.StudioRunStageResult{}, err
		}
	}

	resolution := strings.TrimSpace(p.Resolution)
	if resolution == "" && strings.TrimSpace(proj.AspectRatio) != "" {
		resolution = resolutionForAspectRatio(proj.AspectRatio, s.cfg.DefaultResolution)
	}

	inBytes, err := json.Marshal(contracts.VideoConcatBasicInput{
		ClipsManifestJSON:  string(manifestBytes),
		OutputDir:          outDir,
		NarrationAudioPath: narFallback,
		Resolution:         resolution,
		FPS:                p.FPS,
	})
	if err != nil {
		return contracts.StudioRunStageResult{}, apperr.Internal(err)
	}
	rawOut, err := s.wf.RunNode(ctx, contracts.NodeTypeVideoConcatBasic, inBytes)
	if err != nil {
		return contracts.StudioRunStageResult{}, err
	}
	var out contracts.VideoConcatBasicOutput
	if err := decodeNodeOutput(rawOut, &out); err != nil {
		return contracts.StudioRunStageResult{}, err
	}

	ep.Stage = domainstudio.StageExport
	if _, err := s.repo.UpdateEpisode(ctx, ep); err != nil {
		return contracts.StudioRunStageResult{}, err
	}
	rawText := s.readGuardedStringFile(ctx, epRoot, ep.RawScriptPath)
	normPtr := s.readGuardedNormalizedScriptDomain(ctx, epRoot, ep.ScriptJSONPath)
	s.warnDiskMirror(ctx, "episode.json", s.persistEpisodeToDisk(ctx, proj, ep, rawText, normPtr, shots, out.OutputPath, out.AssetID))

	detail, err := s.GetEpisode(ctx, ep.ID)
	if err != nil {
		return contracts.StudioRunStageResult{}, err
	}
	s.emit(contracts.EventStudioEpisodeUpdated, contracts.StudioEpisodeUpdatedEvent{
		ProjectID: proj.ID,
		EpisodeID: ep.ID,
		Stage:     detail.Episode.Stage,
		Reason:    "video_assembled",
	})
	return contracts.StudioRunStageResult{
		Episode:        detail,
		OutputPath:     out.OutputPath,
		OutputAssetID:  out.AssetID,
		CompletedCount: out.ClipCount,
		Summary:        fmt.Sprintf("Assembled %d clips (%.1fs)", out.ClipCount, out.DurationSec),
	}, nil
}

func (s *Service) registerMedia(ctx context.Context, kind, sourceNode, filePath, rootDir string, durSec float64) string {
	if s.media == nil || strings.TrimSpace(filePath) == "" {
		return ""
	}
	regRoot := strings.TrimSpace(rootDir)
	if regRoot == "" {
		regRoot = filepath.Dir(filePath)
	}
	s.media.AddAllowedRoot(regRoot)
	item, err := s.media.RegisterAsset(ctx, media.RegisterAssetParams{
		Kind:           kind,
		SourceNode:     sourceNode,
		FilePath:       filePath,
		RegisteredRoot: regRoot,
		DurationSec:    durSec,
	})
	if err != nil || item == nil {
		return ""
	}
	return item.ID
}

func (s *Service) reportBatchProgress(ctx context.Context, projectID, episodeID, stage string, cur, total, completed, failed int, targetID, itemLabel, msg string, status contracts.StudioBatchStatus) {
	var pct float64
	if total > 0 {
		pct = float64(cur) / float64(total) * 100.0
	}
	msgKey := "studio.progress.batch_running"
	switch status {
	case contracts.BatchStatusCompleted:
		msgKey = "studio.progress.batch_done"
	case contracts.BatchStatusFailed:
		msgKey = "studio.progress.batch_failed"
	}
	msgParams := map[string]string{
		"stage":     stage,
		"current":   strconv.Itoa(cur),
		"total":     strconv.Itoa(total),
		"completed": strconv.Itoa(completed),
		"failed":    strconv.Itoa(failed),
		"item":      itemLabel,
	}
	progress.Report(ctx, progress.Update{
		Percent: pct,
		Message: msg,
		Key:     msgKey,
		Params:  msgParams,
	})
	s.emit(contracts.EventStudioBatchProgress, contracts.StudioBatchProgressEvent{
		ProjectID: projectID,
		EpisodeID: episodeID,
		Stage:     stage,
		Current:   cur,
		Total:     total,
		TargetID:  targetID,
		Percent:   pct,
		Message:   msg,
		MsgKey:    msgKey,
		MsgParams: msgParams,
		Status:    status,
	})
}

func (s *Service) resolveProjectRoot(proj domainstudio.Project) string {
	if strings.TrimSpace(proj.RootDir) != "" {
		return filepath.Clean(proj.RootDir)
	}
	if pDir := s.ProjectsDir(); pDir != "" && proj.Slug != "" {
		return filepath.Join(pDir, proj.Slug)
	}
	return ""
}

func (s *Service) resolveEpisodeRoot(proj domainstudio.Project, epCode string) string {
	projRoot := s.resolveProjectRoot(proj)
	if projRoot == "" || strings.TrimSpace(epCode) == "" {
		return ""
	}
	return filepath.Join(projRoot, "episodes", epCode)
}

func (s *Service) warnDiskMirror(ctx context.Context, artifact string, err error) {
	if err != nil {
		s.log.WarnContext(ctx, "studio: disk workspace mirror skipped or failed", "artifact", artifact, "error", err)
	}
}

func (s *Service) ensureGuardedProjectDirs(ctx context.Context, projRoot string) error {
	if strings.TrimSpace(projRoot) == "" {
		return nil
	}
	clean := filepath.Clean(projRoot)
	if err := engine.CheckFile(ctx, clean, true); err != nil {
		return err
	}
	if err := ensureProjectDirs(clean); err != nil {
		return apperr.Wrap(apperr.CodeUnavailable, "Failed to initialize project workspace directories", err).
			WithMsg("errors.studio.io_failed", nil)
	}
	return nil
}

func (s *Service) ensureGuardedEpisodeDirs(ctx context.Context, epRoot string) error {
	if strings.TrimSpace(epRoot) == "" {
		return nil
	}
	clean := filepath.Clean(epRoot)
	if err := engine.CheckFile(ctx, clean, true); err != nil {
		return err
	}
	if err := ensureEpisodeDirs(clean); err != nil {
		return apperr.Wrap(apperr.CodeUnavailable, "Failed to initialize episode workspace directories", err).
			WithMsg("errors.studio.io_failed", nil)
	}
	return nil
}

func (s *Service) ensureGuardedDir(ctx context.Context, dir string) error {
	if strings.TrimSpace(dir) == "" {
		return nil
	}
	clean := filepath.Clean(dir)
	if err := engine.CheckFile(ctx, clean, true); err != nil {
		return err
	}
	if err := os.MkdirAll(clean, 0o755); err != nil {
		return apperr.Wrap(apperr.CodeUnavailable, "Failed to prepare workspace directory", err).
			WithMsg("errors.studio.io_failed", nil)
	}
	return nil
}

func (s *Service) writeGuardedFile(ctx context.Context, rootDir, targetPath string, data []byte, perm os.FileMode) error {
	cleanRoot := filepath.Clean(strings.TrimSpace(rootDir))
	cleanTarget := filepath.Clean(strings.TrimSpace(targetPath))
	if strings.TrimSpace(rootDir) == "" || strings.TrimSpace(targetPath) == "" || !isWithinBaseDir(cleanRoot, cleanTarget) {
		return apperr.Validation("workspace artifact path escapes project directory").
			WithMsg("errors.studio.path_outside_workspace", nil)
	}
	if err := engine.CheckFile(ctx, cleanTarget, true); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(cleanTarget), 0o755); err != nil {
		return apperr.Wrap(apperr.CodeUnavailable, "Failed to create workspace artifact directory", err).
			WithMsg("errors.studio.io_failed", nil)
	}
	if err := fsutil.WriteFileAtomic(cleanTarget, data, perm); err != nil {
		return apperr.Wrap(apperr.CodeUnavailable, "Failed to write workspace artifact file", err).
			WithMsg("errors.studio.io_failed", nil)
	}
	return nil
}

func (s *Service) readGuardedBytes(ctx context.Context, rootDir, targetPath string) []byte {
	trimmed := strings.TrimSpace(targetPath)
	if trimmed == "" {
		return nil
	}
	clean := filepath.Clean(trimmed)
	if strings.TrimSpace(rootDir) != "" && !isWithinBaseDir(rootDir, clean) {
		s.log.WarnContext(ctx, "studio: blocked workspace file read outside episode root", "error", "outside_root")
		return nil
	}
	if err := engine.CheckFile(ctx, clean, false); err != nil {
		return nil
	}
	b, err := os.ReadFile(clean)
	if err != nil {
		return nil
	}
	return b
}

func (s *Service) readGuardedStringFile(ctx context.Context, rootDir, targetPath string) string {
	b := s.readGuardedBytes(ctx, rootDir, targetPath)
	if b == nil {
		return ""
	}
	return string(b)
}

func (s *Service) readGuardedNormalizedScriptDomain(ctx context.Context, rootDir, targetPath string) *domainstudio.NormalizedScript {
	b := s.readGuardedBytes(ctx, rootDir, targetPath)
	if len(b) == 0 {
		return nil
	}
	var ns domainstudio.NormalizedScript
	if json.Unmarshal(b, &ns) != nil {
		return nil
	}
	return &ns
}

func (s *Service) readGuardedNormalizedScriptDTO(ctx context.Context, rootDir, targetPath string) *contracts.StudioNormalizedScript {
	ns := s.readGuardedNormalizedScriptDomain(ctx, rootDir, targetPath)
	if ns == nil {
		return nil
	}
	dto := normalizedScriptToDTO(*ns)
	return &dto
}

func (s *Service) persistProjectToDisk(ctx context.Context, proj domainstudio.Project, assets []domainstudio.AssetEntry) error {
	projRoot := s.resolveProjectRoot(proj)
	if projRoot == "" {
		return nil
	}
	if err := s.ensureGuardedProjectDirs(ctx, projRoot); err != nil {
		return err
	}
	if pb, err := json.MarshalIndent(proj, "", "  "); err != nil {
		return apperr.Internal(err)
	} else if werr := s.writeGuardedFile(ctx, projRoot, filepath.Join(projRoot, "project.json"), append(pb, '\n'), 0o644); werr != nil {
		return werr
	}
	if ab, err := json.MarshalIndent(domainstudio.AssetBiblePackage{
		StylePrompt: proj.StylePrompt,
		Assets:      assets,
	}, "", "  "); err != nil {
		return apperr.Internal(err)
	} else if werr := s.writeGuardedFile(ctx, projRoot, filepath.Join(projRoot, "assets", "asset_bible.json"), append(ab, '\n'), 0o644); werr != nil {
		return werr
	}
	return nil
}

func (s *Service) syncEpisodeShotsToDisk(ctx context.Context, proj domainstudio.Project, ep domainstudio.Episode, shots []domainstudio.Shot) error {
	epRoot := s.resolveEpisodeRoot(proj, ep.Code)
	if epRoot == "" {
		return nil
	}
	if err := s.ensureGuardedEpisodeDirs(ctx, epRoot); err != nil {
		return err
	}
	pkg := domainstudio.ShotPromptPackage{
		EpisodeTitle:   ep.Title,
		ProductionMode: proj.ProductionMode,
		Shots:          shots,
	}
	data, err := json.MarshalIndent(pkg, "", "  ")
	if err != nil {
		return apperr.Internal(err)
	}
	target := ep.ShotsJSONPath
	if target == "" {
		target = filepath.Join(epRoot, "storyboard", "shots.json")
	}
	return s.writeGuardedFile(ctx, epRoot, target, append(data, '\n'), 0o644)
}

func (s *Service) persistEpisodeToDisk(ctx context.Context, proj domainstudio.Project, ep domainstudio.Episode, rawScript string, normScript *domainstudio.NormalizedScript, shots []domainstudio.Shot, finalPath, finalAssetID string) error {
	epRoot := s.resolveEpisodeRoot(proj, ep.Code)
	if epRoot == "" {
		return nil
	}
	if err := s.ensureGuardedEpisodeDirs(ctx, epRoot); err != nil {
		return err
	}
	meta := episodeDiskMeta{
		Episode:           ep,
		FinalVideoPath:    finalPath,
		FinalVideoAssetID: finalAssetID,
	}
	if mb, err := json.MarshalIndent(meta, "", "  "); err != nil {
		return apperr.Internal(err)
	} else if werr := s.writeGuardedFile(ctx, epRoot, filepath.Join(epRoot, "episode.json"), append(mb, '\n'), 0o644); werr != nil {
		return werr
	}
	if rawScript != "" && ep.RawScriptPath != "" {
		if werr := s.writeGuardedFile(ctx, epRoot, ep.RawScriptPath, []byte(rawScript), 0o644); werr != nil {
			return werr
		}
	}
	if normScript != nil && ep.ScriptJSONPath != "" {
		if sb, err := json.MarshalIndent(normScript, "", "  "); err != nil {
			return apperr.Internal(err)
		} else if werr := s.writeGuardedFile(ctx, epRoot, ep.ScriptJSONPath, append(sb, '\n'), 0o644); werr != nil {
			return werr
		}
	}
	if len(shots) > 0 {
		if werr := s.syncEpisodeShotsToDisk(ctx, proj, ep, shots); werr != nil {
			return werr
		}
	}
	return nil
}

func (s *Service) readEpisodeDiskMeta(ctx context.Context, epRoot string) *episodeDiskMeta {
	if epRoot == "" {
		return nil
	}
	b := s.readGuardedBytes(ctx, epRoot, filepath.Join(epRoot, "episode.json"))
	if len(b) == 0 {
		return nil
	}
	var m episodeDiskMeta
	if json.Unmarshal(b, &m) != nil {
		return nil
	}
	m.FinalVideoPath = s.sanitizeSyncedPath(ctx, epRoot, m.Episode.ProjectID, m.FinalVideoPath)
	if m.FinalVideoPath == "" {
		m.FinalVideoAssetID = ""
	}
	return &m
}

func (s *Service) allocateUniqueSlug(ctx context.Context, base string) (string, error) {
	projects, err := s.repo.ListProjects(ctx)
	if err != nil {
		return "", err
	}
	used := make(map[string]bool, len(projects))
	for _, pr := range projects {
		used[strings.ToLower(pr.Slug)] = true
	}
	if !used[strings.ToLower(base)] {
		return base, nil
	}
	for i := 2; i < 1000; i++ {
		candidate := fmt.Sprintf("%s-%d", base, i)
		if !used[strings.ToLower(candidate)] {
			return candidate, nil
		}
	}
	return fmt.Sprintf("%s-%d", base, time.Now().Unix()), nil
}

func ensureProjectDirs(projRoot string) error {
	for _, d := range []string{projRoot, filepath.Join(projRoot, "assets"), filepath.Join(projRoot, "episodes")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	return nil
}

func ensureEpisodeDirs(epRoot string) error {
	for _, sub := range []string{
		epRoot,
		filepath.Join(epRoot, "script"),
		filepath.Join(epRoot, "assets"),
		filepath.Join(epRoot, "storyboard"),
		filepath.Join(epRoot, "media", "assets"),
		filepath.Join(epRoot, "media", "keyframes"),
		filepath.Join(epRoot, "media", "videos"),
		filepath.Join(epRoot, "media", "tts"),
		filepath.Join(epRoot, "media", "thumbs"),
		filepath.Join(epRoot, "timeline"),
		filepath.Join(epRoot, "exports"),
	} {
		if err := os.MkdirAll(sub, 0o755); err != nil {
			return err
		}
	}
	return nil
}

func buildAssetPathMap(assets []domainstudio.AssetEntry) map[string]string {
	m := make(map[string]string, len(assets))
	for _, a := range assets {
		if strings.TrimSpace(a.PrimaryPath) == "" || strings.TrimSpace(a.CodeTag) == "" {
			continue
		}
		if _, err := os.Stat(a.PrimaryPath); err == nil {
			m[strings.ToLower(a.CodeTag)] = a.PrimaryPath
		}
	}
	return m
}

func collectRefImages(tags []string, tagPaths map[string]string) engine.ImageInputRefs {
	if len(tags) == 0 || len(tagPaths) == 0 {
		return nil
	}
	seen := make(map[string]bool)
	var out engine.ImageInputRefs
	for _, tag := range tags {
		norm := strings.ToLower(domainstudio.NormalizeCodeTag(tag, ""))
		if p, ok := tagPaths[norm]; ok && !seen[p] {
			seen[p] = true
			out = append(out, engine.RefFor(p, engine.MediaImage))
		}
	}
	return out
}

func composePrompt(stylePrompt, corePrompt string) string {
	style := strings.TrimSpace(stylePrompt)
	core := strings.TrimSpace(corePrompt)
	if style == "" {
		return core
	}
	if core == "" {
		return style
	}
	return core + ", visual style: " + style
}

func shotSpeechText(sh domainstudio.Shot) string {
	if t := strings.TrimSpace(sh.NarrationText); t != "" {
		return t
	}
	if len(sh.Dialogues) == 0 {
		return ""
	}
	parts := make([]string, 0, len(sh.Dialogues))
	for _, d := range sh.Dialogues {
		line := strings.TrimSpace(d.Line)
		if line != "" {
			parts = append(parts, line)
		}
	}
	return strings.Join(parts, " ")
}

func resolutionForAspectRatio(aspect, fallback string) string {
	return domainstudio.ResolutionForAspectRatio(aspect, fallback)
}

func decodeNodeOutput(raw any, dst any) error {
	b, err := json.Marshal(raw)
	if err != nil {
		return apperr.Wrap(apperr.CodeIntegrity, "Invalid workflow node output", err).
			WithMsg("errors.studio.invalid_node_output", nil)
	}
	if err := json.Unmarshal(b, dst); err != nil {
		return apperr.Wrap(apperr.CodeIntegrity, "Invalid workflow node output", err).
			WithMsg("errors.studio.invalid_node_output", nil)
	}
	return nil
}

func isDirectOrSubpath(parent, child string) bool {
	pAbs, err1 := filepath.Abs(parent)
	cAbs, err2 := filepath.Abs(child)
	if err1 != nil || err2 != nil {
		return false
	}
	rel, err := filepath.Rel(pAbs, cAbs)
	if err != nil || rel == "" || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	return true
}

func toSet(items []string) map[string]bool {
	if len(items) == 0 {
		return nil
	}
	m := make(map[string]bool, len(items))
	for _, it := range items {
		if t := strings.TrimSpace(it); t != "" {
			m[t] = true
		}
	}
	return m
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if t := strings.TrimSpace(v); t != "" {
			return t
		}
	}
	return ""
}

func removeDirWithRetry(dir string) error {
	var err error
	for attempt := range 5 {
		err = os.RemoveAll(dir)
		if err == nil {
			return nil
		}
		if attempt < 4 {
			time.Sleep(time.Duration(attempt+1) * 40 * time.Millisecond)
		}
	}
	return err
}
