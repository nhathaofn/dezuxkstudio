package agent

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"app/internal/contracts"
	domainstudio "app/internal/domain/studio"
	"app/internal/engine"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type studioProjectListToolInput struct{}

type studioProjectGetToolInput struct {
	ID string `json:"id" jsonschema:"Studio project ID or slug"`
}

type studioProjectDeleteToolInput struct {
	ID          string `json:"id" jsonschema:"Studio project ID"`
	DeleteFiles bool   `json:"delete_files,omitempty" jsonschema:"Also delete the project workspace folder inside ProjectsDir"`
}

type studioProjectCreateToolInput struct {
	Title               string `json:"title" jsonschema:"Project title"`
	Slug                string `json:"slug,omitempty" jsonschema:"Optional filesystem slug (derived from title if omitted)"`
	Genre               string `json:"genre,omitempty" jsonschema:"drama (character dialogues) or narration (documentary/voiceover story)"`
	ProductionMode      string `json:"production_mode,omitempty" jsonschema:"storyboard_i2v (keyframe then I2V) or r2v (reference-to-video)"`
	AspectRatio         string `json:"aspect_ratio,omitempty" jsonschema:"Target aspect ratio e.g. 16:9 or 9:16"`
	StylePrompt         string `json:"style_prompt,omitempty" jsonschema:"Global visual style bible prompt applied across shots and sheets"`
	AutoApproveBatches  bool   `json:"auto_approve_batches,omitempty" jsonschema:"If true, trusted workspace studio batches skip interactive confirmation"`
	DefaultTextModel    string `json:"default_text_model,omitempty" jsonschema:"Optional default LLM text model"`
	DefaultImageModel   string `json:"default_image_model,omitempty" jsonschema:"Optional default image generation model"`
	DefaultVideoModel   string `json:"default_video_model,omitempty" jsonschema:"Optional default video generation model"`
	DefaultTTSModel     string `json:"default_tts_model,omitempty" jsonschema:"Optional default TTS voice model"`
	InitialEpisodeTitle string `json:"initial_episode_title,omitempty" jsonschema:"Optional starter episode title (default: Episode 1)"`
}

type studioEpisodeCreateToolInput struct {
	ProjectID string `json:"project_id" jsonschema:"Studio project ID"`
	Code      string `json:"code,omitempty" jsonschema:"Optional episode code (e.g. ep02)"`
	Title     string `json:"title,omitempty" jsonschema:"Episode title"`
}

type studioEpisodeGetToolInput struct {
	EpisodeID string `json:"episode_id" jsonschema:"Studio episode ID"`
}

type studioSaveScriptToolInput struct {
	EpisodeID        string                            `json:"episode_id" jsonschema:"Studio episode ID"`
	RawScript        *string                           `json:"raw_script,omitempty" jsonschema:"Full screenplay or narration text (replaces raw_screenplay.txt when provided)"`
	NormalizedScript *contracts.StudioNormalizedScript `json:"normalized_script,omitempty" jsonschema:"Optional edited structured NormalizedScript JSON object"`
	Stage            *string                           `json:"stage,omitempty" jsonschema:"Optional episode stage: script|assets|storyboard|video|export"`
}

type studioAssetUpsertToolInput struct {
	ID             string `json:"id,omitempty" jsonschema:"Existing asset ID to update or delete; omit to create"`
	ProjectID      string `json:"project_id" jsonschema:"Studio project ID"`
	Kind           string `json:"kind,omitempty" jsonschema:"character|scene|prop"`
	Name           string `json:"name,omitempty" jsonschema:"Display name of character, location, or prop"`
	CodeTag        string `json:"code_tag,omitempty" jsonschema:"Consistent @Tag identifier (e.g. @Minh, @OldCafe)"`
	Description    string `json:"description,omitempty" jsonschema:"Brief role or visual summary"`
	Prompt         string `json:"prompt,omitempty" jsonschema:"Reference-sheet image generation prompt"`
	PrimaryAssetID string `json:"primary_asset_id,omitempty" jsonschema:"Optional media asset ID for existing reference sheet"`
	PrimaryPath    string `json:"primary_path,omitempty" jsonschema:"Optional reference sheet image file path inside workspace"`
	VoiceProvider  string `json:"voice_provider,omitempty" jsonschema:"Optional voice provider note"`
	VoiceID        string `json:"voice_id,omitempty" jsonschema:"Optional voice ID note"`
	VoiceStyle     string `json:"voice_style,omitempty" jsonschema:"Optional voice tone note"`
	Delete         bool   `json:"delete,omitempty" jsonschema:"Set true to delete asset by id"`
}

type studioShotUpsertToolInput struct {
	ID              string                         `json:"id,omitempty" jsonschema:"Existing shot ID to update; omit to append a new shot"`
	EpisodeID       string                         `json:"episode_id" jsonschema:"Studio episode ID"`
	Seq             *int                           `json:"seq,omitempty" jsonschema:"1-based shot sequence number"`
	SceneCode       string                         `json:"scene_code,omitempty" jsonschema:"Scene identifier e.g. S01"`
	Title           string                         `json:"title,omitempty" jsonschema:"Short shot title"`
	ShotMode        string                         `json:"shot_mode,omitempty" jsonschema:"single|grid_2x2|grid_3x3|r2v"`
	DurationSec     *int                           `json:"duration_sec,omitempty" jsonschema:"Target clip duration in seconds"`
	ImagePrompt     *string                        `json:"image_prompt,omitempty" jsonschema:"Keyframe image prompt"`
	VideoPrompt     *string                        `json:"video_prompt,omitempty" jsonschema:"Video motion/camera/dialogue prompt"`
	NarrationText   *string                        `json:"narration_text,omitempty" jsonschema:"Voiceover narration text for this shot"`
	Dialogues       []contracts.StudioDialogueLine `json:"dialogues,omitempty" jsonschema:"Character dialogue lines for Drama mode"`
	RefAssetTags    []string                       `json:"ref_asset_tags,omitempty" jsonschema:"@Tag references used for visual consistency"`
	KeyframeAssetID *string                        `json:"keyframe_asset_id,omitempty"`
	KeyframePath    *string                        `json:"keyframe_path,omitempty"`
	VideoAssetID    *string                        `json:"video_asset_id,omitempty"`
	VideoPath       *string                        `json:"video_path,omitempty"`
	AudioAssetID    *string                        `json:"audio_asset_id,omitempty"`
	AudioPath       *string                        `json:"audio_path,omitempty"`
	Status          *string                        `json:"status,omitempty" jsonschema:"pending|keyframe_ready|video_ready|tts_ready|qc_passed|failed"`
	ErrorText       *string                        `json:"error_text,omitempty" jsonschema:"Optional shot error detail when status=failed"`
}

type studioSyncWorkspaceToolInput struct {
	ProjectID string `json:"project_id,omitempty" jsonschema:"Optional studio project ID or slug; empty scans and syncs all workspace projects"`
}

type studioEpisodeRunStageToolInput struct {
	EpisodeID          string   `json:"episode_id" jsonschema:"Studio episode ID"`
	Stage              string   `json:"stage" jsonschema:"script_normalize|extract_assets|author_shots|generate_assets|generate_keyframes|generate_videos|generate_tts|probe_shots|concat_video"`
	AssetIDs           []string `json:"asset_ids,omitempty" jsonschema:"Optional asset IDs filter for generate_assets"`
	ShotIDs            []string `json:"shot_ids,omitempty" jsonschema:"Optional shot IDs filter for shot-level stages"`
	Model              string   `json:"model,omitempty" jsonschema:"Optional provider/model override (empty uses project/app default)"`
	Voice              string   `json:"voice,omitempty" jsonschema:"Optional TTS voice override for generate_tts"`
	Resolution         string   `json:"resolution,omitempty" jsonschema:"Optional output resolution e.g. 1920x1080 or 720p"`
	FPS                int      `json:"fps,omitempty" jsonschema:"Optional output FPS for concat_video"`
	NarrationAudioPath string   `json:"narration_audio_path,omitempty" jsonschema:"Optional narration audio path to mix in concat_video"`
	Force              bool     `json:"force,omitempty" jsonschema:"Re-run even for targets that already have generated outputs"`
}

type studioQueueEnqueueToolInput struct {
	EpisodeID string   `json:"episode_id" jsonschema:"Studio episode ID"`
	Stage     string   `json:"stage" jsonschema:"generate_assets|generate_keyframes|generate_videos|generate_tts|lipsync"`
	AssetIDs  []string `json:"asset_ids,omitempty" jsonschema:"Optional asset IDs filter"`
	ShotIDs   []string `json:"shot_ids,omitempty" jsonschema:"Optional shot IDs filter"`
	Model     string   `json:"model,omitempty" jsonschema:"Optional provider/model override"`
	Force     bool     `json:"force,omitempty" jsonschema:"Re-enqueue even if target already has output"`
}

type studioQueueListToolInput struct {
	ProjectID string `json:"project_id,omitempty" jsonschema:"Optional studio project ID filter"`
	EpisodeID string `json:"episode_id,omitempty" jsonschema:"Optional episode ID filter"`
	Status    string `json:"status,omitempty" jsonschema:"queued|submitted|polling|paused|succeeded|failed|canceled"`
	Limit     int    `json:"limit,omitempty" jsonschema:"Optional max queue rows to return"`
}

type studioQueueControlToolInput struct {
	Action    string `json:"action" jsonschema:"pause|resume|cancel|retry"`
	ProjectID string `json:"project_id,omitempty" jsonschema:"Project ID for episode/project pause/resume"`
	EpisodeID string `json:"episode_id,omitempty" jsonschema:"Episode ID for batch pause/resume"`
	JobID     string `json:"job_id,omitempty" jsonschema:"Specific job ID for single-job control"`
}

type studioCostSummaryToolInput struct {
	ProjectID string `json:"project_id" jsonschema:"Studio project ID"`
	EpisodeID string `json:"episode_id,omitempty" jsonschema:"Optional episode ID filter"`
	Limit     int    `json:"limit,omitempty" jsonschema:"Optional max ledger rows to return"`
}

type studioVersionsListToolInput struct {
	TargetKind string `json:"target_kind" jsonschema:"asset|keyframe|video|dub_audio|lipsync_video|grid_panel|clean_video"`
	TargetID   string `json:"target_id" jsonschema:"Asset ID or Shot ID"`
}

