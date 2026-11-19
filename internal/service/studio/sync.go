package studio

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"app/internal/apperr"
	"app/internal/contracts"
	domainmedia "app/internal/domain/mediagen"
	domainstudio "app/internal/domain/studio"
	"app/internal/engine"
)

func (s *Service) SyncWorkspace(ctx context.Context, projectID string) (*contracts.StudioProjectDetailResult, error) {
	if s.repo == nil {
		return nil, apperr.Unavailable("studio repository is unavailable").
			WithMsg("errors.studio.service_unavailable", nil)
	}
	trimmedID := strings.TrimSpace(projectID)
	if trimmedID != "" {
		proj, err := s.repo.GetProject(ctx, trimmedID)
		if err != nil {
			return nil, err
		}
		if err := s.syncSingleProjectFromDisk(ctx, proj); err != nil {
			return nil, err
		}
		detail, err := s.GetProject(ctx, proj.ID)
		return &detail, err
	}

	pDir := s.ProjectsDir()
	if pDir == "" {
		return nil, nil
	}
	if cerr := engine.CheckFile(ctx, pDir, false); cerr != nil {
		return nil, cerr
	}
	entries, err := os.ReadDir(pDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, apperr.Wrap(apperr.CodeUnavailable, "Failed to read studio projects directory", err).
			WithMsg("errors.studio.io_failed", nil)
	}

	var firstSynced *contracts.StudioProjectDetailResult
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		projRoot := filepath.Join(pDir, entry.Name())
		projFile := filepath.Join(projRoot, "project.json")
		if cerr := engine.CheckFile(ctx, projFile, false); cerr != nil {
			continue
		}
		data, rerr := os.ReadFile(projFile)
		if rerr != nil {
			continue
		}
		var diskProj domainstudio.Project
		if jerr := json.Unmarshal(data, &diskProj); jerr != nil {
			continue
		}
		if diskProj.Slug == "" {
			diskProj.Slug = domainstudio.Slugify(entry.Name())
		}
		if diskProj.Title == "" {
			diskProj.Title = entry.Name()
		}
		diskProj.RootDir = projRoot
		// Never trust auto_approve_batches from disk JSON; users must explicitly enable it in SQLite/UI.
		diskProj.AutoApproveBatches = false
		sanitizeDiskProjectEnums(&diskProj)

		existing, gerr := s.repo.GetProject(ctx, diskProj.ID)
		if gerr != nil {
			existing, gerr = s.repo.GetProject(ctx, diskProj.Slug)
		}
		if gerr != nil {
			created, cerr := s.repo.CreateProject(ctx, diskProj)
			if cerr != nil {
				s.log.WarnContext(ctx, "studio: failed to import project from disk", "slug", diskProj.Slug, "error", cerr)
				continue
			}
			existing = created
		} else {
			existing.Title = diskProj.Title
			existing.StylePrompt = diskProj.StylePrompt
			if diskProj.AspectRatio != "" {
				existing.AspectRatio = diskProj.AspectRatio
			}
			if diskProj.Genre != "" {
				existing.Genre = diskProj.Genre
			}
			if diskProj.ProductionMode != "" {
				existing.ProductionMode = diskProj.ProductionMode
			}
			if diskProj.RootDir != "" {
				existing.RootDir = diskProj.RootDir
			}
			if upd, uerr := s.repo.UpdateProject(ctx, existing); uerr == nil {
				existing = upd
			} else {
				s.log.WarnContext(ctx, "studio: failed to update synced project metadata", "project_id", existing.ID, "error", uerr)
			}
		}

		if serr := s.syncSingleProjectFromDisk(ctx, existing); serr != nil {
			s.log.WarnContext(ctx, "studio: failed to sync project workspace artifacts", "project_id", existing.ID, "error", serr)
		}
		if firstSynced == nil {
			if detail, derr := s.GetProject(ctx, existing.ID); derr == nil {
				firstSynced = &detail
			}
		}
	}

	s.emit(contracts.EventStudioProjectChanged, contracts.StudioProjectChangedEvent{
		Action: contracts.ProjectActionSynced,
	})
	return firstSynced, nil
}

