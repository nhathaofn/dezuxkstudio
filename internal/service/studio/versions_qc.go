package studio

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"app/internal/apperr"
	"app/internal/contracts"
	domainstudio "app/internal/domain/studio"
	"app/internal/engine"
	"app/internal/fsutil"
	studionodes "app/internal/tools/studio"
)

func (s *Service) recordSelectedVersion(ctx context.Context, projectID, episodeID string, kind domainstudio.MediaTargetKind, targetID, mediaPath, sourceNode, prompt, modelRef string) *domainstudio.MediaVersion {
	if s.repo == nil || targetID == "" || strings.TrimSpace(mediaPath) == "" {
		return nil
	}
	v, err := s.repo.CreateMediaVersion(ctx, domainstudio.MediaVersion{
		ProjectID:  projectID,
		EpisodeID:  episodeID,
		TargetKind: kind,
		TargetID:   targetID,
		MediaPath:  mediaPath,
		SourceNode: sourceNode,
		PromptUsed: prompt,
		ModelRef:   modelRef,
		IsSelected: true,
	})
	if err != nil {
		s.log.WarnContext(ctx, "studio: failed to persist media version", "id", targetID, "error", err)
		return nil
	}
	return &v
}

// ListVersions returns all saved versions and latest QC report for a shot slot or asset.
func (s *Service) ListVersions(ctx context.Context, p contracts.StudioVersionsListPayload) (contracts.StudioVersionsListResult, error) {
	if s.repo == nil {
		return contracts.StudioVersionsListResult{}, apperr.Unavailable("studio repository unavailable").
			WithMsg("errors.studio.service_unavailable", nil)
	}
	kind := domainstudio.MediaTargetKind(strings.TrimSpace(string(p.TargetKind)))
	if !kind.Valid() || strings.TrimSpace(p.TargetID) == "" {
		return contracts.StudioVersionsListResult{}, apperr.Validation("valid target_kind and target_id are required").
			WithMsg("errors.studio.invalid_target_kind", nil)
	}
	vers, err := s.repo.ListMediaVersions(ctx, kind, p.TargetID)
	if err != nil {
		return contracts.StudioVersionsListResult{}, err
	}
	outVers := make([]contracts.StudioMediaVersion, len(vers))
	for i := range vers {
		outVers[i] = mediaVersionToDTO(vers[i])
	}
	var latestQC *contracts.StudioQCReport
	if rep, rerr := s.repo.GetLatestQCReport(ctx, kind, p.TargetID); rerr == nil {
		dto := qcReportToDTO(rep)
		latestQC = &dto
	}
	return contracts.StudioVersionsListResult{Versions: outVers, LatestQC: latestQC}, nil
}

// SelectVersion activates a specific saved media version and updates the owning shot/asset record.
func (s *Service) SelectVersion(ctx context.Context, p contracts.StudioVersionSelectPayload) (contracts.StudioVersionSelectResult, error) {
	if s.repo == nil {
		return contracts.StudioVersionSelectResult{}, apperr.Unavailable("studio repository unavailable").
			WithMsg("errors.studio.service_unavailable", nil)
	}
	if err := p.Validate(); err != nil {
		return contracts.StudioVersionSelectResult{}, err
	}
	v, err := s.repo.SelectMediaVersion(ctx, p.VersionID)
	if err != nil {
		return contracts.StudioVersionSelectResult{}, err
	}
	epDetail, err := s.applyMediaPathToTarget(ctx, v.ProjectID, v.EpisodeID, v.TargetKind, v.TargetID, v.MediaPath, "", "version_selected")
	if err != nil {
		return contracts.StudioVersionSelectResult{}, err
	}
	return contracts.StudioVersionSelectResult{
		Version: mediaVersionToDTO(v),
		Episode: epDetail,
	}, nil
}

