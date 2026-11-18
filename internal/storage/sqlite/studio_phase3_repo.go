package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/google/uuid"

	"app/internal/apperr"
	"app/internal/domain/studio"
)

type rowQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func revisionConflictErr(expected, current int) error {
	return apperr.New(
		apperr.CodeConflict,
		fmt.Sprintf("Timeline revision conflict: expected %d, current %d", expected, current),
	).WithDetails(map[string]any{
		"expected_revision": expected,
		"current_revision":  current,
	}).WithMsg("errors.studio.timeline_revision_conflict", map[string]any{
		"expected": expected,
		"current":  current,
	})
}

func (r *StudioRepository) defaultTimelineForEpisode(ctx context.Context, q rowQueryer, epID string) (studio.EditTimeline, bool, error) {
	var aspectRatio string
	err := q.QueryRowContext(ctx, `
		SELECT COALESCE(p.aspect_ratio, '')
		FROM studio_episodes e
		LEFT JOIN studio_projects p ON p.id = e.project_id
		WHERE e.id = ?`, epID).Scan(&aspectRatio)
	if errors.Is(err, sql.ErrNoRows) {
		return studio.EditTimeline{}, false, nil
	}
	if err != nil {
		return studio.EditTimeline{}, false, err
	}
	res := studio.ResolutionForAspectRatio(aspectRatio, r.cfg.DefaultResolution)
	w, h := studio.ParseResolutionDims(res, studio.BaselineVideoWidth, studio.BaselineVideoHeight)
	return studio.DefaultEditTimelineWithBGM(w, h, r.cfg.DefaultFPS, r.cfg.TimelineDefaultBGMVolume), true, nil
}

func (r *StudioRepository) timelineUndoRedoFlags(ctx context.Context, q rowQueryer, episodeID string, currentRev int) (canUndo, canRedo bool, err error) {
	if currentRev < 0 {
		return false, false, nil
	}
	var redoCnt int
	if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM studio_timeline_revisions WHERE episode_id = ? AND revision > ?`, episodeID, currentRev).Scan(&redoCnt); err != nil {
		return false, false, err
	}
	if currentRev == 0 {
		return false, redoCnt > 0, nil
	}
	if currentRev == 1 {
		return true, redoCnt > 0, nil
	}
	var undoCnt int
	if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM studio_timeline_revisions WHERE episode_id = ? AND revision < ?`, episodeID, currentRev).Scan(&undoCnt); err != nil {
		return false, false, err
	}
	return undoCnt > 0, redoCnt > 0, nil
}

// GetTimeline fetches the active EditTimeline and revision cursor for episodeID.
func (r *StudioRepository) GetTimeline(ctx context.Context, episodeID string) (studio.TimelineRecord, error) {
	epID := strings.TrimSpace(episodeID)
	if epID == "" {
		return studio.TimelineRecord{}, apperr.Validation("episode_id is required")
	}

	var rec studio.TimelineRecord
	rec.EpisodeID = epID
	err := r.db.read().QueryRowContext(ctx, `
		SELECT revision, timeline_json, updated_at
		FROM studio_timelines
		WHERE episode_id = ?`, epID).
		Scan(&rec.Revision, &rec.TimelineJSON, &rec.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		defTL, ok, derr := r.defaultTimelineForEpisode(ctx, r.db.read(), epID)
		if derr != nil {
			return studio.TimelineRecord{}, wrapStudioReadErr(derr, "verify studio episode")
		}
		if !ok {
			return studio.TimelineRecord{}, studio.ErrEpisodeNotFound(epID)
		}
		b, merr := json.Marshal(defTL)
		if merr != nil {
			return studio.TimelineRecord{}, fmt.Errorf("sqlite: marshal default timeline: %w", merr)
		}
		rec.Revision = 0
		rec.Timeline = defTL
		rec.TimelineJSON = string(b)
		return rec, nil
	}
	if err != nil {
		return studio.TimelineRecord{}, wrapStudioReadErr(err, "get studio timeline")
	}

	var tl studio.EditTimeline
	if jerr := json.Unmarshal([]byte(rec.TimelineJSON), &tl); jerr != nil {
		return studio.TimelineRecord{}, apperr.Wrap(apperr.CodeIntegrity, "Corrupted timeline JSON in database", jerr).
			WithMsg("errors.studio.corrupt_data", nil)
	}
	tl.Normalize()
	rec.Timeline = tl
	canUndo, canRedo, ferr := r.timelineUndoRedoFlags(ctx, r.db.read(), epID, rec.Revision)
	if ferr != nil {
		return studio.TimelineRecord{}, wrapStudioReadErr(ferr, "read timeline undo/redo flags")
	}
	rec.CanUndo = canUndo
	rec.CanRedo = canRedo
	return rec, nil
}

