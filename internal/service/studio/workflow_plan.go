package studio

import (
	"context"
	"math"
	"strings"
	"unicode/utf8"

	"app/internal/apperr"
	"app/internal/contracts"
	domainstudio "app/internal/domain/studio"
)

// GetWorkflowPlan projects deterministic episode readiness, missing items, timeline issues,
// and whether script/asset/shot authoring should run via LLM node or agent-direct chunking.
func (s *Service) GetWorkflowPlan(ctx context.Context, episodeID string) (contracts.StudioWorkflowPlanResult, error) {
	if s.repo == nil {
		return contracts.StudioWorkflowPlanResult{}, apperr.Unavailable("studio repository is unavailable").
			WithMsg("errors.studio.service_unavailable", nil)
	}
	ep, err := s.repo.GetEpisode(ctx, strings.TrimSpace(episodeID))
	if err != nil {
		return contracts.StudioWorkflowPlanResult{}, err
	}
	proj, err := s.repo.GetProject(ctx, ep.ProjectID)
	if err != nil {
		return contracts.StudioWorkflowPlanResult{}, err
	}
	assets, err := s.repo.ListAssets(ctx, proj.ID)
	if err != nil {
		return contracts.StudioWorkflowPlanResult{}, err
	}
	shots, err := s.repo.ListShots(ctx, ep.ID)
	if err != nil {
		return contracts.StudioWorkflowPlanResult{}, err
	}
	jobs, err := s.repo.ListJobs(ctx, domainstudio.JobFilter{
		EpisodeID: ep.ID,
		Limit:     s.cfg.DefaultListLimit,
	})
	if err != nil {
		return contracts.StudioWorkflowPlanResult{}, err
	}

	epRoot := s.resolveEpisodeRoot(proj, ep.Code)
	rawScript := s.readGuardedStringFile(ctx, epRoot, ep.RawScriptPath)
	normScript := s.readGuardedNormalizedScriptDomain(ctx, epRoot, ep.ScriptJSONPath)
	var finalVideoPath string
	if meta := s.readEpisodeDiskMeta(ctx, epRoot); meta != nil {
		finalVideoPath = strings.TrimSpace(meta.FinalVideoPath)
	}

	rawChars := utf8.RuneCountInString(strings.TrimSpace(rawScript))
	textReady := strings.TrimSpace(proj.DefaultTextModel) != ""
	if s.textReadiness != nil {
		textReady = s.textReadiness.IsTextProviderReady(ctx) || strings.TrimSpace(proj.DefaultTextModel) != ""
	}
	authoringMode := "llm_node"
	if !textReady || rawChars > s.cfg.LongScriptChunkChars {
		authoringMode = "agent_direct"
	}

	counts := contracts.StudioWorkflowPlanCounts{
		Assets: len(assets),
		Shots:  len(shots),
	}
	if normScript != nil {
		counts.Scenes = len(normScript.Scenes)
		for _, sc := range normScript.Scenes {
			counts.Beats += len(sc.Beats)
		}
	}

	missingAssetIDs := []string{}
	for _, a := range assets {
		if strings.TrimSpace(a.PrimaryPath) != "" {
			counts.AssetsReady++
		} else {
			missingAssetIDs = append(missingAssetIDs, a.ID)
		}
	}

	missingKeyframeShotIDs := []string{}
	missingVideoShotIDs := []string{}
	missingTTSShotIDs := []string{}
	for _, sh := range shots {
		if strings.TrimSpace(sh.KeyframePath) != "" {
			counts.KeyframesReady++
		} else {
			missingKeyframeShotIDs = append(missingKeyframeShotIDs, sh.ID)
		}
		if strings.TrimSpace(sh.VideoPath) != "" {
			counts.VideosReady++
		} else {
			missingVideoShotIDs = append(missingVideoShotIDs, sh.ID)
		}
		if shotSpeechText(sh) != "" {
			if strings.TrimSpace(sh.AudioPath) != "" {
				counts.TTSReady++
			} else {
				missingTTSShotIDs = append(missingTTSShotIDs, sh.ID)
			}
		}
	}

	failedJobIDs := []string{}
	for _, j := range jobs {
		switch j.Status {
		case domainstudio.JobQueued, domainstudio.JobSubmitted, domainstudio.JobPolling:
			counts.QueueActive++
		case domainstudio.JobFailed:
			counts.QueueFailed++
			failedJobIDs = append(failedJobIDs, j.ID)
		}
	}

	var timelineIssues []contracts.TimelineAssembleIssue
	var hasTimelineClips bool
	if tlRec, tlErr := s.loadEpisodeTimelineRecord(ctx, proj, ep); tlErr == nil {
		for _, tr := range tlRec.Timeline.Tracks {
			if len(tr.Clips) > 0 {
				hasTimelineClips = true
				break
			}
		}
		tolMs := int64(math.Round(s.cfg.AudioOverflowToleranceSec * 1000))
		domIssues := domainstudio.DetectTimelineSpeechIssues(tlRec.Timeline, nil, tolMs)
		timelineIssues = make([]contracts.TimelineAssembleIssue, 0, len(domIssues))
		for _, iss := range domIssues {
			timelineIssues = append(timelineIssues, contracts.TimelineAssembleIssue{
				Code:       iss.Code,
				ShotID:     iss.ShotID,
				ClipID:     iss.ClipID,
				TrackID:    iss.TrackID,
				OverflowMs: iss.OverflowMs,
				OverlapMs:  iss.OverlapMs,
				Message:    iss.Message,
			})
		}
	} else {
		timelineIssues = []contracts.TimelineAssembleIssue{}
	}
	if timelineIssues == nil {
		timelineIssues = []contracts.TimelineAssembleIssue{}
	}

	next := computeWorkflowNextAction(
		ep, proj, rawChars, normScript, authoringMode, textReady, s.cfg.LongScriptChunkChars,
		counts, missingAssetIDs, missingKeyframeShotIDs, missingVideoShotIDs, missingTTSShotIDs,
		failedJobIDs, timelineIssues, hasTimelineClips, finalVideoPath,
	)

	return contracts.StudioWorkflowPlanResult{
		ProjectID:                proj.ID,
		EpisodeID:                ep.ID,
		Stage:                    ep.Stage,
		RawScriptChars:           rawChars,
		LongScriptChunkChars:     s.cfg.LongScriptChunkChars,
		TextProviderReady:        textReady,
		RecommendedAuthoringMode: authoringMode,
		Counts:                   counts,
		MissingAssetIDs:          missingAssetIDs,
		MissingKeyframeShotIDs:   missingKeyframeShotIDs,
		MissingVideoShotIDs:      missingVideoShotIDs,
		MissingTTSShotIDs:        missingTTSShotIDs,
		FailedJobIDs:             failedJobIDs,
		TimelineIssues:           timelineIssues,
		NextAction:               next,
	}, nil
}

