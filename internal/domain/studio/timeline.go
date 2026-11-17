package studio

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/google/uuid"

	"app/internal/apperr"
)

// TrackKind classifies a timeline track lane.
type TrackKind string

const (
	TrackVideo          TrackKind = "video"
	TrackAudioDialogue  TrackKind = "audio_dialogue"
	TrackAudioNarration TrackKind = "audio_narration"
	TrackAudioBGM       TrackKind = "audio_bgm"
	TrackAudioSFX       TrackKind = "audio_sfx"
	TrackText           TrackKind = "text"
)

// TrackKinds returns all valid timeline track kinds in display order.
func TrackKinds() []TrackKind {
	return []TrackKind{
		TrackVideo,
		TrackAudioDialogue,
		TrackAudioNarration,
		TrackAudioBGM,
		TrackAudioSFX,
		TrackText,
	}
}

// Valid reports whether k is a supported TrackKind.
func (k TrackKind) Valid() bool {
	return slices.Contains(TrackKinds(), k)
}

// TransitionType names an incoming clip transition effect.
type TransitionType string

const (
	TransitionNone      TransitionType = "none"
	TransitionFade      TransitionType = "fade"
	TransitionCrossfade TransitionType = "crossfade"
	TransitionWipeLeft  TransitionType = "wipeleft"
	TransitionSlideLeft TransitionType = "slideleft"
)

// TransitionTypes returns all supported clip transitions in display order.
func TransitionTypes() []TransitionType {
	return []TransitionType{
		TransitionNone,
		TransitionFade,
		TransitionCrossfade,
		TransitionWipeLeft,
		TransitionSlideLeft,
	}
}

// Valid reports whether t is a supported TransitionType.
func (t TransitionType) Valid() bool {
	return slices.Contains(TransitionTypes(), t)
}

// KenBurnsPreset names a motion preset for still images on the timeline.
type KenBurnsPreset string

const (
	KenBurnsNone     KenBurnsPreset = "none"
	KenBurnsZoomIn   KenBurnsPreset = "zoom_in"
	KenBurnsZoomOut  KenBurnsPreset = "zoom_out"
	KenBurnsPanLeft  KenBurnsPreset = "pan_left"
	KenBurnsPanRight KenBurnsPreset = "pan_right"
)

// KenBurnsPresets returns all supported Ken Burns motion presets.
func KenBurnsPresets() []KenBurnsPreset {
	return []KenBurnsPreset{
		KenBurnsNone,
		KenBurnsZoomIn,
		KenBurnsZoomOut,
		KenBurnsPanLeft,
		KenBurnsPanRight,
	}
}

// Valid reports whether k is a supported KenBurnsPreset.
func (k KenBurnsPreset) Valid() bool {
	return slices.Contains(KenBurnsPresets(), k)
}

// NLEExportFormat enumerates supported external NLE project export formats.
type NLEExportFormat string

const (
	NLEFormatCapCut   NLEExportFormat = "capcut"
	NLEFormatJianying NLEExportFormat = "jianying"
	NLEFormatFCPXML   NLEExportFormat = "fcpxml"
	NLEFormatOTIO     NLEExportFormat = "otio"
)

// NLEExportFormats returns all supported NLE export formats.
func NLEExportFormats() []NLEExportFormat {
	return []NLEExportFormat{
		NLEFormatCapCut,
		NLEFormatJianying,
		NLEFormatFCPXML,
		NLEFormatOTIO,
	}
}

// Valid reports whether f is a supported NLEExportFormat.
func (f NLEExportFormat) Valid() bool {
	return slices.Contains(NLEExportFormats(), f)
}

// TimelineActor identifies who authored a timeline revision.
type TimelineActor string

const (
	ActorUser  TimelineActor = "user"
	ActorAgent TimelineActor = "agent"
	ActorNode  TimelineActor = "node"
)

// TimelineActors returns all valid timeline revision actors.
func TimelineActors() []TimelineActor {
	return []TimelineActor{ActorUser, ActorAgent, ActorNode}
}

// Valid reports whether a is a valid TimelineActor.
func (a TimelineActor) Valid() bool {
	return a == ActorUser || a == ActorAgent || a == ActorNode
}

// TimelineOpKind enumerates atomic EditTimeline mutation operations.
type TimelineOpKind string

