package studio

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"app/internal/apperr"
	"app/internal/contracts"
	domainstudio "app/internal/domain/studio"
	"app/internal/engine"
)

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
