package studio

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"app/internal/apperr"
	"app/internal/contracts"
	domainstudio "app/internal/domain/studio"
	"app/internal/progress"
)

// WaitForQueue blocks cleanly using job-completion notifications and configured poll intervals
// until all matching queued/submitted/polling jobs finish (or a bounded timeout expires).
func (s *Service) WaitForQueue(ctx context.Context, p contracts.StudioQueueWaitPayload) (contracts.StudioQueueWaitResult, error) {
	if s.repo == nil {
		return contracts.StudioQueueWaitResult{}, apperr.Unavailable("studio repository unavailable").
			WithMsg("errors.studio.service_unavailable", nil)
	}
	if err := p.Validate(); err != nil {
		return contracts.StudioQueueWaitResult{}, err
	}

	maxWait := s.cfg.QueueWaitMaxDuration
	if maxWait <= 0 {
		maxWait = 10 * time.Minute
	}
	if p.TimeoutSec > 0 {
		reqWait := time.Duration(p.TimeoutSec) * time.Second
		if reqWait < maxWait {
			maxWait = reqWait
		}
	}
	pollInt := s.cfg.QueueWaitPollInterval
	if pollInt <= 0 {
		pollInt = 2 * time.Second
	}

	wantJobIDs := toSet(p.JobIDs)
	collect := func() (contracts.StudioQueueWaitResult, error) {
		jobs, err := s.repo.ListJobs(ctx, domainstudio.JobFilter{
			ProjectID: strings.TrimSpace(p.ProjectID),
			EpisodeID: strings.TrimSpace(p.EpisodeID),
			Limit:     s.cfg.DefaultListLimit,
		})
		if err != nil {
			return contracts.StudioQueueWaitResult{}, err
		}
		var filtered []domainstudio.Job
		for _, j := range jobs {
			if len(wantJobIDs) > 0 && !wantJobIDs[j.ID] {
				continue
			}
			filtered = append(filtered, j)
		}
		var res contracts.StudioQueueWaitResult
		for _, j := range filtered {
			switch j.Status {
			case domainstudio.JobSucceeded:
				res.SucceededCount++
			case domainstudio.JobFailed:
				res.FailedCount++
			case domainstudio.JobCanceled:
				res.CanceledCount++
			case domainstudio.JobPaused:
				res.PausedCount++
			case domainstudio.JobQueued, domainstudio.JobSubmitted, domainstudio.JobPolling:
				res.PendingCount++
			}
		}
		res.Jobs = jobsToDTO(filtered)
		res.AllDone = res.PendingCount == 0 && res.PausedCount == 0
		res.Summary = fmt.Sprintf("%d succeeded, %d failed, %d pending, %d paused, %d canceled",
			res.SucceededCount, res.FailedCount, res.PendingCount, res.PausedCount, res.CanceledCount)
		return res, nil
	}

	waitCtx, cancelWait := context.WithTimeout(ctx, maxWait)
	defer cancelWait()

	ch, unsub := s.subscribeQueueWait()
	defer unsub()

	ticker := time.NewTicker(pollInt)
	defer ticker.Stop()

	for {
		res, err := collect()
		if err != nil {
			return contracts.StudioQueueWaitResult{}, err
		}
		total := len(res.Jobs)
		doneCount := res.SucceededCount + res.FailedCount + res.CanceledCount
		var pct float64
		if total > 0 {
			pct = float64(doneCount) / float64(total) * 100.0
		} else {
			pct = 100.0
		}
		progress.Report(ctx, progress.Update{
			Percent: pct,
			Message: fmt.Sprintf("Waiting for studio queue (%d/%d finished, %d active)…", doneCount, total, res.PendingCount),
			Key:     "studio.progress.queue_wait",
			Params: map[string]string{
				"succeeded": strconv.Itoa(res.SucceededCount),
				"failed":    strconv.Itoa(res.FailedCount),
				"active":    strconv.Itoa(res.PendingCount),
			},
		})
		if res.PendingCount == 0 {
			return res, nil
		}

		select {
		case <-ctx.Done():
			return contracts.StudioQueueWaitResult{}, ctx.Err()
		case <-waitCtx.Done():
			finalRes, ferr := collect()
			if ferr != nil {
				return contracts.StudioQueueWaitResult{}, ferr
			}
			finalRes.TimedOut = finalRes.PendingCount > 0
			return finalRes, nil
		case <-ch:
		case <-ticker.C:
		}
	}
}
