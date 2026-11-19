package studio

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"app/internal/apperr"
	"app/internal/contracts"
	domainstudio "app/internal/domain/studio"
	"app/internal/engine"
	"app/internal/progress"
	"app/internal/safego"
)

// InspectShots runs tool.video_contact_sheet across selected (or all video-ready) episode shots
// using a bounded worker pool and returns labeled contact sheets + BLACK/FREEZE/CUT signals.
func (s *Service) InspectShots(ctx context.Context, p contracts.StudioInspectShotsPayload) (contracts.StudioInspectShotsResult, error) {
	if s.repo == nil || s.wf == nil {
		return contracts.StudioInspectShotsResult{}, apperr.Unavailable("studio dependencies are unavailable").
			WithMsg("errors.studio.service_unavailable", nil)
	}
	if err := p.Validate(); err != nil {
		return contracts.StudioInspectShotsResult{}, err
	}

	ep, err := s.repo.GetEpisode(ctx, strings.TrimSpace(p.EpisodeID))
	if err != nil {
		return contracts.StudioInspectShotsResult{}, err
	}
	proj, err := s.repo.GetProject(ctx, ep.ProjectID)
	if err != nil {
		return contracts.StudioInspectShotsResult{}, err
	}
	shots, err := s.repo.ListShots(ctx, ep.ID)
	if err != nil {
		return contracts.StudioInspectShotsResult{}, err
	}

	wantIDs := toSet(p.ShotIDs)
	var targets []domainstudio.Shot
	for _, sh := range shots {
		if len(wantIDs) > 0 && !wantIDs[sh.ID] {
			continue
		}
		if strings.TrimSpace(sh.VideoPath) == "" {
			continue
		}
		targets = append(targets, sh)
	}
	if len(targets) == 0 {
		return contracts.StudioInspectShotsResult{
			EpisodeID: ep.ID,
			Shots:     []contracts.StudioShotInspectionItem{},
			Summary:   "No rendered video shots found to inspect.",
		}, nil
	}

	epRoot := s.resolveEpisodeRoot(proj, ep.Code)
	thumbsDir := ""
	if epRoot != "" {
		thumbsDir = filepath.Join(epRoot, "media", "thumbs")
		if err := s.ensureGuardedDir(ctx, thumbsDir); err != nil {
			return contracts.StudioInspectShotsResult{}, err
		}
	}

	maxFrames := p.MaxFrames
	if maxFrames == 0 {
		maxFrames = s.cfg.ContactSheetMaxFramesPerShot
	}
	perSheet := p.FramesPerSheet
	if perSheet == 0 {
		perSheet = s.cfg.ContactSheetFramesPerSheet
	}

	results := make([]contracts.StudioShotInspectionItem, len(targets))
	workers := min(len(targets), max(1, s.cfg.MaxConcurrentBatch))
	jobs := make(chan int, len(targets))
	for i := range targets {
		jobs <- i
	}
	close(jobs)

	var doneMu sync.Mutex
	completed := 0
	total := len(targets)

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		safego.Go("studio-inspect-shots-worker", func() {
			defer wg.Done()
			for idx := range jobs {
				if ctx.Err() != nil {
					return
				}
				sh := targets[idx]
				item := contracts.StudioShotInspectionItem{
					ShotID:     sh.ID,
					Seq:        sh.Seq,
					Title:      sh.Title,
					VideoPath:  sh.VideoPath,
					SheetPaths: []string{},
					Frames:     []contracts.ContactSheetFrameMeta{},
					Signals: contracts.VideoSignals{
						Blacks:  []contracts.VideoSignalBlack{},
						Freezes: []contracts.VideoSignalFreeze{},
						Cuts:    []contracts.VideoSignalCut{},
					},
				}

				if cerr := engine.CheckFile(ctx, sh.VideoPath, false); cerr != nil {
					item.Error = cerr.Error()
					results[idx] = item
					continue
				}

				inBytes, merr := json.Marshal(contracts.VideoContactSheetInput{
					VideoPath:      engine.ImageInputRef{MediaRef: engine.RefFor(sh.VideoPath, engine.MediaVideo)},
					ShotID:         sh.ID,
					MaxFrames:      maxFrames,
					FramesPerSheet: perSheet,
					IncludeSignals: p.IncludeSignals,
					OutputDir:      thumbsDir,
				})
				if merr != nil {
					item.Error = merr.Error()
					results[idx] = item
					continue
				}

				rawOut, rerr := s.wf.RunNode(ctx, contracts.NodeTypeVideoContactSheet, inBytes)
				if rerr != nil {
					item.Error = rerr.Error()
					results[idx] = item
				} else {
					var out contracts.VideoContactSheetOutput
					if derr := decodeNodeOutput(rawOut, &out); derr != nil {
						item.Error = derr.Error()
					} else {
						item.SheetPaths = out.SheetPaths
						item.Signals = out.Signals
						item.Frames = out.Frames
						item.Summary = out.Summary
						for _, sp := range out.SheetPaths {
							if aid := s.registerMedia(ctx, "image", contracts.NodeTypeVideoContactSheet, sp, thumbsDir, 0); aid != "" {
								item.SheetAssetIDs = append(item.SheetAssetIDs, aid)
							}
						}
					}
					results[idx] = item
				}

				doneMu.Lock()
				completed++
				cur := completed
				doneMu.Unlock()
				progress.Report(ctx, progress.Update{
					Percent: float64(cur) / float64(max(1, total)) * 100.0,
					Message: fmt.Sprintf("Inspected shot %d/%d…", cur, total),
					Key:     "studio.progress.contact_sheet",
					Params: map[string]string{
						"current": strconv.Itoa(cur),
						"total":   strconv.Itoa(total),
					},
				})
			}
		})
	}
	wg.Wait()

	if ctx.Err() != nil {
		return contracts.StudioInspectShotsResult{}, ctx.Err()
	}

	okCount := 0
	sheetTotal := 0
	for _, it := range results {
		if it.Error == "" {
			okCount++
		}
		sheetTotal += len(it.SheetPaths)
	}
	return contracts.StudioInspectShotsResult{
		EpisodeID: ep.ID,
		Shots:     results,
		Summary:   fmt.Sprintf("Inspected %d/%d shots (%d contact sheet(s) generated)", okCount, total, sheetTotal),
	}, nil
}