// SaveTimelineRevision atomically appends a timeline revision after verifying expectedRevision.
// Passing expectedRevision < 0 bypasses the revision match check (internal repair use only).
func (r *StudioRepository) SaveTimelineRevision(
	ctx context.Context,
	episodeID string,
	expectedRevision int,
	actor studio.TimelineActor,
	summary string,
	tl studio.EditTimeline,
) (studio.TimelineRecord, error) {
	epID := strings.TrimSpace(episodeID)
	if epID == "" {
		return studio.TimelineRecord{}, apperr.Validation("episode_id is required")
	}
	if err := tl.ValidateWithMaxDim(r.cfg.MaxVideoDimension); err != nil {
		return studio.TimelineRecord{}, err
	}
	if !actor.Valid() {
		actor = studio.ActorUser
	}
	rawBytes, err := json.Marshal(tl)
	if err != nil {
		return studio.TimelineRecord{}, fmt.Errorf("sqlite: marshal timeline: %w", err)
	}
	rawJSON := string(rawBytes)
	now := time.Now().UnixMilli()
	var out studio.TimelineRecord

	err = r.db.inTx(ctx, func(tx *sql.Tx) error {
		var exists int
		if qerr := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM studio_episodes WHERE id = ?`, epID).Scan(&exists); qerr != nil {
			return qerr
		}
		if exists == 0 {
			return studio.ErrEpisodeNotFound(epID)
		}

		currentRev := 0
		scanErr := tx.QueryRowContext(ctx, `SELECT revision FROM studio_timelines WHERE episode_id = ?`, epID).Scan(&currentRev)
		if scanErr != nil && !errors.Is(scanErr, sql.ErrNoRows) {
			return scanErr
		}

		if expectedRevision >= 0 && currentRev != expectedRevision {
			return revisionConflictErr(expectedRevision, currentRev)
		}

		if _, derr := tx.ExecContext(ctx, `DELETE FROM studio_timeline_revisions WHERE episode_id = ? AND revision > ?`, epID, currentRev); derr != nil {
			return derr
		}

		nextRev := currentRev + 1
		if _, ierr := tx.ExecContext(ctx, `
			INSERT INTO studio_timeline_revisions (id, episode_id, revision, actor, summary, timeline_json, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)`,
			uuid.NewString(), epID, nextRev, string(actor), strings.TrimSpace(summary), rawJSON, now,
		); ierr != nil {
			return ierr
		}

		if r.cfg.TimelineRevisionLimit > 0 {
			if _, perr := tx.ExecContext(ctx, `
				DELETE FROM studio_timeline_revisions
				WHERE episode_id = ? AND id NOT IN (
					SELECT id FROM studio_timeline_revisions
					WHERE episode_id = ?
					ORDER BY revision DESC
					LIMIT ?
				)`, epID, epID, r.cfg.TimelineRevisionLimit); perr != nil {
				return perr
			}
		}

		if _, uerr := tx.ExecContext(ctx, `
			INSERT INTO studio_timelines (episode_id, revision, timeline_json, updated_at)
			VALUES (?, ?, ?, ?)
			ON CONFLICT(episode_id) DO UPDATE SET
				revision = excluded.revision,
				timeline_json = excluded.timeline_json,
				updated_at = excluded.updated_at`,
			epID, nextRev, rawJSON, now,
		); uerr != nil {
			return uerr
		}

		canUndo, canRedo, ferr := r.timelineUndoRedoFlags(ctx, tx, epID, nextRev)
		if ferr != nil {
			return ferr
		}
		out = studio.TimelineRecord{
			EpisodeID:    epID,
			Revision:     nextRev,
			CanUndo:      canUndo,
			CanRedo:      canRedo,
			Timeline:     tl,
			TimelineJSON: rawJSON,
			UpdatedAt:    now,
		}
		return nil
	})
	if err != nil {
		return studio.TimelineRecord{}, wrapStudioWriteErr(err, "save timeline revision", nil, nil)
	}
	return out, nil
}

// UndoTimeline moves the active timeline cursor back one saved revision (including 1 -> 0 initial baseline) while checking expectedRevision.
func (r *StudioRepository) UndoTimeline(ctx context.Context, episodeID string, expectedRevision int) (studio.TimelineRecord, error) {
	return r.stepTimelineRevision(ctx, episodeID, expectedRevision, -1)
}

// RedoTimeline advances the active timeline cursor forward one saved revision while checking expectedRevision.
func (r *StudioRepository) RedoTimeline(ctx context.Context, episodeID string, expectedRevision int) (studio.TimelineRecord, error) {
	return r.stepTimelineRevision(ctx, episodeID, expectedRevision, +1)
}

func (r *StudioRepository) stepTimelineRevision(ctx context.Context, episodeID string, expectedRevision int, dir int) (studio.TimelineRecord, error) {
	epID := strings.TrimSpace(episodeID)
	if epID == "" {
		return studio.TimelineRecord{}, apperr.Validation("episode_id is required")
	}
	now := time.Now().UnixMilli()
	var out studio.TimelineRecord

	err := r.db.inTx(ctx, func(tx *sql.Tx) error {
		var currentRev int
		err := tx.QueryRowContext(ctx, `SELECT revision FROM studio_timelines WHERE episode_id = ?`, epID).Scan(&currentRev)
		if errors.Is(err, sql.ErrNoRows) {
			if expectedRevision > 0 {
				return revisionConflictErr(expectedRevision, 0)
			}
			return apperr.Validation("no timeline revisions exist yet").
				WithMsg("errors.studio.timeline_no_revisions", nil)
		}
		if err != nil {
			return err
		}
		if expectedRevision >= 0 && currentRev != expectedRevision {
			return revisionConflictErr(expectedRevision, currentRev)
		}

		var targetRev int
		var targetJSON string
		var tl studio.EditTimeline

		if dir < 0 && currentRev <= 0 {
			return apperr.Validation("already at earliest timeline revision").
				WithMsg("errors.studio.timeline_nothing_to_undo", nil)
		}

		var q string
		if dir < 0 {
			q = `SELECT revision, timeline_json FROM studio_timeline_revisions WHERE episode_id = ? AND revision < ? ORDER BY revision DESC LIMIT 1`
		} else {
			q = `SELECT revision, timeline_json FROM studio_timeline_revisions WHERE episode_id = ? AND revision > ? ORDER BY revision ASC LIMIT 1`
		}
		err = tx.QueryRowContext(ctx, q, epID, currentRev).Scan(&targetRev, &targetJSON)
		if errors.Is(err, sql.ErrNoRows) {
			if dir < 0 && currentRev == 1 {
				defTL, ok, derr := r.defaultTimelineForEpisode(ctx, tx, epID)
				if derr != nil {
					return derr
				}
				if !ok {
					return studio.ErrEpisodeNotFound(epID)
				}
				rawBytes, merr := json.Marshal(defTL)
				if merr != nil {
					return merr
				}
				targetRev = 0
				tl = defTL
				targetJSON = string(rawBytes)
			} else if dir < 0 {
				return apperr.Validation("already at earliest timeline revision").
					WithMsg("errors.studio.timeline_nothing_to_undo", nil)
			} else {
				return apperr.Validation("already at latest timeline revision").
					WithMsg("errors.studio.timeline_nothing_to_redo", nil)
			}
		} else if err != nil {
			return err
		} else {
			if jerr := json.Unmarshal([]byte(targetJSON), &tl); jerr != nil {
				return apperr.Wrap(apperr.CodeIntegrity, "Corrupted revision timeline JSON in database", jerr).
					WithMsg("errors.studio.corrupt_data", nil)
			}
			tl.Normalize()
		}

		if _, uerr := tx.ExecContext(ctx, `
			UPDATE studio_timelines SET revision = ?, timeline_json = ?, updated_at = ? WHERE episode_id = ?`,
			targetRev, targetJSON, now, epID,
		); uerr != nil {
			return uerr
		}

		canUndo, canRedo, ferr := r.timelineUndoRedoFlags(ctx, tx, epID, targetRev)
		if ferr != nil {
			return ferr
		}
		out = studio.TimelineRecord{
			EpisodeID:    epID,
			Revision:     targetRev,
			CanUndo:      canUndo,
			CanRedo:      canRedo,
			Timeline:     tl,
			TimelineJSON: targetJSON,
			UpdatedAt:    now,
		}
		return nil
	})
	if err != nil {
		return studio.TimelineRecord{}, wrapStudioWriteErr(err, "step timeline revision", nil, nil)
	}
	return out, nil
}

// UpdateTimelineClipCache updates derived waveform peaks and thumbnail sprite metadata on clipID in-place
// without incrementing the timeline revision cursor or pruning the Redo stack.
// Returns (nil, nil) if the episode has no persisted timeline yet or clipID is not present in the active timeline.
func (r *StudioRepository) UpdateTimelineClipCache(
	ctx context.Context,
	episodeID, clipID string,
	peaks []float64,
	spritePath, spriteAssetID string,
) (*studio.TimelineRecord, error) {
	epID := strings.TrimSpace(episodeID)
	cID := strings.TrimSpace(clipID)
	if epID == "" || cID == "" {
		return nil, apperr.Validation("episode_id and clip_id are required")
	}
	for _, pk := range peaks {
		if math.IsNaN(pk) || math.IsInf(pk, 0) {
			return nil, apperr.Validation("peaks contain non-finite float").
				WithMsg("errors.studio.timeline_peaks_invalid", map[string]any{"id": cID})
		}
	}

	now := time.Now().UnixMilli()
	var out *studio.TimelineRecord

	err := r.db.inTx(ctx, func(tx *sql.Tx) error {
		var exists int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM studio_episodes WHERE id = ?`, epID).Scan(&exists); err != nil {
			return err
		}
		if exists == 0 {
			return studio.ErrEpisodeNotFound(epID)
		}

		var currentRev int
		var rawTimeline string
		err := tx.QueryRowContext(ctx, `SELECT revision, timeline_json FROM studio_timelines WHERE episode_id = ?`, epID).Scan(&currentRev, &rawTimeline)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}

		var tl studio.EditTimeline
		if jerr := json.Unmarshal([]byte(rawTimeline), &tl); jerr != nil {
			return apperr.Wrap(apperr.CodeIntegrity, "Corrupted timeline JSON in database", jerr).
				WithMsg("errors.studio.corrupt_data", nil)
		}
		tl.Normalize()

		updated := false
		cleanSprite := strings.TrimSpace(spritePath)
		cleanAsset := strings.TrimSpace(spriteAssetID)
		for ti := range tl.Tracks {
			for ci := range tl.Tracks[ti].Clips {
				if tl.Tracks[ti].Clips[ci].ID != cID {
					continue
				}
				if peaks != nil {
					tl.Tracks[ti].Clips[ci].Peaks = append([]float64(nil), peaks...)
				}
				if cleanSprite != "" {
					tl.Tracks[ti].Clips[ci].SpritePath = cleanSprite
				}
				if cleanAsset != "" {
					tl.Tracks[ti].Clips[ci].SpriteAssetID = cleanAsset
				}
				updated = true
			}
		}
		if !updated {
			return nil
		}

		rawBytes, merr := json.Marshal(tl)
		if merr != nil {
			return merr
		}
		rawJSON := string(rawBytes)

		if _, err := tx.ExecContext(ctx, `
			UPDATE studio_timelines SET timeline_json = ?, updated_at = ? WHERE episode_id = ?`,
			rawJSON, now, epID,
		); err != nil {
			return err
		}
		if currentRev > 0 {
			if _, err := tx.ExecContext(ctx, `
				UPDATE studio_timeline_revisions SET timeline_json = ? WHERE episode_id = ? AND revision = ?`,
				rawJSON, epID, currentRev,
			); err != nil {
				return err
			}
		}

		canUndo, canRedo, ferr := r.timelineUndoRedoFlags(ctx, tx, epID, currentRev)
		if ferr != nil {
			return ferr
		}
		out = &studio.TimelineRecord{
			EpisodeID:    epID,
			Revision:     currentRev,
			CanUndo:      canUndo,
			CanRedo:      canRedo,
			Timeline:     tl,
			TimelineJSON: rawJSON,
			UpdatedAt:    now,
		}
		return nil
	})
	if err != nil {
		return nil, wrapStudioWriteErr(err, "update timeline clip cache", nil, nil)
	}
	return out, nil
}

