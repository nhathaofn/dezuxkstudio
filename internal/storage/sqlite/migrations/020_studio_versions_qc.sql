-- Phase 2: Media version history and multimodal Visual QC reports.

CREATE TABLE IF NOT EXISTS studio_media_versions (
    id            TEXT PRIMARY KEY,
    project_id    TEXT NOT NULL REFERENCES studio_projects(id) ON DELETE CASCADE,
    episode_id    TEXT NOT NULL DEFAULT '',
    target_kind   TEXT NOT NULL,
    target_id     TEXT NOT NULL,
    version_num   INTEGER NOT NULL,
    media_path    TEXT NOT NULL,
    source_node   TEXT NOT NULL DEFAULT '',
    prompt_used   TEXT NOT NULL DEFAULT '',
    model_ref     TEXT NOT NULL DEFAULT '',
    seed          INTEGER,
    cost_usd      REAL NOT NULL DEFAULT 0,
    qc_status     TEXT NOT NULL DEFAULT 'pending',
    is_selected   INTEGER NOT NULL DEFAULT 0,
    metadata_json TEXT NOT NULL DEFAULT '{}',
    created_at    INTEGER NOT NULL,
    UNIQUE(target_kind, target_id, version_num)
);

CREATE INDEX IF NOT EXISTS idx_studio_versions_target
    ON studio_media_versions(target_kind, target_id, version_num DESC);

CREATE INDEX IF NOT EXISTS idx_studio_versions_project
    ON studio_media_versions(project_id, episode_id, created_at DESC);

CREATE TABLE IF NOT EXISTS studio_qc_reports (
    id                 TEXT PRIMARY KEY,
    project_id         TEXT NOT NULL REFERENCES studio_projects(id) ON DELETE CASCADE,
    episode_id         TEXT NOT NULL DEFAULT '',
    target_kind        TEXT NOT NULL,
    target_id          TEXT NOT NULL,
    media_version_id   TEXT NOT NULL DEFAULT '',
    media_path         TEXT NOT NULL,
    verdict            TEXT NOT NULL,
    overall_score      REAL NOT NULL DEFAULT 0,
    ffprobe_json       TEXT NOT NULL DEFAULT '{}',
    issues_json        TEXT NOT NULL DEFAULT '[]',
    vision_summary     TEXT NOT NULL DEFAULT '',
    vision_model_ref   TEXT NOT NULL DEFAULT '',
    sample_frames_json TEXT NOT NULL DEFAULT '[]',
    created_at         INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_studio_qc_target
    ON studio_qc_reports(target_kind, target_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_studio_qc_project
    ON studio_qc_reports(project_id, episode_id, created_at DESC);
