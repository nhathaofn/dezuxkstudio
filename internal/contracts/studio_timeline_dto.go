package contracts

import (
	"fmt"
	"math"
	"strings"

	"app/internal/apperr"
	domainstudio "app/internal/domain/studio"
)

type StudioTimelineClip struct {
	ID              string                      `json:"id"`
	ShotID          string                      `json:"shot_id,omitempty"`
	AssetID         string                      `json:"asset_id,omitempty"`
	Label           string                      `json:"label,omitempty"`
	FilePath        string                      `json:"file_path"`
	TimelineStartMs int64                       `json:"timeline_start_ms"`
	SourceInMs      int64                       `json:"source_in_ms"`
	SourceOutMs     int64                       `json:"source_out_ms"`
	Speed           float64                     `json:"speed"`
	Volume          float64                     `json:"volume"`
	Mute            bool                        `json:"mute,omitempty"`
	TransitionIn    domainstudio.TransitionType `json:"transition_in"`
	TransitionMs    int64                       `json:"transition_ms"`
	KenBurnsPreset  domainstudio.KenBurnsPreset `json:"ken_burns_preset,omitempty"`
	SpritePath      string                      `json:"sprite_path,omitempty"`
	SpriteAssetID   string                      `json:"sprite_asset_id,omitempty"`
	Peaks           []float64                   `json:"peaks"`
}

// StudioTimelineTrack is the transport DTO for one lane in an EditTimeline.
type StudioTimelineTrack struct {
	ID     string                 `json:"id"`
	Name   string                 `json:"name,omitempty"`
	Kind   domainstudio.TrackKind `json:"kind"`
	Muted  bool                   `json:"muted"`
	Solo   bool                   `json:"solo,omitempty"`
	Locked bool                   `json:"locked"`
	Volume float64                `json:"volume"`
	Clips  []StudioTimelineClip   `json:"clips"`
}

// StudioSubtitleCue is the transport DTO for a timed subtitle cue.
type StudioSubtitleCue struct {
	ID          string `json:"id"`
	StartMs     int64  `json:"start_ms"`
	EndMs       int64  `json:"end_ms"`
	Speaker     string `json:"speaker,omitempty"`
	Text        string `json:"text"`
	StylePreset string `json:"style_preset,omitempty"`
}

// StudioTimelineMarker is the transport DTO for a timeline ruler marker.
type StudioTimelineMarker struct {
	ID     string `json:"id"`
	TimeMs int64  `json:"time_ms"`
	Label  string `json:"label"`
	Color  string `json:"color,omitempty"`
}

// StudioEditTimeline is the normalized multi-track post-production timeline DTO.
type StudioEditTimeline struct {
	Version   int                    `json:"version"`
	FPS       int                    `json:"fps"`
	Width     int                    `json:"width"`
	Height    int                    `json:"height"`
	Tracks    []StudioTimelineTrack  `json:"tracks"`
	Subtitles []StudioSubtitleCue    `json:"subtitles"`
	Markers   []StudioTimelineMarker `json:"markers"`
}

// StudioTimelineOp represents one atomic timeline mutation sent to studio:timeline_apply_ops.
type StudioTimelineOp struct {
	Op              domainstudio.TimelineOpKind  `json:"op"`
	Timeline        *StudioEditTimeline          `json:"timeline,omitempty"`
	FPS             *int                         `json:"fps,omitempty"`
	Width           *int                         `json:"width,omitempty"`
	Height          *int                         `json:"height,omitempty"`
	TrackID         string                       `json:"track_id,omitempty"`
	TargetTrackID   string                       `json:"target_track_id,omitempty"`
	TrackKind       domainstudio.TrackKind       `json:"track_kind,omitempty"`
	TrackName       *string                      `json:"track_name,omitempty"`
	Muted           *bool                        `json:"muted,omitempty"`
	Solo            *bool                        `json:"solo,omitempty"`
	Locked          *bool                        `json:"locked,omitempty"`
	Volume          *float64                     `json:"volume,omitempty"`
	ClipID          string                       `json:"clip_id,omitempty"`
	NewClipID       string                       `json:"new_clip_id,omitempty"`
	Clip            *StudioTimelineClip          `json:"clip,omitempty"`
	TimelineStartMs *int64                       `json:"timeline_start_ms,omitempty"`
	SourceInMs      *int64                       `json:"source_in_ms,omitempty"`
	SourceOutMs     *int64                       `json:"source_out_ms,omitempty"`
	Speed           *float64                     `json:"speed,omitempty"`
	TransitionIn    *domainstudio.TransitionType `json:"transition_in,omitempty"`
	TransitionMs    *int64                       `json:"transition_ms,omitempty"`
	KenBurnsPreset  *domainstudio.KenBurnsPreset `json:"ken_burns_preset,omitempty"`
	Label           *string                      `json:"label,omitempty"`
	SplitAtMs       int64                        `json:"split_at_ms,omitempty"`
	SubtitleID      string                       `json:"subtitle_id,omitempty"`
	Subtitle        *StudioSubtitleCue           `json:"subtitle,omitempty"`
	MarkerID        string                       `json:"marker_id,omitempty"`
	Marker          *StudioTimelineMarker        `json:"marker,omitempty"`
}