type studioVersionSelectToolInput struct {
	VersionID string `json:"version_id" jsonschema:"Media version ID to activate"`
}

type studioQCReviewToolInput struct {
	ProjectID      string   `json:"project_id" jsonschema:"Studio project ID"`
	EpisodeID      string   `json:"episode_id,omitempty" jsonschema:"Optional episode ID"`
	TargetKind     string   `json:"target_kind,omitempty" jsonschema:"asset|keyframe|video|dub_audio|lipsync_video|grid_panel|clean_video"`
	TargetID       string   `json:"target_id,omitempty" jsonschema:"Target asset or shot ID"`
	MediaVersionID string   `json:"media_version_id,omitempty" jsonschema:"Optional linked media version ID"`
	MediaPath      string   `json:"media_path" jsonschema:"Path to the image or video file to review"`
	ReferencePaths []string `json:"reference_paths,omitempty" jsonschema:"Optional canonical reference images for continuity checking"`
	PromptExpected string   `json:"prompt_expected,omitempty" jsonschema:"Expected visual content or prompt description"`
	VisionModelRef string   `json:"vision_model_ref,omitempty" jsonschema:"Optional multimodal Vision provider/model override"`
	SampleCount    int      `json:"sample_count,omitempty" jsonschema:"Optional sampled video frame count"`
	SkipVision     bool     `json:"skip_vision,omitempty" jsonschema:"Run FFprobe technical inspection only without Vision LLM"`
}

type studioBatchPreflightToolInput struct {
	ProjectID    string  `json:"project_id" jsonschema:"Studio project ID"`
	EpisodeID    string  `json:"episode_id,omitempty" jsonschema:"Optional episode ID"`
	Stage        string  `json:"stage" jsonschema:"generate_assets|generate_keyframes|generate_videos|generate_tts|lipsync"`
	Model        string  `json:"model,omitempty" jsonschema:"Optional provider/model override"`
	ItemCount    int     `json:"item_count,omitempty" jsonschema:"Optional explicit item count"`
	UnitsPerItem float64 `json:"units_per_item,omitempty" jsonschema:"Optional units per item override (e.g. seconds per clip or 1k chars)"`
}

type studioTimelineGetToolInput struct {
	EpisodeID string `json:"episode_id" jsonschema:"Studio episode ID"`
}

type studioTimelineClipToolInput struct {
	ID              string  `json:"id,omitempty"`
	ShotID          string  `json:"shot_id,omitempty"`
	AssetID         string  `json:"asset_id,omitempty"`
	Label           string  `json:"label,omitempty"`
	FilePath        string  `json:"file_path,omitempty"`
	TimelineStartMs int64   `json:"timeline_start_ms,omitempty"`
	SourceInMs      int64   `json:"source_in_ms,omitempty"`
	SourceOutMs     int64   `json:"source_out_ms,omitempty"`
	Speed           float64 `json:"speed,omitempty"`
	Volume          float64 `json:"volume,omitempty"`
	Mute            bool    `json:"mute,omitempty"`
	TransitionIn    string  `json:"transition_in,omitempty"`
	TransitionMs    int64   `json:"transition_ms,omitempty"`
	KenBurnsPreset  string  `json:"ken_burns_preset,omitempty"`
	SpritePath      string  `json:"sprite_path,omitempty"`
	SpriteAssetID   string  `json:"sprite_asset_id,omitempty"`
}

type studioTimelineTrackToolInput struct {
	ID     string                        `json:"id,omitempty"`
	Name   string                        `json:"name,omitempty"`
	Kind   string                        `json:"kind,omitempty"`
	Muted  bool                          `json:"muted,omitempty"`
	Solo   bool                          `json:"solo,omitempty"`
	Locked bool                          `json:"locked,omitempty"`
	Volume float64                       `json:"volume,omitempty"`
	Clips  []studioTimelineClipToolInput `json:"clips,omitempty"`
}

type studioSubtitleCueToolInput struct {
	ID          string `json:"id,omitempty"`
	StartMs     int64  `json:"start_ms,omitempty"`
	EndMs       int64  `json:"end_ms,omitempty"`
	Speaker     string `json:"speaker,omitempty"`
	Text        string `json:"text,omitempty"`
	StylePreset string `json:"style_preset,omitempty"`
}

type studioEditTimelineToolInput struct {
	Version   int                              `json:"version,omitempty"`
	FPS       int                              `json:"fps,omitempty"`
	Width     int                              `json:"width,omitempty"`
	Height    int                              `json:"height,omitempty"`
	Tracks    []studioTimelineTrackToolInput   `json:"tracks,omitempty"`
	Subtitles []studioSubtitleCueToolInput     `json:"subtitles,omitempty"`
	Markers   []contracts.StudioTimelineMarker `json:"markers,omitempty"`
}

type studioTimelineOpToolInput struct {
	Op              string                          `json:"op" jsonschema:"replace_timeline|set_format|add_track|update_track|remove_track|add_clip|update_clip|move_clip|remove_clip|split_clip|add_subtitle|update_subtitle|remove_subtitle|add_marker|remove_marker"`
	Timeline        *studioEditTimelineToolInput    `json:"timeline,omitempty"`
	FPS             *int                            `json:"fps,omitempty"`
	Width           *int                            `json:"width,omitempty"`
	Height          *int                            `json:"height,omitempty"`
	TrackID         string                          `json:"track_id,omitempty"`
	TargetTrackID   string                          `json:"target_track_id,omitempty"`
	TrackKind       domainstudio.TrackKind          `json:"track_kind,omitempty"`
	TrackName       *string                         `json:"track_name,omitempty"`
	Muted           *bool                           `json:"muted,omitempty"`
	Solo            *bool                           `json:"solo,omitempty"`
	Locked          *bool                           `json:"locked,omitempty"`
	Volume          *float64                        `json:"volume,omitempty"`
	ClipID          string                          `json:"clip_id,omitempty"`
	NewClipID       string                          `json:"new_clip_id,omitempty"`
	Clip            *studioTimelineClipToolInput    `json:"clip,omitempty"`
	TimelineStartMs *int64                          `json:"timeline_start_ms,omitempty"`
	SourceInMs      *int64                          `json:"source_in_ms,omitempty"`
	SourceOutMs     *int64                          `json:"source_out_ms,omitempty"`
	Speed           *float64                        `json:"speed,omitempty"`
	TransitionIn    *domainstudio.TransitionType    `json:"transition_in,omitempty"`
	TransitionMs    *int64                          `json:"transition_ms,omitempty"`
	KenBurnsPreset  *domainstudio.KenBurnsPreset    `json:"ken_burns_preset,omitempty"`
	Label           *string                         `json:"label,omitempty"`
	SplitAtMs       int64                           `json:"split_at_ms,omitempty"`
	SubtitleID      string                          `json:"subtitle_id,omitempty"`
	Subtitle        *studioSubtitleCueToolInput     `json:"subtitle,omitempty"`
	MarkerID        string                          `json:"marker_id,omitempty"`
	Marker          *contracts.StudioTimelineMarker `json:"marker,omitempty"`
}

type studioTimelineApplyOpsToolInput struct {
	EpisodeID        string                      `json:"episode_id" jsonschema:"Studio episode ID"`
	ExpectedRevision int                         `json:"expected_revision" jsonschema:"Current timeline revision number required for optimistic concurrency"`
	Summary          string                      `json:"summary,omitempty" jsonschema:"Short human-readable summary of this timeline edit"`
	Ops              []studioTimelineOpToolInput `json:"ops" jsonschema:"Ordered atomic timeline operations"`
}

type studioTimelineRevisionNavToolInput struct {
	EpisodeID        string `json:"episode_id" jsonschema:"Studio episode ID"`
	ExpectedRevision int    `json:"expected_revision" jsonschema:"Current timeline revision number required for optimistic concurrency"`
}

type studioProjectUpdateToolInput struct {
	ID                 string  `json:"id" jsonschema:"Studio project ID or slug"`
	Title              *string `json:"title,omitempty" jsonschema:"Updated project title"`
	Genre              *string `json:"genre,omitempty" jsonschema:"drama|narration"`
	ProductionMode     *string `json:"production_mode,omitempty" jsonschema:"storyboard_i2v|r2v"`
	AspectRatio        *string `json:"aspect_ratio,omitempty" jsonschema:"Target aspect ratio e.g. 16:9 or 9:16"`
	StylePrompt        *string `json:"style_prompt,omitempty" jsonschema:"Updated style bible prompt"`
	AutoApproveBatches *bool   `json:"auto_approve_batches,omitempty" jsonschema:"Whether trusted workspace studio batches skip approval prompts"`
	DefaultTextModel   *string `json:"default_text_model,omitempty"`
	DefaultImageModel  *string `json:"default_image_model,omitempty"`
	DefaultVideoModel  *string `json:"default_video_model,omitempty"`
	DefaultTTSModel    *string `json:"default_tts_model,omitempty"`
}

type studioShotReorderToolInput struct {
	EpisodeID string   `json:"episode_id" jsonschema:"Studio episode ID"`
	ShotIDs   []string `json:"shot_ids" jsonschema:"Ordered list of shot IDs"`
}

type studioPricingUpsertToolInput struct {
	ProviderID   string  `json:"provider_id" jsonschema:"Provider ID or * wildcard"`
	ModelID      string  `json:"model_id" jsonschema:"Model ID or * wildcard"`
	Capability   string  `json:"capability" jsonschema:"llm|image|video|tts|lipsync"`
	Unit         string  `json:"unit" jsonschema:"Pricing unit e.g. image|second|1k_chars|1k_tokens"`
	UnitPriceUSD float64 `json:"unit_price_usd" jsonschema:"USD unit rate"`
}

