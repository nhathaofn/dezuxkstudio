package contracts

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	"app/internal/engine"
)

// Built-in workflow node type identifiers.
const (
	NodeTypeWatermarkRemover  = "tool.watermark_remover"
	NodeTypeImageGenerate     = "tool.generate_image"
	NodeTypeVideoGenerate     = "tool.generate_video"
	NodeTypeTTSGenerate       = "tool.generate_tts"
	NodeTypeProviderList      = "tool.provider_list"
	NodeTypeScriptNormalize   = "tool.script_normalize"
	NodeTypeExtractAssets     = "tool.extract_assets"
	NodeTypeAuthorShotPrompts = "tool.author_shot_prompts"
	NodeTypeProbeMedia        = "tool.probe_media"
	NodeTypeVideoConcatBasic  = "tool.video_concat_basic"
	NodeTypeBatchPreflight    = "tool.batch_preflight"
	NodeTypeGridSplit         = "tool.grid_split"
	NodeTypeMaskEdit          = "tool.mask_edit"
	NodeTypeExtractFrames     = "tool.extract_frames"
	NodeTypeTTSDubMux         = "tool.tts_dub_mux"
	NodeTypeLipSyncGenerate   = "tool.lipsync_generate"
	NodeTypeQCReview          = "tool.qc_review"
	NodeTypeVideoWMRemove     = "tool.video_watermark_remove"
	NodeTypeTimelineAutoAssem = "tool.timeline_auto_assemble"
	NodeTypeMediaPeaksThumbs  = "tool.media_peaks_thumbs"
	NodeTypeRenderTimeline    = "tool.render_timeline"
	NodeTypeExportNLEDraft    = "tool.export_nle_draft"
)

// RemoverInput is the input payload for the watermark remover node.
type RemoverInput struct {
	Image        engine.ImageInputRef  `json:"image,omitempty"`
	Images       engine.ImageInputRefs `json:"images,omitempty"`
	InPlace      bool                  `json:"inplace,omitempty"`
	OutputDir    string                `json:"output_dir,omitempty"`
	Workers      int                   `json:"workers,omitempty"`
	Corner       string                `json:"corner,omitempty"`
	MinScore     float64               `json:"min_score,omitempty"`
	MinGain      float64               `json:"min_gain,omitempty"`
	MaxGain      float64               `json:"max_gain,omitempty"`
	CopyClean    *bool                 `json:"copy_clean,omitempty"`
	PreserveTree *bool                 `json:"preserve_tree,omitempty"`
}

// RemoverItem provides execution details for each processed image.
type RemoverItem struct {
	Path     string  `json:"path"`
	OutPath  string  `json:"out_path"`
	Detected bool    `json:"detected"`
	Score    float64 `json:"score"`
	Size     int     `json:"size,omitempty"`
	Error    string  `json:"error,omitempty"`
	Status   string  `json:"status,omitempty"`
	Reason   string  `json:"reason,omitempty"`
	AssetID  string  `json:"asset_id,omitempty"`
}

// RemoverOutput is the output payload produced by the watermark remover node.
type RemoverOutput struct {
	Image         engine.MediaRef   `json:"image"`
	Images        []engine.MediaRef `json:"images"`
	Detected      bool              `json:"detected"`
	TotalCount    int               `json:"total_count"`
	DetectedCount int               `json:"detected_count"`
	Results       []RemoverItem     `json:"results"`
}

// ImageGenerateInput is the merged input+params contract for tool.generate_image.
type ImageGenerateInput struct {
	Prompt          string                `json:"prompt"`
	ReferenceImages engine.ImageInputRefs `json:"reference_images,omitempty"`
	Model           string                `json:"model,omitempty"`
	AspectRatio     string                `json:"aspect_ratio,omitempty"`
	Resolution      string                `json:"resolution,omitempty"`
	Seed            *int                  `json:"seed,omitempty"`
	OutputDir       string                `json:"output_dir,omitempty"`
}

// ImageGenerateOutput is the typed result returned by tool.generate_image.
type ImageGenerateOutput struct {
	Image     engine.MediaRef `json:"image"`
	Path      string          `json:"path"`
	MimeType  string          `json:"mimeType"`
	Provider  string          `json:"provider"`
	Model     string          `json:"model"`
	SizeBytes int64           `json:"sizeBytes"`
	AssetID   string          `json:"asset_id,omitempty"`
}

// VideoGenerateInput is the merged input+params contract for tool.generate_video.
type VideoGenerateInput struct {
	Prompt          string                `json:"prompt,omitempty"`
	StartImage      engine.ImageInputRef  `json:"start_image,omitempty"`
	EndImage        engine.ImageInputRef  `json:"end_image,omitempty"`
	ReferenceImages engine.ImageInputRefs `json:"reference_images,omitempty"`
	Model           string                `json:"model,omitempty"`
	AspectRatio     string                `json:"aspect_ratio,omitempty"`
	Resolution      string                `json:"resolution,omitempty"`
	Duration        int                   `json:"duration,omitempty"`
	GenerateAudio   bool                  `json:"generate_audio,omitempty"`
	Seed            *int                  `json:"seed,omitempty"`
	OutputDir       string                `json:"output_dir,omitempty"`
}