// StudioTimelineRevisionSummary summarizes one saved revision in studio_timeline_revisions.
type StudioTimelineRevisionSummary struct {
	ID        string                     `json:"id"`
	Revision  int                        `json:"revision"`
	Actor     domainstudio.TimelineActor `json:"actor"`
	Summary   string                     `json:"summary"`
	CreatedAt int64                      `json:"created_at"`
}

// StudioTimelineResult returns the active timeline state, revision metadata, and history.
type StudioTimelineResult struct {
	ProjectID    string                          `json:"project_id"`
	EpisodeID    string                          `json:"episode_id"`
	Revision     int                             `json:"revision"`
	CanUndo      bool                            `json:"can_undo"`
	CanRedo      bool                            `json:"can_redo"`
	DurationMs   int64                           `json:"duration_ms"`
	TimelinePath string                          `json:"timeline_path,omitempty"`
	Timeline     StudioEditTimeline              `json:"timeline"`
	Revisions    []StudioTimelineRevisionSummary `json:"revisions"`
	UpdatedAt    int64                           `json:"updated_at"`
}

// StudioTimelineApplyOpsPayload applies atomic timeline operations under optimistic revision checking.
type StudioTimelineApplyOpsPayload struct {
	EpisodeID        string                     `json:"episode_id"`
	ExpectedRevision int                        `json:"expected_revision"`
	Actor            domainstudio.TimelineActor `json:"actor,omitempty"`
	Summary          string                     `json:"summary,omitempty"`
	Ops              []StudioTimelineOp         `json:"ops"`
}

// Validate checks StudioTimelineApplyOpsPayload.
func (p StudioTimelineApplyOpsPayload) Validate() error {
	if strings.TrimSpace(p.EpisodeID) == "" {
		return apperr.Validation("episode_id is required")
	}
	if p.ExpectedRevision < 0 {
		return apperr.Validation("expected_revision must be >= 0")
	}
	if len(p.Ops) == 0 {
		return apperr.Validation("at least one timeline op is required")
	}
	return nil
}

// StudioTimelineRevisionNavPayload steps timeline revision history backward (undo) or forward (redo).
type StudioTimelineRevisionNavPayload struct {
	EpisodeID        string `json:"episode_id"`
	ExpectedRevision int    `json:"expected_revision"`
}

// Validate checks StudioTimelineRevisionNavPayload.
func (p StudioTimelineRevisionNavPayload) Validate() error {
	if strings.TrimSpace(p.EpisodeID) == "" {
		return apperr.Validation("episode_id is required")
	}
	if p.ExpectedRevision < 0 {
		return apperr.Validation("expected_revision must be >= 0")
	}
	return nil
}

// StudioTimelineAutoAssemblePayload triggers tool.timeline_auto_assemble on an episode's shots & audio.
type StudioTimelineAutoAssemblePayload struct {
	EpisodeID         string                      `json:"episode_id"`
	ExpectedRevision  *int                        `json:"expected_revision,omitempty"`
	Resolution        string                      `json:"resolution,omitempty"`
	FPS               int                         `json:"fps,omitempty"`
	TransitionDefault domainstudio.TransitionType `json:"transition_default,omitempty"`
	TransitionMs      int64                       `json:"transition_ms,omitempty"`
	BGMPath           string                      `json:"bgm_path,omitempty"`
	BGMVolume         float64                     `json:"bgm_volume,omitempty"`
}

// Validate checks StudioTimelineAutoAssemblePayload.
func (p StudioTimelineAutoAssemblePayload) Validate() error {
	if strings.TrimSpace(p.EpisodeID) == "" {
		return apperr.Validation("episode_id is required")
	}
	if p.ExpectedRevision != nil && *p.ExpectedRevision < 0 {
		return apperr.Validation("expected_revision must be >= 0")
	}
	if p.FPS < 0 {
		return apperr.Validation("fps must be >= 0")
	}
	if p.TransitionMs < 0 {
		return apperr.Validation("transition_ms must be >= 0")
	}
	if math.IsNaN(p.BGMVolume) || math.IsInf(p.BGMVolume, 0) || p.BGMVolume < 0 {
		return apperr.Validation("bgm_volume must be >= 0")
	}
	if p.TransitionDefault != "" && !p.TransitionDefault.Valid() {
		return apperr.Validation(fmt.Sprintf("unsupported transition_default %q", p.TransitionDefault))
	}
	return nil
}

// StudioMediaPeaksThumbsPayload generates waveform peaks and a thumbnail sprite strip for a timeline clip.
type StudioMediaPeaksThumbsPayload struct {
	EpisodeID        string  `json:"episode_id"`
	MediaPath        string  `json:"media_path"`
	ClipID           string  `json:"clip_id,omitempty"`
	ThumbIntervalSec float64 `json:"thumb_interval_sec,omitempty"`
	PeakBins         int     `json:"peak_bins,omitempty"`
}