// SyncAndListWorkspace reconciles disk projects/episodes with SQLite and returns the
// optionally refreshed project alongside the full project listing.
func (s *Service) SyncAndListWorkspace(ctx context.Context, projectID string) (contracts.StudioSyncWorkspaceResult, error) {
	proj, err := s.SyncWorkspace(ctx, projectID)
	if err != nil {
		return contracts.StudioSyncWorkspaceResult{}, err
	}
	list, err := s.ListProjects(ctx)
	if err != nil {
		return contracts.StudioSyncWorkspaceResult{}, err
	}
	return contracts.StudioSyncWorkspaceResult{
		Project:     proj,
		Projects:    list.Projects,
		ProjectsDir: list.ProjectsDir,
	}, nil
}

func sanitizeDiskProjectEnums(dp *domainstudio.Project) {
	if dp == nil {
		return
	}
	if dp.Genre != "" && domainstudio.ValidateGenre(dp.Genre) != nil {
		dp.Genre = ""
	}
	if dp.ProductionMode != "" && domainstudio.ValidateProductionMode(dp.ProductionMode) != nil {
		dp.ProductionMode = ""
	}
	if ar := strings.TrimSpace(dp.AspectRatio); ar != "" && !slices.Contains(domainmedia.VideoAspectRatios(), ar) {
		dp.AspectRatio = ""
	}
}

func (s *Service) sanitizeSyncedPath(ctx context.Context, baseDir, projectID, rawPath string) string {
	trimmed := strings.TrimSpace(rawPath)
	if trimmed == "" || strings.TrimSpace(baseDir) == "" {
		return ""
	}
	candidate := trimmed
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(baseDir, candidate)
	}
	clean := filepath.Clean(candidate)
	if !isWithinBaseDir(baseDir, clean) {
		s.log.WarnContext(ctx, "studio: stripped outside-workspace path during sync", "project_id", projectID)
		return ""
	}
	if err := engine.CheckFile(ctx, clean, false); err != nil {
		s.log.WarnContext(ctx, "studio: stripped untrusted file path during sync", "project_id", projectID, "error", err)
		return ""
	}
	return clean
}

