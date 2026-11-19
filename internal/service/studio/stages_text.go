package studio

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"app/internal/apperr"
	"app/internal/contracts"
	domainmedia "app/internal/domain/mediagen"
	domainstudio "app/internal/domain/studio"
)

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
