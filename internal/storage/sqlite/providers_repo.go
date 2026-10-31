package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"app/internal/domain/providers"
	"app/internal/security"
)

// ProvidersRepository implements providers.Repository using SQLite with transparent DPAPI encryption.
type ProvidersRepository struct {
	db *DB
}

var _ providers.Repository = (*ProvidersRepository)(nil)

// NewProvidersRepository creates a new ProvidersRepository.
func NewProvidersRepository(db *DB) *ProvidersRepository {
	return &ProvidersRepository{db: db}
}

// GetProvider retrieves a provider by ID.
func (r *ProvidersRepository) GetProvider(ctx context.Context, id string) (*providers.Provider, error) {
	row := r.db.sql.QueryRowContext(ctx, `
		SELECT id, name, base_url, auth_domain, allowed_domains_json, probe_rules_json, security_rules_json, status, created_at, updated_at
		FROM dynamic_providers WHERE id = ?
	`, id)

	var p providers.Provider
	var allowedJSON, probeJSON, secJSON string
	var createdAt, updatedAt time.Time

	err := row.Scan(&p.ID, &p.Name, &p.BaseURL, &p.AuthDomain, &allowedJSON, &probeJSON, &secJSON, &p.Status, &createdAt, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("sqlite: get provider %s: %w", id, err)
	}

	p.CreatedAt = createdAt
	p.UpdatedAt = updatedAt
	if err := json.Unmarshal([]byte(allowedJSON), &p.AllowedDomains); err != nil {
		p.AllowedDomains = []string{}
	}
	if err := json.Unmarshal([]byte(probeJSON), &p.ProbeRules); err != nil {
		p.ProbeRules = providers.ProbeRules{}
	}
	if err := json.Unmarshal([]byte(secJSON), &p.SecurityRules); err != nil {
		p.SecurityRules = map[string]any{}
	}

	return &p, nil
}

