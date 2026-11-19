package contracts

import (
	"strings"

	"app/internal/apperr"
	domainstudio "app/internal/domain/studio"
)

// Action names for Studio Tiers 1, 2, and 3.
const (
	ActionStudioWorkflowPlan     = "studio:workflow_plan"
	ActionStudioShotsBatchUpsert = "studio:shots_batch_upsert"
	ActionStudioAssetRename      = "studio:asset_rename"
	ActionStudioAssetMerge       = "studio:asset_merge"
	ActionStudioInspectShots     = "studio:inspect_shots"
	ActionStudioQueueWait        = "studio:queue_wait"
)

// StudioWorkflowPlanPayload queries deterministic workflow state for an episode.
type StudioWorkflowPlanPayload struct {
	EpisodeID string `json:"episode_id"`
}

// Validate checks StudioWorkflowPlanPayload.
func (p StudioWorkflowPlanPayload) Validate() error {
	if strings.TrimSpace(p.EpisodeID) == "" {
		return apperr.Validation("episode_id is required").WithMsg("errors.studio.episode_id_required", nil)
	}
	return nil
}

// StudioWorkflowPlanCounts summarizes episode production progress counts.
type StudioWorkflowPlanCounts struct {
	Scenes         int `json:"scenes"`
	Beats          int `json:"beats"`
	Assets         int `json:"assets"`
	AssetsReady    int `json:"assets_ready"`
	Shots          int `json:"shots"`
	KeyframesReady int `json:"keyframes_ready"`
	VideosReady    int `json:"videos_ready"`
	TTSReady       int `json:"tts_ready"`
	QueueActive    int `json:"queue_active"`
	QueueFailed    int `json:"queue_failed"`
}

// StudioWorkflowNextAction recommends the deterministic next production step.
type StudioWorkflowNextAction struct {
	Action        string         `json:"action"`
	Reason        string         `json:"reason"`
	SuggestedTool string         `json:"suggested_tool,omitempty"`
	SuggestedArgs map[string]any `json:"suggested_args,omitempty"`
}

// StudioWorkflowPlanResult is returned by studio:workflow_plan.
type StudioWorkflowPlanResult struct {
	ProjectID                string                    `json:"project_id"`
	EpisodeID                string                    `json:"episode_id"`
	Stage                    domainstudio.EpisodeStage `json:"stage"`
	RawScriptChars           int                       `json:"raw_script_chars"`
	LongScriptChunkChars     int                       `json:"long_script_chunk_chars"`
	TextProviderReady        bool                      `json:"text_provider_ready"`
	RecommendedAuthoringMode string                    `json:"recommended_authoring_mode"` // llm_node | agent_direct
	Counts                   StudioWorkflowPlanCounts  `json:"counts"`
	MissingAssetIDs          []string                  `json:"missing_asset_ids"`
	MissingKeyframeShotIDs   []string                  `json:"missing_keyframe_shot_ids"`
	MissingVideoShotIDs      []string                  `json:"missing_video_shot_ids"`
	MissingTTSShotIDs        []string                  `json:"missing_tts_shot_ids"`
	FailedJobIDs             []string                  `json:"failed_job_ids"`
	TimelineIssues           []TimelineAssembleIssue   `json:"timeline_issues"`
	NextAction               StudioWorkflowNextAction  `json:"next_action"`
}

// StudioShotBatchItem defines one shot item in StudioShotsBatchUpsertPayload.
type StudioShotBatchItem struct {
	ID              string                   `json:"id,omitempty"`
	Seq             *int                     `json:"seq,omitempty"`
	SceneCode       string                   `json:"scene_code,omitempty"`
	Title           string                   `json:"title,omitempty"`
	ShotMode        domainstudio.ShotMode    `json:"shot_mode,omitempty"`
	DurationSec     *int                     `json:"duration_sec,omitempty"`
	ImagePrompt     *string                  `json:"image_prompt,omitempty"`
	VideoPrompt     *string                  `json:"video_prompt,omitempty"`
	NarrationText   *string                  `json:"narration_text,omitempty"`
	Dialogues       []StudioDialogueLine     `json:"dialogues,omitempty"`
	RefAssetTags    []string                 `json:"ref_asset_tags,omitempty"`
	KeyframeAssetID *string                  `json:"keyframe_asset_id,omitempty"`
	KeyframePath    *string                  `json:"keyframe_path,omitempty"`
	VideoAssetID    *string                  `json:"video_asset_id,omitempty"`
	VideoPath       *string                  `json:"video_path,omitempty"`
	AudioAssetID    *string                  `json:"audio_asset_id,omitempty"`
	AudioPath       *string                  `json:"audio_path,omitempty"`
	Status          *domainstudio.ShotStatus `json:"status,omitempty"`
	ErrorText       *string                  `json:"error_text,omitempty"`
}

