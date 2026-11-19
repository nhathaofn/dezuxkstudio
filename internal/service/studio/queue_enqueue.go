package studio

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"time"

	"app/internal/apperr"
	"app/internal/contracts"
	domainmedia "app/internal/domain/mediagen"
	domainstudio "app/internal/domain/studio"
)

func (s *Service) EnqueueBatch(ctx context.Context, p contracts.StudioQueueEnqueuePayload) (contracts.StudioQueueEnqueueResult, error) {
	if s.repo == nil {
		return contracts.StudioQueueEnqueueResult{}, apperr.Unavailable("studio repository unavailable").
			WithMsg("errors.studio.service_unavailable", nil)
	}
	if err := p.Validate(); err != nil {
		return contracts.StudioQueueEnqueueResult{}, err
	}
	ep, err := s.repo.GetEpisode(ctx, strings.TrimSpace(p.EpisodeID))
	if err != nil {
		return contracts.StudioQueueEnqueueResult{}, err
	}
	proj, err := s.repo.GetProject(ctx, ep.ProjectID)
	if err != nil {
		return contracts.StudioQueueEnqueueResult{}, err
	}
	if !proj.AutoApproveBatches && !p.Approved {
		return contracts.StudioQueueEnqueueResult{}, apperr.Validation("Batch queue enqueue requires approval when auto_approve_batches is disabled").
			WithMsg("errors.studio.approval_required", nil).
			WithDetails(map[string]any{"approval_required": true, "project_id": proj.ID, "stage": p.Stage})
	}

	epRoot := s.resolveEpisodeRoot(proj, ep.Code)
	projRoot := s.resolveProjectRoot(proj)
	if epRoot != "" {
		if err := s.ensureGuardedEpisodeDirs(ctx, epRoot); err != nil {
			return contracts.StudioQueueEnqueueResult{}, err
		}
	} else if projRoot != "" {
		if err := s.ensureGuardedProjectDirs(ctx, projRoot); err != nil {
			return contracts.StudioQueueEnqueueResult{}, err
		}
	}

	stage := strings.ToLower(strings.TrimSpace(p.Stage))
	var created []domainstudio.Job
	skipped := 0
	totalEst := 0.0

	switch stage {
	case "generate_videos":
		created, skipped, totalEst, err = s.enqueueShotVideos(ctx, proj, ep, epRoot, p)
	case "generate_keyframes":
		created, skipped, totalEst, err = s.enqueueShotKeyframes(ctx, proj, ep, epRoot, p)
	case "generate_assets":
		created, skipped, totalEst, err = s.enqueueAssets(ctx, proj, ep, epRoot, projRoot, p)
	case "generate_tts":
		created, skipped, totalEst, err = s.enqueueShotTTS(ctx, proj, ep, epRoot, p)
	case "lipsync":
		created, skipped, totalEst, err = s.enqueueShotLipSync(ctx, proj, ep, epRoot, p)
	case "tts_dub_mux":
		created, skipped, totalEst, err = s.enqueueShotTTSDubMux(ctx, proj, ep, epRoot, p)
	case "video_watermark_remove":
		created, skipped, totalEst, err = s.enqueueShotVideoWatermark(ctx, proj, ep, epRoot, p)
	case "mask_edit":
		created, skipped, totalEst, err = s.enqueueShotMaskEdit(ctx, proj, ep, epRoot, p)
	case "grid_split":
		created, skipped, totalEst, err = s.enqueueShotGridSplit(ctx, proj, ep, epRoot, p)
	case "extract_frames":
		created, skipped, totalEst, err = s.enqueueShotExtractFrames(ctx, proj, ep, epRoot, p)
	case "qc_review":
		created, skipped, totalEst, err = s.enqueueShotQCReview(ctx, proj, ep, p)
	default:
		return contracts.StudioQueueEnqueueResult{}, apperr.Validation("unsupported queue enqueue stage")
	}
	if err != nil {
		return contracts.StudioQueueEnqueueResult{}, err
	}

	if len(created) > 0 {
		s.wakeWorker()
		last := jobToDTO(created[len(created)-1])
		s.emit(contracts.EventStudioQueueUpdated, contracts.StudioQueueUpdatedEvent{
			ProjectID: proj.ID,
			EpisodeID: ep.ID,
			Job:       &last,
			Summary:   fmt.Sprintf("Enqueued %d %s job(s)", len(created), stage),
		})
	}

	return contracts.StudioQueueEnqueueResult{
		Jobs:         jobsToDTO(created),
		Skipped:      skipped,
		EstimatedUSD: math.Round(totalEst*10000) / 10000,
		Summary:      fmt.Sprintf("Enqueued %d job(s), skipped %d", len(created), skipped),
	}, nil
}