const (
	OpReplaceTimeline TimelineOpKind = "replace_timeline"
	OpSetFormat       TimelineOpKind = "set_format"
	OpAddTrack        TimelineOpKind = "add_track"
	OpUpdateTrack     TimelineOpKind = "update_track"
	OpRemoveTrack     TimelineOpKind = "remove_track"
	OpAddClip         TimelineOpKind = "add_clip"
	OpUpdateClip      TimelineOpKind = "update_clip"
	OpMoveClip        TimelineOpKind = "move_clip"
	OpRemoveClip      TimelineOpKind = "remove_clip"
	OpSplitClip       TimelineOpKind = "split_clip"
	OpAddSubtitle     TimelineOpKind = "add_subtitle"
	OpUpdateSubtitle  TimelineOpKind = "update_subtitle"
	OpRemoveSubtitle  TimelineOpKind = "remove_subtitle"
	OpAddMarker       TimelineOpKind = "add_marker"
	OpRemoveMarker    TimelineOpKind = "remove_marker"
)

// TimelineOpKinds returns all supported EditTimeline mutation op kinds.
func TimelineOpKinds() []TimelineOpKind {
	return []TimelineOpKind{
		OpReplaceTimeline, OpSetFormat, OpAddTrack, OpUpdateTrack, OpRemoveTrack,
		OpAddClip, OpUpdateClip, OpMoveClip, OpRemoveClip, OpSplitClip,
		OpAddSubtitle, OpUpdateSubtitle, OpRemoveSubtitle, OpAddMarker, OpRemoveMarker,
	}
}

// Valid reports whether k is a supported TimelineOpKind.
func (k TimelineOpKind) Valid() bool {
	return slices.Contains(TimelineOpKinds(), k)
}

// EditTimeline is the canonical multi-track post-production timeline shared by UI, FFmpeg render, NLE export, and Agent.
type EditTimeline struct {
	Version   int              `json:"version"`
	FPS       int              `json:"fps"`
	Width     int              `json:"width"`
	Height    int              `json:"height"`
	Tracks    []Track          `json:"tracks"`
	Subtitles []SubtitleCue    `json:"subtitles"`
	Markers   []TimelineMarker `json:"markers,omitempty"`
}

// Track groups sequential or overlapping clips of a single media role.
type Track struct {
	ID     string         `json:"id"`
	Name   string         `json:"name,omitempty"`
	Kind   TrackKind      `json:"kind"`
	Muted  bool           `json:"muted"`
	Solo   bool           `json:"solo,omitempty"`
	Locked bool           `json:"locked"`
	Volume float64        `json:"volume"`
	Clips  []TimelineClip `json:"clips"`
}

// TimelineClip places a trimmed slice of a local media file or registered asset onto a Track.
type TimelineClip struct {
	ID              string         `json:"id"`
	ShotID          string         `json:"shot_id,omitempty"`
	AssetID         string         `json:"asset_id,omitempty"`
	Label           string         `json:"label,omitempty"`
	FilePath        string         `json:"file_path"`
	TimelineStartMs int64          `json:"timeline_start_ms"`
	SourceInMs      int64          `json:"source_in_ms"`
	SourceOutMs     int64          `json:"source_out_ms"`
	Speed           float64        `json:"speed"`
	Volume          float64        `json:"volume"`
	TransitionIn    TransitionType `json:"transition_in"`
	TransitionMs    int64          `json:"transition_ms"`
	KenBurnsPreset  KenBurnsPreset `json:"ken_burns_preset,omitempty"`
	SpritePath      string         `json:"sprite_path,omitempty"`
	SpriteAssetID   string         `json:"sprite_asset_id,omitempty"`
	Peaks           []float64      `json:"peaks,omitempty"`
}

// SubtitleCue defines a timed caption/subtitle entry (supports full UTF-8 Vietnamese).
type SubtitleCue struct {
	ID          string `json:"id"`
	StartMs     int64  `json:"start_ms"`
	EndMs       int64  `json:"end_ms"`
	Speaker     string `json:"speaker,omitempty"`
	Text        string `json:"text"`
	StylePreset string `json:"style_preset,omitempty"`
}

// TimelineMarker marks a point of interest on the timeline ruler.
type TimelineMarker struct {
	ID     string `json:"id"`
	TimeMs int64  `json:"time_ms"`
	Label  string `json:"label"`
	Color  string `json:"color,omitempty"`
}

// TimelineRecord represents the active persisted timeline state for an episode.
type TimelineRecord struct {
	EpisodeID    string
	Revision     int
	CanUndo      bool
	CanRedo      bool
	Timeline     EditTimeline
	TimelineJSON string
	UpdatedAt    int64
}