// VideoGenerateOutput is the typed result returned by tool.generate_video.
type VideoGenerateOutput struct {
	Video     engine.MediaRef `json:"video"`
	Path      string          `json:"path"`
	MimeType  string          `json:"mimeType"`
	Provider  string          `json:"provider"`
	Model     string          `json:"model"`
	Duration  float64         `json:"duration"`
	SizeBytes int64           `json:"sizeBytes"`
	AssetID   string          `json:"asset_id,omitempty"`
}

// TTSGenerateInput is the merged input+params contract for tool.generate_tts.
type TTSGenerateInput struct {
	Text      string  `json:"text"`
	Model     string  `json:"model,omitempty"`
	Voice     string  `json:"voice,omitempty"`
	Format    string  `json:"format,omitempty"`
	Speed     float64 `json:"speed,omitempty"`
	Language  string  `json:"language,omitempty"`
	OutputDir string  `json:"output_dir,omitempty"`
}

// UnmarshalJSON tolerates speed encoded either as JSON number (1.2) or string ("1.2").
func (in *TTSGenerateInput) UnmarshalJSON(data []byte) error {
	type raw struct {
		Text      string `json:"text"`
		Model     string `json:"model,omitempty"`
		Voice     string `json:"voice,omitempty"`
		Format    string `json:"format,omitempty"`
		Speed     any    `json:"speed,omitempty"`
		Language  string `json:"language,omitempty"`
		OutputDir string `json:"output_dir,omitempty"`
	}
	var r raw
	if err := json.Unmarshal(data, &r); err != nil {
		return err
	}
	in.Text = r.Text
	in.Model = r.Model
	in.Voice = r.Voice
	in.Format = r.Format
	in.Language = r.Language
	in.OutputDir = r.OutputDir
	switch v := r.Speed.(type) {
	case float64:
		in.Speed = v
	case string:
		if strings.TrimSpace(v) != "" {
			parsed, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
			if err != nil {
				return fmt.Errorf("invalid speed %q: %w", v, err)
			}
			in.Speed = parsed
		}
	}
	return nil
}

// TTSGenerateOutput is the typed result returned by tool.generate_tts.
type TTSGenerateOutput struct {
	Audio     engine.MediaRef `json:"audio"`
	Path      string          `json:"path"`
	MimeType  string          `json:"mimeType"`
	Provider  string          `json:"provider"`
	Model     string          `json:"model"`
	SizeBytes int64           `json:"sizeBytes"`
	AssetID   string          `json:"asset_id,omitempty"`
}

// ScriptNormalizeInput is the input+params contract for tool.script_normalize.
type ScriptNormalizeInput struct {
	RawScript    string `json:"raw_script"`
	Genre        string `json:"genre,omitempty"`
	EpisodeTitle string `json:"episode_title,omitempty"`
	AspectRatio  string `json:"aspect_ratio,omitempty"`
	Model        string `json:"model,omitempty"`
	ProjectDir   string `json:"project_dir,omitempty"`
}

// ScriptNormalizeOutput is the structured result returned by tool.script_normalize.
type ScriptNormalizeOutput struct {
	NormalizedScriptJSON string `json:"normalized_script_json"`
	ScriptPath           string `json:"script_path,omitempty"`
	SceneCount           int    `json:"scene_count"`
	BeatCount            int    `json:"beat_count"`
	Provider             string `json:"provider"`
	Model                string `json:"model"`
}

// ExtractAssetsInput is the input+params contract for tool.extract_assets.
type ExtractAssetsInput struct {
	NormalizedScriptJSON string `json:"normalized_script_json"`
	StylePrompt          string `json:"style_prompt,omitempty"`
	Model                string `json:"model,omitempty"`
	ProjectDir           string `json:"project_dir,omitempty"`
}

// ExtractAssetsOutput is the structured result returned by tool.extract_assets.
type ExtractAssetsOutput struct {
	AssetBibleJSON string `json:"asset_bible_json"`
	AssetsPath     string `json:"assets_path,omitempty"`
	AssetCount     int    `json:"asset_count"`
	Provider       string `json:"provider"`
	Model          string `json:"model"`
}

// AuthorShotPromptsInput is the input+params contract for tool.author_shot_prompts.
type AuthorShotPromptsInput struct {
	NormalizedScriptJSON string `json:"normalized_script_json"`
	AssetBibleJSON       string `json:"asset_bible_json,omitempty"`
	ProductionMode       string `json:"production_mode,omitempty"`
	ShotModeDefault      string `json:"shot_mode_default,omitempty"`
	DefaultDurationSec   int    `json:"default_duration_sec,omitempty"`
	StylePrompt          string `json:"style_prompt,omitempty"`
	Model                string `json:"model,omitempty"`
	ProjectDir           string `json:"project_dir,omitempty"`
}

// AuthorShotPromptsOutput is the structured result returned by tool.author_shot_prompts.
type AuthorShotPromptsOutput struct {
	ShotsJSON string `json:"shots_json"`
	ShotsPath string `json:"shots_path,omitempty"`
	ShotCount int    `json:"shot_count"`
	Provider  string `json:"provider"`
	Model     string `json:"model"`
}

// ProbeMediaInput is the input contract for tool.probe_media.
type ProbeMediaInput struct {
	Media engine.ImageInputRef `json:"media,omitempty"`
	Audio engine.ImageInputRef `json:"audio,omitempty"`
}

