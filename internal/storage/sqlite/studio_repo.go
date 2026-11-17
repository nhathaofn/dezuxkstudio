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

// StudioRepository implements studio.Repository on SQLite.
type StudioRepository struct{ db *DB }

var _ studio.Repository = (*StudioRepository)(nil)

// NewStudioRepository constructs a StudioRepository.
func NewStudioRepository(db *DB) *StudioRepository { return &StudioRepository{db: db} }

const projectCols = `id, slug, title, genre, production_mode, aspect_ratio, style_prompt, root_dir,
	auto_approve_batches, default_text_model, default_image_model, default_video_model, default_tts_model,
	created_at, updated_at`

const episodeCols = `id, project_id, code, title, stage, raw_script_path, script_json_path,
	assets_json_path, shots_json_path, timeline_json_path, created_at, updated_at`

const assetCols = `id, project_id, kind, name, code_tag, description, prompt,
	primary_asset_id, primary_path, voice_provider, voice_id, voice_style, updated_at`

const shotCols = `id, episode_id, seq, scene_code, title, shot_mode, duration_sec,
	image_prompt, video_prompt, narration_text, dialogue_json, ref_asset_tags_json,
	keyframe_asset_id, keyframe_path, video_asset_id, video_path, audio_asset_id, audio_path,
	status, error_text, updated_at`

func scanProject(s rowScanner) (studio.Project, error) {
	var p studio.Project
	var autoApprove int
	if err := s.Scan(
		&p.ID, &p.Slug, &p.Title, &p.Genre, &p.ProductionMode, &p.AspectRatio, &p.StylePrompt, &p.RootDir,
		&autoApprove, &p.DefaultTextModel, &p.DefaultImageModel, &p.DefaultVideoModel, &p.DefaultTTSModel,
		&p.CreatedAt, &p.UpdatedAt,
	); err != nil {
		return studio.Project{}, err
	}
	p.AutoApproveBatches = autoApprove == 1
	return p, nil
}

func scanEpisode(s rowScanner) (studio.Episode, error) {
	var e studio.Episode
	if err := s.Scan(
		&e.ID, &e.ProjectID, &e.Code, &e.Title, &e.Stage, &e.RawScriptPath, &e.ScriptJSONPath,
		&e.AssetsJSONPath, &e.ShotsJSONPath, &e.TimelineJSONPath, &e.CreatedAt, &e.UpdatedAt,
	); err != nil {
		return studio.Episode{}, err
	}
	return e, nil
}

func scanAsset(s rowScanner) (studio.AssetEntry, error) {
	var a studio.AssetEntry
	if err := s.Scan(
		&a.ID, &a.ProjectID, &a.Kind, &a.Name, &a.CodeTag, &a.Description, &a.Prompt,
		&a.PrimaryAssetID, &a.PrimaryPath, &a.VoiceProvider, &a.VoiceID, &a.VoiceStyle, &a.UpdatedAt,
	); err != nil {
		return studio.AssetEntry{}, err
	}
	return a, nil
}

func scanShot(s rowScanner) (studio.Shot, error) {
	var sh studio.Shot
	var dlgJSON, tagsJSON string
	if err := s.Scan(
		&sh.ID, &sh.EpisodeID, &sh.Seq, &sh.SceneCode, &sh.Title, &sh.ShotMode, &sh.DurationSec,
		&sh.ImagePrompt, &sh.VideoPrompt, &sh.NarrationText, &dlgJSON, &tagsJSON,
		&sh.KeyframeAssetID, &sh.KeyframePath, &sh.VideoAssetID, &sh.VideoPath, &sh.AudioAssetID, &sh.AudioPath,
		&sh.Status, &sh.ErrorText, &sh.UpdatedAt,
	); err != nil {
		return studio.Shot{}, err
	}
	if strings.TrimSpace(dlgJSON) != "" {
		_ = json.Unmarshal([]byte(dlgJSON), &sh.Dialogues)
	}
	if strings.TrimSpace(tagsJSON) != "" {
		_ = json.Unmarshal([]byte(tagsJSON), &sh.RefAssetTags)
	}
	return sh, nil
}

func isConstraintErr(err error) bool {
	return err != nil && strings.Contains(strings.ToUpper(err.Error()), "CONSTRAINT")
}

