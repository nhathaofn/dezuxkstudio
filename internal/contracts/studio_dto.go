package contracts

import (
	"fmt"
	"slices"
	"strings"

	"app/internal/apperr"
	domainmedia "app/internal/domain/mediagen"
	domainstudio "app/internal/domain/studio"
)

// Action names for AI Film Studio (Phase 1 MVP).
const (
	ActionStudioProjectList          = "studio:project_list"
	ActionStudioProjectCreate        = "studio:project_create"
	ActionStudioProjectGet           = "studio:project_get"
	ActionStudioProjectUpdate        = "studio:project_update"
	ActionStudioProjectDelete        = "studio:project_delete"
	ActionStudioEpisodeCreate        = "studio:episode_create"
	ActionStudioEpisodeGet           = "studio:episode_get"
	ActionStudioEpisodeSaveScript    = "studio:episode_save_script"
	ActionStudioAssetUpsert          = "studio:asset_upsert"
	ActionStudioShotUpsert           = "studio:shot_upsert"
	ActionStudioShotReorder          = "studio:shot_reorder"
	ActionStudioEpisodeRunStage      = "studio:episode_run_stage"
	ActionStudioQueueEnqueue         = "studio:queue_enqueue"
	ActionStudioQueueList            = "studio:queue_list"
	ActionStudioQueueControl         = "studio:queue_control"
	ActionStudioCostSummary          = "studio:cost_summary"
	ActionStudioPricingUpsert        = "studio:pricing_upsert"
	ActionStudioVersionsList         = "studio:versions_list"
	ActionStudioVersionSelect        = "studio:version_select"
	ActionStudioGridSplit            = "studio:grid_split"
	ActionStudioMaskEdit             = "studio:mask_edit"
	ActionStudioExtractLastFrame     = "studio:extract_last_frame"
	ActionStudioQCReview             = "studio:qc_review"
	ActionStudioBatchPreflight       = "studio:batch_preflight"
	ActionStudioTimelineGet          = "studio:timeline_get"
	ActionStudioTimelineApplyOps     = "studio:timeline_apply_ops"
	ActionStudioTimelineUndo         = "studio:timeline_undo"
	ActionStudioTimelineRedo         = "studio:timeline_redo"
	ActionStudioTimelineAutoAssemble = "studio:timeline_auto_assemble"
	ActionStudioMediaPeaksThumbs     = "studio:media_peaks_thumbs"
	ActionStudioTimelineRender       = "studio:timeline_render"
	ActionStudioTimelineExportNLE    = "studio:timeline_export_nle"
	ActionStudioOpenExportFolder     = "studio:open_export_folder"
)

// Server-pushed event names for AI Film Studio.
const (
	EventStudioProjectChanged  = "studio:project_changed"
	EventStudioEpisodeUpdated  = "studio:episode_updated"
	EventStudioBatchProgress   = "studio:batch_progress"
	EventStudioQueueUpdated    = "studio:queue_updated"
	EventStudioCostUpdated     = "studio:cost_updated"
	EventStudioTimelineChanged = "studio:timeline_changed"
)

// StudioRunStageAction enumerates the executable stage/step operations accepted by studio:episode_run_stage.
type StudioRunStageAction string

const (
	StudioStageScriptNormalize   StudioRunStageAction = "script_normalize"
	StudioStageExtractAssets     StudioRunStageAction = "extract_assets"
	StudioStageAuthorShots       StudioRunStageAction = "author_shots"
	StudioStageGenerateAssets    StudioRunStageAction = "generate_assets"
	StudioStageGenerateKeyframes StudioRunStageAction = "generate_keyframes"
	StudioStageGenerateVideos    StudioRunStageAction = "generate_videos"
	StudioStageGenerateTTS       StudioRunStageAction = "generate_tts"
	StudioStageProbeShots        StudioRunStageAction = "probe_shots"
	StudioStageConcatVideo       StudioRunStageAction = "concat_video"
)

// StudioRunStageActions returns every valid stage action for studio:episode_run_stage.
func StudioRunStageActions() []StudioRunStageAction {
	return []StudioRunStageAction{
		StudioStageScriptNormalize,
		StudioStageExtractAssets,
		StudioStageAuthorShots,
		StudioStageGenerateAssets,
		StudioStageGenerateKeyframes,
		StudioStageGenerateVideos,
		StudioStageGenerateTTS,
		StudioStageProbeShots,
		StudioStageConcatVideo,
	}
}

// StudioProject is the transport DTO for a film studio project.
type StudioProject struct {
	ID                 string                      `json:"id"`
	Slug               string                      `json:"slug"`
	Title              string                      `json:"title"`
	Genre              domainstudio.Genre          `json:"genre"`
	ProductionMode     domainstudio.ProductionMode `json:"production_mode"`
	AspectRatio        string                      `json:"aspect_ratio"`
	StylePrompt        string                      `json:"style_prompt"`
	RootDir            string                      `json:"root_dir"`
	AutoApproveBatches bool                        `json:"auto_approve_batches"`
	DefaultTextModel   string                      `json:"default_text_model"`
	DefaultImageModel  string                      `json:"default_image_model"`
	DefaultVideoModel  string                      `json:"default_video_model"`
	DefaultTTSModel    string                      `json:"default_tts_model"`
	CreatedAt          int64                       `json:"created_at"`
	UpdatedAt          int64                       `json:"updated_at"`
}

// StudioEpisode is the transport DTO for one project episode.
type StudioEpisode struct {
	ID               string                    `json:"id"`
	ProjectID        string                    `json:"project_id"`
	Code             string                    `json:"code"`
	Title            string                    `json:"title"`
	Stage            domainstudio.EpisodeStage `json:"stage"`
	RawScriptPath    string                    `json:"raw_script_path"`
	ScriptJSONPath   string                    `json:"script_json_path"`
	AssetsJSONPath   string                    `json:"assets_json_path"`
	ShotsJSONPath    string                    `json:"shots_json_path"`
	TimelineJSONPath string                    `json:"timeline_json_path"`
	CreatedAt        int64                     `json:"created_at"`
	UpdatedAt        int64                     `json:"updated_at"`
}