// SplitGrid invokes tool.grid_split and records panel variants in studio_media_versions.
func (s *Service) SplitGrid(ctx context.Context, p contracts.StudioGridSplitPayload) (contracts.StudioGridSplitResult, error) {
	if s.repo == nil || s.wf == nil {
		return contracts.StudioGridSplitResult{}, apperr.Unavailable("studio dependencies unavailable").
			WithMsg("errors.studio.service_unavailable", nil)
	}
	if err := p.Validate(); err != nil {
		return contracts.StudioGridSplitResult{}, err
	}
	targetKind := domainstudio.MediaTargetKind(strings.TrimSpace(string(p.TargetKind)))
	if targetKind == "" {
		targetKind = domainstudio.TargetGridPanel
	} else if !targetKind.Valid() {
		return contracts.StudioGridSplitResult{}, apperr.Validation("invalid target_kind").
			WithMsg("errors.studio.invalid_target_kind", nil)
	}

	proj, err := s.repo.GetProject(ctx, strings.TrimSpace(p.ProjectID))
	if err != nil {
		return contracts.StudioGridSplitResult{}, err
	}
	outDir := ""
	if projRoot := s.resolveProjectRoot(proj); projRoot != "" {
		outDir = filepath.Join(projRoot, "assets", "grid_panels")
	}
	if p.EpisodeID != "" {
		if ep, eerr := s.repo.GetEpisode(ctx, p.EpisodeID); eerr == nil {
			if epRoot := s.resolveEpisodeRoot(proj, ep.Code); epRoot != "" {
				outDir = filepath.Join(epRoot, "media", "keyframes", "grid_panels")
			}
		}
	}
	inBytes, merr := json.Marshal(contracts.GridSplitInput{
		ImagePath:  engine.InputRef(p.ImagePath, engine.MediaImage),
		Rows:       p.Rows,
		Cols:       p.Cols,
		MarginPx:   p.MarginPx,
		GutterPx:   p.GutterPx,
		RowWeights: p.RowWeights,
		ColWeights: p.ColWeights,
		OutputDir:  outDir,
	})
	if merr != nil {
		return contracts.StudioGridSplitResult{}, apperr.Internal(merr)
	}
	rawOut, err := s.wf.RunNode(ctx, studionodes.NodeTypeGridSplit, inBytes)
	if err != nil {
		return contracts.StudioGridSplitResult{}, err
	}
	var out contracts.GridSplitOutput
	if err := decodeNodeOutput(rawOut, &out); err != nil {
		return contracts.StudioGridSplitResult{}, err
	}
	if out.Panels == nil {
		out.Panels = []contracts.GridPanel{}
	}

	targetID := strings.TrimSpace(p.TargetID)
	if targetID == "" {
		targetID = proj.ID
	}
	versions := make([]contracts.StudioMediaVersion, 0, len(out.Panels))
	for _, panel := range out.Panels {
		v, verr := s.repo.CreateMediaVersion(ctx, domainstudio.MediaVersion{
			ProjectID:  proj.ID,
			EpisodeID:  p.EpisodeID,
			TargetKind: targetKind,
			TargetID:   targetID,
			MediaPath:  panel.Path,
			SourceNode: studionodes.NodeTypeGridSplit,
			IsSelected: false,
		})
		if verr != nil {
			s.log.WarnContext(ctx, "studio: failed to persist grid split version", "target_id", targetID, "panel_index", panel.Index, "error", verr)
			continue
		}
		versions = append(versions, mediaVersionToDTO(v))
	}
	return contracts.StudioGridSplitResult{Panels: out.Panels, Versions: versions}, nil
}

