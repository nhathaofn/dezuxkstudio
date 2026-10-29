-- 001_initial_schema.sql
-- Base schema for DezuxkStudio desktop application

CREATE TABLE IF NOT EXISTS app_metadata (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS user_settings (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- Seed initial app metadata
INSERT OR IGNORE INTO app_metadata (key, value) VALUES ('schema_version', '1');
INSERT OR IGNORE INTO app_metadata (key, value) VALUES ('installation_time', CURRENT_TIMESTAMP);
