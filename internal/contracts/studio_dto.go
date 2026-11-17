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
	ActionStudioProjectList       = "studio:project_list"
	ActionStudioProjectCreate     = "studio:project_create"
	ActionStudioProjectGet        = "studio:project_get"
	ActionStudioProjectUpdate     = "studio:project_update"
	ActionStudioProjectDelete     = "studio:project_delete"
	ActionStudioEpisodeCreate     = "studio:episode_create"
	ActionStudioEpisodeGet        = "studio:episode_get"
	ActionStudioEpisodeSaveScript = "studio:episode_save_script"
	ActionStudioAssetUpsert       = "studio:asset_upsert"
	ActionStudioShotUpsert        = "studio:shot_upsert"
	ActionStudioShotReorder       = "studio:shot_reorder"
	ActionStudioEpisodeRunStage   = "studio:episode_run_stage"
)

// Server-pushed event names for AI Film Studio.
const (
	EventStudioProjectChanged = "studio:project_changed"
	EventStudioEpisodeUpdated = "studio:episode_updated"
	EventStudioBatchProgress  = "studio:batch_progress"
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
