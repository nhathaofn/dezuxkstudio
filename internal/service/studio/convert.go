package studio

import (
	"app/internal/contracts"
	domainstudio "app/internal/domain/studio"
)

func projectToDTO(p domainstudio.Project) contracts.StudioProject {
	return contracts.StudioProject{
		ID:                 p.ID,
		Slug:               p.Slug,
		Title:              p.Title,
		Genre:              p.Genre,
		ProductionMode:     p.ProductionMode,
		AspectRatio:        p.AspectRatio,
		StylePrompt:        p.StylePrompt,
		RootDir:            p.RootDir,
		AutoApproveBatches: p.AutoApproveBatches,
		DefaultTextModel:   p.DefaultTextModel,
		DefaultImageModel:  p.DefaultImageModel,
		DefaultVideoModel:  p.DefaultVideoModel,
		DefaultTTSModel:    p.DefaultTTSModel,
		CreatedAt:          p.CreatedAt,
		UpdatedAt:          p.UpdatedAt,
	}
}

func episodeToDTO(e domainstudio.Episode) contracts.StudioEpisode {
	return contracts.StudioEpisode{
		ID:               e.ID,
		ProjectID:        e.ProjectID,
		Code:             e.Code,
		Title:            e.Title,
		Stage:            e.Stage,
		RawScriptPath:    e.RawScriptPath,
		ScriptJSONPath:   e.ScriptJSONPath,
		AssetsJSONPath:   e.AssetsJSONPath,
		ShotsJSONPath:    e.ShotsJSONPath,
		TimelineJSONPath: e.TimelineJSONPath,
		CreatedAt:        e.CreatedAt,
		UpdatedAt:        e.UpdatedAt,
	}
}

func assetToDTO(a domainstudio.AssetEntry) contracts.StudioAsset {
	return contracts.StudioAsset{
		ID:             a.ID,
		ProjectID:      a.ProjectID,
		Kind:           a.Kind,
		Name:           a.Name,
		CodeTag:        a.CodeTag,
		Description:    a.Description,
		Prompt:         a.Prompt,
		PrimaryAssetID: a.PrimaryAssetID,
		PrimaryPath:    a.PrimaryPath,
		VoiceProvider:  a.VoiceProvider,
		VoiceID:        a.VoiceID,
		VoiceStyle:     a.VoiceStyle,
		UpdatedAt:      a.UpdatedAt,
	}
}

func assetsToDTO(list []domainstudio.AssetEntry) []contracts.StudioAsset {
	out := make([]contracts.StudioAsset, len(list))
	for i := range list {
		out[i] = assetToDTO(list[i])
	}
	return out
}

func dialoguesToDTO(list []domainstudio.DialogueLine) []contracts.StudioDialogueLine {
	if len(list) == 0 {
		return nil
	}
	out := make([]contracts.StudioDialogueLine, len(list))
	for i, d := range list {
		out[i] = contracts.StudioDialogueLine{
			Speaker: d.Speaker,
			Line:    d.Line,
			Emotion: d.Emotion,
			Cue:     d.Cue,
		}
	}
	return out
}

func dialoguesFromDTO(list []contracts.StudioDialogueLine) []domainstudio.DialogueLine {
	if len(list) == 0 {
		return nil
	}
	out := make([]domainstudio.DialogueLine, len(list))
	for i, d := range list {
		out[i] = domainstudio.DialogueLine{
			Speaker: d.Speaker,
			Line:    d.Line,
			Emotion: d.Emotion,
			Cue:     d.Cue,
		}
	}
	return out
}

func shotToDTO(s domainstudio.Shot) contracts.StudioShot {
	return contracts.StudioShot{
		ID:              s.ID,
		EpisodeID:       s.EpisodeID,
		Seq:             s.Seq,
		SceneCode:       s.SceneCode,
		Title:           s.Title,
		ShotMode:        s.ShotMode,
		DurationSec:     s.DurationSec,
		ImagePrompt:     s.ImagePrompt,
		VideoPrompt:     s.VideoPrompt,
		NarrationText:   s.NarrationText,
		Dialogues:       dialoguesToDTO(s.Dialogues),
		RefAssetTags:    append([]string(nil), s.RefAssetTags...),
		KeyframeAssetID: s.KeyframeAssetID,
		KeyframePath:    s.KeyframePath,
		VideoAssetID:    s.VideoAssetID,
		VideoPath:       s.VideoPath,
		AudioAssetID:    s.AudioAssetID,
		AudioPath:       s.AudioPath,
		Status:          s.Status,
		ErrorText:       s.ErrorText,
		UpdatedAt:       s.UpdatedAt,
	}
}

