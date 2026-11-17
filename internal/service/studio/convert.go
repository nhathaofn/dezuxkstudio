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

func jobToDTO(j domainstudio.Job) contracts.StudioJob {
	return contracts.StudioJob{
		ID:            j.ID,
		ProjectID:     j.ProjectID,
		EpisodeID:     j.EpisodeID,
		ShotID:        j.ShotID,
		AssetID:       j.AssetID,
		JobKind:       string(j.JobKind),
		ProviderID:    j.ProviderID,
		ModelRef:      j.ModelRef,
		Status:        string(j.Status),
		ProviderJobID: j.ProviderJobID,
		Progress:      j.Progress,
		Attempts:      j.Attempts,
		MaxAttempts:   j.MaxAttempts,
		NextPollAt:    j.NextPollAt,
		OutputPath:    j.OutputPath,
		ErrorText:     j.ErrorText,
		EstimatedUSD:  j.EstimatedUSD,
		ActualUSD:     j.ActualUSD,
		CreatedAt:     j.CreatedAt,
		UpdatedAt:     j.UpdatedAt,
		StartedAt:     j.StartedAt,
		FinishedAt:    j.FinishedAt,
	}
}

func jobsToDTO(list []domainstudio.Job) []contracts.StudioJob {
	out := make([]contracts.StudioJob, len(list))
	for i := range list {
		out[i] = jobToDTO(list[i])
	}
	return out
}

func pricingToDTO(p domainstudio.PricingEntry) contracts.StudioPricingEntry {
	return contracts.StudioPricingEntry{
		ID:           p.ID,
		ProviderID:   p.ProviderID,
		ModelID:      p.ModelID,
		Capability:   p.Capability,
		Unit:         p.Unit,
		UnitPriceUSD: p.UnitPriceUSD,
		Currency:     p.Currency,
		UpdatedAt:    p.UpdatedAt,
	}
}

func costLedgerToDTO(c domainstudio.CostLedgerEntry) contracts.StudioCostLedgerEntry {
	return contracts.StudioCostLedgerEntry{
		ID:           c.ID,
		ProjectID:    c.ProjectID,
		EpisodeID:    c.EpisodeID,
		ShotID:       c.ShotID,
		JobID:        c.JobID,
		ProviderID:   c.ProviderID,
		ModelID:      c.ModelID,
		Capability:   c.Capability,
		Operation:    c.Operation,
		InputUnits:   c.InputUnits,
		OutputUnits:  c.OutputUnits,
		Unit:         c.Unit,
		EstimatedUSD: c.EstimatedUSD,
		ActualUSD:    c.ActualUSD,
		Status:       string(c.Status),
		CreatedAt:    c.CreatedAt,
	}
}

func mediaVersionToDTO(v domainstudio.MediaVersion) contracts.StudioMediaVersion {
	return contracts.StudioMediaVersion{
		ID:         v.ID,
		ProjectID:  v.ProjectID,
		EpisodeID:  v.EpisodeID,
		TargetKind: string(v.TargetKind),
		TargetID:   v.TargetID,
		VersionNum: v.VersionNum,
		MediaPath:  v.MediaPath,
		SourceNode: v.SourceNode,
		PromptUsed: v.PromptUsed,
		ModelRef:   v.ModelRef,
		Seed:       v.Seed,
		CostUSD:    v.CostUSD,
		QCStatus:   string(v.QCStatus),
		IsSelected: v.IsSelected,
		CreatedAt:  v.CreatedAt,
	}
}

func qcReportToDTO(r domainstudio.QCReport) contracts.StudioQCReport {
	issues := make([]contracts.QCReviewIssue, len(r.Issues))
	for i, iss := range r.Issues {
		issues[i] = contracts.QCReviewIssue{
			Severity:  iss.Severity,
			Category:  iss.Category,
			Message:   iss.Message,
			FramePath: iss.FramePath,
		}
	}
	return contracts.StudioQCReport{
		ID:             r.ID,
		ProjectID:      r.ProjectID,
		EpisodeID:      r.EpisodeID,
		TargetKind:     string(r.TargetKind),
		TargetID:       r.TargetID,
		MediaVersionID: r.MediaVersionID,
		MediaPath:      r.MediaPath,
		Verdict:        string(r.Verdict),
		OverallScore:   r.OverallScore,
		Issues:         issues,
		VisionSummary:  r.VisionSummary,
		VisionModelRef: r.VisionModelRef,
		SampleFrames:   append([]string(nil), r.SampleFrames...),
		CreatedAt:      r.CreatedAt,
	}
}

