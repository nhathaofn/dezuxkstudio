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

var normalizedScriptSchema = json.RawMessage(`{
	"type": "object",
	"additionalProperties": false,
	"required": ["title", "genre", "aspect_ratio", "logline", "scenes"],
	"properties": {
		"title": {"type": "string"},
		"genre": {"type": "string", "enum": ["drama", "narration"]},
		"aspect_ratio": {"type": "string"},
		"logline": {"type": "string"},
		"language": {"type": "string"},
		"characters": {"type": "array", "items": {"type": "string"}},
		"scenes": {
			"type": "array",
			"items": {
				"type": "object",
				"required": ["code", "title", "beats"],
				"properties": {
					"code": {"type": "string"},
					"title": {"type": "string"},
					"location": {"type": "string"},
					"time_of_day": {"type": "string"},
					"summary": {"type": "string"},
					"beats": {
						"type": "array",
						"items": {
							"type": "object",
							"required": ["code", "description", "duration_sec"],
							"properties": {
								"code": {"type": "string"},
								"description": {"type": "string"},
								"visual_hint": {"type": "string"},
								"camera_hint": {"type": "string"},
								"duration_sec": {"type": "integer"},
								"asset_tags": {"type": "array", "items": {"type": "string"}},
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
								},
								"narrations": {
									"type": "array",
									"items": {
										"type": "object",
										"required": ["text"],
										"properties": {
											"text": {"type": "string"},
											"tone": {"type": "string"},
											"estimated_sec": {"type": "number"}
										}
									}
								}
							}
						}
					}
				}
			}
		}
	}
}`)

// NewScriptNormalizeNode builds the tool.script_normalize workflow node.
func NewScriptNormalizeNode(opts Options) engine.Node {
	spec := engine.Spec{
		Type:        NodeTypeScriptNormalize,
		Provider:    "tool",
		Label:       "Screenplay Normalizer",
		Description: "Parse a raw screenplay or narration script into structured scenes, visual beats, dialogue lines, and narration segments",
		Inputs: []engine.Port{
			{Name: "raw_script", Label: "Raw screenplay text", Type: engine.PortText},
		},
		Outputs: []engine.Port{
			{Name: "normalized_script_json", Label: "Normalized script JSON", Type: engine.PortText},
		},
		Params: []engine.Param{
			{
				Name:    "genre",
				Label:   "Genre (drama or narration)",
				Kind:    engine.ParamSelect,
				Options: optionStrings(domainstudio.Genres()),
				Default: string(domainstudio.GenreDrama),
			},
			{Name: "episode_title", Label: "Episode title (optional)", Kind: engine.ParamText},
			{
				Name:    "aspect_ratio",
				Label:   "Target aspect ratio",
				Kind:    engine.ParamSelect,
				Options: domainmedia.VideoAspectRatios(),
				Default: domainmedia.DefaultVideoAspectRatio,
			},
			{Name: "model", Label: "Text LLM model (empty = Auto default)", Kind: engine.ParamText},
			{Name: "project_dir", Label: "Episode or project directory (optional)", Kind: engine.ParamText, Role: engine.ParamRoleOutputDir},
		},
	}

	return engine.Typed(spec, func(ctx context.Context, in contracts.ScriptNormalizeInput) (contracts.ScriptNormalizeOutput, error) {
		if opts.TextGen == nil {
			return contracts.ScriptNormalizeOutput{}, apperr.Unavailable("Text generation service is unavailable")
		}
		rawScript := strings.TrimSpace(in.RawScript)
		if rawScript == "" {
			return contracts.ScriptNormalizeOutput{}, apperr.Validation("raw_script is required")
		}

		genre := domainstudio.Genre(strings.TrimSpace(in.Genre))
		if genre == "" {
			genre = domainstudio.GenreDrama
		}
		if err := domainstudio.ValidateGenre(genre); err != nil {
			return contracts.ScriptNormalizeOutput{}, err
		}

		aspect := strings.TrimSpace(in.AspectRatio)
		if aspect == "" {
			aspect = domainmedia.DefaultVideoAspectRatio
		}
		title := strings.TrimSpace(in.EpisodeTitle)
		if title == "" {
			title = "Episode 1"
		}

		progress.Report(ctx, progress.Update{
			Percent: 15,
			Message: "Analyzing and normalizing screenplay…",
			Key:     "studio.progress.script_normalize",
		})

		sysPrompt := "You are an expert film director and screenplay structurer. " +
			"Convert the provided script into a clean, faithful NormalizedScript JSON object preserving the original story language in dialogue and narration. " +
			"Split each scene into concise visual beats (typically 4-8 seconds each) suitable for AI video shot production. " +
			"For genre='drama', populate beat.dialogues with speaker lines; for genre='narration', populate beat.narrations with voiceover segments. " +
			"Use consistent @Tag identifiers in beat.asset_tags (e.g. @Hero, @Cafe)."

		userPrompt := fmt.Sprintf("Genre: %s\nEpisode Title: %s\nTarget Aspect Ratio: %s\n\nRaw Script:\n%s",
			genre, title, aspect, rawScript)

		res, err := opts.TextGen.GenerateText(ctx, domainmedia.TextRequest{
			ModelRef:       strings.TrimSpace(in.Model),
			SystemPrompt:   sysPrompt,
			UserPrompt:     userPrompt,
			JSONSchemaName: "normalized_script",
			JSONSchema:     normalizedScriptSchema,
		})
		if err != nil {
			return contracts.ScriptNormalizeOutput{}, err
		}

		var script domainstudio.NormalizedScript
		if err := json.Unmarshal(res.ParsedJSON, &script); err != nil {
			return contracts.ScriptNormalizeOutput{}, apperr.Unavailable("Failed to decode normalized script JSON: " + err.Error())
		}
		if strings.TrimSpace(script.Title) == "" {
			script.Title = title
		}
		script.Genre = genre
		if strings.TrimSpace(script.AspectRatio) == "" {
			script.AspectRatio = aspect
		}

		beatCount := 0
		for si := range script.Scenes {
			if strings.TrimSpace(script.Scenes[si].Code) == "" {
				script.Scenes[si].Code = fmt.Sprintf("S%02d", si+1)
			}
			for bi := range script.Scenes[si].Beats {
				beatCount++
				if strings.TrimSpace(script.Scenes[si].Beats[bi].Code) == "" {
					script.Scenes[si].Beats[bi].Code = fmt.Sprintf("B%02d", beatCount)
				}
				if script.Scenes[si].Beats[bi].DurationSec <= 0 {
					script.Scenes[si].Beats[bi].DurationSec = domainmedia.DefaultVideoDurationSec
				}
			}
		}

		pretty, err := json.MarshalIndent(script, "", "  ")
		if err != nil {
			return contracts.ScriptNormalizeOutput{}, apperr.Internal(err)
		}

		scriptPath, err := writeProjectArtifact(ctx, opts, in.ProjectDir, "script", "normalized_script.json", append(pretty, '\n'))
		if err != nil {
			return contracts.ScriptNormalizeOutput{}, err
		}

		progress.Report(ctx, progress.Update{
			Percent: 100,
			Message: "Script normalized",
			Key:     "studio.progress.done",
		})

		return contracts.ScriptNormalizeOutput{
			NormalizedScriptJSON: string(pretty),
			ScriptPath:           scriptPath,
			SceneCount:           len(script.Scenes),
			BeatCount:            beatCount,
			Provider:             res.Provider,
			Model:                res.Model,
		}, nil
	})
}
