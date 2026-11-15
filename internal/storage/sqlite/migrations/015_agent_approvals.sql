-- 015_agent_approvals.sql
-- Persistent storage for MCP tool approval requests in agent conversations.
-- Tracks approval status, input snapshots, execution decisions, results, and continuation depth.

CREATE TABLE IF NOT EXISTS agent_approvals (
    id                 TEXT PRIMARY KEY,
    conversation_id   TEXT NOT NULL,
    tool_name          TEXT NOT NULL,
    node_type          TEXT NOT NULL,
    input_json         TEXT NOT NULL,
    input_hash         TEXT NOT NULL,
    status             TEXT NOT NULL,
    decision           TEXT NOT NULL DEFAULT '',
    result_json        TEXT NOT NULL DEFAULT '',
    error_text         TEXT NOT NULL DEFAULT '',
    continuation_depth INTEGER NOT NULL DEFAULT 0,
    created_at         INTEGER NOT NULL,
    updated_at         INTEGER NOT NULL,
    FOREIGN KEY (conversation_id) REFERENCES agent_conversations(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_agent_approvals_conv_status
    ON agent_approvals(conversation_id, status);
