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
	"app/internal/config"
	"app/internal/domain/studio"
)

// StudioRepoOption configures a StudioRepository.
type StudioRepoOption func(*StudioRepository)

// WithStudioConfig configures StudioRepository limits and timeline defaults from cfg.
func WithStudioConfig(cfg config.StudioConfig) StudioRepoOption {
	resolved := config.ResolveStudioConfig(cfg)
	return func(r *StudioRepository) {
		r.cfg = resolved
	}
}

// StudioRepository implements studio.Repository on SQLite.
type StudioRepository struct {
	db  *DB
	cfg config.StudioConfig
}

var _ studio.Repository = (*StudioRepository)(nil)

// NewStudioRepository constructs a StudioRepository.
func NewStudioRepository(db *DB, opts ...StudioRepoOption) *StudioRepository {
	r := &StudioRepository{
		db:  db,
		cfg: config.Default(config.ModeDesktop).Studio,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(r)
		}
	}
	return r
}

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
		if err := json.Unmarshal([]byte(dlgJSON), &sh.Dialogues); err != nil {
			return studio.Shot{}, apperr.Wrap(apperr.CodeIntegrity, "Corrupted shot dialogue JSON in database", err).
				WithMsg("errors.studio.corrupt_data", nil)
		}
	}
	if strings.TrimSpace(tagsJSON) != "" {
		if err := json.Unmarshal([]byte(tagsJSON), &sh.RefAssetTags); err != nil {
			return studio.Shot{}, apperr.Wrap(apperr.CodeIntegrity, "Corrupted shot asset tags JSON in database", err).
				WithMsg("errors.studio.corrupt_data", nil)
		}
	}
	return sh, nil
}

func isUniqueConstraintErr(err error) bool {
	if err == nil {
		return false
	}
	upper := strings.ToUpper(err.Error())
	return strings.Contains(upper, "UNIQUE CONSTRAINT") ||
		strings.Contains(upper, "CONSTRAINT_UNIQUE") ||
		strings.Contains(upper, "PRIMARY KEY MUST BE UNIQUE") ||
		strings.Contains(upper, "CONSTRAINT_PRIMARYKEY")
}

func isForeignKeyErr(err error) bool {
	if err == nil {
		return false
	}
	upper := strings.ToUpper(err.Error())
	return strings.Contains(upper, "FOREIGN KEY CONSTRAINT") ||
		strings.Contains(upper, "CONSTRAINT_FOREIGNKEY")
}

func isTransientIOErr(err error) bool {
	if err == nil {
		return false
	}
	upper := strings.ToUpper(err.Error())
	return strings.Contains(upper, "SQLITE_BUSY") ||
		strings.Contains(upper, "DATABASE IS LOCKED") ||
		strings.Contains(upper, "SQLITE_LOCKED") ||
		strings.Contains(upper, "DATABASE TABLE IS LOCKED") ||
		strings.Contains(upper, "SQLITE_FULL") ||
		strings.Contains(upper, "DATABASE OR DISK IS FULL") ||
		strings.Contains(upper, "SQLITE_IOERR") ||
		strings.Contains(upper, "DISK I/O ERROR")
}

func wrapStudioWriteErr(err error, op string, onUnique func() error, onFK func() error) error {
	if err == nil {
		return nil
	}
	var ae *apperr.Error
	if errors.As(err, &ae) {
		return err
	}
	if isUniqueConstraintErr(err) && onUnique != nil {
		return onUnique()
	}
	if isForeignKeyErr(err) {
		if onFK != nil {
			return onFK()
		}
		return apperr.Wrap(apperr.CodeNotFound, "Referenced studio record not found", err)
	}
	if isTransientIOErr(err) {
		return apperr.Wrap(apperr.CodeUnavailable, "Studio database is temporarily busy or unavailable", err).
			WithMsg("errors.studio.storage_unavailable", nil)
	}
	return fmt.Errorf("sqlite: %s: %w", op, err)
}

func wrapStudioReadErr(err error, op string) error {
	if err == nil {
		return nil
	}
	var ae *apperr.Error
	if errors.As(err, &ae) {
		return err
	}
	if isTransientIOErr(err) {
		return apperr.Wrap(apperr.CodeUnavailable, "Studio database is temporarily busy or unavailable", err).
			WithMsg("errors.studio.storage_unavailable", nil)
	}
	return fmt.Errorf("sqlite: %s: %w", op, err)
}

