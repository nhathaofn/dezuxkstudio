package contracts

import (
	"encoding/json"
	"reflect"
)

// NodeSpec names a built-in workflow node whose input/output shapes the frontend uses directly
// (a feature screen that runs one node). Its settings (options, min/max, defaults) are NOT
// duplicated here: screens read them at runtime from workflow:specs.
type NodeSpec struct {
	Key    string // TS constant key: NodeTypes.<Key>
	Type   string // engine node type
	Input  reflect.Type
	Output reflect.Type
}

// Nodes is the node catalogue emitted into generated.ts.
func Nodes() []NodeSpec {
	return []NodeSpec{
		{"WATERMARK_REMOVER", NodeTypeWatermarkRemover, t[RemoverInput](), t[RemoverOutput]()},
		{"PROVIDER_LIST", NodeTypeProviderList, t[Empty](), t[json.RawMessage]()},
		{"PROVIDER_SETUP", NodeTypeProviderSetup, t[json.RawMessage](), t[json.RawMessage]()},
		{"PROVIDER_TEST", NodeTypeProviderTest, t[json.RawMessage](), t[json.RawMessage]()},
		{"PROVIDER_ACTIVATE", NodeTypeProviderActivate, t[json.RawMessage](), t[json.RawMessage]()},
		{"PROVIDER_DELETE", NodeTypeProviderDelete, t[json.RawMessage](), t[json.RawMessage]()},
		{"PROVIDER_DISCOVER_MODELS", NodeTypeProviderDiscoverModel, t[json.RawMessage](), t[json.RawMessage]()},
		{"PROVIDER_SET_DEFAULT", NodeTypeProviderSetDefault, t[json.RawMessage](), t[json.RawMessage]()},
		{"PROVIDER_SETTINGS", NodeTypeProviderSettings, t[json.RawMessage](), t[json.RawMessage]()},
		{"IMAGE_GENERATE", NodeTypeImageGenerate, t[ImageGenerateInput](), t[ImageGenerateOutput]()},
		{"VIDEO_GENERATE", NodeTypeVideoGenerate, t[VideoGenerateInput](), t[VideoGenerateOutput]()},
		{"TTS_GENERATE", NodeTypeTTSGenerate, t[TTSGenerateInput](), t[TTSGenerateOutput]()},
		{"SCRIPT_NORMALIZE", NodeTypeScriptNormalize, t[ScriptNormalizeInput](), t[ScriptNormalizeOutput]()},
		{"EXTRACT_ASSETS", NodeTypeExtractAssets, t[ExtractAssetsInput](), t[ExtractAssetsOutput]()},
		{"AUTHOR_SHOT_PROMPTS", NodeTypeAuthorShotPrompts, t[AuthorShotPromptsInput](), t[AuthorShotPromptsOutput]()},
		{"PROBE_MEDIA", NodeTypeProbeMedia, t[ProbeMediaInput](), t[ProbeMediaOutput]()},
		{"VIDEO_CONCAT_BASIC", NodeTypeVideoConcatBasic, t[VideoConcatBasicInput](), t[VideoConcatBasicOutput]()},
		{"BATCH_PREFLIGHT", NodeTypeBatchPreflight, t[BatchPreflightInput](), t[BatchPreflightOutput]()},
		{"GRID_SPLIT", NodeTypeGridSplit, t[GridSplitInput](), t[GridSplitOutput]()},
		{"MASK_EDIT", NodeTypeMaskEdit, t[MaskEditInput](), t[MaskEditOutput]()},
		{"EXTRACT_FRAMES", NodeTypeExtractFrames, t[ExtractFramesInput](), t[ExtractFramesOutput]()},
		{"TTS_DUB_MUX", NodeTypeTTSDubMux, t[TTSDubMuxInput](), t[TTSDubMuxOutput]()},
		{"LIPSYNC_GENERATE", NodeTypeLipSyncGenerate, t[LipSyncGenerateInput](), t[LipSyncGenerateOutput]()},
		{"QC_REVIEW", NodeTypeQCReview, t[QCReviewInput](), t[QCReviewOutput]()},
		{"VIDEO_WATERMARK_REMOVE", NodeTypeVideoWMRemove, t[VideoWatermarkRemoveInput](), t[VideoWatermarkRemoveOutput]()},
		{"TIMELINE_AUTO_ASSEMBLE", NodeTypeTimelineAutoAssem, t[TimelineAutoAssembleInput](), t[TimelineAutoAssembleOutput]()},
		{"MEDIA_PEAKS_THUMBS", NodeTypeMediaPeaksThumbs, t[MediaPeaksThumbsInput](), t[MediaPeaksThumbsOutput]()},
		{"RENDER_TIMELINE", NodeTypeRenderTimeline, t[RenderTimelineInput](), t[RenderTimelineOutput]()},
		{"EXPORT_NLE_DRAFT", NodeTypeExportNLEDraft, t[ExportNLEDraftInput](), t[ExportNLEDraftOutput]()},
	}
}