// TimelineRevision represents one historical snapshot in studio_timeline_revisions.
type TimelineRevision struct {
	ID           string
	EpisodeID    string
	Revision     int
	Actor        TimelineActor
	Summary      string
	TimelineJSON string
	CreatedAt    int64
}

// TimelineOp describes an atomic mutation applied via studio_timeline_apply_ops.
type TimelineOp struct {
	Op              string          `json:"op"`
	Timeline        *EditTimeline   `json:"timeline,omitempty"`
	FPS             *int            `json:"fps,omitempty"`
	Width           *int            `json:"width,omitempty"`
	Height          *int            `json:"height,omitempty"`
	TrackID         string          `json:"track_id,omitempty"`
	TargetTrackID   string          `json:"target_track_id,omitempty"`
	TrackKind       TrackKind       `json:"track_kind,omitempty"`
	TrackName       *string         `json:"track_name,omitempty"`
	Muted           *bool           `json:"muted,omitempty"`
	Solo            *bool           `json:"solo,omitempty"`
	Locked          *bool           `json:"locked,omitempty"`
	Volume          *float64        `json:"volume,omitempty"`
	ClipID          string          `json:"clip_id,omitempty"`
	NewClipID       string          `json:"new_clip_id,omitempty"`
	Clip            *TimelineClip   `json:"clip,omitempty"`
	TimelineStartMs *int64          `json:"timeline_start_ms,omitempty"`
	SourceInMs      *int64          `json:"source_in_ms,omitempty"`
	SourceOutMs     *int64          `json:"source_out_ms,omitempty"`
	Speed           *float64        `json:"speed,omitempty"`
	TransitionIn    *TransitionType `json:"transition_in,omitempty"`
	TransitionMs    *int64          `json:"transition_ms,omitempty"`
	KenBurnsPreset  *KenBurnsPreset `json:"ken_burns_preset,omitempty"`
	Label           *string         `json:"label,omitempty"`
	SplitAtMs       int64           `json:"split_at_ms,omitempty"`
	SubtitleID      string          `json:"subtitle_id,omitempty"`
	Subtitle        *SubtitleCue    `json:"subtitle,omitempty"`
	MarkerID        string          `json:"marker_id,omitempty"`
	Marker          *TimelineMarker `json:"marker,omitempty"`
}

// DefaultEditTimeline returns a clean initial timeline at the given dimensions and FPS.
func DefaultEditTimeline(width, height, fps int) EditTimeline {
	return DefaultEditTimelineWithBGM(width, height, fps, DefaultBGMVolume)
}

// DefaultEditTimelineWithBGM returns an initial multi-track timeline with caller-configured dimensions, FPS, and BGM volume.
func DefaultEditTimelineWithBGM(width, height, fps int, bgmVolume float64) EditTimeline {
	if width <= 0 {
		width = BaselineVideoWidth
	}
	if height <= 0 {
		height = BaselineVideoHeight
	}
	if fps <= 0 {
		fps = BaselineVideoFPS
	}
	if bgmVolume < 0 || bgmVolume > 2.0 {
		bgmVolume = DefaultBGMVolume
	}
	return EditTimeline{
		Version: 1,
		FPS:     fps,
		Width:   width,
		Height:  height,
		Tracks: []Track{
			{ID: "v1", Name: "Video", Kind: TrackVideo, Volume: 1.0, Clips: []TimelineClip{}},
			{ID: "a1", Name: "Voice", Kind: TrackAudioNarration, Volume: 1.0, Clips: []TimelineClip{}},
			{ID: "a2", Name: "BGM", Kind: TrackAudioBGM, Volume: bgmVolume, Clips: []TimelineClip{}},
		},
		Subtitles: []SubtitleCue{},
	}
}

// TimelineSpanMs returns the playback duration of the clip on the timeline after accounting for speed.
func (c TimelineClip) TimelineSpanMs() int64 {
	srcSpan := c.SourceOutMs - c.SourceInMs
	if srcSpan <= 0 {
		return 0
	}
	spd := c.Speed
	if spd <= 0 {
		spd = 1.0
	}
	return max(1, int64(math.Round(float64(srcSpan)/spd)))
}

// TimelineEndMs returns timeline_start_ms + TimelineSpanMs().
func (c TimelineClip) TimelineEndMs() int64 {
	return c.TimelineStartMs + c.TimelineSpanMs()
}

