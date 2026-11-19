package studio

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"path/filepath"
	"strings"

	"app/internal/apperr"
	"app/internal/contracts"
	domainstudio "app/internal/domain/studio"
	"app/internal/engine"
)

func (s *Service) enqueueShotLipSync(ctx context.Context, proj domainstudio.Project, ep domainstudio.Episode, epRoot string, p contracts.StudioQueueEnqueuePayload) ([]domainstudio.Job, int, float64, error) {
	shots, err := s.repo.ListShots(ctx, ep.ID)
	if err != nil {
		return nil, 0, 0, err
	}
	wantIDs := toSet(p.ShotIDs)
	outDir := ""
	if epRoot != "" {
		outDir = filepath.Join(epRoot, "media", "videos")
	}
	provID, modID := domainstudio.SplitModelRefForPricing(p.Model)
	unitPrice, _, perr := s.EstimateUnitPriceUSD(ctx, provID, modID, "lipsync", "second")
	if perr != nil {
		s.log.WarnContext(ctx, "studio: failed to estimate lipsync unit price", "error", perr)
	}

	var out []domainstudio.Job
	skipped := 0
	estTotal := 0.0
	for _, sh := range shots {
		if len(wantIDs) > 0 && !wantIDs[sh.ID] {
			continue
		}
		if strings.TrimSpace(sh.VideoPath) == "" || strings.TrimSpace(sh.AudioPath) == "" {
			skipped++
			continue
		}
		dur := max(1, sh.DurationSec)
		est := math.Round(float64(dur)*unitPrice*10000) / 10000
		payload := queuedLipSyncJobRequest{
			Input: contracts.LipSyncGenerateInput{
				VideoPath: engine.InputRef(sh.VideoPath, engine.MediaVideo),
				AudioPath: engine.InputRef(sh.AudioPath, engine.MediaAudio),
				ModelRef:  p.Model,
				OutputDir: outDir,
			},
		}
		reqBytes, merr := json.Marshal(payload)
		if merr != nil {
			return nil, skipped, estTotal, apperr.Internal(merr)
		}
		j, createdNew, err := s.createIdempotentJob(ctx, domainstudio.Job{
			ProjectID:      proj.ID,
			EpisodeID:      ep.ID,
			ShotID:         sh.ID,
			JobKind:        domainstudio.JobLipSync,
			ProviderID:     provID,
			ModelRef:       p.Model,
			IdempotencyKey: fmt.Sprintf("studio:%s:lipsync:%s", ep.ID, sh.ID),
			RequestJSON:    string(reqBytes),
			MaxAttempts:    s.cfg.QueueMaxAttempts,
			EstimatedUSD:   est,
		}, p.Force)
		if err != nil {
			return nil, skipped, estTotal, err
		}
		if !createdNew {
			skipped++
			continue
		}
		out = append(out, j)
		estTotal += est
	}
	return out, skipped, estTotal, nil
}

