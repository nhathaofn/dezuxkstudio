-- 006_agent_runtime.sql
-- Store isolated DezuxkStudio agent chat history and user-approved workspace folders.

CREATE TABLE IF NOT EXISTS agent_chat_messages (
    id TEXT PRIMARY KEY,
    provider_id TEXT NOT NULL,
    role TEXT NOT NULL,
    text TEXT NOT NULL DEFAULT '',
    thought TEXT NOT NULL DEFAULT '',
    tool TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_agent_chat_messages_created_at
    ON agent_chat_messages (created_at ASC, id ASC);

CREATE TABLE IF NOT EXISTS agent_allowed_dirs (
    path TEXT PRIMARY KEY,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