func editTimelineToDTO(tl domainstudio.EditTimeline) contracts.StudioEditTimeline {
	tracks := make([]contracts.StudioTimelineTrack, len(tl.Tracks))
	for i, tr := range tl.Tracks {
		clips := make([]contracts.StudioTimelineClip, len(tr.Clips))
		for j, cl := range tr.Clips {
			clips[j] = contracts.StudioTimelineClip{
				ID:              cl.ID,
				ShotID:          cl.ShotID,
				AssetID:         cl.AssetID,
				Label:           cl.Label,
				FilePath:        cl.FilePath,
				TimelineStartMs: cl.TimelineStartMs,
				SourceInMs:      cl.SourceInMs,
				SourceOutMs:     cl.SourceOutMs,
				Speed:           cl.Speed,
				Volume:          cl.Volume,
				TransitionIn:    cl.TransitionIn,
				TransitionMs:    cl.TransitionMs,
				KenBurnsPreset:  cl.KenBurnsPreset,
				SpritePath:      cl.SpritePath,
				SpriteAssetID:   cl.SpriteAssetID,
				Peaks:           append([]float64(nil), cl.Peaks...),
			}
		}
		tracks[i] = contracts.StudioTimelineTrack{
			ID:     tr.ID,
			Name:   tr.Name,
			Kind:   tr.Kind,
			Muted:  tr.Muted,
			Solo:   tr.Solo,
			Locked: tr.Locked,
			Volume: tr.Volume,
			Clips:  clips,
		}
	}
	subs := make([]contracts.StudioSubtitleCue, len(tl.Subtitles))
	for i, s := range tl.Subtitles {
		subs[i] = contracts.StudioSubtitleCue{
			ID:          s.ID,
			StartMs:     s.StartMs,
			EndMs:       s.EndMs,
			Speaker:     s.Speaker,
			Text:        s.Text,
			StylePreset: s.StylePreset,
		}
	}
	markers := make([]contracts.StudioTimelineMarker, len(tl.Markers))
	for i, m := range tl.Markers {
		markers[i] = contracts.StudioTimelineMarker{
			ID:     m.ID,
			TimeMs: m.TimeMs,
			Label:  m.Label,
			Color:  m.Color,
		}
	}
	return contracts.StudioEditTimeline{
		Version:   tl.Version,
		FPS:       tl.FPS,
		Width:     tl.Width,
		Height:    tl.Height,
		Tracks:    tracks,
		Subtitles: subs,
		Markers:   markers,
	}
}

func editTimelineFromDTO(tl *contracts.StudioEditTimeline) *domainstudio.EditTimeline {
	if tl == nil {
		return nil
	}
	tracks := make([]domainstudio.Track, len(tl.Tracks))
	for i, tr := range tl.Tracks {
		clips := make([]domainstudio.TimelineClip, len(tr.Clips))
		for j, cl := range tr.Clips {
			clips[j] = domainstudio.TimelineClip{
				ID:              cl.ID,
				ShotID:          cl.ShotID,
				AssetID:         cl.AssetID,
				Label:           cl.Label,
				FilePath:        cl.FilePath,
				TimelineStartMs: cl.TimelineStartMs,
				SourceInMs:      cl.SourceInMs,
				SourceOutMs:     cl.SourceOutMs,
				Speed:           cl.Speed,
				Volume:          cl.Volume,
				TransitionIn:    cl.TransitionIn,
				TransitionMs:    cl.TransitionMs,
				KenBurnsPreset:  cl.KenBurnsPreset,
				SpritePath:      cl.SpritePath,
				SpriteAssetID:   cl.SpriteAssetID,
				Peaks:           append([]float64(nil), cl.Peaks...),
			}
		}
		tracks[i] = domainstudio.Track{
			ID:     tr.ID,
			Name:   tr.Name,
			Kind:   tr.Kind,
			Muted:  tr.Muted,
			Solo:   tr.Solo,
			Locked: tr.Locked,
			Volume: tr.Volume,
			Clips:  clips,
		}
	}
	subs := make([]domainstudio.SubtitleCue, len(tl.Subtitles))
	for i, s := range tl.Subtitles {
		subs[i] = domainstudio.SubtitleCue{
			ID:          s.ID,
			StartMs:     s.StartMs,
			EndMs:       s.EndMs,
			Speaker:     s.Speaker,
			Text:        s.Text,
			StylePreset: s.StylePreset,
		}
	}
	markers := make([]domainstudio.TimelineMarker, len(tl.Markers))
	for i, m := range tl.Markers {
		markers[i] = domainstudio.TimelineMarker{
			ID:     m.ID,
			TimeMs: m.TimeMs,
			Label:  m.Label,
			Color:  m.Color,
		}
	}
	return &domainstudio.EditTimeline{
		Version:   tl.Version,
		FPS:       tl.FPS,
		Width:     tl.Width,
		Height:    tl.Height,
		Tracks:    tracks,
		Subtitles: subs,
		Markers:   markers,
	}
}