func (s *Service) enqueueShotTTSDubMux(ctx context.Context, proj domainstudio.Project, ep domainstudio.Episode, epRoot string, p contracts.StudioQueueEnqueuePayload) ([]domainstudio.Job, int, float64, error) {
	shots, err := s.repo.ListShots(ctx, ep.ID)
	if err != nil {
		return nil, 0, 0, err
	}
	assets, _ := s.repo.ListAssets(ctx, proj.ID)
	voiceBySpeaker := map[string]domainstudio.AssetEntry{}
	for _, a := range assets {
		if a.Kind == domainstudio.AssetCharacter && (a.VoiceID != "" || a.VoiceProvider != "") {
			voiceBySpeaker[strings.ToLower(strings.TrimSpace(a.Name))] = a
			voiceBySpeaker[strings.ToLower(strings.TrimSpace(a.CodeTag))] = a
		}
	}
	wantIDs := toSet(p.ShotIDs)
	outDir := ""
	if epRoot != "" {
		outDir = filepath.Join(epRoot, "media", "videos")
	}
	var out []domainstudio.Job
	skipped := 0
	estTotal := 0.0
	for _, sh := range shots {
		if len(wantIDs) > 0 && !wantIDs[sh.ID] {
			continue
		}
		txt := shotSpeechText(sh)
		if strings.TrimSpace(sh.VideoPath) == "" || (txt == "" && strings.TrimSpace(sh.AudioPath) == "") {
			skipped++
			continue
		}
		modelRef, voiceID := resolveShotVoiceConfig(sh, p.Model, proj.DefaultTTSModel, "", voiceBySpeaker)
		provID, modID := domainstudio.SplitModelRefForPricing(modelRef)
		unitPrice, _, _ := s.EstimateUnitPriceUSD(ctx, provID, modID, "tts", "1k_chars")
		est := math.Round((float64(len([]rune(txt)))/1000.0)*unitPrice*10000) / 10000
		payload := queuedTTSDubMuxJobRequest{
			Input: contracts.TTSDubMuxInput{
				VideoPath: engine.InputRef(sh.VideoPath, engine.MediaVideo),
				Text:      txt,
				AudioPath: engine.InputRef(sh.AudioPath, engine.MediaAudio),
				ModelRef:  modelRef,
				Voice:     voiceID,
				OutputDir: outDir,
			},
		}
		reqBytes, merr := json.Marshal(payload)
		if merr != nil {
			return nil, skipped, estTotal, apperr.Internal(merr)
		}
		j, createdNew, err := s.createIdempotentJob(ctx, domainstudio.Job{
			ProjectID:      proj.ID,
			EpisodeID:      ep.ID,
			ShotID:         sh.ID,
			JobKind:        domainstudio.JobTTSDubMux,
			ProviderID:     provID,
			ModelRef:       modelRef,
			IdempotencyKey: fmt.Sprintf("studio:%s:tts_dub_mux:%s", ep.ID, sh.ID),
			RequestJSON:    string(reqBytes),
			MaxAttempts:    s.cfg.QueueMaxAttempts,
			EstimatedUSD:   est,
		}, p.Force)
		if err != nil {
			return nil, skipped, estTotal, err
		}
		if !createdNew {
			skipped++
			continue
		}
		out = append(out, j)
		estTotal += est
	}
	return out, skipped, estTotal, nil
}

func (s *Service) enqueueShotVideoWatermark(ctx context.Context, proj domainstudio.Project, ep domainstudio.Episode, epRoot string, p contracts.StudioQueueEnqueuePayload) ([]domainstudio.Job, int, float64, error) {
	shots, err := s.repo.ListShots(ctx, ep.ID)
	if err != nil {
		return nil, 0, 0, err
	}
	wantIDs := toSet(p.ShotIDs)
	outDir := ""
	if epRoot != "" {
		outDir = filepath.Join(epRoot, "media", "videos")
	}
	var out []domainstudio.Job
	skipped := 0
	for _, sh := range shots {
		if len(wantIDs) > 0 && !wantIDs[sh.ID] {
			continue
		}
		if strings.TrimSpace(sh.VideoPath) == "" {
			skipped++
			continue
		}
		payload := queuedVideoWatermarkJobRequest{
			Input: contracts.VideoWatermarkRemoveInput{
				VideoPath: engine.InputRef(sh.VideoPath, engine.MediaVideo),
				Mode:      "auto",
				OutputDir: outDir,
			},
		}
		reqBytes, merr := json.Marshal(payload)
		if merr != nil {
			return nil, skipped, 0, apperr.Internal(merr)
		}
		j, createdNew, err := s.createIdempotentJob(ctx, domainstudio.Job{
			ProjectID:      proj.ID,
			EpisodeID:      ep.ID,
			ShotID:         sh.ID,
			JobKind:        domainstudio.JobVideoWatermarkRemove,
			IdempotencyKey: fmt.Sprintf("studio:%s:video_wm_remove:%s", ep.ID, sh.ID),
			RequestJSON:    string(reqBytes),
			MaxAttempts:    s.cfg.QueueMaxAttempts,
		}, p.Force)
		if err != nil {
			return nil, skipped, 0, err
		}
		if !createdNew {
			skipped++
			continue
		}
		out = append(out, j)
	}
	return out, skipped, 0, nil
}

