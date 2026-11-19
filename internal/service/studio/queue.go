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
)

// maxRetryShiftExp prevents 64-bit integer overflow when computing 1<<attempt exponential backoff;
// actual delay remains bounded by StudioConfig.QueuePollBackoffMax.
const maxRetryShiftExp = 10

// VideoQueueMedia defines two-phase video submission and single-probe polling for durable job recovery.
type VideoQueueMedia interface {
	SubmitVideoJob(ctx context.Context, req domainmedia.VideoRequest) (domainmedia.VideoSubmitToken, error)
	PollVideoJobOnce(ctx context.Context, token domainmedia.VideoSubmitToken) (domainmedia.VideoPollStatus, error)
}

type activeJobEntry struct {
	projectID string
	episodeID string
	cancel    context.CancelFunc
	done      chan struct{}
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

type queuedTTSDubMuxJobRequest struct {
	Input contracts.TTSDubMuxInput `json:"input"`
}

type queuedVideoWatermarkJobRequest struct {
	Input contracts.VideoWatermarkRemoveInput `json:"input"`
}

type queuedMaskEditJobRequest struct {
	TargetKind domainstudio.MediaTargetKind `json:"target_kind"`
	Input      contracts.MaskEditInput      `json:"input"`
}

type queuedGridSplitJobRequest struct {
	TargetKind domainstudio.MediaTargetKind `json:"target_kind,omitempty"`
	Input      contracts.GridSplitInput     `json:"input"`
}

type queuedExtractFramesJobRequest struct {
	TargetShotID string                       `json:"target_shot_id,omitempty"`
	Input        contracts.ExtractFramesInput `json:"input"`
}

type queuedQCReviewJobRequest struct {
	TargetKind domainstudio.MediaTargetKind `json:"target_kind"`
	MediaPath  string                       `json:"media_path"`
	Input      contracts.QCReviewInput      `json:"input"`
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
	s.mu.RLock()
	fg := s.fileGuard
	s.mu.RUnlock()
	if fg != nil {
		return engine.WithFileGuard(ctx, fg)
	}
	return ctx
}

func (s *Service) trackActiveJob(ctx context.Context, j domainstudio.Job) (context.Context, func()) {
	jobCtx, cancel := context.WithCancel(ctx)
	doneCh := make(chan struct{})
	entry := &activeJobEntry{
		projectID: j.ProjectID,
		episodeID: j.EpisodeID,
		cancel:    cancel,
		done:      doneCh,
	}
	s.jobMu.Lock()
	if s.activeJobs == nil {
		s.activeJobs = make(map[string]*activeJobEntry)
	}
	s.activeJobs[j.ID] = entry
	s.jobMu.Unlock()

	return jobCtx, func() {
		cancel()
		s.jobMu.Lock()
		if cur, ok := s.activeJobs[j.ID]; ok && cur == entry {
			delete(s.activeJobs, j.ID)
		}
		s.jobMu.Unlock()
		close(doneCh)
	}
}

func (s *Service) waitProjectActiveJobs(projectID string, timeout time.Duration) {
	pID := strings.TrimSpace(projectID)
	var waits []chan struct{}
	s.jobMu.Lock()
	for _, entry := range s.activeJobs {
		if entry == nil || entry.done == nil {
			continue
		}
		if pID != "" && entry.projectID != pID {
			continue
		}
		waits = append(waits, entry.done)
	}
	s.jobMu.Unlock()
	if len(waits) == 0 {
		return
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for _, ch := range waits {
		select {
		case <-ch:
		case <-timer.C:
			return
		}
	}
}

func (s *Service) recordPollError(jobID string) int {
	id := strings.TrimSpace(jobID)
	if id == "" {
		return 0
	}
	s.jobMu.Lock()
	if s.pollErrors == nil {
		s.pollErrors = make(map[string]int)
	}
	s.pollErrors[id]++
	cnt := s.pollErrors[id]
	s.jobMu.Unlock()
	return cnt
}

func (s *Service) clearPollErrors(jobID string) {
	id := strings.TrimSpace(jobID)
	if id == "" {
		return
	}
	s.jobMu.Lock()
	delete(s.pollErrors, id)
	s.jobMu.Unlock()
}

func (s *Service) cancelActiveJob(jobID string) {
	id := strings.TrimSpace(jobID)
	if id == "" {
		return
	}
	s.jobMu.Lock()
	entry := s.activeJobs[id]
	s.jobMu.Unlock()
	if entry != nil && entry.cancel != nil {
		entry.cancel()
	}
}

func (s *Service) cancelEpisodeActiveJobs(projectID, episodeID string) {
	pID := strings.TrimSpace(projectID)
	epID := strings.TrimSpace(episodeID)
	var cancels []context.CancelFunc
	s.jobMu.Lock()
	for _, entry := range s.activeJobs {
		if entry == nil || entry.cancel == nil {
			continue
		}
		if pID != "" && entry.projectID != pID {
			continue
		}
		if epID != "" && entry.episodeID != epID {
			continue
		}
		cancels = append(cancels, entry.cancel)
	}
	s.jobMu.Unlock()
	for _, fn := range cancels {
		fn()
	}
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
				if _, uerr := s.repo.UpdateJob(ctx, j); uerr != nil {
					s.log.WarnContext(ctx, "studio: failed to update polling job on startup recovery", "id", j.ID, "error", uerr)
				}
			}
		case j.Status == domainstudio.JobSubmitted:
			if j.Attempts >= j.MaxAttempts {
				j.Status = domainstudio.JobFailed
				j.ErrorText = "worker terminated before receiving provider job id"
				j.FinishedAt = now
			} else {
				j.Status = domainstudio.JobQueued
			}
			if _, uerr := s.repo.UpdateJob(ctx, j); uerr != nil {
				s.log.WarnContext(ctx, "studio: failed to recover submitted job", "id", j.ID, "error", uerr)
			}
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

	pollJob, ok, err := s.repo.ClaimNextPollJob(ctx, now, leaseMs)
	if err != nil {
		if ctx.Err() == nil {
			s.log.WarnContext(ctx, "studio: failed to claim next polling job", "error", err)
		}
	} else if ok {
		s.executePollJob(ctx, pollJob)
		return true
	}

	queuedJob, ok, err := s.repo.ClaimNextQueuedJob(ctx, now)
	if err != nil {
		if ctx.Err() == nil {
			s.log.WarnContext(ctx, "studio: failed to claim next queued job", "error", err)
		}
	} else if ok {
		s.executeQueuedJob(ctx, queuedJob)
		return true
	}
	return false
}

// EnqueueBatch validates approval gating, computes idempotency keys & preflight costs,
// inserts durable jobs into studio_jobs, and wakes background workers.
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

func (s *Service) runQueuedWorkflowNode(ctx, runCtx context.Context, j domainstudio.Job, nodeType string, input any, out any) bool {
	if s.wf == nil {
		s.failJob(ctx, j, apperr.Unavailable("workflow engine unavailable"))
		return false
	}
	inBytes, merr := json.Marshal(input)
	if merr != nil {
		s.failJob(ctx, j, apperr.Internal(merr))
		return false
	}
	rawOut, err := s.wf.RunNode(runCtx, nodeType, inBytes)
	if err != nil {
		if s.isJobCanceledOrPaused(ctx, j.ID) || ctx.Err() != nil {
			return false
		}
		s.handleSubmitError(ctx, j, err)
		return false
	}
	if merr := decodeNodeOutput(rawOut, out); merr != nil {
		s.failJob(ctx, j, merr)
		return false
	}
	return !s.isJobCanceledOrPaused(ctx, j.ID)
}

func (s *Service) executeQueuedJob(ctx context.Context, j domainstudio.Job) {
	if s.isJobCanceledOrPaused(ctx, j.ID) {
		return
	}
	now := time.Now().UnixMilli()
	runCtx, untrack := s.trackActiveJob(s.withWorkerGuard(ctx), j)
	defer untrack()

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
			if s.isJobCanceledOrPaused(ctx, j.ID) || ctx.Err() != nil {
				return
			}
			s.handleSubmitError(ctx, j, err)
			return
		}
		tokJSON, _ := json.Marshal(tok)
		// Durable submit checkpoint: once provider_job_id is returned, persist it even if ctx was canceled right at return.
		persistCtx, cancelPersist := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancelPersist()
		updated, err := s.repo.SetJobProviderSubmission(persistCtx, j.ID, tok.ProviderJobID, string(tokJSON), domainstudio.JobPolling, now)
		if err != nil {
			s.log.ErrorContext(persistCtx, "studio: failed to persist provider_job_id", "job_id", j.ID, "provider_job_id", tok.ProviderJobID, "error", err)
			j.ProviderJobID = tok.ProviderJobID
			j.SubmitTokenJSON = string(tokJSON)
			s.failJob(persistCtx, j, apperr.Wrap(apperr.CodeUnavailable, "Failed to persist submitted provider job state", err))
			return
		}
		dto := jobToDTO(updated)
		s.emit(contracts.EventStudioQueueUpdated, contracts.StudioQueueUpdatedEvent{
			ProjectID: updated.ProjectID, EpisodeID: updated.EpisodeID, Job: &dto, Summary: "Remote video task submitted",
		})

	case domainstudio.JobImage:
		var qReq queuedImageJobRequest
		if err := json.Unmarshal([]byte(j.RequestJSON), &qReq); err != nil {
			s.failJob(ctx, j, err)
			return
		}
		var out contracts.ImageGenerateOutput
		if !s.runQueuedWorkflowNode(ctx, runCtx, j, contracts.NodeTypeImageGenerate, qReq.Input, &out) {
			return
		}
		targetID := firstNonEmpty(j.AssetID, j.ShotID)
		regID := s.registerMedia(ctx, "image", contracts.NodeTypeImageGenerate, out.Path, qReq.Input.OutputDir, 0)
		s.recordSelectedVersion(ctx, j.ProjectID, j.EpisodeID, qReq.TargetKind, targetID, out.Path, contracts.NodeTypeImageGenerate, qReq.Input.Prompt, j.ModelRef)
		if _, aerr := s.applyMediaPathToTarget(ctx, j.ProjectID, j.EpisodeID, qReq.TargetKind, targetID, out.Path, regID, "queue_image_done"); aerr != nil {
			s.log.WarnContext(ctx, "studio: failed to apply image output to target", "job_id", j.ID, "target_id", targetID, "error", aerr)
		}
		s.succeedJob(ctx, j, out.Path)

	case domainstudio.JobTTS:
		var qReq queuedTTSJobRequest
		if err := json.Unmarshal([]byte(j.RequestJSON), &qReq); err != nil {
			s.failJob(ctx, j, err)
			return
		}
		var out contracts.TTSGenerateOutput
		if !s.runQueuedWorkflowNode(ctx, runCtx, j, contracts.NodeTypeTTSGenerate, qReq.Input, &out) {
			return
		}
		regID := s.registerMedia(ctx, "audio", contracts.NodeTypeTTSGenerate, out.Path, qReq.Input.OutputDir, 0)
		s.recordSelectedVersion(ctx, j.ProjectID, j.EpisodeID, domainstudio.TargetDubAudio, j.ShotID, out.Path, contracts.NodeTypeTTSGenerate, qReq.Input.Text, j.ModelRef)
		if _, aerr := s.applyMediaPathToTarget(ctx, j.ProjectID, j.EpisodeID, domainstudio.TargetDubAudio, j.ShotID, out.Path, regID, "queue_tts_done"); aerr != nil {
			s.log.WarnContext(ctx, "studio: failed to apply TTS output to shot", "job_id", j.ID, "shot_id", j.ShotID, "error", aerr)
		}
		s.succeedJob(ctx, j, out.Path)

	case domainstudio.JobLipSync:
		var qReq queuedLipSyncJobRequest
		if err := json.Unmarshal([]byte(j.RequestJSON), &qReq); err != nil {
			s.failJob(ctx, j, err)
			return
		}
		var out contracts.LipSyncGenerateOutput
		if !s.runQueuedWorkflowNode(ctx, runCtx, j, contracts.NodeTypeLipSyncGenerate, qReq.Input, &out) {
			return
		}
		s.recordSelectedVersion(ctx, j.ProjectID, j.EpisodeID, domainstudio.TargetLipSyncVideo, j.ShotID, out.Path, contracts.NodeTypeLipSyncGenerate, qReq.Input.Prompt, j.ModelRef)
		if _, aerr := s.applyMediaPathToTarget(ctx, j.ProjectID, j.EpisodeID, domainstudio.TargetLipSyncVideo, j.ShotID, out.Path, out.AssetID, "queue_lipsync_done"); aerr != nil {
			s.log.WarnContext(ctx, "studio: failed to apply lipsync output to shot", "job_id", j.ID, "shot_id", j.ShotID, "error", aerr)
		}
		s.succeedJob(ctx, j, out.Path)

	case domainstudio.JobTTSDubMux:
		var qReq queuedTTSDubMuxJobRequest
		if err := json.Unmarshal([]byte(j.RequestJSON), &qReq); err != nil {
			s.failJob(ctx, j, err)
			return
		}
		var out contracts.TTSDubMuxOutput
		if !s.runQueuedWorkflowNode(ctx, runCtx, j, contracts.NodeTypeTTSDubMux, qReq.Input, &out) {
			return
		}
		s.recordSelectedVersion(ctx, j.ProjectID, j.EpisodeID, domainstudio.TargetVideo, j.ShotID, out.Path, contracts.NodeTypeTTSDubMux, qReq.Input.Text, j.ModelRef)
		if _, aerr := s.applyMediaPathToTarget(ctx, j.ProjectID, j.EpisodeID, domainstudio.TargetVideo, j.ShotID, out.Path, out.AssetID, "queue_tts_dub_mux_done"); aerr != nil {
			s.log.WarnContext(ctx, "studio: failed to apply tts_dub_mux output to shot", "job_id", j.ID, "shot_id", j.ShotID, "error", aerr)
		}
		s.succeedJob(ctx, j, out.Path)

	case domainstudio.JobVideoWatermarkRemove:
		var qReq queuedVideoWatermarkJobRequest
		if err := json.Unmarshal([]byte(j.RequestJSON), &qReq); err != nil {
			s.failJob(ctx, j, err)
			return
		}
		var out contracts.VideoWatermarkRemoveOutput
		if !s.runQueuedWorkflowNode(ctx, runCtx, j, contracts.NodeTypeVideoWMRemove, qReq.Input, &out) {
			return
		}
		s.recordSelectedVersion(ctx, j.ProjectID, j.EpisodeID, domainstudio.TargetVideo, j.ShotID, out.Path, contracts.NodeTypeVideoWMRemove, "", "")
		if _, aerr := s.applyMediaPathToTarget(ctx, j.ProjectID, j.EpisodeID, domainstudio.TargetVideo, j.ShotID, out.Path, out.AssetID, "queue_video_wm_done"); aerr != nil {
			s.log.WarnContext(ctx, "studio: failed to apply video_wm_remove output to shot", "job_id", j.ID, "shot_id", j.ShotID, "error", aerr)
		}
		s.succeedJob(ctx, j, out.Path)

	case domainstudio.JobMaskEdit:
		var qReq queuedMaskEditJobRequest
		if err := json.Unmarshal([]byte(j.RequestJSON), &qReq); err != nil {
			s.failJob(ctx, j, err)
			return
		}
		var out contracts.MaskEditOutput
		if !s.runQueuedWorkflowNode(ctx, runCtx, j, contracts.NodeTypeMaskEdit, qReq.Input, &out) {
			return
		}
		targetID := firstNonEmpty(j.AssetID, j.ShotID)
		kind := qReq.TargetKind
		if !kind.Valid() {
			kind = domainstudio.TargetKeyframe
		}
		s.recordSelectedVersion(ctx, j.ProjectID, j.EpisodeID, kind, targetID, out.Path, contracts.NodeTypeMaskEdit, qReq.Input.Prompt, j.ModelRef)
		if _, aerr := s.applyMediaPathToTarget(ctx, j.ProjectID, j.EpisodeID, kind, targetID, out.Path, out.AssetID, "queue_mask_edit_done"); aerr != nil {
			s.log.WarnContext(ctx, "studio: failed to apply mask_edit output to target", "job_id", j.ID, "target_id", targetID, "error", aerr)
		}
		s.succeedJob(ctx, j, out.Path)

	case domainstudio.JobGridSplit:
		var qReq queuedGridSplitJobRequest
		if err := json.Unmarshal([]byte(j.RequestJSON), &qReq); err != nil {
			s.failJob(ctx, j, err)
			return
		}
		var out contracts.GridSplitOutput
		if !s.runQueuedWorkflowNode(ctx, runCtx, j, contracts.NodeTypeGridSplit, qReq.Input, &out) {
			return
		}
		firstPath := ""
		if len(out.Panels) > 0 {
			firstPath = out.Panels[0].Path
		}
		s.succeedJob(ctx, j, firstPath)

	case domainstudio.JobExtractFrames:
		var qReq queuedExtractFramesJobRequest
		if err := json.Unmarshal([]byte(j.RequestJSON), &qReq); err != nil {
			s.failJob(ctx, j, err)
			return
		}
		var out contracts.ExtractFramesOutput
		if !s.runQueuedWorkflowNode(ctx, runCtx, j, contracts.NodeTypeExtractFrames, qReq.Input, &out) {
			return
		}
		assetID := ""
		if len(out.Frames) > 0 {
			assetID = out.Frames[len(out.Frames)-1].AssetID
		}
		targetShotID := firstNonEmpty(qReq.TargetShotID, j.ShotID)
		if targetShotID != "" && out.LastFramePath != "" {
			s.recordSelectedVersion(ctx, j.ProjectID, j.EpisodeID, domainstudio.TargetKeyframe, targetShotID, out.LastFramePath, contracts.NodeTypeExtractFrames, "extracted last frame continuity", "")
			if _, aerr := s.applyMediaPathToTarget(ctx, j.ProjectID, j.EpisodeID, domainstudio.TargetKeyframe, targetShotID, out.LastFramePath, assetID, "queue_extract_frames_done"); aerr != nil {
				s.log.WarnContext(ctx, "studio: failed to apply extracted frame to shot", "job_id", j.ID, "shot_id", targetShotID, "error", aerr)
			}
		}
		s.succeedJob(ctx, j, out.LastFramePath)

	case domainstudio.JobQCReview:
		var qReq queuedQCReviewJobRequest
		if err := json.Unmarshal([]byte(j.RequestJSON), &qReq); err != nil {
			s.failJob(ctx, j, err)
			return
		}
		var out contracts.QCReviewOutput
		if !s.runQueuedWorkflowNode(ctx, runCtx, j, contracts.NodeTypeQCReview, qReq.Input, &out) {
			return
		}
		targetID := firstNonEmpty(qReq.Input.TargetID, j.ShotID, j.AssetID, j.ProjectID)
		kind := qReq.TargetKind
		if !kind.Valid() {
			kind = domainstudio.TargetVideo
		}
		verdict := domainstudio.QCStatus(strings.ToLower(strings.TrimSpace(out.Verdict)))
		if !verdict.Valid() {
			verdict = domainstudio.QCWarn
		}
		issues := make([]domainstudio.QCIssue, len(out.Issues))
		for i, iss := range out.Issues {
			issues[i] = domainstudio.QCIssue{
				Severity: iss.Severity, Category: iss.Category, Message: iss.Message,
				MsgKey: iss.MsgKey, MsgParams: iss.MsgParams, FramePath: iss.FramePath,
			}
		}
		if _, rerr := s.repo.CreateQCReport(ctx, domainstudio.QCReport{
			ProjectID:      j.ProjectID,
			EpisodeID:      j.EpisodeID,
			TargetKind:     kind,
			TargetID:       targetID,
			MediaPath:      firstNonEmpty(qReq.MediaPath, qReq.Input.MediaPath.LocalURL),
			Verdict:        verdict,
			OverallScore:   out.OverallScore,
			FFprobeJSON:    out.FFprobeJSON,
			Issues:         issues,
			VisionSummary:  out.VisionSummary,
			VisionModelRef: qReq.Input.VisionModelRef,
			SampleFrames:   out.SampleFrames,
		}); rerr != nil {
			s.log.WarnContext(ctx, "studio: failed to persist queued qc report", "job_id", j.ID, "error", rerr)
		}
		s.succeedJob(ctx, j, string(verdict))

	default:
		s.failJob(ctx, j, apperr.Validation("unsupported job kind "+string(j.JobKind)))
	}
}