// ListJobs queries durable studio jobs filtered by project/episode/status.
func (s *Service) ListJobs(ctx context.Context, p contracts.StudioQueueListPayload) (contracts.StudioQueueListResult, error) {
	if s.repo == nil {
		return contracts.StudioQueueListResult{}, apperr.Unavailable("studio repository unavailable").
			WithMsg("errors.studio.service_unavailable", nil)
	}
	if err := p.Validate(); err != nil {
		return contracts.StudioQueueListResult{}, err
	}
	jobs, err := s.repo.ListJobs(ctx, domainstudio.JobFilter{
		ProjectID: strings.TrimSpace(p.ProjectID),
		EpisodeID: strings.TrimSpace(p.EpisodeID),
		Status:    domainstudio.JobStatus(strings.TrimSpace(string(p.Status))),
		Limit:     p.Limit,
	})
	if err != nil {
		return contracts.StudioQueueListResult{}, err
	}
	return contracts.StudioQueueListResult{Jobs: jobsToDTO(jobs)}, nil
}

// ControlQueue pauses, resumes, cancels, or retries durable jobs cleanly without duplicating remote submits.
func (s *Service) ControlQueue(ctx context.Context, p contracts.StudioQueueControlPayload) (contracts.StudioQueueControlResult, error) {
	if s.repo == nil {
		return contracts.StudioQueueControlResult{}, apperr.Unavailable("studio repository unavailable").
			WithMsg("errors.studio.service_unavailable", nil)
	}
	if err := p.Validate(); err != nil {
		return contracts.StudioQueueControlResult{}, err
	}
	act := contracts.StudioQueueAction(strings.ToLower(strings.TrimSpace(string(p.Action))))
	switch act {
	case contracts.QueueActionPause:
		if p.JobID != "" {
			j, err := s.repo.GetJob(ctx, p.JobID)
			if err != nil {
				return contracts.StudioQueueControlResult{}, err
			}
			if !j.Status.IsTerminal() {
				j.Status = domainstudio.JobPaused
				j, err = s.repo.TransitionJobControl(ctx, j)
				if err != nil {
					return contracts.StudioQueueControlResult{}, err
				}
				s.cancelActiveJob(p.JobID)
				s.clearPollErrors(p.JobID)
				dto := jobToDTO(j)
				s.emit(contracts.EventStudioQueueUpdated, contracts.StudioQueueUpdatedEvent{ProjectID: j.ProjectID, EpisodeID: j.EpisodeID, Job: &dto, Summary: "Job paused"})
				return contracts.StudioQueueControlResult{Affected: 1, Job: &dto}, nil
			}
			dto := jobToDTO(j)
			return contracts.StudioQueueControlResult{Affected: 0, Job: &dto}, nil
		}
		n, err := s.repo.PauseEpisodeJobs(ctx, p.ProjectID, p.EpisodeID)
		if err != nil {
			return contracts.StudioQueueControlResult{}, err
		}
		s.cancelEpisodeActiveJobs(p.ProjectID, p.EpisodeID)
		s.emit(contracts.EventStudioQueueUpdated, contracts.StudioQueueUpdatedEvent{ProjectID: p.ProjectID, EpisodeID: p.EpisodeID, Summary: fmt.Sprintf("Paused %d job(s)", n)})
		return contracts.StudioQueueControlResult{Affected: n}, nil

	case contracts.QueueActionResume:
		if p.JobID != "" {
			j, err := s.repo.GetJob(ctx, p.JobID)
			if err != nil {
				return contracts.StudioQueueControlResult{}, err
			}
			if j.Status == domainstudio.JobPaused {
				if j.ProviderJobID != "" {
					j.Status = domainstudio.JobPolling
					j.NextPollAt = time.Now().UnixMilli()
				} else {
					j.Status = domainstudio.JobQueued
				}
				j, err = s.repo.TransitionJobControl(ctx, j)
				if err != nil {
					return contracts.StudioQueueControlResult{}, err
				}
				s.clearPollErrors(p.JobID)
				s.wakeWorker()
				dto := jobToDTO(j)
				s.emit(contracts.EventStudioQueueUpdated, contracts.StudioQueueUpdatedEvent{ProjectID: j.ProjectID, EpisodeID: j.EpisodeID, Job: &dto, Summary: "Job resumed"})
				return contracts.StudioQueueControlResult{Affected: 1, Job: &dto}, nil
			}
			dto := jobToDTO(j)
			return contracts.StudioQueueControlResult{Affected: 0, Job: &dto}, nil
		}
		n, err := s.repo.ResumeEpisodeJobs(ctx, p.ProjectID, p.EpisodeID)
		if err != nil {
			return contracts.StudioQueueControlResult{}, err
		}
		if n > 0 {
			s.wakeWorker()
		}
		s.emit(contracts.EventStudioQueueUpdated, contracts.StudioQueueUpdatedEvent{ProjectID: p.ProjectID, EpisodeID: p.EpisodeID, Summary: fmt.Sprintf("Resumed %d job(s)", n)})
		return contracts.StudioQueueControlResult{Affected: n}, nil

	case contracts.QueueActionCancel:
		j, err := s.repo.CancelJob(ctx, p.JobID)
		if err != nil {
			return contracts.StudioQueueControlResult{}, err
		}
		s.cancelActiveJob(p.JobID)
		s.clearPollErrors(p.JobID)
		dto := jobToDTO(j)
		s.emit(contracts.EventStudioQueueUpdated, contracts.StudioQueueUpdatedEvent{ProjectID: j.ProjectID, EpisodeID: j.EpisodeID, Job: &dto, Summary: "Job canceled"})
		return contracts.StudioQueueControlResult{Affected: 1, Job: &dto}, nil

	case contracts.QueueActionRetry:
		if strings.TrimSpace(p.JobID) == "" {
			return contracts.StudioQueueControlResult{}, apperr.Validation("job_id is required for retry")
		}
		j, err := s.repo.GetJob(ctx, p.JobID)
		if err != nil {
			return contracts.StudioQueueControlResult{}, err
		}
		if j.Status == domainstudio.JobSucceeded {
			return contracts.StudioQueueControlResult{}, apperr.Validation("cannot retry a job that already succeeded")
		}
		if proj, perr := s.repo.GetProject(ctx, j.ProjectID); perr == nil {
			if !proj.AutoApproveBatches && !p.Approved {
				return contracts.StudioQueueControlResult{}, apperr.Validation("Retrying studio job requires approval when auto_approve_batches is disabled").
					WithMsg("errors.studio.approval_required", nil).
					WithDetails(map[string]any{"approval_required": true, "project_id": proj.ID, "job_id": j.ID})
			}
		} else {
			return contracts.StudioQueueControlResult{}, perr
		}
		if j.ProviderJobID != "" {
			// Already submitted remotely: resume polling instead of double-billing
			j.Status = domainstudio.JobPolling
			j.NextPollAt = time.Now().UnixMilli()
		} else {
			j.Status = domainstudio.JobQueued
			j.Attempts = 0
		}
		j.ErrorText = ""
		j.FinishedAt = 0
		j, err = s.repo.TransitionJobControl(ctx, j)
		if err != nil {
			return contracts.StudioQueueControlResult{}, err
		}
		s.clearPollErrors(p.JobID)
		s.wakeWorker()
		dto := jobToDTO(j)
		s.emit(contracts.EventStudioQueueUpdated, contracts.StudioQueueUpdatedEvent{ProjectID: j.ProjectID, EpisodeID: j.EpisodeID, Job: &dto, Summary: "Job retried"})
		return contracts.StudioQueueControlResult{Affected: 1, Job: &dto}, nil
	}
	return contracts.StudioQueueControlResult{}, apperr.Validation("unsupported queue action")
}

