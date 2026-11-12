package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"app/internal/domain/provider"
)

// ProviderRepository implements provider.Repository.
type ProviderRepository struct{ db *DB }

var _ provider.Repository = (*ProviderRepository)(nil)

// NewProviderRepository creates the repository.
func NewProviderRepository(db *DB) *ProviderRepository { return &ProviderRepository{db: db} }

const credentialColumns = `id, provider_id, name, values_json, secret_hints_json, is_active, created_at, updated_at`

type rowScanner interface{ Scan(dest ...any) error }

func scanCredential(s rowScanner) (provider.Credential, error) {
	var (
		c                provider.Credential
		values, hints    string
		active           int
		created, updated int64
	)
	if err := s.Scan(&c.ID, &c.ProviderID, &c.Name, &values, &hints, &active, &created, &updated); err != nil {
		return provider.Credential{}, err
	}
	c.Values = map[string]string{}
	c.SecretHints = map[string]string{}
	_ = json.Unmarshal([]byte(values), &c.Values)
	_ = json.Unmarshal([]byte(hints), &c.SecretHints)
	c.Active = active == 1
	c.CreatedAt = time.UnixMilli(created)
	c.UpdatedAt = time.UnixMilli(updated)
	return c, nil
}

func encodeMap(m map[string]string) string {
	if m == nil {
		m = map[string]string{}
	}
	raw, _ := json.Marshal(m)
	return string(raw)
}