// ApplyMaskEdit saves the painted mask PNG (when passed as base64), invokes tool.mask_edit,
// records a new selected version, and updates the shot or asset image.
func (s *Service) ApplyMaskEdit(ctx context.Context, p contracts.StudioMaskEditPayload) (contracts.StudioMaskEditResult, error) {
	if s.repo == nil || s.wf == nil {
		return contracts.StudioMaskEditResult{}, apperr.Unavailable("studio dependencies unavailable").
			WithMsg("errors.studio.service_unavailable", nil)
	}
	if err := p.Validate(); err != nil {
		return contracts.StudioMaskEditResult{}, err
	}
	kind := domainstudio.MediaTargetKind(strings.TrimSpace(string(p.TargetKind)))
	if kind != "" && !kind.Valid() {
		return contracts.StudioMaskEditResult{}, apperr.Validation("invalid target_kind").
			WithMsg("errors.studio.invalid_target_kind", nil)
	}

	proj, err := s.repo.GetProject(ctx, strings.TrimSpace(p.ProjectID))
	if err != nil {
		return contracts.StudioMaskEditResult{}, err
	}

	projRoot := s.resolveProjectRoot(proj)
	outDir := ""
	if projRoot != "" {
		outDir = filepath.Join(projRoot, "edits")
	}
	if p.EpisodeID != "" {
		if ep, eerr := s.repo.GetEpisode(ctx, p.EpisodeID); eerr == nil {
			if epRoot := s.resolveEpisodeRoot(proj, ep.Code); epRoot != "" {
				outDir = filepath.Join(epRoot, "media", "keyframes")
			}
		}
	}
	maskPath := strings.TrimSpace(p.MaskPath)
	if p.MaskDataBase64 != "" {
		maskBytes, derr := decodeBase64PNG(p.MaskDataBase64, s.cfg.MaxMaskBytes)
		if derr != nil {
			return contracts.StudioMaskEditResult{}, derr
		}
		if outDir == "" {
			return contracts.StudioMaskEditResult{}, apperr.Validation("project workspace directory is required for base64 mask staging").
				WithMsg("errors.studio.workspace_required", nil)
		}
		maskPath = filepath.Join(outDir, fmt.Sprintf("mask_%d.png", time.Now().UnixNano()))
		if err := engine.CheckFile(ctx, maskPath, true); err != nil {
			return contracts.StudioMaskEditResult{}, err
		}
		if err := os.MkdirAll(outDir, 0o750); err != nil {
			return contracts.StudioMaskEditResult{}, apperr.Wrap(apperr.CodeUnavailable, "Failed to prepare mask edit workspace directory", err).
				WithMsg("errors.studio.io_failed", nil)
		}
		if err := fsutil.WriteFileAtomic(maskPath, maskBytes, 0o600); err != nil {
			return contracts.StudioMaskEditResult{}, apperr.Wrap(apperr.CodeUnavailable, "Failed to write mask image file", err).
				WithMsg("errors.studio.io_failed", nil)
		}
	}
	if maskPath == "" {
		return contracts.StudioMaskEditResult{}, apperr.Validation("mask_path or mask_data_base64 is required")
	}

	modelRef := firstNonEmpty(p.ModelRef, proj.DefaultImageModel)
	aspectRatio := firstNonEmpty(p.AspectRatio, proj.AspectRatio)
	inBytes, merr := json.Marshal(contracts.MaskEditInput{
		ImagePath:   engine.InputRef(p.ImagePath, engine.MediaImage),
		MaskPath:    engine.InputRef(maskPath, engine.MediaImage),
		Prompt:      p.Prompt,
		ModelRef:    modelRef,
		AspectRatio: aspectRatio,
		Resolution:  strings.TrimSpace(p.Resolution),
		OutputDir:   outDir,
	})
	if merr != nil {
		return contracts.StudioMaskEditResult{}, apperr.Internal(merr)
	}
	rawOut, err := s.wf.RunNode(ctx, studionodes.NodeTypeMaskEdit, inBytes)
	if err != nil {
		return contracts.StudioMaskEditResult{}, err
	}
	var out contracts.MaskEditOutput
	if err := decodeNodeOutput(rawOut, &out); err != nil {
		return contracts.StudioMaskEditResult{}, err
	}

	var verDTO *contracts.StudioMediaVersion
	var epDetail *contracts.StudioEpisodeDetailResult
	if kind.Valid() && strings.TrimSpace(p.TargetID) != "" {
		if v := s.recordSelectedVersion(ctx, proj.ID, p.EpisodeID, kind, p.TargetID, out.Path, studionodes.NodeTypeMaskEdit, p.Prompt, modelRef); v != nil {
			dto := mediaVersionToDTO(*v)
			verDTO = &dto
		}
		var epErr error
		epDetail, epErr = s.applyMediaPathToTarget(ctx, proj.ID, p.EpisodeID, kind, p.TargetID, out.Path, out.AssetID, "mask_edit_applied")
		if epErr != nil {
			s.log.WarnContext(ctx, "studio: failed to bind mask edit result to target", "target_id", p.TargetID, "error", epErr)
		}
	}
	return contracts.StudioMaskEditResult{
		Path:    out.Path,
		AssetID: out.AssetID,
		Version: verDTO,
		Episode: epDetail,
	}, nil
}