// StudioAsset is the transport DTO for one asset bible entry.
type StudioAsset struct {
	ID             string                 `json:"id"`
	ProjectID      string                 `json:"project_id"`
	Kind           domainstudio.AssetKind `json:"kind"`
	Name           string                 `json:"name"`
	CodeTag        string                 `json:"code_tag"`
	Description    string                 `json:"description"`
	Prompt         string                 `json:"prompt"`
	PrimaryAssetID string                 `json:"primary_asset_id"`
	PrimaryPath    string                 `json:"primary_path"`
	VoiceProvider  string                 `json:"voice_provider,omitempty"`
	VoiceID        string                 `json:"voice_id,omitempty"`
	VoiceStyle     string                 `json:"voice_style,omitempty"`
	UpdatedAt      int64                  `json:"updated_at"`
}

// StudioDialogueLine is a spoken character line inside a scene/shot.
type StudioDialogueLine struct {
	Speaker string `json:"speaker"`
	Line    string `json:"line"`
	Emotion string `json:"emotion,omitempty"`
	Cue     string `json:"cue,omitempty"`
}

// StudioNarrationSegment is a voiceover segment inside a script beat.
type StudioNarrationSegment struct {
	Text         string  `json:"text"`
	Tone         string  `json:"tone,omitempty"`
	EstimatedSec float64 `json:"estimated_sec,omitempty"`
}

// StudioScriptBeat is one structured beat inside a script scene.
type StudioScriptBeat struct {
	Code        string                   `json:"code"`
	Description string                   `json:"description"`
	VisualHint  string                   `json:"visual_hint,omitempty"`
	CameraHint  string                   `json:"camera_hint,omitempty"`
	DurationSec int                      `json:"duration_sec"`
	AssetTags   []string                 `json:"asset_tags,omitempty"`
	Dialogues   []StudioDialogueLine     `json:"dialogues,omitempty"`
	Narrations  []StudioNarrationSegment `json:"narrations,omitempty"`
}

// StudioScriptScene is one scene in a normalized screenplay.
type StudioScriptScene struct {
	Code      string             `json:"code"`
	Title     string             `json:"title"`
	Location  string             `json:"location,omitempty"`
	TimeOfDay string             `json:"time_of_day,omitempty"`
	Summary   string             `json:"summary,omitempty"`
	Beats     []StudioScriptBeat `json:"beats"`
}

// StudioNormalizedScript is the structured screenplay DTO returned to the UI.
type StudioNormalizedScript struct {
	Title       string              `json:"title"`
	Genre       domainstudio.Genre  `json:"genre"`
	AspectRatio string              `json:"aspect_ratio"`
	Logline     string              `json:"logline"`
	Language    string              `json:"language,omitempty"`
	Characters  []string            `json:"characters,omitempty"`
	Scenes      []StudioScriptScene `json:"scenes"`
}

// StudioShot is the transport DTO for one storyboard shot.
type StudioShot struct {
	ID              string                  `json:"id"`
	EpisodeID       string                  `json:"episode_id"`
	Seq             int                     `json:"seq"`
	SceneCode       string                  `json:"scene_code"`
	Title           string                  `json:"title"`
	ShotMode        domainstudio.ShotMode   `json:"shot_mode"`
	DurationSec     int                     `json:"duration_sec"`
	ImagePrompt     string                  `json:"image_prompt"`
	VideoPrompt     string                  `json:"video_prompt"`
	NarrationText   string                  `json:"narration_text"`
	Dialogues       []StudioDialogueLine    `json:"dialogues,omitempty"`
	RefAssetTags    []string                `json:"ref_asset_tags,omitempty"`
	KeyframeAssetID string                  `json:"keyframe_asset_id"`
	KeyframePath    string                  `json:"keyframe_path"`
	VideoAssetID    string                  `json:"video_asset_id"`
	VideoPath       string                  `json:"video_path"`
	AudioAssetID    string                  `json:"audio_asset_id"`
	AudioPath       string                  `json:"audio_path"`
	Status          domainstudio.ShotStatus `json:"status"`
	ErrorText       string                  `json:"error_text"`
	UpdatedAt       int64                   `json:"updated_at"`
}

// StudioProjectListResult returns all studio projects and the active projects directory.
type StudioProjectListResult struct {
	Projects    []StudioProject `json:"projects"`
	ProjectsDir string          `json:"projects_dir"`
}

// StudioIDPayload identifies a project or episode by ID (or project slug).
type StudioIDPayload struct {
	ID string `json:"id"`
}

// Validate checks StudioIDPayload.
func (p StudioIDPayload) Validate() error {
	if strings.TrimSpace(p.ID) == "" {
		return apperr.Validation("id is required")
	}
	return nil
}

// StudioProjectCreatePayload creates a new film studio project and optional starter episode.
type StudioProjectCreatePayload struct {
	Title               string                      `json:"title"`
	Slug                string                      `json:"slug,omitempty"`
	Genre               domainstudio.Genre          `json:"genre,omitempty"`
	ProductionMode      domainstudio.ProductionMode `json:"production_mode,omitempty"`
	AspectRatio         string                      `json:"aspect_ratio,omitempty"`
	StylePrompt         string                      `json:"style_prompt,omitempty"`
	AutoApproveBatches  bool                        `json:"auto_approve_batches,omitempty"`
	DefaultTextModel    string                      `json:"default_text_model,omitempty"`
	DefaultImageModel   string                      `json:"default_image_model,omitempty"`
	DefaultVideoModel   string                      `json:"default_video_model,omitempty"`
	DefaultTTSModel     string                      `json:"default_tts_model,omitempty"`
	InitialEpisodeTitle string                      `json:"initial_episode_title,omitempty"`
}

// Validate checks StudioProjectCreatePayload invariants.
func (p *StudioProjectCreatePayload) Validate() error {
	p.Title = strings.TrimSpace(p.Title)
	if p.Title == "" {
		return apperr.Validation("project title is required").WithMsg("errors.studio.project_title_required", nil)
	}
	if p.Genre == "" {
		p.Genre = domainstudio.GenreDrama
	}
	if err := domainstudio.ValidateGenre(p.Genre); err != nil {
		return err
	}
	if p.ProductionMode == "" {
		p.ProductionMode = domainstudio.ModeStoryboardI2V
	}
	if err := domainstudio.ValidateProductionMode(p.ProductionMode); err != nil {
		return err
	}
	if strings.TrimSpace(p.AspectRatio) == "" {
		p.AspectRatio = domainmedia.DefaultVideoAspectRatio
	}
	if !slices.Contains(domainmedia.VideoAspectRatios(), p.AspectRatio) {
		return apperr.Validation(fmt.Sprintf("unsupported aspect ratio %q", p.AspectRatio)).
			WithMsg("errors.mediagen.invalid_aspect_ratio", map[string]any{"value": p.AspectRatio})
	}
	return nil
}