// ListProjects returns all studio projects ordered by most recently updated.
func (r *StudioRepository) ListProjects(ctx context.Context) ([]studio.Project, error) {
	rows, err := r.db.read().QueryContext(ctx,
		`SELECT `+projectCols+` FROM studio_projects ORDER BY updated_at DESC, created_at DESC, id ASC`)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list studio projects: %w", err)
	}
	defer rows.Close()
	out := []studio.Project{}
	for rows.Next() {
		p, err := scanProject(rows)
		if err != nil {
			return nil, fmt.Errorf("sqlite: scan studio project: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// GetProject retrieves a project by ID or slug.
func (r *StudioRepository) GetProject(ctx context.Context, idOrSlug string) (studio.Project, error) {
	key := strings.TrimSpace(idOrSlug)
	p, err := scanProject(r.db.read().QueryRowContext(ctx,
		`SELECT `+projectCols+` FROM studio_projects WHERE id = ? OR slug = ? LIMIT 1`, key, key))
	if errors.Is(err, sql.ErrNoRows) {
		return studio.Project{}, studio.ErrProjectNotFound(idOrSlug)
	}
	if err != nil {
		return studio.Project{}, fmt.Errorf("sqlite: get studio project: %w", err)
	}
	return p, nil
}

// CreateProject validates and inserts a new project.
func (r *StudioRepository) CreateProject(ctx context.Context, p studio.Project) (studio.Project, error) {
	if err := p.Validate(); err != nil {
		return studio.Project{}, err
	}
	if p.ID == "" {
		p.ID = uuid.NewString()
	}
	now := time.Now().UnixMilli()
	if p.CreatedAt <= 0 {
		p.CreatedAt = now
	}
	p.UpdatedAt = now

	_, err := r.db.sql.ExecContext(ctx, `
		INSERT INTO studio_projects (`+projectCols+`)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		p.ID, p.Slug, p.Title, string(p.Genre), string(p.ProductionMode), p.AspectRatio, p.StylePrompt, p.RootDir,
		boolInt(p.AutoApproveBatches), p.DefaultTextModel, p.DefaultImageModel, p.DefaultVideoModel, p.DefaultTTSModel,
		p.CreatedAt, p.UpdatedAt,
	)
	if isConstraintErr(err) {
		return studio.Project{}, apperr.New(apperr.CodeConflict, fmt.Sprintf("Project slug %q already exists", p.Slug)).
			WithMsg("errors.studio.duplicate_slug", map[string]any{"slug": p.Slug})
	}
	if err != nil {
		return studio.Project{}, fmt.Errorf("sqlite: create studio project: %w", err)
	}
	return p, nil
}

// UpdateProject updates mutable fields of an existing project.
func (r *StudioRepository) UpdateProject(ctx context.Context, p studio.Project) (studio.Project, error) {
	if err := p.Validate(); err != nil {
		return studio.Project{}, err
	}
	p.UpdatedAt = time.Now().UnixMilli()
	res, err := r.db.sql.ExecContext(ctx, `
		UPDATE studio_projects SET
			slug = ?, title = ?, genre = ?, production_mode = ?, aspect_ratio = ?, style_prompt = ?,
			root_dir = ?, auto_approve_batches = ?, default_text_model = ?, default_image_model = ?,
			default_video_model = ?, default_tts_model = ?, updated_at = ?
		WHERE id = ?`,
		p.Slug, p.Title, string(p.Genre), string(p.ProductionMode), p.AspectRatio, p.StylePrompt,
		p.RootDir, boolInt(p.AutoApproveBatches), p.DefaultTextModel, p.DefaultImageModel,
		p.DefaultVideoModel, p.DefaultTTSModel, p.UpdatedAt, p.ID,
	)
	if isConstraintErr(err) {
		return studio.Project{}, apperr.New(apperr.CodeConflict, fmt.Sprintf("Project slug %q already exists", p.Slug)).
			WithMsg("errors.studio.duplicate_slug", map[string]any{"slug": p.Slug})
	}
	if err != nil {
		return studio.Project{}, fmt.Errorf("sqlite: update studio project: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return studio.Project{}, studio.ErrProjectNotFound(p.ID)
	}
	return r.GetProject(ctx, p.ID)
}

// DeleteProject deletes a project and cascades to episodes, assets, and shots.
func (r *StudioRepository) DeleteProject(ctx context.Context, id string) error {
	res, err := r.db.sql.ExecContext(ctx, `DELETE FROM studio_projects WHERE id = ?`, strings.TrimSpace(id))
	if err != nil {
		return fmt.Errorf("sqlite: delete studio project: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return studio.ErrProjectNotFound(id)
	}
	return nil
}

// ListEpisodes returns all episodes of a project ordered by code/creation time.
func (r *StudioRepository) ListEpisodes(ctx context.Context, projectID string) ([]studio.Episode, error) {
	rows, err := r.db.read().QueryContext(ctx,
		`SELECT `+episodeCols+` FROM studio_episodes WHERE project_id = ? ORDER BY created_at ASC, code ASC`,
		strings.TrimSpace(projectID))
	if err != nil {
		return nil, fmt.Errorf("sqlite: list studio episodes: %w", err)
	}
	defer rows.Close()
	out := []studio.Episode{}
	for rows.Next() {
		ep, err := scanEpisode(rows)
		if err != nil {
			return nil, fmt.Errorf("sqlite: scan studio episode: %w", err)
		}
		out = append(out, ep)
	}
	return out, rows.Err()
}

// GetEpisode fetches an episode by ID.
func (r *StudioRepository) GetEpisode(ctx context.Context, id string) (studio.Episode, error) {
	ep, err := scanEpisode(r.db.read().QueryRowContext(ctx,
		`SELECT `+episodeCols+` FROM studio_episodes WHERE id = ?`, strings.TrimSpace(id)))
	if errors.Is(err, sql.ErrNoRows) {
		return studio.Episode{}, studio.ErrEpisodeNotFound(id)
	}
	if err != nil {
		return studio.Episode{}, fmt.Errorf("sqlite: get studio episode: %w", err)
	}
	return ep, nil
}

// CreateEpisode inserts a new episode for a project.
func (r *StudioRepository) CreateEpisode(ctx context.Context, ep studio.Episode) (studio.Episode, error) {
	if err := ep.Validate(); err != nil {
		return studio.Episode{}, err
	}
	if ep.ID == "" {
		ep.ID = uuid.NewString()
	}
	now := time.Now().UnixMilli()
	if ep.CreatedAt <= 0 {
		ep.CreatedAt = now
	}
	ep.UpdatedAt = now
	_, err := r.db.sql.ExecContext(ctx, `
		INSERT INTO studio_episodes (`+episodeCols+`)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		ep.ID, ep.ProjectID, ep.Code, ep.Title, string(ep.Stage), ep.RawScriptPath, ep.ScriptJSONPath,
		ep.AssetsJSONPath, ep.ShotsJSONPath, ep.TimelineJSONPath, ep.CreatedAt, ep.UpdatedAt,
	)
	if isConstraintErr(err) {
		return studio.Episode{}, apperr.New(apperr.CodeConflict, fmt.Sprintf("Episode %q already exists in project", ep.Code))
	}
	if err != nil {
		return studio.Episode{}, fmt.Errorf("sqlite: create studio episode: %w", err)
	}
	return ep, nil
}

// UpdateEpisode updates an existing episode.
func (r *StudioRepository) UpdateEpisode(ctx context.Context, ep studio.Episode) (studio.Episode, error) {
	if err := ep.Validate(); err != nil {
		return studio.Episode{}, err
	}
	ep.UpdatedAt = time.Now().UnixMilli()
	res, err := r.db.sql.ExecContext(ctx, `
		UPDATE studio_episodes SET
			code = ?, title = ?, stage = ?, raw_script_path = ?, script_json_path = ?,
			assets_json_path = ?, shots_json_path = ?, timeline_json_path = ?, updated_at = ?
		WHERE id = ?`,
		ep.Code, ep.Title, string(ep.Stage), ep.RawScriptPath, ep.ScriptJSONPath,
		ep.AssetsJSONPath, ep.ShotsJSONPath, ep.TimelineJSONPath, ep.UpdatedAt, ep.ID,
	)
	if err != nil {
		return studio.Episode{}, fmt.Errorf("sqlite: update studio episode: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return studio.Episode{}, studio.ErrEpisodeNotFound(ep.ID)
	}
	return r.GetEpisode(ctx, ep.ID)
}

// ListAssets returns all asset bible entries for a project ordered by kind and code_tag.
func (r *StudioRepository) ListAssets(ctx context.Context, projectID string) ([]studio.AssetEntry, error) {
	rows, err := r.db.read().QueryContext(ctx,
		`SELECT `+assetCols+` FROM studio_assets WHERE project_id = ? ORDER BY kind ASC, code_tag ASC`,
		strings.TrimSpace(projectID))
	if err != nil {
		return nil, fmt.Errorf("sqlite: list studio assets: %w", err)
	}
	defer rows.Close()
	out := []studio.AssetEntry{}
	for rows.Next() {
		a, err := scanAsset(rows)
		if err != nil {
			return nil, fmt.Errorf("sqlite: scan studio asset: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// GetAsset fetches one studio asset entry by ID.
func (r *StudioRepository) GetAsset(ctx context.Context, id string) (studio.AssetEntry, error) {
	a, err := scanAsset(r.db.read().QueryRowContext(ctx,
		`SELECT `+assetCols+` FROM studio_assets WHERE id = ?`, strings.TrimSpace(id)))
	if errors.Is(err, sql.ErrNoRows) {
		return studio.AssetEntry{}, studio.ErrAssetNotFound(id)
	}
	if err != nil {
		return studio.AssetEntry{}, fmt.Errorf("sqlite: get studio asset: %w", err)
	}
	return a, nil
}

// UpsertAsset inserts or updates an asset bible entry (matching by id or project_id+code_tag).
func (r *StudioRepository) UpsertAsset(ctx context.Context, a studio.AssetEntry) (studio.AssetEntry, error) {
	if err := a.Validate(); err != nil {
		return studio.AssetEntry{}, err
	}
	if a.ID == "" {
		a.ID = uuid.NewString()
	}
	a.UpdatedAt = time.Now().UnixMilli()

	err := r.db.inTx(ctx, func(tx *sql.Tx) error {
		var existingID string
		err := tx.QueryRowContext(ctx, `SELECT id FROM studio_assets WHERE id = ?`, a.ID).Scan(&existingID)
		if errors.Is(err, sql.ErrNoRows) {
			err = tx.QueryRowContext(ctx, `SELECT id FROM studio_assets WHERE project_id = ? AND code_tag = ?`, a.ProjectID, a.CodeTag).Scan(&existingID)
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("sqlite: lookup studio asset: %w", err)
		}
		if existingID != "" {
			a.ID = existingID
			_, err = tx.ExecContext(ctx, `
				UPDATE studio_assets SET
					kind = ?, name = ?, code_tag = ?, description = ?, prompt = ?,
					primary_asset_id = ?, primary_path = ?, voice_provider = ?, voice_id = ?, voice_style = ?, updated_at = ?
				WHERE id = ?`,
				string(a.Kind), a.Name, a.CodeTag, a.Description, a.Prompt,
				a.PrimaryAssetID, a.PrimaryPath, a.VoiceProvider, a.VoiceID, a.VoiceStyle, a.UpdatedAt, a.ID,
			)
			if err != nil {
				return fmt.Errorf("sqlite: update studio asset: %w", err)
			}
			return nil
		}
		_, err = tx.ExecContext(ctx, `
			INSERT INTO studio_assets (`+assetCols+`)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			a.ID, a.ProjectID, string(a.Kind), a.Name, a.CodeTag, a.Description, a.Prompt,
			a.PrimaryAssetID, a.PrimaryPath, a.VoiceProvider, a.VoiceID, a.VoiceStyle, a.UpdatedAt,
		)
		if err != nil {
			return fmt.Errorf("sqlite: insert studio asset: %w", err)
		}
		return nil
	})
	if isConstraintErr(err) {
		return studio.AssetEntry{}, apperr.New(apperr.CodeConflict, fmt.Sprintf("Asset tag %q conflicts with an existing asset", a.CodeTag))
	}
	if err != nil {
		return studio.AssetEntry{}, err
	}
	return a, nil
}

// ReplaceAssets atomically replaces all assets of a project in one WAL transaction.
func (r *StudioRepository) ReplaceAssets(ctx context.Context, projectID string, assets []studio.AssetEntry) ([]studio.AssetEntry, error) {
	pid := strings.TrimSpace(projectID)
	now := time.Now().UnixMilli()
	normalized := make([]studio.AssetEntry, 0, len(assets))
	for _, a := range assets {
		a.ProjectID = pid
		if err := a.Validate(); err != nil {
			return nil, err
		}
		if a.ID == "" {
			a.ID = uuid.NewString()
		}
		a.UpdatedAt = now
		normalized = append(normalized, a)
	}

	err := r.db.inTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM studio_assets WHERE project_id = ?`, pid); err != nil {
			return fmt.Errorf("sqlite: clear studio assets: %w", err)
		}
		for _, a := range normalized {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO studio_assets (`+assetCols+`)
				VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
				a.ID, a.ProjectID, string(a.Kind), a.Name, a.CodeTag, a.Description, a.Prompt,
				a.PrimaryAssetID, a.PrimaryPath, a.VoiceProvider, a.VoiceID, a.VoiceStyle, a.UpdatedAt,
			); err != nil {
				return fmt.Errorf("sqlite: insert studio asset: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return r.ListAssets(ctx, pid)
}

// DeleteAsset deletes a single studio asset by ID.
func (r *StudioRepository) DeleteAsset(ctx context.Context, id string) error {
	res, err := r.db.sql.ExecContext(ctx, `DELETE FROM studio_assets WHERE id = ?`, strings.TrimSpace(id))
	if err != nil {
		return fmt.Errorf("sqlite: delete studio asset: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return studio.ErrAssetNotFound(id)
	}
	return nil
}

// ListShots returns all shots for an episode ordered by seq ascending.
func (r *StudioRepository) ListShots(ctx context.Context, episodeID string) ([]studio.Shot, error) {
	rows, err := r.db.read().QueryContext(ctx,
		`SELECT `+shotCols+` FROM studio_shots WHERE episode_id = ? ORDER BY seq ASC, id ASC`,
		strings.TrimSpace(episodeID))
	if err != nil {
		return nil, fmt.Errorf("sqlite: list studio shots: %w", err)
	}
	defer rows.Close()
	out := []studio.Shot{}
	for rows.Next() {
		sh, err := scanShot(rows)
		if err != nil {
			return nil, fmt.Errorf("sqlite: scan studio shot: %w", err)
		}
		out = append(out, sh)
	}
	return out, rows.Err()
}

// GetShot fetches one shot by ID.
func (r *StudioRepository) GetShot(ctx context.Context, id string) (studio.Shot, error) {
	sh, err := scanShot(r.db.read().QueryRowContext(ctx,
		`SELECT `+shotCols+` FROM studio_shots WHERE id = ?`, strings.TrimSpace(id)))
	if errors.Is(err, sql.ErrNoRows) {
		return studio.Shot{}, studio.ErrShotNotFound(id)
	}
	if err != nil {
		return studio.Shot{}, fmt.Errorf("sqlite: get studio shot: %w", err)
	}
	return sh, nil
}

func marshalShotJSONSlices(sh studio.Shot) (dlgJSON, tagsJSON string) {
	if len(sh.Dialogues) > 0 {
		if b, err := json.Marshal(sh.Dialogues); err == nil {
			dlgJSON = string(b)
		}
	}
	if dlgJSON == "" {
		dlgJSON = "[]"
	}
	if len(sh.RefAssetTags) > 0 {
		if b, err := json.Marshal(sh.RefAssetTags); err == nil {
			tagsJSON = string(b)
		}
	}
	if tagsJSON == "" {
		tagsJSON = "[]"
	}
	return dlgJSON, tagsJSON
}

// UpsertShot inserts or updates a shot in an episode.
func (r *StudioRepository) UpsertShot(ctx context.Context, s studio.Shot) (studio.Shot, error) {
	if err := s.Validate(); err != nil {
		return studio.Shot{}, err
	}
	if s.ID == "" {
		s.ID = uuid.NewString()
	}
	s.UpdatedAt = time.Now().UnixMilli()
	dlgJSON, tagsJSON := marshalShotJSONSlices(s)

	err := r.db.inTx(ctx, func(tx *sql.Tx) error {
		var existing string
		err := tx.QueryRowContext(ctx, `SELECT id FROM studio_shots WHERE id = ?`, s.ID).Scan(&existing)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("sqlite: lookup studio shot: %w", err)
		}
		if existing != "" {
			_, err = tx.ExecContext(ctx, `
				UPDATE studio_shots SET
					seq = ?, scene_code = ?, title = ?, shot_mode = ?, duration_sec = ?,
					image_prompt = ?, video_prompt = ?, narration_text = ?, dialogue_json = ?, ref_asset_tags_json = ?,
					keyframe_asset_id = ?, keyframe_path = ?, video_asset_id = ?, video_path = ?,
					audio_asset_id = ?, audio_path = ?, status = ?, error_text = ?, updated_at = ?
				WHERE id = ?`,
				s.Seq, s.SceneCode, s.Title, string(s.ShotMode), s.DurationSec,
				s.ImagePrompt, s.VideoPrompt, s.NarrationText, dlgJSON, tagsJSON,
				s.KeyframeAssetID, s.KeyframePath, s.VideoAssetID, s.VideoPath,
				s.AudioAssetID, s.AudioPath, string(s.Status), s.ErrorText, s.UpdatedAt, s.ID,
			)
			if err != nil {
				return fmt.Errorf("sqlite: update studio shot: %w", err)
			}
			return nil
		}
		if s.Seq == 0 {
			var maxSeq int
			_ = tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq), 0) FROM studio_shots WHERE episode_id = ?`, s.EpisodeID).Scan(&maxSeq)
			s.Seq = maxSeq + 1
		}
		_, err = tx.ExecContext(ctx, `
			INSERT INTO studio_shots (`+shotCols+`)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			s.ID, s.EpisodeID, s.Seq, s.SceneCode, s.Title, string(s.ShotMode), s.DurationSec,
			s.ImagePrompt, s.VideoPrompt, s.NarrationText, dlgJSON, tagsJSON,
			s.KeyframeAssetID, s.KeyframePath, s.VideoAssetID, s.VideoPath,
			s.AudioAssetID, s.AudioPath, string(s.Status), s.ErrorText, s.UpdatedAt,
		)
		if err != nil {
			return fmt.Errorf("sqlite: insert studio shot: %w", err)
		}
		return nil
	})
	if err != nil {
		return studio.Shot{}, err
	}
	return s, nil
}

// ReplaceShots atomically replaces all shots of an episode in a single transaction.
func (r *StudioRepository) ReplaceShots(ctx context.Context, episodeID string, shots []studio.Shot) ([]studio.Shot, error) {
	epID := strings.TrimSpace(episodeID)
	now := time.Now().UnixMilli()
	normalized := make([]studio.Shot, 0, len(shots))
	for i, sh := range shots {
		sh.EpisodeID = epID
		if sh.Seq <= 0 {
			sh.Seq = i + 1
		}
		if err := sh.Validate(); err != nil {
			return nil, err
		}
		if sh.ID == "" {
			sh.ID = uuid.NewString()
		}
		sh.UpdatedAt = now
		normalized = append(normalized, sh)
	}

	err := r.db.inTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM studio_shots WHERE episode_id = ?`, epID); err != nil {
			return fmt.Errorf("sqlite: clear studio shots: %w", err)
		}
		for _, sh := range normalized {
			dlgJSON, tagsJSON := marshalShotJSONSlices(sh)
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO studio_shots (`+shotCols+`)
				VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
				sh.ID, sh.EpisodeID, sh.Seq, sh.SceneCode, sh.Title, string(sh.ShotMode), sh.DurationSec,
				sh.ImagePrompt, sh.VideoPrompt, sh.NarrationText, dlgJSON, tagsJSON,
				sh.KeyframeAssetID, sh.KeyframePath, sh.VideoAssetID, sh.VideoPath,
				sh.AudioAssetID, sh.AudioPath, string(sh.Status), sh.ErrorText, sh.UpdatedAt,
			); err != nil {
				return fmt.Errorf("sqlite: insert studio shot: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return r.ListShots(ctx, epID)
}

// ReorderShots updates shot sequence numbers in one transaction.
func (r *StudioRepository) ReorderShots(ctx context.Context, episodeID string, shotIDs []string) ([]studio.Shot, error) {
	epID := strings.TrimSpace(episodeID)
	now := time.Now().UnixMilli()
	err := r.db.inTx(ctx, func(tx *sql.Tx) error {
		for idx, id := range shotIDs {
			res, err := tx.ExecContext(ctx,
				`UPDATE studio_shots SET seq = ?, updated_at = ? WHERE id = ? AND episode_id = ?`,
				idx+1, now, strings.TrimSpace(id), epID,
			)
			if err != nil {
				return fmt.Errorf("sqlite: reorder studio shot: %w", err)
			}
			if n, _ := res.RowsAffected(); n == 0 {
				return studio.ErrShotNotFound(id)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return r.ListShots(ctx, epID)
}