// ProbeMediaOutput is the structured probe output returned by tool.probe_media.
type ProbeMediaOutput struct {
	ProbeJSON   string  `json:"probe_json"`
	DurationSec float64 `json:"duration_sec"`
	Width       int     `json:"width,omitempty"`
	Height      int     `json:"height,omitempty"`
	FPS         float64 `json:"fps,omitempty"`
	HasAudio    bool    `json:"has_audio"`
	SilentRatio float64 `json:"silent_ratio"`
	VideoCodec  string  `json:"video_codec,omitempty"`
	AudioCodec  string  `json:"audio_codec,omitempty"`
	FormatName  string  `json:"format_name,omitempty"`
}

// VideoConcatBasicInput is the input+params contract for tool.video_concat_basic.
type VideoConcatBasicInput struct {
	ClipsManifestJSON  string `json:"clips_manifest_json"`
	OutputDir          string `json:"output_dir,omitempty"`
	NarrationAudioPath string `json:"narration_audio_path,omitempty"`
	Resolution         string `json:"resolution,omitempty"`
	FPS                int    `json:"fps,omitempty"`
}

// VideoConcatBasicOutput is the output produced by tool.video_concat_basic.
type VideoConcatBasicOutput struct {
	Video       engine.MediaRef `json:"video"`
	OutputPath  string          `json:"output_path"`
	AssetID     string          `json:"asset_id,omitempty"`
	DurationSec float64         `json:"duration_sec"`
	ClipCount   int             `json:"clip_count"`
}

// BatchPreflightInput is the input+params contract for tool.batch_preflight.
type BatchPreflightInput struct {
	ProjectID    string  `json:"project_id,omitempty"`
	EpisodeID    string  `json:"episode_id,omitempty"`
	ModelRef     string  `json:"model_ref,omitempty"`
	Capability   string  `json:"capability"`
	ItemCount    int     `json:"item_count"`
	UnitsPerItem float64 `json:"units_per_item,omitempty"`
}

// BatchPreflightMessage describes a localizable warning or blocker returned by batch preflight.
type BatchPreflightMessage struct {
	Message   string            `json:"message"`
	MsgKey    string            `json:"msg_key,omitempty"`
	MsgParams map[string]string `json:"msg_params,omitempty"`
}

// BatchPreflightOutput is returned by tool.batch_preflight.
type BatchPreflightOutput struct {
	Report       string                  `json:"report"`
	Ready        bool                    `json:"ready"`
	ProviderID   string                  `json:"provider_id,omitempty"`
	ModelID      string                  `json:"model_id,omitempty"`
	Capability   string                  `json:"capability"`
	ItemCount    int                     `json:"item_count"`
	Unit         string                  `json:"unit"`
	UnitPriceUSD float64                 `json:"unit_price_usd"`
	EstimatedUSD float64                 `json:"estimated_usd"`
	EstimatedSec int                     `json:"estimated_sec"`
	Warnings     []string                `json:"warnings"`
	WarningItems []BatchPreflightMessage `json:"warning_items"`
	Blockers     []string                `json:"blockers"`
	BlockerItems []BatchPreflightMessage `json:"blocker_items"`
}

// GridSplitInput is the input+params contract for tool.grid_split.
type GridSplitInput struct {
	ImagePath  engine.ImageInputRef `json:"image_path"`
	Rows       int                  `json:"rows,omitempty"`
	Cols       int                  `json:"cols,omitempty"`
	MarginPx   int                  `json:"margin_px,omitempty"`
	GutterPx   int                  `json:"gutter_px,omitempty"`
	RowWeights []float64            `json:"row_weights,omitempty"`
	ColWeights []float64            `json:"col_weights,omitempty"`
	OutputDir  string               `json:"output_dir,omitempty"`
}

// GridPanel describes one cropped cell produced by tool.grid_split.
type GridPanel struct {
	Index   int    `json:"index"`
	Row     int    `json:"row"`
	Col     int    `json:"col"`
	Path    string `json:"path"`
	Width   int    `json:"width"`
	Height  int    `json:"height"`
	AssetID string `json:"asset_id,omitempty"`
}

// GridSplitOutput is returned by tool.grid_split.
type GridSplitOutput struct {
	Images    []engine.MediaRef `json:"images"`
	Panels    []GridPanel       `json:"panels"`
	OutputDir string            `json:"output_dir"`
}

// MaskEditInput is the input+params contract for tool.mask_edit.
type MaskEditInput struct {
	ImagePath   engine.ImageInputRef `json:"image_path"`
	MaskPath    engine.ImageInputRef `json:"mask_path"`
	Prompt      string               `json:"prompt"`
	ModelRef    string               `json:"model_ref,omitempty"`
	AspectRatio string               `json:"aspect_ratio,omitempty"`
	Resolution  string               `json:"resolution,omitempty"`
	Seed        *int                 `json:"seed,omitempty"`
	OutputDir   string               `json:"output_dir,omitempty"`
}

// MaskEditOutput is returned by tool.mask_edit.
type MaskEditOutput struct {
	Image    engine.MediaRef `json:"image"`
	Path     string          `json:"path"`
	AssetID  string          `json:"asset_id,omitempty"`
	MimeType string          `json:"mime_type,omitempty"`
	Provider string          `json:"provider,omitempty"`
	Model    string          `json:"model,omitempty"`
}

