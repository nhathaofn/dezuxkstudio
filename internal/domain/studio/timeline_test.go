package studio

import (
	"testing"
)

func TestEditTimelineValidateAndApplyOps(t *testing.T) {
	tl := DefaultEditTimeline(1920, 1080, 24)
	if err := tl.Validate(); err != nil {
		t.Fatalf("default timeline validate: %v", err)
	}

	// Add video clip + audio clip + Vietnamese subtitle + split + trim + mute/solo
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
		Volume:          1.0,
		TransitionIn:    TransitionNone,
		TransitionMs:    0,
	}
	aud1 := TimelineClip{
		ID:              "a1-clip",
		AssetID:         "asset-a1",
		Label:           "Voice 1",
		FilePath:        "D:/proj/ep01/media/tts/shot1.wav",
		TimelineStartMs: 200,
		SourceInMs:      0,
		SourceOutMs:     4000,
		Speed:           1.0,
		Volume:          1.0,
	}
	sub1 := SubtitleCue{
		ID:      "sub1",
		StartMs: 200,
		EndMs:   3800,
		Speaker: "Minh",
		Text:    "Đêm nay trời mưa rất to ở Sài Gòn.",
	}

	mutedTrue := true
	soloTrue := true
	newVol := 0.85
	next, err := ApplyTimelineOps(tl, []TimelineOp{
		{Op: "add_clip", TrackID: "v1", Clip: &clip1},
		{Op: "add_clip", TrackID: "a1", Clip: &aud1},
		{Op: "add_subtitle", Subtitle: &sub1},
		{Op: "split_clip", TrackID: "v1", ClipID: "c1", NewClipID: "c2", SplitAtMs: 2500},
		{Op: "update_track", TrackID: "a2", Muted: &mutedTrue, Solo: &soloTrue, Volume: &newVol},
	})
	if err != nil {
		t.Fatalf("ApplyTimelineOps: %v", err)
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

	// Locking a track prevents subsequent clip mutations on it
	lockedTrue := true
	lockedTL, err := ApplyTimelineOps(next, []TimelineOp{
		{Op: "update_track", TrackID: "v1", Locked: &lockedTrue},
	})
	if err != nil {
		t.Fatalf("lock track: %v", err)
	}
	if _, err := ApplyTimelineOps(lockedTL, []TimelineOp{
		{Op: "remove_clip", TrackID: "v1", ClipID: "c2"},
	}); err == nil {
		t.Fatal("expected error modifying clip on locked track")
	}
}
