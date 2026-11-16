-- 016_media_assets.sql
-- Media asset registry for shared asset management across features (MediaGen, Watermark, Agent).
-- Indexes assets with metadata, dimensions, duration, and registered root for safe access.

CREATE TABLE IF NOT EXISTS media_assets (
    id               TEXT PRIMARY KEY,
    kind             TEXT NOT NULL,
    source_node      TEXT NOT NULL,
    file_path        TEXT NOT NULL,
    registered_root  TEXT NOT NULL,
    mime_type        TEXT NOT NULL,
    size_bytes       INTEGER NOT NULL,
    width            INTEGER NOT NULL DEFAULT 0,
    height           INTEGER NOT NULL DEFAULT 0,
    duration_sec     REAL NOT NULL DEFAULT 0,
    metadata_json    TEXT NOT NULL DEFAULT '{}',
    created_at       INTEGER NOT NULL,
    last_verified_at INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_media_assets_kind_created ON media_assets(kind, created_at DESC);
CREATE UNIQUE INDEX IF NOT EXISTS idx_media_assets_file_path ON media_assets(file_path);
