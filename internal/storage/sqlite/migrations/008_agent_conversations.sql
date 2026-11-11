-- 008_agent_conversations.sql
-- Group agent chat messages into conversations, so a new chat keeps the previous ones in history.

CREATE TABLE IF NOT EXISTS agent_conversations (
    id TEXT PRIMARY KEY,
    title TEXT NOT NULL DEFAULT '',
    provider_id TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_agent_conversations_updated_at
    ON agent_conversations (updated_at DESC);

ALTER TABLE agent_chat_messages ADD COLUMN conversation_id TEXT NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_agent_chat_messages_conversation
    ON agent_chat_messages (conversation_id, created_at ASC, id ASC);

-- Messages saved before conversations existed become one conversation, titled by its first user message.
INSERT INTO agent_conversations (id, title, provider_id, created_at, updated_at)
SELECT
    'legacy',
    COALESCE((SELECT substr(text, 1, 80) FROM agent_chat_messages WHERE role = 'user' ORDER BY created_at, id LIMIT 1), ''),
    COALESCE((SELECT provider_id FROM agent_chat_messages ORDER BY created_at, id LIMIT 1), ''),
    MIN(created_at),
    MAX(created_at)
FROM agent_chat_messages
HAVING COUNT(*) > 0;

UPDATE agent_chat_messages SET conversation_id = 'legacy' WHERE conversation_id = '';
