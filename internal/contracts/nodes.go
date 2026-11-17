package contracts

import "reflect"

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
		{"IMAGE_GENERATE", NodeTypeImageGenerate, t[ImageGenerateInput](), t[ImageGenerateOutput]()},
		{"VIDEO_GENERATE", NodeTypeVideoGenerate, t[VideoGenerateInput](), t[VideoGenerateOutput]()},
		{"TTS_GENERATE", NodeTypeTTSGenerate, t[TTSGenerateInput](), t[TTSGenerateOutput]()},
		{"SCRIPT_NORMALIZE", NodeTypeScriptNormalize, t[ScriptNormalizeInput](), t[ScriptNormalizeOutput]()},
		{"EXTRACT_ASSETS", NodeTypeExtractAssets, t[ExtractAssetsInput](), t[ExtractAssetsOutput]()},
		{"AUTHOR_SHOT_PROMPTS", NodeTypeAuthorShotPrompts, t[AuthorShotPromptsInput](), t[AuthorShotPromptsOutput]()},
		{"PROBE_MEDIA", NodeTypeProbeMedia, t[ProbeMediaInput](), t[ProbeMediaOutput]()},
		{"VIDEO_CONCAT_BASIC", NodeTypeVideoConcatBasic, t[VideoConcatBasicInput](), t[VideoConcatBasicOutput]()},
	}
}