type studioGridSplitToolInput struct {
	ProjectID  string    `json:"project_id" jsonschema:"Studio project ID"`
	EpisodeID  string    `json:"episode_id,omitempty" jsonschema:"Optional episode ID"`
	TargetKind string    `json:"target_kind,omitempty" jsonschema:"asset|keyframe|grid_panel"`
	TargetID   string    `json:"target_id,omitempty" jsonschema:"Target asset or shot ID"`
	ImagePath  string    `json:"image_path" jsonschema:"Path to the storyboard grid image inside the workspace"`
	Rows       int       `json:"rows" jsonschema:"Grid row count"`
	Cols       int       `json:"cols" jsonschema:"Grid column count"`
	MarginPx   int       `json:"margin_px,omitempty" jsonschema:"Optional outer pixel margin"`
	GutterPx   int       `json:"gutter_px,omitempty" jsonschema:"Optional gutter pixels between cells"`
	RowWeights []float64 `json:"row_weights,omitempty" jsonschema:"Optional relative row proportions"`
	ColWeights []float64 `json:"col_weights,omitempty" jsonschema:"Optional relative column proportions"`
}

type studioMaskEditToolInput struct {
	ProjectID      string `json:"project_id" jsonschema:"Studio project ID"`
	EpisodeID      string `json:"episode_id,omitempty" jsonschema:"Optional episode ID"`
	TargetKind     string `json:"target_kind,omitempty" jsonschema:"asset|keyframe"`
	TargetID       string `json:"target_id,omitempty" jsonschema:"Target asset or shot ID"`
	ImagePath      string `json:"image_path" jsonschema:"Source image path inside the workspace"`
	MaskPath       string `json:"mask_path,omitempty" jsonschema:"Binary mask PNG path inside the workspace (white edits, black preserves)"`
	MaskDataBase64 string `json:"mask_data_base64,omitempty" jsonschema:"Optional base64 PNG mask when mask_path is omitted"`
	Prompt         string `json:"prompt" jsonschema:"Inpainting / repair prompt for masked region"`
	ModelRef       string `json:"model_ref,omitempty" jsonschema:"Optional image provider/model override"`
	AspectRatio    string `json:"aspect_ratio,omitempty" jsonschema:"Optional output aspect ratio override"`
	Resolution     string `json:"resolution,omitempty" jsonschema:"Optional output resolution override"`
}

type studioExtractLastFrameToolInput struct {
	EpisodeID    string `json:"episode_id" jsonschema:"Studio episode ID"`
	SourceShotID string `json:"source_shot_id" jsonschema:"Source shot ID whose final frame will be extracted"`
	TargetShotID string `json:"target_shot_id,omitempty" jsonschema:"Optional next shot ID to bind the extracted frame as keyframe"`
}

type studioTimelineAutoAssembleToolInput struct {
	EpisodeID         string  `json:"episode_id" jsonschema:"Studio episode ID"`
	ExpectedRevision  *int    `json:"expected_revision,omitempty" jsonschema:"Optional expected timeline revision check"`
	Resolution        string  `json:"resolution,omitempty" jsonschema:"Output resolution WxH"`
	FPS               int     `json:"fps,omitempty" jsonschema:"Output frame rate"`
	TransitionDefault string  `json:"transition_default,omitempty" jsonschema:"none|fade|crossfade|wipeleft|slideleft"`
	TransitionMs      int64   `json:"transition_ms,omitempty" jsonschema:"Transition duration in ms"`
	BGMPath           string  `json:"bgm_path,omitempty" jsonschema:"Optional workspace BGM audio file path"`
	BGMVolume         float64 `json:"bgm_volume,omitempty" jsonschema:"Optional BGM track gain (0..2)"`
}

type studioMediaPeaksThumbsToolInput struct {
	EpisodeID        string  `json:"episode_id" jsonschema:"Studio episode ID"`
	MediaPath        string  `json:"media_path" jsonschema:"Media clip path inside the workspace"`
	ClipID           string  `json:"clip_id,omitempty" jsonschema:"Optional timeline clip ID to hydrate with peaks & sprite"`
	ThumbIntervalSec float64 `json:"thumb_interval_sec,omitempty" jsonschema:"Optional thumbnail sampling interval seconds"`
	PeakBins         int     `json:"peak_bins,omitempty" jsonschema:"Optional waveform peak bucket count"`
}

type studioTimelineRenderToolInput struct {
	EpisodeID        string  `json:"episode_id" jsonschema:"Studio episode ID"`
	Filename         string  `json:"filename,omitempty" jsonschema:"Optional output .mp4 filename"`
	BurnSubtitles    bool    `json:"burn_subtitles,omitempty" jsonschema:"Whether to burn UTF-8 subtitles onto video"`
	SubtitleFontSize int     `json:"subtitle_font_size,omitempty" jsonschema:"Optional burned subtitle font size"`
	LoudnormLUFS     float64 `json:"loudnorm_lufs,omitempty" jsonschema:"Optional EBU R128 loudness target e.g. -14"`
	CRF              *int    `json:"crf,omitempty" jsonschema:"Optional H.264 CRF (0..51)"`
	Preset           string  `json:"preset,omitempty" jsonschema:"Optional FFmpeg encoding preset"`
	Overwrite        bool    `json:"overwrite,omitempty" jsonschema:"Replace an existing rendered MP4 at the target filename"`
}

type studioTimelineExportNLEToolInput struct {
	EpisodeID        string `json:"episode_id" jsonschema:"Studio episode ID"`
	Format           string `json:"format" jsonschema:"capcut|jianying|fcpxml|otio"`
	ProjectName      string `json:"project_name,omitempty" jsonschema:"Optional exported draft project name"`
	IncludeMediaCopy *bool  `json:"include_media_copy,omitempty" jsonschema:"Whether to copy media inside NLE draft zip"`
}

