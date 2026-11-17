package studio

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"app/internal/apperr"
	"app/internal/contracts"
	domainbinaries "app/internal/domain/binaries"
	domainstudio "app/internal/domain/studio"
	"app/internal/engine"
	"app/internal/progress"
)

type externalAudioManifestEntry struct {
	TrackKind       domainstudio.TrackKind `json:"track_kind,omitempty"`
	Label           string                 `json:"label,omitempty"`
	FilePath        string                 `json:"file_path"`
	AssetID         string                 `json:"asset_id,omitempty"`
	TimelineStartMs int64                  `json:"timeline_start_ms,omitempty"`
	SourceInMs      int64                  `json:"source_in_ms,omitempty"`
	SourceOutMs     int64                  `json:"source_out_ms,omitempty"`
	Volume          float64                `json:"volume,omitempty"`
}

// NewTimelineAutoAssembleNode builds tool.timeline_auto_assemble to turn episode shots & audio into an EditTimeline.
func NewTimelineAutoAssembleNode(opts Options) engine.Node {
	opts.StudioConfig = withStudioDefaults(opts.StudioConfig)
	defaultRes := opts.StudioConfig.DefaultResolution
	defaultFPS := opts.StudioConfig.DefaultFPS

	spec := engine.Spec{
		Type:        NodeTypeTimelineAutoAssem,
		Provider:    "studio",
		Label:       "Timeline Auto-Assembler",
		Description: "Sequentially assembles ready video shots, narration/dialogue audio tracks, Vietnamese subtitle cues, transitions, and optional BGM into a canonical multi-track EditTimeline.",
		Inputs: []engine.Port{
			{Name: "shots_json", Label: "Shots JSON", Type: engine.PortText},
			{Name: "audio_manifest_json", Label: "Audio Manifest JSON", Type: engine.PortText, Optional: true},
		},
		Outputs: []engine.Port{
			{Name: "timeline_json", Label: "EditTimeline JSON", Type: engine.PortText},
		},
		Params: []engine.Param{
			{Name: "episode_id", Label: "Episode ID", Kind: engine.ParamText},
			{Name: "resolution", Label: "Resolution (WxH)", Kind: engine.ParamText, Default: defaultRes},
			{Name: "fps", Label: "Frame Rate (FPS)", Kind: engine.ParamInteger, Min: 1, Max: 120, Default: strconv.Itoa(defaultFPS)},
			{Name: "transition_default", Label: "Default Transition", Kind: engine.ParamSelect, Options: optionStrings(domainstudio.TransitionTypes()), Default: string(domainstudio.TransitionCrossfade)},
			{Name: "transition_ms", Label: "Transition Duration (ms)", Kind: engine.ParamInteger, Min: 0, Max: 5000, Default: strconv.Itoa(opts.StudioConfig.TimelineDefaultTransitionMs)},
			{Name: "bgm_path", Label: "Background Music File", Kind: engine.ParamText},
			{Name: "bgm_volume", Label: "BGM Volume (0..2)", Kind: engine.ParamText, Default: fmt.Sprintf("%.2f", opts.StudioConfig.TimelineDefaultBGMVolume)},
			{Name: "project_dir", Label: "Episode Directory", Kind: engine.ParamText, Role: engine.ParamRoleOutputDir},
		},
	}

	return engine.Typed(spec, func(ctx context.Context, in contracts.TimelineAutoAssembleInput) (contracts.TimelineAutoAssembleOutput, error) {
		shots, err := parseShotsForAssemble(in.ShotsJSON)
		if err != nil {
			return contracts.TimelineAutoAssembleOutput{}, err
		}
		extraAudio, err := parseAudioManifestForAssemble(in.AudioManifestJSON)
		if err != nil {
			return contracts.TimelineAutoAssembleOutput{}, err
		}

		resStr := strings.TrimSpace(in.Resolution)
		if resStr == "" {
			resStr = defaultRes
		}
		width, height, err := parseResolutionWxH(resStr, opts.StudioConfig.MaxVideoDimension)
		if err != nil {
			return contracts.TimelineAutoAssembleOutput{}, err
		}
		fps := in.FPS
		if fps <= 0 {
			fps = defaultFPS
		}
		if fps < 1 || fps > 120 {
			return contracts.TimelineAutoAssembleOutput{}, apperr.Validation("fps must be between 1 and 120")
		}

		transType := domainstudio.TransitionType(strings.TrimSpace(in.TransitionDefault))
		if transType == "" {
			transType = domainstudio.TransitionCrossfade
		}
		if !transType.Valid() {
			return contracts.TimelineAutoAssembleOutput{}, apperr.Validation(fmt.Sprintf("unsupported transition_default %q", transType))
		}
		transMs := in.TransitionMs
		if transMs < 0 {
			transMs = 0
		}

		bgmVol := in.BGMVolume
		if bgmVol <= 0 {
			bgmVol = opts.StudioConfig.TimelineDefaultBGMVolume
		}
		if bgmVol > 2.0 {
			bgmVol = 2.0
		}

		// Filter shots with video media and sort by seq.
		ready := make([]domainstudio.Shot, 0, len(shots))
		for _, sh := range shots {
			if strings.TrimSpace(sh.VideoPath) == "" {
				continue
			}
			ready = append(ready, sh)
		}
		if len(ready) == 0 {
			return contracts.TimelineAutoAssembleOutput{}, apperr.Validation("shots_json contains no shots with video_path")
		}
		slices.SortStableFunc(ready, func(a, b domainstudio.Shot) int {
			if a.Seq != b.Seq {
				if a.Seq < b.Seq {
					return -1
				}
				return 1
			}
			return strings.Compare(a.ID, b.ID)
		})

		progress.Report(ctx, progress.Update{Percent: 10, Message: "Validating and probing timeline clips…", Key: "studio.progress.timeline_assemble"})

		var ffprobeExe string
		if opts.Binaries != nil {
			if exe, _, _, rerr := opts.Binaries.Resolve(ctx, domainbinaries.IDFFprobe); rerr == nil {
				ffprobeExe = strings.TrimSpace(exe)
			}
		}

		probeMs := func(path string, fallbackSec int) int64 {
			if ffprobeExe != "" {
				probeCtx, cancel := context.WithTimeout(ctx, opts.StudioConfig.FFprobeTimeout)
				defer cancel()
				if pOut, perr := probeWithFFprobe(probeCtx, ffprobeExe, path); perr == nil && pOut.DurationSec > 0.05 {
					return int64(math.Round(pOut.DurationSec * 1000))
				}
			}
			if fallbackSec > 0 {
				return int64(fallbackSec) * 1000
			}
			return int64(opts.StudioConfig.DefaultShotDurationSec) * 1000
		}

		tl := domainstudio.EditTimeline{
			Version: 1,
			FPS:     fps,
			Width:   width,
			Height:  height,
		}
		videoTrack := domainstudio.Track{ID: "v1", Name: "Video", Kind: domainstudio.TrackVideo, Volume: 1.0, Clips: []domainstudio.TimelineClip{}}
		speechKind := domainstudio.TrackAudioNarration
		hasDialogues := false
		for _, sh := range ready {
			if len(sh.Dialogues) > 0 {
				hasDialogues = true
				break
			}
		}
		if hasDialogues {
			speechKind = domainstudio.TrackAudioDialogue
		}
		speechTrack := domainstudio.Track{ID: "a1", Name: "Voice / Speech", Kind: speechKind, Volume: 1.0, Clips: []domainstudio.TimelineClip{}}

		var cursorMs int64
		var prevClipDurMs int64

		for idx, sh := range ready {
			vidPath, rerr := resolveInputFile(ctx, opts, sh.VideoPath)
			if rerr != nil {
				return contracts.TimelineAutoAssembleOutput{}, rerr
			}
			vidDurMs := probeMs(vidPath, sh.DurationSec)
			if vidDurMs < 200 {
				vidDurMs = 1000
			}

			var audPath string
			var audDurMs int64
			if strings.TrimSpace(sh.AudioPath) != "" {
				resolvedAud, aerr := resolveInputFile(ctx, opts, sh.AudioPath)
				if aerr != nil {
					return contracts.TimelineAutoAssembleOutput{}, aerr
				}
				audPath = resolvedAud
				audDurMs = probeMs(audPath, sh.DurationSec)
			}

			startMs := cursorMs
			clipTrans := domainstudio.TransitionNone
			var clipTransMs int64
			if idx > 0 && transType != domainstudio.TransitionNone && transMs > 0 {
				switch transType {
				case domainstudio.TransitionFade:
					clipTrans = domainstudio.TransitionFade
					clipTransMs = min(transMs, vidDurMs/3)
				default:
					overlap := min(transMs, prevClipDurMs/3, vidDurMs/3)
					if overlap >= 80 {
						startMs = max(0, cursorMs-overlap)
						clipTrans = transType
						clipTransMs = overlap
					}
				}
			}

			label := strings.TrimSpace(sh.Title)
			if label == "" {
				label = fmt.Sprintf("Shot %d", sh.Seq)
			}
			clipID := "clip-" + strings.TrimSpace(sh.ID)
			if clipID == "clip-" {
				clipID = fmt.Sprintf("clip-%d", idx+1)
			}
			videoTrack.Clips = append(videoTrack.Clips, domainstudio.TimelineClip{
				ID:              clipID,
				ShotID:          sh.ID,
				AssetID:         sh.VideoAssetID,
				Label:           label,
				FilePath:        vidPath,
				TimelineStartMs: startMs,
				SourceInMs:      0,
				SourceOutMs:     vidDurMs,
				Speed:           1.0,
				Volume:          1.0,
				TransitionIn:    clipTrans,
				TransitionMs:    clipTransMs,
				KenBurnsPreset:  domainstudio.KenBurnsNone,
			})

			speechSpanMs := vidDurMs
			if audPath != "" && audDurMs > 0 {
				speechSpanMs = audDurMs
				speechTrack.Clips = append(speechTrack.Clips, domainstudio.TimelineClip{
					ID:              fmt.Sprintf("aud-%d", idx+1),
					ShotID:          sh.ID,
					AssetID:         sh.AudioAssetID,
					Label:           label + " Audio",
					FilePath:        audPath,
					TimelineStartMs: startMs,
					SourceInMs:      0,
					SourceOutMs:     audDurMs,
					Speed:           1.0,
					Volume:          1.0,
					TransitionIn:    domainstudio.TransitionNone,
					TransitionMs:    0,
				})
			}

			cues := buildSubtitleCuesForShot(sh, startMs, max(vidDurMs, speechSpanMs), idx+1)
			tl.Subtitles = append(tl.Subtitles, cues...)

			cursorMs = startMs + vidDurMs
			prevClipDurMs = vidDurMs
		}

		tl.Tracks = append(tl.Tracks, videoTrack)
		if len(speechTrack.Clips) > 0 {
			tl.Tracks = append(tl.Tracks, speechTrack)
		}

		// Optional extra audio manifest clips (grouped by track kind).
		extraTracks := make(map[domainstudio.TrackKind]*domainstudio.Track)
		for i, ea := range extraAudio {
			if strings.TrimSpace(ea.FilePath) == "" {
				continue
			}
			resolved, rerr := resolveInputFile(ctx, opts, ea.FilePath)
			if rerr != nil {
				return contracts.TimelineAutoAssembleOutput{}, rerr
			}
			kind := ea.TrackKind
			if !kind.Valid() || kind == domainstudio.TrackVideo || kind == domainstudio.TrackText {
				kind = domainstudio.TrackAudioSFX
			}
			tr, exists := extraTracks[kind]
			if !exists {
				tr = &domainstudio.Track{
					ID:     fmt.Sprintf("a-%s", strings.TrimPrefix(string(kind), "audio_")),
					Name:   strings.TrimPrefix(string(kind), "audio_"),
					Kind:   kind,
					Volume: 1.0,
					Clips:  []domainstudio.TimelineClip{},
				}
				extraTracks[kind] = tr
			}
			outMs := ea.SourceOutMs
			if outMs <= ea.SourceInMs {
				outMs = ea.SourceInMs + probeMs(resolved, 5)
			}
			vol := ea.Volume
			if vol <= 0 {
				vol = 1.0
			}
			tr.Clips = append(tr.Clips, domainstudio.TimelineClip{
				ID:              fmt.Sprintf("xaud-%d", i+1),
				AssetID:         ea.AssetID,
				Label:           ea.Label,
				FilePath:        resolved,
				TimelineStartMs: max(0, ea.TimelineStartMs),
				SourceInMs:      max(0, ea.SourceInMs),
				SourceOutMs:     outMs,
				Speed:           1.0,
				Volume:          vol,
			})
		}
		for _, kind := range []domainstudio.TrackKind{
			domainstudio.TrackAudioDialogue,
			domainstudio.TrackAudioNarration,
			domainstudio.TrackAudioSFX,
			domainstudio.TrackAudioBGM,
		} {
			if tr, ok := extraTracks[kind]; ok && len(tr.Clips) > 0 {
				tl.Tracks = append(tl.Tracks, *tr)
			}
		}

		// Optional BGM track spanning full video duration.
		if strings.TrimSpace(in.BGMPath) != "" {
			bgmResolved, berr := resolveInputFile(ctx, opts, in.BGMPath)
			if berr != nil {
				return contracts.TimelineAutoAssembleOutput{}, berr
			}
			totalVideoMs := max(1000, cursorMs)
			bgmSrcMs := probeMs(bgmResolved, int(totalVideoMs/1000))
			bgmOutMs := min(bgmSrcMs, totalVideoMs)
			if bgmOutMs <= 0 {
				bgmOutMs = totalVideoMs
			}
			tl.Tracks = append(tl.Tracks, domainstudio.Track{
				ID:     "a-bgm",
				Name:   "BGM",
				Kind:   domainstudio.TrackAudioBGM,
				Volume: bgmVol,
				Clips: []domainstudio.TimelineClip{{
					ID:              "bgm-1",
					Label:           filepath.Base(bgmResolved),
					FilePath:        bgmResolved,
					TimelineStartMs: 0,
					SourceInMs:      0,
					SourceOutMs:     bgmOutMs,
					Speed:           1.0,
					Volume:          1.0,
					TransitionIn:    domainstudio.TransitionFade,
					TransitionMs:    min(800, bgmOutMs/4),
				}},
			})
		}

		if err := tl.Validate(); err != nil {
			return contracts.TimelineAutoAssembleOutput{}, err
		}

		pretty, err := json.MarshalIndent(tl, "", "  ")
		if err != nil {
			return contracts.TimelineAutoAssembleOutput{}, apperr.Internal(err)
		}
		pretty = append(pretty, '\n')

		timelinePath := ""
		if strings.TrimSpace(in.ProjectDir) != "" {
			timelinePath, err = writeProjectArtifact(ctx, opts, in.ProjectDir, "timeline", "timeline.json", pretty)
			if err != nil {
				return contracts.TimelineAutoAssembleOutput{}, err
			}
		}

		clipCount := 0
		for _, tr := range tl.Tracks {
			clipCount += len(tr.Clips)
		}
		progress.Report(ctx, progress.Update{Percent: 100, Message: "Timeline assembled", Key: "studio.progress.done"})

		return contracts.TimelineAutoAssembleOutput{
			TimelineJSON:  string(pretty),
			TimelinePath:  timelinePath,
			DurationMs:    tl.DurationMs(),
			TrackCount:    len(tl.Tracks),
			ClipCount:     clipCount,
			SubtitleCount: len(tl.Subtitles),
		}, nil
	})
}