func (s *Service) executePollJob(ctx context.Context, j domainstudio.Job) {
	if s.isJobCanceledOrPaused(ctx, j.ID) {
		return
	}
	if s.videoMedia == nil {
		s.failJob(ctx, j, apperr.Unavailable("video polling driver is unavailable"))
		return
	}
	var tok domainmedia.VideoSubmitToken
	if err := json.Unmarshal([]byte(j.SubmitTokenJSON), &tok); err != nil {
		s.failJob(ctx, j, err)
		return
	}
	runCtx, untrack := s.trackActiveJob(s.withWorkerGuard(ctx), j)
	defer untrack()

	runCtx = domainmedia.ContextWithUsageMeta(runCtx, domainmedia.UsageMeta{
		ProjectID:    j.ProjectID,
		EpisodeID:    j.EpisodeID,
		ShotID:       j.ShotID,
		JobID:        j.ID,
		Operation:    "generate_video",
		EstimatedUSD: j.EstimatedUSD,
	})

	now := time.Now().UnixMilli()
	submittedAt := max(j.StartedAt, j.CreatedAt)
	if s.cfg.QueueMaxPollAge > 0 && submittedAt > 0 && now-submittedAt >= s.cfg.QueueMaxPollAge.Milliseconds() {
		s.clearPollErrors(j.ID)
		s.failJob(ctx, j, apperr.Unavailable("video polling timed out after max poll age"))
		return
	}

	st, err := s.videoMedia.PollVideoJobOnce(runCtx, tok)
	now = time.Now().UnixMilli()
	pollDelay := max(1, s.cfg.QueuePollInterval.Milliseconds())

	if err != nil {
		if s.isJobCanceledOrPaused(ctx, j.ID) || ctx.Err() != nil {
			s.clearPollErrors(j.ID)
			return
		}
		if st.Failed {
			s.clearPollErrors(j.ID)
			s.failJob(ctx, j, err)
			if j.ShotID != "" {
				if sh, serr := s.repo.GetShot(ctx, j.ShotID); serr == nil {
					sh.Status = domainstudio.ShotFailed
					sh.ErrorText = err.Error()
					if _, uerr := s.repo.UpsertShot(ctx, sh); uerr != nil {
						s.log.WarnContext(ctx, "studio: failed to update failed shot status", "shot_id", j.ShotID, "error", uerr)
					}
				}
			}
			return
		}
		errCount := s.recordPollError(j.ID)
		if s.cfg.QueueMaxPollErrors > 0 && errCount >= s.cfg.QueueMaxPollErrors {
			s.clearPollErrors(j.ID)
			s.failJob(ctx, j, apperr.Wrap(apperr.CodeUnavailable, fmt.Sprintf("video polling failed after %d consecutive errors", errCount), err))
			return
		}
		j.Status = domainstudio.JobPolling
		j.ErrorText = err.Error()
		j.NextPollAt = now + pollDelay
		if _, uerr := s.repo.UpdateJob(ctx, j); uerr != nil {
			s.log.WarnContext(ctx, "studio: failed to persist poll retry timestamp", "job_id", j.ID, "error", uerr)
		}
		return
	}
	s.clearPollErrors(j.ID)

	if !st.Done {
		if s.isJobCanceledOrPaused(ctx, j.ID) {
			return
		}
		j.Status = domainstudio.JobPolling
		if st.Progress > j.Progress {
			j.Progress = st.Progress
		}
		j.ErrorText = ""
		j.NextPollAt = now + pollDelay
		updated, uerr := s.repo.UpdateJob(ctx, j)
		if uerr != nil {
			s.log.WarnContext(ctx, "studio: failed to persist poll progress update", "job_id", j.ID, "error", uerr)
		} else {
			dto := jobToDTO(updated)
			s.emit(contracts.EventStudioQueueUpdated, contracts.StudioQueueUpdatedEvent{
				ProjectID: updated.ProjectID, EpisodeID: updated.EpisodeID, Job: &dto, Summary: "Polling video job",
			})
		}
		return
	}

	outPath := st.Result.Path
	if s.isJobCanceledOrPaused(ctx, j.ID) {
		return
	}
	var regID string
	if j.ShotID != "" {
		if sh, serr := s.repo.GetShot(ctx, j.ShotID); serr == nil {
			regID = s.registerMedia(ctx, "video", contracts.NodeTypeVideoGenerate, outPath, tok.OutputDir, st.Result.Duration)
			s.recordSelectedVersion(ctx, j.ProjectID, j.EpisodeID, domainstudio.TargetVideo, sh.ID, outPath, contracts.NodeTypeVideoGenerate, sh.VideoPrompt, j.ModelRef)
			if _, aerr := s.applyMediaPathToTarget(ctx, j.ProjectID, j.EpisodeID, domainstudio.TargetVideo, sh.ID, outPath, regID, "queue_video_done"); aerr != nil {
				s.log.WarnContext(ctx, "studio: failed to apply polled video to shot", "job_id", j.ID, "shot_id", sh.ID, "error", aerr)
			}
		}
	}
	s.succeedJob(ctx, j, outPath)
}