// StudioShotsBatchUpsertPayload creates, patches, or replaces multiple storyboard shots in one atomic operation.
type StudioShotsBatchUpsertPayload struct {
	EpisodeID  string                `json:"episode_id"`
	ReplaceAll bool                  `json:"replace_all,omitempty"`
	Shots      []StudioShotBatchItem `json:"shots"`
}

// Validate checks StudioShotsBatchUpsertPayload.
func (p StudioShotsBatchUpsertPayload) Validate() error {
	if strings.TrimSpace(p.EpisodeID) == "" {
		return apperr.Validation("episode_id is required").WithMsg("errors.studio.episode_id_required", nil)
	}
	if !p.ReplaceAll && len(p.Shots) == 0 {
		return apperr.Validation("shots must not be empty when replace_all is false").WithMsg("errors.studio.shots_required", nil)
	}
	for _, sh := range p.Shots {
		if sh.Seq != nil && *sh.Seq < 0 {
			return apperr.Validation("shot seq must be >= 0").WithMsg("errors.studio.shot_seq_invalid", nil)
		}
		if sh.DurationSec != nil && *sh.DurationSec < domainstudio.MinShotDurationSec {
			return apperr.Validation("shot duration_sec must be at least 1 second").
				WithMsg("errors.studio.shot_duration_invalid", map[string]any{"min": domainstudio.MinShotDurationSec})
		}
		if sh.ShotMode != "" {
			if err := domainstudio.ValidateShotMode(sh.ShotMode); err != nil {
				return err
			}
		}
		if sh.Status != nil {
			if err := domainstudio.ValidateShotStatus(*sh.Status); err != nil {
				return err
			}
		}
	}
	return nil
}

// StudioAssetRenamePayload renames an asset and cascades @Tag / speaker references across scripts and shots.
type StudioAssetRenamePayload struct {
	ProjectID  string `json:"project_id"`
	AssetID    string `json:"asset_id"`
	NewName    string `json:"new_name,omitempty"`
	NewCodeTag string `json:"new_code_tag"`
	DryRun     bool   `json:"dry_run,omitempty"`
}

// Validate checks StudioAssetRenamePayload.
func (p StudioAssetRenamePayload) Validate() error {
	if strings.TrimSpace(p.ProjectID) == "" {
		return apperr.Validation("project_id is required")
	}
	if strings.TrimSpace(p.AssetID) == "" {
		return apperr.Validation("asset_id is required")
	}
	if domainstudio.NormalizeCodeTag(p.NewCodeTag, p.NewName) == "" {
		return apperr.Validation("new_code_tag or new_name is required")
	}
	return nil
}

// StudioAssetMergePayload merges source_asset_id into target_asset_id and cascades @Tag references.
type StudioAssetMergePayload struct {
	ProjectID     string `json:"project_id"`
	SourceAssetID string `json:"source_asset_id"`
	TargetAssetID string `json:"target_asset_id"`
	DryRun        bool   `json:"dry_run,omitempty"`
}

// Validate checks StudioAssetMergePayload.
func (p StudioAssetMergePayload) Validate() error {
	if strings.TrimSpace(p.ProjectID) == "" {
		return apperr.Validation("project_id is required")
	}
	if strings.TrimSpace(p.SourceAssetID) == "" || strings.TrimSpace(p.TargetAssetID) == "" {
		return apperr.Validation("source_asset_id and target_asset_id are required")
	}
	if strings.TrimSpace(p.SourceAssetID) == strings.TrimSpace(p.TargetAssetID) {
		return apperr.Validation("source_asset_id and target_asset_id must be different")
	}
	return nil
}

