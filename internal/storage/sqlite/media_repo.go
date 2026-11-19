package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	domainmedia "app/internal/domain/media"

	"github.com/google/uuid"
)

// MediaAssetRecord aliases domainmedia.AssetRecord for SQLite persistence.
type MediaAssetRecord = domainmedia.AssetRecord

// MediaListFilter aliases domainmedia.ListFilter for ListAssets queries.
type MediaListFilter = domainmedia.ListFilter

var _ domainmedia.Repository = (*MediaRepository)(nil)

// MediaRepository manages persistence of media asset records in SQLite.
type MediaRepository struct {
	db *DB
}

// NewMediaRepository constructs a new MediaRepository.
func NewMediaRepository(db *DB) *MediaRepository {
	return &MediaRepository{db: db}
}

// RegisterAsset inserts a new media asset or updates an existing one when file_path conflicts.
func (r *MediaRepository) RegisterAsset(ctx context.Context, asset MediaAssetRecord) (*MediaAssetRecord, error) {
	if strings.TrimSpace(asset.FilePath) == "" {
		return nil, errors.New("sqlite: file_path cannot be empty")
	}
	if strings.TrimSpace(asset.ID) == "" {
		asset.ID = uuid.NewString()
	}
	now := time.Now().Unix()
	if asset.CreatedAt <= 0 {
		asset.CreatedAt = now
	}
	if asset.LastVerifiedAt <= 0 {
		asset.LastVerifiedAt = now
	}
	if strings.TrimSpace(asset.MetadataJSON) == "" {
		asset.MetadataJSON = "{}"
	}

	query := `
		INSERT INTO media_assets (
			id, kind, source_node, file_path, registered_root,
			mime_type, size_bytes, width, height, duration_sec,
			metadata_json, created_at, last_verified_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(file_path) DO UPDATE SET
			kind = excluded.kind,
			source_node = excluded.source_node,
			registered_root = excluded.registered_root,
			mime_type = excluded.mime_type,
			size_bytes = excluded.size_bytes,
			width = excluded.width,
			height = excluded.height,
			duration_sec = excluded.duration_sec,
			metadata_json = excluded.metadata_json,
			last_verified_at = excluded.last_verified_at
		RETURNING id, created_at;`

	var returnedID string
	var returnedCreatedAt int64
	err := r.db.sql.QueryRowContext(ctx, query,
		asset.ID,
		asset.Kind,
		asset.SourceNode,
		asset.FilePath,
		asset.RegisteredRoot,
		asset.MIMEType,
		asset.SizeBytes,
		asset.Width,
		asset.Height,
		asset.DurationSec,
		asset.MetadataJSON,
		asset.CreatedAt,
		asset.LastVerifiedAt,
	).Scan(&returnedID, &returnedCreatedAt)
	if err != nil {
		return nil, fmt.Errorf("sqlite: register media asset: %w", err)
	}

	asset.ID = returnedID
	asset.CreatedAt = returnedCreatedAt
	return &asset, nil
}

// GetAsset retrieves an asset by its primary key ID. Returns nil, nil if not found.
func (r *MediaRepository) GetAsset(ctx context.Context, id string) (*MediaAssetRecord, error) {
	query := `
		SELECT id, kind, source_node, file_path, registered_root,
		       mime_type, size_bytes, width, height, duration_sec,
		       metadata_json, created_at, last_verified_at
		FROM media_assets
		WHERE id = ?;`

	var a MediaAssetRecord
	err := r.db.read().QueryRowContext(ctx, query, id).Scan(
		&a.ID,
		&a.Kind,
		&a.SourceNode,
		&a.FilePath,
		&a.RegisteredRoot,
		&a.MIMEType,
		&a.SizeBytes,
		&a.Width,
		&a.Height,
		&a.DurationSec,
		&a.MetadataJSON,
		&a.CreatedAt,
		&a.LastVerifiedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("sqlite: get media asset %s: %w", id, err)
	}
	return &a, nil
}

// GetAssetByPath retrieves an asset by its unique file path. Returns nil, nil if not found.
func (r *MediaRepository) GetAssetByPath(ctx context.Context, filePath string) (*MediaAssetRecord, error) {
	query := `
		SELECT id, kind, source_node, file_path, registered_root,
		       mime_type, size_bytes, width, height, duration_sec,
		       metadata_json, created_at, last_verified_at
		FROM media_assets
		WHERE file_path = ?;`

	var a MediaAssetRecord
	err := r.db.read().QueryRowContext(ctx, query, filePath).Scan(
		&a.ID,
		&a.Kind,
		&a.SourceNode,
		&a.FilePath,
		&a.RegisteredRoot,
		&a.MIMEType,
		&a.SizeBytes,
		&a.Width,
		&a.Height,
		&a.DurationSec,
		&a.MetadataJSON,
		&a.CreatedAt,
		&a.LastVerifiedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("sqlite: get media asset by path %q: %w", filePath, err)
	}
	return &a, nil
}