// ListCredentials implements provider.Repository.
func (r *ProviderRepository) ListCredentials(ctx context.Context) ([]provider.Credential, error) {
	rows, err := r.db.sql.QueryContext(ctx,
		`SELECT `+credentialColumns+` FROM provider_credentials ORDER BY provider_id, created_at ASC, id ASC`)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list provider credentials: %w", err)
	}
	defer rows.Close()
	var out []provider.Credential
	for rows.Next() {
		c, err := scanCredential(rows)
		if err != nil {
			return nil, fmt.Errorf("sqlite: scan provider credential: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// GetCredential implements provider.Repository.
func (r *ProviderRepository) GetCredential(ctx context.Context, id string) (provider.Credential, error) {
	c, err := scanCredential(r.db.sql.QueryRowContext(ctx,
		`SELECT `+credentialColumns+` FROM provider_credentials WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return provider.Credential{}, provider.ErrNotFound(id)
	}
	if err != nil {
		return provider.Credential{}, fmt.Errorf("sqlite: get provider credential: %w", err)
	}
	return c, nil
}

func (r *ProviderRepository) tx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := r.db.sql.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sqlite: begin: %w", err)
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sqlite: commit: %w", err)
	}
	return nil
}

// CreateCredential implements provider.Repository.
func (r *ProviderRepository) CreateCredential(ctx context.Context, c provider.Credential) (provider.Credential, error) {
	if c.ID == "" {
		c.ID = uuid.NewString()
	}
	now := time.Now()
	c.CreatedAt, c.UpdatedAt = now, now
	err := r.tx(ctx, func(tx *sql.Tx) error {
		var n int
		if err := tx.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM provider_credentials WHERE provider_id = ? AND is_active = 1`, c.ProviderID).Scan(&n); err != nil {
			return fmt.Errorf("sqlite: count active: %w", err)
		}
		c.Active = n == 0
		_, err := tx.ExecContext(ctx, `INSERT INTO provider_credentials (`+credentialColumns+`) VALUES (?,?,?,?,?,?,?,?)`,
			c.ID, c.ProviderID, c.Name, encodeMap(c.Values), encodeMap(c.SecretHints), boolInt(c.Active),
			now.UnixMilli(), now.UnixMilli())
		if err != nil {
			return fmt.Errorf("sqlite: insert provider credential: %w", err)
		}
		return nil
	})
	return c, err
}

// UpdateCredential implements provider.Repository (name, values and hints; never the active flag).
func (r *ProviderRepository) UpdateCredential(ctx context.Context, c provider.Credential) error {
	res, err := r.db.sql.ExecContext(ctx,
		`UPDATE provider_credentials SET name = ?, values_json = ?, secret_hints_json = ?, updated_at = ? WHERE id = ?`,
		c.Name, encodeMap(c.Values), encodeMap(c.SecretHints), time.Now().UnixMilli(), c.ID)
	if err != nil {
		return fmt.Errorf("sqlite: update provider credential: %w", err)
	}
	return affected(res, c.ID)
}

// DeleteCredential implements provider.Repository; deleting the active credential promotes the oldest other one.
func (r *ProviderRepository) DeleteCredential(ctx context.Context, id string) error {
	return r.tx(ctx, func(tx *sql.Tx) error {
		var providerID string
		var active int
		err := tx.QueryRowContext(ctx, `SELECT provider_id, is_active FROM provider_credentials WHERE id = ?`, id).
			Scan(&providerID, &active)
		if errors.Is(err, sql.ErrNoRows) {
			return provider.ErrNotFound(id)
		}
		if err != nil {
			return fmt.Errorf("sqlite: load provider credential: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM provider_credentials WHERE id = ?`, id); err != nil {
			return fmt.Errorf("sqlite: delete provider credential: %w", err)
		}
		if active == 1 {
			_, err := tx.ExecContext(ctx, `UPDATE provider_credentials SET is_active = 1 WHERE id = (
				SELECT id FROM provider_credentials WHERE provider_id = ? ORDER BY created_at ASC, id ASC LIMIT 1)`, providerID)
			if err != nil {
				return fmt.Errorf("sqlite: promote provider credential: %w", err)
			}
		}
		return nil
	})
}

// ActivateCredential implements provider.Repository.
func (r *ProviderRepository) ActivateCredential(ctx context.Context, id string) error {
	return r.tx(ctx, func(tx *sql.Tx) error {
		var providerID string
		err := tx.QueryRowContext(ctx, `SELECT provider_id FROM provider_credentials WHERE id = ?`, id).Scan(&providerID)
		if errors.Is(err, sql.ErrNoRows) {
			return provider.ErrNotFound(id)
		}
		if err != nil {
			return fmt.Errorf("sqlite: load provider credential: %w", err)
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE provider_credentials SET is_active = 0 WHERE provider_id = ? AND is_active = 1`, providerID); err != nil {
			return fmt.Errorf("sqlite: deactivate: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE provider_credentials SET is_active = 1 WHERE id = ?`, id); err != nil {
			return fmt.Errorf("sqlite: activate: %w", err)
		}
		return nil
	})
}

// ---- custom providers ----

// ListCustom implements provider.Repository.
func (r *ProviderRepository) ListCustom(ctx context.Context) ([]provider.CustomProvider, error) {
	rows, err := r.db.sql.QueryContext(ctx,
		`SELECT id, name, discovery_format, base_url, key_hint, created_at, updated_at FROM custom_providers ORDER BY created_at ASC, id ASC`)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list custom providers: %w", err)
	}
	var out []provider.CustomProvider
	for rows.Next() {
		p, err := scanCustom(rows)
		if err != nil {
			rows.Close()
			return nil, fmt.Errorf("sqlite: scan custom provider: %w", err)
		}
		out = append(out, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		if out[i].Models, err = r.models(ctx, out[i].ID); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func scanCustom(s rowScanner) (provider.CustomProvider, error) {
	var p provider.CustomProvider
	var format string
	var created, updated int64
	if err := s.Scan(&p.ID, &p.Name, &format, &p.BaseURL, &p.KeyHint, &created, &updated); err != nil {
		return provider.CustomProvider{}, err
	}
	p.DiscoveryFormat = provider.DiscoveryFormat(format)
	p.CreatedAt, p.UpdatedAt = time.UnixMilli(created), time.UnixMilli(updated)
	return p, nil
}

func (r *ProviderRepository) models(ctx context.Context, id string) ([]provider.CustomModel, error) {
	rows, err := r.db.sql.QueryContext(ctx,
		`SELECT model_id, endpoint, enabled, is_default FROM custom_provider_models WHERE provider_id = ? ORDER BY position ASC`, id)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list custom models: %w", err)
	}
	defer rows.Close()
	var out []provider.CustomModel
	for rows.Next() {
		var m provider.CustomModel
		var endpoint string
		var enabled, def int
		if err := rows.Scan(&m.ModelID, &endpoint, &enabled, &def); err != nil {
			return nil, fmt.Errorf("sqlite: scan custom model: %w", err)
		}
		m.Endpoint, m.Enabled, m.IsDefault = provider.Endpoint(endpoint), enabled == 1, def == 1
		out = append(out, m)
	}
	return out, rows.Err()
}

// GetCustom implements provider.Repository.
func (r *ProviderRepository) GetCustom(ctx context.Context, id string) (provider.CustomProvider, error) {
	p, err := scanCustom(r.db.sql.QueryRowContext(ctx,
		`SELECT id, name, discovery_format, base_url, key_hint, created_at, updated_at FROM custom_providers WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return provider.CustomProvider{}, provider.ErrNotFound(id)
	}
	if err != nil {
		return provider.CustomProvider{}, fmt.Errorf("sqlite: get custom provider: %w", err)
	}
	p.Models, err = r.models(ctx, id)
	return p, err
}

func writeModels(ctx context.Context, tx *sql.Tx, id string, models []provider.CustomModel) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM custom_provider_models WHERE provider_id = ?`, id); err != nil {
		return fmt.Errorf("sqlite: clear custom models: %w", err)
	}
	for i, m := range models {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO custom_provider_models (provider_id, model_id, endpoint, enabled, is_default, position) VALUES (?,?,?,?,?,?)`,
			id, m.ModelID, string(m.Endpoint), boolInt(m.Enabled), boolInt(m.IsDefault), i); err != nil {
			return fmt.Errorf("sqlite: insert custom model: %w", err)
		}
	}
	return nil
}

// CreateCustom implements provider.Repository.
func (r *ProviderRepository) CreateCustom(ctx context.Context, p provider.CustomProvider) (provider.CustomProvider, error) {
	if p.ID == "" {
		p.ID = uuid.NewString()
	}
	now := time.Now()
	p.CreatedAt, p.UpdatedAt = now, now
	err := r.tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO custom_providers (id, name, discovery_format, base_url, key_hint, created_at, updated_at) VALUES (?,?,?,?,?,?,?)`,
			p.ID, p.Name, string(p.DiscoveryFormat), p.BaseURL, p.KeyHint, now.UnixMilli(), now.UnixMilli()); err != nil {
			return fmt.Errorf("sqlite: insert custom provider: %w", err)
		}
		return writeModels(ctx, tx, p.ID, p.Models)
	})
	return p, err
}

// UpdateCustom implements provider.Repository.
func (r *ProviderRepository) UpdateCustom(ctx context.Context, p provider.CustomProvider) error {
	return r.tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			`UPDATE custom_providers SET name = ?, discovery_format = ?, base_url = ?, key_hint = ?, updated_at = ? WHERE id = ?`,
			p.Name, string(p.DiscoveryFormat), p.BaseURL, p.KeyHint, time.Now().UnixMilli(), p.ID)
		if err != nil {
			return fmt.Errorf("sqlite: update custom provider: %w", err)
		}
		if err := affected(res, p.ID); err != nil {
			return err
		}
		return writeModels(ctx, tx, p.ID, p.Models)
	})
}

// DeleteCustom implements provider.Repository.
func (r *ProviderRepository) DeleteCustom(ctx context.Context, id string) error {
	return r.tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM custom_provider_models WHERE provider_id = ?`, id); err != nil {
			return fmt.Errorf("sqlite: delete custom models: %w", err)
		}
		res, err := tx.ExecContext(ctx, `DELETE FROM custom_providers WHERE id = ?`, id)
		if err != nil {
			return fmt.Errorf("sqlite: delete custom provider: %w", err)
		}
		return affected(res, id)
	})
}

func affected(res sql.Result, id string) error {
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("sqlite: rows affected: %w", err)
	}
	if n == 0 {
		return provider.ErrNotFound(id)
	}
	return nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
