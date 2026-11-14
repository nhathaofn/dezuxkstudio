-- 014_agent_foreign_keys.sql
-- Rescue any orphaned agent messages or CLI sessions into agent_conversations, then
-- rebuild child tables with FOREIGN KEY (conversation_id) REFERENCES agent_conversations(id) ON DELETE CASCADE.
-- Note: provider_models.owner_id and provider_settings.owner_id are intentionally polymorphic and stay FK-free.

-- Step A.1: Rescue orphaned chat message threads (including legacy empty conversation_id).
INSERT OR IGNORE INTO agent_conversations (id, title, provider_id, created_at, updated_at)
SELECT
    m.conversation_id,
    CASE
        WHEN m.conversation_id = '' THEN 'Legacy chat'
        ELSE COALESCE(
            (
                SELECT substr(TRIM(m2.text, ' ' || char(9) || char(10) || char(13)), 1, 80)
                FROM agent_chat_messages m2
                WHERE m2.conversation_id = m.conversation_id
                  AND m2.role = 'user'
                  AND TRIM(m2.text, ' ' || char(9) || char(10) || char(13)) != ''
                ORDER BY m2.created_at ASC, m2.id ASC
                LIMIT 1
            ),
            'Recovered conversation'
        )
    END,
    COALESCE(
        (
            SELECT m3.provider_id
            FROM agent_chat_messages m3
            WHERE m3.conversation_id = m.conversation_id
              AND m3.provider_id != ''
            ORDER BY m3.created_at ASC, m3.id ASC
            LIMIT 1
        ),
        ''
    ),
    MIN(m.created_at),
    MAX(m.created_at)
FROM agent_chat_messages m
WHERE NOT EXISTS (
    SELECT 1 FROM agent_conversations c WHERE c.id = m.conversation_id
)
GROUP BY m.conversation_id;

-- Step A.2: Rescue orphaned provider session rows whose parent conversation is still missing.
-- agent_conversations timestamps are Unix milliseconds, matching agent_chat_messages.created_at.
INSERT OR IGNORE INTO agent_conversations (id, title, provider_id, created_at, updated_at)
SELECT
    s.conversation_id,
    'Recovered conversation',
    MIN(s.provider_id),
    CAST(strftime('%s', 'now') AS INTEGER) * 1000,
    CAST(strftime('%s', 'now') AS INTEGER) * 1000
FROM agent_conversation_sessions s
WHERE NOT EXISTS (
    SELECT 1 FROM agent_conversations c WHERE c.id = s.conversation_id
)
GROUP BY s.conversation_id;

-- Step B: Rebuild agent_chat_messages with ON DELETE CASCADE FK to agent_conversations.
CREATE TABLE agent_chat_messages_new (
    id TEXT PRIMARY KEY,
    provider_id TEXT NOT NULL,
    role TEXT NOT NULL,
    text TEXT NOT NULL DEFAULT '',
    thought TEXT NOT NULL DEFAULT '',
    tool TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    conversation_id TEXT NOT NULL DEFAULT '',
    metadata TEXT NOT NULL DEFAULT '',
    FOREIGN KEY (conversation_id) REFERENCES agent_conversations(id) ON DELETE CASCADE
);

INSERT INTO agent_chat_messages_new (
    id, provider_id, role, text, thought, tool, created_at, conversation_id, metadata
)
SELECT
    id, provider_id, role, text, thought, tool, created_at, conversation_id, metadata
FROM agent_chat_messages;

DROP TABLE agent_chat_messages;

ALTER TABLE agent_chat_messages_new RENAME TO agent_chat_messages;

CREATE INDEX IF NOT EXISTS idx_agent_chat_messages_created_at
    ON agent_chat_messages (created_at ASC, id ASC);

CREATE INDEX IF NOT EXISTS idx_agent_chat_messages_conversation
    ON agent_chat_messages (conversation_id, created_at ASC, id ASC);

-- Step C: Rebuild agent_conversation_sessions with ON DELETE CASCADE FK to agent_conversations.
CREATE TABLE agent_conversation_sessions_new (
    conversation_id TEXT NOT NULL,
    provider_id     TEXT NOT NULL,
    session_id      TEXT NOT NULL,
    updated_at      TEXT NOT NULL,
    PRIMARY KEY (conversation_id, provider_id),
    FOREIGN KEY (conversation_id) REFERENCES agent_conversations(id) ON DELETE CASCADE
);

INSERT INTO agent_conversation_sessions_new (
    conversation_id, provider_id, session_id, updated_at
)
SELECT
    conversation_id, provider_id, session_id, updated_at
FROM agent_conversation_sessions;

DROP TABLE agent_conversation_sessions;

ALTER TABLE agent_conversation_sessions_new RENAME TO agent_conversation_sessions;

CREATE INDEX IF NOT EXISTS idx_agent_conversation_sessions_conv
    ON agent_conversation_sessions (conversation_id);
