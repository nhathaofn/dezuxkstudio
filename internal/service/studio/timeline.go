package studio

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"app/internal/apperr"
	"app/internal/contracts"
	domainmedia "app/internal/domain/mediagen"
	domainstudio "app/internal/domain/studio"
	"app/internal/engine"
	"app/internal/fsutil"
)

// GetTimeline returns the episode's active EditTimeline, revision metadata, and recent history.
func (s *Service) GetTimeline(ctx context.Context, episodeID string) (contracts.StudioTimelineResult, error) {
	if s.repo == nil {
		return contracts.StudioTimelineResult{}, apperr.Unavailable("studio repository is unavailable")
	}
	ep, err := s.repo.GetEpisode(ctx, strings.TrimSpace(episodeID))
	if err != nil {
		return contracts.StudioTimelineResult{}, err
	}
	proj, err := s.repo.GetProject(ctx, ep.ProjectID)
	if err != nil {
		return contracts.StudioTimelineResult{}, err
	}
	rec, err := s.loadEpisodeTimelineRecord(ctx, proj, ep)
	if err != nil {
		return contracts.StudioTimelineResult{}, err
	}
	revs, err := s.repo.ListTimelineRevisions(ctx, ep.ID, 30)
	if err != nil {
		return contracts.StudioTimelineResult{}, err
	}
	return timelineResultToDTO(proj.ID, s.resolveTimelineJSONPath(proj, ep), rec, revs), nil
}

// ApplyTimelineOps applies atomic timeline edits under optimistic expected_revision concurrency control.
func (s *Service) ApplyTimelineOps(ctx context.Context, p contracts.StudioTimelineApplyOpsPayload) (contracts.StudioTimelineResult, error) {
	if s.repo == nil {
		return contracts.StudioTimelineResult{}, apperr.Unavailable("studio repository is unavailable")
	}
	if err := p.Validate(); err != nil {
		return contracts.StudioTimelineResult{}, err
	}

	ep, err := s.repo.GetEpisode(ctx, strings.TrimSpace(p.EpisodeID))
	if err != nil {
		return contracts.StudioTimelineResult{}, err
	}
	proj, err := s.repo.GetProject(ctx, ep.ProjectID)
	if err != nil {
		return contracts.StudioTimelineResult{}, err
	}

	epRoot := s.resolveEpisodeRoot(proj, ep.Code)
	domainOps := timelineOpsFromDTO(p.Ops)
	if err := s.validateAndHydrateTimelineOps(ctx, epRoot, domainOps); err != nil {
		return contracts.StudioTimelineResult{}, err
	}

	curRec, err := s.loadEpisodeTimelineRecord(ctx, proj, ep)
	if err != nil {
		return contracts.StudioTimelineResult{}, err
	}
	if p.ExpectedRevision != curRec.Revision {
		return contracts.StudioTimelineResult{}, apperr.New(apperr.CodeConflict, fmt.Sprintf("timeline revision conflict: expected %d, actual %d", p.ExpectedRevision, curRec.Revision))
	}

	nextTL, err := domainstudio.ApplyTimelineOps(curRec.Timeline, domainOps)
	if err != nil {
		return contracts.StudioTimelineResult{}, err
	}
	s.hydrateTimelineClipAssets(ctx, epRoot, &nextTL)

	actor := domainstudio.TimelineActor(strings.TrimSpace(p.Actor))
	if !actor.Valid() {
		actor = domainstudio.ActorUser
	}
	summary := strings.TrimSpace(p.Summary)
	if summary == "" {
		summary = summarizeTimelineOps(domainOps)
	}

	saved, err := s.repo.SaveTimelineRevision(ctx, ep.ID, p.ExpectedRevision, actor, summary, nextTL)
	if err != nil {
		return contracts.StudioTimelineResult{}, err
	}

	ep, timelinePath := s.persistTimelineJSON(ctx, proj, ep, saved.Timeline)
	_ = ep
	revs, _ := s.repo.ListTimelineRevisions(ctx, ep.ID, 30)
	s.emitTimelineChanged(proj.ID, saved, string(actor), summary)
	return timelineResultToDTO(proj.ID, timelinePath, saved, revs), nil
}

