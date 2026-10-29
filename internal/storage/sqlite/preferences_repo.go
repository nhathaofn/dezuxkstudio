package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"app/internal/domain/preferences"
)

// preferencesKey is the user_settings row holding the preference document.
const preferencesKey = "preferences"

// PreferencesRepository implements preferences.Repository as a JSON document.
type PreferencesRepository struct{ db *DB }

var _ preferences.Repository = (*PreferencesRepository)(nil)

// NewPreferencesRepository creates the repository.
func NewPreferencesRepository(db *DB) *PreferencesRepository { return &PreferencesRepository{db: db} }

// Load implements preferences.Repository.
func (r *PreferencesRepository) Load(ctx context.Context) (preferences.Preferences, bool, error) {
	var raw string
	err := r.db.sql.QueryRowContext(ctx, "SELECT value FROM user_settings WHERE key = ?", preferencesKey).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return preferences.Preferences{}, false, nil
	}
	if err != nil {
		return preferences.Preferences{}, false, fmt.Errorf("sqlite: load preferences: %w", err)
	}
	var p preferences.Preferences
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		// A corrupt document is treated as legacy data and repaired by Migrate.
		return preferences.Preferences{}, true, nil
	}
	return p, true, nil
}

// Save implements preferences.Repository.
func (r *PreferencesRepository) Save(ctx context.Context, p preferences.Preferences) error {
	raw, err := json.Marshal(p)
	if err != nil {
		return fmt.Errorf("sqlite: encode preferences: %w", err)
	}
	_, err = r.db.sql.ExecContext(ctx, `
		INSERT INTO user_settings (key, value, updated_at) VALUES (?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = CURRENT_TIMESTAMP;`,
		preferencesKey, string(raw))
	if err != nil {
		return fmt.Errorf("sqlite: save preferences: %w", err)
	}
	return nil
}