func buildSubtitleCuesForShot(sh domainstudio.Shot, startMs, spanMs int64, shotIdx int) []domainstudio.SubtitleCue {
	if spanMs < 200 {
		spanMs = 1000
	}
	if len(sh.Dialogues) > 0 {
		type part struct {
			speaker string
			text    string
			weight  int
		}
		var parts []part
		totalWeight := 0
		for _, d := range sh.Dialogues {
			line := strings.TrimSpace(d.Line)
			if line == "" {
				continue
			}
			w := max(1, utf8.RuneCountInString(line))
			parts = append(parts, part{speaker: strings.TrimSpace(d.Speaker), text: line, weight: w})
			totalWeight += w
		}
		if len(parts) > 0 {
			cues := make([]domainstudio.SubtitleCue, 0, len(parts))
			cur := startMs
			endLimit := startMs + spanMs
			for i, pt := range parts {
				end := endLimit
				if i < len(parts)-1 {
					sliceMs := int64(math.Round(float64(spanMs) * float64(pt.weight) / float64(totalWeight)))
					sliceMs = max(300, sliceMs)
					end = min(endLimit-int64(len(parts)-1-i)*200, cur+sliceMs)
				}
				if end <= cur {
					end = cur + 200
				}
				cues = append(cues, domainstudio.SubtitleCue{
					ID:      fmt.Sprintf("sub-%d-%d", shotIdx, i+1),
					StartMs: cur,
					EndMs:   end,
					Speaker: pt.speaker,
					Text:    pt.text,
				})
				cur = end
			}
			return cues
		}
	}
	if txt := strings.TrimSpace(sh.NarrationText); txt != "" {
		return []domainstudio.SubtitleCue{{
			ID:      fmt.Sprintf("sub-%d", shotIdx),
			StartMs: startMs,
			EndMs:   startMs + spanMs,
			Text:    txt,
		}}
	}
	return nil
}