// UndoTimeline steps backward one revision while enforcing expected_revision.
func (s *Service) UndoTimeline(ctx context.Context, p contracts.StudioTimelineRevisionNavPayload) (contracts.StudioTimelineResult, error) {
	if s.repo == nil {
		return contracts.StudioTimelineResult{}, apperr.Unavailable("studio repository is unavailable")
	}
	if err := p.Validate(); err != nil {
		return contracts.StudioTimelineResult{}, err
	}

	ep, err := s.repo.GetEpisode(ctx, strings.TrimSpace(p.EpisodeID))
	if err != nil {
		return contracts.StudioTimelineResult{}, err
	}
	proj, err := s.repo.GetProject(ctx, ep.ProjectID)
	if err != nil {
		return contracts.StudioTimelineResult{}, err
	}

	rec, err := s.repo.UndoTimeline(ctx, ep.ID, p.ExpectedRevision)
	if err != nil {
		return contracts.StudioTimelineResult{}, err
	}
	s.hydrateTimelineClipAssets(ctx, s.resolveEpisodeRoot(proj, ep.Code), &rec.Timeline)
	ep, timelinePath := s.persistTimelineJSON(ctx, proj, ep, rec.Timeline)
	_ = ep
	revs, _ := s.repo.ListTimelineRevisions(ctx, ep.ID, 30)
	s.emitTimelineChanged(proj.ID, rec, string(domainstudio.ActorUser), "undo")
	return timelineResultToDTO(proj.ID, timelinePath, rec, revs), nil
}

// RedoTimeline steps forward one undone revision while enforcing expected_revision.
func (s *Service) RedoTimeline(ctx context.Context, p contracts.StudioTimelineRevisionNavPayload) (contracts.StudioTimelineResult, error) {
	if s.repo == nil {
		return contracts.StudioTimelineResult{}, apperr.Unavailable("studio repository is unavailable")
	}
	if err := p.Validate(); err != nil {
		return contracts.StudioTimelineResult{}, err
	}

	ep, err := s.repo.GetEpisode(ctx, strings.TrimSpace(p.EpisodeID))
	if err != nil {
		return contracts.StudioTimelineResult{}, err
	}
	proj, err := s.repo.GetProject(ctx, ep.ProjectID)
	if err != nil {
		return contracts.StudioTimelineResult{}, err
	}

	rec, err := s.repo.RedoTimeline(ctx, ep.ID, p.ExpectedRevision)
	if err != nil {
		return contracts.StudioTimelineResult{}, err
	}
	s.hydrateTimelineClipAssets(ctx, s.resolveEpisodeRoot(proj, ep.Code), &rec.Timeline)
	ep, timelinePath := s.persistTimelineJSON(ctx, proj, ep, rec.Timeline)
	_ = ep
	revs, _ := s.repo.ListTimelineRevisions(ctx, ep.ID, 30)
	s.emitTimelineChanged(proj.ID, rec, string(domainstudio.ActorUser), "redo")
	return timelineResultToDTO(proj.ID, timelinePath, rec, revs), nil
}