// StudioProjectUpdatePayload patches mutable project settings.
type StudioProjectUpdatePayload struct {
	ID                 string                       `json:"id"`
	Title              *string                      `json:"title,omitempty"`
	Genre              *domainstudio.Genre          `json:"genre,omitempty"`
	ProductionMode     *domainstudio.ProductionMode `json:"production_mode,omitempty"`
	AspectRatio        *string                      `json:"aspect_ratio,omitempty"`
	StylePrompt        *string                      `json:"style_prompt,omitempty"`
	AutoApproveBatches *bool                        `json:"auto_approve_batches,omitempty"`
	DefaultTextModel   *string                      `json:"default_text_model,omitempty"`
	DefaultImageModel  *string                      `json:"default_image_model,omitempty"`
	DefaultVideoModel  *string                      `json:"default_video_model,omitempty"`
	DefaultTTSModel    *string                      `json:"default_tts_model,omitempty"`
}

// Validate checks StudioProjectUpdatePayload.
func (p StudioProjectUpdatePayload) Validate() error {
	if strings.TrimSpace(p.ID) == "" {
		return apperr.Validation("project id is required")
	}
	if p.Title != nil && strings.TrimSpace(*p.Title) == "" {
		return apperr.Validation("project title cannot be empty").WithMsg("errors.studio.project_title_required", nil)
	}
	if p.Genre != nil {
		if err := domainstudio.ValidateGenre(*p.Genre); err != nil {
			return err
		}
	}
	if p.ProductionMode != nil {
		if err := domainstudio.ValidateProductionMode(*p.ProductionMode); err != nil {
			return err
		}
	}
	if p.AspectRatio != nil && !slices.Contains(domainmedia.VideoAspectRatios(), strings.TrimSpace(*p.AspectRatio)) {
		return apperr.Validation(fmt.Sprintf("unsupported aspect ratio %q", *p.AspectRatio)).
			WithMsg("errors.mediagen.invalid_aspect_ratio", map[string]any{"value": *p.AspectRatio})
	}
	return nil
}

// StudioProjectDeletePayload deletes a project record and optionally its workspace folder.
type StudioProjectDeletePayload struct {
	ID          string `json:"id"`
	DeleteFiles bool   `json:"delete_files,omitempty"`
}

// Validate checks StudioProjectDeletePayload.
func (p StudioProjectDeletePayload) Validate() error {
	if strings.TrimSpace(p.ID) == "" {
		return apperr.Validation("project id is required")
	}
	return nil
}

// StudioProjectDetailResult returns a project with its episodes, assets, and optional default episode detail.
type StudioProjectDetailResult struct {
	Project       StudioProject              `json:"project"`
	Episodes      []StudioEpisode            `json:"episodes"`
	Assets        []StudioAsset              `json:"assets"`
	ActiveEpisode *StudioEpisodeDetailResult `json:"active_episode,omitempty"`
}

// StudioEpisodeCreatePayload creates a new episode in a project.
type StudioEpisodeCreatePayload struct {
	ProjectID string `json:"project_id"`
	Code      string `json:"code,omitempty"`
	Title     string `json:"title,omitempty"`
}

// Validate checks StudioEpisodeCreatePayload.
func (p StudioEpisodeCreatePayload) Validate() error {
	if strings.TrimSpace(p.ProjectID) == "" {
		return apperr.Validation("project_id is required")
	}
	return nil
}

// StudioEpisodeDetailResult bundles everything needed to render or execute an episode stage.
type StudioEpisodeDetailResult struct {
	Project           StudioProject           `json:"project"`
	Episode           StudioEpisode           `json:"episode"`
	Assets            []StudioAsset           `json:"assets"`
	Shots             []StudioShot            `json:"shots"`
	RawScript         string                  `json:"raw_script"`
	NormalizedScript  *StudioNormalizedScript `json:"normalized_script,omitempty"`
	FinalVideoPath    string                  `json:"final_video_path,omitempty"`
	FinalVideoAssetID string                  `json:"final_video_asset_id,omitempty"`
}

// StudioEpisodeSaveScriptPayload saves raw screenplay text and/or edited normalized script JSON for an episode.
type StudioEpisodeSaveScriptPayload struct {
	EpisodeID        string                     `json:"episode_id"`
	RawScript        *string                    `json:"raw_script,omitempty"`
	NormalizedScript *StudioNormalizedScript    `json:"normalized_script,omitempty"`
	Stage            *domainstudio.EpisodeStage `json:"stage,omitempty"`
}

// Validate checks StudioEpisodeSaveScriptPayload.
func (p StudioEpisodeSaveScriptPayload) Validate() error {
	if strings.TrimSpace(p.EpisodeID) == "" {
		return apperr.Validation("episode_id is required")
	}
	if p.Stage != nil {
		if err := domainstudio.ValidateEpisodeStage(*p.Stage); err != nil {
			return err
		}
	}
	return nil
}

// StudioAssetUpsertPayload creates, updates, or deletes an entry in the project's asset bible.
type StudioAssetUpsertPayload struct {
	ID             string                 `json:"id,omitempty"`
	ProjectID      string                 `json:"project_id"`
	Kind           domainstudio.AssetKind `json:"kind"`
	Name           string                 `json:"name"`
	CodeTag        string                 `json:"code_tag,omitempty"`
	Description    string                 `json:"description,omitempty"`
	Prompt         string                 `json:"prompt,omitempty"`
	PrimaryAssetID string                 `json:"primary_asset_id,omitempty"`
	PrimaryPath    string                 `json:"primary_path,omitempty"`
	VoiceProvider  string                 `json:"voice_provider,omitempty"`
	VoiceID        string                 `json:"voice_id,omitempty"`
	VoiceStyle     string                 `json:"voice_style,omitempty"`
	Delete         bool                   `json:"delete,omitempty"`
}

// Validate checks StudioAssetUpsertPayload.
func (p StudioAssetUpsertPayload) Validate() error {
	if p.Delete {
		if strings.TrimSpace(p.ID) == "" {
			return apperr.Validation("asset id is required for delete")
		}
		return nil
	}
	if strings.TrimSpace(p.ProjectID) == "" {
		return apperr.Validation("project_id is required")
	}
	if strings.TrimSpace(p.Name) == "" {
		return apperr.Validation("asset name is required").WithMsg("errors.studio.asset_name_required", nil)
	}
	if p.Kind != "" {
		if err := domainstudio.ValidateAssetKind(p.Kind); err != nil {
			return err
		}
	}
	return nil
}

