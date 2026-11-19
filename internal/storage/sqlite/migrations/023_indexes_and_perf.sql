-- 023_indexes_and_perf.sql
-- Query-performance indexes across media library and film studio tables.

CREATE INDEX IF NOT EXISTS idx_media_assets_created_at
    ON media_assets(created_at DESC);

CREATE INDEX IF NOT EXISTS idx_studio_projects_updated_at
    ON studio_projects(updated_at DESC);

CREATE INDEX IF NOT EXISTS idx_studio_jobs_ep_shot_kind
    ON studio_jobs(episode_id, shot_id, job_kind);

CREATE INDEX IF NOT EXISTS idx_studio_pricing_cap
    ON studio_pricing_catalog(capability, provider_id, model_id);
