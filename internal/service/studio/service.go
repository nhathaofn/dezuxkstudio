// Package studio orchestrates DezuxkStudio's AI film production projects,
// Hybrid SQLite <-> workspace JSON synchronization, and Phase 1 stage execution.
package studio

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
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
	mediagennodes "app/internal/tools/mediagen"
	studionodes "app/internal/tools/studio"
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
	Repo         domainstudio.Repository
	Workflow     WorkflowRunner
	Media        MediaRegistrar
	VideoMedia   VideoQueueMedia
	FileGuard    engine.FileGuard
	StudioConfig config.StudioConfig
	ProjectsDir  string
	OnEvent      func(eventName string, data any)
	Logger       *slog.Logger
}

// Service manages projects, episodes, asset bibles, shots, workspace sync, and stage execution.
type Service struct {
	repo       domainstudio.Repository
	wf         WorkflowRunner
	media      MediaRegistrar
	videoMedia VideoQueueMedia
	fileGuard  engine.FileGuard
	cfg        config.StudioConfig
	onEvent    func(eventName string, data any)
	log        *slog.Logger

	mu          sync.RWMutex
	projectsDir string

	runMu      sync.Mutex
	runningEps map[string]bool

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
	cfg := opts.StudioConfig
	def := config.Default(config.ModeDesktop).Studio
	if cfg.DefaultFPS <= 0 {
		cfg.DefaultFPS = def.DefaultFPS
	}
	if strings.TrimSpace(cfg.DefaultResolution) == "" {
		cfg.DefaultResolution = def.DefaultResolution
	}
	if cfg.MaxConcurrentBatch <= 0 {
		cfg.MaxConcurrentBatch = def.MaxConcurrentBatch
	}
	if cfg.QueueWorkers <= 0 {
		cfg.QueueWorkers = def.QueueWorkers
	}
	if cfg.QueuePollInterval <= 0 {
		cfg.QueuePollInterval = def.QueuePollInterval
	}
	if cfg.QueueMaxAttempts <= 0 {
		cfg.QueueMaxAttempts = def.QueueMaxAttempts
	}

	projectsDir := strings.TrimSpace(opts.ProjectsDir)
	if projectsDir != "" {
		projectsDir = filepath.Clean(projectsDir)
		_ = os.MkdirAll(projectsDir, 0o755)
	}

	return &Service{
		repo:        opts.Repo,
		wf:          opts.Workflow,
		media:       opts.Media,
		videoMedia:  opts.VideoMedia,
		fileGuard:   opts.FileGuard,
		cfg:         cfg,
		onEvent:     opts.OnEvent,
		log:         log,
		projectsDir: projectsDir,
		runningEps:  make(map[string]bool),
		wakeCh:      make(chan struct{}, 1),
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
	_ = os.MkdirAll(clean, 0o755)

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
		return contracts.StudioProjectListResult{}, apperr.Unavailable("studio repository is unavailable")
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
		return contracts.StudioProjectDetailResult{}, apperr.Unavailable("studio repository is unavailable")
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
		if err := ensureProjectDirs(projRoot); err != nil {
			return contracts.StudioProjectDetailResult{}, apperr.Internal(err)
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

	_ = s.persistProjectToDisk(proj, nil)

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
		Action:    "created",
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
		return contracts.StudioProjectDetailResult{}, apperr.Unavailable("studio repository is unavailable")
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
		return contracts.StudioProjectDetailResult{}, apperr.Unavailable("studio repository is unavailable")
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
	assets, _ := s.repo.ListAssets(ctx, updated.ID)
	_ = s.persistProjectToDisk(updated, assets)

	dtoProj := projectToDTO(updated)
	s.emit(contracts.EventStudioProjectChanged, contracts.StudioProjectChangedEvent{
		ProjectID: updated.ID,
		Action:    "updated",
		Project:   &dtoProj,
	})
	return s.GetProject(ctx, updated.ID)
}

// DeleteProject removes the project from SQLite and optionally removes its folder when safely inside ProjectsDir.
func (s *Service) DeleteProject(ctx context.Context, p contracts.StudioProjectDeletePayload) error {
	if s.repo == nil {
		return apperr.Unavailable("studio repository is unavailable")
	}
	if err := p.Validate(); err != nil {
		return err
	}
	proj, err := s.repo.GetProject(ctx, strings.TrimSpace(p.ID))
	if err != nil {
		return err
	}
	if err := s.repo.DeleteProject(ctx, proj.ID); err != nil {
		return err
	}
	if p.DeleteFiles && proj.RootDir != "" {
		if pDir := s.ProjectsDir(); pDir != "" && isDirectOrSubpath(pDir, proj.RootDir) {
			_ = os.RemoveAll(proj.RootDir)
		}
	}
	s.emit(contracts.EventStudioProjectChanged, contracts.StudioProjectChangedEvent{
		ProjectID: proj.ID,
		Action:    "deleted",
	})
	return nil
}

// CreateEpisode creates a new episode in a project and initializes its workspace files.
func (s *Service) CreateEpisode(ctx context.Context, p contracts.StudioEpisodeCreatePayload) (contracts.StudioEpisodeDetailResult, error) {
	if s.repo == nil {
		return contracts.StudioEpisodeDetailResult{}, apperr.Unavailable("studio repository is unavailable")
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
	var rawPath, scriptJSONPath, assetsJSONPath, shotsJSONPath string
	if epRoot != "" {
		if err := ensureEpisodeDirs(epRoot); err != nil {
			return contracts.StudioEpisodeDetailResult{}, apperr.Internal(err)
		}
		rawPath = filepath.Join(epRoot, "script", "raw_screenplay.txt")
		scriptJSONPath = filepath.Join(epRoot, "script", "normalized_script.json")
		assetsJSONPath = filepath.Join(epRoot, "assets", "asset_bible.json")
		shotsJSONPath = filepath.Join(epRoot, "storyboard", "shots.json")
	}

	ep, err := s.repo.CreateEpisode(ctx, domainstudio.Episode{
		ProjectID:      proj.ID,
		Code:           code,
		Title:          title,
		Stage:          domainstudio.StageScript,
		RawScriptPath:  rawPath,
		ScriptJSONPath: scriptJSONPath,
		AssetsJSONPath: assetsJSONPath,
		ShotsJSONPath:  shotsJSONPath,
	})
	if err != nil {
		return contracts.StudioEpisodeDetailResult{}, err
	}

	if epRoot != "" {
		_ = s.persistEpisodeToDisk(proj, ep, "", nil, nil, "", "")
	}
	return s.GetEpisode(ctx, ep.ID)
}

// GetEpisode returns full episode details including script, asset bible, shots, and latest export.
func (s *Service) GetEpisode(ctx context.Context, episodeID string) (contracts.StudioEpisodeDetailResult, error) {
	if s.repo == nil {
		return contracts.StudioEpisodeDetailResult{}, apperr.Unavailable("studio repository is unavailable")
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

	rawScript := readStringFile(ep.RawScriptPath)
	normScript := readNormalizedScriptDTO(ep.ScriptJSONPath)
	var finalPath, finalAssetID string
	if meta := s.readEpisodeDiskMeta(s.resolveEpisodeRoot(proj, ep.Code)); meta != nil {
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
		return contracts.StudioEpisodeDetailResult{}, apperr.Unavailable("studio repository is unavailable")
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
		if err := ensureEpisodeDirs(epRoot); err != nil {
			return contracts.StudioEpisodeDetailResult{}, apperr.Internal(err)
		}
		if ep.RawScriptPath == "" {
			ep.RawScriptPath = filepath.Join(epRoot, "script", "raw_screenplay.txt")
		}
		if ep.ScriptJSONPath == "" {
			ep.ScriptJSONPath = filepath.Join(epRoot, "script", "normalized_script.json")
		}
	}

	rawText := readStringFile(ep.RawScriptPath)
	if p.RawScript != nil {
		rawText = *p.RawScript
		if ep.RawScriptPath != "" {
			if err := fsutil.WriteFileAtomic(ep.RawScriptPath, []byte(rawText), 0o644); err != nil {
				return contracts.StudioEpisodeDetailResult{}, apperr.Internal(err)
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
			if err := fsutil.WriteFileAtomic(ep.ScriptJSONPath, append(data, '\n'), 0o644); err != nil {
				return contracts.StudioEpisodeDetailResult{}, apperr.Internal(err)
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

	shots, _ := s.repo.ListShots(ctx, updatedEp.ID)
	meta := s.readEpisodeDiskMeta(epRoot)
	var finalPath, finalAssetID string
	if meta != nil {
		finalPath = meta.FinalVideoPath
		finalAssetID = meta.FinalVideoAssetID
	}
	_ = s.persistEpisodeToDisk(proj, updatedEp, rawText, normDomain, shots, finalPath, finalAssetID)

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
		return contracts.StudioAssetUpsertResult{}, apperr.Unavailable("studio repository is unavailable")
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
		assets, _ := s.repo.ListAssets(ctx, existing.ProjectID)
		if proj, perr := s.repo.GetProject(ctx, existing.ProjectID); perr == nil {
			_ = s.persistProjectToDisk(proj, assets)
			dtoProj := projectToDTO(proj)
			s.emit(contracts.EventStudioProjectChanged, contracts.StudioProjectChangedEvent{
				ProjectID: proj.ID,
				Action:    "updated",
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
	assets, _ := s.repo.ListAssets(ctx, proj.ID)
	_ = s.persistProjectToDisk(proj, assets)

	dtoProj := projectToDTO(proj)
	s.emit(contracts.EventStudioProjectChanged, contracts.StudioProjectChangedEvent{
		ProjectID: proj.ID,
		Action:    "updated",
		Project:   &dtoProj,
	})
	dtoAsset := assetToDTO(saved)
	return contracts.StudioAssetUpsertResult{Asset: &dtoAsset, Assets: assetsToDTO(assets)}, nil
}

// UpsertShot creates or updates an episode shot and syncs storyboard/shots.json.
func (s *Service) UpsertShot(ctx context.Context, p contracts.StudioShotUpsertPayload) (contracts.StudioShotUpsertResult, error) {
	if s.repo == nil {
		return contracts.StudioShotUpsertResult{}, apperr.Unavailable("studio repository is unavailable")
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
		shots, _ := s.repo.ListShots(ctx, ep.ID)
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

	saved, err := s.repo.UpsertShot(ctx, cur)
	if err != nil {
		return contracts.StudioShotUpsertResult{}, err
	}
	shots, _ := s.repo.ListShots(ctx, ep.ID)
	_ = s.syncEpisodeShotsToDisk(proj, ep, shots)

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
		return contracts.StudioShotListResult{}, apperr.Unavailable("studio repository is unavailable")
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
	_ = s.syncEpisodeShotsToDisk(proj, ep, reordered)
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
		return nil, apperr.Unavailable("studio repository is unavailable")
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
	entries, err := os.ReadDir(pDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, apperr.Internal(err)
	}

	var firstSynced *contracts.StudioProjectDetailResult
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		projRoot := filepath.Join(pDir, entry.Name())
		projFile := filepath.Join(projRoot, "project.json")
		data, rerr := os.ReadFile(projFile)
		if rerr != nil {
			continue
		}
		var diskProj domainstudio.Project
		if jerr := json.Unmarshal(data, &diskProj); jerr != nil {
			continue
		}
		if diskProj.Slug == "" {
			diskProj.Slug = entry.Name()
		}
		if diskProj.Title == "" {
			diskProj.Title = entry.Name()
		}
		diskProj.RootDir = projRoot

		existing, gerr := s.repo.GetProject(ctx, diskProj.ID)
		if gerr != nil {
			existing, gerr = s.repo.GetProject(ctx, diskProj.Slug)
		}
		if gerr != nil {
			created, cerr := s.repo.CreateProject(ctx, diskProj)
			if cerr != nil {
				continue
			}
			existing = created
		} else {
			existing.Title = diskProj.Title
			existing.StylePrompt = diskProj.StylePrompt
			existing.AspectRatio = diskProj.AspectRatio
			existing.Genre = diskProj.Genre
			existing.ProductionMode = diskProj.ProductionMode
			if diskProj.RootDir != "" {
				existing.RootDir = diskProj.RootDir
			}
			if upd, uerr := s.repo.UpdateProject(ctx, existing); uerr == nil {
				existing = upd
			}
		}

		_ = s.syncSingleProjectFromDisk(ctx, existing)
		if firstSynced == nil {
			if detail, derr := s.GetProject(ctx, existing.ID); derr == nil {
				firstSynced = &detail
			}
		}
	}

	s.emit(contracts.EventStudioProjectChanged, contracts.StudioProjectChangedEvent{
		Action: "synced",
	})
	return firstSynced, nil
}

func (s *Service) syncSingleProjectFromDisk(ctx context.Context, proj domainstudio.Project) error {
	projRoot := s.resolveProjectRoot(proj)
	if projRoot == "" {
		return nil
	}
	if s.media != nil {
		s.media.AddAllowedRoot(projRoot)
	}

	// Sync project.json metadata if modified on disk.
	if b, err := os.ReadFile(filepath.Join(projRoot, "project.json")); err == nil {
		var dp domainstudio.Project
		if json.Unmarshal(b, &dp) == nil && dp.Title != "" {
			proj.Title = dp.Title
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
			proj.AutoApproveBatches = dp.AutoApproveBatches
			if updated, uerr := s.repo.UpdateProject(ctx, proj); uerr == nil {
				proj = updated
			}
		}
	}

	// Sync asset_bible.json -> studio_assets.
	biblePath := filepath.Join(projRoot, "assets", "asset_bible.json")
	if b, err := os.ReadFile(biblePath); err == nil {
		var pkg domainstudio.AssetBiblePackage
		if json.Unmarshal(b, &pkg) == nil {
			existingAssets, _ := s.repo.ListAssets(ctx, proj.ID)
			byTag := make(map[string]domainstudio.AssetEntry, len(existingAssets))
			for _, ea := range existingAssets {
				byTag[strings.ToLower(ea.CodeTag)] = ea
			}
			merged := make([]domainstudio.AssetEntry, 0, len(pkg.Assets))
			for _, da := range pkg.Assets {
				da.ProjectID = proj.ID
				da.Normalize()
				if prev, ok := byTag[strings.ToLower(da.CodeTag)]; ok {
					if da.ID == "" {
						da.ID = prev.ID
					}
					if da.PrimaryPath == "" {
						da.PrimaryPath = prev.PrimaryPath
					}
					if da.PrimaryAssetID == "" {
						da.PrimaryAssetID = prev.PrimaryAssetID
					}
				}
				merged = append(merged, da)
			}
			_, _ = s.repo.ReplaceAssets(ctx, proj.ID, merged)
		}
	}

	// Sync episodes/*/storyboard/shots.json -> studio_shots.
	epsDir := filepath.Join(projRoot, "episodes")
	epEntries, err := os.ReadDir(epsDir)
	if err == nil {
		existingEps, _ := s.repo.ListEpisodes(ctx, proj.ID)
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
			ep, exists := epByCode[epCode]
			if !exists {
				created, cerr := s.repo.CreateEpisode(ctx, domainstudio.Episode{
					ProjectID:      proj.ID,
					Code:           epCode,
					Title:          strings.ToUpper(epCode),
					Stage:          domainstudio.StageScript,
					RawScriptPath:  filepath.Join(epRoot, "script", "raw_screenplay.txt"),
					ScriptJSONPath: filepath.Join(epRoot, "script", "normalized_script.json"),
					AssetsJSONPath: filepath.Join(epRoot, "assets", "asset_bible.json"),
					ShotsJSONPath:  filepath.Join(epRoot, "storyboard", "shots.json"),
				})
				if cerr != nil {
					continue
				}
				ep = created
			}
			if b, serr := os.ReadFile(filepath.Join(epRoot, "storyboard", "shots.json")); serr == nil {
				var sp domainstudio.ShotPromptPackage
				if json.Unmarshal(b, &sp) == nil {
					existingShots, _ := s.repo.ListShots(ctx, ep.ID)
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
						prev, ok := byID[ds.ID]
						if !ok {
							prev, ok = bySeq[ds.Seq]
						}
						if ok {
							if ds.ID == "" {
								ds.ID = prev.ID
							}
							if ds.KeyframePath == "" {
								ds.KeyframePath = prev.KeyframePath
								ds.KeyframeAssetID = prev.KeyframeAssetID
							}
							if ds.VideoPath == "" {
								ds.VideoPath = prev.VideoPath
								ds.VideoAssetID = prev.VideoAssetID
							}
							if ds.AudioPath == "" {
								ds.AudioPath = prev.AudioPath
								ds.AudioAssetID = prev.AudioAssetID
							}
						}
						merged = append(merged, ds)
					}
					_, _ = s.repo.ReplaceShots(ctx, ep.ID, merged)
				}
			}
		}
	}

	dtoProj := projectToDTO(proj)
	s.emit(contracts.EventStudioProjectChanged, contracts.StudioProjectChangedEvent{
		ProjectID: proj.ID,
		Action:    "synced",
		Project:   &dtoProj,
	})
	return nil
}

// RunStage executes any Phase 1 stage or shot/asset batch using shared workflow nodes.
func (s *Service) RunStage(ctx context.Context, p contracts.StudioEpisodeRunStagePayload) (contracts.StudioRunStageResult, error) {
	if s.repo == nil || s.wf == nil {
		return contracts.StudioRunStageResult{}, apperr.Unavailable("studio dependencies are unavailable")
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
		return contracts.StudioRunStageResult{}, apperr.New(apperr.CodeConflict, "episode stage execution is already in progress")
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
			WithDetails(map[string]any{"approval_required": true, "project_id": proj.ID, "stage": string(p.Stage)})
	}
	ctx = domainmedia.ContextWithUsageMeta(ctx, domainmedia.UsageMeta{
		ProjectID: proj.ID,
		EpisodeID: ep.ID,
		Operation: string(p.Stage),
	})
	epRoot := s.resolveEpisodeRoot(proj, ep.Code)
	if epRoot != "" {
		if err := ensureEpisodeDirs(epRoot); err != nil {
			return contracts.StudioRunStageResult{}, apperr.Internal(err)
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
	rawScript := strings.TrimSpace(readStringFile(ep.RawScriptPath))
	if rawScript == "" {
		return contracts.StudioRunStageResult{}, apperr.Validation("raw screenplay is empty; save script text before normalizing")
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
	rawOut, err := s.wf.RunNode(ctx, studionodes.NodeTypeScriptNormalize, inBytes)
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
		_ = fsutil.WriteFileAtomic(ep.ScriptJSONPath, []byte(out.NormalizedScriptJSON), 0o644)
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
	normRaw := readStringFile(ep.ScriptJSONPath)
	if strings.TrimSpace(normRaw) == "" {
		return contracts.StudioRunStageResult{}, apperr.Validation("normalized_script.json is required before extracting assets")
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
	rawOut, err := s.wf.RunNode(ctx, studionodes.NodeTypeExtractAssets, inBytes)
	if err != nil {
		return contracts.StudioRunStageResult{}, err
	}
	var out contracts.ExtractAssetsOutput
	if err := decodeNodeOutput(rawOut, &out); err != nil {
		return contracts.StudioRunStageResult{}, err
	}

	var pkg domainstudio.AssetBiblePackage
	if err := json.Unmarshal([]byte(out.AssetBibleJSON), &pkg); err != nil {
		return contracts.StudioRunStageResult{}, apperr.Unavailable("invalid asset bible JSON output: " + err.Error())
	}

	existingAssets, _ := s.repo.ListAssets(ctx, proj.ID)
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
		_, _ = s.repo.UpdateEpisode(ctx, ep)
	}
	_ = s.persistProjectToDisk(proj, savedAssets)

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
	normRaw := readStringFile(ep.ScriptJSONPath)
	if strings.TrimSpace(normRaw) == "" {
		return contracts.StudioRunStageResult{}, apperr.Validation("normalized_script.json is required before authoring shots")
	}
	assets, _ := s.repo.ListAssets(ctx, proj.ID)
	bibleBytes, _ := json.Marshal(domainstudio.AssetBiblePackage{
		StylePrompt: proj.StylePrompt,
		Assets:      assets,
	})

	shotModeDef := domainstudio.ShotModeSingle
	if proj.ProductionMode == domainstudio.ModeR2V {
		shotModeDef = domainstudio.ShotModeR2V
	}
	model := firstNonEmpty(p.Model, proj.DefaultTextModel)
	inBytes, err := json.Marshal(contracts.AuthorShotPromptsInput{
		NormalizedScriptJSON: normRaw,
		AssetBibleJSON:       string(bibleBytes),
		ProductionMode:       string(proj.ProductionMode),
		ShotModeDefault:      string(shotModeDef),
		StylePrompt:          proj.StylePrompt,
		Model:                model,
		ProjectDir:           epRoot,
	})
	if err != nil {
		return contracts.StudioRunStageResult{}, apperr.Internal(err)
	}
	rawOut, err := s.wf.RunNode(ctx, studionodes.NodeTypeAuthorShotPrompts, inBytes)
	if err != nil {
		return contracts.StudioRunStageResult{}, err
	}
	var out contracts.AuthorShotPromptsOutput
	if err := decodeNodeOutput(rawOut, &out); err != nil {
		return contracts.StudioRunStageResult{}, err
	}

	var pkg domainstudio.ShotPromptPackage
	if err := json.Unmarshal([]byte(out.ShotsJSON), &pkg); err != nil {
		return contracts.StudioRunStageResult{}, apperr.Unavailable("invalid shots JSON output: " + err.Error())
	}

	for i := range pkg.Shots {
		pkg.Shots[i].ID = ""
		pkg.Shots[i].EpisodeID = ep.ID
		pkg.Shots[i].Seq = i + 1
		pkg.Shots[i].Status = domainstudio.ShotPending
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
	_ = s.syncEpisodeShotsToDisk(proj, ep, savedShots)

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
		_ = os.MkdirAll(outDir, 0o755)
	}
	model := firstNonEmpty(p.Model, proj.DefaultImageModel)

	completed, failed := 0, 0
	var lastErr error
	for idx, asset := range targets {
		if ctx.Err() != nil {
			return contracts.StudioRunStageResult{}, ctx.Err()
		}
		s.reportBatchProgress(ctx, proj.ID, ep.ID, string(p.Stage), idx+1, len(targets), asset.ID, fmt.Sprintf("Generating asset reference: %s", asset.Name), "running")

		prompt := composePrompt(proj.StylePrompt, firstNonEmpty(asset.Prompt, asset.Description, asset.Name))
		reqBytes, _ := json.Marshal(mediagennodes.ImageGenerateInput{
			Prompt:      prompt,
			Model:       model,
			AspectRatio: proj.AspectRatio,
			Resolution:  p.Resolution,
			OutputDir:   outDir,
		})
		rawOut, rerr := s.wf.RunNode(ctx, mediagennodes.NodeTypeImage, reqBytes)
		if rerr != nil {
			failed++
			lastErr = rerr
			continue
		}
		var out mediagennodes.ImageGenerateOutput
		if merr := decodeNodeOutput(rawOut, &out); merr != nil {
			failed++
			lastErr = merr
			continue
		}
		asset.PrimaryPath = out.Path
		if regID := s.registerMedia(ctx, "image", mediagennodes.NodeTypeImage, out.Path, outDir, 0); regID != "" {
			asset.PrimaryAssetID = regID
		}
		if _, uerr := s.repo.UpsertAsset(ctx, asset); uerr != nil {
			failed++
			lastErr = uerr
			continue
		}
		s.recordSelectedVersion(ctx, proj.ID, ep.ID, domainstudio.TargetAsset, asset.ID, out.Path, mediagennodes.NodeTypeImage, prompt, p.Model)
		completed++
	}

	if allAssets, lerr := s.repo.ListAssets(ctx, proj.ID); lerr == nil {
		_ = s.persistProjectToDisk(proj, allAssets)
	}
	status := "completed"
	if failed > 0 && completed == 0 {
		status = "failed"
	}
	s.reportBatchProgress(ctx, proj.ID, ep.ID, string(p.Stage), len(targets), len(targets), "", fmt.Sprintf("Assets generated: %d ok, %d failed", completed, failed), status)

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
	assets, _ := s.repo.ListAssets(ctx, proj.ID)
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
		_ = os.MkdirAll(outDir, 0o755)
	}
	model := firstNonEmpty(p.Model, proj.DefaultImageModel)

	completed, failed := 0, 0
	var lastErr error
	for idx, shot := range targets {
		if ctx.Err() != nil {
			return contracts.StudioRunStageResult{}, ctx.Err()
		}
		s.reportBatchProgress(ctx, proj.ID, ep.ID, string(p.Stage), idx+1, len(targets), shot.ID, fmt.Sprintf("Generating keyframe #%d: %s", shot.Seq, shot.Title), "running")

		refs := collectRefImages(shot.RefAssetTags, tagPathMap)
		prompt := composePrompt(proj.StylePrompt, firstNonEmpty(shot.ImagePrompt, shot.Title))
		reqBytes, _ := json.Marshal(mediagennodes.ImageGenerateInput{
			Prompt:          prompt,
			ReferenceImages: refs,
			Model:           model,
			AspectRatio:     proj.AspectRatio,
			Resolution:      p.Resolution,
			OutputDir:       outDir,
		})
		rawOut, rerr := s.wf.RunNode(ctx, mediagennodes.NodeTypeImage, reqBytes)
		if rerr != nil {
			failed++
			lastErr = rerr
			shot.Status = domainstudio.ShotFailed
			shot.ErrorText = rerr.Error()
			_, _ = s.repo.UpsertShot(ctx, shot)
			continue
		}
		var out mediagennodes.ImageGenerateOutput
		if merr := decodeNodeOutput(rawOut, &out); merr != nil {
			failed++
			lastErr = merr
			continue
		}
		shot.KeyframePath = out.Path
		if regID := s.registerMedia(ctx, "image", mediagennodes.NodeTypeImage, out.Path, outDir, 0); regID != "" {
			shot.KeyframeAssetID = regID
		}
		if shot.Status == domainstudio.ShotPending || shot.Status == domainstudio.ShotFailed {
			shot.Status = domainstudio.ShotKeyframeReady
		}
		shot.ErrorText = ""
		_, _ = s.repo.UpsertShot(ctx, shot)
		s.recordSelectedVersion(ctx, proj.ID, ep.ID, domainstudio.TargetKeyframe, shot.ID, out.Path, mediagennodes.NodeTypeImage, prompt, p.Model)
		completed++
	}

	if completed > 0 && ep.Stage == domainstudio.StageStoryboard {
		ep.Stage = domainstudio.StageVideo
		_, _ = s.repo.UpdateEpisode(ctx, ep)
	}
	if refreshed, lerr := s.repo.ListShots(ctx, ep.ID); lerr == nil {
		_ = s.syncEpisodeShotsToDisk(proj, ep, refreshed)
	}
	status := "completed"
	if failed > 0 && completed == 0 {
		status = "failed"
	}
	s.reportBatchProgress(ctx, proj.ID, ep.ID, string(p.Stage), len(targets), len(targets), "", fmt.Sprintf("Keyframes: %d ok, %d failed", completed, failed), status)

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
	assets, _ := s.repo.ListAssets(ctx, proj.ID)
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
		_ = os.MkdirAll(outDir, 0o755)
	}
	model := firstNonEmpty(p.Model, proj.DefaultVideoModel)

	// Run bounded workers up to config studio.max_concurrent_batch.
	workers := 0
	if len(targets) > 0 {
		workers = s.cfg.MaxConcurrentBatch
		if workers <= 0 {
			workers = 1
		}
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

				vidIn := mediagennodes.VideoGenerateInput{
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

				reqBytes, _ := json.Marshal(vidIn)
				rawOut, rerr := s.wf.RunNode(ctx, mediagennodes.NodeTypeVideo, reqBytes)
				if rerr != nil {
					shot.Status = domainstudio.ShotFailed
					shot.ErrorText = rerr.Error()
					_, _ = s.repo.UpsertShot(ctx, shot)
					stateMu.Lock()
					failed++
					doneCount++
					lastErr = rerr
					curDone := doneCount
					stateMu.Unlock()
					s.reportBatchProgress(ctx, proj.ID, ep.ID, string(p.Stage), curDone, len(targets), shot.ID, fmt.Sprintf("Shot #%d video failed", shot.Seq), "running")
					continue
				}

				var out mediagennodes.VideoGenerateOutput
				if merr := decodeNodeOutput(rawOut, &out); merr != nil {
					stateMu.Lock()
					failed++
					doneCount++
					lastErr = merr
					stateMu.Unlock()
					continue
				}

				shot.VideoPath = out.Path
				if regID := s.registerMedia(ctx, "video", mediagennodes.NodeTypeVideo, out.Path, outDir, out.Duration); regID != "" {
					shot.VideoAssetID = regID
				}
				shot.Status = domainstudio.ShotVideoReady
				shot.ErrorText = ""
				_, _ = s.repo.UpsertShot(ctx, shot)
				s.recordSelectedVersion(ctx, proj.ID, ep.ID, domainstudio.TargetVideo, shot.ID, out.Path, mediagennodes.NodeTypeVideo, shot.VideoPrompt, p.Model)

				stateMu.Lock()
				completed++
				doneCount++
				curDone := doneCount
				stateMu.Unlock()
				s.reportBatchProgress(ctx, proj.ID, ep.ID, string(p.Stage), curDone, len(targets), shot.ID, fmt.Sprintf("Generated shot #%d video", shot.Seq), "running")
			}
		})
	}
	wg.Wait()
	if ctx.Err() != nil {
		return contracts.StudioRunStageResult{}, ctx.Err()
	}

	if refreshed, lerr := s.repo.ListShots(ctx, ep.ID); lerr == nil {
		_ = s.syncEpisodeShotsToDisk(proj, ep, refreshed)
	}
	if completed > 0 && ep.Stage != domainstudio.StageExport {
		ep.Stage = domainstudio.StageVideo
		_, _ = s.repo.UpdateEpisode(ctx, ep)
	}

	status := "completed"
	if failed > 0 && completed == 0 {
		status = "failed"
	}
	s.reportBatchProgress(ctx, proj.ID, ep.ID, string(p.Stage), len(targets), len(targets), "", fmt.Sprintf("Videos: %d ok, %d failed", completed, failed), status)

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
		_ = os.MkdirAll(outDir, 0o755)
	}
	model := firstNonEmpty(p.Model, proj.DefaultTTSModel)

	completed, failed := 0, 0
	var lastErr error
	for idx, shot := range targets {
		if ctx.Err() != nil {
			return contracts.StudioRunStageResult{}, ctx.Err()
		}
		s.reportBatchProgress(ctx, proj.ID, ep.ID, string(p.Stage), idx+1, len(targets), shot.ID, fmt.Sprintf("Synthesizing TTS for shot #%d", shot.Seq), "running")
		reqBytes, _ := json.Marshal(mediagennodes.TTSGenerateInput{
			Text:      shotSpeechText(shot),
			Model:     model,
			Voice:     p.Voice,
			OutputDir: outDir,
		})
		rawOut, rerr := s.wf.RunNode(ctx, mediagennodes.NodeTypeTTS, reqBytes)
		if rerr != nil {
			failed++
			lastErr = rerr
			continue
		}
		var out mediagennodes.TTSGenerateOutput
		if merr := decodeNodeOutput(rawOut, &out); merr != nil {
			failed++
			lastErr = merr
			continue
		}
		shot.AudioPath = out.Path
		if regID := s.registerMedia(ctx, "audio", mediagennodes.NodeTypeTTS, out.Path, outDir, 0); regID != "" {
			shot.AudioAssetID = regID
		}
		if shot.Status == domainstudio.ShotPending || shot.Status == domainstudio.ShotKeyframeReady {
			shot.Status = domainstudio.ShotTTSReady
		}
		_, _ = s.repo.UpsertShot(ctx, shot)
		s.recordSelectedVersion(ctx, proj.ID, ep.ID, domainstudio.TargetDubAudio, shot.ID, out.Path, mediagennodes.NodeTypeTTS, shotSpeechText(shot), p.Model)
		completed++
	}

	if refreshed, lerr := s.repo.ListShots(ctx, ep.ID); lerr == nil {
		_ = s.syncEpisodeShotsToDisk(proj, ep, refreshed)
	}
	status := "completed"
	if failed > 0 && completed == 0 {
		status = "failed"
	}
	s.reportBatchProgress(ctx, proj.ID, ep.ID, string(p.Stage), len(targets), len(targets), "", fmt.Sprintf("TTS: %d ok, %d failed", completed, failed), status)

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
	probed, failed := 0, 0
	var lastErr error
	for _, sh := range shots {
		if len(filter) > 0 && !filter[sh.ID] {
			continue
		}
		if strings.TrimSpace(sh.VideoPath) == "" {
			continue
		}
		inBytes, _ := json.Marshal(contracts.ProbeMediaInput{
			Media: engine.ImageInputRef{MediaRef: engine.RefFor(sh.VideoPath, engine.MediaVideo)},
		})
		rawOut, rerr := s.wf.RunNode(ctx, studionodes.NodeTypeProbeMedia, inBytes)
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
		probed++
		if out.DurationSec > 0 && int(out.DurationSec+0.5) >= 1 && int(out.DurationSec+0.5) <= 60 {
			sh.DurationSec = int(out.DurationSec + 0.5)
			_, _ = s.repo.UpsertShot(ctx, sh)
		}
	}

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
		return contracts.StudioRunStageResult{}, apperr.Validation("no generated shot videos found to assemble")
	}

	manifestBytes, err := json.Marshal(clips)
	if err != nil {
		return contracts.StudioRunStageResult{}, apperr.Internal(err)
	}
	outDir := ""
	if epRoot != "" {
		outDir = filepath.Join(epRoot, "exports")
		_ = os.MkdirAll(outDir, 0o755)
	}

	resolution := strings.TrimSpace(p.Resolution)
	if resolution == "" {
		resolution = resolutionForAspectRatio(proj.AspectRatio, s.cfg.DefaultResolution)
	}
	fps := p.FPS
	if fps <= 0 {
		fps = s.cfg.DefaultFPS
	}

	inBytes, err := json.Marshal(contracts.VideoConcatBasicInput{
		ClipsManifestJSON:  string(manifestBytes),
		OutputDir:          outDir,
		NarrationAudioPath: narFallback,
		Resolution:         resolution,
		FPS:                fps,
	})
	if err != nil {
		return contracts.StudioRunStageResult{}, apperr.Internal(err)
	}
	rawOut, err := s.wf.RunNode(ctx, studionodes.NodeTypeVideoConcatBasic, inBytes)
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
	rawText := readStringFile(ep.RawScriptPath)
	var normPtr *domainstudio.NormalizedScript
	if b, rerr := os.ReadFile(ep.ScriptJSONPath); rerr == nil {
		var ns domainstudio.NormalizedScript
		if json.Unmarshal(b, &ns) == nil {
			normPtr = &ns
		}
	}
	_ = s.persistEpisodeToDisk(proj, ep, rawText, normPtr, shots, out.OutputPath, out.AssetID)

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

func (s *Service) reportBatchProgress(ctx context.Context, projectID, episodeID, stage string, cur, total int, targetID, msg, status string) {
	var pct float64
	if total > 0 {
		pct = float64(cur) / float64(total) * 100.0
	}
	progress.Report(ctx, progress.Update{
		Percent: pct,
		Message: msg,
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

func (s *Service) persistProjectToDisk(proj domainstudio.Project, assets []domainstudio.AssetEntry) error {
	projRoot := s.resolveProjectRoot(proj)
	if projRoot == "" {
		return nil
	}
	if err := ensureProjectDirs(projRoot); err != nil {
		return err
	}
	if pb, err := json.MarshalIndent(proj, "", "  "); err == nil {
		_ = fsutil.WriteFileAtomic(filepath.Join(projRoot, "project.json"), append(pb, '\n'), 0o644)
	}
	if ab, err := json.MarshalIndent(domainstudio.AssetBiblePackage{
		StylePrompt: proj.StylePrompt,
		Assets:      assets,
	}, "", "  "); err == nil {
		_ = fsutil.WriteFileAtomic(filepath.Join(projRoot, "assets", "asset_bible.json"), append(ab, '\n'), 0o644)
	}
	return nil
}

func (s *Service) syncEpisodeShotsToDisk(proj domainstudio.Project, ep domainstudio.Episode, shots []domainstudio.Shot) error {
	epRoot := s.resolveEpisodeRoot(proj, ep.Code)
	if epRoot == "" {
		return nil
	}
	if err := ensureEpisodeDirs(epRoot); err != nil {
		return err
	}
	pkg := domainstudio.ShotPromptPackage{
		EpisodeTitle:   ep.Title,
		ProductionMode: proj.ProductionMode,
		Shots:          shots,
	}
	data, err := json.MarshalIndent(pkg, "", "  ")
	if err != nil {
		return err
	}
	target := ep.ShotsJSONPath
	if target == "" {
		target = filepath.Join(epRoot, "storyboard", "shots.json")
	}
	return fsutil.WriteFileAtomic(target, append(data, '\n'), 0o644)
}

func (s *Service) persistEpisodeToDisk(proj domainstudio.Project, ep domainstudio.Episode, rawScript string, normScript *domainstudio.NormalizedScript, shots []domainstudio.Shot, finalPath, finalAssetID string) error {
	epRoot := s.resolveEpisodeRoot(proj, ep.Code)
	if epRoot == "" {
		return nil
	}
	if err := ensureEpisodeDirs(epRoot); err != nil {
		return err
	}
	meta := episodeDiskMeta{
		Episode:           ep,
		FinalVideoPath:    finalPath,
		FinalVideoAssetID: finalAssetID,
	}
	if mb, err := json.MarshalIndent(meta, "", "  "); err == nil {
		_ = fsutil.WriteFileAtomic(filepath.Join(epRoot, "episode.json"), append(mb, '\n'), 0o644)
	}
	if rawScript != "" && ep.RawScriptPath != "" {
		_ = fsutil.WriteFileAtomic(ep.RawScriptPath, []byte(rawScript), 0o644)
	}
	if normScript != nil && ep.ScriptJSONPath != "" {
		if sb, err := json.MarshalIndent(normScript, "", "  "); err == nil {
			_ = fsutil.WriteFileAtomic(ep.ScriptJSONPath, append(sb, '\n'), 0o644)
		}
	}
	if len(shots) > 0 {
		_ = s.syncEpisodeShotsToDisk(proj, ep, shots)
	}
	return nil
}

func (s *Service) readEpisodeDiskMeta(epRoot string) *episodeDiskMeta {
	if epRoot == "" {
		return nil
	}
	b, err := os.ReadFile(filepath.Join(epRoot, "episode.json"))
	if err != nil {
		return nil
	}
	var m episodeDiskMeta
	if json.Unmarshal(b, &m) != nil {
		return nil
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
	switch strings.TrimSpace(aspect) {
	case "9:16":
		return "1080x1920"
	case "1:1":
		return "1080x1080"
	case "4:3":
		return "1440x1080"
	case "3:4":
		return "1080x1440"
	case "16:9":
		return "1920x1080"
	default:
		if strings.TrimSpace(fallback) != "" {
			return fallback
		}
		return "1920x1080"
	}
}

func decodeNodeOutput(raw any, dst any) error {
	b, err := json.Marshal(raw)
	if err != nil {
		return apperr.Internal(err)
	}
	if err := json.Unmarshal(b, dst); err != nil {
		return apperr.Internal(err)
	}
	return nil
}

func readStringFile(path string) string {
	p := strings.TrimSpace(path)
	if p == "" {
		return ""
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	return string(b)
}

func readNormalizedScriptDTO(path string) *contracts.StudioNormalizedScript {
	p := strings.TrimSpace(path)
	if p == "" {
		return nil
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	var ns domainstudio.NormalizedScript
	if json.Unmarshal(b, &ns) != nil {
		return nil
	}
	dto := normalizedScriptToDTO(ns)
	return &dto
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

// ListProjectsRaw returns sorted slugs for diagnostics or inspection if needed.
func (s *Service) ListProjectSlugs(ctx context.Context) ([]string, error) {
	list, err := s.repo.ListProjects(ctx)
	if err != nil {
		return nil, err
	}
	slugs := make([]string, 0, len(list))
	for _, pr := range list {
		slugs = append(slugs, pr.Slug)
	}
	sort.Strings(slugs)
	return slugs, nil
}