func (s *Service) enqueueShotMaskEdit(ctx context.Context, proj domainstudio.Project, ep domainstudio.Episode, epRoot string, p contracts.StudioQueueEnqueuePayload) ([]domainstudio.Job, int, float64, error) {
	shots, err := s.repo.ListShots(ctx, ep.ID)
	if err != nil {
		return nil, 0, 0, err
	}
	wantIDs := toSet(p.ShotIDs)
	outDir := ""
	if epRoot != "" {
		outDir = filepath.Join(epRoot, "media", "keyframes")
	}
	modelRef := firstNonEmpty(p.Model, proj.DefaultImageModel)
	provID, modID := domainstudio.SplitModelRefForPricing(modelRef)
	unitPrice, _, _ := s.EstimateUnitPriceUSD(ctx, provID, modID, "image", "image")
	var out []domainstudio.Job
	skipped := 0
	estTotal := 0.0
	for _, sh := range shots {
		if len(wantIDs) > 0 && !wantIDs[sh.ID] {
			continue
		}
		if strings.TrimSpace(sh.KeyframePath) == "" {
			skipped++
			continue
		}
		payload := queuedMaskEditJobRequest{
			TargetKind: domainstudio.TargetKeyframe,
			Input: contracts.MaskEditInput{
				ImagePath:   engine.InputRef(sh.KeyframePath, engine.MediaImage),
				MaskPath:    engine.InputRef(sh.KeyframePath, engine.MediaImage),
				Prompt:      firstNonEmpty(sh.ImagePrompt, sh.Title),
				ModelRef:    modelRef,
				AspectRatio: proj.AspectRatio,
				OutputDir:   outDir,
			},
		}
		reqBytes, merr := json.Marshal(payload)
		if merr != nil {
			return nil, skipped, estTotal, apperr.Internal(merr)
		}
		j, createdNew, err := s.createIdempotentJob(ctx, domainstudio.Job{
			ProjectID:      proj.ID,
			EpisodeID:      ep.ID,
			ShotID:         sh.ID,
			JobKind:        domainstudio.JobMaskEdit,
			ProviderID:     provID,
			ModelRef:       modelRef,
			IdempotencyKey: fmt.Sprintf("studio:%s:mask_edit:%s", ep.ID, sh.ID),
			RequestJSON:    string(reqBytes),
			MaxAttempts:    s.cfg.QueueMaxAttempts,
			EstimatedUSD:   unitPrice,
		}, p.Force)
		if err != nil {
			return nil, skipped, estTotal, err
		}
		if !createdNew {
			skipped++
			continue
		}
		out = append(out, j)
		estTotal += unitPrice
	}
	return out, skipped, estTotal, nil
}

func (s *Service) enqueueShotGridSplit(ctx context.Context, proj domainstudio.Project, ep domainstudio.Episode, epRoot string, p contracts.StudioQueueEnqueuePayload) ([]domainstudio.Job, int, float64, error) {
	shots, err := s.repo.ListShots(ctx, ep.ID)
	if err != nil {
		return nil, 0, 0, err
	}
	wantIDs := toSet(p.ShotIDs)
	outDir := ""
	if epRoot != "" {
		outDir = filepath.Join(epRoot, "media", "keyframes")
	}
	var out []domainstudio.Job
	skipped := 0
	for _, sh := range shots {
		if len(wantIDs) > 0 && !wantIDs[sh.ID] {
			continue
		}
		if strings.TrimSpace(sh.KeyframePath) == "" {
			skipped++
			continue
		}
		payload := queuedGridSplitJobRequest{
			TargetKind: domainstudio.TargetKeyframe,
			Input: contracts.GridSplitInput{
				ImagePath: engine.InputRef(sh.KeyframePath, engine.MediaImage),
				Rows:      2,
				Cols:      2,
				OutputDir: outDir,
			},
		}
		reqBytes, merr := json.Marshal(payload)
		if merr != nil {
			return nil, skipped, 0, apperr.Internal(merr)
		}
		j, createdNew, err := s.createIdempotentJob(ctx, domainstudio.Job{
			ProjectID:      proj.ID,
			EpisodeID:      ep.ID,
			ShotID:         sh.ID,
			JobKind:        domainstudio.JobGridSplit,
			IdempotencyKey: fmt.Sprintf("studio:%s:grid_split:%s", ep.ID, sh.ID),
			RequestJSON:    string(reqBytes),
			MaxAttempts:    s.cfg.QueueMaxAttempts,
		}, p.Force)
		if err != nil {
			return nil, skipped, 0, err
		}
		if !createdNew {
			skipped++
			continue
		}
		out = append(out, j)
	}
	return out, skipped, 0, nil
}

