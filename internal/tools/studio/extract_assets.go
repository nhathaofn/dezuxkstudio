package studio

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"app/internal/apperr"
	"app/internal/contracts"
	domainmedia "app/internal/domain/mediagen"
	domainstudio "app/internal/domain/studio"
	"app/internal/engine"
	"app/internal/progress"
)

var assetBibleSchema = json.RawMessage(`{
	"type": "object",
	"additionalProperties": false,
	"required": ["assets"],
	"properties": {
		"style_prompt": {"type": "string"},
		"assets": {
			"type": "array",
			"items": {
				"type": "object",
				"required": ["kind", "name", "code_tag", "description", "prompt"],
				"properties": {
					"kind": {"type": "string", "enum": ["character", "scene", "prop"]},
					"name": {"type": "string"},
					"code_tag": {"type": "string"},
					"description": {"type": "string"},
					"prompt": {"type": "string"}
				}
			}
		}
	}
}`)

// NewExtractAssetsNode builds the tool.extract_assets workflow node.
func NewExtractAssetsNode(opts Options) engine.Node {
	opts.StudioConfig = withStudioDefaults(opts.StudioConfig)
	spec := engine.Spec{
		Type:        NodeTypeExtractAssets,
		Provider:    studioProviderID,
		Label:       "Asset Bible Extractor",
		Description: "Extract recurring characters, scenes/locations, and key props with consistent @Tags and reference-sheet image prompts from a normalized script",
		Inputs: []engine.Port{
			{Name: "normalized_script_json", Label: "Normalized script JSON", Type: engine.PortText},
		},
		Outputs: []engine.Port{
			{Name: "asset_bible_json", Label: "Asset bible JSON", Type: engine.PortText},
		},
		Params: []engine.Param{
			{Name: "style_prompt", Label: "Visual style bible prompt (optional)", Kind: engine.ParamText},
			{Name: "model", Label: "Text LLM model (empty = Auto default)", Kind: engine.ParamText},
			{Name: "project_dir", Label: "Episode or project directory (optional)", Kind: engine.ParamText, Role: engine.ParamRoleOutputDir},
		},
	}

	return engine.Typed(spec, func(ctx context.Context, in contracts.ExtractAssetsInput) (contracts.ExtractAssetsOutput, error) {
		if opts.TextGen == nil {
			return contracts.ExtractAssetsOutput{}, apperr.Unavailable("Text generation service is unavailable")
		}
		scriptRaw := strings.TrimSpace(in.NormalizedScriptJSON)
		if scriptRaw == "" {
			return contracts.ExtractAssetsOutput{}, apperr.Validation("normalized_script_json is required")
		}
		if !json.Valid([]byte(scriptRaw)) {
			return contracts.ExtractAssetsOutput{}, apperr.Validation("normalized_script_json must be valid JSON")
		}

		progress.Report(ctx, progress.Update{
			Percent: 15,
			Message: "Extracting characters, scenes, and props…",
			Key:     "studio.progress.extract_assets",
		})

		sysPrompt := "You are a film production designer creating a visual Asset Bible. " +
			"Extract every distinct recurring character, scene/location, and important prop from the normalized script. " +
			"Assign a unique ASCII @Tag to each asset (e.g. @Minh, @OldCafe, @SilverKey). " +
			"For characters, write a detailed English visual prompt for a clean 3-view character turnaround reference sheet on neutral background, preserving clothing, age, hair, and facial consistency. " +
			"For scenes, write an establishing environment concept sheet prompt with consistent lighting and architecture. " +
			"Incorporate the provided visual style prompt when present."

		userPrompt := fmt.Sprintf("Visual Style Prompt: %s\n\nNormalized Script JSON:\n%s",
			strings.TrimSpace(in.StylePrompt), scriptRaw)

		res, err := opts.TextGen.GenerateText(ctx, domainmedia.TextRequest{
			ModelRef:       strings.TrimSpace(in.Model),
			SystemPrompt:   sysPrompt,
			UserPrompt:     userPrompt,
			JSONSchemaName: "asset_bible",
			JSONSchema:     assetBibleSchema,
		})
		if err != nil {
			return contracts.ExtractAssetsOutput{}, err
		}

		var pkg domainstudio.AssetBiblePackage
		if err := json.Unmarshal(res.ParsedJSON, &pkg); err != nil {
			return contracts.ExtractAssetsOutput{}, apperr.Wrap(apperr.CodeUnavailable, "Failed to decode asset bible JSON", err).
				WithMsg("errors.studio.llm_invalid_json", nil)
		}
		if strings.TrimSpace(in.StylePrompt) != "" && strings.TrimSpace(pkg.StylePrompt) == "" {
			pkg.StylePrompt = strings.TrimSpace(in.StylePrompt)
		}

		seenTags := make(map[string]bool, len(pkg.Assets))
		cleanAssets := make([]domainstudio.AssetEntry, 0, len(pkg.Assets))
		for _, a := range pkg.Assets {
			a.Normalize()
			if a.Name == "" || a.CodeTag == "" {
				continue
			}
			if domainstudio.ValidateAssetKind(a.Kind) != nil {
				a.Kind = domainstudio.AssetCharacter
			}
			key := strings.ToLower(a.CodeTag)
			if seenTags[key] {
				continue
			}
			seenTags[key] = true
			cleanAssets = append(cleanAssets, a)
		}
		pkg.Assets = cleanAssets

		pretty, err := json.MarshalIndent(pkg, "", "  ")
		if err != nil {
			return contracts.ExtractAssetsOutput{}, apperr.Internal(err)
		}

		assetsPath, err := writeProjectArtifact(ctx, opts, in.ProjectDir, "assets", "asset_bible.json", append(pretty, '\n'))
		if err != nil {
			return contracts.ExtractAssetsOutput{}, err
		}

		progress.Report(ctx, progress.Update{
			Percent: 100,
			Message: "Asset bible extracted",
			Key:     "studio.progress.done",
		})

		return contracts.ExtractAssetsOutput{
			AssetBibleJSON: string(pretty),
			AssetsPath:     assetsPath,
			AssetCount:     len(pkg.Assets),
			Provider:       res.Provider,
			Model:          res.Model,
		}, nil
	})
}
