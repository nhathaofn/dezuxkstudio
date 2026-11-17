package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"app/internal/apperr"
	"app/internal/domain/studio"
)

func revisionConflictErr(expected, current int) error {
	return apperr.New(
		apperr.CodeConflict,
		fmt.Sprintf("Timeline revision conflict: expected %d, current %d", expected, current),
	).WithDetails(map[string]any{
		"expected_revision": expected,
		"current_revision":  current,
	})
}

func (r *StudioRepository) timelineUndoRedoFlags(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, episodeID string, currentRev int) (canUndo, canRedo bool) {
	if currentRev <= 0 {
		return false, false
	}
	var undoCnt, redoCnt int
	_ = q.QueryRowContext(ctx, `SELECT COUNT(*) FROM studio_timeline_revisions WHERE episode_id = ? AND revision < ?`, episodeID, currentRev).Scan(&undoCnt)
	_ = q.QueryRowContext(ctx, `SELECT COUNT(*) FROM studio_timeline_revisions WHERE episode_id = ? AND revision > ?`, episodeID, currentRev).Scan(&redoCnt)
	return undoCnt > 0, redoCnt > 0
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
		var exists int
		if qerr := r.db.read().QueryRowContext(ctx, `SELECT COUNT(*) FROM studio_episodes WHERE id = ?`, epID).Scan(&exists); qerr != nil {
			return studio.TimelineRecord{}, fmt.Errorf("sqlite: verify studio episode: %w", qerr)
		}
		if exists == 0 {
			return studio.TimelineRecord{}, studio.ErrEpisodeNotFound(epID)
		}
		defTL := studio.DefaultEditTimeline(studio.BaselineVideoWidth, studio.BaselineVideoHeight, studio.BaselineVideoFPS)
		b, _ := json.Marshal(defTL)
		rec.Revision = 0
		rec.Timeline = defTL
		rec.TimelineJSON = string(b)
		return rec, nil
	}
	if err != nil {
		return studio.TimelineRecord{}, fmt.Errorf("sqlite: get studio timeline: %w", err)
	}

	var tl studio.EditTimeline
	if jerr := json.Unmarshal([]byte(rec.TimelineJSON), &tl); jerr != nil {
		return studio.TimelineRecord{}, apperr.New(apperr.CodeIntegrity, "corrupted timeline JSON in database")
	}
	tl.Normalize()
	rec.Timeline = tl
	rec.CanUndo, rec.CanRedo = r.timelineUndoRedoFlags(ctx, r.db.read(), epID, rec.Revision)
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
	if err := tl.Validate(); err != nil {
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
			return fmt.Errorf("sqlite: check episode for timeline: %w", qerr)
		}
		if exists == 0 {
			return studio.ErrEpisodeNotFound(epID)
		}

		currentRev := 0
		scanErr := tx.QueryRowContext(ctx, `SELECT revision FROM studio_timelines WHERE episode_id = ?`, epID).Scan(&currentRev)
		if scanErr != nil && !errors.Is(scanErr, sql.ErrNoRows) {
			return fmt.Errorf("sqlite: query timeline revision: %w", scanErr)
		}

		if expectedRevision >= 0 && currentRev != expectedRevision {
			return revisionConflictErr(expectedRevision, currentRev)
		}

		if _, derr := tx.ExecContext(ctx, `DELETE FROM studio_timeline_revisions WHERE episode_id = ? AND revision > ?`, epID, currentRev); derr != nil {
			return fmt.Errorf("sqlite: prune redo timeline revisions: %w", derr)
		}

		nextRev := currentRev + 1
		if _, ierr := tx.ExecContext(ctx, `
			INSERT INTO studio_timeline_revisions (id, episode_id, revision, actor, summary, timeline_json, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)`,
			uuid.NewString(), epID, nextRev, string(actor), strings.TrimSpace(summary), rawJSON, now,
		); ierr != nil {
			return fmt.Errorf("sqlite: insert timeline revision: %w", ierr)
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
			return fmt.Errorf("sqlite: upsert studio_timelines: %w", uerr)
		}

		canUndo, canRedo := r.timelineUndoRedoFlags(ctx, tx, epID, nextRev)
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
		return studio.TimelineRecord{}, err
	}
	return out, nil
}

// UndoTimeline moves the active timeline cursor back one saved revision while checking expectedRevision.
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
			return apperr.Validation("no timeline revisions exist yet")
		}
		if err != nil {
			return fmt.Errorf("sqlite: read timeline cursor: %w", err)
		}
		if expectedRevision >= 0 && currentRev != expectedRevision {
			return revisionConflictErr(expectedRevision, currentRev)
		}

		var targetRev int
		var targetJSON string
		var q string
		if dir < 0 {
			q = `SELECT revision, timeline_json FROM studio_timeline_revisions WHERE episode_id = ? AND revision < ? ORDER BY revision DESC LIMIT 1`
		} else {
			q = `SELECT revision, timeline_json FROM studio_timeline_revisions WHERE episode_id = ? AND revision > ? ORDER BY revision ASC LIMIT 1`
		}
		err = tx.QueryRowContext(ctx, q, epID, currentRev).Scan(&targetRev, &targetJSON)
		if errors.Is(err, sql.ErrNoRows) {
			if dir < 0 {
				return apperr.Validation("already at earliest timeline revision")
			}
			return apperr.Validation("already at latest timeline revision")
		}
		if err != nil {
			return fmt.Errorf("sqlite: load target timeline revision: %w", err)
		}

		var tl studio.EditTimeline
		if jerr := json.Unmarshal([]byte(targetJSON), &tl); jerr != nil {
			return apperr.New(apperr.CodeIntegrity, "corrupted revision timeline JSON")
		}
		tl.Normalize()

		if _, uerr := tx.ExecContext(ctx, `
			UPDATE studio_timelines SET revision = ?, timeline_json = ?, updated_at = ? WHERE episode_id = ?`,
			targetRev, targetJSON, now, epID,
		); uerr != nil {
			return fmt.Errorf("sqlite: update timeline cursor: %w", uerr)
		}

		canUndo, canRedo := r.timelineUndoRedoFlags(ctx, tx, epID, targetRev)
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
		return studio.TimelineRecord{}, err
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
		limit = studio.DefaultRevisionHistoryLimit
	}
	rows, err := r.db.read().QueryContext(ctx, `
		SELECT id, episode_id, revision, actor, summary, timeline_json, created_at
		FROM studio_timeline_revisions
		WHERE episode_id = ?
		ORDER BY revision DESC
		LIMIT ?`, epID, limit)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list timeline revisions: %w", err)
	}
	defer rows.Close()
	out := []studio.TimelineRevision{}
	for rows.Next() {
		var rev studio.TimelineRevision
		var actor string
		if err := rows.Scan(&rev.ID, &rev.EpisodeID, &rev.Revision, &actor, &rev.Summary, &rev.TimelineJSON, &rev.CreatedAt); err != nil {
			return nil, fmt.Errorf("sqlite: scan timeline revision: %w", err)
		}
		rev.Actor = studio.TimelineActor(actor)
		out = append(out, rev)
	}
	return out, rows.Err()
}