func (s *Service) enqueueShotExtractFrames(ctx context.Context, proj domainstudio.Project, ep domainstudio.Episode, epRoot string, p contracts.StudioQueueEnqueuePayload) ([]domainstudio.Job, int, float64, error) {
	shots, err := s.repo.ListShots(ctx, ep.ID)
	if err != nil {
		return nil, 0, 0, err
	}
	wantIDs := toSet(p.ShotIDs)
	outDir := ""
	if epRoot != "" {
		outDir = filepath.Join(epRoot, "media", "keyframes")
	}
	var out []domainstudio.Job
	skipped := 0
	for _, sh := range shots {
		if len(wantIDs) > 0 && !wantIDs[sh.ID] {
			continue
		}
		if strings.TrimSpace(sh.VideoPath) == "" {
			skipped++
			continue
		}
		payload := queuedExtractFramesJobRequest{
			TargetShotID: sh.ID,
			Input: contracts.ExtractFramesInput{
				VideoPath: engine.InputRef(sh.VideoPath, engine.MediaVideo),
				Mode:      "last",
				OutputDir: outDir,
			},
		}
		reqBytes, merr := json.Marshal(payload)
		if merr != nil {
			return nil, skipped, 0, apperr.Internal(merr)
		}
		j, createdNew, err := s.createIdempotentJob(ctx, domainstudio.Job{
			ProjectID:      proj.ID,
			EpisodeID:      ep.ID,
			ShotID:         sh.ID,
			JobKind:        domainstudio.JobExtractFrames,
			IdempotencyKey: fmt.Sprintf("studio:%s:extract_frames:%s", ep.ID, sh.ID),
			RequestJSON:    string(reqBytes),
			MaxAttempts:    s.cfg.QueueMaxAttempts,
		}, p.Force)
		if err != nil {
			return nil, skipped, 0, err
		}
		if !createdNew {
			skipped++
			continue
		}
		out = append(out, j)
	}
	return out, skipped, 0, nil
}

func (s *Service) enqueueShotQCReview(ctx context.Context, proj domainstudio.Project, ep domainstudio.Episode, p contracts.StudioQueueEnqueuePayload) ([]domainstudio.Job, int, float64, error) {
	shots, err := s.repo.ListShots(ctx, ep.ID)
	if err != nil {
		return nil, 0, 0, err
	}
	wantIDs := toSet(p.ShotIDs)
	var out []domainstudio.Job
	skipped := 0
	for _, sh := range shots {
		if len(wantIDs) > 0 && !wantIDs[sh.ID] {
			continue
		}
		mediaPath := firstNonEmpty(sh.VideoPath, sh.KeyframePath)
		if mediaPath == "" {
			skipped++
			continue
		}
		kind := domainstudio.TargetVideo
		mediaType := engine.MediaVideo
		if sh.VideoPath == "" {
			kind = domainstudio.TargetKeyframe
			mediaType = engine.MediaImage
		}
		payload := queuedQCReviewJobRequest{
			TargetKind: kind,
			MediaPath:  mediaPath,
			Input: contracts.QCReviewInput{
				MediaPath:      engine.InputRef(mediaPath, mediaType),
				TargetKind:     string(kind),
				TargetID:       sh.ID,
				PromptExpected: firstNonEmpty(sh.VideoPrompt, sh.ImagePrompt, sh.Title),
				VisionModelRef: firstNonEmpty(p.Model, proj.DefaultTextModel),
			},
		}
		reqBytes, merr := json.Marshal(payload)
		if merr != nil {
			return nil, skipped, 0, apperr.Internal(merr)
		}
		j, createdNew, err := s.createIdempotentJob(ctx, domainstudio.Job{
			ProjectID:      proj.ID,
			EpisodeID:      ep.ID,
			ShotID:         sh.ID,
			JobKind:        domainstudio.JobQCReview,
			ModelRef:       payload.Input.VisionModelRef,
			IdempotencyKey: fmt.Sprintf("studio:%s:qc_review:%s", ep.ID, sh.ID),
			RequestJSON:    string(reqBytes),
			MaxAttempts:    s.cfg.QueueMaxAttempts,
		}, p.Force)
		if err != nil {
			return nil, skipped, 0, err
		}
		if !createdNew {
			skipped++
			continue
		}
		out = append(out, j)
	}
	return out, skipped, 0, nil
}
