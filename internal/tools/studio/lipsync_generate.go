package studio

import (
	"context"
	"fmt"
	"path/filepath"

	"app/internal/apperr"
	"app/internal/contracts"
	domainmedia "app/internal/domain/mediagen"
	"app/internal/engine"
	"app/internal/progress"
)

// NewLipSyncGenerateNode returns tool.lipsync_generate for driving character mouth sync with audio tracks.
func NewLipSyncGenerateNode(opts Options) engine.Node {
	opts.StudioConfig = withStudioDefaults(opts.StudioConfig)
	maxFace := opts.StudioConfig.LipSyncMaxFaceIndex

	spec := engine.Spec{
		Type:        NodeTypeLipSyncGenerate,
		Provider:    studioProviderID,
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
			{Name: "face_index", Label: "Target Face Index", Kind: engine.ParamInteger, Min: 0, Max: maxFace, Default: "0"},
			{Name: "prompt", Label: "Guidance Prompt", Kind: engine.ParamText},
			{Name: "output_dir", Label: "Output Folder", Kind: engine.ParamText, Role: engine.ParamRoleOutputDir},
		},
	}

	return engine.Typed(spec, func(ctx context.Context, in contracts.LipSyncGenerateInput) (contracts.LipSyncGenerateOutput, error) {
		if in.FaceIndex < 0 || in.FaceIndex > maxFace {
			return contracts.LipSyncGenerateOutput{}, apperr.Validation(fmt.Sprintf("face_index must be between 0 and %d", maxFace)).
				WithMsg("errors.studio.invalid_face_index", map[string]any{"max": maxFace})
		}
		vidPath, err := resolveMediaInput(ctx, opts, in.VideoPath.LocalURL, inputKindVideo)
		if err != nil {
			return contracts.LipSyncGenerateOutput{}, err
		}
		audPath, err := resolveMediaInput(ctx, opts, in.AudioPath.LocalURL, inputKindAudio)
		if err != nil {
			return contracts.LipSyncGenerateOutput{}, err
		}
		if vidPath == "" || audPath == "" {
			return contracts.LipSyncGenerateOutput{}, apperr.Validation("both video_path and audio_path are required").
				WithMsg("errors.studio.lipsync_inputs_required", nil)
		}
		if opts.LipSyncGen == nil {
			return contracts.LipSyncGenerateOutput{}, apperr.Unavailable("Lip-sync generator service is unavailable").
				WithMsg("errors.studio.lipsync_service_unavailable", nil)
		}

		outDir, err := resolveNodeOutputDir(ctx, opts, in.OutputDir, filepath.Join("studio", "lipsync"))
		if err != nil {
			return contracts.LipSyncGenerateOutput{}, err
		}

		progress.Report(ctx, progress.Update{
			Percent: 15,
			Message: "Generating lip-synced video…",
			Key:     "studio.progress.lipsync_generate",
		})

		res, err := opts.LipSyncGen.GenerateLipSync(ctx, domainmedia.LipSyncRequest{
			ModelRef:  in.ModelRef,
			VideoPath: vidPath,
			AudioPath: audPath,
			FaceIndex: in.FaceIndex,
			Prompt:    in.Prompt,
			OutputDir: outDir,
		})
		if err != nil {
			return contracts.LipSyncGenerateOutput{}, err
		}
		assetID := registerStudioAsset(ctx, opts, "video", NodeTypeLipSyncGenerate, res.Path, outDir)
		progress.Report(ctx, progress.Update{
			Percent: 100,
			Message: "Lip-sync video ready",
			Key:     "studio.progress.done",
		})
		return contracts.LipSyncGenerateOutput{
			Video:    engine.RefFor(res.Path, engine.MediaVideo),
			Path:     res.Path,
			AssetID:  assetID,
			MimeType: res.MimeType,
			Provider: res.Provider,
			Model:    res.Model,
		}, nil
	})
}
