package studio

import (
	"math"
	"testing"

	"app/internal/apperr"
)

func TestEditTimelineValidateAndApplyAllOps(t *testing.T) {
	tl := DefaultEditTimeline(1920, 1080, 24)
	if err := tl.Validate(); err != nil {
		t.Fatalf("default timeline validate: %v", err)
	}
	if tl.Tracks[2].Volume != DefaultBGMVolume {
		t.Fatalf("default BGM track volume = %v, want %v", tl.Tracks[2].Volume, DefaultBGMVolume)
	}
	if tl.Markers == nil {
		t.Fatal("expected Markers slice initialized non-nil")
	}

	// 1. add_clip (normal omitted volume -> 1.0, muted volume 0 -> preserved 0), add_subtitle, split_clip, update_track
	clip1 := TimelineClip{
		ID:              "c1",
		ShotID:          "s1",
		AssetID:         "asset-v1",
		Label:           "Shot 1",
		FilePath:        "D:/proj/ep01/media/videos/shot1.mp4",
		TimelineStartMs: 0,
		SourceInMs:      0,
		SourceOutMs:     6000,
		Speed:           1.0,
		TransitionIn:    TransitionNone,
	}
	mutedClip := TimelineClip{
		ID:              "a1-muted",
		AssetID:         "asset-a1",
		Label:           "Muted Voice",
		FilePath:        "D:/proj/ep01/media/tts/shot1.wav",
		TimelineStartMs: 200,
		SourceInMs:      0,
		SourceOutMs:     4000,
		Speed:           1.0,
		Volume:          0,
		Mute:            true,
	}
	sub1 := SubtitleCue{
		ID:      "sub1",
		StartMs: 200,
		EndMs:   3800,
		Speaker: "Minh",
		Text:    "Đêm nay trời mưa rất to ở Sài Gòn.",
	}
	mk1 := TimelineMarker{
		ID:     "mk1",
		TimeMs: 1500,
		Label:  "Beat 1",
		Color:  "#ffaa00",
	}

	mutedTrue := true
	soloTrue := true
	newVol := 0.85
	fps30 := 30
	w1280 := 1280
	h720 := 720
	sfxName := "SFX"

	next, err := ApplyTimelineOps(tl, []TimelineOp{
		{Op: string(OpSetFormat), FPS: &fps30, Width: &w1280, Height: &h720},
		{Op: string(OpAddTrack), TrackID: "sfx-1", TrackKind: TrackAudioSFX, TrackName: &sfxName},
		{Op: string(OpAddClip), TrackID: "v1", Clip: &clip1},
		{Op: string(OpAddClip), TrackID: "a1", Clip: &mutedClip},
		{Op: string(OpAddSubtitle), Subtitle: &sub1},
		{Op: string(OpAddMarker), Marker: &mk1},
		{Op: string(OpSplitClip), TrackID: "v1", ClipID: "c1", NewClipID: "c2", SplitAtMs: 2500},
		{Op: string(OpUpdateTrack), TrackID: "a2", Muted: &mutedTrue, Solo: &soloTrue, Volume: &newVol},
	})
	if err != nil {
		t.Fatalf("ApplyTimelineOps initial batch: %v", err)
	}

	if next.FPS != 30 || next.Width != 1280 || next.Height != 720 {
		t.Fatalf("set_format not applied: %dx%d@%d", next.Width, next.Height, next.FPS)
	}
	if next.Tracks[0].Clips[0].Volume != 1.0 {
		t.Fatalf("expected unmuted clip volume default 1.0, got %v", next.Tracks[0].Clips[0].Volume)
	}
	if next.Tracks[1].Clips[0].Volume != 0 || !next.Tracks[1].Clips[0].Mute {
		t.Fatalf("expected muted clip volume 0 preserved, got %+v", next.Tracks[1].Clips[0])
	}
	if len(next.Tracks[0].Clips) != 2 {
		t.Fatalf("expected 2 clips after split, got %d", len(next.Tracks[0].Clips))
	}
	cLeft := next.Tracks[0].Clips[0]
	cRight := next.Tracks[0].Clips[1]
	if cLeft.SourceInMs != 0 || cLeft.SourceOutMs != 2500 {
		t.Fatalf("unexpected left split bounds: %+v", cLeft)
	}
	if cRight.ID != "c2" || cRight.TimelineStartMs != 2500 || cRight.SourceInMs != 2500 || cRight.SourceOutMs != 6000 {
		t.Fatalf("unexpected right split bounds: %+v", cRight)
	}
	if !next.Tracks[2].Muted || !next.Tracks[2].Solo || next.Tracks[2].Volume != 0.85 {
		t.Fatalf("track mute/solo/volume not updated: %+v", next.Tracks[2])
	}
	if next.DurationMs() != 6000 {
		t.Fatalf("DurationMs = %d, want 6000", next.DurationMs())
	}

	// 2. update_clip, move_clip, update_subtitle, remove_subtitle, remove_marker, remove_track, replace_timeline
	spd := 1.5
	lbl := "Trimmed"
	moveStart := int64(500)
	updatedSub := SubtitleCue{ID: "sub1", StartMs: 300, EndMs: 3500, Text: "Đã chỉnh sửa"}
	sfxClip := TimelineClip{ID: "sfx-c1", FilePath: "D:/proj/sfx.wav", SourceInMs: 0, SourceOutMs: 1000}

	next2, err := ApplyTimelineOps(next, []TimelineOp{
		{Op: string(OpAddClip), TrackID: "sfx-1", Clip: &sfxClip},
		{Op: string(OpMoveClip), TrackID: "sfx-1", ClipID: "sfx-c1", TargetTrackID: "a1", TimelineStartMs: &moveStart},
		{Op: string(OpUpdateClip), TrackID: "v1", ClipID: "c2", Speed: &spd, Label: &lbl},
		{Op: string(OpUpdateSubtitle), SubtitleID: "sub1", Subtitle: &updatedSub},
		{Op: string(OpRemoveTrack), TrackID: "sfx-1"},
		{Op: string(OpRemoveSubtitle), SubtitleID: "sub1"},
		{Op: string(OpRemoveMarker), MarkerID: "mk1"},
	})
	if err != nil {
		t.Fatalf("ApplyTimelineOps second batch: %v", err)
	}
	if len(next2.Tracks) != 3 || len(next2.Subtitles) != 0 || len(next2.Markers) != 0 {
		t.Fatalf("expected sfx track, subtitle, and marker removed: tracks=%d subs=%d markers=%d", len(next2.Tracks), len(next2.Subtitles), len(next2.Markers))
	}
	if len(next2.Tracks[1].Clips) != 2 {
		t.Fatalf("expected moved clip on track a1, got %d clips", len(next2.Tracks[1].Clips))
	}

	fresh := DefaultEditTimeline(1920, 1080, 24)
	replaced, err := ApplyTimelineOps(next2, []TimelineOp{
		{Op: string(OpReplaceTimeline), Timeline: &fresh},
	})
	if err != nil || replaced.Width != 1920 || len(replaced.Tracks[0].Clips) != 0 {
		t.Fatalf("replace_timeline failed: %+v err=%v", replaced, err)
	}

	// 3. Locking a track prevents clip mutations and track removal
	lockedTrue := true
	lockedTL, err := ApplyTimelineOps(next, []TimelineOp{
		{Op: string(OpUpdateTrack), TrackID: "v1", Locked: &lockedTrue},
	})
	if err != nil {
		t.Fatalf("lock track: %v", err)
	}
	for _, blockedOp := range []TimelineOp{
		{Op: string(OpRemoveClip), TrackID: "v1", ClipID: "c2"},
		{Op: string(OpRemoveTrack), TrackID: "v1"},
	} {
		if _, err := ApplyTimelineOps(lockedTL, []TimelineOp{blockedOp}); !apperr.IsCode(err, apperr.CodeValidation) {
			t.Fatalf("expected CodeValidation on locked track op %s, got %v", blockedOp.Op, err)
		}
	}
}