// ExtractFramesInput is the input+params contract for tool.extract_frames.
type ExtractFramesInput struct {
	VideoPath     engine.ImageInputRef `json:"video_path"`
	Mode          string               `json:"mode,omitempty"`
	TimestampsSec []float64            `json:"timestamps_sec,omitempty"`
	TimestampsCSV string               `json:"timestamps_csv,omitempty"`
	Count         int                  `json:"count,omitempty"`
	OutputDir     string               `json:"output_dir,omitempty"`
}

// ExtractedFrame describes one saved frame image from tool.extract_frames.
type ExtractedFrame struct {
	Path         string  `json:"path"`
	TimestampSec float64 `json:"timestamp_sec"`
	Width        int     `json:"width,omitempty"`
	Height       int     `json:"height,omitempty"`
	AssetID      string  `json:"asset_id,omitempty"`
}

// ExtractFramesOutput is returned by tool.extract_frames.
type ExtractFramesOutput struct {
	LastFrame     engine.MediaRef  `json:"last_frame"`
	Frames        []ExtractedFrame `json:"frames"`
	LastFramePath string           `json:"last_frame_path,omitempty"`
	OutputDir     string           `json:"output_dir"`
}

// TTSDubMuxInput is the input+params contract for tool.tts_dub_mux.
type TTSDubMuxInput struct {
	VideoPath      engine.ImageInputRef `json:"video_path"`
	Text           string               `json:"text,omitempty"`
	AudioPath      engine.ImageInputRef `json:"audio_path,omitempty"`
	ModelRef       string               `json:"model_ref,omitempty"`
	Voice          string               `json:"voice,omitempty"`
	Language       string               `json:"language,omitempty"`
	OverflowMode   string               `json:"overflow_mode,omitempty"`
	DuckOriginal   *bool                `json:"duck_original,omitempty"`
	OriginalVolume float64              `json:"original_volume,omitempty"`
	OutputDir      string               `json:"output_dir,omitempty"`
}

// TTSDubMuxOutput is returned by tool.tts_dub_mux.
type TTSDubMuxOutput struct {
	Video         engine.MediaRef `json:"video"`
	Path          string          `json:"path"`
	AudioPath     string          `json:"audio_path"`
	AssetID       string          `json:"asset_id,omitempty"`
	DurationSec   float64         `json:"duration_sec"`
	AdjustedRatio float64         `json:"adjusted_ratio,omitempty"`
}

// LipSyncGenerateInput is the input+params contract for tool.lipsync_generate.
type LipSyncGenerateInput struct {
	VideoPath engine.ImageInputRef `json:"video_path"`
	AudioPath engine.ImageInputRef `json:"audio_path"`
	ModelRef  string               `json:"model_ref,omitempty"`
	FaceIndex int                  `json:"face_index,omitempty"`
	Prompt    string               `json:"prompt,omitempty"`
	OutputDir string               `json:"output_dir,omitempty"`
}

// LipSyncGenerateOutput is returned by tool.lipsync_generate.
type LipSyncGenerateOutput struct {
	Video    engine.MediaRef `json:"video"`
	Path     string          `json:"path"`
	AssetID  string          `json:"asset_id,omitempty"`
	MimeType string          `json:"mime_type,omitempty"`
	Provider string          `json:"provider,omitempty"`
	Model    string          `json:"model,omitempty"`
}

// QCReviewInput is the input+params contract for tool.qc_review.
type QCReviewInput struct {
	MediaPath      engine.ImageInputRef  `json:"media_path"`
	TargetKind     string                `json:"target_kind,omitempty"`
	TargetID       string                `json:"target_id,omitempty"`
	ReferencePaths engine.ImageInputRefs `json:"reference_paths,omitempty"`
	PromptExpected string                `json:"prompt_expected,omitempty"`
	VisionModelRef string                `json:"vision_model_ref,omitempty"`
	SampleCount    int                   `json:"sample_count,omitempty"`
	SkipVision     bool                  `json:"skip_vision,omitempty"`
}

// QCReviewIssue describes one issue found during automated QC inspection.
type QCReviewIssue struct {
	Severity  string            `json:"severity"`
	Category  string            `json:"category"`
	Message   string            `json:"message"`
	MsgKey    string            `json:"msg_key,omitempty"`
	MsgParams map[string]string `json:"msg_params,omitempty"`
	FramePath string            `json:"frame_path,omitempty"`
}

// QCReviewOutput is returned by tool.qc_review.
type QCReviewOutput struct {
	Report        string          `json:"report"`
	Verdict       string          `json:"verdict"`
	OverallScore  float64         `json:"overall_score"`
	FFprobeJSON   string          `json:"ffprobe_json,omitempty"`
	Issues        []QCReviewIssue `json:"issues"`
	VisionSummary string          `json:"vision_summary,omitempty"`
	SampleFrames  []string        `json:"sample_frames"`
}

// VideoWatermarkRemoveInput is the input+params contract for tool.video_watermark_remove.
type VideoWatermarkRemoveInput struct {
	VideoPath engine.ImageInputRef `json:"video_path"`
	Mode      string               `json:"mode,omitempty"`
	X         int                  `json:"x,omitempty"`
	Y         int                  `json:"y,omitempty"`
	Width     int                  `json:"width,omitempty"`
	Height    int                  `json:"height,omitempty"`
	CropEdge  string               `json:"crop_edge,omitempty"`
	CropPx    int                  `json:"crop_px,omitempty"`
	OutputDir string               `json:"output_dir,omitempty"`
}

