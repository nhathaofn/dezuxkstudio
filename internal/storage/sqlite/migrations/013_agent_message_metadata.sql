-- 013_agent_message_metadata.sql
-- Store structured metadata (e.g. subagent delegations) per persisted agent chat message.

ALTER TABLE agent_chat_messages ADD COLUMN metadata TEXT NOT NULL DEFAULT '';