func TestEditTimelineCloneAndNonFiniteRejections(t *testing.T) {
	tl := DefaultEditTimeline(1920, 1080, 24)
	tl.Tracks[0].Clips = []TimelineClip{{
		ID:          "c1",
		FilePath:    "D:/v.mp4",
		SourceInMs:  0,
		SourceOutMs: 2000,
		Speed:       1.0,
		Volume:      1.0,
		Peaks:       []float64{0.1, 0.5, 0.9},
	}}
	tl.Subtitles = []SubtitleCue{{ID: "s1", StartMs: 0, EndMs: 1000, Text: "Hi"}}
	tl.Markers = []TimelineMarker{{ID: "m1", TimeMs: 500, Label: "M"}}

	cloned := tl.Clone()
	cloned.Tracks[0].Clips[0].Peaks[0] = 99
	cloned.Subtitles[0].Text = "Changed"
	cloned.Markers[0].Label = "Changed"
	if tl.Tracks[0].Clips[0].Peaks[0] != 0.1 || tl.Subtitles[0].Text != "Hi" || tl.Markers[0].Label != "M" {
		t.Fatal("Clone() must deep-copy Peaks, Subtitles, and Markers slices")
	}

	nanVal := math.NaN()
	infVal := math.Inf(1)

	// Reject NaN/Inf via ApplyTimelineOps and Validate
	if _, err := ApplyTimelineOps(tl, []TimelineOp{
		{Op: string(OpUpdateTrack), TrackID: "v1", Volume: &nanVal},
	}); !apperr.IsCode(err, apperr.CodeValidation) {
		t.Fatalf("expected NaN track volume rejected, got %v", err)
	}
	if _, err := ApplyTimelineOps(tl, []TimelineOp{
		{Op: string(OpUpdateClip), TrackID: "v1", ClipID: "c1", Speed: &infVal},
	}); !apperr.IsCode(err, apperr.CodeValidation) {
		t.Fatalf("expected +Inf clip speed rejected, got %v", err)
	}

	badPeaks := tl.Clone()
	badPeaks.Tracks[0].Clips[0].Peaks[1] = math.NaN()
	if err := badPeaks.Validate(); !apperr.IsCode(err, apperr.CodeValidation) {
		t.Fatalf("expected NaN clip peak rejected, got %v", err)
	}

	tall := DefaultEditTimeline(7680, 4320, 24)
	if err := tall.ValidateWithMaxDim(4096); !apperr.IsCode(err, apperr.CodeValidation) {
		t.Fatalf("expected 8K rejected when maxDim=4096, got %v", err)
	}
	if err := tall.ValidateWithMaxDim(8192); err != nil {
		t.Fatalf("expected 8K accepted when maxDim=8192, got %v", err)
	}
}

