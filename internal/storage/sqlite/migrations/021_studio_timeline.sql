-- 021_studio_timeline.sql
-- Phase 3: Multi-track EditTimeline persistence and revision history for Undo/Redo & optimistic concurrency.

CREATE TABLE IF NOT EXISTS studio_timelines (
    episode_id    TEXT PRIMARY KEY REFERENCES studio_episodes(id) ON DELETE CASCADE,
    revision      INTEGER NOT NULL DEFAULT 0,
    timeline_json TEXT NOT NULL,
    updated_at    INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS studio_timeline_revisions (
    id            TEXT PRIMARY KEY,
    episode_id    TEXT NOT NULL REFERENCES studio_episodes(id) ON DELETE CASCADE,
    revision      INTEGER NOT NULL,
    actor         TEXT NOT NULL DEFAULT 'user',
    summary       TEXT NOT NULL DEFAULT '',
    timeline_json TEXT NOT NULL,
    created_at    INTEGER NOT NULL,
    UNIQUE(episode_id, revision)
);

CREATE INDEX IF NOT EXISTS idx_studio_timeline_revisions_ep_rev
    ON studio_timeline_revisions(episode_id, revision DESC);
