package studio

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"app/internal/apperr"
	"app/internal/contracts"
	domainstudio "app/internal/domain/studio"
	"app/internal/engine"
)

// BatchUpsertShots creates, patches, or atomically replaces storyboard shots for an episode.
func (s *Service) BatchUpsertShots(ctx context.Context, p contracts.StudioShotsBatchUpsertPayload) (contracts.StudioShotListResult, error) {
	if s.repo == nil {
		return contracts.StudioShotListResult{}, apperr.Unavailable("studio repository is unavailable").
			WithMsg("errors.studio.service_unavailable", nil)
	}
	if err := p.Validate(); err != nil {
		return contracts.StudioShotListResult{}, err
	}
	if len(p.Shots) > s.cfg.MaxBatchShotsUpsert {
		return contracts.StudioShotListResult{}, apperr.Validation(fmt.Sprintf("batch exceeds max_batch_shots_upsert (%d)", s.cfg.MaxBatchShotsUpsert)).
			WithMsg("errors.studio.batch_too_large", map[string]any{"max": s.cfg.MaxBatchShotsUpsert})
	}

	ep, err := s.repo.GetEpisode(ctx, strings.TrimSpace(p.EpisodeID))
	if err != nil {
		return contracts.StudioShotListResult{}, err
	}
	proj, err := s.repo.GetProject(ctx, ep.ProjectID)
	if err != nil {
		return contracts.StudioShotListResult{}, err
	}

	for _, item := range p.Shots {
		for _, ptr := range []*string{item.KeyframePath, item.VideoPath, item.AudioPath} {
			if ptr != nil && strings.TrimSpace(*ptr) != "" {
				if cerr := engine.CheckFile(ctx, strings.TrimSpace(*ptr), false); cerr != nil {
					return contracts.StudioShotListResult{}, cerr
				}
			}
		}
	}

	existing, err := s.repo.ListShots(ctx, ep.ID)
	if err != nil {
		return contracts.StudioShotListResult{}, err
	}
	byID := make(map[string]domainstudio.Shot, len(existing))
	bySeq := make(map[int]domainstudio.Shot, len(existing))
	maxSeq := 0
	for _, sh := range existing {
		byID[sh.ID] = sh
		bySeq[sh.Seq] = sh
		if sh.Seq > maxSeq {
			maxSeq = sh.Seq
		}
	}

	applyItem := func(base domainstudio.Shot, item contracts.StudioShotBatchItem, fallbackSeq int) domainstudio.Shot {
		cur := base
		cur.EpisodeID = ep.ID
		if id := strings.TrimSpace(item.ID); id != "" {
			cur.ID = id
		}
		if item.Seq != nil && *item.Seq > 0 {
			cur.Seq = *item.Seq
		} else if cur.Seq <= 0 {
			cur.Seq = fallbackSeq
		}
		if item.SceneCode != "" {
			cur.SceneCode = item.SceneCode
		}
		if item.Title != "" {
			cur.Title = item.Title
		}
		if item.ShotMode != "" {
			cur.ShotMode = item.ShotMode
		}
		if item.DurationSec != nil {
			cur.DurationSec = *item.DurationSec
		}
		if item.ImagePrompt != nil {
			cur.ImagePrompt = *item.ImagePrompt
		}
		if item.VideoPrompt != nil {
			cur.VideoPrompt = *item.VideoPrompt
		}
		if item.NarrationText != nil {
			cur.NarrationText = *item.NarrationText
		}
		if item.Dialogues != nil {
			cur.Dialogues = dialoguesFromDTO(item.Dialogues)
		}
		if item.RefAssetTags != nil {
			cur.RefAssetTags = append([]string(nil), item.RefAssetTags...)
		}
		if item.KeyframeAssetID != nil {
			cur.KeyframeAssetID = *item.KeyframeAssetID
		}
		if item.KeyframePath != nil {
			cur.KeyframePath = *item.KeyframePath
		}
		if item.VideoAssetID != nil {
			cur.VideoAssetID = *item.VideoAssetID
		}
		if item.VideoPath != nil {
			cur.VideoPath = *item.VideoPath
		}
		if item.AudioAssetID != nil {
			cur.AudioAssetID = *item.AudioAssetID
		}
		if item.AudioPath != nil {
			cur.AudioPath = *item.AudioPath
		}
		if item.Status != nil {
			cur.Status = *item.Status
		}
		if item.ErrorText != nil {
			cur.ErrorText = *item.ErrorText
		}
		return cur
	}

	var updatedList []domainstudio.Shot
	if p.ReplaceAll {
		prepared := make([]domainstudio.Shot, 0, len(p.Shots))
		for idx, item := range p.Shots {
			var base domainstudio.Shot
			if id := strings.TrimSpace(item.ID); id != "" {
				if ex, ok := byID[id]; ok {
					base = ex
				}
			}
			cur := applyItem(base, item, idx+1)
			if err := cur.ValidateWithLimits(s.cfg.MaxShotDurationSec, s.cfg.DefaultShotDurationSec); err != nil {
				return contracts.StudioShotListResult{}, err
			}
			prepared = append(prepared, cur)
		}
		updatedList, err = s.repo.ReplaceShots(ctx, ep.ID, prepared)
		if err != nil {
			return contracts.StudioShotListResult{}, err
		}
	} else {
		for _, item := range p.Shots {
			var base domainstudio.Shot
			if id := strings.TrimSpace(item.ID); id != "" {
				if ex, ok := byID[id]; ok {
					base = ex
				}
			} else if item.Seq != nil && *item.Seq > 0 {
				if ex, ok := bySeq[*item.Seq]; ok {
					base = ex
				}
			}
			nextSeq := maxSeq + 1
			if base.ID != "" {
				nextSeq = base.Seq
			}
			cur := applyItem(base, item, nextSeq)
			if err := cur.ValidateWithLimits(s.cfg.MaxShotDurationSec, s.cfg.DefaultShotDurationSec); err != nil {
				return contracts.StudioShotListResult{}, err
			}
			saved, uerr := s.repo.UpsertShot(ctx, cur)
			if uerr != nil {
				return contracts.StudioShotListResult{}, uerr
			}
			byID[saved.ID] = saved
			bySeq[saved.Seq] = saved
			if saved.Seq > maxSeq {
				maxSeq = saved.Seq
			}
		}
		updatedList, err = s.repo.ListShots(ctx, ep.ID)
		if err != nil {
			return contracts.StudioShotListResult{}, err
		}
	}

	if ep.Stage == domainstudio.StageScript || ep.Stage == domainstudio.StageAssets {
		ep.Stage = domainstudio.StageStoryboard
		if savedEp, uerr := s.repo.UpdateEpisode(ctx, ep); uerr == nil {
			ep = savedEp
		}
	}
	s.warnDiskMirror(ctx, "shots.json", s.syncEpisodeShotsToDisk(ctx, proj, ep, updatedList))
	s.emit(contracts.EventStudioEpisodeUpdated, contracts.StudioEpisodeUpdatedEvent{
		ProjectID: proj.ID,
		EpisodeID: ep.ID,
		Stage:     ep.Stage,
		Reason:    "shots_batch_upserted",
	})
	return contracts.StudioShotListResult{Shots: shotsToDTO(updatedList)}, nil
}

