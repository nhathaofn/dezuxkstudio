package contracts

import (
	"encoding/json"
	"fmt"
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
	Results       []RemoverItem     `json:"results,omitempty"`
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