// StudioAssetUpsertResult returns the mutated asset and refreshed project asset list.
type StudioAssetUpsertResult struct {
	Asset  *StudioAsset  `json:"asset,omitempty"`
	Assets []StudioAsset `json:"assets"`
}

// StudioShotUpsertPayload creates or updates a storyboard shot.
type StudioShotUpsertPayload struct {
	ID              string                   `json:"id,omitempty"`
	EpisodeID       string                   `json:"episode_id"`
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

// Validate checks StudioShotUpsertPayload.
func (p StudioShotUpsertPayload) Validate() error {
	if strings.TrimSpace(p.EpisodeID) == "" {
		return apperr.Validation("episode_id is required")
	}
	if p.DurationSec != nil && (*p.DurationSec < 1 || *p.DurationSec > 60) {
		return apperr.Validation("shot duration_sec must be between 1 and 60")
	}
	if p.ShotMode != "" {
		if err := domainstudio.ValidateShotMode(p.ShotMode); err != nil {
			return err
		}
	}
	if p.Status != nil {
		if err := domainstudio.ValidateShotStatus(*p.Status); err != nil {
			return err
		}
	}
	return nil
}

// StudioShotUpsertResult returns the updated shot and full ordered episode shot list.
type StudioShotUpsertResult struct {
	Shot  StudioShot   `json:"shot"`
	Shots []StudioShot `json:"shots"`
}

// StudioShotReorderPayload reorders shots in an episode.
type StudioShotReorderPayload struct {
	EpisodeID string   `json:"episode_id"`
	ShotIDs   []string `json:"shot_ids"`
}

// Validate checks StudioShotReorderPayload.
func (p StudioShotReorderPayload) Validate() error {
	if strings.TrimSpace(p.EpisodeID) == "" {
		return apperr.Validation("episode_id is required")
	}
	if len(p.ShotIDs) == 0 {
		return apperr.Validation("shot_ids is required")
	}
	return nil
}

// StudioShotListResult returns the reordered shot list.
type StudioShotListResult struct {
	Shots []StudioShot `json:"shots"`
}

// StudioEpisodeRunStagePayload executes a Phase 1 production stage (single target or batch) through engine nodes.
type StudioEpisodeRunStagePayload struct {
	EpisodeID          string               `json:"episode_id"`
	Stage              StudioRunStageAction `json:"stage"`
	AssetIDs           []string             `json:"asset_ids,omitempty"`
	ShotIDs            []string             `json:"shot_ids,omitempty"`
	Model              string               `json:"model,omitempty"`
	Voice              string               `json:"voice,omitempty"`
	Resolution         string               `json:"resolution,omitempty"`
	FPS                int                  `json:"fps,omitempty"`
	NarrationAudioPath string               `json:"narration_audio_path,omitempty"`
	Force              bool                 `json:"force,omitempty"`
	Approved           bool                 `json:"approved,omitempty"`
}

// Validate checks StudioEpisodeRunStagePayload.
func (p StudioEpisodeRunStagePayload) Validate() error {
	if strings.TrimSpace(p.EpisodeID) == "" {
		return apperr.Validation("episode_id is required")
	}
	if !slices.Contains(StudioRunStageActions(), p.Stage) {
		return apperr.Validation(fmt.Sprintf("unsupported studio run stage %q", p.Stage))
	}
	return nil
}

// StudioRunStageResult returns updated episode state plus summary of the executed stage.
type StudioRunStageResult struct {
	Episode        StudioEpisodeDetailResult `json:"episode"`
	OutputPath     string                    `json:"output_path,omitempty"`
	OutputAssetID  string                    `json:"output_asset_id,omitempty"`
	CompletedCount int                       `json:"completed_count"`
	FailedCount    int                       `json:"failed_count"`
	Summary        string                    `json:"summary"`
}

// StudioProjectChangedEvent is published on studio:project_changed.
type StudioProjectChangedEvent struct {
	ProjectID string         `json:"project_id"`
	Action    string         `json:"action"` // created | updated | deleted | synced
	Project   *StudioProject `json:"project,omitempty"`
}

// StudioEpisodeUpdatedEvent is published on studio:episode_updated.
type StudioEpisodeUpdatedEvent struct {
	ProjectID string                    `json:"project_id"`
	EpisodeID string                    `json:"episode_id"`
	Stage     domainstudio.EpisodeStage `json:"stage"`
	Reason    string                    `json:"reason"`
}

// StudioBatchProgressEvent is published on studio:batch_progress during multi-item execution.
type StudioBatchProgressEvent struct {
	ProjectID string  `json:"project_id"`
	EpisodeID string  `json:"episode_id"`
	Stage     string  `json:"stage"`
	Current   int     `json:"current"`
	Total     int     `json:"total"`
	TargetID  string  `json:"target_id,omitempty"`
	Percent   float64 `json:"percent"`
	Message   string  `json:"message"`
	Status    string  `json:"status"` // running | completed | failed
}

// StudioJob represents a durable Phase 2 queue item synced with studio_jobs.
type StudioJob struct {
	ID            string  `json:"id"`
	ProjectID     string  `json:"project_id"`
	EpisodeID     string  `json:"episode_id,omitempty"`
	ShotID        string  `json:"shot_id,omitempty"`
	AssetID       string  `json:"asset_id,omitempty"`
	JobKind       string  `json:"job_kind"`
	ProviderID    string  `json:"provider_id,omitempty"`
	ModelRef      string  `json:"model_ref,omitempty"`
	Status        string  `json:"status"`
	ProviderJobID string  `json:"provider_job_id,omitempty"`
	Progress      int     `json:"progress"`
	Attempts      int     `json:"attempts"`
	MaxAttempts   int     `json:"max_attempts"`
	NextPollAt    int64   `json:"next_poll_at,omitempty"`
	OutputPath    string  `json:"output_path,omitempty"`
	ErrorText     string  `json:"error_text,omitempty"`
	EstimatedUSD  float64 `json:"estimated_usd"`
	ActualUSD     float64 `json:"actual_usd"`
	CreatedAt     int64   `json:"created_at"`
	UpdatedAt     int64   `json:"updated_at"`
	StartedAt     int64   `json:"started_at,omitempty"`
	FinishedAt    int64   `json:"finished_at,omitempty"`
}

// StudioQueueEnqueuePayload queues durable background jobs for an episode stage or target list.
type StudioQueueEnqueuePayload struct {
	EpisodeID string   `json:"episode_id"`
	Stage     string   `json:"stage"` // generate_assets | generate_keyframes | generate_videos | generate_tts | lipsync
	AssetIDs  []string `json:"asset_ids,omitempty"`
	ShotIDs   []string `json:"shot_ids,omitempty"`
	Model     string   `json:"model,omitempty"`
	Force     bool     `json:"force,omitempty"`
	Approved  bool     `json:"approved,omitempty"`
}

// Validate checks StudioQueueEnqueuePayload fields.
func (p StudioQueueEnqueuePayload) Validate() error {
	if strings.TrimSpace(p.EpisodeID) == "" {
		return apperr.Validation("episode_id is required")
	}
	if strings.TrimSpace(p.Stage) == "" {
		return apperr.Validation("stage is required")
	}
	return nil
}

// StudioQueueEnqueueResult returns queued jobs and preflight total cost.
type StudioQueueEnqueueResult struct {
	Jobs         []StudioJob `json:"jobs"`
	Skipped      int         `json:"skipped"`
	EstimatedUSD float64     `json:"estimated_usd"`
	Summary      string      `json:"summary"`
}

// StudioQueueListPayload filters durable studio jobs.
type StudioQueueListPayload struct {
	ProjectID string `json:"project_id,omitempty"`
	EpisodeID string `json:"episode_id,omitempty"`
	Status    string `json:"status,omitempty"`
	Limit     int    `json:"limit,omitempty"`
}

// StudioQueueListResult returns matching studio jobs.
type StudioQueueListResult struct {
	Jobs []StudioJob `json:"jobs"`
}

// StudioQueueControlPayload pauses, resumes, cancels, or retries queue jobs.
type StudioQueueControlPayload struct {
	Action    string `json:"action"` // pause | resume | cancel | retry
	ProjectID string `json:"project_id,omitempty"`
	EpisodeID string `json:"episode_id,omitempty"`
	JobID     string `json:"job_id,omitempty"`
}

// Validate checks StudioQueueControlPayload.
func (p StudioQueueControlPayload) Validate() error {
	switch strings.ToLower(strings.TrimSpace(p.Action)) {
	case "pause", "resume", "cancel", "retry":
		return nil
	default:
		return apperr.Validation("action must be pause, resume, cancel, or retry")
	}
}

// StudioQueueControlResult returns affected count and optional updated job.
type StudioQueueControlResult struct {
	Affected int        `json:"affected"`
	Job      *StudioJob `json:"job,omitempty"`
}

// StudioPricingEntry represents a rate in studio_pricing_catalog.
type StudioPricingEntry struct {
	ID           string  `json:"id"`
	ProviderID   string  `json:"provider_id"`
	ModelID      string  `json:"model_id"`
	Capability   string  `json:"capability"`
	Unit         string  `json:"unit"`
	UnitPriceUSD float64 `json:"unit_price_usd"`
	Currency     string  `json:"currency"`
	UpdatedAt    int64   `json:"updated_at"`
}

// StudioCostLedgerEntry represents one cost entry in studio_cost_ledger.
type StudioCostLedgerEntry struct {
	ID           string  `json:"id"`
	ProjectID    string  `json:"project_id,omitempty"`
	EpisodeID    string  `json:"episode_id,omitempty"`
	ShotID       string  `json:"shot_id,omitempty"`
	JobID        string  `json:"job_id,omitempty"`
	ProviderID   string  `json:"provider_id"`
	ModelID      string  `json:"model_id,omitempty"`
	Capability   string  `json:"capability"`
	Operation    string  `json:"operation,omitempty"`
	InputUnits   float64 `json:"input_units"`
	OutputUnits  float64 `json:"output_units"`
	Unit         string  `json:"unit"`
	EstimatedUSD float64 `json:"estimated_usd"`
	ActualUSD    float64 `json:"actual_usd"`
	Status       string  `json:"status"`
	CreatedAt    int64   `json:"created_at"`
}

// StudioCostSummaryPayload queries cost totals and recent ledger entries.
type StudioCostSummaryPayload struct {
	ProjectID string `json:"project_id"`
	EpisodeID string `json:"episode_id,omitempty"`
	Limit     int    `json:"limit,omitempty"`
}

// Validate validates StudioCostSummaryPayload.
func (p StudioCostSummaryPayload) Validate() error {
	if strings.TrimSpace(p.ProjectID) == "" {
		return apperr.Validation("project_id is required")
	}
	return nil
}

// StudioCostSummaryResult returns cost totals, breakdown maps, recent ledger rows, and catalog pricing.
type StudioCostSummaryResult struct {
	ProjectID         string                  `json:"project_id"`
	EpisodeID         string                  `json:"episode_id,omitempty"`
	Currency          string                  `json:"currency"`
	EstimatedTotalUSD float64                 `json:"estimated_total_usd"`
	ActualTotalUSD    float64                 `json:"actual_total_usd"`
	EntryCount        int                     `json:"entry_count"`
	ByCapability      map[string]float64      `json:"by_capability"`
	ByProvider        map[string]float64      `json:"by_provider"`
	Entries           []StudioCostLedgerEntry `json:"entries"`
	Pricing           []StudioPricingEntry    `json:"pricing"`
}

// StudioPricingUpsertPayload updates or adds a pricing rate in studio_pricing_catalog.
type StudioPricingUpsertPayload struct {
	ProviderID   string  `json:"provider_id"`
	ModelID      string  `json:"model_id"`
	Capability   string  `json:"capability"`
	Unit         string  `json:"unit"`
	UnitPriceUSD float64 `json:"unit_price_usd"`
}

// StudioMediaVersion represents an output version in studio_media_versions.
type StudioMediaVersion struct {
	ID         string  `json:"id"`
	ProjectID  string  `json:"project_id"`
	EpisodeID  string  `json:"episode_id,omitempty"`
	TargetKind string  `json:"target_kind"`
	TargetID   string  `json:"target_id"`
	VersionNum int     `json:"version_num"`
	MediaPath  string  `json:"media_path"`
	SourceNode string  `json:"source_node,omitempty"`
	PromptUsed string  `json:"prompt_used,omitempty"`
	ModelRef   string  `json:"model_ref,omitempty"`
	Seed       *int    `json:"seed,omitempty"`
	CostUSD    float64 `json:"cost_usd"`
	QCStatus   string  `json:"qc_status"`
	IsSelected bool    `json:"is_selected"`
	CreatedAt  int64   `json:"created_at"`
}

// StudioVersionsListPayload lists versions for a target asset or shot slot.
type StudioVersionsListPayload struct {
	TargetKind string `json:"target_kind"`
	TargetID   string `json:"target_id"`
}

// StudioVersionsListResult returns version history plus latest QC report if any.
type StudioVersionsListResult struct {
	Versions []StudioMediaVersion `json:"versions"`
	LatestQC *StudioQCReport      `json:"latest_qc,omitempty"`
}

// StudioVersionSelectPayload activates a specific saved media version.
type StudioVersionSelectPayload struct {
	VersionID string `json:"version_id"`
}

// StudioVersionSelectResult returns the newly selected version and updated episode state if applicable.
type StudioVersionSelectResult struct {
	Version StudioMediaVersion         `json:"version"`
	Episode *StudioEpisodeDetailResult `json:"episode,omitempty"`
}

// StudioGridSplitPayload splits a grid image via tool.grid_split and records versions.
type StudioGridSplitPayload struct {
	ProjectID  string    `json:"project_id"`
	EpisodeID  string    `json:"episode_id,omitempty"`
	TargetKind string    `json:"target_kind,omitempty"`
	TargetID   string    `json:"target_id,omitempty"`
	ImagePath  string    `json:"image_path"`
	Rows       int       `json:"rows"`
	Cols       int       `json:"cols"`
	MarginPx   int       `json:"margin_px,omitempty"`
	GutterPx   int       `json:"gutter_px,omitempty"`
	RowWeights []float64 `json:"row_weights,omitempty"`
	ColWeights []float64 `json:"col_weights,omitempty"`
}

// StudioGridSplitResult returns split panels and created version records.
type StudioGridSplitResult struct {
	Panels   []GridPanel          `json:"panels"`
	Versions []StudioMediaVersion `json:"versions,omitempty"`
}

// StudioMaskEditPayload runs canvas-masked inpainting and selects the resulting image version.
type StudioMaskEditPayload struct {
	ProjectID      string `json:"project_id"`
	EpisodeID      string `json:"episode_id,omitempty"`
	TargetKind     string `json:"target_kind,omitempty"`
	TargetID       string `json:"target_id,omitempty"`
	ImagePath      string `json:"image_path"`
	MaskPath       string `json:"mask_path,omitempty"`
	MaskDataBase64 string `json:"mask_data_base64,omitempty"`
	Prompt         string `json:"prompt"`
	ModelRef       string `json:"model_ref,omitempty"`
}

// StudioMaskEditResult returns the inpainted file and updated episode state.
type StudioMaskEditResult struct {
	Path    string                     `json:"path"`
	AssetID string                     `json:"asset_id,omitempty"`
	Version *StudioMediaVersion        `json:"version,omitempty"`
	Episode *StudioEpisodeDetailResult `json:"episode,omitempty"`
}

// StudioExtractLastFramePayload extracts the last frame from a shot's video and optionally binds it to the next shot.
type StudioExtractLastFramePayload struct {
	EpisodeID    string `json:"episode_id"`
	SourceShotID string `json:"source_shot_id"`
	TargetShotID string `json:"target_shot_id,omitempty"`
}

// StudioExtractLastFrameResult returns the extracted frame and updated episode detail.
type StudioExtractLastFrameResult struct {
	FramePath string                     `json:"frame_path"`
	AssetID   string                     `json:"asset_id,omitempty"`
	Version   *StudioMediaVersion        `json:"version,omitempty"`
	Episode   *StudioEpisodeDetailResult `json:"episode,omitempty"`
}

// StudioQCReviewPayload runs tool.qc_review on a media artifact and saves a studio_qc_reports row.
type StudioQCReviewPayload struct {
	ProjectID      string   `json:"project_id"`
	EpisodeID      string   `json:"episode_id,omitempty"`
	TargetKind     string   `json:"target_kind,omitempty"`
	TargetID       string   `json:"target_id,omitempty"`
	MediaVersionID string   `json:"media_version_id,omitempty"`
	MediaPath      string   `json:"media_path"`
	ReferencePaths []string `json:"reference_paths,omitempty"`
	PromptExpected string   `json:"prompt_expected,omitempty"`
	VisionModelRef string   `json:"vision_model_ref,omitempty"`
	SampleCount    int      `json:"sample_count,omitempty"`
	SkipVision     bool     `json:"skip_vision,omitempty"`
}

// StudioQCReport represents a saved visual & technical QC report.
type StudioQCReport struct {
	ID             string          `json:"id"`
	ProjectID      string          `json:"project_id"`
	EpisodeID      string          `json:"episode_id,omitempty"`
	TargetKind     string          `json:"target_kind"`
	TargetID       string          `json:"target_id"`
	MediaVersionID string          `json:"media_version_id,omitempty"`
	MediaPath      string          `json:"media_path"`
	Verdict        string          `json:"verdict"`
	OverallScore   float64         `json:"overall_score"`
	Issues         []QCReviewIssue `json:"issues"`
	VisionSummary  string          `json:"vision_summary,omitempty"`
	VisionModelRef string          `json:"vision_model_ref,omitempty"`
	SampleFrames   []string        `json:"sample_frames,omitempty"`
	CreatedAt      int64           `json:"created_at"`
}

// StudioBatchPreflightPayload computes readiness and estimated cost for an episode stage batch.
type StudioBatchPreflightPayload struct {
	ProjectID string `json:"project_id"`
	EpisodeID string `json:"episode_id,omitempty"`
	Stage     string `json:"stage"`
	Model     string `json:"model,omitempty"`
	ItemCount int    `json:"item_count,omitempty"`
}

// StudioQueueUpdatedEvent is pushed when queue jobs change state.
type StudioQueueUpdatedEvent struct {
	ProjectID string     `json:"project_id"`
	EpisodeID string     `json:"episode_id,omitempty"`
	Job       *StudioJob `json:"job,omitempty"`
	Summary   string     `json:"summary,omitempty"`
}

// StudioCostUpdatedEvent is pushed when ledger spend updates.
type StudioCostUpdatedEvent struct {
	ProjectID         string  `json:"project_id"`
	EpisodeID         string  `json:"episode_id,omitempty"`
	ActualTotalUSD    float64 `json:"actual_total_usd"`
	EstimatedTotalUSD float64 `json:"estimated_total_usd"`
	EntryCount        int     `json:"entry_count"`
}

// StudioTimelineClip is the transport DTO for one trimmed media clip on a timeline track.
type StudioTimelineClip struct {
	ID              string                      `json:"id"`
	ShotID          string                      `json:"shot_id,omitempty"`
	AssetID         string                      `json:"asset_id,omitempty"`
	Label           string                      `json:"label,omitempty"`
	FilePath        string                      `json:"file_path"`
	TimelineStartMs int64                       `json:"timeline_start_ms"`
	SourceInMs      int64                       `json:"source_in_ms"`
	SourceOutMs     int64                       `json:"source_out_ms"`
	Speed           float64                     `json:"speed"`
	Volume          float64                     `json:"volume"`
	TransitionIn    domainstudio.TransitionType `json:"transition_in"`
	TransitionMs    int64                       `json:"transition_ms"`
	KenBurnsPreset  domainstudio.KenBurnsPreset `json:"ken_burns_preset,omitempty"`
	SpritePath      string                      `json:"sprite_path,omitempty"`
	SpriteAssetID   string                      `json:"sprite_asset_id,omitempty"`
	Peaks           []float64                   `json:"peaks,omitempty"`
}

// StudioTimelineTrack is the transport DTO for one lane in an EditTimeline.
type StudioTimelineTrack struct {
	ID     string                 `json:"id"`
	Name   string                 `json:"name,omitempty"`
	Kind   domainstudio.TrackKind `json:"kind"`
	Muted  bool                   `json:"muted"`
	Solo   bool                   `json:"solo,omitempty"`
	Locked bool                   `json:"locked"`
	Volume float64                `json:"volume"`
	Clips  []StudioTimelineClip   `json:"clips"`
}

// StudioSubtitleCue is the transport DTO for a timed subtitle cue.
type StudioSubtitleCue struct {
	ID          string `json:"id"`
	StartMs     int64  `json:"start_ms"`
	EndMs       int64  `json:"end_ms"`
	Speaker     string `json:"speaker,omitempty"`
	Text        string `json:"text"`
	StylePreset string `json:"style_preset,omitempty"`
}

// StudioTimelineMarker is the transport DTO for a timeline ruler marker.
type StudioTimelineMarker struct {
	ID     string `json:"id"`
	TimeMs int64  `json:"time_ms"`
	Label  string `json:"label"`
	Color  string `json:"color,omitempty"`
}

// StudioEditTimeline is the normalized multi-track post-production timeline DTO.
type StudioEditTimeline struct {
	Version   int                    `json:"version"`
	FPS       int                    `json:"fps"`
	Width     int                    `json:"width"`
	Height    int                    `json:"height"`
	Tracks    []StudioTimelineTrack  `json:"tracks"`
	Subtitles []StudioSubtitleCue    `json:"subtitles"`
	Markers   []StudioTimelineMarker `json:"markers,omitempty"`
}

// StudioTimelineOp represents one atomic timeline mutation sent to studio:timeline_apply_ops.
type StudioTimelineOp struct {
	Op              string                       `json:"op"`
	Timeline        *StudioEditTimeline          `json:"timeline,omitempty"`
	FPS             *int                         `json:"fps,omitempty"`
	Width           *int                         `json:"width,omitempty"`
	Height          *int                         `json:"height,omitempty"`
	TrackID         string                       `json:"track_id,omitempty"`
	TargetTrackID   string                       `json:"target_track_id,omitempty"`
	TrackKind       domainstudio.TrackKind       `json:"track_kind,omitempty"`
	TrackName       *string                      `json:"track_name,omitempty"`
	Muted           *bool                        `json:"muted,omitempty"`
	Solo            *bool                        `json:"solo,omitempty"`
	Locked          *bool                        `json:"locked,omitempty"`
	Volume          *float64                     `json:"volume,omitempty"`
	ClipID          string                       `json:"clip_id,omitempty"`
	NewClipID       string                       `json:"new_clip_id,omitempty"`
	Clip            *StudioTimelineClip          `json:"clip,omitempty"`
	TimelineStartMs *int64                       `json:"timeline_start_ms,omitempty"`
	SourceInMs      *int64                       `json:"source_in_ms,omitempty"`
	SourceOutMs     *int64                       `json:"source_out_ms,omitempty"`
	Speed           *float64                     `json:"speed,omitempty"`
	TransitionIn    *domainstudio.TransitionType `json:"transition_in,omitempty"`
	TransitionMs    *int64                       `json:"transition_ms,omitempty"`
	KenBurnsPreset  *domainstudio.KenBurnsPreset `json:"ken_burns_preset,omitempty"`
	Label           *string                      `json:"label,omitempty"`
	SplitAtMs       int64                        `json:"split_at_ms,omitempty"`
	SubtitleID      string                       `json:"subtitle_id,omitempty"`
	Subtitle        *StudioSubtitleCue           `json:"subtitle,omitempty"`
	MarkerID        string                       `json:"marker_id,omitempty"`
	Marker          *StudioTimelineMarker        `json:"marker,omitempty"`
}

// StudioTimelineRevisionSummary summarizes one saved revision in studio_timeline_revisions.
type StudioTimelineRevisionSummary struct {
	ID        string `json:"id"`
	Revision  int    `json:"revision"`
	Actor     string `json:"actor"`
	Summary   string `json:"summary"`
	CreatedAt int64  `json:"created_at"`
}

// StudioTimelineResult returns the active timeline state, revision metadata, and history.
type StudioTimelineResult struct {
	ProjectID    string                          `json:"project_id"`
	EpisodeID    string                          `json:"episode_id"`
	Revision     int                             `json:"revision"`
	CanUndo      bool                            `json:"can_undo"`
	CanRedo      bool                            `json:"can_redo"`
	DurationMs   int64                           `json:"duration_ms"`
	TimelinePath string                          `json:"timeline_path,omitempty"`
	Timeline     StudioEditTimeline              `json:"timeline"`
	Revisions    []StudioTimelineRevisionSummary `json:"revisions,omitempty"`
	UpdatedAt    int64                           `json:"updated_at"`
}

// StudioTimelineApplyOpsPayload applies atomic timeline operations under optimistic revision checking.
type StudioTimelineApplyOpsPayload struct {
	EpisodeID        string             `json:"episode_id"`
	ExpectedRevision int                `json:"expected_revision"`
	Actor            string             `json:"actor,omitempty"`
	Summary          string             `json:"summary,omitempty"`
	Ops              []StudioTimelineOp `json:"ops"`
}

// Validate checks StudioTimelineApplyOpsPayload.
func (p StudioTimelineApplyOpsPayload) Validate() error {
	if strings.TrimSpace(p.EpisodeID) == "" {
		return apperr.Validation("episode_id is required")
	}
	if p.ExpectedRevision < 0 {
		return apperr.Validation("expected_revision must be >= 0")
	}
	if len(p.Ops) == 0 {
		return apperr.Validation("at least one timeline op is required")
	}
	return nil
}

// StudioTimelineRevisionNavPayload steps timeline revision history backward (undo) or forward (redo).
type StudioTimelineRevisionNavPayload struct {
	EpisodeID        string `json:"episode_id"`
	ExpectedRevision int    `json:"expected_revision"`
}

// Validate checks StudioTimelineRevisionNavPayload.
func (p StudioTimelineRevisionNavPayload) Validate() error {
	if strings.TrimSpace(p.EpisodeID) == "" {
		return apperr.Validation("episode_id is required")
	}
	if p.ExpectedRevision < 0 {
		return apperr.Validation("expected_revision must be >= 0")
	}
	return nil
}

// StudioTimelineAutoAssemblePayload triggers tool.timeline_auto_assemble on an episode's shots & audio.
type StudioTimelineAutoAssemblePayload struct {
	EpisodeID         string                      `json:"episode_id"`
	ExpectedRevision  *int                        `json:"expected_revision,omitempty"`
	Resolution        string                      `json:"resolution,omitempty"`
	FPS               int                         `json:"fps,omitempty"`
	TransitionDefault domainstudio.TransitionType `json:"transition_default,omitempty"`
	TransitionMs      int64                       `json:"transition_ms,omitempty"`
	BGMPath           string                      `json:"bgm_path,omitempty"`
	BGMVolume         float64                     `json:"bgm_volume,omitempty"`
}

// Validate checks StudioTimelineAutoAssemblePayload.
func (p StudioTimelineAutoAssemblePayload) Validate() error {
	if strings.TrimSpace(p.EpisodeID) == "" {
		return apperr.Validation("episode_id is required")
	}
	if p.TransitionDefault != "" && !p.TransitionDefault.Valid() {
		return apperr.Validation(fmt.Sprintf("unsupported transition_default %q", p.TransitionDefault))
	}
	return nil
}

// StudioMediaPeaksThumbsPayload generates waveform peaks and a thumbnail sprite strip for a timeline clip.
type StudioMediaPeaksThumbsPayload struct {
	EpisodeID        string  `json:"episode_id"`
	MediaPath        string  `json:"media_path"`
	ClipID           string  `json:"clip_id,omitempty"`
	ThumbIntervalSec float64 `json:"thumb_interval_sec,omitempty"`
	PeakBins         int     `json:"peak_bins,omitempty"`
}

// Validate checks StudioMediaPeaksThumbsPayload.
func (p StudioMediaPeaksThumbsPayload) Validate() error {
	if strings.TrimSpace(p.EpisodeID) == "" {
		return apperr.Validation("episode_id is required")
	}
	if strings.TrimSpace(p.MediaPath) == "" {
		return apperr.Validation("media_path is required")
	}
	return nil
}

// StudioTimelineRenderPayload renders the episode's active EditTimeline into an MP4 + subtitle files.
type StudioTimelineRenderPayload struct {
	EpisodeID        string  `json:"episode_id"`
	Filename         string  `json:"filename,omitempty"`
	BurnSubtitles    bool    `json:"burn_subtitles,omitempty"`
	SubtitleFontSize int     `json:"subtitle_font_size,omitempty"`
	LoudnormLUFS     float64 `json:"loudnorm_lufs,omitempty"`
	CRF              int     `json:"crf,omitempty"`
	Preset           string  `json:"preset,omitempty"`
}

// Validate checks StudioTimelineRenderPayload.
func (p StudioTimelineRenderPayload) Validate() error {
	if strings.TrimSpace(p.EpisodeID) == "" {
		return apperr.Validation("episode_id is required")
	}
	return nil
}

// StudioTimelineRenderResult returns the rendered MP4, subtitle artifacts, and refreshed episode detail.
type StudioTimelineRenderResult struct {
	OutputPath  string                    `json:"output_path"`
	AssetID     string                    `json:"asset_id,omitempty"`
	SRTPath     string                    `json:"srt_path,omitempty"`
	ASSPath     string                    `json:"ass_path,omitempty"`
	DurationSec float64                   `json:"duration_sec"`
	Episode     StudioEpisodeDetailResult `json:"episode"`
}

// StudioTimelineExportNLEPayload exports the episode's active EditTimeline to CapCut/Jianying/FCPXML/OTIO.
type StudioTimelineExportNLEPayload struct {
	EpisodeID        string                       `json:"episode_id"`
	Format           domainstudio.NLEExportFormat `json:"format"`
	ProjectName      string                       `json:"project_name,omitempty"`
	IncludeMediaCopy *bool                        `json:"include_media_copy,omitempty"`
}

// Validate checks StudioTimelineExportNLEPayload.
func (p StudioTimelineExportNLEPayload) Validate() error {
	if strings.TrimSpace(p.EpisodeID) == "" {
		return apperr.Validation("episode_id is required")
	}
	if p.Format == "" {
		return apperr.Validation("format is required")
	}
	if !p.Format.Valid() {
		return apperr.Validation(fmt.Sprintf("unsupported NLE export format %q", p.Format))
	}
	return nil
}

// StudioOpenExportFolderPayload opens an episode export folder (or parent directory of a file inside it) in OS explorer.
type StudioOpenExportFolderPayload struct {
	EpisodeID string `json:"episode_id"`
	FilePath  string `json:"file_path,omitempty"`
}

// Validate checks StudioOpenExportFolderPayload.
func (p StudioOpenExportFolderPayload) Validate() error {
	if strings.TrimSpace(p.EpisodeID) == "" && strings.TrimSpace(p.FilePath) == "" {
		return apperr.Validation("episode_id or file_path is required")
	}
	return nil
}

// StudioTimelineChangedEvent is published on studio:timeline_changed whenever UI, Agent, or a Node modifies a timeline.
type StudioTimelineChangedEvent struct {
	ProjectID  string             `json:"project_id"`
	EpisodeID  string             `json:"episode_id"`
	Revision   int                `json:"revision"`
	CanUndo    bool               `json:"can_undo"`
	CanRedo    bool               `json:"can_redo"`
	DurationMs int64              `json:"duration_ms"`
	Actor      string             `json:"actor"`
	Summary    string             `json:"summary"`
	Timeline   StudioEditTimeline `json:"timeline"`
}