// ExtractLastFrame runs tool.extract_frames in "last" mode and binds the extracted keyframe to TargetShotID when specified.
func (s *Service) ExtractLastFrame(ctx context.Context, p contracts.StudioExtractLastFramePayload) (contracts.StudioExtractLastFrameResult, error) {
	if s.repo == nil || s.wf == nil {
		return contracts.StudioExtractLastFrameResult{}, apperr.Unavailable("studio dependencies unavailable").
			WithMsg("errors.studio.service_unavailable", nil)
	}
	if err := p.Validate(); err != nil {
		return contracts.StudioExtractLastFrameResult{}, err
	}
	ep, err := s.repo.GetEpisode(ctx, strings.TrimSpace(p.EpisodeID))
	if err != nil {
		return contracts.StudioExtractLastFrameResult{}, err
	}
	proj, err := s.repo.GetProject(ctx, ep.ProjectID)
	if err != nil {
		return contracts.StudioExtractLastFrameResult{}, err
	}
	srcShot, err := s.repo.GetShot(ctx, strings.TrimSpace(p.SourceShotID))
	if err != nil {
		return contracts.StudioExtractLastFrameResult{}, err
	}
	if strings.TrimSpace(srcShot.VideoPath) == "" {
		return contracts.StudioExtractLastFrameResult{}, apperr.Validation("source shot has no generated video").
			WithMsg("errors.studio.shot_video_required", nil)
	}

	outDir := ""
	if epRoot := s.resolveEpisodeRoot(proj, ep.Code); epRoot != "" {
		outDir = filepath.Join(epRoot, "media", "keyframes")
	}
	inBytes, merr := json.Marshal(contracts.ExtractFramesInput{
		VideoPath: engine.InputRef(srcShot.VideoPath, engine.MediaVideo),
		Mode:      "last",
		OutputDir: outDir,
	})
	if merr != nil {
		return contracts.StudioExtractLastFrameResult{}, apperr.Internal(merr)
	}
	rawOut, err := s.wf.RunNode(ctx, studionodes.NodeTypeExtractFrames, inBytes)
	if err != nil {
		return contracts.StudioExtractLastFrameResult{}, err
	}
	var out contracts.ExtractFramesOutput
	if err := decodeNodeOutput(rawOut, &out); err != nil {
		return contracts.StudioExtractLastFrameResult{}, err
	}
	if out.LastFramePath == "" {
		return contracts.StudioExtractLastFrameResult{}, apperr.Unavailable("no frame was extracted from video").
			WithMsg("errors.studio.extract_frame_failed", nil)
	}
	var assetID string
	if len(out.Frames) > 0 {
		assetID = out.Frames[len(out.Frames)-1].AssetID
	}

	var verDTO *contracts.StudioMediaVersion
	var epDetail *contracts.StudioEpisodeDetailResult
	if targetID := strings.TrimSpace(p.TargetShotID); targetID != "" {
		if v := s.recordSelectedVersion(ctx, proj.ID, ep.ID, domainstudio.TargetKeyframe, targetID, out.LastFramePath, studionodes.NodeTypeExtractFrames, "extracted last frame continuity", ""); v != nil {
			dto := mediaVersionToDTO(*v)
			verDTO = &dto
		}
		var epErr error
		epDetail, epErr = s.applyMediaPathToTarget(ctx, proj.ID, ep.ID, domainstudio.TargetKeyframe, targetID, out.LastFramePath, assetID, "extracted_last_frame")
		if epErr != nil {
			s.log.WarnContext(ctx, "studio: failed to bind extracted last frame to target shot", "target_id", targetID, "error", epErr)
		}
	}
	return contracts.StudioExtractLastFrameResult{
		FramePath: out.LastFramePath,
		AssetID:   assetID,
		Version:   verDTO,
		Episode:   epDetail,
	}, nil
}

