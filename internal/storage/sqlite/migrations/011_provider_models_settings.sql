-- 011_provider_models_settings.sql
-- One model table and one settings table for built-in AND custom providers (owner_id = registry id or
-- custom provider id), plus the user's default model per purpose.

CREATE TABLE IF NOT EXISTS provider_models (
    owner_id TEXT NOT NULL,
    model_id TEXT NOT NULL,
    capability TEXT NOT NULL,
    endpoint TEXT NOT NULL DEFAULT '',
    display_name TEXT NOT NULL DEFAULT '',
    enabled INTEGER NOT NULL DEFAULT 1,
    is_default INTEGER NOT NULL DEFAULT 0,
    user_added INTEGER NOT NULL DEFAULT 0,
    facts_json TEXT NOT NULL DEFAULT '{}',
    position INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (owner_id, model_id, capability)
);

-- Carry over custom provider models; the capability follows from the endpoint (catalog of 009/010).
INSERT OR IGNORE INTO provider_models (owner_id, model_id, capability, endpoint, enabled, is_default, position)
SELECT provider_id, model_id,
       CASE
           WHEN endpoint IN ('openai-images', 'openai-images-generations', 'openai-images-edits',
                             'openai-chat-image', 'openrouter-images', 'gemini-image') THEN 'image'
           WHEN endpoint IN ('openai-video', 'gemini-video') THEN 'video'
           WHEN endpoint IN ('openai-tts', 'gemini-tts') THEN 'tts'
           ELSE 'llm'
       END,
       endpoint, enabled, is_default, position
FROM custom_provider_models;

DROP TABLE IF EXISTS custom_provider_models;

CREATE TABLE IF NOT EXISTS provider_settings (
    owner_id TEXT NOT NULL,
    key TEXT NOT NULL,
    value INTEGER NOT NULL,
    PRIMARY KEY (owner_id, key)
);

CREATE TABLE IF NOT EXISTS provider_defaults (
    purpose TEXT PRIMARY KEY,
    model_ref TEXT NOT NULL
);