func timelineClipFromDTO(cl *contracts.StudioTimelineClip) *domainstudio.TimelineClip {
	if cl == nil {
		return nil
	}
	return &domainstudio.TimelineClip{
		ID:              cl.ID,
		ShotID:          cl.ShotID,
		AssetID:         cl.AssetID,
		Label:           cl.Label,
		FilePath:        cl.FilePath,
		TimelineStartMs: cl.TimelineStartMs,
		SourceInMs:      cl.SourceInMs,
		SourceOutMs:     cl.SourceOutMs,
		Speed:           cl.Speed,
		Volume:          cl.Volume,
		TransitionIn:    cl.TransitionIn,
		TransitionMs:    cl.TransitionMs,
		KenBurnsPreset:  cl.KenBurnsPreset,
		SpritePath:      cl.SpritePath,
		SpriteAssetID:   cl.SpriteAssetID,
		Peaks:           append([]float64(nil), cl.Peaks...),
	}
}

func timelineOpFromDTO(op contracts.StudioTimelineOp) domainstudio.TimelineOp {
	var sub *domainstudio.SubtitleCue
	if op.Subtitle != nil {
		sub = &domainstudio.SubtitleCue{
			ID:          op.Subtitle.ID,
			StartMs:     op.Subtitle.StartMs,
			EndMs:       op.Subtitle.EndMs,
			Speaker:     op.Subtitle.Speaker,
			Text:        op.Subtitle.Text,
			StylePreset: op.Subtitle.StylePreset,
		}
	}
	var mrk *domainstudio.TimelineMarker
	if op.Marker != nil {
		mrk = &domainstudio.TimelineMarker{
			ID:     op.Marker.ID,
			TimeMs: op.Marker.TimeMs,
			Label:  op.Marker.Label,
			Color:  op.Marker.Color,
		}
	}
	return domainstudio.TimelineOp{
		Op:              op.Op,
		Timeline:        editTimelineFromDTO(op.Timeline),
		FPS:             op.FPS,
		Width:           op.Width,
		Height:          op.Height,
		TrackID:         op.TrackID,
		TargetTrackID:   op.TargetTrackID,
		TrackKind:       op.TrackKind,
		TrackName:       op.TrackName,
		Muted:           op.Muted,
		Solo:            op.Solo,
		Locked:          op.Locked,
		Volume:          op.Volume,
		ClipID:          op.ClipID,
		NewClipID:       op.NewClipID,
		Clip:            timelineClipFromDTO(op.Clip),
		TimelineStartMs: op.TimelineStartMs,
		SourceInMs:      op.SourceInMs,
		SourceOutMs:     op.SourceOutMs,
		Speed:           op.Speed,
		TransitionIn:    op.TransitionIn,
		TransitionMs:    op.TransitionMs,
		KenBurnsPreset:  op.KenBurnsPreset,
		Label:           op.Label,
		SplitAtMs:       op.SplitAtMs,
		SubtitleID:      op.SubtitleID,
		Subtitle:        sub,
		MarkerID:        op.MarkerID,
		Marker:          mrk,
	}
}

func timelineOpsFromDTO(ops []contracts.StudioTimelineOp) []domainstudio.TimelineOp {
	out := make([]domainstudio.TimelineOp, len(ops))
	for i := range ops {
		out[i] = timelineOpFromDTO(ops[i])
	}
	return out
}

func timelineResultToDTO(projectID, timelinePath string, rec domainstudio.TimelineRecord, revs []domainstudio.TimelineRevision) contracts.StudioTimelineResult {
	revSummaries := make([]contracts.StudioTimelineRevisionSummary, len(revs))
	for i, r := range revs {
		revSummaries[i] = contracts.StudioTimelineRevisionSummary{
			ID:        r.ID,
			Revision:  r.Revision,
			Actor:     string(r.Actor),
			Summary:   r.Summary,
			CreatedAt: r.CreatedAt,
		}
	}
	return contracts.StudioTimelineResult{
		ProjectID:    projectID,
		EpisodeID:    rec.EpisodeID,
		Revision:     rec.Revision,
		CanUndo:      rec.CanUndo,
		CanRedo:      rec.CanRedo,
		DurationMs:   rec.Timeline.DurationMs(),
		TimelinePath: timelinePath,
		Timeline:     editTimelineToDTO(rec.Timeline),
		Revisions:    revSummaries,
		UpdatedAt:    rec.UpdatedAt,
	}
}
