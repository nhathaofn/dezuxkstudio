package studio

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"app/internal/apperr"
	"app/internal/contracts"
	domainmedia "app/internal/domain/mediagen"
	domainstudio "app/internal/domain/studio"
)

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
	s.notifyQueueWaiters()
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
	s.notifyQueueWaiters()
}
