package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"app/internal/domain/binaries"
)

// BinariesRepository implements binaries.Repository using SQLite.
type BinariesRepository struct{ db *DB }

var _ binaries.Repository = (*BinariesRepository)(nil)

// NewBinariesRepository creates a new BinariesRepository.
func NewBinariesRepository(db *DB) *BinariesRepository {
	return &BinariesRepository{db: db}
}

// GetCustomPath returns the custom path configured for binary id, or empty string if unset.
func (r *BinariesRepository) GetCustomPath(ctx context.Context, id binaries.BinaryID) (string, error) {
	var p string
	err := r.db.sql.QueryRowContext(ctx, "SELECT custom_path FROM binary_configs WHERE id = ?", string(id)).Scan(&p)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("sqlite: get custom path for %s: %w", id, err)
	}
	return p, nil
}

// GetAllCustomPaths returns custom paths for all configured binaries.
func (r *BinariesRepository) GetAllCustomPaths(ctx context.Context) (map[binaries.BinaryID]string, error) {
	rows, err := r.db.sql.QueryContext(ctx, "SELECT id, custom_path FROM binary_configs")
	if err != nil {
		return nil, fmt.Errorf("sqlite: get all custom paths: %w", err)
	}
	defer rows.Close()

	res := make(map[binaries.BinaryID]string)
	for rows.Next() {
		var id, p string
		if err := rows.Scan(&id, &p); err != nil {
			return nil, fmt.Errorf("sqlite: scan binary config: %w", err)
		}
		res[binaries.BinaryID(id)] = p
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: iterate binary configs: %w", err)
	}
	return res, nil
}

// SetCustomPath sets or updates the custom path for a binary. An empty customPath removes/clears the override.
func (r *BinariesRepository) SetCustomPath(ctx context.Context, id binaries.BinaryID, customPath string) error {
	_, err := r.db.sql.ExecContext(ctx, `
		INSERT INTO binary_configs (id, custom_path, updated_at) VALUES (?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(id) DO UPDATE SET custom_path = excluded.custom_path, updated_at = CURRENT_TIMESTAMP;`,
		string(id), customPath)
	if err != nil {
		return fmt.Errorf("sqlite: set custom path for %s: %w", id, err)
	}
	return nil
}