func (s *Service) registerStudioTools(server *mcpToolset) {
	addMCPTool(server, "studio_project_list",
		"List all AI film studio projects and the active workspace projects directory.",
		func(ctx context.Context, _ studioProjectListToolInput) (*mcp.CallToolResult, any, error) {
			if s.studio == nil {
				return mcpError("studio service is unavailable")
			}
			res, err := s.studio.ListProjects(engine.WithFileGuard(ctx, s.CheckTrustedFile))
			if err != nil {
				return mcpError(err.Error())
			}
			return mcpJSON(res)
		})

	addMCPTool(server, "studio_project_get",
		"Get studio project metadata, episodes list, active episode details, and asset bible by ID or slug.",
		func(ctx context.Context, in studioProjectGetToolInput) (*mcp.CallToolResult, any, error) {
			if s.studio == nil {
				return mcpError("studio service is unavailable")
			}
			res, err := s.studio.GetProject(engine.WithFileGuard(ctx, s.CheckTrustedFile), strings.TrimSpace(in.ID))
			if err != nil {
				return mcpError(err.Error())
			}
			return mcpJSON(res)
		})

	addMCPTool(server, "studio_project_create",
		"Create a new AI film studio project with its workspace folder structure and starter Episode 1.",
		func(ctx context.Context, in studioProjectCreateToolInput) (*mcp.CallToolResult, any, error) {
			if s.studio == nil {
				return mcpError("studio service is unavailable")
			}
			res, err := s.studio.CreateProject(engine.WithFileGuard(ctx, s.CheckTrustedFile), contracts.StudioProjectCreatePayload{
				Title:               in.Title,
				Slug:                in.Slug,
				Genre:               domainstudio.Genre(strings.TrimSpace(in.Genre)),
				ProductionMode:      domainstudio.ProductionMode(strings.TrimSpace(in.ProductionMode)),
				AspectRatio:         in.AspectRatio,
				StylePrompt:         in.StylePrompt,
				AutoApproveBatches:  in.AutoApproveBatches,
				DefaultTextModel:    strings.TrimSpace(in.DefaultTextModel),
				DefaultImageModel:   strings.TrimSpace(in.DefaultImageModel),
				DefaultVideoModel:   strings.TrimSpace(in.DefaultVideoModel),
				DefaultTTSModel:     strings.TrimSpace(in.DefaultTTSModel),
				InitialEpisodeTitle: in.InitialEpisodeTitle,
			})
			if err != nil {
				return mcpError(err.Error())
			}
			return mcpJSON(res)
		})

	addMCPTool(server, "studio_project_delete",
		"Delete an AI film studio project from DezuxkStudio (and optionally its workspace folder inside ProjectsDir; always requires user approval).",
		func(ctx context.Context, in studioProjectDeleteToolInput) (*mcp.CallToolResult, any, error) {
			if s.studio == nil {
				return mcpError("studio service is unavailable")
			}
			ext, ok := s.studio.(StudioExtendedManager)
			if !ok {
				return mcpError("studio project delete unavailable")
			}
			p := contracts.StudioProjectDeletePayload{
				ID:          strings.TrimSpace(in.ID),
				DeleteFiles: in.DeleteFiles,
			}
			if err := p.Validate(); err != nil {
				return mcpError(err.Error())
			}
			guardedCtx := engine.WithFileGuard(ctx, s.CheckTrustedFile)
			projDetail, err := s.studio.GetProject(guardedCtx, p.ID)
			if err != nil {
				return mcpError(err.Error())
			}
			p.ID = projDetail.Project.ID
			summary := fmt.Sprintf("Delete studio project %q (%s)", projDetail.Project.Title, projDetail.Project.ID)
			if p.DeleteFiles {
				summary += " including workspace files"
			}
			return s.needsApproval(ctx, "studio_project_delete", "", p, "Delete studio project", summary, func(execCtx context.Context) (any, error) {
				if err := ext.DeleteProject(engine.WithFileGuard(execCtx, s.CheckTrustedFile), p); err != nil {
					return nil, err
				}
				return map[string]any{"ok": true, "deleted_id": p.ID}, nil
			})
		})

	addMCPTool(server, "studio_episode_get",
		"Get complete film episode details: project settings, asset bible, storyboard shots, raw/normalized script, and latest export.",
		func(ctx context.Context, in studioEpisodeGetToolInput) (*mcp.CallToolResult, any, error) {
			if s.studio == nil {
				return mcpError("studio service is unavailable")
			}
			res, err := s.studio.GetEpisode(engine.WithFileGuard(ctx, s.CheckTrustedFile), in.EpisodeID)
			if err != nil {
				return mcpError(err.Error())
			}
			return mcpJSON(res)
		})

	addMCPTool(server, "studio_save_script",
		"Save raw screenplay text and/or edited normalized script JSON for an episode, keeping SQLite and disk JSON synchronized.",
		func(ctx context.Context, in studioSaveScriptToolInput) (*mcp.CallToolResult, any, error) {
			if s.studio == nil {
				return mcpError("studio service is unavailable")
			}
			var stagePtr *domainstudio.EpisodeStage
			if in.Stage != nil && strings.TrimSpace(*in.Stage) != "" {
				st := domainstudio.EpisodeStage(strings.TrimSpace(*in.Stage))
				stagePtr = &st
			}
			res, err := s.studio.SaveScript(engine.WithFileGuard(ctx, s.CheckTrustedFile), contracts.StudioEpisodeSaveScriptPayload{
				EpisodeID:        in.EpisodeID,
				RawScript:        in.RawScript,
				NormalizedScript: in.NormalizedScript,
				Stage:            stagePtr,
			})
			if err != nil {
				return mcpError(err.Error())
			}
			return mcpJSON(res)
		})

	addMCPTool(server, "studio_episode_create",
		"Create a new episode inside an existing studio project.",
		func(ctx context.Context, in studioEpisodeCreateToolInput) (*mcp.CallToolResult, any, error) {
			if s.studio == nil {
				return mcpError("studio service is unavailable")
			}
			res, err := s.studio.CreateEpisode(engine.WithFileGuard(ctx, s.CheckTrustedFile), contracts.StudioEpisodeCreatePayload{
				ProjectID: in.ProjectID,
				Code:      in.Code,
				Title:     in.Title,
			})
			if err != nil {
				return mcpError(err.Error())
			}
			return mcpJSON(res)
		})

	addMCPTool(server, "studio_asset_upsert",
		"Create, update, or delete an Asset Bible entry (character, scene, prop with @Tag) and sync asset_bible.json. Deleting an asset requires user approval.",
		func(ctx context.Context, in studioAssetUpsertToolInput) (*mcp.CallToolResult, any, error) {
			if s.studio == nil {
				return mcpError("studio service is unavailable")
			}
			primaryPath := strings.TrimSpace(in.PrimaryPath)
			if !in.Delete && primaryPath != "" && s.policy != nil {
				resolved, verr := s.policy.ValidateRead(primaryPath)
				if verr != nil {
					return mcpError(verr.Error())
				}
				primaryPath = resolved
			}
			p := contracts.StudioAssetUpsertPayload{
				ID:             strings.TrimSpace(in.ID),
				ProjectID:      strings.TrimSpace(in.ProjectID),
				Kind:           domainstudio.AssetKind(strings.TrimSpace(in.Kind)),
				Name:           in.Name,
				CodeTag:        in.CodeTag,
				Description:    in.Description,
				Prompt:         in.Prompt,
				PrimaryAssetID: in.PrimaryAssetID,
				PrimaryPath:    primaryPath,
				VoiceProvider:  in.VoiceProvider,
				VoiceID:        in.VoiceID,
				VoiceStyle:     in.VoiceStyle,
				Delete:         in.Delete,
			}
			if p.Delete {
				if err := p.Validate(); err != nil {
					return mcpError(err.Error())
				}
				return s.needsApproval(ctx, "studio_asset_upsert", "", p, "Delete studio asset",
					fmt.Sprintf("Delete Asset Bible entry %s from project %s", p.ID, p.ProjectID),
					func(execCtx context.Context) (any, error) {
						return s.studio.UpsertAsset(engine.WithFileGuard(execCtx, s.CheckTrustedFile), p)
					})
			}
			res, err := s.studio.UpsertAsset(engine.WithFileGuard(ctx, s.CheckTrustedFile), p)
			if err != nil {
				return mcpError(err.Error())
			}
			return mcpJSON(res)
		})

	addMCPTool(server, "studio_shot_upsert",
		"Create or update a storyboard shot (prompts, @Tag refs, duration, dialogue, narration, or media paths) and sync shots.json.",
		func(ctx context.Context, in studioShotUpsertToolInput) (*mcp.CallToolResult, any, error) {
			if s.studio == nil {
				return mcpError("studio service is unavailable")
			}
			validateOptPath := func(ptr *string) (*string, error) {
				if ptr == nil || strings.TrimSpace(*ptr) == "" {
					return ptr, nil
				}
				if s.policy == nil {
					return ptr, nil
				}
				resolved, verr := s.policy.ValidateRead(*ptr)
				if verr != nil {
					return nil, verr
				}
				return &resolved, nil
			}
			keyframePath, err := validateOptPath(in.KeyframePath)
			if err != nil {
				return mcpError(err.Error())
			}
			videoPath, err := validateOptPath(in.VideoPath)
			if err != nil {
				return mcpError(err.Error())
			}
			audioPath, err := validateOptPath(in.AudioPath)
			if err != nil {
				return mcpError(err.Error())
			}
			var statusPtr *domainstudio.ShotStatus
			if in.Status != nil && strings.TrimSpace(*in.Status) != "" {
				st := domainstudio.ShotStatus(strings.TrimSpace(*in.Status))
				statusPtr = &st
			}
			res, err := s.studio.UpsertShot(engine.WithFileGuard(ctx, s.CheckTrustedFile), contracts.StudioShotUpsertPayload{
				ID:              in.ID,
				EpisodeID:       in.EpisodeID,
				Seq:             in.Seq,
				SceneCode:       in.SceneCode,
				Title:           in.Title,
				ShotMode:        domainstudio.ShotMode(strings.TrimSpace(in.ShotMode)),
				DurationSec:     in.DurationSec,
				ImagePrompt:     in.ImagePrompt,
				VideoPrompt:     in.VideoPrompt,
				NarrationText:   in.NarrationText,
				Dialogues:       in.Dialogues,
				RefAssetTags:    in.RefAssetTags,
				KeyframeAssetID: in.KeyframeAssetID,
				KeyframePath:    keyframePath,
				VideoAssetID:    in.VideoAssetID,
				VideoPath:       videoPath,
				AudioAssetID:    in.AudioAssetID,
				AudioPath:       audioPath,
				Status:          statusPtr,
				ErrorText:       in.ErrorText,
			})
			if err != nil {
				return mcpError(err.Error())
			}
			return mcpJSON(res)
		})

	addMCPTool(server, "studio_sync_workspace",
		"Synchronize studio project.json, asset_bible.json, and shots.json edited on disk back into DezuxkStudio SQLite state.",
		func(ctx context.Context, in studioSyncWorkspaceToolInput) (*mcp.CallToolResult, any, error) {
			if s.studio == nil {
				return mcpError("studio service is unavailable")
			}
			detail, err := s.studio.SyncWorkspace(engine.WithFileGuard(ctx, s.CheckTrustedFile), in.ProjectID)
			if err != nil {
				return mcpError(err.Error())
			}
			if detail == nil {
				return mcpJSON(map[string]any{"ok": true, "synced": 0})
			}
			return mcpJSON(detail)
		})

	addMCPTool(server, "studio_episode_run_stage",
		"Execute a film studio production stage (script_normalize, extract_assets, author_shots, generate_assets, generate_keyframes, generate_videos, generate_tts, probe_shots, concat_video) through shared engine nodes. Batch/generation stages require user approval unless the project enables auto_approve_batches inside trusted folders.",
		func(ctx context.Context, in studioEpisodeRunStageToolInput) (*mcp.CallToolResult, any, error) {
			if s.studio == nil {
				return mcpError("studio service is unavailable")
			}
			p := contracts.StudioEpisodeRunStagePayload{
				EpisodeID:          strings.TrimSpace(in.EpisodeID),
				Stage:              contracts.StudioRunStageAction(strings.TrimSpace(in.Stage)),
				AssetIDs:           in.AssetIDs,
				ShotIDs:            in.ShotIDs,
				Model:              strings.TrimSpace(in.Model),
				Voice:              strings.TrimSpace(in.Voice),
				Resolution:         strings.TrimSpace(in.Resolution),
				FPS:                in.FPS,
				NarrationAudioPath: strings.TrimSpace(in.NarrationAudioPath),
				Force:              in.Force,
			}
			if err := p.Validate(); err != nil {
				return mcpError(err.Error())
			}

			guardedCtx := engine.WithFileGuard(ctx, s.CheckTrustedFile)
			epDetail, err := s.studio.GetEpisode(guardedCtx, p.EpisodeID)
			if err != nil {
				return mcpError(err.Error())
			}

			// Validate any explicit external audio path with the agent file policy.
			if p.NarrationAudioPath != "" && s.policy != nil {
				resolvedAudio, rerr := s.policy.ValidateRead(p.NarrationAudioPath)
				if rerr != nil {
					return mcpError(rerr.Error())
				}
				p.NarrationAudioPath = resolvedAudio
			}

			if p.Stage == contracts.StudioStageProbeShots {
				res, rerr := s.studio.RunStage(guardedCtx, p)
				if rerr != nil {
					return mcpError(rerr.Error())
				}
				return mcpJSON(res)
			}

			prep := s.studioStageTargets(epDetail, p)
			if epDetail.Project.AutoApproveBatches && allFileTargetsTrusted(s.policy, prep) {
				p.Approved = true
				res, rerr := s.studio.RunStage(guardedCtx, p)
				if rerr != nil {
					return mcpError(rerr.Error())
				}
				return mcpJSON(res)
			}

			title := fmt.Sprintf("Run studio stage %s", p.Stage)
			summary := fmt.Sprintf("Run studio stage %q on %s / %s", p.Stage, epDetail.Project.Title, epDetail.Episode.Code)
			if len(p.ShotIDs) > 0 {
				summary = fmt.Sprintf("%s (%d shot(s))", summary, len(p.ShotIDs))
			} else if len(p.AssetIDs) > 0 {
				summary = fmt.Sprintf("%s (%d asset(s))", summary, len(p.AssetIDs))
			}
			return s.needsApproval(ctx, "studio_episode_run_stage", "", p, title, summary, func(execCtx context.Context) (any, error) {
				p.Approved = true
				return s.studio.RunStage(engine.WithFileGuard(execCtx, s.CheckTrustedFile), p)
			})
		})

	addMCPTool(server, "studio_batch_preflight",
		"Verify active provider credentials, pricing, and USD cost estimate before running a batch stage or enqueuing jobs.",
		func(ctx context.Context, in studioBatchPreflightToolInput) (*mcp.CallToolResult, any, error) {
			p2, ok := s.studio.(StudioPhase2Manager)
			if !ok {
				return mcpError("studio phase 2 preflight is unavailable")
			}
			out, err := p2.RunBatchPreflight(engine.WithFileGuard(ctx, s.CheckTrustedFile), contracts.StudioBatchPreflightPayload{
				ProjectID:    strings.TrimSpace(in.ProjectID),
				EpisodeID:    strings.TrimSpace(in.EpisodeID),
				Stage:        strings.TrimSpace(in.Stage),
				Model:        strings.TrimSpace(in.Model),
				ItemCount:    in.ItemCount,
				UnitsPerItem: in.UnitsPerItem,
			})
			if err != nil {
				return mcpError(err.Error())
			}
			return mcpJSON(out)
		})

	addMCPTool(server, "studio_queue_enqueue",
		"Enqueue durable crash-recoverable studio generation jobs into the background worker queue (respecting project approval gate).",
		func(ctx context.Context, in studioQueueEnqueueToolInput) (*mcp.CallToolResult, any, error) {
			if s.studio == nil {
				return mcpError("studio service is unavailable")
			}
			p2, ok := s.studio.(StudioPhase2Manager)
			if !ok {
				return mcpError("studio phase 2 queue is unavailable")
			}
			p := contracts.StudioQueueEnqueuePayload{
				EpisodeID: strings.TrimSpace(in.EpisodeID),
				Stage:     strings.TrimSpace(in.Stage),
				AssetIDs:  in.AssetIDs,
				ShotIDs:   in.ShotIDs,
				Model:     strings.TrimSpace(in.Model),
				Force:     in.Force,
			}
			if err := p.Validate(); err != nil {
				return mcpError(err.Error())
			}
			guardedCtx := engine.WithFileGuard(ctx, s.CheckTrustedFile)
			epDetail, err := s.studio.GetEpisode(guardedCtx, p.EpisodeID)
			if err != nil {
				return mcpError(err.Error())
			}
			prep := s.studioStageTargets(epDetail, contracts.StudioEpisodeRunStagePayload{
				EpisodeID: p.EpisodeID,
				Stage:     contracts.StudioRunStageAction(p.Stage),
				AssetIDs:  p.AssetIDs,
				ShotIDs:   p.ShotIDs,
			})
			if epDetail.Project.AutoApproveBatches && allFileTargetsTrusted(s.policy, prep) {
				p.Approved = true
				res, qerr := p2.EnqueueBatch(guardedCtx, p)
				if qerr != nil {
					return mcpError(qerr.Error())
				}
				return mcpJSON(res)
			}
			title := fmt.Sprintf("Enqueue durable batch %s", p.Stage)
			summary := fmt.Sprintf("Enqueue durable %q jobs on %s / %s", p.Stage, epDetail.Project.Title, epDetail.Episode.Code)
			return s.needsApproval(ctx, "studio_queue_enqueue", "", p, title, summary, func(execCtx context.Context) (any, error) {
				p.Approved = true
				return p2.EnqueueBatch(engine.WithFileGuard(execCtx, s.CheckTrustedFile), p)
			})
		})

	addMCPTool(server, "studio_queue_list",
		"List durable studio queue jobs filtered by project, episode, or status.",
		func(ctx context.Context, in studioQueueListToolInput) (*mcp.CallToolResult, any, error) {
			p2, ok := s.studio.(StudioPhase2Manager)
			if !ok {
				return mcpError("studio phase 2 queue is unavailable")
			}
			res, err := p2.ListJobs(engine.WithFileGuard(ctx, s.CheckTrustedFile), contracts.StudioQueueListPayload{
				ProjectID: strings.TrimSpace(in.ProjectID),
				EpisodeID: strings.TrimSpace(in.EpisodeID),
				Status:    domainstudio.JobStatus(strings.TrimSpace(in.Status)),
				Limit:     in.Limit,
			})
			if err != nil {
				return mcpError(err.Error())
			}
			return mcpJSON(res)
		})

	addMCPTool(server, "studio_queue_control",
		"Pause, resume, cancel, or retry durable studio queue jobs without duplicating already-submitted remote video tasks.",
		func(ctx context.Context, in studioQueueControlToolInput) (*mcp.CallToolResult, any, error) {
			if s.studio == nil {
				return mcpError("studio service is unavailable")
			}
			p2, ok := s.studio.(StudioPhase2Manager)
			if !ok {
				return mcpError("studio phase 2 queue is unavailable")
			}
			p := contracts.StudioQueueControlPayload{
				Action:    contracts.StudioQueueAction(strings.TrimSpace(in.Action)),
				ProjectID: strings.TrimSpace(in.ProjectID),
				EpisodeID: strings.TrimSpace(in.EpisodeID),
				JobID:     strings.TrimSpace(in.JobID),
			}
			if err := p.Validate(); err != nil {
				return mcpError(err.Error())
			}
			guardedCtx := engine.WithFileGuard(ctx, s.CheckTrustedFile)
			if strings.EqualFold(string(p.Action), string(contracts.QueueActionRetry)) {
				var (
					proj    contracts.StudioProject
					hasProj bool
				)
				if p.ProjectID != "" {
					if pd, err := s.studio.GetProject(guardedCtx, p.ProjectID); err == nil {
						proj = pd.Project
						hasProj = true
					}
				} else if p.EpisodeID != "" {
					if ep, err := s.studio.GetEpisode(guardedCtx, p.EpisodeID); err == nil {
						proj = ep.Project
						hasProj = true
					}
				} else if p.JobID != "" {
					if jobs, err := p2.ListJobs(guardedCtx, contracts.StudioQueueListPayload{}); err == nil {
						for _, j := range jobs.Jobs {
							if j.ID == p.JobID && j.ProjectID != "" {
								if pd, perr := s.studio.GetProject(guardedCtx, j.ProjectID); perr == nil {
									proj = pd.Project
									hasProj = true
								}
								break
							}
						}
					}
				}
				if !hasProj || !proj.AutoApproveBatches || !allFileTargetsTrusted(s.policy, s.studioProjectPrep(proj)) {
					targetLabel := p.JobID
					if targetLabel == "" {
						targetLabel = p.EpisodeID
					}
					if targetLabel == "" {
						targetLabel = p.ProjectID
					}
					return s.needsApproval(ctx, "studio_queue_control", "", p, "Retry studio queue jobs",
						fmt.Sprintf("Retry failed/canceled studio queue jobs (%s)", targetLabel),
						func(execCtx context.Context) (any, error) {
							return p2.ControlQueue(engine.WithFileGuard(execCtx, s.CheckTrustedFile), p)
						})
				}
			}
			res, err := p2.ControlQueue(guardedCtx, p)
			if err != nil {
				return mcpError(err.Error())
			}
			return mcpJSON(res)
		})

	addMCPTool(server, "studio_cost_summary",
		"Get cumulative USD cost totals, per-capability breakdown, ledger rows, and unit pricing for a studio project or episode.",
		func(ctx context.Context, in studioCostSummaryToolInput) (*mcp.CallToolResult, any, error) {
			p2, ok := s.studio.(StudioPhase2Manager)
			if !ok {
				return mcpError("studio billing ledger is unavailable")
			}
			res, err := p2.GetCostSummary(engine.WithFileGuard(ctx, s.CheckTrustedFile), contracts.StudioCostSummaryPayload{
				ProjectID: strings.TrimSpace(in.ProjectID),
				EpisodeID: strings.TrimSpace(in.EpisodeID),
				Limit:     in.Limit,
			})
			if err != nil {
				return mcpError(err.Error())
			}
			return mcpJSON(res)
		})

	addMCPTool(server, "studio_versions_list",
		"List version history (v1, v2, ...) and latest QC report for an asset, keyframe, video, or audio slot.",
		func(ctx context.Context, in studioVersionsListToolInput) (*mcp.CallToolResult, any, error) {
			p2, ok := s.studio.(StudioPhase2Manager)
			if !ok {
				return mcpError("studio media versions unavailable")
			}
			res, err := p2.ListVersions(engine.WithFileGuard(ctx, s.CheckTrustedFile), contracts.StudioVersionsListPayload{
				TargetKind: domainstudio.MediaTargetKind(strings.TrimSpace(in.TargetKind)),
				TargetID:   strings.TrimSpace(in.TargetID),
			})
			if err != nil {
				return mcpError(err.Error())
			}
			return mcpJSON(res)
		})

	addMCPTool(server, "studio_version_select",
		"Switch the active selected output version for an asset or shot slot.",
		func(ctx context.Context, in studioVersionSelectToolInput) (*mcp.CallToolResult, any, error) {
			p2, ok := s.studio.(StudioPhase2Manager)
			if !ok {
				return mcpError("studio media versions unavailable")
			}
			res, err := p2.SelectVersion(engine.WithFileGuard(ctx, s.CheckTrustedFile), contracts.StudioVersionSelectPayload{
				VersionID: strings.TrimSpace(in.VersionID),
			})
			if err != nil {
				return mcpError(err.Error())
			}
			return mcpJSON(res)
		})

	addMCPTool(server, "studio_qc_review",
		"Inspect an image or MP4 clip with FFprobe + sampled FFmpeg frames + multimodal Vision critique and record the QC report.",
		func(ctx context.Context, in studioQCReviewToolInput) (*mcp.CallToolResult, any, error) {
			if s.studio == nil {
				return mcpError("studio service is unavailable")
			}
			p2, ok := s.studio.(StudioPhase2Manager)
			if !ok {
				return mcpError("studio QC review unavailable")
			}
			mediaPath := strings.TrimSpace(in.MediaPath)
			if mediaPath != "" && s.policy != nil {
				resolved, verr := s.policy.ValidateRead(mediaPath)
				if verr != nil {
					return mcpError(verr.Error())
				}
				mediaPath = resolved
			}
			refPaths := make([]string, 0, len(in.ReferencePaths))
			for _, rp := range in.ReferencePaths {
				trimmed := strings.TrimSpace(rp)
				if trimmed == "" {
					continue
				}
				if s.policy != nil {
					resolved, verr := s.policy.ValidateRead(trimmed)
					if verr != nil {
						return mcpError(verr.Error())
					}
					trimmed = resolved
				}
				refPaths = append(refPaths, trimmed)
			}
			p := contracts.StudioQCReviewPayload{
				ProjectID:      strings.TrimSpace(in.ProjectID),
				EpisodeID:      strings.TrimSpace(in.EpisodeID),
				TargetKind:     domainstudio.MediaTargetKind(strings.TrimSpace(in.TargetKind)),
				TargetID:       strings.TrimSpace(in.TargetID),
				MediaVersionID: strings.TrimSpace(in.MediaVersionID),
				MediaPath:      mediaPath,
				ReferencePaths: refPaths,
				PromptExpected: strings.TrimSpace(in.PromptExpected),
				VisionModelRef: strings.TrimSpace(in.VisionModelRef),
				SampleCount:    in.SampleCount,
				SkipVision:     in.SkipVision,
			}
			if err := p.Validate(); err != nil {
				return mcpError(err.Error())
			}
			guardedCtx := engine.WithFileGuard(ctx, s.CheckTrustedFile)
			if !p.SkipVision {
				projDetail, err := s.studio.GetProject(guardedCtx, p.ProjectID)
				if err != nil {
					return mcpError(err.Error())
				}
				allFiles := append([]string{p.MediaPath}, p.ReferencePaths...)
				prep := s.studioProjectPrep(projDetail.Project, allFiles...)
				if !projDetail.Project.AutoApproveBatches || !allFileTargetsTrusted(s.policy, prep) {
					return s.needsApproval(ctx, "studio_qc_review", "", p, "Run visual QC review",
						fmt.Sprintf("Run multimodal Vision QC review on %s", filepath.Base(p.MediaPath)),
						func(execCtx context.Context) (any, error) {
							return p2.RunQCReview(engine.WithFileGuard(execCtx, s.CheckTrustedFile), p)
						})
				}
			}
			res, err := p2.RunQCReview(guardedCtx, p)
			if err != nil {
				return mcpError(err.Error())
			}
			return mcpJSON(res)
		})

	addMCPTool(server, "studio_timeline_get",
		"Get an episode's multi-track EditTimeline JSON, current revision number, Undo/Redo state, and recent revision summaries.",
		func(ctx context.Context, in studioTimelineGetToolInput) (*mcp.CallToolResult, any, error) {
			p3, ok := s.studio.(StudioPhase3Manager)
			if !ok {
				return mcpError("studio timeline management unavailable")
			}
			res, err := p3.GetTimeline(engine.WithFileGuard(ctx, s.CheckTrustedFile), strings.TrimSpace(in.EpisodeID))
			if err != nil {
				return mcpError(err.Error())
			}
			return mcpJSON(res)
		})

	addMCPTool(server, "studio_timeline_apply_ops",
		"Apply atomic EditTimeline mutations (add/move/split/trim/remove clip, update track mute/solo/volume, edit Vietnamese subtitle cues, add markers) while enforcing expected_revision concurrency.",
		func(ctx context.Context, in studioTimelineApplyOpsToolInput) (*mcp.CallToolResult, any, error) {
			p3, ok := s.studio.(StudioPhase3Manager)
			if !ok {
				return mcpError("studio timeline management unavailable")
			}
			ops, err := s.validateAndConvertTimelineOpInputs(in.Ops)
			if err != nil {
				return mcpError(err.Error())
			}
			res, err := p3.ApplyTimelineOps(engine.WithFileGuard(ctx, s.CheckTrustedFile), contracts.StudioTimelineApplyOpsPayload{
				EpisodeID:        strings.TrimSpace(in.EpisodeID),
				ExpectedRevision: in.ExpectedRevision,
				Actor:            domainstudio.ActorAgent,
				Summary:          strings.TrimSpace(in.Summary),
				Ops:              ops,
			})
			if err != nil {
				return mcpError(err.Error())
			}
			return mcpJSON(res)
		})

	addMCPTool(server, "studio_timeline_undo",
		"Undo the latest saved EditTimeline revision for an episode while enforcing expected_revision.",
		func(ctx context.Context, in studioTimelineRevisionNavToolInput) (*mcp.CallToolResult, any, error) {
			p3, ok := s.studio.(StudioPhase3Manager)
			if !ok {
				return mcpError("studio timeline management unavailable")
			}
			res, err := p3.UndoTimeline(engine.WithFileGuard(ctx, s.CheckTrustedFile), contracts.StudioTimelineRevisionNavPayload{
				EpisodeID:        strings.TrimSpace(in.EpisodeID),
				ExpectedRevision: in.ExpectedRevision,
			})
			if err != nil {
				return mcpError(err.Error())
			}
			return mcpJSON(res)
		})

	addMCPTool(server, "studio_timeline_redo",
		"Redo the next undone EditTimeline revision for an episode while enforcing expected_revision.",
		func(ctx context.Context, in studioTimelineRevisionNavToolInput) (*mcp.CallToolResult, any, error) {
			p3, ok := s.studio.(StudioPhase3Manager)
			if !ok {
				return mcpError("studio timeline management unavailable")
			}
			res, err := p3.RedoTimeline(engine.WithFileGuard(ctx, s.CheckTrustedFile), contracts.StudioTimelineRevisionNavPayload{
				EpisodeID:        strings.TrimSpace(in.EpisodeID),
				ExpectedRevision: in.ExpectedRevision,
			})
			if err != nil {
				return mcpError(err.Error())
			}
			return mcpJSON(res)
		})

	addMCPTool(server, "studio_project_update",
		"Update AI film studio project metadata, visual style bible prompt, or default model selections. Enabling auto_approve_batches on an existing project requires user approval.",
		func(ctx context.Context, in studioProjectUpdateToolInput) (*mcp.CallToolResult, any, error) {
			if s.studio == nil {
				return mcpError("studio service is unavailable")
			}
			ext, ok := s.studio.(StudioExtendedManager)
			if !ok {
				return mcpError("studio project updates unavailable")
			}
			var genrePtr *domainstudio.Genre
			if in.Genre != nil {
				g := domainstudio.Genre(strings.TrimSpace(*in.Genre))
				genrePtr = &g
			}
			var modePtr *domainstudio.ProductionMode
			if in.ProductionMode != nil {
				m := domainstudio.ProductionMode(strings.TrimSpace(*in.ProductionMode))
				modePtr = &m
			}
			p := contracts.StudioProjectUpdatePayload{
				ID:                 strings.TrimSpace(in.ID),
				Title:              in.Title,
				Genre:              genrePtr,
				ProductionMode:     modePtr,
				AspectRatio:        in.AspectRatio,
				StylePrompt:        in.StylePrompt,
				AutoApproveBatches: in.AutoApproveBatches,
				DefaultTextModel:   in.DefaultTextModel,
				DefaultImageModel:  in.DefaultImageModel,
				DefaultVideoModel:  in.DefaultVideoModel,
				DefaultTTSModel:    in.DefaultTTSModel,
			}
			if err := p.Validate(); err != nil {
				return mcpError(err.Error())
			}
			guardedCtx := engine.WithFileGuard(ctx, s.CheckTrustedFile)
			curProj, err := s.studio.GetProject(guardedCtx, p.ID)
			if err != nil {
				return mcpError(err.Error())
			}
			p.ID = curProj.Project.ID
			if p.AutoApproveBatches != nil && *p.AutoApproveBatches && !curProj.Project.AutoApproveBatches {
				return s.needsApproval(ctx, "studio_project_update", "", p, "Enable studio batch auto-approval",
					fmt.Sprintf("Enable auto_approve_batches on studio project %q", curProj.Project.Title),
					func(execCtx context.Context) (any, error) {
						return ext.UpdateProject(engine.WithFileGuard(execCtx, s.CheckTrustedFile), p)
					})
			}
			res, err := ext.UpdateProject(guardedCtx, p)
			if err != nil {
				return mcpError(err.Error())
			}
			return mcpJSON(res)
		})

	addMCPTool(server, "studio_shot_reorder",
		"Reorder storyboard shots in an episode and persist storyboard/shots.json.",
		func(ctx context.Context, in studioShotReorderToolInput) (*mcp.CallToolResult, any, error) {
			ext, ok := s.studio.(StudioExtendedManager)
			if !ok {
				return mcpError("studio shot reorder unavailable")
			}
			res, err := ext.ReorderShots(engine.WithFileGuard(ctx, s.CheckTrustedFile), contracts.StudioShotReorderPayload{
				EpisodeID: strings.TrimSpace(in.EpisodeID),
				ShotIDs:   in.ShotIDs,
			})
			if err != nil {
				return mcpError(err.Error())
			}
			return mcpJSON(res)
		})

	addMCPTool(server, "studio_pricing_upsert",
		"Upsert a provider/model unit rate rule in studio_pricing_catalog for cost estimates (requires user approval).",
		func(ctx context.Context, in studioPricingUpsertToolInput) (*mcp.CallToolResult, any, error) {
			ext, ok := s.studio.(StudioExtendedManager)
			if !ok {
				return mcpError("studio pricing catalog updates unavailable")
			}
			p := contracts.StudioPricingUpsertPayload{
				ProviderID:   strings.TrimSpace(in.ProviderID),
				ModelID:      strings.TrimSpace(in.ModelID),
				Capability:   strings.TrimSpace(in.Capability),
				Unit:         strings.TrimSpace(in.Unit),
				UnitPriceUSD: in.UnitPriceUSD,
			}
			if err := p.Validate(); err != nil {
				return mcpError(err.Error())
			}
			return s.needsApproval(ctx, "studio_pricing_upsert", "", p, "Update studio pricing catalog",
				fmt.Sprintf("Set %s/%s (%s) pricing rate to $%.4f per %s", p.ProviderID, p.ModelID, p.Capability, p.UnitPriceUSD, p.Unit),
				func(execCtx context.Context) (any, error) {
					return ext.UpsertPricing(engine.WithFileGuard(execCtx, s.CheckTrustedFile), p)
				})
		})

	addMCPTool(server, "studio_grid_split",
		"Split a multi-panel grid image into individual frame files and record them in media version history.",
		func(ctx context.Context, in studioGridSplitToolInput) (*mcp.CallToolResult, any, error) {
			ext, ok := s.studio.(StudioExtendedManager)
			if !ok {
				return mcpError("studio grid split unavailable")
			}
			imgPath := strings.TrimSpace(in.ImagePath)
			if imgPath != "" && s.policy != nil {
				resolved, err := s.policy.ValidateRead(imgPath)
				if err != nil {
					return mcpError(err.Error())
				}
				imgPath = resolved
			}
			res, err := ext.SplitGrid(engine.WithFileGuard(ctx, s.CheckTrustedFile), contracts.StudioGridSplitPayload{
				ProjectID:  strings.TrimSpace(in.ProjectID),
				EpisodeID:  strings.TrimSpace(in.EpisodeID),
				TargetKind: domainstudio.MediaTargetKind(strings.TrimSpace(in.TargetKind)),
				TargetID:   strings.TrimSpace(in.TargetID),
				ImagePath:  imgPath,
				Rows:       in.Rows,
				Cols:       in.Cols,
				MarginPx:   in.MarginPx,
				GutterPx:   in.GutterPx,
				RowWeights: in.RowWeights,
				ColWeights: in.ColWeights,
			})
			if err != nil {
				return mcpError(err.Error())
			}
			return mcpJSON(res)
		})

	addMCPTool(server, "studio_mask_edit",
		"Apply targeted mask inpainting to a keyframe or asset reference sheet and record the repaired output version.",
		func(ctx context.Context, in studioMaskEditToolInput) (*mcp.CallToolResult, any, error) {
			if s.studio == nil {
				return mcpError("studio service is unavailable")
			}
			ext, ok := s.studio.(StudioExtendedManager)
			if !ok {
				return mcpError("studio mask edit unavailable")
			}
			imgPath := strings.TrimSpace(in.ImagePath)
			if imgPath != "" && s.policy != nil {
				resolved, err := s.policy.ValidateRead(imgPath)
				if err != nil {
					return mcpError(err.Error())
				}
				imgPath = resolved
			}
			maskPath := strings.TrimSpace(in.MaskPath)
			if maskPath != "" && s.policy != nil {
				resolved, err := s.policy.ValidateRead(maskPath)
				if err != nil {
					return mcpError(err.Error())
				}
				maskPath = resolved
			}
			p := contracts.StudioMaskEditPayload{
				ProjectID:      strings.TrimSpace(in.ProjectID),
				EpisodeID:      strings.TrimSpace(in.EpisodeID),
				TargetKind:     domainstudio.MediaTargetKind(strings.TrimSpace(in.TargetKind)),
				TargetID:       strings.TrimSpace(in.TargetID),
				ImagePath:      imgPath,
				MaskPath:       maskPath,
				MaskDataBase64: strings.TrimSpace(in.MaskDataBase64),
				Prompt:         strings.TrimSpace(in.Prompt),
				ModelRef:       strings.TrimSpace(in.ModelRef),
				AspectRatio:    strings.TrimSpace(in.AspectRatio),
				Resolution:     strings.TrimSpace(in.Resolution),
			}
			if err := p.Validate(); err != nil {
				return mcpError(err.Error())
			}
			guardedCtx := engine.WithFileGuard(ctx, s.CheckTrustedFile)
			projDetail, err := s.studio.GetProject(guardedCtx, p.ProjectID)
			if err != nil {
				return mcpError(err.Error())
			}
			prep := s.studioProjectPrep(projDetail.Project, p.ImagePath, p.MaskPath)
			if projDetail.Project.AutoApproveBatches && allFileTargetsTrusted(s.policy, prep) {
				res, rerr := ext.ApplyMaskEdit(guardedCtx, p)
				if rerr != nil {
					return mcpError(rerr.Error())
				}
				return mcpJSON(res)
			}
			return s.needsApproval(ctx, "studio_mask_edit", "", p, "Run studio mask edit", fmt.Sprintf("Inpaint masked area of %s", filepath.Base(imgPath)), func(execCtx context.Context) (any, error) {
				return ext.ApplyMaskEdit(engine.WithFileGuard(execCtx, s.CheckTrustedFile), p)
			})
		})

	addMCPTool(server, "studio_extract_last_frame",
		"Extract the final frame of a shot's video for shot-to-shot visual continuity and optionally bind it to the next shot's keyframe.",
		func(ctx context.Context, in studioExtractLastFrameToolInput) (*mcp.CallToolResult, any, error) {
			ext, ok := s.studio.(StudioExtendedManager)
			if !ok {
				return mcpError("studio extract last frame unavailable")
			}
			res, err := ext.ExtractLastFrame(engine.WithFileGuard(ctx, s.CheckTrustedFile), contracts.StudioExtractLastFramePayload{
				EpisodeID:    strings.TrimSpace(in.EpisodeID),
				SourceShotID: strings.TrimSpace(in.SourceShotID),
				TargetShotID: strings.TrimSpace(in.TargetShotID),
			})
			if err != nil {
				return mcpError(err.Error())
			}
			return mcpJSON(res)
		})

	addMCPTool(server, "studio_timeline_auto_assemble",
		"Auto-assemble ready episode shots, speech audio, subtitles, transitions, and optional BGM into a canonical multi-track EditTimeline.",
		func(ctx context.Context, in studioTimelineAutoAssembleToolInput) (*mcp.CallToolResult, any, error) {
			if s.studio == nil {
				return mcpError("studio service is unavailable")
			}
			ext, ok := s.studio.(StudioExtendedManager)
			if !ok {
				return mcpError("studio timeline auto-assemble unavailable")
			}
			bgmPath := strings.TrimSpace(in.BGMPath)
			if bgmPath != "" && s.policy != nil {
				resolved, err := s.policy.ValidateRead(bgmPath)
				if err != nil {
					return mcpError(err.Error())
				}
				bgmPath = resolved
			}
			p := contracts.StudioTimelineAutoAssemblePayload{
				EpisodeID:         strings.TrimSpace(in.EpisodeID),
				ExpectedRevision:  in.ExpectedRevision,
				Resolution:        strings.TrimSpace(in.Resolution),
				FPS:               in.FPS,
				TransitionDefault: domainstudio.TransitionType(strings.TrimSpace(in.TransitionDefault)),
				TransitionMs:      in.TransitionMs,
				BGMPath:           bgmPath,
				BGMVolume:         in.BGMVolume,
			}
			if err := p.Validate(); err != nil {
				return mcpError(err.Error())
			}
			guardedCtx := engine.WithFileGuard(ctx, s.CheckTrustedFile)
			epDetail, err := s.studio.GetEpisode(guardedCtx, p.EpisodeID)
			if err != nil {
				return mcpError(err.Error())
			}
			hasExistingTimeline := false
			if p3, ok := s.studio.(StudioPhase3Manager); ok {
				if curTL, tlErr := p3.GetTimeline(guardedCtx, p.EpisodeID); tlErr == nil && curTL.Revision > 0 {
					for _, tr := range curTL.Timeline.Tracks {
						if len(tr.Clips) > 0 {
							hasExistingTimeline = true
							break
						}
					}
					if len(curTL.Timeline.Subtitles) > 0 || len(curTL.Timeline.Markers) > 0 {
						hasExistingTimeline = true
					}
				}
			}
			if hasExistingTimeline {
				prep := s.studioProjectPrep(epDetail.Project, bgmPath)
				if !epDetail.Project.AutoApproveBatches || !allFileTargetsTrusted(s.policy, prep) {
					return s.needsApproval(ctx, "studio_timeline_auto_assemble", "", p, "Replace existing studio timeline",
						fmt.Sprintf("Auto-assemble and replace existing timeline on %s / %s", epDetail.Project.Title, epDetail.Episode.Code),
						func(execCtx context.Context) (any, error) {
							return ext.AutoAssembleTimeline(engine.WithFileGuard(execCtx, s.CheckTrustedFile), p)
						})
				}
			}
			res, err := ext.AutoAssembleTimeline(guardedCtx, p)
			if err != nil {
				return mcpError(err.Error())
			}
			return mcpJSON(res)
		})

	addMCPTool(server, "studio_media_peaks_thumbs",
		"Extract audio waveform RMS peaks and thumbnail sprite strips for a media clip on the timeline.",
		func(ctx context.Context, in studioMediaPeaksThumbsToolInput) (*mcp.CallToolResult, any, error) {
			ext, ok := s.studio.(StudioExtendedManager)
			if !ok {
				return mcpError("studio media peaks & thumbs unavailable")
			}
			mediaPath := strings.TrimSpace(in.MediaPath)
			if mediaPath != "" && s.policy != nil {
				resolved, err := s.policy.ValidateRead(mediaPath)
				if err != nil {
					return mcpError(err.Error())
				}
				mediaPath = resolved
			}
			res, err := ext.ComputeMediaPeaksThumbs(engine.WithFileGuard(ctx, s.CheckTrustedFile), contracts.StudioMediaPeaksThumbsPayload{
				EpisodeID:        strings.TrimSpace(in.EpisodeID),
				MediaPath:        mediaPath,
				ClipID:           strings.TrimSpace(in.ClipID),
				ThumbIntervalSec: in.ThumbIntervalSec,
				PeakBins:         in.PeakBins,
			})
			if err != nil {
				return mcpError(err.Error())
			}
			return mcpJSON(res)
		})

	addMCPTool(server, "studio_timeline_render",
		"Render an episode's multi-track EditTimeline to a mastered MP4 with transitions, loudness normalization, and UTF-8 SRT/ASS subtitles.",
		func(ctx context.Context, in studioTimelineRenderToolInput) (*mcp.CallToolResult, any, error) {
			if s.studio == nil {
				return mcpError("studio service is unavailable")
			}
			ext, ok := s.studio.(StudioExtendedManager)
			if !ok {
				return mcpError("studio timeline render unavailable")
			}
			p := contracts.StudioTimelineRenderPayload{
				EpisodeID:        strings.TrimSpace(in.EpisodeID),
				Filename:         strings.TrimSpace(in.Filename),
				BurnSubtitles:    in.BurnSubtitles,
				SubtitleFontSize: in.SubtitleFontSize,
				LoudnormLUFS:     in.LoudnormLUFS,
				CRF:              in.CRF,
				Preset:           strings.TrimSpace(in.Preset),
				Overwrite:        in.Overwrite,
			}
			if err := p.Validate(); err != nil {
				return mcpError(err.Error())
			}
			guardedCtx := engine.WithFileGuard(ctx, s.CheckTrustedFile)
			epDetail, err := s.studio.GetEpisode(guardedCtx, p.EpisodeID)
			if err != nil {
				return mcpError(err.Error())
			}
			prep := s.studioProjectPrep(epDetail.Project)
			if !p.Overwrite && epDetail.Project.AutoApproveBatches && allFileTargetsTrusted(s.policy, prep) {
				res, rerr := ext.RenderTimeline(guardedCtx, p)
				if rerr != nil {
					return mcpError(rerr.Error())
				}
				return mcpJSON(res)
			}
			summary := fmt.Sprintf("Render timeline MP4 for %s / %s", epDetail.Project.Title, epDetail.Episode.Code)
			if p.Overwrite {
				summary += " (overwriting existing file)"
			}
			return s.needsApproval(ctx, "studio_timeline_render", "", p, "Render studio timeline MP4", summary, func(execCtx context.Context) (any, error) {
				return ext.RenderTimeline(engine.WithFileGuard(execCtx, s.CheckTrustedFile), p)
			})
		})

	addMCPTool(server, "studio_timeline_export_nle",
		"Export an episode's EditTimeline into CapCut/Jianying draft zip, FCPXML, or OpenTimelineIO format.",
		func(ctx context.Context, in studioTimelineExportNLEToolInput) (*mcp.CallToolResult, any, error) {
			if s.studio == nil {
				return mcpError("studio service is unavailable")
			}
			ext, ok := s.studio.(StudioExtendedManager)
			if !ok {
				return mcpError("studio NLE export unavailable")
			}
			p := contracts.StudioTimelineExportNLEPayload{
				EpisodeID:        strings.TrimSpace(in.EpisodeID),
				Format:           domainstudio.NLEExportFormat(strings.TrimSpace(in.Format)),
				ProjectName:      strings.TrimSpace(in.ProjectName),
				IncludeMediaCopy: in.IncludeMediaCopy,
			}
			if err := p.Validate(); err != nil {
				return mcpError(err.Error())
			}
			guardedCtx := engine.WithFileGuard(ctx, s.CheckTrustedFile)
			epDetail, err := s.studio.GetEpisode(guardedCtx, p.EpisodeID)
			if err != nil {
				return mcpError(err.Error())
			}
			prep := s.studioProjectPrep(epDetail.Project)
			if epDetail.Project.AutoApproveBatches && allFileTargetsTrusted(s.policy, prep) {
				res, eerr := ext.ExportNLEDraft(guardedCtx, p)
				if eerr != nil {
					return mcpError(eerr.Error())
				}
				return mcpJSON(res)
			}
			return s.needsApproval(ctx, "studio_timeline_export_nle", "", p, "Export studio NLE draft",
				fmt.Sprintf("Export %s draft for %s / %s", p.Format, epDetail.Project.Title, epDetail.Episode.Code),
				func(execCtx context.Context) (any, error) {
					return ext.ExportNLEDraft(engine.WithFileGuard(execCtx, s.CheckTrustedFile), p)
				})
		})
}