func TestDetectTimelineSpeechIssues(t *testing.T) {
	tl := EditTimeline{
		Version: 1,
		FPS:     24,
		Width:   1920,
		Height:  1080,
		Tracks: []Track{
			{
				ID:   "v1",
				Kind: TrackVideo,
				Clips: []TimelineClip{
					{ID: "v-s1", ShotID: "s1", FilePath: "s1.mp4", TimelineStartMs: 0, SourceInMs: 0, SourceOutMs: 4000, Speed: 1.0, Volume: 1.0},
					{ID: "v-s2", ShotID: "s2", FilePath: "s2.mp4", TimelineStartMs: 4000, SourceInMs: 0, SourceOutMs: 4000, Speed: 1.0, Volume: 1.0},
				},
			},
			{
				ID:   "a1",
				Kind: TrackAudioNarration,
				Clips: []TimelineClip{
					{ID: "a-s1", ShotID: "s1", FilePath: "s1.wav", TimelineStartMs: 0, SourceInMs: 0, SourceOutMs: 5200, Speed: 1.0, Volume: 1.0},
					{ID: "a-s2", ShotID: "s2", FilePath: "s2.wav", TimelineStartMs: 4000, SourceInMs: 0, SourceOutMs: 3000, Speed: 1.0, Volume: 1.0},
				},
			},
		},
	}

	issues := DetectTimelineSpeechIssues(tl, nil, 150)
	if len(issues) != 2 {
		t.Fatalf("expected 2 issues (overrun + collision), got %d: %+v", len(issues), issues)
	}
	if issues[0].Code != "narration_overrun" || issues[0].ShotID != "s1" || issues[0].OverflowMs != 1200 {
		t.Fatalf("unexpected first issue: %+v", issues[0])
	}
	if issues[1].Code != "narration_source_collision" || issues[1].ClipID != "a-s2" || issues[1].OverlapMs != 1200 {
		t.Fatalf("unexpected second issue: %+v", issues[1])
	}

	// Speed-fitting a-s1 to 1.3x resolves both overrun and collision (5200/1.3 = 4000ms).
	tl.Tracks[1].Clips[0].Speed = 1.3
	resolved := DetectTimelineSpeechIssues(tl, nil, 150)
	if len(resolved) != 0 {
		t.Fatalf("expected 0 issues after speed fit, got %+v", resolved)
	}
}