// VideoWatermarkRemoveOutput is returned by tool.video_watermark_remove.
type VideoWatermarkRemoveOutput struct {
	Video       engine.MediaRef `json:"video"`
	Path        string          `json:"path"`
	AssetID     string          `json:"asset_id,omitempty"`
	Width       int             `json:"width,omitempty"`
	Height      int             `json:"height,omitempty"`
	DurationSec float64         `json:"duration_sec,omitempty"`
}

// TimelineAutoAssembleInput is the input+params contract for tool.timeline_auto_assemble.
type TimelineAutoAssembleInput struct {
	ShotsJSON         string   `json:"shots_json"`
	AudioManifestJSON string   `json:"audio_manifest_json,omitempty"`
	EpisodeID         string   `json:"episode_id,omitempty"`
	Resolution        string   `json:"resolution,omitempty"`
	FPS               int      `json:"fps,omitempty"`
	TransitionDefault string   `json:"transition_default,omitempty"`
	TransitionMs      *int64   `json:"transition_ms,omitempty"`
	BGMPath           string   `json:"bgm_path,omitempty"`
	BGMVolume         *float64 `json:"bgm_volume,omitempty"`
	ProjectDir        string   `json:"project_dir,omitempty"`
}

// TimelineAutoAssembleOutput is returned by tool.timeline_auto_assemble.
type TimelineAutoAssembleOutput struct {
	TimelineJSON  string `json:"timeline_json"`
	TimelinePath  string `json:"timeline_path,omitempty"`
	DurationMs    int64  `json:"duration_ms"`
	TrackCount    int    `json:"track_count"`
	ClipCount     int    `json:"clip_count"`
	SubtitleCount int    `json:"subtitle_count"`
}

// MediaPeaksThumbsInput is the input+params contract for tool.media_peaks_thumbs.
type MediaPeaksThumbsInput struct {
	Media            engine.ImageInputRef `json:"media,omitempty"`
	Audio            engine.ImageInputRef `json:"audio,omitempty"`
	ThumbIntervalSec float64              `json:"thumb_interval_sec,omitempty"`
	PeakBins         int                  `json:"peak_bins,omitempty"`
	ThumbHeight      int                  `json:"thumb_height,omitempty"`
	OutputDir        string               `json:"output_dir,omitempty"`
}

// MediaPeaksThumbsOutput is returned by tool.media_peaks_thumbs.
type MediaPeaksThumbsOutput struct {
	PeaksThumbsJSON  string    `json:"peaks_thumbs_json"`
	SpriteSheetPath  string    `json:"sprite_sheet_path,omitempty"`
	SpriteAssetID    string    `json:"sprite_asset_id,omitempty"`
	PeaksJSONPath    string    `json:"peaks_json_path"`
	Peaks            []float64 `json:"peaks"`
	ThumbCount       int       `json:"thumb_count"`
	ThumbIntervalSec float64   `json:"thumb_interval_sec"`
	DurationMs       int64     `json:"duration_ms"`
}

// RenderTimelineInput is the input+params contract for tool.render_timeline.
type RenderTimelineInput struct {
	TimelineJSON     string  `json:"timeline_json"`
	OutputDir        string  `json:"output_dir,omitempty"`
	Filename         string  `json:"filename,omitempty"`
	BurnSubtitles    bool    `json:"burn_subtitles,omitempty"`
	SubtitleFontSize int     `json:"subtitle_font_size,omitempty"`
	LoudnormLUFS     float64 `json:"loudnorm_lufs,omitempty"`
	CRF              *int    `json:"crf,omitempty"`
	Preset           string  `json:"preset,omitempty"`
	Overwrite        bool    `json:"overwrite,omitempty"`
}

// RenderTimelineOutput is returned by tool.render_timeline.
type RenderTimelineOutput struct {
	Video       engine.MediaRef `json:"video"`
	OutputPath  string          `json:"output_path"`
	AssetID     string          `json:"asset_id,omitempty"`
	SRTPath     string          `json:"srt_path,omitempty"`
	ASSPath     string          `json:"ass_path,omitempty"`
	DurationSec float64         `json:"duration_sec"`
}

// ExportNLEDraftInput is the input+params contract for tool.export_nle_draft.
type ExportNLEDraftInput struct {
	TimelineJSON     string `json:"timeline_json"`
	Format           string `json:"format,omitempty"`
	OutputDir        string `json:"output_dir,omitempty"`
	ProjectName      string `json:"project_name,omitempty"`
	IncludeMediaCopy *bool  `json:"include_media_copy,omitempty"`
}

// ExportNLEDraftOutput is returned by tool.export_nle_draft.
type ExportNLEDraftOutput struct {
	ExportPath string `json:"export_path"`
	AssetID    string `json:"asset_id,omitempty"`
	Format     string `json:"format"`
	FileCount  int    `json:"file_count"`
	TotalBytes int64  `json:"total_bytes"`
}

func decodeTextOrJSON(raw json.RawMessage) string {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return trimmed
}

