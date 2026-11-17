package studio

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"app/internal/apperr"
	"app/internal/contracts"
	domainmedia "app/internal/domain/mediagen"
	domainstudio "app/internal/domain/studio"
	"app/internal/engine"
	"app/internal/safego"
	mediagennodes "app/internal/tools/mediagen"
	studionodes "app/internal/tools/studio"
)

// VideoQueueMedia defines two-phase video submission and single-probe polling for durable job recovery.
type VideoQueueMedia interface {
	SubmitVideoJob(ctx context.Context, req domainmedia.VideoRequest) (domainmedia.VideoSubmitToken, error)
	PollVideoJobOnce(ctx context.Context, token domainmedia.VideoSubmitToken) (domainmedia.VideoPollStatus, error)
}

type queuedImageJobRequest struct {
	TargetKind domainstudio.MediaTargetKind `json:"target_kind"`
	Input      contracts.ImageGenerateInput `json:"input"`
}

type queuedTTSJobRequest struct {
	Input contracts.TTSGenerateInput `json:"input"`
}

type queuedLipSyncJobRequest struct {
	Input contracts.LipSyncGenerateInput `json:"input"`
}

func (s *Service) wakeWorker() {
	if s.wakeCh == nil {
		return
	}
	select {
	case s.wakeCh <- struct{}{}:
	default:
	}
}

func (s *Service) withWorkerGuard(ctx context.Context) context.Context {
	if s.fileGuard != nil {
		return engine.WithFileGuard(ctx, s.fileGuard)
	}
	return ctx
}

// RecoverOnStartup inspects studio_jobs on startup so submitted/polling jobs with a persisted
// provider_job_id resume polling immediately without ever calling remote Submit twice.
func (s *Service) RecoverOnStartup(ctx context.Context) error {
	if s.repo == nil {
		return nil
	}
	jobs, err := s.repo.ListRecoverableJobs(ctx)
	if err != nil {
		return err
	}
	now := time.Now().UnixMilli()
	for _, j := range jobs {
		switch {
		case j.ProviderJobID != "":
			if j.Status != domainstudio.JobPolling || j.NextPollAt > now {
				j.Status = domainstudio.JobPolling
				j.NextPollAt = now
				_, _ = s.repo.UpdateJob(ctx, j)
			}
		case j.Status == domainstudio.JobSubmitted:
			if j.Attempts >= j.MaxAttempts {
				j.Status = domainstudio.JobFailed
				j.ErrorText = "worker terminated before receiving provider job id"
				j.FinishedAt = now
			} else {
				j.Status = domainstudio.JobQueued
			}
			_, _ = s.repo.UpdateJob(ctx, j)
		}
	}
	return nil
}

// RunQueueWorker recovers unfinished jobs and runs the durable studio worker pool until ctx closes.
func (s *Service) RunQueueWorker(ctx context.Context) {
	if err := s.RecoverOnStartup(ctx); err != nil {
		s.log.WarnContext(ctx, "studio: queue startup recovery failed", "error", err)
	}
	workers := s.cfg.QueueWorkers
	pollInterval := s.cfg.QueuePollInterval

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		safego.Go("studio-queue-worker", func() {
			defer wg.Done()
			for {
				if ctx.Err() != nil {
					return
				}
				if s.StepQueueOnce(ctx) {
					continue
				}
				timer := time.NewTimer(pollInterval)
				select {
				case <-ctx.Done():
					timer.Stop()
					return
				case <-s.wakeCh:
					timer.Stop()
				case <-timer.C:
				}
			}
		})
	}
	wg.Wait()
}

// StepQueueOnce processes at most one ready polling job (first priority) or queued job.
func (s *Service) StepQueueOnce(ctx context.Context) bool {
	if s.repo == nil || ctx.Err() != nil {
		return false
	}
	now := time.Now().UnixMilli()
	leaseMs := max(s.cfg.QueuePollLeaseMin.Milliseconds(), s.cfg.QueuePollInterval.Milliseconds()*4)

	if pollJob, ok, err := s.repo.ClaimNextPollJob(ctx, now, leaseMs); err == nil && ok {
		s.executePollJob(ctx, pollJob)
		return true
	}
	if queuedJob, ok, err := s.repo.ClaimNextQueuedJob(ctx, now); err == nil && ok {
		s.executeQueuedJob(ctx, queuedJob)
		return true
	}
	return false
}