// RenameAsset renames an Asset Bible entry and cascades @CodeTag / speaker references across project episodes.
func (s *Service) RenameAsset(ctx context.Context, p contracts.StudioAssetRenamePayload) (contracts.StudioAssetRefactorResult, error) {
	if s.repo == nil {
		return contracts.StudioAssetRefactorResult{}, apperr.Unavailable("studio repository is unavailable").
			WithMsg("errors.studio.service_unavailable", nil)
	}
	if err := p.Validate(); err != nil {
		return contracts.StudioAssetRefactorResult{}, err
	}
	proj, err := s.repo.GetProject(ctx, strings.TrimSpace(p.ProjectID))
	if err != nil {
		return contracts.StudioAssetRefactorResult{}, err
	}
	assets, err := s.repo.ListAssets(ctx, proj.ID)
	if err != nil {
		return contracts.StudioAssetRefactorResult{}, err
	}
	targetIdx := -1
	for i := range assets {
		if assets[i].ID == strings.TrimSpace(p.AssetID) {
			targetIdx = i
			break
		}
	}
	if targetIdx < 0 {
		return contracts.StudioAssetRefactorResult{}, domainstudio.ErrAssetNotFound(p.AssetID)
	}
	src := assets[targetIdx]
	newName := strings.TrimSpace(p.NewName)
	if newName == "" {
		newName = src.Name
	}
	newTag := domainstudio.NormalizeCodeTag(p.NewCodeTag, newName)
	if newTag == "" {
		return contracts.StudioAssetRefactorResult{}, apperr.Validation("new_code_tag is invalid")
	}
	for i, a := range assets {
		if i != targetIdx && strings.EqualFold(a.CodeTag, newTag) {
			return contracts.StudioAssetRefactorResult{}, apperr.New(apperr.CodeConflict, fmt.Sprintf("another asset already uses %s; use studio_asset_merge instead", newTag)).
				WithMsg("errors.studio.duplicate_asset_tag", nil)
		}
	}

	oldSpeaker, newSpeaker := "", ""
	if src.Kind == domainstudio.AssetCharacter && newName != src.Name {
		oldSpeaker, newSpeaker = src.Name, newName
	}

	res, err := s.cascadeAssetTagRefactor(ctx, proj, src.CodeTag, newTag, oldSpeaker, newSpeaker, p.DryRun)
	if err != nil {
		return contracts.StudioAssetRefactorResult{}, err
	}
	if p.DryRun {
		return res, nil
	}

	src.Name = newName
	src.CodeTag = newTag
	if _, err := s.repo.UpsertAsset(ctx, src); err != nil {
		return contracts.StudioAssetRefactorResult{}, err
	}
	refreshedAssets, err := s.repo.ListAssets(ctx, proj.ID)
	if err != nil {
		return contracts.StudioAssetRefactorResult{}, err
	}
	s.warnDiskMirror(ctx, "asset_bible.json", s.persistProjectToDisk(ctx, proj, refreshedAssets))
	dtoProj := projectToDTO(proj)
	s.emit(contracts.EventStudioProjectChanged, contracts.StudioProjectChangedEvent{
		ProjectID: proj.ID,
		Action:    contracts.ProjectActionUpdated,
		Project:   &dtoProj,
	})
	res.Assets = assetsToDTO(refreshedAssets)
	return res, nil
}