// Normalize fills default fields and sorts clips/subtitles deterministically.
func (tl *EditTimeline) Normalize() {
	if tl.Version <= 0 {
		tl.Version = 1
	}
	if tl.FPS <= 0 {
		tl.FPS = BaselineVideoFPS
	}
	if tl.Width <= 0 {
		tl.Width = BaselineVideoWidth
	}
	if tl.Height <= 0 {
		tl.Height = BaselineVideoHeight
	}
	if tl.Width%2 != 0 {
		tl.Width--
	}
	if tl.Height%2 != 0 {
		tl.Height--
	}
	if tl.Tracks == nil {
		tl.Tracks = []Track{}
	}
	for ti := range tl.Tracks {
		tr := &tl.Tracks[ti]
		tr.ID = strings.TrimSpace(tr.ID)
		if tr.ID == "" {
			tr.ID = fmt.Sprintf("track-%d", ti+1)
		}
		tr.Name = strings.TrimSpace(tr.Name)
		if tr.Kind == "" {
			tr.Kind = TrackVideo
		}
		if tr.Volume < 0 {
			tr.Volume = 0
		}
		if tr.Clips == nil {
			tr.Clips = []TimelineClip{}
		}
		for ci := range tr.Clips {
			cl := &tr.Clips[ci]
			cl.ID = strings.TrimSpace(cl.ID)
			if cl.ID == "" {
				cl.ID = "clip-" + uuid.NewString()[:8]
			}
			cl.FilePath = strings.TrimSpace(cl.FilePath)
			cl.AssetID = strings.TrimSpace(cl.AssetID)
			cl.ShotID = strings.TrimSpace(cl.ShotID)
			cl.Label = strings.TrimSpace(cl.Label)
			if cl.Speed <= 0 {
				cl.Speed = 1.0
			}
			if cl.Volume < 0 {
				cl.Volume = 0
			}
			if cl.TransitionIn == "" {
				cl.TransitionIn = TransitionNone
			}
			if cl.TransitionMs < 0 {
				cl.TransitionMs = 0
			}
			if cl.KenBurnsPreset == "" {
				cl.KenBurnsPreset = KenBurnsNone
			}
		}
		slices.SortStableFunc(tr.Clips, func(a, b TimelineClip) int {
			if a.TimelineStartMs != b.TimelineStartMs {
				if a.TimelineStartMs < b.TimelineStartMs {
					return -1
				}
				return 1
			}
			return strings.Compare(a.ID, b.ID)
		})
	}
	if tl.Subtitles == nil {
		tl.Subtitles = []SubtitleCue{}
	}
	for si := range tl.Subtitles {
		sub := &tl.Subtitles[si]
		sub.ID = strings.TrimSpace(sub.ID)
		if sub.ID == "" {
			sub.ID = fmt.Sprintf("sub-%d", si+1)
		}
		sub.Speaker = strings.TrimSpace(sub.Speaker)
		sub.Text = strings.TrimSpace(sub.Text)
		sub.StylePreset = strings.TrimSpace(sub.StylePreset)
	}
	slices.SortStableFunc(tl.Subtitles, func(a, b SubtitleCue) int {
		if a.StartMs != b.StartMs {
			if a.StartMs < b.StartMs {
				return -1
			}
			return 1
		}
		return strings.Compare(a.ID, b.ID)
	})
	for mi := range tl.Markers {
		m := &tl.Markers[mi]
		m.ID = strings.TrimSpace(m.ID)
		if m.ID == "" {
			m.ID = fmt.Sprintf("mk-%d", mi+1)
		}
		m.Label = strings.TrimSpace(m.Label)
		m.Color = strings.TrimSpace(m.Color)
	}
}

