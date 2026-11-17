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

const maxMaskBase64Bytes = 8 << 20

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
		s.log.Warn("studio: failed to persist media version", "target", targetID, "err", err)
		return nil
	}
	return &v
}

// ListVersions returns all saved versions and latest QC report for a shot slot or asset.
func (s *Service) ListVersions(ctx context.Context, p contracts.StudioVersionsListPayload) (contracts.StudioVersionsListResult, error) {
	if s.repo == nil {
		return contracts.StudioVersionsListResult{}, apperr.Unavailable("studio repository unavailable")
	}
	kind := domainstudio.MediaTargetKind(strings.TrimSpace(p.TargetKind))
	if !kind.Valid() || strings.TrimSpace(p.TargetID) == "" {
		return contracts.StudioVersionsListResult{}, apperr.Validation("valid target_kind and target_id are required")
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
		return contracts.StudioVersionSelectResult{}, apperr.Unavailable("studio repository unavailable")
	}
	if strings.TrimSpace(p.VersionID) == "" {
		return contracts.StudioVersionSelectResult{}, apperr.Validation("version_id is required")
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
		return contracts.StudioGridSplitResult{}, apperr.Unavailable("studio dependencies unavailable")
	}
	proj, err := s.repo.GetProject(ctx, strings.TrimSpace(p.ProjectID))
	if err != nil {
		return contracts.StudioGridSplitResult{}, err
	}
	outDir := filepath.Join(s.resolveProjectRoot(proj), "assets", "grid_panels")
	if p.EpisodeID != "" {
		if ep, eerr := s.repo.GetEpisode(ctx, p.EpisodeID); eerr == nil {
			if epRoot := s.resolveEpisodeRoot(proj, ep.Code); epRoot != "" {
				outDir = filepath.Join(epRoot, "images", "grid_panels")
			}
		}
	}
	inBytes, _ := json.Marshal(contracts.GridSplitInput{
		ImagePath:  p.ImagePath,
		Rows:       p.Rows,
		Cols:       p.Cols,
		MarginPx:   p.MarginPx,
		GutterPx:   p.GutterPx,
		RowWeights: p.RowWeights,
		ColWeights: p.ColWeights,
		OutputDir:  outDir,
	})
	rawOut, err := s.wf.RunNode(ctx, studionodes.NodeTypeGridSplit, inBytes)
	if err != nil {
		return contracts.StudioGridSplitResult{}, err
	}
	var out contracts.GridSplitOutput
	if err := decodeNodeOutput(rawOut, &out); err != nil {
		return contracts.StudioGridSplitResult{}, err
	}

	targetKind := domainstudio.MediaTargetKind(strings.TrimSpace(p.TargetKind))
	if !targetKind.Valid() {
		targetKind = domainstudio.TargetGridPanel
	}
	targetID := strings.TrimSpace(p.TargetID)
	if targetID == "" {
		targetID = proj.ID
	}
	var versions []contracts.StudioMediaVersion
	for _, panel := range out.Panels {
		v, err := s.repo.CreateMediaVersion(ctx, domainstudio.MediaVersion{
			ProjectID:  proj.ID,
			EpisodeID:  p.EpisodeID,
			TargetKind: targetKind,
			TargetID:   targetID,
			MediaPath:  panel.Path,
			SourceNode: studionodes.NodeTypeGridSplit,
			IsSelected: false,
		})
		if err == nil {
			versions = append(versions, mediaVersionToDTO(v))
		}
	}
	return contracts.StudioGridSplitResult{Panels: out.Panels, Versions: versions}, nil
}