// RunQCReview invokes tool.qc_review and stores the technical + multimodal Vision critique report.
func (s *Service) RunQCReview(ctx context.Context, p contracts.StudioQCReviewPayload) (contracts.StudioQCReport, error) {
	if s.repo == nil || s.wf == nil {
		return contracts.StudioQCReport{}, apperr.Unavailable("studio dependencies unavailable").
			WithMsg("errors.studio.service_unavailable", nil)
	}
	if err := p.Validate(); err != nil {
		return contracts.StudioQCReport{}, err
	}
	kind := domainstudio.MediaTargetKind(strings.TrimSpace(string(p.TargetKind)))
	if kind == "" {
		kind = domainstudio.TargetVideo
	} else if !kind.Valid() {
		return contracts.StudioQCReport{}, apperr.Validation("invalid target_kind").
			WithMsg("errors.studio.invalid_target_kind", nil)
	}

	proj, err := s.repo.GetProject(ctx, strings.TrimSpace(p.ProjectID))
	if err != nil {
		return contracts.StudioQCReport{}, err
	}
	visionModel := firstNonEmpty(p.VisionModelRef, proj.DefaultTextModel)
	inBytes, merr := json.Marshal(contracts.QCReviewInput{
		MediaPath:      engine.InputRef(p.MediaPath, engine.MediaImage),
		TargetKind:     string(kind),
		TargetID:       p.TargetID,
		ReferencePaths: engine.InputRefs(p.ReferencePaths, engine.MediaImage),
		PromptExpected: p.PromptExpected,
		VisionModelRef: visionModel,
		SampleCount:    p.SampleCount,
		SkipVision:     p.SkipVision,
	})
	if merr != nil {
		return contracts.StudioQCReport{}, apperr.Internal(merr)
	}
	rawOut, err := s.wf.RunNode(ctx, studionodes.NodeTypeQCReview, inBytes)
	if err != nil {
		return contracts.StudioQCReport{}, err
	}
	var out contracts.QCReviewOutput
	if err := decodeNodeOutput(rawOut, &out); err != nil {
		return contracts.StudioQCReport{}, err
	}

	targetID := strings.TrimSpace(p.TargetID)
	if targetID == "" {
		targetID = proj.ID
	}
	verdict := domainstudio.QCStatus(strings.ToLower(strings.TrimSpace(out.Verdict)))
	if !verdict.Valid() {
		verdict = domainstudio.QCWarn
	}
	issues := make([]domainstudio.QCIssue, len(out.Issues))
	for i, iss := range out.Issues {
		issues[i] = domainstudio.QCIssue{
			Severity:  iss.Severity,
			Category:  iss.Category,
			Message:   iss.Message,
			MsgKey:    iss.MsgKey,
			MsgParams: iss.MsgParams,
			FramePath: iss.FramePath,
		}
	}
	rep, err := s.repo.CreateQCReport(ctx, domainstudio.QCReport{
		ProjectID:      proj.ID,
		EpisodeID:      p.EpisodeID,
		TargetKind:     kind,
		TargetID:       targetID,
		MediaVersionID: p.MediaVersionID,
		MediaPath:      p.MediaPath,
		Verdict:        verdict,
		OverallScore:   out.OverallScore,
		FFprobeJSON:    out.FFprobeJSON,
		Issues:         issues,
		VisionSummary:  out.VisionSummary,
		VisionModelRef: visionModel,
		SampleFrames:   out.SampleFrames,
	})
	if err != nil {
		return contracts.StudioQCReport{}, err
	}
	return qcReportToDTO(rep), nil
}

