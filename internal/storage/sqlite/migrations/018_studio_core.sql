-- 018_studio_core.sql
-- Phase 1 AI Film Studio persistence schema: projects, episodes, asset bible, and shot storyboard.
-- Complements <WorkspaceDir>/projects/<slug>/ JSON + media files with indexed queryable state.

CREATE TABLE IF NOT EXISTS studio_projects (
    id                   TEXT PRIMARY KEY,
    slug                 TEXT NOT NULL UNIQUE,
    title                TEXT NOT NULL,
    genre                TEXT NOT NULL,
    production_mode      TEXT NOT NULL,
    aspect_ratio         TEXT NOT NULL DEFAULT '16:9',
    style_prompt         TEXT NOT NULL DEFAULT '',
    root_dir             TEXT NOT NULL DEFAULT '',
    auto_approve_batches INTEGER NOT NULL DEFAULT 0,
    default_text_model   TEXT NOT NULL DEFAULT '',
    default_image_model  TEXT NOT NULL DEFAULT '',
    default_video_model  TEXT NOT NULL DEFAULT '',
    default_tts_model    TEXT NOT NULL DEFAULT '',
    created_at           INTEGER NOT NULL,
    updated_at           INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS studio_episodes (
    id                 TEXT PRIMARY KEY,
    project_id         TEXT NOT NULL,
    code               TEXT NOT NULL,
    title              TEXT NOT NULL,
    stage              TEXT NOT NULL DEFAULT 'script',
    raw_script_path    TEXT NOT NULL DEFAULT '',
    script_json_path   TEXT NOT NULL DEFAULT '',
    assets_json_path   TEXT NOT NULL DEFAULT '',
    shots_json_path    TEXT NOT NULL DEFAULT '',
    timeline_json_path TEXT NOT NULL DEFAULT '',
    created_at         INTEGER NOT NULL,
    updated_at         INTEGER NOT NULL,
    FOREIGN KEY (project_id) REFERENCES studio_projects(id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_studio_episodes_project_code
    ON studio_episodes(project_id, code);
CREATE INDEX IF NOT EXISTS idx_studio_episodes_project_created
    ON studio_episodes(project_id, created_at ASC);

CREATE TABLE IF NOT EXISTS studio_assets (
    id               TEXT PRIMARY KEY,
    project_id       TEXT NOT NULL,
    kind             TEXT NOT NULL,
    name             TEXT NOT NULL,
    code_tag         TEXT NOT NULL,
    description      TEXT NOT NULL DEFAULT '',
    prompt           TEXT NOT NULL DEFAULT '',
    primary_asset_id TEXT NOT NULL DEFAULT '',
    primary_path     TEXT NOT NULL DEFAULT '',
    voice_provider   TEXT NOT NULL DEFAULT '',
    voice_id         TEXT NOT NULL DEFAULT '',
    voice_style      TEXT NOT NULL DEFAULT '',
    updated_at       INTEGER NOT NULL,
    FOREIGN KEY (project_id) REFERENCES studio_projects(id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_studio_assets_project_tag
    ON studio_assets(project_id, code_tag);
CREATE INDEX IF NOT EXISTS idx_studio_assets_project_kind
    ON studio_assets(project_id, kind, code_tag);

CREATE TABLE IF NOT EXISTS studio_shots (
    id                  TEXT PRIMARY KEY,
    episode_id          TEXT NOT NULL,
    seq                 INTEGER NOT NULL,
    scene_code          TEXT NOT NULL DEFAULT '',
    title               TEXT NOT NULL DEFAULT '',
    shot_mode           TEXT NOT NULL DEFAULT 'single',
    duration_sec        INTEGER NOT NULL DEFAULT 5,
    image_prompt        TEXT NOT NULL DEFAULT '',
    video_prompt        TEXT NOT NULL DEFAULT '',
    narration_text      TEXT NOT NULL DEFAULT '',
    dialogue_json       TEXT NOT NULL DEFAULT '[]',
    ref_asset_tags_json TEXT NOT NULL DEFAULT '[]',
    keyframe_asset_id   TEXT NOT NULL DEFAULT '',
    keyframe_path       TEXT NOT NULL DEFAULT '',
    video_asset_id      TEXT NOT NULL DEFAULT '',
    video_path          TEXT NOT NULL DEFAULT '',
    audio_asset_id      TEXT NOT NULL DEFAULT '',
    audio_path          TEXT NOT NULL DEFAULT '',
    status              TEXT NOT NULL DEFAULT 'pending',
    error_text          TEXT NOT NULL DEFAULT '',
    updated_at          INTEGER NOT NULL,
    FOREIGN KEY (episode_id) REFERENCES studio_episodes(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_studio_shots_episode_seq
    ON studio_shots(episode_id, seq ASC);