// EnqueueBatch validates approval gating, computes idempotency keys & preflight costs,
// inserts durable jobs into studio_jobs, and wakes background workers.
func (s *Service) EnqueueBatch(ctx context.Context, p contracts.StudioQueueEnqueuePayload) (contracts.StudioQueueEnqueueResult, error) {
	if s.repo == nil {
		return contracts.StudioQueueEnqueueResult{}, apperr.Unavailable("studio repository unavailable")
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
			WithDetails(map[string]any{"approval_required": true, "project_id": proj.ID, "stage": p.Stage})
	}

	epRoot := s.resolveEpisodeRoot(proj, ep.Code)
	projRoot := s.resolveProjectRoot(proj)
	if epRoot != "" {
		_ = ensureEpisodeDirs(epRoot)
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
		created, skipped, totalEst, err = s.enqueueAssets(ctx, proj, ep, projRoot, p)
	case "generate_tts":
		created, skipped, totalEst, err = s.enqueueShotTTS(ctx, proj, ep, epRoot, p)
	case "lipsync":
		created, skipped, totalEst, err = s.enqueueShotLipSync(ctx, proj, ep, epRoot, p)
	default:
		return contracts.StudioQueueEnqueueResult{}, apperr.Validation("unsupported queue enqueue stage " + p.Stage)
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
		return contracts.StudioQueueListResult{}, apperr.Unavailable("studio repository unavailable")
	}
	jobs, err := s.repo.ListJobs(ctx, domainstudio.JobFilter{
		ProjectID: strings.TrimSpace(p.ProjectID),
		EpisodeID: strings.TrimSpace(p.EpisodeID),
		Status:    domainstudio.JobStatus(strings.TrimSpace(p.Status)),
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
		return contracts.StudioQueueControlResult{}, apperr.Unavailable("studio repository unavailable")
	}
	if err := p.Validate(); err != nil {
		return contracts.StudioQueueControlResult{}, err
	}
	act := strings.ToLower(strings.TrimSpace(p.Action))
	switch act {
	case "pause":
		if p.JobID != "" {
			j, err := s.repo.GetJob(ctx, p.JobID)
			if err != nil {
				return contracts.StudioQueueControlResult{}, err
			}
			if !j.Status.IsTerminal() {
				j.Status = domainstudio.JobPaused
				j, err = s.repo.UpdateJob(ctx, j)
				if err != nil {
					return contracts.StudioQueueControlResult{}, err
				}
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
		s.emit(contracts.EventStudioQueueUpdated, contracts.StudioQueueUpdatedEvent{ProjectID: p.ProjectID, EpisodeID: p.EpisodeID, Summary: fmt.Sprintf("Paused %d job(s)", n)})
		return contracts.StudioQueueControlResult{Affected: n}, nil

	case "resume":
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
				j, err = s.repo.UpdateJob(ctx, j)
				if err != nil {
					return contracts.StudioQueueControlResult{}, err
				}
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

	case "cancel":
		j, err := s.repo.CancelJob(ctx, p.JobID)
		if err != nil {
			return contracts.StudioQueueControlResult{}, err
		}
		dto := jobToDTO(j)
		s.emit(contracts.EventStudioQueueUpdated, contracts.StudioQueueUpdatedEvent{ProjectID: j.ProjectID, EpisodeID: j.EpisodeID, Job: &dto, Summary: "Job canceled"})
		return contracts.StudioQueueControlResult{Affected: 1, Job: &dto}, nil

	case "retry":
		j, err := s.repo.GetJob(ctx, p.JobID)
		if err != nil {
			return contracts.StudioQueueControlResult{}, err
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
		j, err = s.repo.UpdateJob(ctx, j)
		if err != nil {
			return contracts.StudioQueueControlResult{}, err
		}
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
		if !force {
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
	outDir := filepath.Join(epRoot, "videos")
	modelRef := firstNonEmpty(p.Model, proj.DefaultVideoModel)
	provID, modID := domainstudio.SplitModelRefForPricing(modelRef)
	unitPrice, _, _ := s.EstimateUnitPriceUSD(ctx, provID, modID, "video", "second")

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
		reqBytes, _ := json.Marshal(req)
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
	assets, _ := s.repo.ListAssets(ctx, proj.ID)
	tagPaths := buildAssetPathMap(assets)
	wantIDs := toSet(p.ShotIDs)
	outDir := filepath.Join(epRoot, "images")
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
		reqBytes, _ := json.Marshal(payload)
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

func (s *Service) enqueueAssets(ctx context.Context, proj domainstudio.Project, ep domainstudio.Episode, projRoot string, p contracts.StudioQueueEnqueuePayload) ([]domainstudio.Job, int, float64, error) {
	assets, err := s.repo.ListAssets(ctx, proj.ID)
	if err != nil {
		return nil, 0, 0, err
	}
	wantIDs := toSet(p.AssetIDs)
	outDir := filepath.Join(projRoot, "assets")
	modelRef := firstNonEmpty(p.Model, proj.DefaultImageModel)
	provID, modID := domainstudio.SplitModelRefForPricing(modelRef)
	unitPrice, _, _ := s.EstimateUnitPriceUSD(ctx, provID, modID, "image", "image")

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
		reqBytes, _ := json.Marshal(payload)
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
	assets, _ := s.repo.ListAssets(ctx, proj.ID)
	voiceBySpeaker := map[string]domainstudio.AssetEntry{}
	for _, a := range assets {
		if a.Kind == domainstudio.AssetCharacter && (a.VoiceID != "" || a.VoiceProvider != "") {
			voiceBySpeaker[strings.ToLower(strings.TrimSpace(a.Name))] = a
			voiceBySpeaker[strings.ToLower(strings.TrimSpace(a.CodeTag))] = a
		}
	}
	wantIDs := toSet(p.ShotIDs)
	outDir := filepath.Join(epRoot, "audio")
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
		modelRef := firstNonEmpty(p.Model, proj.DefaultTTSModel)
		voiceID := ""
		if len(sh.Dialogues) > 0 {
			spk := strings.ToLower(strings.TrimSpace(sh.Dialogues[0].Speaker))
			if ca, ok := voiceBySpeaker[spk]; ok {
				voiceID = ca.VoiceID
				if p.Model == "" && ca.VoiceProvider != "" {
					modelRef = ca.VoiceProvider
				}
			}
		}
		provID, modID := domainstudio.SplitModelRefForPricing(modelRef)
		unitPrice, _, _ := s.EstimateUnitPriceUSD(ctx, provID, modID, "tts", "1k_chars")
		est := math.Round((float64(len([]rune(txt)))/1000.0)*unitPrice*10000) / 10000
		payload := queuedTTSJobRequest{
			Input: contracts.TTSGenerateInput{
				Text:      txt,
				Model:     modelRef,
				Voice:     voiceID,
				OutputDir: outDir,
			},
		}
		reqBytes, _ := json.Marshal(payload)
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

func (s *Service) enqueueShotLipSync(ctx context.Context, proj domainstudio.Project, ep domainstudio.Episode, epRoot string, p contracts.StudioQueueEnqueuePayload) ([]domainstudio.Job, int, float64, error) {
	shots, err := s.repo.ListShots(ctx, ep.ID)
	if err != nil {
		return nil, 0, 0, err
	}
	wantIDs := toSet(p.ShotIDs)
	outDir := filepath.Join(epRoot, "videos")
	provID, modID := domainstudio.SplitModelRefForPricing(p.Model)
	unitPrice, _, _ := s.EstimateUnitPriceUSD(ctx, provID, modID, "lipsync", "second")

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
				VideoPath: sh.VideoPath,
				AudioPath: sh.AudioPath,
				ModelRef:  p.Model,
				OutputDir: outDir,
			},
		}
		reqBytes, _ := json.Marshal(payload)
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

func (s *Service) executeQueuedJob(ctx context.Context, j domainstudio.Job) {
	now := time.Now().UnixMilli()
	runCtx := s.withWorkerGuard(ctx)
	runCtx = domainmedia.ContextWithUsageMeta(runCtx, domainmedia.UsageMeta{
		ProjectID:    j.ProjectID,
		EpisodeID:    j.EpisodeID,
		ShotID:       j.ShotID,
		JobID:        j.ID,
		Operation:    string(j.JobKind),
		EstimatedUSD: j.EstimatedUSD,
	})

	switch j.JobKind {
	case domainstudio.JobVideo:
		if s.videoMedia == nil {
			s.failJob(ctx, j, apperr.Unavailable("video generation queue driver is unavailable"))
			return
		}
		var req domainmedia.VideoRequest
		if err := json.Unmarshal([]byte(j.RequestJSON), &req); err != nil {
			s.failJob(ctx, j, err)
			return
		}
		tok, err := s.videoMedia.SubmitVideoJob(runCtx, req)
		if err != nil {
			s.handleSubmitError(ctx, j, err)
			return
		}
		tokJSON, _ := json.Marshal(tok)
		// Durable submit checkpoint: once provider_job_id is stored, restarts only resume polling!
		updated, err := s.repo.SetJobProviderSubmission(ctx, j.ID, tok.ProviderJobID, string(tokJSON), domainstudio.JobPolling, now)
		if err != nil {
			s.log.ErrorContext(ctx, "studio: failed to persist provider_job_id", "id", j.ID, "provider_job_id", tok.ProviderJobID, "error", err)
			return
		}
		dto := jobToDTO(updated)
		s.emit(contracts.EventStudioQueueUpdated, contracts.StudioQueueUpdatedEvent{
			ProjectID: updated.ProjectID, EpisodeID: updated.EpisodeID, Job: &dto, Summary: "Remote video task submitted",
		})

	case domainstudio.JobImage:
		if s.wf == nil {
			s.failJob(ctx, j, apperr.Unavailable("workflow engine unavailable"))
			return
		}
		var qReq queuedImageJobRequest
		if err := json.Unmarshal([]byte(j.RequestJSON), &qReq); err != nil {
			s.failJob(ctx, j, err)
			return
		}
		inBytes, _ := json.Marshal(qReq.Input)
		rawOut, err := s.wf.RunNode(runCtx, mediagennodes.NodeTypeImage, inBytes)
		if err != nil {
			s.handleSubmitError(ctx, j, err)
			return
		}
		var out mediagennodes.ImageGenerateOutput
		if merr := decodeNodeOutput(rawOut, &out); merr != nil {
			s.failJob(ctx, j, merr)
			return
		}
		targetID := firstNonEmpty(j.AssetID, j.ShotID)
		regID := s.registerMedia(ctx, "image", mediagennodes.NodeTypeImage, out.Path, qReq.Input.OutputDir, 0)
		s.recordSelectedVersion(ctx, j.ProjectID, j.EpisodeID, qReq.TargetKind, targetID, out.Path, mediagennodes.NodeTypeImage, qReq.Input.Prompt, j.ModelRef)
		_, _ = s.applyMediaPathToTarget(ctx, j.ProjectID, j.EpisodeID, qReq.TargetKind, targetID, out.Path, regID, "queue_image_done")
		s.succeedJob(ctx, j, out.Path)

	case domainstudio.JobTTS:
		if s.wf == nil {
			s.failJob(ctx, j, apperr.Unavailable("workflow engine unavailable"))
			return
		}
		var qReq queuedTTSJobRequest
		if err := json.Unmarshal([]byte(j.RequestJSON), &qReq); err != nil {
			s.failJob(ctx, j, err)
			return
		}
		inBytes, _ := json.Marshal(qReq.Input)
		rawOut, err := s.wf.RunNode(runCtx, mediagennodes.NodeTypeTTS, inBytes)
		if err != nil {
			s.handleSubmitError(ctx, j, err)
			return
		}
		var out mediagennodes.TTSGenerateOutput
		if merr := decodeNodeOutput(rawOut, &out); merr != nil {
			s.failJob(ctx, j, merr)
			return
		}
		regID := s.registerMedia(ctx, "audio", mediagennodes.NodeTypeTTS, out.Path, qReq.Input.OutputDir, 0)
		s.recordSelectedVersion(ctx, j.ProjectID, j.EpisodeID, domainstudio.TargetDubAudio, j.ShotID, out.Path, mediagennodes.NodeTypeTTS, qReq.Input.Text, j.ModelRef)
		_, _ = s.applyMediaPathToTarget(ctx, j.ProjectID, j.EpisodeID, domainstudio.TargetDubAudio, j.ShotID, out.Path, regID, "queue_tts_done")
		s.succeedJob(ctx, j, out.Path)

	case domainstudio.JobLipSync:
		if s.wf == nil {
			s.failJob(ctx, j, apperr.Unavailable("workflow engine unavailable"))
			return
		}
		var qReq queuedLipSyncJobRequest
		if err := json.Unmarshal([]byte(j.RequestJSON), &qReq); err != nil {
			s.failJob(ctx, j, err)
			return
		}
		inBytes, _ := json.Marshal(qReq.Input)
		rawOut, err := s.wf.RunNode(runCtx, studionodes.NodeTypeLipSyncGenerate, inBytes)
		if err != nil {
			s.handleSubmitError(ctx, j, err)
			return
		}
		var out contracts.LipSyncGenerateOutput
		if merr := decodeNodeOutput(rawOut, &out); merr != nil {
			s.failJob(ctx, j, merr)
			return
		}
		s.recordSelectedVersion(ctx, j.ProjectID, j.EpisodeID, domainstudio.TargetLipSyncVideo, j.ShotID, out.Path, studionodes.NodeTypeLipSyncGenerate, qReq.Input.Prompt, j.ModelRef)
		_, _ = s.applyMediaPathToTarget(ctx, j.ProjectID, j.EpisodeID, domainstudio.TargetLipSyncVideo, j.ShotID, out.Path, out.AssetID, "queue_lipsync_done")
		s.succeedJob(ctx, j, out.Path)

	default:
		s.failJob(ctx, j, apperr.Validation("unsupported job kind "+string(j.JobKind)))
	}
}

func (s *Service) executePollJob(ctx context.Context, j domainstudio.Job) {
	if s.videoMedia == nil {
		s.failJob(ctx, j, apperr.Unavailable("video polling driver is unavailable"))
		return
	}
	var tok domainmedia.VideoSubmitToken
	if err := json.Unmarshal([]byte(j.SubmitTokenJSON), &tok); err != nil {
		s.failJob(ctx, j, err)
		return
	}
	runCtx := s.withWorkerGuard(ctx)
	runCtx = domainmedia.ContextWithUsageMeta(runCtx, domainmedia.UsageMeta{
		ProjectID:    j.ProjectID,
		EpisodeID:    j.EpisodeID,
		ShotID:       j.ShotID,
		JobID:        j.ID,
		Operation:    "generate_video",
		EstimatedUSD: j.EstimatedUSD,
	})

	st, err := s.videoMedia.PollVideoJobOnce(runCtx, tok)
	now := time.Now().UnixMilli()
	pollDelay := max(1, s.cfg.QueuePollInterval.Milliseconds())

	if err != nil {
		if st.Failed {
			s.failJob(ctx, j, err)
			if j.ShotID != "" {
				if sh, serr := s.repo.GetShot(ctx, j.ShotID); serr == nil {
					sh.Status = domainstudio.ShotFailed
					sh.ErrorText = err.Error()
					_, _ = s.repo.UpsertShot(ctx, sh)
				}
			}
			return
		}
		j.Status = domainstudio.JobPolling
		j.ErrorText = err.Error()
		j.NextPollAt = now + pollDelay
		_, _ = s.repo.UpdateJob(ctx, j)
		return
	}

	if !st.Done {
		j.Status = domainstudio.JobPolling
		if st.Progress > j.Progress {
			j.Progress = st.Progress
		}
		j.ErrorText = ""
		j.NextPollAt = now + pollDelay
		updated, uerr := s.repo.UpdateJob(ctx, j)
		if uerr == nil {
			dto := jobToDTO(updated)
			s.emit(contracts.EventStudioQueueUpdated, contracts.StudioQueueUpdatedEvent{
				ProjectID: updated.ProjectID, EpisodeID: updated.EpisodeID, Job: &dto, Summary: "Polling video job",
			})
		}
		return
	}

	outPath := st.Result.Path
	if cur, cerr := s.repo.GetJob(ctx, j.ID); cerr == nil && cur.Status == domainstudio.JobCanceled {
		return
	}
	var regID string
	if j.ShotID != "" {
		if sh, serr := s.repo.GetShot(ctx, j.ShotID); serr == nil {
			regID = s.registerMedia(ctx, "video", mediagennodes.NodeTypeVideo, outPath, tok.OutputDir, st.Result.Duration)
			s.recordSelectedVersion(ctx, j.ProjectID, j.EpisodeID, domainstudio.TargetVideo, sh.ID, outPath, mediagennodes.NodeTypeVideo, sh.VideoPrompt, j.ModelRef)
			_, _ = s.applyMediaPathToTarget(ctx, j.ProjectID, j.EpisodeID, domainstudio.TargetVideo, sh.ID, outPath, regID, "queue_video_done")
		}
	}
	s.succeedJob(ctx, j, outPath)
}

func (s *Service) handleSubmitError(ctx context.Context, j domainstudio.Job, err error) {
	if j.Attempts < j.MaxAttempts {
		j.Status = domainstudio.JobQueued
		j.ErrorText = err.Error()
		updated, uerr := s.repo.UpdateJob(ctx, j)
		if uerr == nil {
			dto := jobToDTO(updated)
			s.emit(contracts.EventStudioQueueUpdated, contracts.StudioQueueUpdatedEvent{
				ProjectID: updated.ProjectID, EpisodeID: updated.EpisodeID, Job: &dto, Summary: "Retrying queued job",
			})
		}
		return
	}
	s.failJob(ctx, j, err)
}

func (s *Service) succeedJob(ctx context.Context, j domainstudio.Job, outPath string) {
	now := time.Now().UnixMilli()
	j.Status = domainstudio.JobSucceeded
	j.Progress = 100
	j.OutputPath = outPath
	j.ErrorText = ""
	j.FinishedAt = now
	if cur, cerr := s.repo.GetJob(ctx, j.ID); cerr == nil && cur.ActualUSD > 0 {
		j.ActualUSD = cur.ActualUSD
	}
	updated, err := s.repo.UpdateJob(ctx, j)
	if err == nil {
		dto := jobToDTO(updated)
		s.emit(contracts.EventStudioQueueUpdated, contracts.StudioQueueUpdatedEvent{
			ProjectID: updated.ProjectID, EpisodeID: updated.EpisodeID, Job: &dto, Summary: "Job completed",
		})
	}
}

func (s *Service) failJob(ctx context.Context, j domainstudio.Job, err error) {
	j.Status = domainstudio.JobFailed
	j.ErrorText = err.Error()
	j.FinishedAt = time.Now().UnixMilli()
	updated, uerr := s.repo.UpdateJob(ctx, j)
	if uerr == nil {
		dto := jobToDTO(updated)
		s.emit(contracts.EventStudioQueueUpdated, contracts.StudioQueueUpdatedEvent{
			ProjectID: updated.ProjectID, EpisodeID: updated.EpisodeID, Job: &dto, Summary: "Job failed",
		})
	}
}
