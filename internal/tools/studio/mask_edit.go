package studio

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"app/internal/apperr"
	"app/internal/contracts"
	domainmedia "app/internal/domain/mediagen"
	"app/internal/engine"
	"app/internal/progress"
)

// NewMaskEditNode returns tool.mask_edit for canvas-masked region inpainting via mediagen.
func NewMaskEditNode(opts Options) engine.Node {
	opts.StudioConfig = withStudioDefaults(opts.StudioConfig)
	aspectOptions := domainmedia.ImageAspectRatios()
	resOptions := domainmedia.ImageResolutions()

	spec := engine.Spec{
		Type:        NodeTypeMaskEdit,
		Provider:    studioProviderID,
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
			{Name: "aspect_ratio", Label: "Aspect Ratio", Kind: engine.ParamSelect, Options: aspectOptions, Default: domainmedia.DefaultImageAspectRatio},
			{Name: "resolution", Label: "Resolution", Kind: engine.ParamSelect, Options: resOptions, Default: domainmedia.DefaultImageResolution},
			{Name: "seed", Label: "Random Seed", Kind: engine.ParamInteger},
			{Name: "output_dir", Label: "Output Folder", Kind: engine.ParamText, Role: engine.ParamRoleOutputDir},
		},
	}

	return engine.Typed(spec, func(ctx context.Context, in contracts.MaskEditInput) (contracts.MaskEditOutput, error) {
		if strings.TrimSpace(in.Prompt) == "" {
			return contracts.MaskEditOutput{}, apperr.Validation("prompt is required for mask_edit").
				WithMsg("errors.studio.prompt_required", nil)
		}
		imgPath, err := resolveMediaInput(ctx, opts, in.ImagePath.LocalURL, inputKindImage)
		if err != nil {
			return contracts.MaskEditOutput{}, err
		}
		maskPath, err := resolveMediaInput(ctx, opts, in.MaskPath.LocalURL, inputKindImage)
		if err != nil {
			return contracts.MaskEditOutput{}, err
		}
		if imgPath == "" || maskPath == "" {
			return contracts.MaskEditOutput{}, apperr.Validation("both image_path and mask_path are required").
				WithMsg("errors.studio.mask_inputs_required", nil)
		}
		if info, serr := os.Stat(maskPath); serr == nil && info.Size() > opts.StudioConfig.MaxMaskBytes {
			return contracts.MaskEditOutput{}, apperr.Validation("mask image exceeds maximum allowed size").
				WithMsg("errors.studio.mask_too_large", nil)
		}

		aspect := strings.TrimSpace(in.AspectRatio)
		if aspect != "" && !slices.Contains(aspectOptions, aspect) {
			return contracts.MaskEditOutput{}, apperr.Validation("unsupported aspect_ratio").
				WithMsg("errors.studio.invalid_aspect_ratio", nil)
		}
		resolution := strings.TrimSpace(in.Resolution)
		if resolution != "" && !slices.Contains(resOptions, resolution) {
			return contracts.MaskEditOutput{}, apperr.Validation("unsupported resolution").
				WithMsg("errors.studio.invalid_resolution", nil)
		}

		if opts.ImageGen == nil {
			return contracts.MaskEditOutput{}, apperr.Unavailable("Image generator service is unavailable").
				WithMsg("errors.studio.image_service_unavailable", nil)
		}

		outDir, err := resolveNodeOutputDir(ctx, opts, in.OutputDir, filepath.Join("studio", "edits"))
		if err != nil {
			return contracts.MaskEditOutput{}, err
		}

		progress.Report(ctx, progress.Update{
			Percent: 20,
			Message: "Applying masked region edit…",
			Key:     "studio.progress.mask_edit",
		})

		res, err := opts.ImageGen.GenerateImage(ctx, domainmedia.ImageRequest{
			ModelRef:        in.ModelRef,
			Prompt:          in.Prompt,
			ReferenceImages: []string{imgPath},
			MaskPath:        maskPath,
			AspectRatio:     aspect,
			Resolution:      resolution,
			Seed:            in.Seed,
			OutputDir:       outDir,
		})
		if err != nil {
			return contracts.MaskEditOutput{}, err
		}
		assetID := registerStudioAsset(ctx, opts, "image", NodeTypeMaskEdit, res.Path, outDir)
		progress.Report(ctx, progress.Update{
			Percent: 100,
			Message: "Masked edit complete",
			Key:     "studio.progress.done",
		})
		return contracts.MaskEditOutput{
			Image:    engine.RefFor(res.Path, engine.MediaImage),
			Path:     res.Path,
			AssetID:  assetID,
			MimeType: res.MimeType,
			Provider: res.Provider,
			Model:    res.Model,
		}, nil
	})
}