// AutoAssembleTimeline builds an initial multi-track EditTimeline from the episode's shots & audio.
func (s *Service) AutoAssembleTimeline(ctx context.Context, p contracts.StudioTimelineAutoAssemblePayload) (contracts.StudioTimelineResult, error) {
	if s.repo == nil || s.wf == nil {
		return contracts.StudioTimelineResult{}, apperr.Unavailable("studio dependencies are unavailable")
	}
	if err := p.Validate(); err != nil {
		return contracts.StudioTimelineResult{}, err
	}

	ep, err := s.repo.GetEpisode(ctx, strings.TrimSpace(p.EpisodeID))
	if err != nil {
		return contracts.StudioTimelineResult{}, err
	}
	proj, err := s.repo.GetProject(ctx, ep.ProjectID)
	if err != nil {
		return contracts.StudioTimelineResult{}, err
	}
	shots, err := s.repo.ListShots(ctx, ep.ID)
	if err != nil {
		return contracts.StudioTimelineResult{}, err
	}
	if len(shots) == 0 {
		return contracts.StudioTimelineResult{}, apperr.Validation("episode has no storyboard shots to assemble")
	}

	bgmPath := strings.TrimSpace(p.BGMPath)
	if bgmPath != "" {
		if err := engine.CheckFile(ctx, bgmPath, false); err != nil {
			return contracts.StudioTimelineResult{}, err
		}
	}

	curRec, err := s.loadEpisodeTimelineRecord(ctx, proj, ep)
	if err != nil {
		return contracts.StudioTimelineResult{}, err
	}
	expectedRev := curRec.Revision
	if p.ExpectedRevision != nil {
		if *p.ExpectedRevision < 0 {
			return contracts.StudioTimelineResult{}, apperr.Validation("expected_revision must be >= 0")
		}
		if *p.ExpectedRevision != curRec.Revision {
			return contracts.StudioTimelineResult{}, apperr.New(apperr.CodeConflict, fmt.Sprintf("timeline revision conflict: expected %d, actual %d", *p.ExpectedRevision, curRec.Revision))
		}
		expectedRev = *p.ExpectedRevision
	}

	epRoot := s.resolveEpisodeRoot(proj, ep.Code)
	resolution := strings.TrimSpace(p.Resolution)
	if resolution == "" {
		resolution = resolutionForAspectRatio(proj.AspectRatio, s.cfg.DefaultResolution)
	}
	fps := p.FPS
	if fps <= 0 {
		fps = s.cfg.DefaultFPS
	}

	shotsBytes, err := json.Marshal(domainstudio.ShotPromptPackage{
		EpisodeTitle: ep.Title,
		Shots:        shots,
	})
	if err != nil {
		return contracts.StudioTimelineResult{}, apperr.Internal(err)
	}
	inBytes, err := json.Marshal(contracts.TimelineAutoAssembleInput{
		ShotsJSON:         string(shotsBytes),
		EpisodeID:         ep.ID,
		Resolution:        resolution,
		FPS:               fps,
		TransitionDefault: string(p.TransitionDefault),
		TransitionMs:      p.TransitionMs,
		BGMPath:           bgmPath,
		BGMVolume:         p.BGMVolume,
		ProjectDir:        epRoot,
	})
	if err != nil {
		return contracts.StudioTimelineResult{}, apperr.Internal(err)
	}

	ctx = domainmedia.ContextWithUsageMeta(ctx, domainmedia.UsageMeta{
		ProjectID: proj.ID,
		EpisodeID: ep.ID,
		Operation: "timeline_auto_assemble",
	})
	rawOut, err := s.wf.RunNode(ctx, contracts.NodeTypeTimelineAutoAssem, inBytes)
	if err != nil {
		return contracts.StudioTimelineResult{}, err
	}
	var nodeOut contracts.TimelineAutoAssembleOutput
	if err := decodeNodeOutput(rawOut, &nodeOut); err != nil {
		return contracts.StudioTimelineResult{}, err
	}

	var assembled domainstudio.EditTimeline
	if err := json.Unmarshal([]byte(nodeOut.TimelineJSON), &assembled); err != nil {
		return contracts.StudioTimelineResult{}, apperr.Internal(fmt.Errorf("decode assembled timeline: %w", err))
	}
	assembled.Normalize()
	s.hydrateTimelineClipAssets(ctx, epRoot, &assembled)

	summary := fmt.Sprintf("auto assemble (%d clips, %d subtitles)", nodeOut.ClipCount, nodeOut.SubtitleCount)
	saved, err := s.repo.SaveTimelineRevision(ctx, ep.ID, expectedRev, domainstudio.ActorNode, summary, assembled)
	if err != nil {
		return contracts.StudioTimelineResult{}, err
	}

	ep, timelinePath := s.persistTimelineJSON(ctx, proj, ep, saved.Timeline)
	_ = ep
	revs, _ := s.repo.ListTimelineRevisions(ctx, ep.ID, 30)
	s.emitTimelineChanged(proj.ID, saved, string(domainstudio.ActorNode), summary)
	return timelineResultToDTO(proj.ID, timelinePath, saved, revs), nil
}

