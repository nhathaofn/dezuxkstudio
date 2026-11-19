package studio

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"app/internal/apperr"
	"app/internal/contracts"
	domainstudio "app/internal/domain/studio"
	"app/internal/engine"
	"app/internal/fsutil"
	"app/internal/progress"
	"app/internal/service/media"
)

type episodeDiskMeta struct {
	Episode           domainstudio.Episode `json:"episode"`
	FinalVideoPath    string               `json:"final_video_path,omitempty"`
	FinalVideoAssetID string               `json:"final_video_asset_id,omitempty"`
}

// New constructs a Studio Service.

func (s *Service) registerMedia(ctx context.Context, kind, sourceNode, filePath, rootDir string, durSec float64) string {
	if s.media == nil || strings.TrimSpace(filePath) == "" {
		return ""
	}
	regRoot := strings.TrimSpace(rootDir)
	if regRoot == "" {
		regRoot = filepath.Dir(filePath)
	}
	s.media.AddAllowedRoot(regRoot)
	item, err := s.media.RegisterAsset(ctx, media.RegisterAssetParams{
		Kind:           kind,
		SourceNode:     sourceNode,
		FilePath:       filePath,
		RegisteredRoot: regRoot,
		DurationSec:    durSec,
	})
	if err != nil || item == nil {
		return ""
	}
	return item.ID
}

func (s *Service) reportBatchProgress(ctx context.Context, projectID, episodeID, stage string, cur, total, completed, failed int, targetID, itemLabel, msg string, status contracts.StudioBatchStatus) {
	var pct float64
	if total > 0 {
		pct = float64(cur) / float64(total) * 100.0
	}
	msgKey := "studio.progress.batch_running"
	switch status {
	case contracts.BatchStatusCompleted:
		msgKey = "studio.progress.batch_done"
	case contracts.BatchStatusFailed:
		msgKey = "studio.progress.batch_failed"
	}
	msgParams := map[string]string{
		"stage":     stage,
		"current":   strconv.Itoa(cur),
		"total":     strconv.Itoa(total),
		"completed": strconv.Itoa(completed),
		"failed":    strconv.Itoa(failed),
		"item":      itemLabel,
	}
	progress.Report(ctx, progress.Update{
		Percent: pct,
		Message: msg,
		Key:     msgKey,
		Params:  msgParams,
	})
	s.emit(contracts.EventStudioBatchProgress, contracts.StudioBatchProgressEvent{
		ProjectID: projectID,
		EpisodeID: episodeID,
		Stage:     stage,
		Current:   cur,
		Total:     total,
		TargetID:  targetID,
		Percent:   pct,
		Message:   msg,
		MsgKey:    msgKey,
		MsgParams: msgParams,
		Status:    status,
	})
}

func (s *Service) resolveProjectRoot(proj domainstudio.Project) string {
	if strings.TrimSpace(proj.RootDir) != "" {
		return filepath.Clean(proj.RootDir)
	}
	if pDir := s.ProjectsDir(); pDir != "" && proj.Slug != "" {
		return filepath.Join(pDir, proj.Slug)
	}
	return ""
}

func (s *Service) resolveEpisodeRoot(proj domainstudio.Project, epCode string) string {
	projRoot := s.resolveProjectRoot(proj)
	if projRoot == "" || strings.TrimSpace(epCode) == "" {
		return ""
	}
	return filepath.Join(projRoot, "episodes", epCode)
}

func (s *Service) warnDiskMirror(ctx context.Context, artifact string, err error) {
	if err != nil {
		s.log.WarnContext(ctx, "studio: disk workspace mirror skipped or failed", "artifact", artifact, "error", err)
	}
}

func (s *Service) ensureGuardedProjectDirs(ctx context.Context, projRoot string) error {
	if strings.TrimSpace(projRoot) == "" {
		return nil
	}
	clean := filepath.Clean(projRoot)
	if err := engine.CheckFile(ctx, clean, true); err != nil {
		return err
	}
	if err := ensureProjectDirs(clean); err != nil {
		return apperr.Wrap(apperr.CodeUnavailable, "Failed to initialize project workspace directories", err).
			WithMsg("errors.studio.io_failed", nil)
	}
	return nil
}

