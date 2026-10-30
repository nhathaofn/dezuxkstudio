-- 002_binaries.sql
-- Store custom paths and configurations for external binaries (ffmpeg, ffprobe, yt-dlp, etc.)

CREATE TABLE IF NOT EXISTS binary_configs (
    id TEXT PRIMARY KEY,
    custom_path TEXT NOT NULL DEFAULT '',
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