// StudioAssetRefactorResult describes the dry-run impact or committed refactor counts for rename/merge.
type StudioAssetRefactorResult struct {
	DryRun               bool          `json:"dry_run"`
	OldCodeTag           string        `json:"old_code_tag"`
	NewCodeTag           string        `json:"new_code_tag"`
	ImpactedEpisodeCount int           `json:"impacted_episode_count"`
	ImpactedShotCount    int           `json:"impacted_shot_count"`
	UpdatedScriptsCount  int           `json:"updated_scripts_count"`
	UpdatedShotsCount    int           `json:"updated_shots_count"`
	DeletedAssetID       string        `json:"deleted_asset_id,omitempty"`
	Assets               []StudioAsset `json:"assets,omitempty"`
}

// StudioInspectShotsPayload generates visual contact sheets and BLACK/FREEZE/CUT signals for episode shots.
type StudioInspectShotsPayload struct {
	EpisodeID      string   `json:"episode_id"`
	ShotIDs        []string `json:"shot_ids,omitempty"`
	MaxFrames      int      `json:"max_frames,omitempty"`
	FramesPerSheet int      `json:"frames_per_sheet,omitempty"`
	IncludeSignals *bool    `json:"include_signals,omitempty"`
}

// Validate checks StudioInspectShotsPayload.
func (p StudioInspectShotsPayload) Validate() error {
	if strings.TrimSpace(p.EpisodeID) == "" {
		return apperr.Validation("episode_id is required").WithMsg("errors.studio.episode_id_required", nil)
	}
	if p.MaxFrames < 0 {
		return apperr.Validation("max_frames must be >= 0")
	}
	if p.FramesPerSheet < 0 {
		return apperr.Validation("frames_per_sheet must be >= 0")
	}
	return nil
}

// StudioShotInspectionItem holds the generated contact sheets and signal metadata for one shot.
type StudioShotInspectionItem struct {
	ShotID        string                  `json:"shot_id"`
	Seq           int                     `json:"seq"`
	Title         string                  `json:"title"`
	VideoPath     string                  `json:"video_path"`
	SheetPaths    []string                `json:"sheet_paths"`
	SheetAssetIDs []string                `json:"sheet_asset_ids,omitempty"`
	Signals       VideoSignals            `json:"signals"`
	Frames        []ContactSheetFrameMeta `json:"frames"`
	Summary       string                  `json:"summary"`
	Error         string                  `json:"error,omitempty"`
}

// StudioInspectShotsResult is returned by studio:inspect_shots.
type StudioInspectShotsResult struct {
	EpisodeID string                     `json:"episode_id"`
	Shots     []StudioShotInspectionItem `json:"shots"`
	Summary   string                     `json:"summary"`
}

// StudioQueueWaitPayload waits for queued/active durable studio jobs to reach terminal states.
type StudioQueueWaitPayload struct {
	ProjectID  string   `json:"project_id,omitempty"`
	EpisodeID  string   `json:"episode_id,omitempty"`
	JobIDs     []string `json:"job_ids,omitempty"`
	TimeoutSec int      `json:"timeout_sec,omitempty"`
}

// Validate checks StudioQueueWaitPayload.
func (p StudioQueueWaitPayload) Validate() error {
	if strings.TrimSpace(p.ProjectID) == "" && strings.TrimSpace(p.EpisodeID) == "" && len(p.JobIDs) == 0 {
		return apperr.Validation("project_id, episode_id, or job_ids is required")
	}
	if p.TimeoutSec < 0 {
		return apperr.Validation("timeout_sec must be >= 0")
	}
	return nil
}

// StudioQueueWaitResult summarizes terminal and remaining queue jobs when studio:queue_wait returns.
type StudioQueueWaitResult struct {
	AllDone        bool        `json:"all_done"`
	TimedOut       bool        `json:"timed_out"`
	SucceededCount int         `json:"succeeded_count"`
	FailedCount    int         `json:"failed_count"`
	CanceledCount  int         `json:"canceled_count"`
	PendingCount   int         `json:"pending_count"`
	PausedCount    int         `json:"paused_count"`
	Jobs           []StudioJob `json:"jobs"`
	Summary        string      `json:"summary"`
}