func (s *Service) ensureGuardedEpisodeDirs(ctx context.Context, epRoot string) error {
	if strings.TrimSpace(epRoot) == "" {
		return nil
	}
	clean := filepath.Clean(epRoot)
	if err := engine.CheckFile(ctx, clean, true); err != nil {
		return err
	}
	if err := ensureEpisodeDirs(clean); err != nil {
		return apperr.Wrap(apperr.CodeUnavailable, "Failed to initialize episode workspace directories", err).
			WithMsg("errors.studio.io_failed", nil)
	}
	return nil
}

func (s *Service) ensureGuardedDir(ctx context.Context, dir string) error {
	if strings.TrimSpace(dir) == "" {
		return nil
	}
	clean := filepath.Clean(dir)
	if err := engine.CheckFile(ctx, clean, true); err != nil {
		return err
	}
	if err := os.MkdirAll(clean, 0o755); err != nil {
		return apperr.Wrap(apperr.CodeUnavailable, "Failed to prepare workspace directory", err).
			WithMsg("errors.studio.io_failed", nil)
	}
	return nil
}

func (s *Service) writeGuardedFile(ctx context.Context, rootDir, targetPath string, data []byte, perm os.FileMode) error {
	cleanRoot := filepath.Clean(strings.TrimSpace(rootDir))
	cleanTarget := filepath.Clean(strings.TrimSpace(targetPath))
	if strings.TrimSpace(rootDir) == "" || strings.TrimSpace(targetPath) == "" || !isWithinBaseDir(cleanRoot, cleanTarget) {
		return apperr.Validation("workspace artifact path escapes project directory").
			WithMsg("errors.studio.path_outside_workspace", nil)
	}
	if err := engine.CheckFile(ctx, cleanTarget, true); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(cleanTarget), 0o755); err != nil {
		return apperr.Wrap(apperr.CodeUnavailable, "Failed to create workspace artifact directory", err).
			WithMsg("errors.studio.io_failed", nil)
	}
	if err := fsutil.WriteFileAtomic(cleanTarget, data, perm); err != nil {
		return apperr.Wrap(apperr.CodeUnavailable, "Failed to write workspace artifact file", err).
			WithMsg("errors.studio.io_failed", nil)
	}
	return nil
}

func (s *Service) readGuardedBytes(ctx context.Context, rootDir, targetPath string) []byte {
	trimmed := strings.TrimSpace(targetPath)
	if trimmed == "" {
		return nil
	}
	clean := filepath.Clean(trimmed)
	if strings.TrimSpace(rootDir) != "" && !isWithinBaseDir(rootDir, clean) {
		s.log.WarnContext(ctx, "studio: blocked workspace file read outside episode root", "error", "outside_root")
		return nil
	}
	if err := engine.CheckFile(ctx, clean, false); err != nil {
		return nil
	}
	b, err := os.ReadFile(clean)
	if err != nil {
		return nil
	}
	return b
}

func (s *Service) readGuardedStringFile(ctx context.Context, rootDir, targetPath string) string {
	b := s.readGuardedBytes(ctx, rootDir, targetPath)
	if b == nil {
		return ""
	}
	return string(b)
}

func (s *Service) readGuardedNormalizedScriptDomain(ctx context.Context, rootDir, targetPath string) *domainstudio.NormalizedScript {
	b := s.readGuardedBytes(ctx, rootDir, targetPath)
	if len(b) == 0 {
		return nil
	}
	var ns domainstudio.NormalizedScript
	if json.Unmarshal(b, &ns) != nil {
		return nil
	}
	return &ns
}

func (s *Service) readGuardedNormalizedScriptDTO(ctx context.Context, rootDir, targetPath string) *contracts.StudioNormalizedScript {
	ns := s.readGuardedNormalizedScriptDomain(ctx, rootDir, targetPath)
	if ns == nil {
		return nil
	}
	dto := normalizedScriptToDTO(*ns)
	return &dto
}

func (s *Service) persistProjectToDisk(ctx context.Context, proj domainstudio.Project, assets []domainstudio.AssetEntry) error {
	projRoot := s.resolveProjectRoot(proj)
	if projRoot == "" {
		return nil
	}
	if err := s.ensureGuardedProjectDirs(ctx, projRoot); err != nil {
		return err
	}
	if pb, err := json.MarshalIndent(proj, "", "  "); err != nil {
		return apperr.Internal(err)
	} else if werr := s.writeGuardedFile(ctx, projRoot, filepath.Join(projRoot, "project.json"), append(pb, '\n'), 0o644); werr != nil {
		return werr
	}
	if ab, err := json.MarshalIndent(domainstudio.AssetBiblePackage{
		StylePrompt: proj.StylePrompt,
		Assets:      assets,
	}, "", "  "); err != nil {
		return apperr.Internal(err)
	} else if werr := s.writeGuardedFile(ctx, projRoot, filepath.Join(projRoot, "assets", "asset_bible.json"), append(ab, '\n'), 0o644); werr != nil {
		return werr
	}
	return nil
}

