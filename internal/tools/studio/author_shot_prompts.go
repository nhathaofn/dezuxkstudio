package studio

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"app/internal/apperr"
	"app/internal/contracts"
	domainmedia "app/internal/domain/mediagen"
	domainstudio "app/internal/domain/studio"
	"app/internal/engine"
	"app/internal/progress"
)

var shotPromptPackageSchema = json.RawMessage(`{
	"type": "object",
	"additionalProperties": false,
	"required": ["production_mode", "shots"],
	"properties": {
		"episode_title": {"type": "string"},
		"production_mode": {"type": "string", "enum": ["storyboard_i2v", "r2v"]},
		"shots": {
			"type": "array",
			"items": {
				"type": "object",
				"required": ["seq", "scene_code", "title", "shot_mode", "duration_sec", "image_prompt", "video_prompt"],
				"properties": {
					"seq": {"type": "integer"},
					"scene_code": {"type": "string"},
					"title": {"type": "string"},
					"shot_mode": {"type": "string", "enum": ["single", "grid_2x2", "grid_3x3", "r2v"]},
					"duration_sec": {"type": "integer"},
					"image_prompt": {"type": "string"},
					"video_prompt": {"type": "string"},
					"narration_text": {"type": "string"},
					"ref_asset_tags": {"type": "array", "items": {"type": "string"}},
					"dialogues": {
						"type": "array",
						"items": {
							"type": "object",
							"required": ["speaker", "line"],
							"properties": {
								"speaker": {"type": "string"},
								"line": {"type": "string"},
								"emotion": {"type": "string"},
								"cue": {"type": "string"}
							}
						}
					}
				}
			}
		}
	}
}`)

