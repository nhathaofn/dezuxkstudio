-- 005_agent_providers.sql
-- Store user-configured custom executable paths for ACP agent providers.

CREATE TABLE IF NOT EXISTS agent_providers (
    id TEXT PRIMARY KEY,
    custom_path TEXT NOT NULL DEFAULT '',
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