// ListTimelineRevisions returns recorded timeline revision summaries ordered newest-first.
func (r *StudioRepository) ListTimelineRevisions(ctx context.Context, episodeID string, limit int) ([]studio.TimelineRevision, error) {
	epID := strings.TrimSpace(episodeID)
	if epID == "" {
		return nil, apperr.Validation("episode_id is required")
	}
	if limit <= 0 {
		if r.cfg.TimelineRevisionLimit > 0 {
			limit = r.cfg.TimelineRevisionLimit
		} else {
			limit = studio.DefaultRevisionHistoryLimit
		}
	}
	rows, err := r.db.read().QueryContext(ctx, `
		SELECT id, episode_id, revision, actor, summary, timeline_json, created_at
		FROM studio_timeline_revisions
		WHERE episode_id = ?
		ORDER BY revision DESC
		LIMIT ?`, epID, limit)
	if err != nil {
		return nil, wrapStudioReadErr(err, "list timeline revisions")
	}
	defer rows.Close()
	out := []studio.TimelineRevision{}
	for rows.Next() {
		var rev studio.TimelineRevision
		var actor string
		if err := rows.Scan(&rev.ID, &rev.EpisodeID, &rev.Revision, &actor, &rev.Summary, &rev.TimelineJSON, &rev.CreatedAt); err != nil {
			return nil, wrapStudioReadErr(err, "scan timeline revision")
		}
		rev.Actor = studio.TimelineActor(actor)
		out = append(out, rev)
	}
	return out, wrapStudioReadErr(rows.Err(), "iterate timeline revisions")
}
