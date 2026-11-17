package studio

import (
	"context"
	"encoding/json"
	"path/filepath"

	"app/internal/apperr"
	"app/internal/contracts"
	domainmedia "app/internal/domain/mediagen"
	"app/internal/engine"
)

type lipSyncGenerateNode struct {
	opts Options
}

// NewLipSyncGenerateNode returns tool.lipsync_generate for driving character mouth sync with audio tracks.
func NewLipSyncGenerateNode(opts Options) engine.Node {
	opts.StudioConfig = withStudioDefaults(opts.StudioConfig)
	return &lipSyncGenerateNode{opts: opts}
}

func (n *lipSyncGenerateNode) Spec() engine.Spec {
	return engine.Spec{
		Type:        NodeTypeLipSyncGenerate,
		Provider:    "studio",
		Label:       "Character Lip-Sync Video",
		Description: "Synchronizes character facial/lip movement in a video clip with a spoken dialogue audio track via your configured LipSync provider.",
		Inputs: []engine.Port{
			{Name: "video_path", Label: "Source Video", Type: engine.PortVideo},
			{Name: "audio_path", Label: "Dialogue Audio", Type: engine.PortAudio},
		},
		Outputs: []engine.Port{
			{Name: "video", Label: "Lip-Synced Video", Type: engine.PortVideo},
		},
		Params: []engine.Param{
			{Name: "model_ref", Label: "Lip-Sync Model", Kind: engine.ParamText},
			{Name: "face_index", Label: "Target Face Index", Kind: engine.ParamInteger, Min: 0, Max: 8, Default: "0"},
			{Name: "prompt", Label: "Guidance Prompt", Kind: engine.ParamText},
			{Name: "output_dir", Label: "Output Folder", Kind: engine.ParamText, Role: engine.ParamRoleOutputDir},
		},
	}
}

func (n *lipSyncGenerateNode) Run(ctx context.Context, raw json.RawMessage) (any, error) {
	var in contracts.LipSyncGenerateInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, apperr.Validation("invalid lipsync_generate input").WithDetails(map[string]any{"cause": err.Error()})
	}
	vidPath, err := resolveInputFile(ctx, n.opts, in.VideoPath)
	if err != nil {
		return nil, err
	}
	audPath, err := resolveInputFile(ctx, n.opts, in.AudioPath)
	if err != nil {
		return nil, err
	}
	if vidPath == "" || audPath == "" {
		return nil, apperr.Validation("both video_path and audio_path are required")
	}
	if n.opts.LipSyncGen == nil {
		return nil, apperr.Unavailable("Lip-sync generator service is unavailable")
	}

	outDir, err := resolveNodeOutputDir(ctx, n.opts, in.OutputDir, filepath.Join("studio", "lipsync"))
	if err != nil {
		return nil, err
	}

	res, err := n.opts.LipSyncGen.GenerateLipSync(ctx, domainmedia.LipSyncRequest{
		ModelRef:  in.ModelRef,
		VideoPath: vidPath,
		AudioPath: audPath,
		FaceIndex: in.FaceIndex,
		Prompt:    in.Prompt,
		OutputDir: outDir,
	})
	if err != nil {
		return nil, err
	}
	assetID := registerStudioAsset(ctx, n.opts, "video", NodeTypeLipSyncGenerate, res.Path, outDir)
	return contracts.LipSyncGenerateOutput{
		Video:    engine.RefFor(res.Path, engine.MediaVideo),
		Path:     res.Path,
		AssetID:  assetID,
		MimeType: res.MimeType,
		Provider: res.Provider,
		Model:    res.Model,
	}, nil
}