// UnmarshalJSON accepts normalized_script_json encoded as either a JSON string or an inline JSON object.
func (in *ExtractAssetsInput) UnmarshalJSON(data []byte) error {
	type raw struct {
		NormalizedScriptJSON json.RawMessage `json:"normalized_script_json"`
		StylePrompt          string          `json:"style_prompt,omitempty"`
		Model                string          `json:"model,omitempty"`
		ProjectDir           string          `json:"project_dir,omitempty"`
	}
	var r raw
	if err := json.Unmarshal(data, &r); err != nil {
		return err
	}
	in.NormalizedScriptJSON = decodeTextOrJSON(r.NormalizedScriptJSON)
	in.StylePrompt = r.StylePrompt
	in.Model = r.Model
	in.ProjectDir = r.ProjectDir
	return nil
}

// UnmarshalJSON accepts normalized_script_json and asset_bible_json as either JSON strings or inline objects.
func (in *AuthorShotPromptsInput) UnmarshalJSON(data []byte) error {
	type raw struct {
		NormalizedScriptJSON json.RawMessage `json:"normalized_script_json"`
		AssetBibleJSON       json.RawMessage `json:"asset_bible_json,omitempty"`
		ProductionMode       string          `json:"production_mode,omitempty"`
		ShotModeDefault      string          `json:"shot_mode_default,omitempty"`
		DefaultDurationSec   int             `json:"default_duration_sec,omitempty"`
		StylePrompt          string          `json:"style_prompt,omitempty"`
		Model                string          `json:"model,omitempty"`
		ProjectDir           string          `json:"project_dir,omitempty"`
	}
	var r raw
	if err := json.Unmarshal(data, &r); err != nil {
		return err
	}
	in.NormalizedScriptJSON = decodeTextOrJSON(r.NormalizedScriptJSON)
	in.AssetBibleJSON = decodeTextOrJSON(r.AssetBibleJSON)
	in.ProductionMode = r.ProductionMode
	in.ShotModeDefault = r.ShotModeDefault
	in.DefaultDurationSec = r.DefaultDurationSec
	in.StylePrompt = r.StylePrompt
	in.Model = r.Model
	in.ProjectDir = r.ProjectDir
	return nil
}

// UnmarshalJSON accepts clips_manifest_json as either a JSON string or an inline JSON array/object.
func (in *VideoConcatBasicInput) UnmarshalJSON(data []byte) error {
	type raw struct {
		ClipsManifestJSON  json.RawMessage `json:"clips_manifest_json"`
		OutputDir          string          `json:"output_dir,omitempty"`
		NarrationAudioPath string          `json:"narration_audio_path,omitempty"`
		Resolution         string          `json:"resolution,omitempty"`
		FPS                int             `json:"fps,omitempty"`
	}
	var r raw
	if err := json.Unmarshal(data, &r); err != nil {
		return err
	}
	in.ClipsManifestJSON = decodeTextOrJSON(r.ClipsManifestJSON)
	in.OutputDir = r.OutputDir
	in.NarrationAudioPath = r.NarrationAudioPath
	in.Resolution = r.Resolution
	in.FPS = r.FPS
	return nil
}

func parseFlexibleFloat(raw any) (float64, error) {
	switch v := raw.(type) {
	case nil:
		return 0, nil
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return 0, fmt.Errorf("invalid non-finite number")
		}
		return v, nil
	case string:
		trimmed := strings.TrimSpace(v)
		if trimmed == "" {
			return 0, nil
		}
		f, err := strconv.ParseFloat(trimmed, 64)
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
			return 0, fmt.Errorf("invalid number %q", v)
		}
		return f, nil
	default:
		return 0, fmt.Errorf("expected number or numeric string, got %T", raw)
	}
}

func parseOptionalFlexibleFloat(raw any) (*float64, error) {
	switch v := raw.(type) {
	case nil:
		return nil, nil
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return nil, fmt.Errorf("invalid non-finite number")
		}
		out := v
		return &out, nil
	case string:
		trimmed := strings.TrimSpace(v)
		if trimmed == "" {
			return nil, nil
		}
		f, err := strconv.ParseFloat(trimmed, 64)
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
			return nil, fmt.Errorf("invalid number %q", v)
		}
		return &f, nil
	default:
		return nil, fmt.Errorf("expected number or numeric string, got %T", raw)
	}
}

func parseOptionalFlexibleInt64(raw any) (*int64, error) {
	switch v := raw.(type) {
	case nil:
		return nil, nil
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) || math.Trunc(v) != v {
			return nil, fmt.Errorf("invalid integer %v", v)
		}
		out := int64(v)
		return &out, nil
	case string:
		trimmed := strings.TrimSpace(v)
		if trimmed == "" {
			return nil, nil
		}
		n, err := strconv.ParseInt(trimmed, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid integer %q", v)
		}
		return &n, nil
	default:
		return nil, fmt.Errorf("expected integer or numeric string, got %T", raw)
	}
}

