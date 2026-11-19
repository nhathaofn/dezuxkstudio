package studio

import (
	"context"
	"strings"
	"sync"
	"time"

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