func (s *Service) handleSubmitError(ctx context.Context, j domainstudio.Job, err error) {
	if isPermanentJobError(err) || j.Attempts >= j.MaxAttempts {
		s.failJob(ctx, j, err)
		return
	}
	backoff := s.retryBackoffForAttempt(j.Attempts)
	j.Status = domainstudio.JobQueued
	j.ErrorText = err.Error()
	j.NextPollAt = time.Now().UnixMilli() + backoff.Milliseconds()
	updated, uerr := s.repo.UpdateJob(ctx, j)
	if uerr == nil {
		dto := jobToDTO(updated)
		s.emit(contracts.EventStudioQueueUpdated, contracts.StudioQueueUpdatedEvent{
			ProjectID: updated.ProjectID, EpisodeID: updated.EpisodeID, Job: &dto, Summary: "Retrying queued job",
		})
	} else {
		s.log.WarnContext(ctx, "studio: failed to schedule queued job retry", "job_id", j.ID, "error", uerr)
	}
}

func isPermanentJobError(err error) bool {
	return apperr.IsCode(err, apperr.CodeValidation) ||
		apperr.IsCode(err, apperr.CodeUnauthorized) ||
		apperr.IsCode(err, apperr.CodeNotFound) ||
		apperr.IsCode(err, apperr.CodeBadRequest)
}

