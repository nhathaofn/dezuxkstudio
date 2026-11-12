-- 010_provider_health.sql
-- Result of the latest connectivity test of each credential / custom provider (manual or at start-up),
-- so the settings screen can flag dead keys before a new test finishes. Never holds a secret.

ALTER TABLE provider_credentials ADD COLUMN last_outcome TEXT NOT NULL DEFAULT '';
ALTER TABLE provider_credentials ADD COLUMN last_detail TEXT NOT NULL DEFAULT '';
ALTER TABLE provider_credentials ADD COLUMN last_tested_at INTEGER NOT NULL DEFAULT 0;

ALTER TABLE custom_providers ADD COLUMN last_outcome TEXT NOT NULL DEFAULT '';
ALTER TABLE custom_providers ADD COLUMN last_detail TEXT NOT NULL DEFAULT '';
ALTER TABLE custom_providers ADD COLUMN last_tested_at INTEGER NOT NULL DEFAULT 0;
