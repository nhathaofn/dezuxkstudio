package studio

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"app/internal/apperr"
	"app/internal/contracts"
	domainstudio "app/internal/domain/studio"
	"app/internal/engine"
	"app/internal/safego"
)

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