// ComputeMediaPeaksThumbs extracts audio RMS peaks and a thumbnail sprite strip for a timeline clip or media file.
func (s *Service) ComputeMediaPeaksThumbs(ctx context.Context, p contracts.StudioMediaPeaksThumbsPayload) (contracts.MediaPeaksThumbsOutput, error) {
	if s.repo == nil || s.wf == nil {
		return contracts.MediaPeaksThumbsOutput{}, apperr.Unavailable("studio dependencies are unavailable")
	}
	if err := p.Validate(); err != nil {
		return contracts.MediaPeaksThumbsOutput{}, err
	}

	ep, err := s.repo.GetEpisode(ctx, strings.TrimSpace(p.EpisodeID))
	if err != nil {
		return contracts.MediaPeaksThumbsOutput{}, err
	}
	proj, err := s.repo.GetProject(ctx, ep.ProjectID)
	if err != nil {
		return contracts.MediaPeaksThumbsOutput{}, err
	}
	mediaPath := filepath.Clean(strings.TrimSpace(p.MediaPath))
	if err := engine.CheckFile(ctx, mediaPath, false); err != nil {
		return contracts.MediaPeaksThumbsOutput{}, err
	}

	epRoot := s.resolveEpisodeRoot(proj, ep.Code)
	outDir := ""
	if epRoot != "" {
		outDir = filepath.Join(epRoot, "media", "thumbs")
	}
	inBytes, err := json.Marshal(contracts.MediaPeaksThumbsInput{
		Media:            engine.ImageInputRef{MediaRef: engine.RefFor(mediaPath, engine.MediaVideo)},
		ThumbIntervalSec: p.ThumbIntervalSec,
		PeakBins:         p.PeakBins,
		OutputDir:        outDir,
	})
	if err != nil {
		return contracts.MediaPeaksThumbsOutput{}, apperr.Internal(err)
	}

	rawOut, err := s.wf.RunNode(ctx, contracts.NodeTypeMediaPeaksThumbs, inBytes)
	if err != nil {
		return contracts.MediaPeaksThumbsOutput{}, err
	}
	var out contracts.MediaPeaksThumbsOutput
	if err := decodeNodeOutput(rawOut, &out); err != nil {
		return contracts.MediaPeaksThumbsOutput{}, err
	}
	if out.SpriteAssetID == "" && strings.TrimSpace(out.SpriteSheetPath) != "" {
		out.SpriteAssetID = s.registerMedia(ctx, "image", contracts.NodeTypeMediaPeaksThumbs, out.SpriteSheetPath, outDir, 0)
	}

	if clipID := strings.TrimSpace(p.ClipID); clipID != "" {
		rec, rerr := s.loadEpisodeTimelineRecord(ctx, proj, ep)
		if rerr == nil && updateClipPeaksThumbs(&rec.Timeline, clipID, out) {
			if saved, serr := s.repo.SaveTimelineRevision(ctx, ep.ID, rec.Revision, domainstudio.ActorNode, "clip peaks & thumbs", rec.Timeline); serr == nil {
				_, _ = s.persistTimelineJSON(ctx, proj, ep, saved.Timeline)
				s.emitTimelineChanged(proj.ID, saved, string(domainstudio.ActorNode), "clip peaks & thumbs")
			}
		}
	}
	return out, nil
}