func (s *Service) retryBackoffForAttempt(attempts int) time.Duration {
	base := max(time.Millisecond, s.cfg.QueuePollInterval)
	maxB := max(base, s.cfg.QueuePollBackoffMax)
	shift := max(0, min(attempts-1, maxRetryShiftExp))
	backoff := base << shift
	if backoff > maxB || backoff <= 0 {
		return maxB
	}
	return backoff
}

func (s *Service) isJobCanceledOrPaused(ctx context.Context, jobID string) bool {
	if s.repo == nil || strings.TrimSpace(jobID) == "" {
		return false
	}
	cur, err := s.repo.GetJob(ctx, jobID)
	if err != nil {
		return false
	}
	return cur.Status == domainstudio.JobCanceled || cur.Status == domainstudio.JobPaused
}

func resolveShotVoiceConfig(sh domainstudio.Shot, overrideModel, defaultModel, overrideVoice string, voiceBySpeaker map[string]domainstudio.AssetEntry) (modelRef, voiceID string) {
	modelRef = firstNonEmpty(overrideModel, defaultModel)
	voiceID = strings.TrimSpace(overrideVoice)
	if len(sh.Dialogues) == 0 || len(voiceBySpeaker) == 0 {
		return modelRef, voiceID
	}
	for _, d := range sh.Dialogues {
		spk := strings.ToLower(strings.TrimSpace(d.Speaker))
		if spk == "" {
			continue
		}
		if ca, ok := voiceBySpeaker[spk]; ok {
			if voiceID == "" && strings.TrimSpace(ca.VoiceID) != "" {
				voiceID = ca.VoiceID
			}
			if overrideModel == "" && strings.TrimSpace(ca.VoiceProvider) != "" {
				modelRef = ca.VoiceProvider
			}
			if voiceID != "" || modelRef != "" {
				break
			}
		}
	}
	return modelRef, voiceID
}