func (s *Service) syncSingleProjectFromDisk(ctx context.Context, proj domainstudio.Project) error {
	projRoot := s.resolveProjectRoot(proj)
	if projRoot == "" {
		return nil
	}
	if err := engine.CheckFile(ctx, projRoot, false); err != nil {
		return err
	}
	if s.media != nil {
		s.media.AddAllowedRoot(projRoot)
	}

	// Sync project.json metadata if modified on disk (never overwriting AutoApproveBatches!).
	projJSONPath := filepath.Join(projRoot, "project.json")
	if engine.CheckFile(ctx, projJSONPath, false) == nil {
		if b, err := os.ReadFile(projJSONPath); err == nil {
			var dp domainstudio.Project
			if json.Unmarshal(b, &dp) == nil && strings.TrimSpace(dp.Title) != "" {
				sanitizeDiskProjectEnums(&dp)
				proj.Title = strings.TrimSpace(dp.Title)
				if dp.Genre != "" {
					proj.Genre = dp.Genre
				}
				if dp.ProductionMode != "" {
					proj.ProductionMode = dp.ProductionMode
				}
				if dp.AspectRatio != "" {
					proj.AspectRatio = dp.AspectRatio
				}
				proj.StylePrompt = dp.StylePrompt
				if updated, uerr := s.repo.UpdateProject(ctx, proj); uerr == nil {
					proj = updated
				} else {
					s.log.WarnContext(ctx, "studio: failed to update project from disk project.json", "project_id", proj.ID, "error", uerr)
				}
			}
		}
	}

	// Sync asset_bible.json -> studio_assets.
	biblePath := filepath.Join(projRoot, "assets", "asset_bible.json")
	if engine.CheckFile(ctx, biblePath, false) == nil {
		if b, err := os.ReadFile(biblePath); err == nil {
			var pkg domainstudio.AssetBiblePackage
			if json.Unmarshal(b, &pkg) == nil {
				existingAssets, lerr := s.repo.ListAssets(ctx, proj.ID)
				if lerr != nil {
					s.log.WarnContext(ctx, "studio: failed to list existing assets during sync", "project_id", proj.ID, "error", lerr)
				}
				byTag := make(map[string]domainstudio.AssetEntry, len(existingAssets))
				for _, ea := range existingAssets {
					byTag[strings.ToLower(ea.CodeTag)] = ea
				}
				merged := make([]domainstudio.AssetEntry, 0, len(pkg.Assets))
				for _, da := range pkg.Assets {
					da.ProjectID = proj.ID
					da.Normalize()
					hadDiskPath := strings.TrimSpace(da.PrimaryPath) != ""
					da.PrimaryPath = s.sanitizeSyncedPath(ctx, projRoot, proj.ID, da.PrimaryPath)
					if hadDiskPath && da.PrimaryPath == "" {
						da.PrimaryAssetID = ""
					}
					if prev, ok := byTag[strings.ToLower(da.CodeTag)]; ok {
						if da.ID == "" {
							da.ID = prev.ID
						}
						if !hadDiskPath && da.PrimaryPath == "" {
							da.PrimaryPath = prev.PrimaryPath
							if da.PrimaryAssetID == "" {
								da.PrimaryAssetID = prev.PrimaryAssetID
							}
						}
					}
					merged = append(merged, da)
				}
				if _, rerr := s.repo.ReplaceAssets(ctx, proj.ID, merged); rerr != nil {
					s.log.WarnContext(ctx, "studio: failed to replace synced assets", "project_id", proj.ID, "error", rerr)
				}
			}
		}
	}

	// Sync episodes/*/storyboard/shots.json & timeline/timeline.json.
	epsDir := filepath.Join(projRoot, "episodes")
	epEntries, err := os.ReadDir(epsDir)
	if err == nil {
		existingEps, lerr := s.repo.ListEpisodes(ctx, proj.ID)
		if lerr != nil {
			s.log.WarnContext(ctx, "studio: failed to list episodes during sync", "project_id", proj.ID, "error", lerr)
		}
		epByCode := make(map[string]domainstudio.Episode, len(existingEps))
		for _, ee := range existingEps {
			epByCode[ee.Code] = ee
		}
		for _, entry := range epEntries {
			if !entry.IsDir() {
				continue
			}
			epCode := domainstudio.Slugify(entry.Name())
			epRoot := filepath.Join(epsDir, entry.Name())
			if !isWithinBaseDir(projRoot, epRoot) {
				continue
			}
			ep, exists := epByCode[epCode]
			if !exists {
				created, cerr := s.repo.CreateEpisode(ctx, domainstudio.Episode{
					ProjectID:        proj.ID,
					Code:             epCode,
					Title:            strings.ToUpper(epCode),
					Stage:            domainstudio.StageScript,
					RawScriptPath:    filepath.Join(epRoot, "script", "raw_screenplay.txt"),
					ScriptJSONPath:   filepath.Join(epRoot, "script", "normalized_script.json"),
					AssetsJSONPath:   filepath.Join(epRoot, "assets", "asset_bible.json"),
					ShotsJSONPath:    filepath.Join(epRoot, "storyboard", "shots.json"),
					TimelineJSONPath: filepath.Join(epRoot, "timeline", "timeline.json"),
				})
				if cerr != nil {
					s.log.WarnContext(ctx, "studio: failed to create episode during disk sync", "project_id", proj.ID, "code", epCode, "error", cerr)
					continue
				}
				ep = created
			}
			timelinePath := filepath.Join(epRoot, "timeline", "timeline.json")
			if ep.TimelineJSONPath == "" {
				ep.TimelineJSONPath = timelinePath
				if updatedEp, uerr := s.repo.UpdateEpisode(ctx, ep); uerr == nil {
					ep = updatedEp
				} else {
					s.log.WarnContext(ctx, "studio: failed to update episode timeline path during sync", "episode_id", ep.ID, "error", uerr)
				}
			}
			shotsDiskPath := filepath.Join(epRoot, "storyboard", "shots.json")
			if engine.CheckFile(ctx, shotsDiskPath, false) == nil {
				if b, serr := os.ReadFile(shotsDiskPath); serr == nil {
					var sp domainstudio.ShotPromptPackage
					if json.Unmarshal(b, &sp) == nil {
						existingShots, lserr := s.repo.ListShots(ctx, ep.ID)
						if lserr != nil {
							s.log.WarnContext(ctx, "studio: failed to list existing shots during sync", "episode_id", ep.ID, "error", lserr)
						}
						byID := make(map[string]domainstudio.Shot, len(existingShots))
						bySeq := make(map[int]domainstudio.Shot, len(existingShots))
						for _, es := range existingShots {
							byID[es.ID] = es
							bySeq[es.Seq] = es
						}
						merged := make([]domainstudio.Shot, 0, len(sp.Shots))
						for i, ds := range sp.Shots {
							ds.EpisodeID = ep.ID
							if ds.Seq <= 0 {
								ds.Seq = i + 1
							}
							ds.NormalizeWithDefaultDuration(s.cfg.DefaultShotDurationSec)

							hadKF := strings.TrimSpace(ds.KeyframePath) != ""
							hadVid := strings.TrimSpace(ds.VideoPath) != ""
							hadAud := strings.TrimSpace(ds.AudioPath) != ""
							ds.KeyframePath = s.sanitizeSyncedPath(ctx, projRoot, proj.ID, ds.KeyframePath)
							ds.VideoPath = s.sanitizeSyncedPath(ctx, projRoot, proj.ID, ds.VideoPath)
							ds.AudioPath = s.sanitizeSyncedPath(ctx, projRoot, proj.ID, ds.AudioPath)
							if hadKF && ds.KeyframePath == "" {
								ds.KeyframeAssetID = ""
							}
							if hadVid && ds.VideoPath == "" {
								ds.VideoAssetID = ""
							}
							if hadAud && ds.AudioPath == "" {
								ds.AudioAssetID = ""
							}

							prev, ok := byID[ds.ID]
							if !ok {
								prev, ok = bySeq[ds.Seq]
							}
							if ok {
								if ds.ID == "" {
									ds.ID = prev.ID
								}
								if !hadKF && ds.KeyframePath == "" {
									ds.KeyframePath = prev.KeyframePath
									ds.KeyframeAssetID = prev.KeyframeAssetID
								}
								if !hadVid && ds.VideoPath == "" {
									ds.VideoPath = prev.VideoPath
									ds.VideoAssetID = prev.VideoAssetID
								}
								if !hadAud && ds.AudioPath == "" {
									ds.AudioPath = prev.AudioPath
									ds.AudioAssetID = prev.AudioAssetID
								}
							}
							merged = append(merged, ds)
						}
						if _, rerr := s.repo.ReplaceShots(ctx, ep.ID, merged); rerr != nil {
							s.log.WarnContext(ctx, "studio: failed to replace synced shots", "episode_id", ep.ID, "error", rerr)
						}
					}
				}
			}
			if engine.CheckFile(ctx, timelinePath, false) == nil {
				if tb, terr := os.ReadFile(timelinePath); terr == nil {
					var diskTL domainstudio.EditTimeline
					if json.Unmarshal(tb, &diskTL) == nil && len(diskTL.Tracks) > 0 {
						for ti := range diskTL.Tracks {
							filtered := make([]domainstudio.TimelineClip, 0, len(diskTL.Tracks[ti].Clips))
							for _, cl := range diskTL.Tracks[ti].Clips {
								hadFP := strings.TrimSpace(cl.FilePath) != ""
								hadSP := strings.TrimSpace(cl.SpritePath) != ""
								cl.FilePath = s.sanitizeSyncedPath(ctx, projRoot, proj.ID, cl.FilePath)
								cl.SpritePath = s.sanitizeSyncedPath(ctx, projRoot, proj.ID, cl.SpritePath)
								if hadFP && cl.FilePath == "" {
									cl.AssetID = ""
								}
								if hadSP && cl.SpritePath == "" {
									cl.SpriteAssetID = ""
								}
								if cl.FilePath == "" && cl.AssetID == "" {
									continue
								}
								filtered = append(filtered, cl)
							}
							diskTL.Tracks[ti].Clips = filtered
						}
						diskTL.Normalize()
						if verr := diskTL.ValidateWithMaxDim(s.cfg.MaxVideoDimension); verr == nil {
							diskCanon, _ := json.Marshal(diskTL)
							if curRec, rerr := s.repo.GetTimeline(ctx, ep.ID); rerr == nil {
								curCanon, _ := json.Marshal(curRec.Timeline)
								if string(diskCanon) != string(curCanon) {
									if _, serr := s.repo.SaveTimelineRevision(ctx, ep.ID, curRec.Revision, domainstudio.ActorUser, "disk sync", diskTL); serr == nil {
										s.log.DebugContext(ctx, "reconciled timeline.json from disk", "episode_id", ep.ID)
									} else {
										s.log.WarnContext(ctx, "studio: failed to save synced timeline revision", "episode_id", ep.ID, "error", serr)
									}
								}
							}
						} else {
							s.log.WarnContext(ctx, "studio: skipped invalid timeline.json during disk sync", "episode_id", ep.ID, "error", verr)
						}
					}
				}
			}
		}
	}

	dtoProj := projectToDTO(proj)
	s.emit(contracts.EventStudioProjectChanged, contracts.StudioProjectChangedEvent{
		ProjectID: proj.ID,
		Action:    contracts.ProjectActionSynced,
		Project:   &dtoProj,
	})
	return nil
}

// RunStage executes any Phase 1 stage or shot/asset batch using shared workflow nodes.