func parseShotsForAssemble(raw string) ([]domainstudio.Shot, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, apperr.Validation("shots_json is required")
	}
	var pkg domainstudio.ShotPromptPackage
	if err := json.Unmarshal([]byte(trimmed), &pkg); err == nil && pkg.Shots != nil {
		return pkg.Shots, nil
	}
	var arr []domainstudio.Shot
	if err := json.Unmarshal([]byte(trimmed), &arr); err == nil {
		return arr, nil
	}
	return nil, apperr.Validation("shots_json must be a JSON array of shots or ShotPromptPackage object")
}

func parseAudioManifestForAssemble(raw string) ([]externalAudioManifestEntry, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, nil
	}
	var arr []externalAudioManifestEntry
	if err := json.Unmarshal([]byte(trimmed), &arr); err == nil {
		return arr, nil
	}
	var wrap struct {
		Tracks []externalAudioManifestEntry `json:"tracks"`
		Clips  []externalAudioManifestEntry `json:"clips"`
	}
	if err := json.Unmarshal([]byte(trimmed), &wrap); err == nil {
		if len(wrap.Tracks) > 0 {
			return wrap.Tracks, nil
		}
		if len(wrap.Clips) > 0 {
			return wrap.Clips, nil
		}
	}
	return nil, apperr.Validation("audio_manifest_json must be a JSON array or object with tracks/clips")
}