// ListProjects returns all studio projects ordered by most recently updated.
func (r *StudioRepository) ListProjects(ctx context.Context) ([]studio.Project, error) {
	rows, err := r.db.read().QueryContext(ctx,
		`SELECT `+projectCols+` FROM studio_projects ORDER BY updated_at DESC, created_at DESC, id ASC`)
	if err != nil {
		return nil, wrapStudioReadErr(err, "list studio projects")
	}
	defer rows.Close()
	out := []studio.Project{}
	for rows.Next() {
		p, err := scanProject(rows)
		if err != nil {
			return nil, wrapStudioReadErr(err, "scan studio project")
		}
		out = append(out, p)
	}
	return out, wrapStudioReadErr(rows.Err(), "iterate studio projects")
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
		return studio.Project{}, wrapStudioReadErr(err, "get studio project")
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
	if err != nil {
		return studio.Project{}, wrapStudioWriteErr(err, "create studio project", func() error {
			return apperr.Wrap(apperr.CodeConflict, fmt.Sprintf("Project slug %q already exists", p.Slug), err).
				WithMsg("errors.studio.duplicate_slug", map[string]any{"slug": p.Slug})
		}, nil)
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
	if err != nil {
		return studio.Project{}, wrapStudioWriteErr(err, "update studio project", func() error {
			return apperr.Wrap(apperr.CodeConflict, fmt.Sprintf("Project slug %q already exists", p.Slug), err).
				WithMsg("errors.studio.duplicate_slug", map[string]any{"slug": p.Slug})
		}, nil)
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
		return wrapStudioWriteErr(err, "delete studio project", nil, nil)
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
		return nil, wrapStudioReadErr(err, "list studio episodes")
	}
	defer rows.Close()
	out := []studio.Episode{}
	for rows.Next() {
		ep, err := scanEpisode(rows)
		if err != nil {
			return nil, wrapStudioReadErr(err, "scan studio episode")
		}
		out = append(out, ep)
	}
	return out, wrapStudioReadErr(rows.Err(), "iterate studio episodes")
}

// GetEpisode fetches an episode by ID.
func (r *StudioRepository) GetEpisode(ctx context.Context, id string) (studio.Episode, error) {
	ep, err := scanEpisode(r.db.read().QueryRowContext(ctx,
		`SELECT `+episodeCols+` FROM studio_episodes WHERE id = ?`, strings.TrimSpace(id)))
	if errors.Is(err, sql.ErrNoRows) {
		return studio.Episode{}, studio.ErrEpisodeNotFound(id)
	}
	if err != nil {
		return studio.Episode{}, wrapStudioReadErr(err, "get studio episode")
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
	if err != nil {
		return studio.Episode{}, wrapStudioWriteErr(err, "create studio episode", func() error {
			return apperr.Wrap(apperr.CodeConflict, fmt.Sprintf("Episode %q already exists in project", ep.Code), err).
				WithMsg("errors.studio.duplicate_episode", map[string]any{"code": ep.Code})
		}, func() error {
			return studio.ErrProjectNotFound(ep.ProjectID)
		})
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
		return studio.Episode{}, wrapStudioWriteErr(err, "update studio episode", func() error {
			return apperr.Wrap(apperr.CodeConflict, fmt.Sprintf("Episode %q already exists in project", ep.Code), err).
				WithMsg("errors.studio.duplicate_episode", map[string]any{"code": ep.Code})
		}, nil)
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
		return nil, wrapStudioReadErr(err, "list studio assets")
	}
	defer rows.Close()
	out := []studio.AssetEntry{}
	for rows.Next() {
		a, err := scanAsset(rows)
		if err != nil {
			return nil, wrapStudioReadErr(err, "scan studio asset")
		}
		out = append(out, a)
	}
	return out, wrapStudioReadErr(rows.Err(), "iterate studio assets")
}

// GetAsset fetches one studio asset entry by ID.
func (r *StudioRepository) GetAsset(ctx context.Context, id string) (studio.AssetEntry, error) {
	a, err := scanAsset(r.db.read().QueryRowContext(ctx,
		`SELECT `+assetCols+` FROM studio_assets WHERE id = ?`, strings.TrimSpace(id)))
	if errors.Is(err, sql.ErrNoRows) {
		return studio.AssetEntry{}, studio.ErrAssetNotFound(id)
	}
	if err != nil {
		return studio.AssetEntry{}, wrapStudioReadErr(err, "get studio asset")
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
			return err
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
			return err
		}
		_, err = tx.ExecContext(ctx, `
			INSERT INTO studio_assets (`+assetCols+`)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			a.ID, a.ProjectID, string(a.Kind), a.Name, a.CodeTag, a.Description, a.Prompt,
			a.PrimaryAssetID, a.PrimaryPath, a.VoiceProvider, a.VoiceID, a.VoiceStyle, a.UpdatedAt,
		)
		return err
	})
	if err != nil {
		return studio.AssetEntry{}, wrapStudioWriteErr(err, "upsert studio asset", func() error {
			return apperr.Wrap(apperr.CodeConflict, fmt.Sprintf("Asset tag %q conflicts with an existing asset", a.CodeTag), err).
				WithMsg("errors.studio.duplicate_asset_tag", map[string]any{"tag": a.CodeTag})
		}, func() error {
			return studio.ErrProjectNotFound(a.ProjectID)
		})
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
			return err
		}
		for _, a := range normalized {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO studio_assets (`+assetCols+`)
				VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
				a.ID, a.ProjectID, string(a.Kind), a.Name, a.CodeTag, a.Description, a.Prompt,
				a.PrimaryAssetID, a.PrimaryPath, a.VoiceProvider, a.VoiceID, a.VoiceStyle, a.UpdatedAt,
			); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, wrapStudioWriteErr(err, "replace studio assets", func() error {
			return apperr.Wrap(apperr.CodeConflict, "Duplicate asset code_tag in project", err).
				WithMsg("errors.studio.duplicate_asset_tag", nil)
		}, func() error {
			return studio.ErrProjectNotFound(pid)
		})
	}
	return r.ListAssets(ctx, pid)
}

// DeleteAsset deletes a single studio asset by ID.
func (r *StudioRepository) DeleteAsset(ctx context.Context, id string) error {
	res, err := r.db.sql.ExecContext(ctx, `DELETE FROM studio_assets WHERE id = ?`, strings.TrimSpace(id))
	if err != nil {
		return wrapStudioWriteErr(err, "delete studio asset", nil, nil)
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
		return nil, wrapStudioReadErr(err, "list studio shots")
	}
	defer rows.Close()
	out := []studio.Shot{}
	for rows.Next() {
		sh, err := scanShot(rows)
		if err != nil {
			return nil, wrapStudioReadErr(err, "scan studio shot")
		}
		out = append(out, sh)
	}
	return out, wrapStudioReadErr(rows.Err(), "iterate studio shots")
}

// GetShot fetches one shot by ID.
func (r *StudioRepository) GetShot(ctx context.Context, id string) (studio.Shot, error) {
	sh, err := scanShot(r.db.read().QueryRowContext(ctx,
		`SELECT `+shotCols+` FROM studio_shots WHERE id = ?`, strings.TrimSpace(id)))
	if errors.Is(err, sql.ErrNoRows) {
		return studio.Shot{}, studio.ErrShotNotFound(id)
	}
	if err != nil {
		return studio.Shot{}, wrapStudioReadErr(err, "get studio shot")
	}
	return sh, nil
}

func marshalShotJSONSlices(sh studio.Shot) (dlgJSON, tagsJSON string, err error) {
	dlgJSON = "[]"
	if len(sh.Dialogues) > 0 {
		b, merr := json.Marshal(sh.Dialogues)
		if merr != nil {
			return "", "", fmt.Errorf("marshal shot dialogues: %w", merr)
		}
		dlgJSON = string(b)
	}
	tagsJSON = "[]"
	if len(sh.RefAssetTags) > 0 {
		b, merr := json.Marshal(sh.RefAssetTags)
		if merr != nil {
			return "", "", fmt.Errorf("marshal shot ref_asset_tags: %w", merr)
		}
		tagsJSON = string(b)
	}
	return dlgJSON, tagsJSON, nil
}

// UpsertShot inserts or updates a shot in an episode.
func (r *StudioRepository) UpsertShot(ctx context.Context, s studio.Shot) (studio.Shot, error) {
	if err := s.ValidateWithLimits(r.cfg.MaxShotDurationSec, r.cfg.DefaultShotDurationSec); err != nil {
		return studio.Shot{}, err
	}
	if s.ID == "" {
		s.ID = uuid.NewString()
	}
	s.UpdatedAt = time.Now().UnixMilli()
	dlgJSON, tagsJSON, err := marshalShotJSONSlices(s)
	if err != nil {
		return studio.Shot{}, fmt.Errorf("sqlite: %w", err)
	}

	err = r.db.inTx(ctx, func(tx *sql.Tx) error {
		var existing string
		qerr := tx.QueryRowContext(ctx, `SELECT id FROM studio_shots WHERE id = ?`, s.ID).Scan(&existing)
		if qerr != nil && !errors.Is(qerr, sql.ErrNoRows) {
			return qerr
		}
		if existing != "" {
			_, uerr := tx.ExecContext(ctx, `
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
			return uerr
		}
		if s.Seq == 0 {
			var maxSeq int
			if serr := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq), 0) FROM studio_shots WHERE episode_id = ?`, s.EpisodeID).Scan(&maxSeq); serr != nil {
				return serr
			}
			s.Seq = maxSeq + 1
		}
		_, ierr := tx.ExecContext(ctx, `
			INSERT INTO studio_shots (`+shotCols+`)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			s.ID, s.EpisodeID, s.Seq, s.SceneCode, s.Title, string(s.ShotMode), s.DurationSec,
			s.ImagePrompt, s.VideoPrompt, s.NarrationText, dlgJSON, tagsJSON,
			s.KeyframeAssetID, s.KeyframePath, s.VideoAssetID, s.VideoPath,
			s.AudioAssetID, s.AudioPath, string(s.Status), s.ErrorText, s.UpdatedAt,
		)
		return ierr
	})
	if err != nil {
		return studio.Shot{}, wrapStudioWriteErr(err, "upsert studio shot", func() error {
			return apperr.Wrap(apperr.CodeConflict, "Shot conflicts with an existing shot", err)
		}, func() error {
			return studio.ErrEpisodeNotFound(s.EpisodeID)
		})
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
		if err := sh.ValidateWithLimits(r.cfg.MaxShotDurationSec, r.cfg.DefaultShotDurationSec); err != nil {
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
			return err
		}
		for _, sh := range normalized {
			dlgJSON, tagsJSON, merr := marshalShotJSONSlices(sh)
			if merr != nil {
				return merr
			}
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO studio_shots (`+shotCols+`)
				VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
				sh.ID, sh.EpisodeID, sh.Seq, sh.SceneCode, sh.Title, string(sh.ShotMode), sh.DurationSec,
				sh.ImagePrompt, sh.VideoPrompt, sh.NarrationText, dlgJSON, tagsJSON,
				sh.KeyframeAssetID, sh.KeyframePath, sh.VideoAssetID, sh.VideoPath,
				sh.AudioAssetID, sh.AudioPath, string(sh.Status), sh.ErrorText, sh.UpdatedAt,
			); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, wrapStudioWriteErr(err, "replace studio shots", func() error {
			return apperr.Wrap(apperr.CodeConflict, "Duplicate shot ID in episode", err)
		}, func() error {
			return studio.ErrEpisodeNotFound(epID)
		})
	}
	return r.ListShots(ctx, epID)
}