func computeWorkflowNextAction(
	ep domainstudio.Episode,
	proj domainstudio.Project,
	rawChars int,
	normScript *domainstudio.NormalizedScript,
	authoringMode string,
	textReady bool,
	longChunkChars int,
	counts contracts.StudioWorkflowPlanCounts,
	missingAssetIDs, missingKeyframeShotIDs, missingVideoShotIDs, missingTTSShotIDs, failedJobIDs []string,
	timelineIssues []contracts.TimelineAssembleIssue,
	hasTimelineClips bool,
	finalVideoPath string,
) contracts.StudioWorkflowNextAction {
	if rawChars == 0 && normScript == nil && counts.Shots == 0 {
		return contracts.StudioWorkflowNextAction{
			Action:        "save_script",
			Reason:        "Episode has no raw or normalized script yet.",
			SuggestedTool: "studio_save_script",
			SuggestedArgs: map[string]any{"episode_id": ep.ID},
		}
	}
	if normScript == nil && counts.Shots == 0 {
		if authoringMode == "agent_direct" {
			reason := "No PurposeText LLM provider is configured; author normalized_script directly."
			if textReady && rawChars > longChunkChars {
				reason = "Raw script exceeds long_script_chunk_chars; chunk and author directly with script-analyst."
			}
			return contracts.StudioWorkflowNextAction{
				Action:        "author_direct",
				Reason:        reason,
				SuggestedTool: "studio_save_script",
				SuggestedArgs: map[string]any{"episode_id": ep.ID},
			}
		}
		return contracts.StudioWorkflowNextAction{
			Action:        "script_normalize",
			Reason:        "Raw screenplay is ready for LLM normalization.",
			SuggestedTool: "studio_episode_run_stage",
			SuggestedArgs: map[string]any{"episode_id": ep.ID, "stage": "script_normalize"},
		}
	}
	if counts.Assets == 0 && normScript != nil {
		if authoringMode == "agent_direct" {
			return contracts.StudioWorkflowNextAction{
				Action:        "extract_assets_direct",
				Reason:        "Author Asset Bible entries directly via studio_asset_upsert.",
				SuggestedTool: "studio_asset_upsert",
				SuggestedArgs: map[string]any{"project_id": proj.ID},
			}
		}
		return contracts.StudioWorkflowNextAction{
			Action:        "extract_assets",
			Reason:        "Normalized script is ready for asset bible extraction.",
			SuggestedTool: "studio_episode_run_stage",
			SuggestedArgs: map[string]any{"episode_id": ep.ID, "stage": "extract_assets"},
		}
	}
	if counts.Shots == 0 {
		if authoringMode == "agent_direct" {
			return contracts.StudioWorkflowNextAction{
				Action:        "author_shots_direct",
				Reason:        "Author storyboard shots directly in batches via studio_shots_batch_upsert.",
				SuggestedTool: "studio_shots_batch_upsert",
				SuggestedArgs: map[string]any{"episode_id": ep.ID},
			}
		}
		return contracts.StudioWorkflowNextAction{
			Action:        "author_shots",
			Reason:        "Normalized script and assets are ready for storyboard shot authoring.",
			SuggestedTool: "studio_episode_run_stage",
			SuggestedArgs: map[string]any{"episode_id": ep.ID, "stage": "author_shots"},
		}
	}
	if counts.QueueActive > 0 {
		return contracts.StudioWorkflowNextAction{
			Action:        "queue_wait",
			Reason:        "Background durable queue jobs are still active for this episode.",
			SuggestedTool: "studio_queue_wait",
			SuggestedArgs: map[string]any{"episode_id": ep.ID},
		}
	}
	if len(missingAssetIDs) > 0 {
		return contracts.StudioWorkflowNextAction{
			Action:        "generate_assets",
			Reason:        "Asset Bible entries are missing reference images.",
			SuggestedTool: "studio_queue_enqueue",
			SuggestedArgs: map[string]any{"episode_id": ep.ID, "stage": "generate_assets", "asset_ids": missingAssetIDs},
		}
	}
	if proj.ProductionMode != domainstudio.ModeR2V && len(missingKeyframeShotIDs) > 0 {
		return contracts.StudioWorkflowNextAction{
			Action:        "generate_keyframes",
			Reason:        "Storyboard shots are missing start keyframes.",
			SuggestedTool: "studio_queue_enqueue",
			SuggestedArgs: map[string]any{"episode_id": ep.ID, "stage": "generate_keyframes", "shot_ids": missingKeyframeShotIDs},
		}
	}
	if len(missingVideoShotIDs) > 0 {
		return contracts.StudioWorkflowNextAction{
			Action:        "generate_videos",
			Reason:        "Storyboard shots are missing rendered video clips.",
			SuggestedTool: "studio_queue_enqueue",
			SuggestedArgs: map[string]any{"episode_id": ep.ID, "stage": "generate_videos", "shot_ids": missingVideoShotIDs},
		}
	}
	if len(missingTTSShotIDs) > 0 {
		return contracts.StudioWorkflowNextAction{
			Action:        "generate_tts",
			Reason:        "Shots with narration or dialogue are missing TTS audio.",
			SuggestedTool: "studio_queue_enqueue",
			SuggestedArgs: map[string]any{"episode_id": ep.ID, "stage": "generate_tts", "shot_ids": missingTTSShotIDs},
		}
	}
	if len(failedJobIDs) > 0 {
		return contracts.StudioWorkflowNextAction{
			Action:        "review_failed_jobs",
			Reason:        "One or more queue jobs failed and need inspection or retry.",
			SuggestedTool: "studio_queue_list",
			SuggestedArgs: map[string]any{"episode_id": ep.ID, "status": "failed"},
		}
	}
	if !hasTimelineClips {
		return contracts.StudioWorkflowNextAction{
			Action:        "inspect_and_assemble",
			Reason:        "Shot media is ready; inspect contact sheets and auto-assemble the timeline.",
			SuggestedTool: "studio_inspect_shots",
			SuggestedArgs: map[string]any{"episode_id": ep.ID},
		}
	}
	if len(timelineIssues) > 0 {
		return contracts.StudioWorkflowNextAction{
			Action:        "fix_timeline_issues",
			Reason:        "Timeline has narration overrun or speech collision issues to resolve.",
			SuggestedTool: "studio_timeline_apply_ops",
			SuggestedArgs: map[string]any{"episode_id": ep.ID},
		}
	}
	if finalVideoPath == "" {
		return contracts.StudioWorkflowNextAction{
			Action:        "render_timeline",
			Reason:        "Timeline is assembled and ready for final MP4 render.",
			SuggestedTool: "studio_timeline_render",
			SuggestedArgs: map[string]any{"episode_id": ep.ID},
		}
	}
	return contracts.StudioWorkflowNextAction{
		Action: "complete",
		Reason: "Episode production and final timeline render are complete.",
	}
}
