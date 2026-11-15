-- 017_agent_provider_links.sql
-- Opt-in provider credential links for agent provider runtime.
-- Maps agent providers to existing provider credentials without storing raw secrets.

CREATE TABLE IF NOT EXISTS agent_provider_links (
    agent_provider_id TEXT PRIMARY KEY,
    provider_id       TEXT NOT NULL,
    credential_id     TEXT NOT NULL,
    auth_method       TEXT NOT NULL,
    updated_at        INTEGER NOT NULL
);