// MergeAssets merges source_asset_id into target_asset_id, deletes source, and cascades @Tag references.
func (s *Service) MergeAssets(ctx context.Context, p contracts.StudioAssetMergePayload) (contracts.StudioAssetRefactorResult, error) {
	if s.repo == nil {
		return contracts.StudioAssetRefactorResult{}, apperr.Unavailable("studio repository is unavailable").
			WithMsg("errors.studio.service_unavailable", nil)
	}
	if err := p.Validate(); err != nil {
		return contracts.StudioAssetRefactorResult{}, err
	}
	proj, err := s.repo.GetProject(ctx, strings.TrimSpace(p.ProjectID))
	if err != nil {
		return contracts.StudioAssetRefactorResult{}, err
	}
	assets, err := s.repo.ListAssets(ctx, proj.ID)
	if err != nil {
		return contracts.StudioAssetRefactorResult{}, err
	}
	var srcAsset, tgtAsset *domainstudio.AssetEntry
	for i := range assets {
		if assets[i].ID == strings.TrimSpace(p.SourceAssetID) {
			srcAsset = &assets[i]
		}
		if assets[i].ID == strings.TrimSpace(p.TargetAssetID) {
			tgtAsset = &assets[i]
		}
	}
	if srcAsset == nil {
		return contracts.StudioAssetRefactorResult{}, domainstudio.ErrAssetNotFound(p.SourceAssetID)
	}
	if tgtAsset == nil {
		return contracts.StudioAssetRefactorResult{}, domainstudio.ErrAssetNotFound(p.TargetAssetID)
	}

	oldSpeaker, newSpeaker := "", ""
	if srcAsset.Kind == domainstudio.AssetCharacter && tgtAsset.Kind == domainstudio.AssetCharacter && srcAsset.Name != tgtAsset.Name {
		oldSpeaker, newSpeaker = srcAsset.Name, tgtAsset.Name
	}

	res, err := s.cascadeAssetTagRefactor(ctx, proj, srcAsset.CodeTag, tgtAsset.CodeTag, oldSpeaker, newSpeaker, p.DryRun)
	if err != nil {
		return contracts.StudioAssetRefactorResult{}, err
	}
	if p.DryRun {
		res.DeletedAssetID = srcAsset.ID
		return res, nil
	}

	if err := s.repo.DeleteAsset(ctx, srcAsset.ID); err != nil {
		return contracts.StudioAssetRefactorResult{}, err
	}
	refreshedAssets, err := s.repo.ListAssets(ctx, proj.ID)
	if err != nil {
		return contracts.StudioAssetRefactorResult{}, err
	}
	s.warnDiskMirror(ctx, "asset_bible.json", s.persistProjectToDisk(ctx, proj, refreshedAssets))
	dtoProj := projectToDTO(proj)
	s.emit(contracts.EventStudioProjectChanged, contracts.StudioProjectChangedEvent{
		ProjectID: proj.ID,
		Action:    contracts.ProjectActionUpdated,
		Project:   &dtoProj,
	})
	res.DeletedAssetID = srcAsset.ID
	res.Assets = assetsToDTO(refreshedAssets)
	return res, nil
}