// RenderTimeline executes tool.render_timeline on the episode's active EditTimeline and updates episode export state.
func (s *Service) RenderTimeline(ctx context.Context, p contracts.StudioTimelineRenderPayload) (contracts.StudioTimelineRenderResult, error) {
	if s.repo == nil || s.wf == nil {
		return contracts.StudioTimelineRenderResult{}, apperr.Unavailable("studio dependencies are unavailable")
	}
	if err := p.Validate(); err != nil {
		return contracts.StudioTimelineRenderResult{}, err
	}

	ep, err := s.repo.GetEpisode(ctx, strings.TrimSpace(p.EpisodeID))
	if err != nil {
		return contracts.StudioTimelineRenderResult{}, err
	}
	proj, err := s.repo.GetProject(ctx, ep.ProjectID)
	if err != nil {
		return contracts.StudioTimelineRenderResult{}, err
	}

	rec, err := s.loadEpisodeTimelineRecord(ctx, proj, ep)
	if err != nil {
		return contracts.StudioTimelineRenderResult{}, err
	}
	if countTimelineClips(rec.Timeline) == 0 {
		return contracts.StudioTimelineRenderResult{}, apperr.Validation("timeline has no clips to render")
	}

	tlBytes, err := json.Marshal(rec.Timeline)
	if err != nil {
		return contracts.StudioTimelineRenderResult{}, apperr.Internal(err)
	}

	epRoot := s.resolveEpisodeRoot(proj, ep.Code)
	outDir := ""
	if epRoot != "" {
		outDir = filepath.Join(epRoot, "exports")
	}
	inBytes, err := json.Marshal(contracts.RenderTimelineInput{
		TimelineJSON:     string(tlBytes),
		OutputDir:        outDir,
		Filename:         strings.TrimSpace(p.Filename),
		BurnSubtitles:    p.BurnSubtitles,
		SubtitleFontSize: p.SubtitleFontSize,
		LoudnormLUFS:     p.LoudnormLUFS,
		CRF:              p.CRF,
		Preset:           strings.TrimSpace(p.Preset),
	})
	if err != nil {
		return contracts.StudioTimelineRenderResult{}, apperr.Internal(err)
	}

	ctx = domainmedia.ContextWithUsageMeta(ctx, domainmedia.UsageMeta{
		ProjectID: proj.ID,
		EpisodeID: ep.ID,
		Operation: "render_timeline",
	})
	rawOut, err := s.wf.RunNode(ctx, contracts.NodeTypeRenderTimeline, inBytes)
	if err != nil {
		return contracts.StudioTimelineRenderResult{}, err
	}
	var nodeOut contracts.RenderTimelineOutput
	if err := decodeNodeOutput(rawOut, &nodeOut); err != nil {
		return contracts.StudioTimelineRenderResult{}, err
	}
	if nodeOut.AssetID == "" && strings.TrimSpace(nodeOut.OutputPath) != "" {
		nodeOut.AssetID = s.registerMedia(ctx, "video", contracts.NodeTypeRenderTimeline, nodeOut.OutputPath, outDir, nodeOut.DurationSec)
	}

	ep.Stage = domainstudio.StageExport
	if updatedEp, uerr := s.repo.UpdateEpisode(ctx, ep); uerr == nil {
		ep = updatedEp
	}
	shots, _ := s.repo.ListShots(ctx, ep.ID)
	rawText := readStringFile(ep.RawScriptPath)
	var normPtr *domainstudio.NormalizedScript
	if b, rerr := os.ReadFile(ep.ScriptJSONPath); rerr == nil {
		var ns domainstudio.NormalizedScript
		if json.Unmarshal(b, &ns) == nil {
			normPtr = &ns
		}
	}
	_ = s.persistEpisodeToDisk(proj, ep, rawText, normPtr, shots, nodeOut.OutputPath, nodeOut.AssetID)

	detail, err := s.GetEpisode(ctx, ep.ID)
	if err != nil {
		return contracts.StudioTimelineRenderResult{}, err
	}
	s.emit(contracts.EventStudioEpisodeUpdated, contracts.StudioEpisodeUpdatedEvent{
		ProjectID: proj.ID,
		EpisodeID: ep.ID,
		Stage:     detail.Episode.Stage,
		Reason:    "timeline_rendered",
	})

	return contracts.StudioTimelineRenderResult{
		OutputPath:  nodeOut.OutputPath,
		AssetID:     nodeOut.AssetID,
		SRTPath:     nodeOut.SRTPath,
		ASSPath:     nodeOut.ASSPath,
		DurationSec: nodeOut.DurationSec,
		Episode:     detail,
	}, nil
}

