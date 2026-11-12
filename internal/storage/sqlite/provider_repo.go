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

const healthColumns = `last_outcome, last_detail, last_tested_at`

type rowScanner interface{ Scan(dest ...any) error }

func scanCredential(s rowScanner) (provider.Credential, error) {
	var (
		c                provider.Credential
		values, hints    string
		active           int
		created, updated int64
	)
	var outcome, detail string
	var tested int64
	if err := s.Scan(&c.ID, &c.ProviderID, &c.Name, &values, &hints, &active, &created, &updated, &outcome, &detail, &tested); err != nil {
		return provider.Credential{}, err
	}
	c.Values = map[string]string{}
	c.SecretHints = map[string]string{}
	_ = json.Unmarshal([]byte(values), &c.Values)
	_ = json.Unmarshal([]byte(hints), &c.SecretHints)
	c.Active = active == 1
	c.CreatedAt = time.UnixMilli(created)
	c.UpdatedAt = time.UnixMilli(updated)
	c.Health = health(outcome, detail, tested)
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
		`SELECT `+credentialColumns+`, `+healthColumns+` FROM provider_credentials ORDER BY provider_id, created_at ASC, id ASC`)
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
		`SELECT `+credentialColumns+`, `+healthColumns+` FROM provider_credentials WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return provider.Credential{}, provider.ErrNotFound(id)
	}
	if err != nil {
		return provider.Credential{}, fmt.Errorf("sqlite: get provider credential: %w", err)
	}
	return c, nil
}

// CreateCredential implements provider.Repository.
func (r *ProviderRepository) CreateCredential(ctx context.Context, c provider.Credential) (provider.Credential, error) {
	if c.ID == "" {
		c.ID = uuid.NewString()
	}
	now := time.Now()
	c.CreatedAt, c.UpdatedAt = now, now
	err := r.db.inTx(ctx, func(tx *sql.Tx) error {
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
	return r.db.inTx(ctx, func(tx *sql.Tx) error {
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
	return r.db.inTx(ctx, func(tx *sql.Tx) error {
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
		`SELECT id, name, discovery_format, base_url, key_hint, created_at, updated_at, `+healthColumns+` FROM custom_providers ORDER BY created_at ASC, id ASC`)
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
		if err := r.fillCustom(ctx, &out[i]); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func scanCustom(s rowScanner) (provider.CustomProvider, error) {
	var p provider.CustomProvider
	var format string
	var created, updated, tested int64
	var outcome, detail string
	if err := s.Scan(&p.ID, &p.Name, &format, &p.BaseURL, &p.KeyHint, &created, &updated, &outcome, &detail, &tested); err != nil {
		return provider.CustomProvider{}, err
	}
	p.DiscoveryFormat = provider.DiscoveryFormat(format)
	p.CreatedAt, p.UpdatedAt = time.UnixMilli(created), time.UnixMilli(updated)
	p.Health = health(outcome, detail, tested)
	return p, nil
}

// querier is satisfied by *sql.DB and *sql.Tx.
type querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func listModels(ctx context.Context, q querier, ownerID string) ([]provider.Model, error) {
	rows, err := q.QueryContext(ctx, `SELECT model_id, capability, endpoint, display_name, enabled, is_default, user_added, facts_json
		FROM provider_models WHERE owner_id = ? ORDER BY position ASC, model_id ASC`, ownerID)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list provider models: %w", err)
	}
	defer rows.Close()
	out := []provider.Model{}
	for rows.Next() {
		var m provider.Model
		var capability, endpoint, facts string
		var enabled, def, user int
		if err := rows.Scan(&m.ModelID, &capability, &endpoint, &m.DisplayName, &enabled, &def, &user, &facts); err != nil {
			return nil, fmt.Errorf("sqlite: scan provider model: %w", err)
		}
		m.Capability, m.Endpoint = provider.Capability(capability), provider.Endpoint(endpoint)
		m.Enabled, m.IsDefault, m.UserAdded = enabled == 1, def == 1, user == 1
		_ = json.Unmarshal([]byte(facts), &m.Facts)
		out = append(out, m)
	}
	return out, rows.Err()
}

func writeModels(ctx context.Context, q querier, ownerID string, models []provider.Model) error {
	if _, err := q.ExecContext(ctx, `DELETE FROM provider_models WHERE owner_id = ?`, ownerID); err != nil {
		return fmt.Errorf("sqlite: clear provider models: %w", err)
	}
	for i, m := range models {
		facts, err := json.Marshal(m.Facts)
		if err != nil {
			return fmt.Errorf("sqlite: encode model facts: %w", err)
		}
		if _, err := q.ExecContext(ctx, `INSERT INTO provider_models
			(owner_id, model_id, capability, endpoint, display_name, enabled, is_default, user_added, facts_json, position)
			VALUES (?,?,?,?,?,?,?,?,?,?)`,
			ownerID, m.ModelID, string(m.Capability), string(m.Endpoint), m.DisplayName,
			boolInt(m.Enabled), boolInt(m.IsDefault), boolInt(m.UserAdded), string(facts), i); err != nil {
			return fmt.Errorf("sqlite: insert provider model: %w", err)
		}
	}
	return nil
}

// ListModels implements provider.Repository.
func (r *ProviderRepository) ListModels(ctx context.Context, ownerID string) ([]provider.Model, error) {
	return listModels(ctx, r.db.sql, ownerID)
}

// ReplaceModels implements provider.Repository.
func (r *ProviderRepository) ReplaceModels(ctx context.Context, ownerID string, models []provider.Model) error {
	return r.db.inTx(ctx, func(tx *sql.Tx) error { return writeModels(ctx, tx, ownerID, models) })
}

// GetSettings implements provider.Repository.
func (r *ProviderRepository) GetSettings(ctx context.Context, ownerID string) (provider.Settings, error) {
	rows, err := r.db.sql.QueryContext(ctx, `SELECT key, value FROM provider_settings WHERE owner_id = ?`, ownerID)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list provider settings: %w", err)
	}
	defer rows.Close()
	out := provider.Settings{}
	for rows.Next() {
		var k string
		var v int
		if err := rows.Scan(&k, &v); err != nil {
			return nil, fmt.Errorf("sqlite: scan provider setting: %w", err)
		}
		out[k] = v
	}
	return out, rows.Err()
}

// UpdateSettings implements provider.Repository.
func (r *ProviderRepository) UpdateSettings(ctx context.Context, ownerID string, set provider.Settings, clear []string) error {
	return r.db.inTx(ctx, func(tx *sql.Tx) error {
		for k, v := range set {
			if _, err := tx.ExecContext(ctx, `INSERT INTO provider_settings (owner_id, key, value) VALUES (?,?,?)
				ON CONFLICT(owner_id, key) DO UPDATE SET value = excluded.value`, ownerID, k, v); err != nil {
				return fmt.Errorf("sqlite: set provider setting: %w", err)
			}
		}
		for _, k := range clear {
			if _, err := tx.ExecContext(ctx, `DELETE FROM provider_settings WHERE owner_id = ? AND key = ?`, ownerID, k); err != nil {
				return fmt.Errorf("sqlite: clear provider setting: %w", err)
			}
		}
		return nil
	})
}

// GetDefaults implements provider.Repository.
func (r *ProviderRepository) GetDefaults(ctx context.Context) (map[provider.Purpose]string, error) {
	rows, err := r.db.sql.QueryContext(ctx, `SELECT purpose, model_ref FROM provider_defaults`)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list provider defaults: %w", err)
	}
	defer rows.Close()
	out := map[provider.Purpose]string{}
	for rows.Next() {
		var p, ref string
		if err := rows.Scan(&p, &ref); err != nil {
			return nil, fmt.Errorf("sqlite: scan provider default: %w", err)
		}
		out[provider.Purpose(p)] = ref
	}
	return out, rows.Err()
}

// SetDefault implements provider.Repository ("" removes the choice, i.e. back to auto).
func (r *ProviderRepository) SetDefault(ctx context.Context, p provider.Purpose, ref string) error {
	var err error
	if ref == "" {
		_, err = r.db.sql.ExecContext(ctx, `DELETE FROM provider_defaults WHERE purpose = ?`, string(p))
	} else {
		_, err = r.db.sql.ExecContext(ctx, `INSERT INTO provider_defaults (purpose, model_ref) VALUES (?, ?)
			ON CONFLICT(purpose) DO UPDATE SET model_ref = excluded.model_ref`, string(p), ref)
	}
	if err != nil {
		return fmt.Errorf("sqlite: set provider default: %w", err)
	}
	return nil
}

func (r *ProviderRepository) fillCustom(ctx context.Context, p *provider.CustomProvider) error {
	var err error
	if p.Models, err = r.ListModels(ctx, p.ID); err != nil {
		return err
	}
	p.Settings, err = r.GetSettings(ctx, p.ID)
	return err
}

// GetCustom implements provider.Repository.
func (r *ProviderRepository) GetCustom(ctx context.Context, id string) (provider.CustomProvider, error) {
	p, err := scanCustom(r.db.sql.QueryRowContext(ctx,
		`SELECT id, name, discovery_format, base_url, key_hint, created_at, updated_at, `+healthColumns+` FROM custom_providers WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return provider.CustomProvider{}, provider.ErrNotFound(id)
	}
	if err != nil {
		return provider.CustomProvider{}, fmt.Errorf("sqlite: get custom provider: %w", err)
	}
	return p, r.fillCustom(ctx, &p)
}

