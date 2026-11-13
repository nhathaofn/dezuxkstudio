-- 012_agent_conversation_sessions.sql
-- Persist native CLI session/thread IDs per conversation and provider so switching chats
-- or restarting DezuxkStudio resumes the provider's native session rather than replaying.

CREATE TABLE IF NOT EXISTS agent_conversation_sessions (
    conversation_id TEXT NOT NULL,
    provider_id     TEXT NOT NULL,
    session_id      TEXT NOT NULL,
    updated_at      TEXT NOT NULL,
    PRIMARY KEY (conversation_id, provider_id)
);

CREATE INDEX IF NOT EXISTS idx_agent_conversation_sessions_conv
    ON agent_conversation_sessions (conversation_id);
