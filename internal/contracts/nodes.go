package contracts

import (
	"reflect"

	mediagennodes "app/internal/tools/mediagen"
	"app/internal/tools/watermark"
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
		{"WATERMARK_REMOVER", watermark.NodeType, t[watermark.RemoverInput](), t[watermark.RemoverOutput]()},
		{"IMAGE_GENERATE", mediagennodes.NodeTypeImage, t[mediagennodes.ImageGenerateInput](), t[mediagennodes.ImageGenerateOutput]()},
		{"VIDEO_GENERATE", mediagennodes.NodeTypeVideo, t[mediagennodes.VideoGenerateInput](), t[mediagennodes.VideoGenerateOutput]()},
		{"TTS_GENERATE", mediagennodes.NodeTypeTTS, t[mediagennodes.TTSGenerateInput](), t[mediagennodes.TTSGenerateOutput]()},
	}
}