// ListAssets returns a paginated list of media assets matching the filter and total matching count.
func (r *MediaRepository) ListAssets(ctx context.Context, filter MediaListFilter) ([]MediaAssetRecord, int, error) {
	var conditions []string
	var args []any

	if kind := strings.TrimSpace(filter.Kind); kind != "" {
		conditions = append(conditions, "kind = ?")
		args = append(args, kind)
	}
	if search := strings.TrimSpace(filter.Search); search != "" {
		conditions = append(conditions, "(file_path LIKE ? OR metadata_json LIKE ?)")
		pattern := "%" + search + "%"
		args = append(args, pattern, pattern)
	}

	whereClause := ""
	if len(conditions) > 0 {
		whereClause = " WHERE " + strings.Join(conditions, " AND ")
	}

	countQuery := "SELECT COUNT(*) FROM media_assets" + whereClause
	var total int
	if err := r.db.read().QueryRowContext(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("sqlite: count media assets: %w", err)
	}

	limit := filter.Limit
	if limit <= 0 {
		limit = 50
	} else if limit > 500 {
		limit = 500
	}
	offset := filter.Offset
	if offset < 0 {
		offset = 0
	}

	selectQuery := `
		SELECT id, kind, source_node, file_path, registered_root,
		       mime_type, size_bytes, width, height, duration_sec,
		       metadata_json, created_at, last_verified_at
		FROM media_assets` + whereClause + `
		ORDER BY created_at DESC, id DESC
		LIMIT ? OFFSET ?;`

	selectArgs := make([]any, 0, len(args)+2)
	selectArgs = append(selectArgs, args...)
	selectArgs = append(selectArgs, limit, offset)

	rows, err := r.db.read().QueryContext(ctx, selectQuery, selectArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("sqlite: list media assets: %w", err)
	}
	defer rows.Close()

	var out []MediaAssetRecord
	for rows.Next() {
		var a MediaAssetRecord
		if err := rows.Scan(
			&a.ID,
			&a.Kind,
			&a.SourceNode,
			&a.FilePath,
			&a.RegisteredRoot,
			&a.MIMEType,
			&a.SizeBytes,
			&a.Width,
			&a.Height,
			&a.DurationSec,
			&a.MetadataJSON,
			&a.CreatedAt,
			&a.LastVerifiedAt,
		); err != nil {
			return nil, 0, fmt.Errorf("sqlite: scan media asset: %w", err)
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("sqlite: iterate media assets: %w", err)
	}
	return out, total, nil
}

// DeleteAsset deletes an asset by ID.
func (r *MediaRepository) DeleteAsset(ctx context.Context, id string) error {
	_, err := r.db.sql.ExecContext(ctx, "DELETE FROM media_assets WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("sqlite: delete media asset %s: %w", id, err)
	}
	return nil
}

// PruneMissingAssets deletes media assets whose IDs are in missingIDs.
func (r *MediaRepository) PruneMissingAssets(ctx context.Context, missingIDs []string) (int64, error) {
	if len(missingIDs) == 0 {
		return 0, nil
	}
	var totalDeleted int64
	chunkSize := 400
	for i := 0; i < len(missingIDs); i += chunkSize {
		end := i + chunkSize
		if end > len(missingIDs) {
			end = len(missingIDs)
		}
		chunk := missingIDs[i:end]
		placeholders := make([]string, len(chunk))
		args := make([]any, len(chunk))
		for j, id := range chunk {
			placeholders[j] = "?"
			args[j] = id
		}
		query := fmt.Sprintf("DELETE FROM media_assets WHERE id IN (%s);", strings.Join(placeholders, ","))
		res, err := r.db.sql.ExecContext(ctx, query, args...)
		if err != nil {
			return totalDeleted, fmt.Errorf("sqlite: prune missing assets: %w", err)
		}
		n, _ := res.RowsAffected()
		totalDeleted += n
	}
	return totalDeleted, nil
}

// PruneExpiredAssets deletes records created before olderThan, and also prunes
// the oldest records exceeding maxItems (keeping the newest maxItems).
func (r *MediaRepository) PruneExpiredAssets(ctx context.Context, olderThan time.Time, maxItems int) (int64, error) {
	var totalDeleted int64

	if !olderThan.IsZero() {
		res, err := r.db.sql.ExecContext(ctx, "DELETE FROM media_assets WHERE created_at < ?;", olderThan.Unix())
		if err != nil {
			return totalDeleted, fmt.Errorf("sqlite: prune expired assets by retention: %w", err)
		}
		n, _ := res.RowsAffected()
		totalDeleted += n
	}

	if maxItems > 0 {
		query := `DELETE FROM media_assets WHERE id IN (
			SELECT id FROM media_assets
			ORDER BY created_at DESC, id DESC
			LIMIT -1 OFFSET ?
		);`
		res, err := r.db.sql.ExecContext(ctx, query, maxItems)
		if err != nil {
			return totalDeleted, fmt.Errorf("sqlite: prune excess media assets: %w", err)
		}
		n, _ := res.RowsAffected()
		totalDeleted += n
	}

	return totalDeleted, nil
}

// UpdateLastVerified updates the last_verified_at timestamp for an asset.
func (r *MediaRepository) UpdateLastVerified(ctx context.Context, id string, verifiedAt int64) error {
	_, err := r.db.sql.ExecContext(ctx, "UPDATE media_assets SET last_verified_at = ? WHERE id = ?;", verifiedAt, id)
	if err != nil {
		return fmt.Errorf("sqlite: update last_verified_at for asset %s: %w", id, err)
	}
	return nil
}
