-- 009_providers.sql
-- API provider credentials (built-in registry providers) and user-defined custom providers.
-- Secret values are NOT stored here: they live in the OS credential store (internal/secrets).
-- secret_hints_json only keeps masked previews ("sk-…abcd") for display.

CREATE TABLE IF NOT EXISTS provider_credentials (
    id TEXT PRIMARY KEY,
    provider_id TEXT NOT NULL,
    name TEXT NOT NULL,
    values_json TEXT NOT NULL DEFAULT '{}',
    secret_hints_json TEXT NOT NULL DEFAULT '{}',
    is_active INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_provider_credentials_provider
    ON provider_credentials (provider_id, created_at ASC);

-- At most one active credential per provider.
CREATE UNIQUE INDEX IF NOT EXISTS uq_provider_credentials_active
    ON provider_credentials (provider_id) WHERE is_active = 1;

CREATE TABLE IF NOT EXISTS custom_providers (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    discovery_format TEXT NOT NULL,
    base_url TEXT NOT NULL,
    key_hint TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS custom_provider_models (
    provider_id TEXT NOT NULL REFERENCES custom_providers(id) ON DELETE CASCADE,
    model_id TEXT NOT NULL,
    endpoint TEXT NOT NULL,
    enabled INTEGER NOT NULL DEFAULT 1,
    is_default INTEGER NOT NULL DEFAULT 0,
    position INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (provider_id, model_id, endpoint)
);