func (s *Service) syncEpisodeShotsToDisk(ctx context.Context, proj domainstudio.Project, ep domainstudio.Episode, shots []domainstudio.Shot) error {
	epRoot := s.resolveEpisodeRoot(proj, ep.Code)
	if epRoot == "" {
		return nil
	}
	if err := s.ensureGuardedEpisodeDirs(ctx, epRoot); err != nil {
		return err
	}
	pkg := domainstudio.ShotPromptPackage{
		EpisodeTitle:   ep.Title,
		ProductionMode: proj.ProductionMode,
		Shots:          shots,
	}
	data, err := json.MarshalIndent(pkg, "", "  ")
	if err != nil {
		return apperr.Internal(err)
	}
	target := ep.ShotsJSONPath
	if target == "" {
		target = filepath.Join(epRoot, "storyboard", "shots.json")
	}
	return s.writeGuardedFile(ctx, epRoot, target, append(data, '\n'), 0o644)
}

func (s *Service) persistEpisodeToDisk(ctx context.Context, proj domainstudio.Project, ep domainstudio.Episode, rawScript string, normScript *domainstudio.NormalizedScript, shots []domainstudio.Shot, finalPath, finalAssetID string) error {
	epRoot := s.resolveEpisodeRoot(proj, ep.Code)
	if epRoot == "" {
		return nil
	}
	if err := s.ensureGuardedEpisodeDirs(ctx, epRoot); err != nil {
		return err
	}
	meta := episodeDiskMeta{
		Episode:           ep,
		FinalVideoPath:    finalPath,
		FinalVideoAssetID: finalAssetID,
	}
	if mb, err := json.MarshalIndent(meta, "", "  "); err != nil {
		return apperr.Internal(err)
	} else if werr := s.writeGuardedFile(ctx, epRoot, filepath.Join(epRoot, "episode.json"), append(mb, '\n'), 0o644); werr != nil {
		return werr
	}
	if rawScript != "" && ep.RawScriptPath != "" {
		if werr := s.writeGuardedFile(ctx, epRoot, ep.RawScriptPath, []byte(rawScript), 0o644); werr != nil {
			return werr
		}
	}
	if normScript != nil && ep.ScriptJSONPath != "" {
		if sb, err := json.MarshalIndent(normScript, "", "  "); err != nil {
			return apperr.Internal(err)
		} else if werr := s.writeGuardedFile(ctx, epRoot, ep.ScriptJSONPath, append(sb, '\n'), 0o644); werr != nil {
			return werr
		}
	}
	if len(shots) > 0 {
		if werr := s.syncEpisodeShotsToDisk(ctx, proj, ep, shots); werr != nil {
			return werr
		}
	}
	return nil
}

func (s *Service) readEpisodeDiskMeta(ctx context.Context, epRoot string) *episodeDiskMeta {
	if epRoot == "" {
		return nil
	}
	b := s.readGuardedBytes(ctx, epRoot, filepath.Join(epRoot, "episode.json"))
	if len(b) == 0 {
		return nil
	}
	var m episodeDiskMeta
	if json.Unmarshal(b, &m) != nil {
		return nil
	}
	m.FinalVideoPath = s.sanitizeSyncedPath(ctx, epRoot, m.Episode.ProjectID, m.FinalVideoPath)
	if m.FinalVideoPath == "" {
		m.FinalVideoAssetID = ""
	}
	return &m
}

func (s *Service) allocateUniqueSlug(ctx context.Context, base string) (string, error) {
	projects, err := s.repo.ListProjects(ctx)
	if err != nil {
		return "", err
	}
	used := make(map[string]bool, len(projects))
	for _, pr := range projects {
		used[strings.ToLower(pr.Slug)] = true
	}
	if !used[strings.ToLower(base)] {
		return base, nil
	}
	for i := 2; i < 1000; i++ {
		candidate := fmt.Sprintf("%s-%d", base, i)
		if !used[strings.ToLower(candidate)] {
			return candidate, nil
		}
	}
	return fmt.Sprintf("%s-%d", base, time.Now().Unix()), nil
}