func (s *Service) succeedJob(ctx context.Context, j domainstudio.Job, outPath string) {
	s.clearPollErrors(j.ID)
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
	if err != nil {
		s.log.ErrorContext(ctx, "studio: failed to mark job succeeded", "job_id", j.ID, "error", err)
		return
	}
	dto := jobToDTO(updated)
	s.emit(contracts.EventStudioQueueUpdated, contracts.StudioQueueUpdatedEvent{
		ProjectID: updated.ProjectID, EpisodeID: updated.EpisodeID, Job: &dto, Summary: "Job completed",
	})
}

func (s *Service) failJob(ctx context.Context, j domainstudio.Job, err error) {
	s.clearPollErrors(j.ID)
	j.Status = domainstudio.JobFailed
	j.ErrorText = err.Error()
	j.FinishedAt = time.Now().UnixMilli()
	updated, uerr := s.repo.UpdateJob(ctx, j)
	if uerr != nil {
		s.log.ErrorContext(ctx, "studio: failed to mark job failed", "job_id", j.ID, "error", uerr)
		return
	}
	dto := jobToDTO(updated)
	s.emit(contracts.EventStudioQueueUpdated, contracts.StudioQueueUpdatedEvent{
		ProjectID: updated.ProjectID, EpisodeID: updated.EpisodeID, Job: &dto, Summary: "Job failed",
	})
}