func (s *Service) createIdempotentJob(ctx context.Context, job domainstudio.Job, force bool) (domainstudio.Job, bool, error) {
	if existing, err := s.repo.GetJobByIdempotencyKey(ctx, job.IdempotencyKey); err == nil {
		if !existing.Status.IsTerminal() {
			return existing, false, nil
		}
		if !force && existing.Status == domainstudio.JobSucceeded {
			return existing, false, nil
		}
		job.IdempotencyKey = fmt.Sprintf("%s:%d", job.IdempotencyKey, time.Now().UnixNano())
	}
	saved, err := s.repo.CreateJob(ctx, job)
	if err != nil {
		return domainstudio.Job{}, false, err
	}
	return saved, true, nil
}

func (s *Service) enqueueShotVideos(ctx context.Context, proj domainstudio.Project, ep domainstudio.Episode, epRoot string, p contracts.StudioQueueEnqueuePayload) ([]domainstudio.Job, int, float64, error) {
	shots, err := s.repo.ListShots(ctx, ep.ID)
	if err != nil {
		return nil, 0, 0, err
	}
	wantIDs := toSet(p.ShotIDs)
	outDir := ""
	if epRoot != "" {
		outDir = filepath.Join(epRoot, "media", "videos")
	}
	modelRef := firstNonEmpty(p.Model, proj.DefaultVideoModel)
	provID, modID := domainstudio.SplitModelRefForPricing(modelRef)
	unitPrice, _, perr := s.EstimateUnitPriceUSD(ctx, provID, modID, "video", "second")
	if perr != nil {
		s.log.WarnContext(ctx, "studio: failed to estimate video unit price", "error", perr)
	}

	var out []domainstudio.Job
	skipped := 0
	estTotal := 0.0
	for _, sh := range shots {
		if len(wantIDs) > 0 && !wantIDs[sh.ID] {
			continue
		}
		if !p.Force && strings.TrimSpace(sh.VideoPath) != "" {
			skipped++
			continue
		}
		dur := max(1, sh.DurationSec)
		req := domainmedia.VideoRequest{
			ModelRef:    modelRef,
			Prompt:      composePrompt(proj.StylePrompt, firstNonEmpty(sh.VideoPrompt, sh.ImagePrompt, sh.Title)),
			StartImage:  sh.KeyframePath,
			AspectRatio: proj.AspectRatio,
			DurationSec: dur,
			OutputDir:   outDir,
		}
		reqBytes, merr := json.Marshal(req)
		if merr != nil {
			return nil, skipped, estTotal, apperr.Internal(merr)
		}
		est := math.Round(float64(dur)*unitPrice*10000) / 10000
		j, createdNew, err := s.createIdempotentJob(ctx, domainstudio.Job{
			ProjectID:      proj.ID,
			EpisodeID:      ep.ID,
			ShotID:         sh.ID,
			JobKind:        domainstudio.JobVideo,
			ProviderID:     provID,
			ModelRef:       modelRef,
			IdempotencyKey: fmt.Sprintf("studio:%s:video:%s", ep.ID, sh.ID),
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

func (s *Service) enqueueShotKeyframes(ctx context.Context, proj domainstudio.Project, ep domainstudio.Episode, epRoot string, p contracts.StudioQueueEnqueuePayload) ([]domainstudio.Job, int, float64, error) {
	shots, err := s.repo.ListShots(ctx, ep.ID)
	if err != nil {
		return nil, 0, 0, err
	}
	assets, aerr := s.repo.ListAssets(ctx, proj.ID)
	if aerr != nil {
		s.log.WarnContext(ctx, "studio: failed to list assets for keyframe enqueue", "project_id", proj.ID, "error", aerr)
	}
	tagPaths := buildAssetPathMap(assets)
	wantIDs := toSet(p.ShotIDs)
	outDir := ""
	if epRoot != "" {
		outDir = filepath.Join(epRoot, "media", "keyframes")
	}
	modelRef := firstNonEmpty(p.Model, proj.DefaultImageModel)
	provID, modID := domainstudio.SplitModelRefForPricing(modelRef)
	unitPrice, _, perr := s.EstimateUnitPriceUSD(ctx, provID, modID, "image", "image")
	if perr != nil {
		s.log.WarnContext(ctx, "studio: failed to estimate image unit price", "error", perr)
	}

	var out []domainstudio.Job
	skipped := 0
	estTotal := 0.0
	for _, sh := range shots {
		if len(wantIDs) > 0 && !wantIDs[sh.ID] {
			continue
		}
		if !p.Force && strings.TrimSpace(sh.KeyframePath) != "" {
			skipped++
			continue
		}
		payload := queuedImageJobRequest{
			TargetKind: domainstudio.TargetKeyframe,
			Input: contracts.ImageGenerateInput{
				Model:           modelRef,
				Prompt:          composePrompt(proj.StylePrompt, firstNonEmpty(sh.ImagePrompt, sh.VideoPrompt, sh.Title)),
				ReferenceImages: collectRefImages(sh.RefAssetTags, tagPaths),
				AspectRatio:     proj.AspectRatio,
				OutputDir:       outDir,
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
			JobKind:        domainstudio.JobImage,
			ProviderID:     provID,
			ModelRef:       modelRef,
			IdempotencyKey: fmt.Sprintf("studio:%s:keyframe:%s", ep.ID, sh.ID),
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

func (s *Service) enqueueAssets(ctx context.Context, proj domainstudio.Project, ep domainstudio.Episode, epRoot, projRoot string, p contracts.StudioQueueEnqueuePayload) ([]domainstudio.Job, int, float64, error) {
	assets, err := s.repo.ListAssets(ctx, proj.ID)
	if err != nil {
		return nil, 0, 0, err
	}
	wantIDs := toSet(p.AssetIDs)
	outDir := ""
	if epRoot != "" {
		outDir = filepath.Join(epRoot, "media", "assets")
	} else if projRoot != "" {
		outDir = filepath.Join(projRoot, "assets")
	}
	modelRef := firstNonEmpty(p.Model, proj.DefaultImageModel)
	provID, modID := domainstudio.SplitModelRefForPricing(modelRef)
	unitPrice, _, perr := s.EstimateUnitPriceUSD(ctx, provID, modID, "image", "image")
	if perr != nil {
		s.log.WarnContext(ctx, "studio: failed to estimate asset image unit price", "error", perr)
	}

	var out []domainstudio.Job
	skipped := 0
	estTotal := 0.0
	for _, a := range assets {
		if len(wantIDs) > 0 && !wantIDs[a.ID] {
			continue
		}
		if !p.Force && strings.TrimSpace(a.PrimaryPath) != "" {
			skipped++
			continue
		}
		payload := queuedImageJobRequest{
			TargetKind: domainstudio.TargetAsset,
			Input: contracts.ImageGenerateInput{
				Model:       modelRef,
				Prompt:      composePrompt(proj.StylePrompt, firstNonEmpty(a.Prompt, a.Description, a.Name)),
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
			AssetID:        a.ID,
			JobKind:        domainstudio.JobImage,
			ProviderID:     provID,
			ModelRef:       modelRef,
			IdempotencyKey: fmt.Sprintf("studio:%s:asset:%s", proj.ID, a.ID),
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

func (s *Service) enqueueShotTTS(ctx context.Context, proj domainstudio.Project, ep domainstudio.Episode, epRoot string, p contracts.StudioQueueEnqueuePayload) ([]domainstudio.Job, int, float64, error) {
	shots, err := s.repo.ListShots(ctx, ep.ID)
	if err != nil {
		return nil, 0, 0, err
	}
	assets, aerr := s.repo.ListAssets(ctx, proj.ID)
	if aerr != nil {
		s.log.WarnContext(ctx, "studio: failed to list assets for tts enqueue", "project_id", proj.ID, "error", aerr)
	}
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
		outDir = filepath.Join(epRoot, "media", "tts")
	}
	var out []domainstudio.Job
	skipped := 0
	estTotal := 0.0
	for _, sh := range shots {
		if len(wantIDs) > 0 && !wantIDs[sh.ID] {
			continue
		}
		txt := shotSpeechText(sh)
		if txt == "" || (!p.Force && strings.TrimSpace(sh.AudioPath) != "") {
			skipped++
			continue
		}
		modelRef, voiceID := resolveShotVoiceConfig(sh, p.Model, proj.DefaultTTSModel, "", voiceBySpeaker)
		provID, modID := domainstudio.SplitModelRefForPricing(modelRef)
		unitPrice, _, perr := s.EstimateUnitPriceUSD(ctx, provID, modID, "tts", "1k_chars")
		if perr != nil {
			s.log.WarnContext(ctx, "studio: failed to estimate tts unit price", "error", perr)
		}
		est := math.Round((float64(len([]rune(txt)))/1000.0)*unitPrice*10000) / 10000
		payload := queuedTTSJobRequest{
			Input: contracts.TTSGenerateInput{
				Text:      txt,
				Model:     modelRef,
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
			JobKind:        domainstudio.JobTTS,
			ProviderID:     provID,
			ModelRef:       modelRef,
			IdempotencyKey: fmt.Sprintf("studio:%s:tts:%s", ep.ID, sh.ID),
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