func ensureProjectDirs(projRoot string) error {
	for _, d := range []string{projRoot, filepath.Join(projRoot, "assets"), filepath.Join(projRoot, "episodes")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	return nil
}

func ensureEpisodeDirs(epRoot string) error {
	for _, sub := range []string{
		epRoot,
		filepath.Join(epRoot, "script"),
		filepath.Join(epRoot, "assets"),
		filepath.Join(epRoot, "storyboard"),
		filepath.Join(epRoot, "media", "assets"),
		filepath.Join(epRoot, "media", "keyframes"),
		filepath.Join(epRoot, "media", "videos"),
		filepath.Join(epRoot, "media", "tts"),
		filepath.Join(epRoot, "media", "thumbs"),
		filepath.Join(epRoot, "timeline"),
		filepath.Join(epRoot, "exports"),
	} {
		if err := os.MkdirAll(sub, 0o755); err != nil {
			return err
		}
	}
	return nil
}

func buildAssetPathMap(assets []domainstudio.AssetEntry) map[string]string {
	m := make(map[string]string, len(assets))
	for _, a := range assets {
		if strings.TrimSpace(a.PrimaryPath) == "" || strings.TrimSpace(a.CodeTag) == "" {
			continue
		}
		if _, err := os.Stat(a.PrimaryPath); err == nil {
			m[strings.ToLower(a.CodeTag)] = a.PrimaryPath
		}
	}
	return m
}

func collectRefImages(tags []string, tagPaths map[string]string) engine.ImageInputRefs {
	if len(tags) == 0 || len(tagPaths) == 0 {
		return nil
	}
	seen := make(map[string]bool)
	var out engine.ImageInputRefs
	for _, tag := range tags {
		norm := strings.ToLower(domainstudio.NormalizeCodeTag(tag, ""))
		if p, ok := tagPaths[norm]; ok && !seen[p] {
			seen[p] = true
			out = append(out, engine.RefFor(p, engine.MediaImage))
		}
	}
	return out
}

func composePrompt(stylePrompt, corePrompt string) string {
	style := strings.TrimSpace(stylePrompt)
	core := strings.TrimSpace(corePrompt)
	if style == "" {
		return core
	}
	if core == "" {
		return style
	}
	return core + ", visual style: " + style
}

func shotSpeechText(sh domainstudio.Shot) string {
	if t := strings.TrimSpace(sh.NarrationText); t != "" {
		return t
	}
	if len(sh.Dialogues) == 0 {
		return ""
	}
	parts := make([]string, 0, len(sh.Dialogues))
	for _, d := range sh.Dialogues {
		line := strings.TrimSpace(d.Line)
		if line != "" {
			parts = append(parts, line)
		}
	}
	return strings.Join(parts, " ")
}

func resolutionForAspectRatio(aspect, fallback string) string {
	return domainstudio.ResolutionForAspectRatio(aspect, fallback)
}

func decodeNodeOutput(raw any, dst any) error {
	b, err := json.Marshal(raw)
	if err != nil {
		return apperr.Wrap(apperr.CodeIntegrity, "Invalid workflow node output", err).
			WithMsg("errors.studio.invalid_node_output", nil)
	}
	if err := json.Unmarshal(b, dst); err != nil {
		return apperr.Wrap(apperr.CodeIntegrity, "Invalid workflow node output", err).
			WithMsg("errors.studio.invalid_node_output", nil)
	}
	return nil
}

func isDirectOrSubpath(parent, child string) bool {
	pAbs, err1 := filepath.Abs(parent)
	cAbs, err2 := filepath.Abs(child)
	if err1 != nil || err2 != nil {
		return false
	}
	rel, err := filepath.Rel(pAbs, cAbs)
	if err != nil || rel == "" || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	return true
}

func toSet(items []string) map[string]bool {
	if len(items) == 0 {
		return nil
	}
	m := make(map[string]bool, len(items))
	for _, it := range items {
		if t := strings.TrimSpace(it); t != "" {
			m[t] = true
		}
	}
	return m
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if t := strings.TrimSpace(v); t != "" {
			return t
		}
	}
	return ""
}

func removeDirWithRetry(dir string) error {
	var err error
	for attempt := range 5 {
		err = os.RemoveAll(dir)
		if err == nil {
			return nil
		}
		if attempt < 4 {
			time.Sleep(time.Duration(attempt+1) * 40 * time.Millisecond)
		}
	}
	return err
}