func shotsToDTO(list []domainstudio.Shot) []contracts.StudioShot {
	out := make([]contracts.StudioShot, len(list))
	for i := range list {
		out[i] = shotToDTO(list[i])
	}
	return out
}

func normalizedScriptToDTO(ns domainstudio.NormalizedScript) contracts.StudioNormalizedScript {
	scenes := make([]contracts.StudioScriptScene, len(ns.Scenes))
	for si, sc := range ns.Scenes {
		beats := make([]contracts.StudioScriptBeat, len(sc.Beats))
		for bi, bt := range sc.Beats {
			nars := make([]contracts.StudioNarrationSegment, len(bt.Narrations))
			for ni, nr := range bt.Narrations {
				nars[ni] = contracts.StudioNarrationSegment{
					Text:         nr.Text,
					Tone:         nr.Tone,
					EstimatedSec: nr.EstimatedSec,
				}
			}
			beats[bi] = contracts.StudioScriptBeat{
				Code:        bt.Code,
				Description: bt.Description,
				VisualHint:  bt.VisualHint,
				CameraHint:  bt.CameraHint,
				DurationSec: bt.DurationSec,
				AssetTags:   append([]string(nil), bt.AssetTags...),
				Dialogues:   dialoguesToDTO(bt.Dialogues),
				Narrations:  nars,
			}
		}
		scenes[si] = contracts.StudioScriptScene{
			Code:      sc.Code,
			Title:     sc.Title,
			Location:  sc.Location,
			TimeOfDay: sc.TimeOfDay,
			Summary:   sc.Summary,
			Beats:     beats,
		}
	}
	return contracts.StudioNormalizedScript{
		Title:       ns.Title,
		Genre:       ns.Genre,
		AspectRatio: ns.AspectRatio,
		Logline:     ns.Logline,
		Language:    ns.Language,
		Characters:  append([]string(nil), ns.Characters...),
		Scenes:      scenes,
	}
}

func normalizedScriptFromDTO(ns contracts.StudioNormalizedScript) domainstudio.NormalizedScript {
	scenes := make([]domainstudio.Scene, len(ns.Scenes))
	for si, sc := range ns.Scenes {
		beats := make([]domainstudio.Beat, len(sc.Beats))
		for bi, bt := range sc.Beats {
			nars := make([]domainstudio.NarrationSegment, len(bt.Narrations))
			for ni, nr := range bt.Narrations {
				nars[ni] = domainstudio.NarrationSegment{
					Text:         nr.Text,
					Tone:         nr.Tone,
					EstimatedSec: nr.EstimatedSec,
				}
			}
			beats[bi] = domainstudio.Beat{
				Code:        bt.Code,
				Description: bt.Description,
				VisualHint:  bt.VisualHint,
				CameraHint:  bt.CameraHint,
				DurationSec: bt.DurationSec,
				AssetTags:   append([]string(nil), bt.AssetTags...),
				Dialogues:   dialoguesFromDTO(bt.Dialogues),
				Narrations:  nars,
			}
		}
		scenes[si] = domainstudio.Scene{
			Code:      sc.Code,
			Title:     sc.Title,
			Location:  sc.Location,
			TimeOfDay: sc.TimeOfDay,
			Summary:   sc.Summary,
			Beats:     beats,
		}
	}
	return domainstudio.NormalizedScript{
		Title:       ns.Title,
		Genre:       ns.Genre,
		AspectRatio: ns.AspectRatio,
		Logline:     ns.Logline,
		Language:    ns.Language,
		Characters:  append([]string(nil), ns.Characters...),
		Scenes:      scenes,
	}
}