// ExportNLEDraft packages the episode's active EditTimeline into CapCut/Jianying .zip, FCPXML, or OTIO.
func (s *Service) ExportNLEDraft(ctx context.Context, p contracts.StudioTimelineExportNLEPayload) (contracts.ExportNLEDraftOutput, error) {
	if s.repo == nil || s.wf == nil {
		return contracts.ExportNLEDraftOutput{}, apperr.Unavailable("studio dependencies are unavailable")
	}
	if err := p.Validate(); err != nil {
		return contracts.ExportNLEDraftOutput{}, err
	}

	ep, err := s.repo.GetEpisode(ctx, strings.TrimSpace(p.EpisodeID))
	if err != nil {
		return contracts.ExportNLEDraftOutput{}, err
	}
	proj, err := s.repo.GetProject(ctx, ep.ProjectID)
	if err != nil {
		return contracts.ExportNLEDraftOutput{}, err
	}
	rec, err := s.loadEpisodeTimelineRecord(ctx, proj, ep)
	if err != nil {
		return contracts.ExportNLEDraftOutput{}, err
	}
	if countTimelineClips(rec.Timeline) == 0 {
		return contracts.ExportNLEDraftOutput{}, apperr.Validation("timeline has no clips to export")
	}

	tlBytes, err := json.Marshal(rec.Timeline)
	if err != nil {
		return contracts.ExportNLEDraftOutput{}, apperr.Internal(err)
	}
	epRoot := s.resolveEpisodeRoot(proj, ep.Code)
	outDir := ""
	if epRoot != "" {
		outDir = filepath.Join(epRoot, "exports")
	}
	projName := strings.TrimSpace(p.ProjectName)
	if projName == "" {
		projName = strings.TrimSpace(proj.Title)
		if ep.Code != "" {
			projName = strings.TrimSpace(projName + "_" + ep.Code)
		}
	}

	inBytes, err := json.Marshal(contracts.ExportNLEDraftInput{
		TimelineJSON:     string(tlBytes),
		Format:           string(p.Format),
		OutputDir:        outDir,
		ProjectName:      projName,
		IncludeMediaCopy: p.IncludeMediaCopy,
	})
	if err != nil {
		return contracts.ExportNLEDraftOutput{}, apperr.Internal(err)
	}

	rawOut, err := s.wf.RunNode(ctx, contracts.NodeTypeExportNLEDraft, inBytes)
	if err != nil {
		return contracts.ExportNLEDraftOutput{}, err
	}
	var out contracts.ExportNLEDraftOutput
	if err := decodeNodeOutput(rawOut, &out); err != nil {
		return contracts.ExportNLEDraftOutput{}, err
	}
	if out.AssetID == "" && strings.TrimSpace(out.ExportPath) != "" {
		kind := "export"
		if strings.EqualFold(filepath.Ext(out.ExportPath), ".zip") {
			kind = "archive"
		}
		out.AssetID = s.registerMedia(ctx, kind, contracts.NodeTypeExportNLEDraft, out.ExportPath, outDir, 0)
	}
	return out, nil
}

// OpenExportFolder opens the episode exports directory (or the validated export file's parent folder) in the host OS file explorer.
func (s *Service) OpenExportFolder(ctx context.Context, p contracts.StudioOpenExportFolderPayload) error {
	if err := p.Validate(); err != nil {
		return err
	}
	if s.openDirectory == nil {
		return apperr.Unavailable("export folder opener is unavailable in this environment")
	}

	var targetDir string
	epID := strings.TrimSpace(p.EpisodeID)
	filePath := strings.TrimSpace(p.FilePath)

	if epID != "" {
		if s.repo == nil {
			return apperr.Unavailable("studio repository is unavailable")
		}
		ep, err := s.repo.GetEpisode(ctx, epID)
		if err != nil {
			return err
		}
		proj, err := s.repo.GetProject(ctx, ep.ProjectID)
		if err != nil {
			return err
		}
		epRoot := s.resolveEpisodeRoot(proj, ep.Code)
		if epRoot == "" {
			return apperr.Validation("episode workspace root is not configured")
		}
		exportsDir := filepath.Join(epRoot, "exports")
		if filePath == "" {
			if err := os.MkdirAll(exportsDir, 0o755); err != nil {
				return apperr.Internal(err)
			}
			targetDir = exportsDir
		} else {
			cleanFile := filepath.Clean(filePath)
			if !isWithinBaseDir(epRoot, cleanFile) {
				return apperr.New(apperr.CodeUnauthorized, "file_path must reside inside episode workspace")
			}
			targetDir = resolveExistingDirOrParent(cleanFile)
		}
	} else {
		base := s.ProjectsDir()
		if base == "" {
			return apperr.New(apperr.CodeUnauthorized, "projects_dir is required to validate file_path")
		}
		cleanFile := filepath.Clean(filePath)
		if !isWithinBaseDir(base, cleanFile) {
			return apperr.New(apperr.CodeUnauthorized, "file_path must reside inside studio projects workspace")
		}
		targetDir = resolveExistingDirOrParent(cleanFile)
	}

	if err := engine.CheckFile(ctx, targetDir, false); err != nil {
		return err
	}
	if info, err := os.Stat(targetDir); err != nil || !info.IsDir() {
		return apperr.NotFound(fmt.Sprintf("export folder %q does not exist", targetDir))
	}
	if err := s.openDirectory(targetDir); err != nil {
		return apperr.Internal(fmt.Errorf("open export folder: %w", err))
	}
	return nil
}