func (s *Service) applyMediaPathToTarget(ctx context.Context, projectID, episodeID string, kind domainstudio.MediaTargetKind, targetID, mediaPath, assetID, reason string) (*contracts.StudioEpisodeDetailResult, error) {
	switch kind {
	case domainstudio.TargetAsset:
		a, err := s.repo.GetAsset(ctx, targetID)
		if err != nil {
			return nil, err
		}
		a.PrimaryPath = mediaPath
		if assetID != "" {
			a.PrimaryAssetID = assetID
		}
		if _, err := s.repo.UpsertAsset(ctx, a); err != nil {
			return nil, err
		}
		if proj, perr := s.repo.GetProject(ctx, projectID); perr == nil {
			if allAssets, lerr := s.repo.ListAssets(ctx, proj.ID); lerr == nil {
				s.warnDiskMirror(ctx, "asset_bible.json", s.persistProjectToDisk(ctx, proj, allAssets))
			}
		}
	case domainstudio.TargetKeyframe, domainstudio.TargetVideo, domainstudio.TargetDubAudio, domainstudio.TargetLipSyncVideo:
		sh, err := s.repo.GetShot(ctx, targetID)
		if err != nil {
			return nil, err
		}
		switch kind {
		case domainstudio.TargetKeyframe:
			sh.KeyframePath = mediaPath
			if assetID != "" {
				sh.KeyframeAssetID = assetID
			}
			if sh.Status == domainstudio.ShotPending || sh.Status == domainstudio.ShotFailed {
				sh.Status = domainstudio.ShotKeyframeReady
			}
		case domainstudio.TargetVideo, domainstudio.TargetLipSyncVideo:
			sh.VideoPath = mediaPath
			if assetID != "" {
				sh.VideoAssetID = assetID
			}
			sh.Status = domainstudio.ShotVideoReady
			sh.ErrorText = ""
		case domainstudio.TargetDubAudio:
			sh.AudioPath = mediaPath
			if assetID != "" {
				sh.AudioAssetID = assetID
			}
			if sh.Status == domainstudio.ShotPending || sh.Status == domainstudio.ShotKeyframeReady {
				sh.Status = domainstudio.ShotTTSReady
			}
		}
		if _, err := s.repo.UpsertShot(ctx, sh); err != nil {
			return nil, err
		}
		epID := firstNonEmpty(episodeID, sh.EpisodeID)
		if epID != "" {
			if detail, gerr := s.GetEpisode(ctx, epID); gerr == nil {
				if proj, perr := s.repo.GetProject(ctx, projectID); perr == nil {
					if epRow, eerr := s.repo.GetEpisode(ctx, epID); eerr == nil {
						if refreshed, lerr := s.repo.ListShots(ctx, epID); lerr == nil {
							s.warnDiskMirror(ctx, "shots.json", s.syncEpisodeShotsToDisk(ctx, proj, epRow, refreshed))
						}
					}
				}
				s.emit(contracts.EventStudioEpisodeUpdated, contracts.StudioEpisodeUpdatedEvent{
					ProjectID: projectID,
					EpisodeID: epID,
					Stage:     detail.Episode.Stage,
					Reason:    reason,
				})
				return &detail, nil
			}
		}
	}
	return nil, nil
}

func decodeBase64PNG(raw string, maxMaskBytes int64) ([]byte, error) {
	s := strings.TrimSpace(raw)
	if idx := strings.Index(s, ","); idx >= 0 && strings.HasPrefix(s[:idx], "data:") {
		s = s[idx+1:]
	}
	// Pre-check encoded base64 length against 4/3 of maxMaskBytes before allocating decoded bytes.
	if maxMaskBytes > 0 && int64(len(s)) > (maxMaskBytes*4)/3+8 {
		return nil, apperr.Validation("mask image payload too large").
			WithMsg("errors.studio.mask_too_large", nil)
	}
	dec, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, apperr.Validation("invalid mask base64 payload").
			WithMsg("errors.studio.invalid_mask", nil)
	}
	if maxMaskBytes > 0 && int64(len(dec)) > maxMaskBytes {
		return nil, apperr.Validation("mask image payload too large").
			WithMsg("errors.studio.mask_too_large", nil)
	}
	return dec, nil
}