func parseFlexibleFloatSlice(raw any) ([]float64, error) {
	switch v := raw.(type) {
	case nil:
		return nil, nil
	case []any:
		out := make([]float64, 0, len(v))
		for _, item := range v {
			f, err := parseFlexibleFloat(item)
			if err != nil {
				return nil, err
			}
			out = append(out, f)
		}
		return out, nil
	case string:
		trimmed := strings.TrimSpace(v)
		if trimmed == "" {
			return nil, nil
		}
		if strings.HasPrefix(trimmed, "[") {
			var arr []any
			if err := json.Unmarshal([]byte(trimmed), &arr); err != nil {
				return nil, fmt.Errorf("invalid numeric array %q: %w", v, err)
			}
			return parseFlexibleFloatSlice(arr)
		}
		parts := strings.Split(trimmed, ",")
		out := make([]float64, 0, len(parts))
		for _, part := range parts {
			p := strings.TrimSpace(part)
			if p == "" {
				continue
			}
			f, err := strconv.ParseFloat(p, 64)
			if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
				return nil, fmt.Errorf("invalid number %q", p)
			}
			out = append(out, f)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("expected number array or CSV string, got %T", raw)
	}
}

// UnmarshalJSON accepts units_per_item encoded as either a JSON number or numeric string.
func (in *BatchPreflightInput) UnmarshalJSON(data []byte) error {
	type raw struct {
		ProjectID    string `json:"project_id,omitempty"`
		EpisodeID    string `json:"episode_id,omitempty"`
		ModelRef     string `json:"model_ref,omitempty"`
		Capability   string `json:"capability"`
		ItemCount    int    `json:"item_count"`
		UnitsPerItem any    `json:"units_per_item,omitempty"`
	}
	var r raw
	if err := json.Unmarshal(data, &r); err != nil {
		return err
	}
	units, err := parseFlexibleFloat(r.UnitsPerItem)
	if err != nil {
		return err
	}
	in.ProjectID = r.ProjectID
	in.EpisodeID = r.EpisodeID
	in.ModelRef = r.ModelRef
	in.Capability = r.Capability
	in.ItemCount = r.ItemCount
	in.UnitsPerItem = units
	return nil
}

// UnmarshalJSON accepts row_weights and col_weights encoded as JSON number arrays or CSV strings.
func (in *GridSplitInput) UnmarshalJSON(data []byte) error {
	type raw struct {
		ImagePath  engine.ImageInputRef `json:"image_path"`
		Rows       int                  `json:"rows,omitempty"`
		Cols       int                  `json:"cols,omitempty"`
		MarginPx   int                  `json:"margin_px,omitempty"`
		GutterPx   int                  `json:"gutter_px,omitempty"`
		RowWeights any                  `json:"row_weights,omitempty"`
		ColWeights any                  `json:"col_weights,omitempty"`
		OutputDir  string               `json:"output_dir,omitempty"`
	}
	var r raw
	if err := json.Unmarshal(data, &r); err != nil {
		return err
	}
	rw, err := parseFlexibleFloatSlice(r.RowWeights)
	if err != nil {
		return err
	}
	cw, err := parseFlexibleFloatSlice(r.ColWeights)
	if err != nil {
		return err
	}
	in.ImagePath = r.ImagePath
	in.Rows = r.Rows
	in.Cols = r.Cols
	in.MarginPx = r.MarginPx
	in.GutterPx = r.GutterPx
	in.RowWeights = rw
	in.ColWeights = cw
	in.OutputDir = r.OutputDir
	return nil
}

// UnmarshalJSON accepts timestamps_sec encoded as either a JSON number array or CSV string.
func (in *ExtractFramesInput) UnmarshalJSON(data []byte) error {
	type raw struct {
		VideoPath     engine.ImageInputRef `json:"video_path"`
		Mode          string               `json:"mode,omitempty"`
		TimestampsSec any                  `json:"timestamps_sec,omitempty"`
		TimestampsCSV string               `json:"timestamps_csv,omitempty"`
		Count         int                  `json:"count,omitempty"`
		OutputDir     string               `json:"output_dir,omitempty"`
	}
	var r raw
	if err := json.Unmarshal(data, &r); err != nil {
		return err
	}
	ts, err := parseFlexibleFloatSlice(r.TimestampsSec)
	if err != nil {
		return err
	}
	in.VideoPath = r.VideoPath
	in.Mode = r.Mode
	in.TimestampsSec = ts
	in.TimestampsCSV = r.TimestampsCSV
	in.Count = r.Count
	in.OutputDir = r.OutputDir
	return nil
}

// UnmarshalJSON accepts original_volume encoded as either a JSON number or numeric string.
func (in *TTSDubMuxInput) UnmarshalJSON(data []byte) error {
	type raw struct {
		VideoPath      engine.ImageInputRef `json:"video_path"`
		Text           string               `json:"text,omitempty"`
		AudioPath      engine.ImageInputRef `json:"audio_path,omitempty"`
		ModelRef       string               `json:"model_ref,omitempty"`
		Voice          string               `json:"voice,omitempty"`
		Language       string               `json:"language,omitempty"`
		OverflowMode   string               `json:"overflow_mode,omitempty"`
		DuckOriginal   *bool                `json:"duck_original,omitempty"`
		OriginalVolume any                  `json:"original_volume,omitempty"`
		OutputDir      string               `json:"output_dir,omitempty"`
	}
	var r raw
	if err := json.Unmarshal(data, &r); err != nil {
		return err
	}
	vol, err := parseFlexibleFloat(r.OriginalVolume)
	if err != nil {
		return err
	}
	in.VideoPath = r.VideoPath
	in.Text = r.Text
	in.AudioPath = r.AudioPath
	in.ModelRef = r.ModelRef
	in.Voice = r.Voice
	in.Language = r.Language
	in.OverflowMode = r.OverflowMode
	in.DuckOriginal = r.DuckOriginal
	in.OriginalVolume = vol
	in.OutputDir = r.OutputDir
	return nil
}