func (s *Service) loadEpisodeTimelineRecord(ctx context.Context, proj domainstudio.Project, ep domainstudio.Episode) (domainstudio.TimelineRecord, error) {
	rec, err := s.repo.GetTimeline(ctx, ep.ID)
	if err != nil {
		return domainstudio.TimelineRecord{}, err
	}
	if rec.Revision == 0 {
		w, h := parseResolutionDims(resolutionForAspectRatio(proj.AspectRatio, s.cfg.DefaultResolution))
		fps := s.cfg.DefaultFPS
		if fps <= 0 {
			fps = 24
		}
		if len(rec.Timeline.Tracks) == 0 {
			rec.Timeline = domainstudio.DefaultEditTimeline(w, h, fps)
		} else {
			rec.Timeline.Width = w
			rec.Timeline.Height = h
			rec.Timeline.FPS = fps
			rec.Timeline.Normalize()
		}
	}
	return rec, nil
}

func (s *Service) validateAndHydrateTimelineOps(ctx context.Context, epRoot string, ops []domainstudio.TimelineOp) error {
	checkClip := func(cl *domainstudio.TimelineClip) error {
		if cl == nil {
			return nil
		}
		if fp := strings.TrimSpace(cl.FilePath); fp != "" {
			clean := filepath.Clean(fp)
			if err := engine.CheckFile(ctx, clean, false); err != nil {
				return err
			}
			cl.FilePath = clean
		}
		if sp := strings.TrimSpace(cl.SpritePath); sp != "" {
			clean := filepath.Clean(sp)
			if err := engine.CheckFile(ctx, clean, false); err != nil {
				return err
			}
			cl.SpritePath = clean
		}
		return nil
	}

	for i := range ops {
		if ops[i].Timeline != nil {
			for ti := range ops[i].Timeline.Tracks {
				for ci := range ops[i].Timeline.Tracks[ti].Clips {
					if err := checkClip(&ops[i].Timeline.Tracks[ti].Clips[ci]); err != nil {
						return err
					}
				}
			}
		}
		if err := checkClip(ops[i].Clip); err != nil {
			return err
		}
	}
	_ = epRoot
	return nil
}

func (s *Service) hydrateTimelineClipAssets(ctx context.Context, epRoot string, tl *domainstudio.EditTimeline) {
	if s.media == nil || tl == nil {
		return
	}
	for ti := range tl.Tracks {
		kind := "video"
		if tl.Tracks[ti].Kind != domainstudio.TrackVideo {
			kind = "audio"
		}
		for ci := range tl.Tracks[ti].Clips {
			cl := &tl.Tracks[ti].Clips[ci]
			if cl.AssetID == "" && strings.TrimSpace(cl.FilePath) != "" {
				if engine.CheckFile(ctx, cl.FilePath, false) == nil {
					if _, err := os.Stat(cl.FilePath); err == nil {
						root := filepath.Dir(cl.FilePath)
						if epRoot != "" && isWithinBaseDir(epRoot, cl.FilePath) {
							root = epRoot
						}
						if regID := s.registerMedia(ctx, kind, "timeline_clip", cl.FilePath, root, 0); regID != "" {
							cl.AssetID = regID
						}
					}
				}
			}
			if cl.SpriteAssetID == "" && strings.TrimSpace(cl.SpritePath) != "" {
				if engine.CheckFile(ctx, cl.SpritePath, false) == nil {
					if _, err := os.Stat(cl.SpritePath); err == nil {
						root := filepath.Dir(cl.SpritePath)
						if epRoot != "" && isWithinBaseDir(epRoot, cl.SpritePath) {
							root = epRoot
						}
						if regID := s.registerMedia(ctx, "image", contracts.NodeTypeMediaPeaksThumbs, cl.SpritePath, root, 0); regID != "" {
							cl.SpriteAssetID = regID
						}
					}
				}
			}
		}
	}
}