// Validate normalizes and validates EditTimeline invariants.
func (tl *EditTimeline) Validate() error {
	tl.Normalize()
	if tl.FPS < 1 || tl.FPS > 120 {
		return apperr.Validation("timeline fps must be between 1 and 120")
	}
	if tl.Width < 64 || tl.Width > 16384 || tl.Height < 64 || tl.Height > 16384 {
		return apperr.Validation("timeline width and height must be between 64 and 16384")
	}
	trackSeen := make(map[string]bool, len(tl.Tracks))
	clipSeen := make(map[string]bool)
	for _, tr := range tl.Tracks {
		if !tr.Kind.Valid() {
			return apperr.Validation(fmt.Sprintf("unsupported track kind %q", tr.Kind))
		}
		if trackSeen[tr.ID] {
			return apperr.Validation(fmt.Sprintf("duplicate track id %q", tr.ID))
		}
		trackSeen[tr.ID] = true
		if tr.Volume < 0 || tr.Volume > 2.0 {
			return apperr.Validation(fmt.Sprintf("track %q volume must be between 0 and 2.0", tr.ID))
		}
		for _, cl := range tr.Clips {
			if clipSeen[cl.ID] {
				return apperr.Validation(fmt.Sprintf("duplicate clip id %q", cl.ID))
			}
			clipSeen[cl.ID] = true
			if cl.FilePath == "" && cl.AssetID == "" {
				return apperr.Validation(fmt.Sprintf("clip %q requires file_path or asset_id", cl.ID))
			}
			if cl.TimelineStartMs < 0 {
				return apperr.Validation(fmt.Sprintf("clip %q timeline_start_ms must be >= 0", cl.ID))
			}
			if cl.SourceInMs < 0 || cl.SourceOutMs <= cl.SourceInMs {
				return apperr.Validation(fmt.Sprintf("clip %q requires 0 <= source_in_ms < source_out_ms", cl.ID))
			}
			if cl.Speed < 0.25 || cl.Speed > 4.0 {
				return apperr.Validation(fmt.Sprintf("clip %q speed must be between 0.25 and 4.0", cl.ID))
			}
			if cl.Volume < 0 || cl.Volume > 2.0 {
				return apperr.Validation(fmt.Sprintf("clip %q volume must be between 0 and 2.0", cl.ID))
			}
			if !cl.TransitionIn.Valid() {
				return apperr.Validation(fmt.Sprintf("clip %q has invalid transition_in %q", cl.ID, cl.TransitionIn))
			}
			if !cl.KenBurnsPreset.Valid() {
				return apperr.Validation(fmt.Sprintf("clip %q has invalid ken_burns_preset %q", cl.ID, cl.KenBurnsPreset))
			}
		}
	}
	subSeen := make(map[string]bool, len(tl.Subtitles))
	for _, sub := range tl.Subtitles {
		if subSeen[sub.ID] {
			return apperr.Validation(fmt.Sprintf("duplicate subtitle id %q", sub.ID))
		}
		subSeen[sub.ID] = true
		if sub.StartMs < 0 || sub.EndMs <= sub.StartMs {
			return apperr.Validation(fmt.Sprintf("subtitle %q requires 0 <= start_ms < end_ms", sub.ID))
		}
		if sub.Text == "" {
			return apperr.Validation(fmt.Sprintf("subtitle %q text cannot be empty", sub.ID))
		}
	}
	for _, mk := range tl.Markers {
		if mk.TimeMs < 0 {
			return apperr.Validation(fmt.Sprintf("marker %q time_ms must be >= 0", mk.ID))
		}
	}
	return nil
}

// DurationMs returns the total timeline duration across all tracks and subtitle cues.
func (tl EditTimeline) DurationMs() int64 {
	var maxEnd int64
	for _, tr := range tl.Tracks {
		for _, cl := range tr.Clips {
			if end := cl.TimelineEndMs(); end > maxEnd {
				maxEnd = end
			}
		}
	}
	for _, sub := range tl.Subtitles {
		if sub.EndMs > maxEnd {
			maxEnd = sub.EndMs
		}
	}
	return maxEnd
}

// Clone returns a deep copy of EditTimeline safe for mutation.
func (tl EditTimeline) Clone() EditTimeline {
	b, _ := json.Marshal(tl)
	var copyTL EditTimeline
	_ = json.Unmarshal(b, &copyTL)
	return copyTL
}