// CreateCustom implements provider.Repository.
func (r *ProviderRepository) CreateCustom(ctx context.Context, p provider.CustomProvider) (provider.CustomProvider, error) {
	if p.ID == "" {
		p.ID = uuid.NewString()
	}
	now := time.Now()
	p.CreatedAt, p.UpdatedAt = now, now
	err := r.db.inTx(ctx, func(tx *sql.Tx) error {
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
	return r.db.inTx(ctx, func(tx *sql.Tx) error {
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
	return r.db.inTx(ctx, func(tx *sql.Tx) error {
		for _, table := range []string{"provider_models", "provider_settings"} {
			if _, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE owner_id = ?`, id); err != nil {
				return fmt.Errorf("sqlite: delete custom provider %s: %w", table, err)
			}
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

func health(outcome, detail string, tested int64) provider.Health {
	h := provider.Health{Outcome: provider.Outcome(outcome), Detail: detail}
	if tested > 0 {
		h.TestedAt = time.UnixMilli(tested)
	}
	return h
}

func healthArgs(h provider.Health) (string, string, int64) {
	var at int64
	if !h.TestedAt.IsZero() {
		at = h.TestedAt.UnixMilli()
	}
	return string(h.Outcome), h.Detail, at
}

// SetCredentialHealth implements provider.Repository.
func (r *ProviderRepository) SetCredentialHealth(ctx context.Context, id string, h provider.Health) error {
	o, d, at := healthArgs(h)
	res, err := r.db.sql.ExecContext(ctx,
		`UPDATE provider_credentials SET last_outcome = ?, last_detail = ?, last_tested_at = ? WHERE id = ?`, o, d, at, id)
	if err != nil {
		return fmt.Errorf("sqlite: set credential health: %w", err)
	}
	return affected(res, id)
}

// SetCustomHealth implements provider.Repository.
func (r *ProviderRepository) SetCustomHealth(ctx context.Context, id string, h provider.Health) error {
	o, d, at := healthArgs(h)
	res, err := r.db.sql.ExecContext(ctx,
		`UPDATE custom_providers SET last_outcome = ?, last_detail = ?, last_tested_at = ? WHERE id = ?`, o, d, at, id)
	if err != nil {
		return fmt.Errorf("sqlite: set custom health: %w", err)
	}
	return affected(res, id)
}