// ListProviders returns all registered dynamic providers.
func (r *ProvidersRepository) ListProviders(ctx context.Context) ([]providers.Provider, error) {
	rows, err := r.db.sql.QueryContext(ctx, `
		SELECT id, name, base_url, auth_domain, allowed_domains_json, probe_rules_json, security_rules_json, status, created_at, updated_at
		FROM dynamic_providers ORDER BY name ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list providers: %w", err)
	}
	defer rows.Close()

	var result []providers.Provider
	for rows.Next() {
		var p providers.Provider
		var allowedJSON, probeJSON, secJSON string
		var createdAt, updatedAt time.Time
		if err := rows.Scan(&p.ID, &p.Name, &p.BaseURL, &p.AuthDomain, &allowedJSON, &probeJSON, &secJSON, &p.Status, &createdAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("sqlite: scan provider: %w", err)
		}
		p.CreatedAt = createdAt
		p.UpdatedAt = updatedAt
		_ = json.Unmarshal([]byte(allowedJSON), &p.AllowedDomains)
		_ = json.Unmarshal([]byte(probeJSON), &p.ProbeRules)
		_ = json.Unmarshal([]byte(secJSON), &p.SecurityRules)
		result = append(result, p)
	}
	return result, rows.Err()
}

// SaveProvider inserts or updates a provider blueprint.
func (r *ProvidersRepository) SaveProvider(ctx context.Context, p *providers.Provider) error {
	allowedJSON, err := json.Marshal(p.AllowedDomains)
	if err != nil {
		return fmt.Errorf("sqlite: marshal allowed domains: %w", err)
	}
	probeJSON, err := json.Marshal(p.ProbeRules)
	if err != nil {
		return fmt.Errorf("sqlite: marshal probe rules: %w", err)
	}
	secJSON, err := json.Marshal(p.SecurityRules)
	if err != nil {
		return fmt.Errorf("sqlite: marshal security rules: %w", err)
	}

	_, err = r.db.sql.ExecContext(ctx, `
		INSERT INTO dynamic_providers (id, name, base_url, auth_domain, allowed_domains_json, probe_rules_json, security_rules_json, status, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(id) DO UPDATE SET
			name = excluded.name,
			base_url = excluded.base_url,
			auth_domain = excluded.auth_domain,
			allowed_domains_json = excluded.allowed_domains_json,
			probe_rules_json = excluded.probe_rules_json,
			security_rules_json = excluded.security_rules_json,
			status = excluded.status,
			updated_at = CURRENT_TIMESTAMP
	`, p.ID, p.Name, p.BaseURL, p.AuthDomain, string(allowedJSON), string(probeJSON), string(secJSON), string(p.Status))
	if err != nil {
		return fmt.Errorf("sqlite: save provider %s: %w", p.ID, err)
	}
	return nil
}

// DeleteProvider deletes a provider by ID (cascades to accounts and secrets).
func (r *ProvidersRepository) DeleteProvider(ctx context.Context, id string) error {
	_, err := r.db.sql.ExecContext(ctx, "DELETE FROM dynamic_providers WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("sqlite: delete provider %s: %w", id, err)
	}
	return nil
}

// GetAccount retrieves an account by ID.
func (r *ProvidersRepository) GetAccount(ctx context.Context, id string) (*providers.Account, error) {
	row := r.db.sql.QueryRowContext(ctx, `
		SELECT id, provider_id, label, identity, status, profile_dir, proxy_id, paused_until, usage_json, metadata_json, last_checked_at, created_at, updated_at
		FROM accounts WHERE id = ?
	`, id)

	var acc providers.Account
	var usageJSON, metaJSON string
	var pausedUntil, lastChecked sql.NullTime
	var createdAt, updatedAt time.Time

	err := row.Scan(&acc.ID, &acc.ProviderID, &acc.Label, &acc.Identity, &acc.Status, &acc.ProfileDir, &acc.ProxyID, &pausedUntil, &usageJSON, &metaJSON, &lastChecked, &createdAt, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("sqlite: get account %s: %w", id, err)
	}

	acc.CreatedAt = createdAt
	acc.UpdatedAt = updatedAt
	if pausedUntil.Valid {
		acc.PausedUntil = &pausedUntil.Time
	}
	if lastChecked.Valid {
		acc.LastCheckedAt = &lastChecked.Time
	}
	_ = json.Unmarshal([]byte(usageJSON), &acc.Usage)
	_ = json.Unmarshal([]byte(metaJSON), &acc.Metadata)

	return &acc, nil
}

// ListAccounts lists all accounts for a specific provider (or all providers if providerID is empty).
func (r *ProvidersRepository) ListAccounts(ctx context.Context, providerID string) ([]providers.Account, error) {
	query := `
		SELECT id, provider_id, label, identity, status, profile_dir, proxy_id, paused_until, usage_json, metadata_json, last_checked_at, created_at, updated_at
		FROM accounts
	`
	var args []any
	if providerID != "" {
		query += " WHERE provider_id = ?"
		args = append(args, providerID)
	}
	query += " ORDER BY label ASC"

	rows, err := r.db.sql.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list accounts: %w", err)
	}
	defer rows.Close()

	var result []providers.Account
	for rows.Next() {
		var acc providers.Account
		var usageJSON, metaJSON string
		var pausedUntil, lastChecked sql.NullTime
		var createdAt, updatedAt time.Time

		if err := rows.Scan(&acc.ID, &acc.ProviderID, &acc.Label, &acc.Identity, &acc.Status, &acc.ProfileDir, &acc.ProxyID, &pausedUntil, &usageJSON, &metaJSON, &lastChecked, &createdAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("sqlite: scan account: %w", err)
		}
		acc.CreatedAt = createdAt
		acc.UpdatedAt = updatedAt
		if pausedUntil.Valid {
			acc.PausedUntil = &pausedUntil.Time
		}
		if lastChecked.Valid {
			acc.LastCheckedAt = &lastChecked.Time
		}
		_ = json.Unmarshal([]byte(usageJSON), &acc.Usage)
		_ = json.Unmarshal([]byte(metaJSON), &acc.Metadata)
		result = append(result, acc)
	}
	return result, rows.Err()
}

// SaveAccount inserts or updates an account.
func (r *ProvidersRepository) SaveAccount(ctx context.Context, acc *providers.Account) error {
	usageJSON, err := json.Marshal(acc.Usage)
	if err != nil {
		return fmt.Errorf("sqlite: marshal usage: %w", err)
	}
	metaJSON, err := json.Marshal(acc.Metadata)
	if err != nil {
		return fmt.Errorf("sqlite: marshal metadata: %w", err)
	}

	_, err = r.db.sql.ExecContext(ctx, `
		INSERT INTO accounts (id, provider_id, label, identity, status, profile_dir, proxy_id, paused_until, usage_json, metadata_json, last_checked_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(id) DO UPDATE SET
			provider_id = excluded.provider_id,
			label = excluded.label,
			identity = excluded.identity,
			status = excluded.status,
			profile_dir = excluded.profile_dir,
			proxy_id = excluded.proxy_id,
			paused_until = excluded.paused_until,
			usage_json = excluded.usage_json,
			metadata_json = excluded.metadata_json,
			last_checked_at = excluded.last_checked_at,
			updated_at = CURRENT_TIMESTAMP
	`, acc.ID, acc.ProviderID, acc.Label, acc.Identity, string(acc.Status), acc.ProfileDir, acc.ProxyID, acc.PausedUntil, string(usageJSON), string(metaJSON), acc.LastCheckedAt)
	if err != nil {
		return fmt.Errorf("sqlite: save account %s: %w", acc.ID, err)
	}
	return nil
}

// DeleteAccount deletes an account by ID (cascades to secrets).
func (r *ProvidersRepository) DeleteAccount(ctx context.Context, id string) error {
	_, err := r.db.sql.ExecContext(ctx, "DELETE FROM accounts WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("sqlite: delete account %s: %w", id, err)
	}
	return nil
}

// UpdateAccountStatus updates only the status and paused_until of an account.
func (r *ProvidersRepository) UpdateAccountStatus(ctx context.Context, id string, status providers.AccountStatus, pausedUntil *time.Time) error {
	_, err := r.db.sql.ExecContext(ctx, `
		UPDATE accounts SET status = ?, paused_until = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?
	`, string(status), pausedUntil, id)
	if err != nil {
		return fmt.Errorf("sqlite: update account status %s: %w", id, err)
	}
	return nil
}

// GetAccountSecrets retrieves and transparently decrypts cookies and tokens with Windows DPAPI.
func (r *ProvidersRepository) GetAccountSecrets(ctx context.Context, accountID string) (*providers.AccountSecrets, error) {
	row := r.db.sql.QueryRowContext(ctx, `
		SELECT encrypted_cookies_blob, encrypted_tokens_blob, cookie_hash, updated_at
		FROM account_secrets WHERE account_id = ?
	`, accountID)

	var encryptedCookies, encryptedTokens []byte
	var cookieHash string
	var updatedAt time.Time

	err := row.Scan(&encryptedCookies, &encryptedTokens, &cookieHash, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("sqlite: get account secrets for %s: %w", accountID, err)
	}

	sec := &providers.AccountSecrets{
		AccountID:  accountID,
		CookieHash: cookieHash,
		UpdatedAt:  updatedAt,
		Cookies:    []providers.Cookie{},
		Tokens:     map[string]string{},
	}

	if len(encryptedCookies) > 0 {
		plainCookies, err := security.DecryptDPAPI(encryptedCookies)
		if err != nil {
			return nil, fmt.Errorf("sqlite: decrypt cookies for %s: %w", accountID, err)
		}
		if err := json.Unmarshal(plainCookies, &sec.Cookies); err != nil {
			return nil, fmt.Errorf("sqlite: unmarshal cookies for %s: %w", accountID, err)
		}
	}

	if len(encryptedTokens) > 0 {
		plainTokens, err := security.DecryptDPAPI(encryptedTokens)
		if err != nil {
			return nil, fmt.Errorf("sqlite: decrypt tokens for %s: %w", accountID, err)
		}
		if err := json.Unmarshal(plainTokens, &sec.Tokens); err != nil {
			return nil, fmt.Errorf("sqlite: unmarshal tokens for %s: %w", accountID, err)
		}
	}

	return sec, nil
}

// SaveAccountSecrets transparently encrypts cookies and tokens with Windows DPAPI before storing.
func (r *ProvidersRepository) SaveAccountSecrets(ctx context.Context, sec *providers.AccountSecrets) error {
	cookiesJSON, err := json.Marshal(sec.Cookies)
	if err != nil {
		return fmt.Errorf("sqlite: marshal cookies: %w", err)
	}
	tokensJSON, err := json.Marshal(sec.Tokens)
	if err != nil {
		return fmt.Errorf("sqlite: marshal tokens: %w", err)
	}

	encryptedCookies, err := security.EncryptDPAPI(cookiesJSON)
	if err != nil {
		return fmt.Errorf("sqlite: encrypt cookies with DPAPI: %w", err)
	}

	encryptedTokens, err := security.EncryptDPAPI(tokensJSON)
	if err != nil {
		return fmt.Errorf("sqlite: encrypt tokens with DPAPI: %w", err)
	}

	_, err = r.db.sql.ExecContext(ctx, `
		INSERT INTO account_secrets (account_id, encrypted_cookies_blob, encrypted_tokens_blob, cookie_hash, updated_at)
		VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(account_id) DO UPDATE SET
			encrypted_cookies_blob = excluded.encrypted_cookies_blob,
			encrypted_tokens_blob = excluded.encrypted_tokens_blob,
			cookie_hash = excluded.cookie_hash,
			updated_at = CURRENT_TIMESTAMP
	`, sec.AccountID, encryptedCookies, encryptedTokens, sec.CookieHash)
	if err != nil {
		return fmt.Errorf("sqlite: save account secrets for %s: %w", sec.AccountID, err)
	}
	return nil
}

// ListFeatures returns features registered for a provider.
func (r *ProvidersRepository) ListFeatures(ctx context.Context, providerID string) ([]providers.ProviderFeature, error) {
	rows, err := r.db.sql.QueryContext(ctx, `
		SELECT id, provider_id, feature_key, name, zone_type, dag_node_type, execution_channel, inputs_schema_json, outputs_schema_json, params_schema_json, created_at
		FROM provider_features WHERE provider_id = ? ORDER BY name ASC
	`, providerID)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list features for %s: %w", providerID, err)
	}
	defer rows.Close()

	var result []providers.ProviderFeature
	for rows.Next() {
		var f providers.ProviderFeature
		var inJSON, outJSON, paramJSON string
		if err := rows.Scan(&f.ID, &f.ProviderID, &f.FeatureKey, &f.Name, &f.ZoneType, &f.DAGNodeType, &f.ExecutionChannel, &inJSON, &outJSON, &paramJSON, &f.CreatedAt); err != nil {
			return nil, fmt.Errorf("sqlite: scan feature: %w", err)
		}
		f.InputsSchema = json.RawMessage(inJSON)
		f.OutputsSchema = json.RawMessage(outJSON)
		f.ParamsSchema = json.RawMessage(paramJSON)
		result = append(result, f)
	}
	return result, rows.Err()
}

// SaveFeature inserts or updates a provider feature.
func (r *ProvidersRepository) SaveFeature(ctx context.Context, f *providers.ProviderFeature) error {
	inJSON := string(f.InputsSchema)
	if inJSON == "" {
		inJSON = "[]"
	}
	outJSON := string(f.OutputsSchema)
	if outJSON == "" {
		outJSON = "[]"
	}
	paramJSON := string(f.ParamsSchema)
	if paramJSON == "" {
		paramJSON = "[]"
	}

	_, err := r.db.sql.ExecContext(ctx, `
		INSERT INTO provider_features (id, provider_id, feature_key, name, zone_type, dag_node_type, execution_channel, inputs_schema_json, outputs_schema_json, params_schema_json)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			provider_id = excluded.provider_id,
			feature_key = excluded.feature_key,
			name = excluded.name,
			zone_type = excluded.zone_type,
			dag_node_type = excluded.dag_node_type,
			execution_channel = excluded.execution_channel,
			inputs_schema_json = excluded.inputs_schema_json,
			outputs_schema_json = excluded.outputs_schema_json,
			params_schema_json = excluded.params_schema_json
	`, f.ID, f.ProviderID, f.FeatureKey, f.Name, string(f.ZoneType), f.DAGNodeType, string(f.ExecutionChannel), inJSON, outJSON, paramJSON)
	if err != nil {
		return fmt.Errorf("sqlite: save feature %s: %w", f.ID, err)
	}
	return nil
}

// GetEndpoint retrieves a dynamic endpoint by providerID and featureKey.
func (r *ProvidersRepository) GetEndpoint(ctx context.Context, providerID, featureKey string) (*providers.DynamicEndpoint, error) {
	row := r.db.sql.QueryRowContext(ctx, `
		SELECT id, provider_id, feature_key, http_method, url_template, headers_template_json, rpc_method_name, resolved_rpc_id, resolution_strategy, expires_at, updated_at
		FROM dynamic_endpoints WHERE provider_id = ? AND feature_key = ?
	`, providerID, featureKey)

	var ep providers.DynamicEndpoint
	var headersJSON string
	var expiresAt sql.NullTime

	err := row.Scan(&ep.ID, &ep.ProviderID, &ep.FeatureKey, &ep.HTTPMethod, &ep.URLTemplate, &headersJSON, &ep.RPCMethodName, &ep.ResolvedRPCID, &ep.ResolutionStrategy, &expiresAt, &ep.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("sqlite: get endpoint %s/%s: %w", providerID, featureKey, err)
	}

	if expiresAt.Valid {
		ep.ExpiresAt = &expiresAt.Time
	}
	_ = json.Unmarshal([]byte(headersJSON), &ep.HeadersTemplate)
	return &ep, nil
}

// SaveEndpoint inserts or updates a dynamic endpoint.
func (r *ProvidersRepository) SaveEndpoint(ctx context.Context, ep *providers.DynamicEndpoint) error {
	headersJSON, err := json.Marshal(ep.HeadersTemplate)
	if err != nil {
		return fmt.Errorf("sqlite: marshal headers template: %w", err)
	}

	_, err = r.db.sql.ExecContext(ctx, `
		INSERT INTO dynamic_endpoints (id, provider_id, feature_key, http_method, url_template, headers_template_json, rpc_method_name, resolved_rpc_id, resolution_strategy, expires_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(id) DO UPDATE SET
			provider_id = excluded.provider_id,
			feature_key = excluded.feature_key,
			http_method = excluded.http_method,
			url_template = excluded.url_template,
			headers_template_json = excluded.headers_template_json,
			rpc_method_name = excluded.rpc_method_name,
			resolved_rpc_id = excluded.resolved_rpc_id,
			resolution_strategy = excluded.resolution_strategy,
			expires_at = excluded.expires_at,
			updated_at = CURRENT_TIMESTAMP
	`, ep.ID, ep.ProviderID, ep.FeatureKey, ep.HTTPMethod, ep.URLTemplate, string(headersJSON), ep.RPCMethodName, ep.ResolvedRPCID, ep.ResolutionStrategy, ep.ExpiresAt)
	if err != nil {
		return fmt.Errorf("sqlite: save endpoint %s: %w", ep.ID, err)
	}
	return nil
}

// ListCookieRules returns cookie partition rules for a provider.
func (r *ProvidersRepository) ListCookieRules(ctx context.Context, providerID string) ([]providers.CookiePartitionRule, error) {
	rows, err := r.db.sql.QueryContext(ctx, `
		SELECT id, provider_id, zone_type, domain_pattern, name_pattern, is_required
		FROM cookie_partition_rules WHERE provider_id = ?
	`, providerID)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list cookie rules for %s: %w", providerID, err)
	}
	defer rows.Close()

	var result []providers.CookiePartitionRule
	for rows.Next() {
		var rule providers.CookiePartitionRule
		var reqInt int
		if err := rows.Scan(&rule.ID, &rule.ProviderID, &rule.ZoneType, &rule.DomainPattern, &rule.NamePattern, &reqInt); err != nil {
			return nil, fmt.Errorf("sqlite: scan cookie rule: %w", err)
		}
		rule.IsRequired = reqInt == 1
		result = append(result, rule)
	}
	return result, rows.Err()
}

// SaveCookieRule inserts or updates a cookie partition rule.
func (r *ProvidersRepository) SaveCookieRule(ctx context.Context, rule *providers.CookiePartitionRule) error {
	reqInt := 0
	if rule.IsRequired {
		reqInt = 1
	}

	_, err := r.db.sql.ExecContext(ctx, `
		INSERT INTO cookie_partition_rules (id, provider_id, zone_type, domain_pattern, name_pattern, is_required)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			provider_id = excluded.provider_id,
			zone_type = excluded.zone_type,
			domain_pattern = excluded.domain_pattern,
			name_pattern = excluded.name_pattern,
			is_required = excluded.is_required
	`, rule.ID, rule.ProviderID, string(rule.ZoneType), rule.DomainPattern, rule.NamePattern, reqInt)
	if err != nil {
		return fmt.Errorf("sqlite: save cookie rule %s: %w", rule.ID, err)
	}
	return nil
}