// ApplyTimelineOps applies a sequence of atomic TimelineOp edits to tl and validates the resulting timeline.
func ApplyTimelineOps(tl EditTimeline, ops []TimelineOp) (EditTimeline, error) {
	if len(ops) == 0 {
		return EditTimeline{}, apperr.Validation("at least one timeline operation is required")
	}
	cur := tl.Clone()
	cur.Normalize()

	findTrack := func(id string) int {
		for i := range cur.Tracks {
			if cur.Tracks[i].ID == id {
				return i
			}
		}
		return -1
	}
	findClip := func(trackID, clipID string) (int, int) {
		for ti := range cur.Tracks {
			if trackID != "" && cur.Tracks[ti].ID != trackID {
				continue
			}
			for ci := range cur.Tracks[ti].Clips {
				if cur.Tracks[ti].Clips[ci].ID == clipID {
					return ti, ci
				}
			}
		}
		return -1, -1
	}

	for idx, op := range ops {
		kind := strings.ToLower(strings.TrimSpace(op.Op))
		switch kind {
		case "replace_timeline":
			if op.Timeline == nil {
				return EditTimeline{}, apperr.Validation(fmt.Sprintf("op[%d] replace_timeline requires timeline", idx))
			}
			cur = op.Timeline.Clone()
			cur.Normalize()

		case "set_format":
			if op.FPS != nil {
				cur.FPS = *op.FPS
			}
			if op.Width != nil {
				cur.Width = *op.Width
			}
			if op.Height != nil {
				cur.Height = *op.Height
			}

		case "add_track":
			trKind := op.TrackKind
			if trKind == "" {
				trKind = TrackVideo
			}
			id := strings.TrimSpace(op.TrackID)
			if id == "" {
				id = "track-" + uuid.NewString()[:8]
			}
			if findTrack(id) >= 0 {
				return EditTimeline{}, apperr.Validation(fmt.Sprintf("track %q already exists", id))
			}
			vol := 1.0
			if op.Volume != nil {
				vol = *op.Volume
			}
			name := string(trKind)
			if op.TrackName != nil && strings.TrimSpace(*op.TrackName) != "" {
				name = strings.TrimSpace(*op.TrackName)
			}
			muted := op.Muted != nil && *op.Muted
			solo := op.Solo != nil && *op.Solo
			locked := op.Locked != nil && *op.Locked
			cur.Tracks = append(cur.Tracks, Track{
				ID:     id,
				Name:   name,
				Kind:   trKind,
				Muted:  muted,
				Solo:   solo,
				Locked: locked,
				Volume: vol,
				Clips:  []TimelineClip{},
			})

		case "update_track":
			ti := findTrack(strings.TrimSpace(op.TrackID))
			if ti < 0 {
				return EditTimeline{}, apperr.NotFound(fmt.Sprintf("track %q not found", op.TrackID))
			}
			tr := &cur.Tracks[ti]
			if op.TrackName != nil {
				tr.Name = strings.TrimSpace(*op.TrackName)
			}
			if op.Muted != nil {
				tr.Muted = *op.Muted
			}
			if op.Solo != nil {
				tr.Solo = *op.Solo
			}
			if op.Locked != nil {
				tr.Locked = *op.Locked
			}
			if op.Volume != nil {
				tr.Volume = *op.Volume
			}

		case "remove_track":
			ti := findTrack(strings.TrimSpace(op.TrackID))
			if ti < 0 {
				return EditTimeline{}, apperr.NotFound(fmt.Sprintf("track %q not found", op.TrackID))
			}
			if cur.Tracks[ti].Locked {
				return EditTimeline{}, apperr.Validation(fmt.Sprintf("track %q is locked", op.TrackID))
			}
			cur.Tracks = append(cur.Tracks[:ti], cur.Tracks[ti+1:]...)

		case "add_clip":
			if op.Clip == nil {
				return EditTimeline{}, apperr.Validation(fmt.Sprintf("op[%d] add_clip requires clip", idx))
			}
			ti := findTrack(strings.TrimSpace(op.TrackID))
			if ti < 0 {
				return EditTimeline{}, apperr.NotFound(fmt.Sprintf("track %q not found", op.TrackID))
			}
			if cur.Tracks[ti].Locked {
				return EditTimeline{}, apperr.Validation(fmt.Sprintf("track %q is locked", cur.Tracks[ti].ID))
			}
			cl := *op.Clip
			if strings.TrimSpace(cl.ID) == "" {
				cl.ID = "clip-" + uuid.NewString()[:8]
			}
			if cl.Speed <= 0 {
				cl.Speed = 1.0
			}
			if cl.Volume == 0 {
				cl.Volume = 1.0
			}
			cur.Tracks[ti].Clips = append(cur.Tracks[ti].Clips, cl)

		case "update_clip":
			ti, ci := findClip(strings.TrimSpace(op.TrackID), strings.TrimSpace(op.ClipID))
			if ti < 0 {
				return EditTimeline{}, apperr.NotFound(fmt.Sprintf("clip %q not found", op.ClipID))
			}
			if cur.Tracks[ti].Locked {
				return EditTimeline{}, apperr.Validation(fmt.Sprintf("track %q is locked", cur.Tracks[ti].ID))
			}
			cl := &cur.Tracks[ti].Clips[ci]
			if op.Clip != nil {
				updated := *op.Clip
				updated.ID = cl.ID
				*cl = updated
			}
			if op.TimelineStartMs != nil {
				cl.TimelineStartMs = *op.TimelineStartMs
			}
			if op.SourceInMs != nil {
				cl.SourceInMs = *op.SourceInMs
			}
			if op.SourceOutMs != nil {
				cl.SourceOutMs = *op.SourceOutMs
			}
			if op.Speed != nil {
				cl.Speed = *op.Speed
			}
			if op.Volume != nil {
				cl.Volume = *op.Volume
			}
			if op.TransitionIn != nil {
				cl.TransitionIn = *op.TransitionIn
			}
			if op.TransitionMs != nil {
				cl.TransitionMs = *op.TransitionMs
			}
			if op.KenBurnsPreset != nil {
				cl.KenBurnsPreset = *op.KenBurnsPreset
			}
			if op.Label != nil {
				cl.Label = strings.TrimSpace(*op.Label)
			}

		case "move_clip":
			ti, ci := findClip(strings.TrimSpace(op.TrackID), strings.TrimSpace(op.ClipID))
			if ti < 0 {
				return EditTimeline{}, apperr.NotFound(fmt.Sprintf("clip %q not found", op.ClipID))
			}
			if cur.Tracks[ti].Locked {
				return EditTimeline{}, apperr.Validation(fmt.Sprintf("track %q is locked", cur.Tracks[ti].ID))
			}
			cl := cur.Tracks[ti].Clips[ci]
			if op.TimelineStartMs != nil {
				cl.TimelineStartMs = *op.TimelineStartMs
			}
			targetTrackID := strings.TrimSpace(op.TargetTrackID)
			if targetTrackID != "" && targetTrackID != cur.Tracks[ti].ID {
				targetIdx := findTrack(targetTrackID)
				if targetIdx < 0 {
					return EditTimeline{}, apperr.NotFound(fmt.Sprintf("target_track_id %q not found", targetTrackID))
				}
				if cur.Tracks[targetIdx].Locked {
					return EditTimeline{}, apperr.Validation(fmt.Sprintf("target track %q is locked", targetTrackID))
				}
				cur.Tracks[ti].Clips = append(cur.Tracks[ti].Clips[:ci], cur.Tracks[ti].Clips[ci+1:]...)
				cur.Tracks[targetIdx].Clips = append(cur.Tracks[targetIdx].Clips, cl)
			} else {
				cur.Tracks[ti].Clips[ci] = cl
			}

		case "remove_clip":
			ti, ci := findClip(strings.TrimSpace(op.TrackID), strings.TrimSpace(op.ClipID))
			if ti < 0 {
				return EditTimeline{}, apperr.NotFound(fmt.Sprintf("clip %q not found", op.ClipID))
			}
			if cur.Tracks[ti].Locked {
				return EditTimeline{}, apperr.Validation(fmt.Sprintf("track %q is locked", cur.Tracks[ti].ID))
			}
			cur.Tracks[ti].Clips = append(cur.Tracks[ti].Clips[:ci], cur.Tracks[ti].Clips[ci+1:]...)

		case "split_clip":
			ti, ci := findClip(strings.TrimSpace(op.TrackID), strings.TrimSpace(op.ClipID))
			if ti < 0 {
				return EditTimeline{}, apperr.NotFound(fmt.Sprintf("clip %q not found", op.ClipID))
			}
			if cur.Tracks[ti].Locked {
				return EditTimeline{}, apperr.Validation(fmt.Sprintf("track %q is locked", cur.Tracks[ti].ID))
			}
			orig := cur.Tracks[ti].Clips[ci]
			spanMs := orig.TimelineSpanMs()
			deltaTimelineMs := op.SplitAtMs - orig.TimelineStartMs
			const minSplitSegmentMs = 100
			if deltaTimelineMs < minSplitSegmentMs || spanMs-deltaTimelineMs < minSplitSegmentMs {
				return EditTimeline{}, apperr.Validation(fmt.Sprintf("split_at_ms (%d) must leave at least %dms on both halves", op.SplitAtMs, minSplitSegmentMs))
			}
			spd := orig.Speed
			if spd <= 0 {
				spd = 1.0
			}
			deltaSrcMs := int64(math.Round(float64(deltaTimelineMs) * spd))
			splitSrcMs := orig.SourceInMs + deltaSrcMs
			if splitSrcMs <= orig.SourceInMs || splitSrcMs >= orig.SourceOutMs {
				return EditTimeline{}, apperr.Validation("split_at_ms is outside clip source bounds")
			}

			left := orig
			left.SourceOutMs = splitSrcMs

			right := orig
			newID := strings.TrimSpace(op.NewClipID)
			if newID == "" {
				newID = "clip-" + uuid.NewString()[:8]
			}
			right.ID = newID
			right.TimelineStartMs = op.SplitAtMs
			right.SourceInMs = splitSrcMs
			right.TransitionIn = TransitionNone
			right.TransitionMs = 0

			cur.Tracks[ti].Clips[ci] = left
			cur.Tracks[ti].Clips = append(cur.Tracks[ti].Clips, right)

		case "add_subtitle":
			if op.Subtitle == nil {
				return EditTimeline{}, apperr.Validation(fmt.Sprintf("op[%d] add_subtitle requires subtitle", idx))
			}
			cue := *op.Subtitle
			if strings.TrimSpace(cue.ID) == "" {
				cue.ID = "sub-" + uuid.NewString()[:8]
			}
			cur.Subtitles = append(cur.Subtitles, cue)

		case "update_subtitle":
			if op.Subtitle == nil {
				return EditTimeline{}, apperr.Validation(fmt.Sprintf("op[%d] update_subtitle requires subtitle", idx))
			}
			targetID := strings.TrimSpace(op.SubtitleID)
			if targetID == "" {
				targetID = strings.TrimSpace(op.Subtitle.ID)
			}
			found := -1
			for si := range cur.Subtitles {
				if cur.Subtitles[si].ID == targetID {
					found = si
					break
				}
			}
			if found < 0 {
				return EditTimeline{}, apperr.NotFound(fmt.Sprintf("subtitle %q not found", targetID))
			}
			updated := *op.Subtitle
			updated.ID = cur.Subtitles[found].ID
			cur.Subtitles[found] = updated

		case "remove_subtitle":
			targetID := strings.TrimSpace(op.SubtitleID)
			found := -1
			for si := range cur.Subtitles {
				if cur.Subtitles[si].ID == targetID {
					found = si
					break
				}
			}
			if found < 0 {
				return EditTimeline{}, apperr.NotFound(fmt.Sprintf("subtitle %q not found", targetID))
			}
			cur.Subtitles = append(cur.Subtitles[:found], cur.Subtitles[found+1:]...)

		case "add_marker":
			if op.Marker == nil {
				return EditTimeline{}, apperr.Validation(fmt.Sprintf("op[%d] add_marker requires marker", idx))
			}
			mk := *op.Marker
			if strings.TrimSpace(mk.ID) == "" {
				mk.ID = "mk-" + uuid.NewString()[:8]
			}
			cur.Markers = append(cur.Markers, mk)

		case "remove_marker":
			targetID := strings.TrimSpace(op.MarkerID)
			found := -1
			for mi := range cur.Markers {
				if cur.Markers[mi].ID == targetID {
					found = mi
					break
				}
			}
			if found < 0 {
				return EditTimeline{}, apperr.NotFound(fmt.Sprintf("marker %q not found", targetID))
			}
			cur.Markers = append(cur.Markers[:found], cur.Markers[found+1:]...)

		default:
			return EditTimeline{}, apperr.Validation(fmt.Sprintf("unsupported timeline op %q", op.Op))
		}
	}

	if err := cur.Validate(); err != nil {
		return EditTimeline{}, err
	}
	return cur, nil
}

// DefaultRevisionHistoryLimit bounds ListTimelineRevisions when no positive limit is specified.
const DefaultRevisionHistoryLimit = 30

// Phase3Repository defines persistence operations added in Studio Phase 3 (Timeline & Revisions).
type Phase3Repository interface {
	GetTimeline(ctx context.Context, episodeID string) (TimelineRecord, error)
	SaveTimelineRevision(ctx context.Context, episodeID string, expectedRevision int, actor TimelineActor, summary string, tl EditTimeline) (TimelineRecord, error)
	UndoTimeline(ctx context.Context, episodeID string, expectedRevision int) (TimelineRecord, error)
	RedoTimeline(ctx context.Context, episodeID string, expectedRevision int) (TimelineRecord, error)
	ListTimelineRevisions(ctx context.Context, episodeID string, limit int) ([]TimelineRevision, error)
}