// Validate checks StudioMediaPeaksThumbsPayload.
func (p StudioMediaPeaksThumbsPayload) Validate() error {
	if strings.TrimSpace(p.EpisodeID) == "" {
		return apperr.Validation("episode_id is required")
	}
	if strings.TrimSpace(p.MediaPath) == "" {
		return apperr.Validation("media_path is required")
	}
	if math.IsNaN(p.ThumbIntervalSec) || math.IsInf(p.ThumbIntervalSec, 0) || p.ThumbIntervalSec < 0 {
		return apperr.Validation("thumb_interval_sec must be >= 0")
	}
	if p.PeakBins < 0 {
		return apperr.Validation("peak_bins must be >= 0")
	}
	return nil
}

// StudioTimelineRenderPayload renders the episode's active EditTimeline into an MP4 + subtitle files.
type StudioTimelineRenderPayload struct {
	EpisodeID        string  `json:"episode_id"`
	Filename         string  `json:"filename,omitempty"`
	BurnSubtitles    bool    `json:"burn_subtitles,omitempty"`
	SubtitleFontSize int     `json:"subtitle_font_size,omitempty"`
	LoudnormLUFS     float64 `json:"loudnorm_lufs,omitempty"`
	CRF              *int    `json:"crf,omitempty"`
	Preset           string  `json:"preset,omitempty"`
	Overwrite        bool    `json:"overwrite,omitempty"`
}

// Validate checks StudioTimelineRenderPayload.
func (p StudioTimelineRenderPayload) Validate() error {
	if strings.TrimSpace(p.EpisodeID) == "" {
		return apperr.Validation("episode_id is required")
	}
	if p.SubtitleFontSize < 0 {
		return apperr.Validation("subtitle_font_size must be >= 0")
	}
	if math.IsNaN(p.LoudnormLUFS) || math.IsInf(p.LoudnormLUFS, 0) || (p.LoudnormLUFS != 0 && (p.LoudnormLUFS < -70 || p.LoudnormLUFS > -5)) {
		return apperr.Validation("loudnorm_lufs must be 0 or between -70 and -5")
	}
	if p.CRF != nil && (*p.CRF < 0 || *p.CRF > 51) {
		return apperr.Validation("crf must be between 0 and 51")
	}
	return nil
}

// StudioTimelineRenderResult returns the rendered MP4, subtitle artifacts, and refreshed episode detail.
type StudioTimelineRenderResult struct {
	OutputPath  string                    `json:"output_path"`
	AssetID     string                    `json:"asset_id,omitempty"`
	SRTPath     string                    `json:"srt_path,omitempty"`
	ASSPath     string                    `json:"ass_path,omitempty"`
	DurationSec float64                   `json:"duration_sec"`
	Episode     StudioEpisodeDetailResult `json:"episode"`
}

// StudioTimelineExportNLEPayload exports the episode's active EditTimeline to CapCut/Jianying/FCPXML/OTIO.
type StudioTimelineExportNLEPayload struct {
	EpisodeID        string                       `json:"episode_id"`
	Format           domainstudio.NLEExportFormat `json:"format"`
	ProjectName      string                       `json:"project_name,omitempty"`
	IncludeMediaCopy *bool                        `json:"include_media_copy,omitempty"`
}

// Validate checks StudioTimelineExportNLEPayload.
func (p StudioTimelineExportNLEPayload) Validate() error {
	if strings.TrimSpace(p.EpisodeID) == "" {
		return apperr.Validation("episode_id is required")
	}
	if p.Format == "" {
		return apperr.Validation("format is required")
	}
	if !p.Format.Valid() {
		return apperr.Validation(fmt.Sprintf("unsupported NLE export format %q", p.Format))
	}
	return nil
}

// StudioOpenExportFolderPayload opens an episode export folder (or parent directory of a file inside it) in OS explorer.
type StudioOpenExportFolderPayload struct {
	EpisodeID string `json:"episode_id"`
	FilePath  string `json:"file_path,omitempty"`
}

// Validate checks StudioOpenExportFolderPayload.
func (p StudioOpenExportFolderPayload) Validate() error {
	if strings.TrimSpace(p.EpisodeID) == "" && strings.TrimSpace(p.FilePath) == "" {
		return apperr.Validation("episode_id or file_path is required")
	}
	return nil
}

// StudioTimelineChangedEvent is published on studio:timeline_changed whenever UI, Agent, or a Node modifies a timeline.
type StudioTimelineChangedEvent struct {
	ProjectID  string                     `json:"project_id"`
	EpisodeID  string                     `json:"episode_id"`
	Revision   int                        `json:"revision"`
	CanUndo    bool                       `json:"can_undo"`
	CanRedo    bool                       `json:"can_redo"`
	DurationMs int64                      `json:"duration_ms"`
	Actor      domainstudio.TimelineActor `json:"actor"`
	Summary    string                     `json:"summary"`
	Timeline   StudioEditTimeline         `json:"timeline"`
}