// ApplyMaskEdit saves the painted mask PNG (when passed as base64), invokes tool.mask_edit,
// records a new selected version, and updates the shot or asset image.
func (s *Service) ApplyMaskEdit(ctx context.Context, p contracts.StudioMaskEditPayload) (contracts.StudioMaskEditResult, error) {
	if s.repo == nil || s.wf == nil {
		return contracts.StudioMaskEditResult{}, apperr.Unavailable("studio dependencies unavailable")
	}
	proj, err := s.repo.GetProject(ctx, strings.TrimSpace(p.ProjectID))
	if err != nil {
		return contracts.StudioMaskEditResult{}, err
	}
	if strings.TrimSpace(p.ImagePath) == "" || strings.TrimSpace(p.Prompt) == "" {
		return contracts.StudioMaskEditResult{}, apperr.Validation("image_path and prompt are required")
	}

	projRoot := s.resolveProjectRoot(proj)
	outDir := filepath.Join(projRoot, "edits")
	maskPath := strings.TrimSpace(p.MaskPath)
	if p.MaskDataBase64 != "" {
		maskBytes, derr := decodeBase64PNG(p.MaskDataBase64)
		if derr != nil {
			return contracts.StudioMaskEditResult{}, derr
		}
		if err := os.MkdirAll(outDir, 0o750); err != nil {
			return contracts.StudioMaskEditResult{}, apperr.Internal(err)
		}
		maskPath = filepath.Join(outDir, fmt.Sprintf("mask_%d.png", time.Now().UnixNano()))
		if err := engine.CheckFile(ctx, maskPath, true); err != nil {
			return contracts.StudioMaskEditResult{}, err
		}
		if err := fsutil.WriteFileAtomic(maskPath, maskBytes, 0o600); err != nil {
			return contracts.StudioMaskEditResult{}, apperr.Internal(err)
		}
	}
	if maskPath == "" {
		return contracts.StudioMaskEditResult{}, apperr.Validation("mask_path or mask_data_base64 is required")
	}

	modelRef := firstNonEmpty(p.ModelRef, proj.DefaultImageModel)
	inBytes, _ := json.Marshal(contracts.MaskEditInput{
		ImagePath:   p.ImagePath,
		MaskPath:    maskPath,
		Prompt:      p.Prompt,
		ModelRef:    modelRef,
		AspectRatio: proj.AspectRatio,
		OutputDir:   outDir,
	})
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
	kind := domainstudio.MediaTargetKind(strings.TrimSpace(p.TargetKind))
	if kind.Valid() && strings.TrimSpace(p.TargetID) != "" {
		if v := s.recordSelectedVersion(ctx, proj.ID, p.EpisodeID, kind, p.TargetID, out.Path, studionodes.NodeTypeMaskEdit, p.Prompt, modelRef); v != nil {
			dto := mediaVersionToDTO(*v)
			verDTO = &dto
		}
		epDetail, _ = s.applyMediaPathToTarget(ctx, proj.ID, p.EpisodeID, kind, p.TargetID, out.Path, out.AssetID, "mask_edit_applied")
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
		return contracts.StudioExtractLastFrameResult{}, apperr.Unavailable("studio dependencies unavailable")
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
		return contracts.StudioExtractLastFrameResult{}, apperr.Validation("source shot has no generated video")
	}

	outDir := filepath.Join(s.resolveEpisodeRoot(proj, ep.Code), "images")
	inBytes, _ := json.Marshal(contracts.ExtractFramesInput{
		VideoPath: srcShot.VideoPath,
		Mode:      "last",
		OutputDir: outDir,
	})
	rawOut, err := s.wf.RunNode(ctx, studionodes.NodeTypeExtractFrames, inBytes)
	if err != nil {
		return contracts.StudioExtractLastFrameResult{}, err
	}
	var out contracts.ExtractFramesOutput
	if err := decodeNodeOutput(rawOut, &out); err != nil {
		return contracts.StudioExtractLastFrameResult{}, err
	}
	if out.LastFramePath == "" {
		return contracts.StudioExtractLastFrameResult{}, apperr.Unavailable("no frame was extracted from video")
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
		epDetail, _ = s.applyMediaPathToTarget(ctx, proj.ID, ep.ID, domainstudio.TargetKeyframe, targetID, out.LastFramePath, assetID, "extracted_last_frame")
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
		return contracts.StudioQCReport{}, apperr.Unavailable("studio dependencies unavailable")
	}
	proj, err := s.repo.GetProject(ctx, strings.TrimSpace(p.ProjectID))
	if err != nil {
		return contracts.StudioQCReport{}, err
	}
	visionModel := firstNonEmpty(p.VisionModelRef, proj.DefaultTextModel)
	inBytes, _ := json.Marshal(contracts.QCReviewInput{
		MediaPath:      p.MediaPath,
		TargetKind:     p.TargetKind,
		TargetID:       p.TargetID,
		ReferencePaths: p.ReferencePaths,
		PromptExpected: p.PromptExpected,
		VisionModelRef: visionModel,
		SampleCount:    p.SampleCount,
		SkipVision:     p.SkipVision,
	})
	rawOut, err := s.wf.RunNode(ctx, studionodes.NodeTypeQCReview, inBytes)
	if err != nil {
		return contracts.StudioQCReport{}, err
	}
	var out contracts.QCReviewOutput
	if err := decodeNodeOutput(rawOut, &out); err != nil {
		return contracts.StudioQCReport{}, err
	}

	kind := domainstudio.MediaTargetKind(strings.TrimSpace(p.TargetKind))
	if !kind.Valid() {
		kind = domainstudio.TargetVideo
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
				_ = s.persistProjectToDisk(proj, allAssets)
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
							_ = s.syncEpisodeShotsToDisk(proj, epRow, refreshed)
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

func decodeBase64PNG(raw string) ([]byte, error) {
	s := strings.TrimSpace(raw)
	if idx := strings.Index(s, ","); idx >= 0 && strings.HasPrefix(s[:idx], "data:") {
		s = s[idx+1:]
	}
	if len(s) > maxMaskBase64Bytes {
		return nil, apperr.Validation("mask image payload too large")
	}
	dec, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, apperr.Validation("invalid mask base64 payload")
	}
	return dec, nil
}