func (s *Service) resolveTimelineJSONPath(proj domainstudio.Project, ep domainstudio.Episode) string {
	if strings.TrimSpace(ep.TimelineJSONPath) != "" {
		return ep.TimelineJSONPath
	}
	epRoot := s.resolveEpisodeRoot(proj, ep.Code)
	if epRoot == "" {
		return ""
	}
	return filepath.Join(epRoot, "timeline", "timeline.json")
}

func (s *Service) persistTimelineJSON(ctx context.Context, proj domainstudio.Project, ep domainstudio.Episode, tl domainstudio.EditTimeline) (domainstudio.Episode, string) {
	epRoot := s.resolveEpisodeRoot(proj, ep.Code)
	timelinePath := s.resolveTimelineJSONPath(proj, ep)
	if epRoot == "" || timelinePath == "" {
		return ep, timelinePath
	}
	_ = ensureEpisodeDirs(epRoot)
	if b, err := json.MarshalIndent(tl, "", "  "); err == nil {
		_ = fsutil.WriteFileAtomic(timelinePath, append(b, '\n'), 0o644)
	}
	if ep.TimelineJSONPath != timelinePath {
		ep.TimelineJSONPath = timelinePath
		if updated, err := s.repo.UpdateEpisode(ctx, ep); err == nil {
			ep = updated
		}
	}
	return ep, timelinePath
}

func (s *Service) emitTimelineChanged(projID string, rec domainstudio.TimelineRecord, actor, summary string) {
	s.emit(contracts.EventStudioTimelineChanged, contracts.StudioTimelineChangedEvent{
		ProjectID:  projID,
		EpisodeID:  rec.EpisodeID,
		Revision:   rec.Revision,
		CanUndo:    rec.CanUndo,
		CanRedo:    rec.CanRedo,
		DurationMs: rec.Timeline.DurationMs(),
		Actor:      actor,
		Summary:    summary,
		Timeline:   editTimelineToDTO(rec.Timeline),
	})
}

func updateClipPeaksThumbs(tl *domainstudio.EditTimeline, clipID string, out contracts.MediaPeaksThumbsOutput) bool {
	if tl == nil {
		return false
	}
	for ti := range tl.Tracks {
		for ci := range tl.Tracks[ti].Clips {
			if tl.Tracks[ti].Clips[ci].ID == clipID {
				tl.Tracks[ti].Clips[ci].Peaks = append([]float64(nil), out.Peaks...)
				if strings.TrimSpace(out.SpriteSheetPath) != "" {
					tl.Tracks[ti].Clips[ci].SpritePath = out.SpriteSheetPath
				}
				if strings.TrimSpace(out.SpriteAssetID) != "" {
					tl.Tracks[ti].Clips[ci].SpriteAssetID = out.SpriteAssetID
				}
				return true
			}
		}
	}
	return false
}

func countTimelineClips(tl domainstudio.EditTimeline) int {
	total := 0
	for _, tr := range tl.Tracks {
		total += len(tr.Clips)
	}
	return total
}

func summarizeTimelineOps(ops []domainstudio.TimelineOp) string {
	if len(ops) == 0 {
		return "timeline edit"
	}
	if len(ops) == 1 {
		return ops[0].Op
	}
	return fmt.Sprintf("%s (+%d ops)", ops[0].Op, len(ops)-1)
}

func parseResolutionDims(res string) (int, int) {
	parts := strings.Split(strings.ToLower(strings.TrimSpace(res)), "x")
	if len(parts) != 2 {
		return 1920, 1080
	}
	w, errW := strconv.Atoi(strings.TrimSpace(parts[0]))
	h, errH := strconv.Atoi(strings.TrimSpace(parts[1]))
	if errW != nil || errH != nil || w <= 0 || h <= 0 {
		return 1920, 1080
	}
	return w, h
}

func isWithinBaseDir(baseDir, target string) bool {
	rel, err := filepath.Rel(filepath.Clean(baseDir), filepath.Clean(target))
	if err != nil {
		return false
	}
	return rel == "." || (!strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != "..")
}

func resolveExistingDirOrParent(cleanPath string) string {
	if info, err := os.Stat(cleanPath); err == nil && info.IsDir() {
		return cleanPath
	}
	return filepath.Dir(cleanPath)
}