func (s *Service) validateAndConvertTimelineOpInputs(in []studioTimelineOpToolInput) ([]contracts.StudioTimelineOp, error) {
	convClip := func(c *studioTimelineClipToolInput) (*contracts.StudioTimelineClip, error) {
		if c == nil {
			return nil, nil
		}
		fp := strings.TrimSpace(c.FilePath)
		if fp != "" && s.policy != nil {
			resolved, err := s.policy.ValidateRead(fp)
			if err != nil {
				return nil, err
			}
			fp = resolved
		}
		sp := strings.TrimSpace(c.SpritePath)
		if sp != "" && s.policy != nil {
			resolved, err := s.policy.ValidateRead(sp)
			if err != nil {
				return nil, err
			}
			sp = resolved
		}
		return &contracts.StudioTimelineClip{
			ID:              c.ID,
			ShotID:          c.ShotID,
			AssetID:         c.AssetID,
			Label:           c.Label,
			FilePath:        fp,
			TimelineStartMs: c.TimelineStartMs,
			SourceInMs:      c.SourceInMs,
			SourceOutMs:     c.SourceOutMs,
			Speed:           c.Speed,
			Volume:          c.Volume,
			Mute:            c.Mute,
			TransitionIn:    domainstudio.TransitionType(c.TransitionIn),
			TransitionMs:    c.TransitionMs,
			KenBurnsPreset:  domainstudio.KenBurnsPreset(c.KenBurnsPreset),
			SpritePath:      sp,
			SpriteAssetID:   c.SpriteAssetID,
		}, nil
	}
	out := make([]contracts.StudioTimelineOp, len(in))
	for i, op := range in {
		clipDTO, err := convClip(op.Clip)
		if err != nil {
			return nil, err
		}
		var tlDTO *contracts.StudioEditTimeline
		if op.Timeline != nil {
			tracks := make([]contracts.StudioTimelineTrack, len(op.Timeline.Tracks))
			for ti, tr := range op.Timeline.Tracks {
				clips := make([]contracts.StudioTimelineClip, len(tr.Clips))
				for ci := range tr.Clips {
					c, cerr := convClip(&tr.Clips[ci])
					if cerr != nil {
						return nil, cerr
					}
					clips[ci] = *c
				}
				tracks[ti] = contracts.StudioTimelineTrack{
					ID:     tr.ID,
					Name:   tr.Name,
					Kind:   domainstudio.TrackKind(tr.Kind),
					Muted:  tr.Muted,
					Solo:   tr.Solo,
					Locked: tr.Locked,
					Volume: tr.Volume,
					Clips:  clips,
				}
			}
			subs := make([]contracts.StudioSubtitleCue, len(op.Timeline.Subtitles))
			for si, sub := range op.Timeline.Subtitles {
				subs[si] = contracts.StudioSubtitleCue{
					ID:          sub.ID,
					StartMs:     sub.StartMs,
					EndMs:       sub.EndMs,
					Speaker:     sub.Speaker,
					Text:        sub.Text,
					StylePreset: sub.StylePreset,
				}
			}
			tlDTO = &contracts.StudioEditTimeline{
				Version:   op.Timeline.Version,
				FPS:       op.Timeline.FPS,
				Width:     op.Timeline.Width,
				Height:    op.Timeline.Height,
				Tracks:    tracks,
				Subtitles: subs,
				Markers:   op.Timeline.Markers,
			}
		}
		var subDTO *contracts.StudioSubtitleCue
		if op.Subtitle != nil {
			subDTO = &contracts.StudioSubtitleCue{
				ID:          op.Subtitle.ID,
				StartMs:     op.Subtitle.StartMs,
				EndMs:       op.Subtitle.EndMs,
				Speaker:     op.Subtitle.Speaker,
				Text:        op.Subtitle.Text,
				StylePreset: op.Subtitle.StylePreset,
			}
		}
		out[i] = contracts.StudioTimelineOp{
			Op:              domainstudio.TimelineOpKind(op.Op),
			Timeline:        tlDTO,
			FPS:             op.FPS,
			Width:           op.Width,
			Height:          op.Height,
			TrackID:         op.TrackID,
			TargetTrackID:   op.TargetTrackID,
			TrackKind:       op.TrackKind,
			TrackName:       op.TrackName,
			Muted:           op.Muted,
			Solo:            op.Solo,
			Locked:          op.Locked,
			Volume:          op.Volume,
			ClipID:          op.ClipID,
			NewClipID:       op.NewClipID,
			Clip:            clipDTO,
			TimelineStartMs: op.TimelineStartMs,
			SourceInMs:      op.SourceInMs,
			SourceOutMs:     op.SourceOutMs,
			Speed:           op.Speed,
			TransitionIn:    op.TransitionIn,
			TransitionMs:    op.TransitionMs,
			KenBurnsPreset:  op.KenBurnsPreset,
			Label:           op.Label,
			SplitAtMs:       op.SplitAtMs,
			SubtitleID:      op.SubtitleID,
			Subtitle:        subDTO,
			MarkerID:        op.MarkerID,
			Marker:          op.Marker,
		}
	}
	return out, nil
}