// ReorderShots updates shot sequence numbers in one transaction, rejecting duplicate, unknown, or partial shotIDs.
func (r *StudioRepository) ReorderShots(ctx context.Context, episodeID string, shotIDs []string) ([]studio.Shot, error) {
	epID := strings.TrimSpace(episodeID)
	if epID == "" {
		return nil, apperr.Validation("episode_id is required")
	}
	if len(shotIDs) == 0 {
		return nil, apperr.Validation("shot_ids is required")
	}
	cleaned := make([]string, 0, len(shotIDs))
	seen := make(map[string]bool, len(shotIDs))
	for _, raw := range shotIDs {
		id := strings.TrimSpace(raw)
		if id == "" || seen[id] {
			return nil, apperr.Validation("shot_ids must contain non-empty unique shot IDs").
				WithMsg("errors.studio.invalid_shot_order", nil)
		}
		seen[id] = true
		cleaned = append(cleaned, id)
	}

	now := time.Now().UnixMilli()
	err := r.db.inTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT id FROM studio_shots WHERE episode_id = ?`, epID)
		if err != nil {
			return err
		}
		existing := make(map[string]bool)
		for rows.Next() {
			var id string
			if serr := rows.Scan(&id); serr != nil {
				_ = rows.Close()
				return serr
			}
			existing[id] = true
		}
		if rerr := rows.Err(); rerr != nil {
			_ = rows.Close()
			return rerr
		}
		_ = rows.Close()

		for _, id := range cleaned {
			if !existing[id] {
				return studio.ErrShotNotFound(id)
			}
		}
		if len(cleaned) != len(existing) {
			return apperr.Validation(fmt.Sprintf("shot_ids must include all %d shots in episode", len(existing))).
				WithMsg("errors.studio.invalid_shot_order", map[string]any{"expected": len(existing), "actual": len(cleaned)})
		}

		for idx, id := range cleaned {
			if _, err := tx.ExecContext(ctx,
				`UPDATE studio_shots SET seq = ?, updated_at = ? WHERE id = ? AND episode_id = ?`,
				idx+1, now, id, epID,
			); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, wrapStudioWriteErr(err, "reorder studio shots", nil, nil)
	}
	return r.ListShots(ctx, epID)
}
