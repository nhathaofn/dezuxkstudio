-- Phase 2: Durable background queue, pricing catalog, and cost ledger.

CREATE TABLE IF NOT EXISTS studio_jobs (
    id                TEXT PRIMARY KEY,
    project_id        TEXT NOT NULL REFERENCES studio_projects(id) ON DELETE CASCADE,
    episode_id        TEXT NOT NULL DEFAULT '',
    shot_id           TEXT NOT NULL DEFAULT '',
    asset_id          TEXT NOT NULL DEFAULT '',
    job_kind          TEXT NOT NULL,
    provider_id       TEXT NOT NULL DEFAULT '',
    model_ref         TEXT NOT NULL DEFAULT '',
    idempotency_key   TEXT NOT NULL UNIQUE,
    request_json      TEXT NOT NULL DEFAULT '{}',
    status            TEXT NOT NULL DEFAULT 'queued',
    provider_job_id   TEXT NOT NULL DEFAULT '',
    submit_token_json TEXT NOT NULL DEFAULT '',
    progress          INTEGER NOT NULL DEFAULT 0,
    attempts          INTEGER NOT NULL DEFAULT 0,
    max_attempts      INTEGER NOT NULL DEFAULT 3,
    next_poll_at      INTEGER NOT NULL DEFAULT 0,
    output_path       TEXT NOT NULL DEFAULT '',
    output_json       TEXT NOT NULL DEFAULT '{}',
    error_text        TEXT NOT NULL DEFAULT '',
    estimated_usd     REAL NOT NULL DEFAULT 0,
    actual_usd        REAL NOT NULL DEFAULT 0,
    created_at        INTEGER NOT NULL,
    updated_at        INTEGER NOT NULL,
    started_at        INTEGER NOT NULL DEFAULT 0,
    finished_at       INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_studio_jobs_status_poll
    ON studio_jobs(status, next_poll_at, created_at);

CREATE INDEX IF NOT EXISTS idx_studio_jobs_project_ep
    ON studio_jobs(project_id, episode_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_studio_jobs_provider_id
    ON studio_jobs(provider_job_id) WHERE provider_job_id <> '';

CREATE TABLE IF NOT EXISTS studio_pricing_catalog (
    id             TEXT PRIMARY KEY,
    provider_id    TEXT NOT NULL,
    model_id       TEXT NOT NULL,
    capability     TEXT NOT NULL,
    unit           TEXT NOT NULL,
    unit_price_usd REAL NOT NULL DEFAULT 0,
    currency       TEXT NOT NULL DEFAULT 'USD',
    updated_at     INTEGER NOT NULL,
    UNIQUE(provider_id, model_id, capability, unit)
);

CREATE TABLE IF NOT EXISTS studio_cost_ledger (
    id             TEXT PRIMARY KEY,
    project_id     TEXT NOT NULL DEFAULT '',
    episode_id     TEXT NOT NULL DEFAULT '',
    shot_id        TEXT NOT NULL DEFAULT '',
    job_id         TEXT NOT NULL DEFAULT '',
    provider_id    TEXT NOT NULL,
    model_id       TEXT NOT NULL DEFAULT '',
    capability     TEXT NOT NULL,
    operation      TEXT NOT NULL DEFAULT '',
    input_units    REAL NOT NULL DEFAULT 0,
    output_units   REAL NOT NULL DEFAULT 0,
    unit           TEXT NOT NULL,
    estimated_usd  REAL NOT NULL DEFAULT 0,
    actual_usd     REAL NOT NULL DEFAULT 0,
    status         TEXT NOT NULL DEFAULT 'recorded',
    metadata_json  TEXT NOT NULL DEFAULT '{}',
    created_at     INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_studio_cost_ledger_project
    ON studio_cost_ledger(project_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_studio_cost_ledger_episode
    ON studio_cost_ledger(episode_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_studio_cost_ledger_job
    ON studio_cost_ledger(job_id);

-- Baseline fallback & common provider rates (USD) for preflight cost estimation.
INSERT OR IGNORE INTO studio_pricing_catalog (id, provider_id, model_id, capability, unit, unit_price_usd, currency, updated_at) VALUES
    ('price_default_llm',       '*',         '*',            'llm',     '1k_tokens', 0.005, 'USD', 1763337600000),
    ('price_default_image',     '*',         '*',            'image',   'image',     0.040, 'USD', 1763337600000),
    ('price_default_video',     '*',         '*',            'video',   'second',    0.080, 'USD', 1763337600000),
    ('price_default_tts',       '*',         '*',            'tts',     '1k_chars',  0.015, 'USD', 1763337600000),
    ('price_default_lipsync',   '*',         '*',            'lipsync', 'second',    0.120, 'USD', 1763337600000),
    ('price_openai_gpt_image',  'openai',    'gpt-image-1',  'image',   'image',     0.040, 'USD', 1763337600000),
    ('price_openai_sora2',      'openai',    'sora-2',       'video',   'second',    0.100, 'USD', 1763337600000),
    ('price_openai_tts1',       'openai',    'tts-1',        'tts',     '1k_chars',  0.015, 'USD', 1763337600000),
    ('price_google_veo',        'google',    '*',            'video',   'second',    0.150, 'USD', 1763337600000),
    ('price_kling_video',       'kling',     '*',            'video',   'second',    0.070, 'USD', 1763337600000),
    ('price_kling_lipsync',     'kling',     '*',            'lipsync', 'second',    0.100, 'USD', 1763337600000),
    ('price_minimax_video',     'minimax',   '*',            'video',   'second',    0.060, 'USD', 1763337600000),
    ('price_minimax_lipsync',   'minimax',   '*',            'lipsync', 'second',    0.090, 'USD', 1763337600000),
    ('price_vidu_video',        'vidu',      '*',            'video',   'second',    0.060, 'USD', 1763337600000),
    ('price_ark_video',         'ark',       '*',            'video',   'second',    0.055, 'USD', 1763337600000),
    ('price_dashscope_video',   'dashscope', '*',            'video',   'second',    0.055, 'USD', 1763337600000);