// NewAuthorShotPromptsNode builds the tool.author_shot_prompts workflow node.
func NewAuthorShotPromptsNode(opts Options) engine.Node {
	opts.StudioConfig = withStudioDefaults(opts.StudioConfig)
	spec := engine.Spec{
		Type:        NodeTypeAuthorShotPrompts,
		Provider:    studioProviderID,
		Label:       "Shot Prompt Author",
		Description: "Convert a normalized script and asset bible into an ordered storyboard shot list with tailored image_prompt, video_prompt, @Tag references, dialogue, and narration text",
		Inputs: []engine.Port{
			{Name: "normalized_script_json", Label: "Normalized script JSON", Type: engine.PortText},
			{Name: "asset_bible_json", Label: "Asset bible JSON (optional)", Type: engine.PortText, Optional: true},
		},
		Outputs: []engine.Port{
			{Name: "shots_json", Label: "Storyboard shots JSON", Type: engine.PortText},
		},
		Params: []engine.Param{
			{
				Name:    "production_mode",
				Label:   "Production mode (storyboard_i2v or r2v)",
				Kind:    engine.ParamSelect,
				Options: optionStrings(domainstudio.ProductionModes()),
				Default: string(domainstudio.ModeStoryboardI2V),
			},
			{
				Name:    "shot_mode_default",
				Label:   "Default shot framing mode",
				Kind:    engine.ParamSelect,
				Options: optionStrings(domainstudio.ShotModes()),
				Default: string(domainstudio.ShotModeSingle),
			},
			{
				Name:    "default_duration_sec",
				Label:   "Default shot duration (s)",
				Kind:    engine.ParamInteger,
				Min:     domainstudio.MinShotDurationSec,
				Max:     opts.StudioConfig.MaxShotDurationSec,
				Default: strconv.Itoa(opts.StudioConfig.DefaultShotDurationSec),
			},
			{Name: "style_prompt", Label: "Visual style prompt (optional)", Kind: engine.ParamText},
			{Name: "model", Label: "Text LLM model (empty = Auto default)", Kind: engine.ParamText},
			{Name: "project_dir", Label: "Episode or project directory (optional)", Kind: engine.ParamText, Role: engine.ParamRoleOutputDir},
		},
	}

	return engine.Typed(spec, func(ctx context.Context, in contracts.AuthorShotPromptsInput) (contracts.AuthorShotPromptsOutput, error) {
		if opts.TextGen == nil {
			return contracts.AuthorShotPromptsOutput{}, apperr.Unavailable("Text generation service is unavailable")
		}
		scriptRaw := strings.TrimSpace(in.NormalizedScriptJSON)
		if scriptRaw == "" {
			return contracts.AuthorShotPromptsOutput{}, apperr.Validation("normalized_script_json is required")
		}
		if !json.Valid([]byte(scriptRaw)) {
			return contracts.AuthorShotPromptsOutput{}, apperr.Validation("normalized_script_json must be valid JSON")
		}
		bibleRaw := strings.TrimSpace(in.AssetBibleJSON)
		if bibleRaw != "" && !json.Valid([]byte(bibleRaw)) {
			return contracts.AuthorShotPromptsOutput{}, apperr.Validation("asset_bible_json must be valid JSON")
		}

		prodMode := domainstudio.ProductionMode(strings.TrimSpace(in.ProductionMode))
		if prodMode == "" {
			prodMode = domainstudio.ModeStoryboardI2V
		}
		if err := domainstudio.ValidateProductionMode(prodMode); err != nil {
			return contracts.AuthorShotPromptsOutput{}, err
		}

		shotModeDef := domainstudio.ShotMode(strings.TrimSpace(in.ShotModeDefault))
		if shotModeDef == "" {
			if prodMode == domainstudio.ModeR2V {
				shotModeDef = domainstudio.ShotModeR2V
			} else {
				shotModeDef = domainstudio.ShotModeSingle
			}
		}
		if err := domainstudio.ValidateShotMode(shotModeDef); err != nil {
			return contracts.AuthorShotPromptsOutput{}, err
		}

		defaultDur := opts.StudioConfig.DefaultShotDurationSec
		maxDur := opts.StudioConfig.MaxShotDurationSec
		if in.DefaultDurationSec != 0 {
			if in.DefaultDurationSec < domainstudio.MinShotDurationSec || in.DefaultDurationSec > maxDur {
				return contracts.AuthorShotPromptsOutput{}, apperr.Validation(fmt.Sprintf("default_duration_sec must be between %d and %d", domainstudio.MinShotDurationSec, maxDur)).
					WithMsg("errors.studio.invalid_duration", map[string]any{"min": domainstudio.MinShotDurationSec, "max": maxDur})
			}
			defaultDur = in.DefaultDurationSec
		}

		progress.Report(ctx, progress.Update{
			Percent: 15,
			Message: "Authoring storyboard and video prompts…",
			Key:     "studio.progress.author_shots",
		})

		sysPrompt := fmt.Sprintf("You are an AI cinematography supervisor. Transform every beat of the normalized script into concrete, sequential video shots. "+
			"For each shot, write: (1) image_prompt (detailed English keyframe composition, lighting, character wardrobe/appearance, framing), "+
			"(2) video_prompt (motion, camera movement, acting beats, lip/audio cues; preserve dialogue cues when Drama), "+
			"(3) ref_asset_tags matching the @Tags in the asset bible, "+
			"(4) narration_text and dialogues preserved verbatim in the original script language. "+
			"Keep duration_sec realistic (%d-%d seconds per shot, default %ds).",
			domainstudio.MinShotDurationSec, maxDur, defaultDur)

		userPrompt := fmt.Sprintf("Production Mode: %s\nDefault Shot Mode: %s\nDefault Shot Duration (s): %d\nStyle Prompt: %s\n\nNormalized Script JSON:\n%s\n\nAsset Bible JSON:\n%s",
			prodMode, shotModeDef, defaultDur, strings.TrimSpace(in.StylePrompt), scriptRaw, bibleRaw)

		res, err := opts.TextGen.GenerateText(ctx, domainmedia.TextRequest{
			ModelRef:       strings.TrimSpace(in.Model),
			SystemPrompt:   sysPrompt,
			UserPrompt:     userPrompt,
			JSONSchemaName: "shot_prompt_package",
			JSONSchema:     shotPromptPackageSchema,
		})
		if err != nil {
			return contracts.AuthorShotPromptsOutput{}, err
		}

		var pkg domainstudio.ShotPromptPackage
		if err := json.Unmarshal(res.ParsedJSON, &pkg); err != nil {
			return contracts.AuthorShotPromptsOutput{}, apperr.Wrap(apperr.CodeUnavailable, "Failed to decode shots JSON", err).
				WithMsg("errors.studio.llm_invalid_json", nil)
		}
		pkg.ProductionMode = prodMode

		for i := range pkg.Shots {
			pkg.Shots[i].Seq = i + 1
			if pkg.Shots[i].ShotMode == "" {
				pkg.Shots[i].ShotMode = shotModeDef
			}
			if pkg.Shots[i].Status == "" {
				pkg.Shots[i].Status = domainstudio.ShotPending
			}
			pkg.Shots[i].NormalizeWithDefaultDuration(defaultDur)
			if pkg.Shots[i].DurationSec > maxDur {
				pkg.Shots[i].DurationSec = maxDur
			}
		}

		pretty, err := json.MarshalIndent(pkg, "", "  ")
		if err != nil {
			return contracts.AuthorShotPromptsOutput{}, apperr.Internal(err)
		}

		shotsPath, err := writeProjectArtifact(ctx, opts, in.ProjectDir, "storyboard", "shots.json", append(pretty, '\n'))
		if err != nil {
			return contracts.AuthorShotPromptsOutput{}, err
		}

		progress.Report(ctx, progress.Update{
			Percent: 100,
			Message: "Shot prompts authored",
			Key:     "studio.progress.done",
		})

		return contracts.AuthorShotPromptsOutput{
			ShotsJSON: string(pretty),
			ShotsPath: shotsPath,
			ShotCount: len(pkg.Shots),
			Provider:  res.Provider,
			Model:     res.Model,
		}, nil
	})
}
