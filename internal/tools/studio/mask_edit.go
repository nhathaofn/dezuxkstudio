package studio

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"

	"app/internal/apperr"
	"app/internal/contracts"
	domainmedia "app/internal/domain/mediagen"
	"app/internal/engine"
)

type maskEditNode struct {
	opts Options
}

// NewMaskEditNode returns tool.mask_edit for canvas-masked region inpainting via mediagen.
func NewMaskEditNode(opts Options) engine.Node {
	opts.StudioConfig = withStudioDefaults(opts.StudioConfig)
	return &maskEditNode{opts: opts}
}

func (n *maskEditNode) Spec() engine.Spec {
	return engine.Spec{
		Type:        NodeTypeMaskEdit,
		Provider:    "studio",
		Label:       "Masked Inpaint Edit",
		Description: "Edits a specific region of a keyframe or character image defined by a painted PNG mask using your configured Image model.",
		Inputs: []engine.Port{
			{Name: "image_path", Label: "Base Image", Type: engine.PortImage},
			{Name: "mask_path", Label: "Mask PNG", Type: engine.PortImage},
		},
		Outputs: []engine.Port{
			{Name: "image", Label: "Edited Image", Type: engine.PortImage},
		},
		Params: []engine.Param{
			{Name: "prompt", Label: "Edit Prompt", Kind: engine.ParamText},
			{Name: "model_ref", Label: "Image Model", Kind: engine.ParamText},
			{Name: "aspect_ratio", Label: "Aspect Ratio", Kind: engine.ParamText},
			{Name: "resolution", Label: "Resolution", Kind: engine.ParamText},
			{Name: "output_dir", Label: "Output Folder", Kind: engine.ParamText, Role: engine.ParamRoleOutputDir},
		},
	}
}

func (n *maskEditNode) Run(ctx context.Context, raw json.RawMessage) (any, error) {
	var in contracts.MaskEditInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, apperr.Validation("invalid mask_edit input").WithDetails(map[string]any{"cause": err.Error()})
	}
	if strings.TrimSpace(in.Prompt) == "" {
		return nil, apperr.Validation("prompt is required for mask_edit")
	}
	imgPath, err := resolveInputFile(ctx, n.opts, in.ImagePath)
	if err != nil {
		return nil, err
	}
	maskPath, err := resolveInputFile(ctx, n.opts, in.MaskPath)
	if err != nil {
		return nil, err
	}
	if imgPath == "" || maskPath == "" {
		return nil, apperr.Validation("both image_path and mask_path are required")
	}
	if n.opts.ImageGen == nil {
		return nil, apperr.Unavailable("Image generator service is unavailable")
	}

	outDir, err := resolveNodeOutputDir(ctx, n.opts, in.OutputDir, filepath.Join("studio", "edits"))
	if err != nil {
		return nil, err
	}

	res, err := n.opts.ImageGen.GenerateImage(ctx, domainmedia.ImageRequest{
		ModelRef:        in.ModelRef,
		Prompt:          in.Prompt,
		ReferenceImages: []string{imgPath},
		MaskPath:        maskPath,
		AspectRatio:     in.AspectRatio,
		Resolution:      in.Resolution,
		Seed:            in.Seed,
		OutputDir:       outDir,
	})
	if err != nil {
		return nil, err
	}
	assetID := registerStudioAsset(ctx, n.opts, "image", NodeTypeMaskEdit, res.Path, outDir)
	return contracts.MaskEditOutput{
		Image:    engine.RefFor(res.Path, engine.MediaImage),
		Path:     res.Path,
		AssetID:  assetID,
		MimeType: res.MimeType,
		Provider: res.Provider,
		Model:    res.Model,
	}, nil
}
