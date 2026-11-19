package agent

import (
	"context"
	"fmt"
	"strings"

	"app/internal/contracts"
	domainstudio "app/internal/domain/studio"
	"app/internal/engine"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type studioTimelineGetToolInput struct {
	EpisodeID string `json:"episode_id" jsonschema:"Studio episode ID"`
}

type studioTimelineClipToolInput struct {
	ID              string  `json:"id,omitempty"`
	ShotID          string  `json:"shot_id,omitempty"`
	AssetID         string  `json:"asset_id,omitempty"`
	Label           string  `json:"label,omitempty"`
	FilePath        string  `json:"file_path,omitempty"`
	TimelineStartMs int64   `json:"timeline_start_ms,omitempty"`
	SourceInMs      int64   `json:"source_in_ms,omitempty"`
	SourceOutMs     int64   `json:"source_out_ms,omitempty"`
	Speed           float64 `json:"speed,omitempty"`
	Volume          float64 `json:"volume,omitempty"`
	Mute            bool    `json:"mute,omitempty"`
	TransitionIn    string  `json:"transition_in,omitempty"`
	TransitionMs    int64   `json:"transition_ms,omitempty"`
	KenBurnsPreset  string  `json:"ken_burns_preset,omitempty"`
	SpritePath      string  `json:"sprite_path,omitempty"`
	SpriteAssetID   string  `json:"sprite_asset_id,omitempty"`
}

type studioTimelineTrackToolInput struct {
	ID     string                        `json:"id,omitempty"`
	Name   string                        `json:"name,omitempty"`
	Kind   string                        `json:"kind,omitempty"`
	Muted  bool                          `json:"muted,omitempty"`
	Solo   bool                          `json:"solo,omitempty"`
	Locked bool                          `json:"locked,omitempty"`
	Volume float64                       `json:"volume,omitempty"`
	Clips  []studioTimelineClipToolInput `json:"clips,omitempty"`
}

type studioSubtitleCueToolInput struct {
	ID          string `json:"id,omitempty"`
	StartMs     int64  `json:"start_ms,omitempty"`
	EndMs       int64  `json:"end_ms,omitempty"`
	Speaker     string `json:"speaker,omitempty"`
	Text        string `json:"text,omitempty"`
	StylePreset string `json:"style_preset,omitempty"`
}

type studioEditTimelineToolInput struct {
	Version   int                              `json:"version,omitempty"`
	FPS       int                              `json:"fps,omitempty"`
	Width     int                              `json:"width,omitempty"`
	Height    int                              `json:"height,omitempty"`
	Tracks    []studioTimelineTrackToolInput   `json:"tracks,omitempty"`
	Subtitles []studioSubtitleCueToolInput     `json:"subtitles,omitempty"`
	Markers   []contracts.StudioTimelineMarker `json:"markers,omitempty"`
}

type studioTimelineOpToolInput struct {
	Op              string                          `json:"op" jsonschema:"replace_timeline|set_format|add_track|update_track|remove_track|add_clip|update_clip|move_clip|remove_clip|split_clip|add_subtitle|update_subtitle|remove_subtitle|add_marker|remove_marker"`
	Timeline        *studioEditTimelineToolInput    `json:"timeline,omitempty"`
	FPS             *int                            `json:"fps,omitempty"`
	Width           *int                            `json:"width,omitempty"`
	Height          *int                            `json:"height,omitempty"`
	TrackID         string                          `json:"track_id,omitempty"`
	TargetTrackID   string                          `json:"target_track_id,omitempty"`
	TrackKind       domainstudio.TrackKind          `json:"track_kind,omitempty"`
	TrackName       *string                         `json:"track_name,omitempty"`
	Muted           *bool                           `json:"muted,omitempty"`
	Solo            *bool                           `json:"solo,omitempty"`
	Locked          *bool                           `json:"locked,omitempty"`
	Volume          *float64                        `json:"volume,omitempty"`
	ClipID          string                          `json:"clip_id,omitempty"`
	NewClipID       string                          `json:"new_clip_id,omitempty"`
	Clip            *studioTimelineClipToolInput    `json:"clip,omitempty"`
	TimelineStartMs *int64                          `json:"timeline_start_ms,omitempty"`
	SourceInMs      *int64                          `json:"source_in_ms,omitempty"`
	SourceOutMs     *int64                          `json:"source_out_ms,omitempty"`
	Speed           *float64                        `json:"speed,omitempty"`
	TransitionIn    *domainstudio.TransitionType    `json:"transition_in,omitempty"`
	TransitionMs    *int64                          `json:"transition_ms,omitempty"`
	KenBurnsPreset  *domainstudio.KenBurnsPreset    `json:"ken_burns_preset,omitempty"`
	Label           *string                         `json:"label,omitempty"`
	SplitAtMs       int64                           `json:"split_at_ms,omitempty"`
	SubtitleID      string                          `json:"subtitle_id,omitempty"`
	Subtitle        *studioSubtitleCueToolInput     `json:"subtitle,omitempty"`
	MarkerID        string                          `json:"marker_id,omitempty"`
	Marker          *contracts.StudioTimelineMarker `json:"marker,omitempty"`
}

type studioTimelineApplyOpsToolInput struct {
	EpisodeID        string                      `json:"episode_id" jsonschema:"Studio episode ID"`
	ExpectedRevision int                         `json:"expected_revision" jsonschema:"Current timeline revision number required for optimistic concurrency"`
	Summary          string                      `json:"summary,omitempty" jsonschema:"Short human-readable summary of this timeline edit"`
	Ops              []studioTimelineOpToolInput `json:"ops" jsonschema:"Ordered atomic timeline operations"`
}

type studioTimelineRevisionNavToolInput struct {
	EpisodeID        string `json:"episode_id" jsonschema:"Studio episode ID"`
	ExpectedRevision int    `json:"expected_revision" jsonschema:"Current timeline revision number required for optimistic concurrency"`
}

type studioTimelineAutoAssembleToolInput struct {
	EpisodeID         string  `json:"episode_id" jsonschema:"Studio episode ID"`
	ExpectedRevision  *int    `json:"expected_revision,omitempty" jsonschema:"Optional expected timeline revision check"`
	Resolution        string  `json:"resolution,omitempty" jsonschema:"Output resolution WxH"`
	FPS               int     `json:"fps,omitempty" jsonschema:"Output frame rate"`
	TransitionDefault string  `json:"transition_default,omitempty" jsonschema:"none|fade|crossfade|wipeleft|slideleft"`
	TransitionMs      int64   `json:"transition_ms,omitempty" jsonschema:"Transition duration in ms"`
	BGMPath           string  `json:"bgm_path,omitempty" jsonschema:"Optional workspace BGM audio file path"`
	BGMVolume         float64 `json:"bgm_volume,omitempty" jsonschema:"Optional BGM track gain (0..2)"`
}

type studioMediaPeaksThumbsToolInput struct {
	EpisodeID        string  `json:"episode_id" jsonschema:"Studio episode ID"`
	MediaPath        string  `json:"media_path" jsonschema:"Media clip path inside the workspace"`
	ClipID           string  `json:"clip_id,omitempty" jsonschema:"Optional timeline clip ID to hydrate with peaks & sprite"`
	ThumbIntervalSec float64 `json:"thumb_interval_sec,omitempty" jsonschema:"Optional thumbnail sampling interval seconds"`
	PeakBins         int     `json:"peak_bins,omitempty" jsonschema:"Optional waveform peak bucket count"`
}

type studioTimelineRenderToolInput struct {
	EpisodeID        string  `json:"episode_id" jsonschema:"Studio episode ID"`
	Filename         string  `json:"filename,omitempty" jsonschema:"Optional output .mp4 filename"`
	BurnSubtitles    bool    `json:"burn_subtitles,omitempty" jsonschema:"Whether to burn UTF-8 subtitles onto video"`
	SubtitleFontSize int     `json:"subtitle_font_size,omitempty" jsonschema:"Optional burned subtitle font size"`
	LoudnormLUFS     float64 `json:"loudnorm_lufs,omitempty" jsonschema:"Optional EBU R128 loudness target e.g. -14"`
	CRF              *int    `json:"crf,omitempty" jsonschema:"Optional H.264 CRF (0..51)"`
	Preset           string  `json:"preset,omitempty" jsonschema:"Optional FFmpeg encoding preset"`
	Overwrite        bool    `json:"overwrite,omitempty" jsonschema:"Replace an existing rendered MP4 at the target filename"`
}

type studioTimelineExportNLEToolInput struct {
	EpisodeID        string `json:"episode_id" jsonschema:"Studio episode ID"`
	Format           string `json:"format" jsonschema:"capcut|jianying|fcpxml|otio"`
	ProjectName      string `json:"project_name,omitempty" jsonschema:"Optional exported draft project name"`
	IncludeMediaCopy *bool  `json:"include_media_copy,omitempty" jsonschema:"Whether to copy media inside NLE draft zip"`
}

func (s *Service) registerStudioTimelineTools(server *mcpToolset) {
	addMCPTool(server, "studio_timeline_get",
		"Get an episode's multi-track EditTimeline JSON, current revision number, Undo/Redo state, and recent revision summaries.",
		func(ctx context.Context, in studioTimelineGetToolInput) (*mcp.CallToolResult, any, error) {
			p3, ok := s.studio.(StudioPhase3Manager)
			if !ok {
				return mcpError("studio timeline management unavailable")
			}
			res, err := p3.GetTimeline(engine.WithFileGuard(ctx, s.CheckTrustedFile), strings.TrimSpace(in.EpisodeID))
			if err != nil {
				return mcpError(err.Error())
			}
			return mcpJSON(res)
		})

	addMCPTool(server, "studio_timeline_apply_ops",
		"Apply atomic EditTimeline mutations (add/move/split/trim/remove clip, update track mute/solo/volume, edit Vietnamese subtitle cues, add markers) while enforcing expected_revision concurrency.",
		func(ctx context.Context, in studioTimelineApplyOpsToolInput) (*mcp.CallToolResult, any, error) {
			p3, ok := s.studio.(StudioPhase3Manager)
			if !ok {
				return mcpError("studio timeline management unavailable")
			}
			ops, err := s.validateAndConvertTimelineOpInputs(in.Ops)
			if err != nil {
				return mcpError(err.Error())
			}
			payload := contracts.StudioTimelineApplyOpsPayload{
				EpisodeID:        strings.TrimSpace(in.EpisodeID),
				ExpectedRevision: in.ExpectedRevision,
				Actor:            domainstudio.ActorAgent,
				Summary:          strings.TrimSpace(in.Summary),
				Ops:              ops,
			}
			destructive := false
			for _, op := range ops {
				switch op.Op {
				case domainstudio.OpReplaceTimeline, domainstudio.OpRemoveTrack, domainstudio.OpRemoveClip:
					destructive = true
				}
				if destructive {
					break
				}
			}
			guardedCtx := engine.WithFileGuard(ctx, s.CheckTrustedFile)
			if destructive && !s.skipTrustedApproval() {
				epDetail, epErr := s.studio.GetEpisode(guardedCtx, payload.EpisodeID)
				if epErr != nil {
					return mcpError(epErr.Error())
				}
				if !epDetail.Project.AutoApproveBatches {
					return s.needsApproval(ctx, "studio_timeline_apply_ops", "", payload,
						"Apply destructive timeline changes",
						fmt.Sprintf("Apply destructive timeline ops on %s / %s", epDetail.Project.Title, epDetail.Episode.Code),
						func(execCtx context.Context) (any, error) {
							return p3.ApplyTimelineOps(engine.WithFileGuard(execCtx, s.CheckTrustedFile), payload)
						})
				}
			}
			res, err := p3.ApplyTimelineOps(guardedCtx, payload)
			if err != nil {
				return mcpError(err.Error())
			}
			return mcpJSON(res)
		})

	addMCPTool(server, "studio_timeline_undo",
		"Undo the latest saved EditTimeline revision for an episode while enforcing expected_revision.",
		func(ctx context.Context, in studioTimelineRevisionNavToolInput) (*mcp.CallToolResult, any, error) {
			p3, ok := s.studio.(StudioPhase3Manager)
			if !ok {
				return mcpError("studio timeline management unavailable")
			}
			res, err := p3.UndoTimeline(engine.WithFileGuard(ctx, s.CheckTrustedFile), contracts.StudioTimelineRevisionNavPayload{
				EpisodeID:        strings.TrimSpace(in.EpisodeID),
				ExpectedRevision: in.ExpectedRevision,
			})
			if err != nil {
				return mcpError(err.Error())
			}
			return mcpJSON(res)
		})

	addMCPTool(server, "studio_timeline_redo",
		"Redo the next undone EditTimeline revision for an episode while enforcing expected_revision.",
		func(ctx context.Context, in studioTimelineRevisionNavToolInput) (*mcp.CallToolResult, any, error) {
			p3, ok := s.studio.(StudioPhase3Manager)
			if !ok {
				return mcpError("studio timeline management unavailable")
			}
			res, err := p3.RedoTimeline(engine.WithFileGuard(ctx, s.CheckTrustedFile), contracts.StudioTimelineRevisionNavPayload{
				EpisodeID:        strings.TrimSpace(in.EpisodeID),
				ExpectedRevision: in.ExpectedRevision,
			})
			if err != nil {
				return mcpError(err.Error())
			}
			return mcpJSON(res)
		})

	addMCPTool(server, "studio_timeline_auto_assemble",
		"Auto-assemble ready episode shots, speech audio, subtitles, transitions, and optional BGM into a canonical multi-track EditTimeline.",
		func(ctx context.Context, in studioTimelineAutoAssembleToolInput) (*mcp.CallToolResult, any, error) {
			if s.studio == nil {
				return mcpError("studio service is unavailable")
			}
			ext, ok := s.studio.(StudioExtendedManager)
			if !ok {
				return mcpError("studio timeline auto-assemble unavailable")
			}
			bgmPath := strings.TrimSpace(in.BGMPath)
			if bgmPath != "" && s.policy != nil {
				resolved, err := s.policy.ValidateRead(bgmPath)
				if err != nil {
					return mcpError(err.Error())
				}
				bgmPath = resolved
			}
			p := contracts.StudioTimelineAutoAssemblePayload{
				EpisodeID:         strings.TrimSpace(in.EpisodeID),
				ExpectedRevision:  in.ExpectedRevision,
				Resolution:        strings.TrimSpace(in.Resolution),
				FPS:               in.FPS,
				TransitionDefault: domainstudio.TransitionType(strings.TrimSpace(in.TransitionDefault)),
				TransitionMs:      in.TransitionMs,
				BGMPath:           bgmPath,
				BGMVolume:         in.BGMVolume,
			}
			if err := p.Validate(); err != nil {
				return mcpError(err.Error())
			}
			guardedCtx := engine.WithFileGuard(ctx, s.CheckTrustedFile)
			epDetail, err := s.studio.GetEpisode(guardedCtx, p.EpisodeID)
			if err != nil {
				return mcpError(err.Error())
			}
			hasExistingTimeline := false
			if p3, ok := s.studio.(StudioPhase3Manager); ok {
				if curTL, tlErr := p3.GetTimeline(guardedCtx, p.EpisodeID); tlErr == nil && curTL.Revision > 0 {
					for _, tr := range curTL.Timeline.Tracks {
						if len(tr.Clips) > 0 {
							hasExistingTimeline = true
							break
						}
					}
					if len(curTL.Timeline.Subtitles) > 0 || len(curTL.Timeline.Markers) > 0 {
						hasExistingTimeline = true
					}
				}
			}
			if hasExistingTimeline {
				prep := s.studioProjectPrep(epDetail.Project, bgmPath)
				if !epDetail.Project.AutoApproveBatches || !allFileTargetsTrusted(s.policy, prep) {
					return s.needsApproval(ctx, "studio_timeline_auto_assemble", "", p, "Replace existing studio timeline",
						fmt.Sprintf("Auto-assemble and replace existing timeline on %s / %s", epDetail.Project.Title, epDetail.Episode.Code),
						func(execCtx context.Context) (any, error) {
							return ext.AutoAssembleTimeline(engine.WithFileGuard(execCtx, s.CheckTrustedFile), p)
						})
				}
			}
			res, err := ext.AutoAssembleTimeline(guardedCtx, p)
			if err != nil {
				return mcpError(err.Error())
			}
			return mcpJSON(res)
		})

	addMCPTool(server, "studio_media_peaks_thumbs",
		"Extract audio waveform RMS peaks and thumbnail sprite strips for a media clip on the timeline.",
		func(ctx context.Context, in studioMediaPeaksThumbsToolInput) (*mcp.CallToolResult, any, error) {
			ext, ok := s.studio.(StudioExtendedManager)
			if !ok {
				return mcpError("studio media peaks & thumbs unavailable")
			}
			mediaPath := strings.TrimSpace(in.MediaPath)
			if mediaPath != "" && s.policy != nil {
				resolved, err := s.policy.ValidateRead(mediaPath)
				if err != nil {
					return mcpError(err.Error())
				}
				mediaPath = resolved
			}
			res, err := ext.ComputeMediaPeaksThumbs(engine.WithFileGuard(ctx, s.CheckTrustedFile), contracts.StudioMediaPeaksThumbsPayload{
				EpisodeID:        strings.TrimSpace(in.EpisodeID),
				MediaPath:        mediaPath,
				ClipID:           strings.TrimSpace(in.ClipID),
				ThumbIntervalSec: in.ThumbIntervalSec,
				PeakBins:         in.PeakBins,
			})
			if err != nil {
				return mcpError(err.Error())
			}
			return mcpJSON(res)
		})

	addMCPTool(server, "studio_timeline_render",
		"Render an episode's multi-track EditTimeline to a mastered MP4 with transitions, loudness normalization, and UTF-8 SRT/ASS subtitles.",
		func(ctx context.Context, in studioTimelineRenderToolInput) (*mcp.CallToolResult, any, error) {
			if s.studio == nil {
				return mcpError("studio service is unavailable")
			}
			ext, ok := s.studio.(StudioExtendedManager)
			if !ok {
				return mcpError("studio timeline render unavailable")
			}
			p := contracts.StudioTimelineRenderPayload{
				EpisodeID:        strings.TrimSpace(in.EpisodeID),
				Filename:         strings.TrimSpace(in.Filename),
				BurnSubtitles:    in.BurnSubtitles,
				SubtitleFontSize: in.SubtitleFontSize,
				LoudnormLUFS:     in.LoudnormLUFS,
				CRF:              in.CRF,
				Preset:           strings.TrimSpace(in.Preset),
				Overwrite:        in.Overwrite,
			}
			if err := p.Validate(); err != nil {
				return mcpError(err.Error())
			}
			guardedCtx := engine.WithFileGuard(ctx, s.CheckTrustedFile)
			epDetail, err := s.studio.GetEpisode(guardedCtx, p.EpisodeID)
			if err != nil {
				return mcpError(err.Error())
			}
			prep := s.studioProjectPrep(epDetail.Project)
			if !p.Overwrite && epDetail.Project.AutoApproveBatches && allFileTargetsTrusted(s.policy, prep) {
				res, rerr := ext.RenderTimeline(guardedCtx, p)
				if rerr != nil {
					return mcpError(rerr.Error())
				}
				return mcpJSON(res)
			}
			summary := fmt.Sprintf("Render timeline MP4 for %s / %s", epDetail.Project.Title, epDetail.Episode.Code)
			if p.Overwrite {
				summary += " (overwriting existing file)"
			}
			return s.needsApproval(ctx, "studio_timeline_render", "", p, "Render studio timeline MP4", summary, func(execCtx context.Context) (any, error) {
				return ext.RenderTimeline(engine.WithFileGuard(execCtx, s.CheckTrustedFile), p)
			})
		})

	addMCPTool(server, "studio_timeline_export_nle",
		"Export an episode's EditTimeline into CapCut/Jianying draft zip, FCPXML, or OpenTimelineIO format.",
		func(ctx context.Context, in studioTimelineExportNLEToolInput) (*mcp.CallToolResult, any, error) {
			if s.studio == nil {
				return mcpError("studio service is unavailable")
			}
			ext, ok := s.studio.(StudioExtendedManager)
			if !ok {
				return mcpError("studio NLE export unavailable")
			}
			p := contracts.StudioTimelineExportNLEPayload{
				EpisodeID:        strings.TrimSpace(in.EpisodeID),
				Format:           domainstudio.NLEExportFormat(strings.TrimSpace(in.Format)),
				ProjectName:      strings.TrimSpace(in.ProjectName),
				IncludeMediaCopy: in.IncludeMediaCopy,
			}
			if err := p.Validate(); err != nil {
				return mcpError(err.Error())
			}
			guardedCtx := engine.WithFileGuard(ctx, s.CheckTrustedFile)
			epDetail, err := s.studio.GetEpisode(guardedCtx, p.EpisodeID)
			if err != nil {
				return mcpError(err.Error())
			}
			prep := s.studioProjectPrep(epDetail.Project)
			if epDetail.Project.AutoApproveBatches && allFileTargetsTrusted(s.policy, prep) {
				res, eerr := ext.ExportNLEDraft(guardedCtx, p)
				if eerr != nil {
					return mcpError(eerr.Error())
				}
				return mcpJSON(res)
			}
			return s.needsApproval(ctx, "studio_timeline_export_nle", "", p, "Export studio NLE draft",
				fmt.Sprintf("Export %s draft for %s / %s", p.Format, epDetail.Project.Title, epDetail.Episode.Code),
				func(execCtx context.Context) (any, error) {
					return ext.ExportNLEDraft(engine.WithFileGuard(execCtx, s.CheckTrustedFile), p)
				})
		})
}

func (s *Service) validateAndConvertTimelineOpInputs(in []studioTimelineOpToolInput) ([]contracts.StudioTimelineOp, error) {
	convClip := func(c *studioTimelineClipToolInput) (*contracts.StudioTimelineClip, error) {
		if c == nil {
			return nil, nil
		}
		fp := strings.TrimSpace(c.FilePath)
		if fp != "" && s.policy != nil {
			resolved, err := s.policy.ValidateRead(fp)
			if err != nil {
				return nil, err
			}
			fp = resolved
		}
		sp := strings.TrimSpace(c.SpritePath)
		if sp != "" && s.policy != nil {
			resolved, err := s.policy.ValidateRead(sp)
			if err != nil {
				return nil, err
			}
			sp = resolved
		}
		return &contracts.StudioTimelineClip{
			ID:              c.ID,
			ShotID:          c.ShotID,
			AssetID:         c.AssetID,
			Label:           c.Label,
			FilePath:        fp,
			TimelineStartMs: c.TimelineStartMs,
			SourceInMs:      c.SourceInMs,
			SourceOutMs:     c.SourceOutMs,
			Speed:           c.Speed,
			Volume:          c.Volume,
			Mute:            c.Mute,
			TransitionIn:    domainstudio.TransitionType(c.TransitionIn),
			TransitionMs:    c.TransitionMs,
			KenBurnsPreset:  domainstudio.KenBurnsPreset(c.KenBurnsPreset),
			SpritePath:      sp,
			SpriteAssetID:   c.SpriteAssetID,
		}, nil
	}
	out := make([]contracts.StudioTimelineOp, len(in))
	for i, op := range in {
		clipDTO, err := convClip(op.Clip)
		if err != nil {
			return nil, err
		}
		var tlDTO *contracts.StudioEditTimeline
		if op.Timeline != nil {
			tracks := make([]contracts.StudioTimelineTrack, len(op.Timeline.Tracks))
			for ti, tr := range op.Timeline.Tracks {
				clips := make([]contracts.StudioTimelineClip, len(tr.Clips))
				for ci := range tr.Clips {
					c, cerr := convClip(&tr.Clips[ci])
					if cerr != nil {
						return nil, cerr
					}
					clips[ci] = *c
				}
				tracks[ti] = contracts.StudioTimelineTrack{
					ID:     tr.ID,
					Name:   tr.Name,
					Kind:   domainstudio.TrackKind(tr.Kind),
					Muted:  tr.Muted,
					Solo:   tr.Solo,
					Locked: tr.Locked,
					Volume: tr.Volume,
					Clips:  clips,
				}
			}
			subs := make([]contracts.StudioSubtitleCue, len(op.Timeline.Subtitles))
			for si, sub := range op.Timeline.Subtitles {
				subs[si] = contracts.StudioSubtitleCue{
					ID:          sub.ID,
					StartMs:     sub.StartMs,
					EndMs:       sub.EndMs,
					Speaker:     sub.Speaker,
					Text:        sub.Text,
					StylePreset: sub.StylePreset,
				}
			}
			tlDTO = &contracts.StudioEditTimeline{
				Version:   op.Timeline.Version,
				FPS:       op.Timeline.FPS,
				Width:     op.Timeline.Width,
				Height:    op.Timeline.Height,
				Tracks:    tracks,
				Subtitles: subs,
				Markers:   op.Timeline.Markers,
			}
		}
		var subDTO *contracts.StudioSubtitleCue
		if op.Subtitle != nil {
			subDTO = &contracts.StudioSubtitleCue{
				ID:          op.Subtitle.ID,
				StartMs:     op.Subtitle.StartMs,
				EndMs:       op.Subtitle.EndMs,
				Speaker:     op.Subtitle.Speaker,
				Text:        op.Subtitle.Text,
				StylePreset: op.Subtitle.StylePreset,
			}
		}
		out[i] = contracts.StudioTimelineOp{
			Op:              domainstudio.TimelineOpKind(op.Op),
			Timeline:        tlDTO,
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
			Clip:            clipDTO,
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
			Subtitle:        subDTO,
			MarkerID:        op.MarkerID,
			Marker:          op.Marker,
		}
	}
	return out, nil
}