func (s *Service) cascadeAssetTagRefactor(
	ctx context.Context,
	proj domainstudio.Project,
	oldTag, newTag, oldSpeaker, newSpeaker string,
	dryRun bool,
) (contracts.StudioAssetRefactorResult, error) {
	episodes, err := s.repo.ListEpisodes(ctx, proj.ID)
	if err != nil {
		return contracts.StudioAssetRefactorResult{}, err
	}

	res := contracts.StudioAssetRefactorResult{
		DryRun:     dryRun,
		OldCodeTag: oldTag,
		NewCodeTag: newTag,
	}

	for _, ep := range episodes {
		epRoot := s.resolveEpisodeRoot(proj, ep.Code)
		normScript := s.readGuardedNormalizedScriptDomain(ctx, epRoot, ep.ScriptJSONPath)
		shots, serr := s.repo.ListShots(ctx, ep.ID)
		if serr != nil {
			return contracts.StudioAssetRefactorResult{}, serr
		}

		scriptChanged, modShotIDs := domainstudio.RewriteAssetTagReferences(oldTag, newTag, oldSpeaker, newSpeaker, normScript, shots)
		if !scriptChanged && len(modShotIDs) == 0 {
			continue
		}
		res.ImpactedEpisodeCount++
		res.ImpactedShotCount += len(modShotIDs)

		if dryRun {
			if scriptChanged {
				res.UpdatedScriptsCount++
			}
			res.UpdatedShotsCount += len(modShotIDs)
			continue
		}

		if scriptChanged && normScript != nil && ep.ScriptJSONPath != "" {
			data, merr := json.MarshalIndent(normScript, "", "  ")
			if merr != nil {
				return contracts.StudioAssetRefactorResult{}, apperr.Internal(merr)
			}
			if werr := s.writeGuardedFile(ctx, epRoot, ep.ScriptJSONPath, append(data, '\n'), 0o644); werr != nil {
				return contracts.StudioAssetRefactorResult{}, werr
			}
			res.UpdatedScriptsCount++
		}

		if len(modShotIDs) > 0 {
			modSet := make(map[string]bool, len(modShotIDs))
			for _, id := range modShotIDs {
				modSet[id] = true
			}
			for _, sh := range shots {
				if !modSet[sh.ID] {
					continue
				}
				if _, uerr := s.repo.UpsertShot(ctx, sh); uerr != nil {
					return contracts.StudioAssetRefactorResult{}, uerr
				}
				res.UpdatedShotsCount++
			}
			refreshedShots, lerr := s.repo.ListShots(ctx, ep.ID)
			if lerr == nil {
				s.warnDiskMirror(ctx, "shots.json", s.syncEpisodeShotsToDisk(ctx, proj, ep, refreshedShots))
			}
		}

		s.emit(contracts.EventStudioEpisodeUpdated, contracts.StudioEpisodeUpdatedEvent{
			ProjectID: proj.ID,
			EpisodeID: ep.ID,
			Stage:     ep.Stage,
			Reason:    "asset_tags_refactored",
		})
	}
	return res, nil
}
