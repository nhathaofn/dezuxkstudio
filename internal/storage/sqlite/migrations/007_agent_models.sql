-- 007_agent_models.sql
-- Persist the user's selected model and reasoning effort per agent provider.

ALTER TABLE agent_providers ADD COLUMN model TEXT NOT NULL DEFAULT '';
ALTER TABLE agent_providers ADD COLUMN effort TEXT NOT NULL DEFAULT '';