// UnmarshalJSON accepts shots_json/audio_manifest_json as strings or inline JSON and numeric strings for floats.
func (in *TimelineAutoAssembleInput) UnmarshalJSON(data []byte) error {
	type raw struct {
		ShotsJSON         json.RawMessage `json:"shots_json"`
		AudioManifestJSON json.RawMessage `json:"audio_manifest_json,omitempty"`
		EpisodeID         string          `json:"episode_id,omitempty"`
		Resolution        string          `json:"resolution,omitempty"`
		FPS               int             `json:"fps,omitempty"`
		TransitionDefault string          `json:"transition_default,omitempty"`
		TransitionMs      any             `json:"transition_ms,omitempty"`
		BGMPath           string          `json:"bgm_path,omitempty"`
		BGMVolume         any             `json:"bgm_volume,omitempty"`
		ProjectDir        string          `json:"project_dir,omitempty"`
	}
	var r raw
	if err := json.Unmarshal(data, &r); err != nil {
		return err
	}
	transMs, err := parseOptionalFlexibleInt64(r.TransitionMs)
	if err != nil {
		return err
	}
	vol, err := parseOptionalFlexibleFloat(r.BGMVolume)
	if err != nil {
		return err
	}
	in.ShotsJSON = decodeTextOrJSON(r.ShotsJSON)
	in.AudioManifestJSON = decodeTextOrJSON(r.AudioManifestJSON)
	in.EpisodeID = r.EpisodeID
	in.Resolution = r.Resolution
	in.FPS = r.FPS
	in.TransitionDefault = r.TransitionDefault
	in.TransitionMs = transMs
	in.BGMPath = r.BGMPath
	in.BGMVolume = vol
	in.ProjectDir = r.ProjectDir
	return nil
}

// UnmarshalJSON accepts thumb_interval_sec encoded as either number or string.
func (in *MediaPeaksThumbsInput) UnmarshalJSON(data []byte) error {
	type raw struct {
		Media            engine.ImageInputRef `json:"media,omitempty"`
		Audio            engine.ImageInputRef `json:"audio,omitempty"`
		ThumbIntervalSec any                  `json:"thumb_interval_sec,omitempty"`
		PeakBins         int                  `json:"peak_bins,omitempty"`
		ThumbHeight      int                  `json:"thumb_height,omitempty"`
		OutputDir        string               `json:"output_dir,omitempty"`
	}
	var r raw
	if err := json.Unmarshal(data, &r); err != nil {
		return err
	}
	in.Media = r.Media
	in.Audio = r.Audio
	in.PeakBins = r.PeakBins
	in.ThumbHeight = r.ThumbHeight
	in.OutputDir = r.OutputDir
	interval, err := parseFlexibleFloat(r.ThumbIntervalSec)
	if err != nil {
		return err
	}
	in.ThumbIntervalSec = interval
	return nil
}

// UnmarshalJSON accepts timeline_json as string or inline JSON object and loudnorm_lufs as number or string.
func (in *RenderTimelineInput) UnmarshalJSON(data []byte) error {
	type raw struct {
		TimelineJSON     json.RawMessage `json:"timeline_json"`
		OutputDir        string          `json:"output_dir,omitempty"`
		Filename         string          `json:"filename,omitempty"`
		BurnSubtitles    bool            `json:"burn_subtitles,omitempty"`
		SubtitleFontSize int             `json:"subtitle_font_size,omitempty"`
		LoudnormLUFS     any             `json:"loudnorm_lufs,omitempty"`
		CRF              *int            `json:"crf,omitempty"`
		Preset           string          `json:"preset,omitempty"`
		Overwrite        bool            `json:"overwrite,omitempty"`
	}
	var r raw
	if err := json.Unmarshal(data, &r); err != nil {
		return err
	}
	in.TimelineJSON = decodeTextOrJSON(r.TimelineJSON)
	in.OutputDir = r.OutputDir
	in.Filename = r.Filename
	in.BurnSubtitles = r.BurnSubtitles
	in.SubtitleFontSize = r.SubtitleFontSize
	in.CRF = r.CRF
	in.Preset = r.Preset
	in.Overwrite = r.Overwrite
	lufs, err := parseFlexibleFloat(r.LoudnormLUFS)
	if err != nil {
		return err
	}
	in.LoudnormLUFS = lufs
	return nil
}

// UnmarshalJSON accepts timeline_json as either a JSON string or an inline JSON object.
func (in *ExportNLEDraftInput) UnmarshalJSON(data []byte) error {
	type raw struct {
		TimelineJSON     json.RawMessage `json:"timeline_json"`
		Format           string          `json:"format,omitempty"`
		OutputDir        string          `json:"output_dir,omitempty"`
		ProjectName      string          `json:"project_name,omitempty"`
		IncludeMediaCopy *bool           `json:"include_media_copy,omitempty"`
	}
	var r raw
	if err := json.Unmarshal(data, &r); err != nil {
		return err
	}
	in.TimelineJSON = decodeTextOrJSON(r.TimelineJSON)
	in.Format = r.Format
	in.OutputDir = r.OutputDir
	in.ProjectName = r.ProjectName
	in.IncludeMediaCopy = r.IncludeMediaCopy
	return nil
}