func (s *Service) studioProjectsDir() string {
	if s.studio != nil {
		if ext, ok := s.studio.(interface{ ProjectsDir() string }); ok {
			if dir := strings.TrimSpace(ext.ProjectsDir()); dir != "" {
				return dir
			}
		}
	}
	if s.policy != nil {
		if dir := strings.TrimSpace(s.policy.ProjectsDir()); dir != "" {
			return dir
		}
	}
	return ""
}

func (s *Service) studioProjectPrep(proj contracts.StudioProject, files ...string) preparedNodeInput {
	var prep preparedNodeInput
	if root := strings.TrimSpace(proj.RootDir); root != "" {
		prep.OutputDirs = append(prep.OutputDirs, root)
	} else if pDir := s.studioProjectsDir(); pDir != "" {
		prep.OutputDirs = append(prep.OutputDirs, pDir)
	}
	for _, f := range files {
		if trimmed := strings.TrimSpace(f); trimmed != "" {
			prep.Files = append(prep.Files, trimmed)
		}
	}
	return prep
}

func (s *Service) studioStageTargets(ep contracts.StudioEpisodeDetailResult, p contracts.StudioEpisodeRunStagePayload) preparedNodeInput {
	prep := s.studioProjectPrep(ep.Project, p.NarrationAudioPath)
	wantAssets := make(map[string]bool, len(p.AssetIDs))
	for _, id := range p.AssetIDs {
		wantAssets[id] = true
	}
	for _, a := range ep.Assets {
		if len(wantAssets) > 0 && !wantAssets[a.ID] {
			continue
		}
		if path := strings.TrimSpace(a.PrimaryPath); path != "" {
			prep.Files = append(prep.Files, path)
		}
	}
	wantShots := make(map[string]bool, len(p.ShotIDs))
	for _, id := range p.ShotIDs {
		wantShots[id] = true
	}
	for _, sh := range ep.Shots {
		if len(wantShots) > 0 && !wantShots[sh.ID] {
			continue
		}
		for _, fp := range []string{sh.KeyframePath, sh.VideoPath, sh.AudioPath} {
			if path := strings.TrimSpace(fp); path != "" {
				prep.Files = append(prep.Files, path)
			}
		}
	}
	return prep
}
