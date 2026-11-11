package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"app/internal/domain/agent"
)

// AgentRepository implements agent.Repository using SQLite.
type AgentRepository struct{ db *DB }

var _ agent.Repository = (*AgentRepository)(nil)

// NewAgentRepository creates a new AgentRepository.
func NewAgentRepository(db *DB) *AgentRepository {
	return &AgentRepository{db: db}
}

// GetCustomPath returns the custom executable path configured for id, or an empty string if unset.
func (r *AgentRepository) GetCustomPath(ctx context.Context, id agent.ProviderID) (string, error) {
	var p string
	err := r.db.sql.QueryRowContext(ctx, "SELECT custom_path FROM agent_providers WHERE id = ?", string(id)).Scan(&p)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("sqlite: get agent custom path for %s: %w", id, err)
	}
	return p, nil
}

// GetAllCustomPaths returns custom paths for all configured agent providers.
func (r *AgentRepository) GetAllCustomPaths(ctx context.Context) (map[agent.ProviderID]string, error) {
	rows, err := r.db.sql.QueryContext(ctx, "SELECT id, custom_path FROM agent_providers")
	if err != nil {
		return nil, fmt.Errorf("sqlite: get all agent custom paths: %w", err)
	}
	defer rows.Close()

	res := make(map[agent.ProviderID]string)
	for rows.Next() {
		var id, p string
		if err := rows.Scan(&id, &p); err != nil {
			return nil, fmt.Errorf("sqlite: scan agent provider: %w", err)
		}
		res[agent.ProviderID(id)] = p
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: iterate agent providers: %w", err)
	}
	return res, nil
}

// SetCustomPath saves or clears the custom path for an agent provider.
func (r *AgentRepository) SetCustomPath(ctx context.Context, id agent.ProviderID, customPath string) error {
	_, err := r.db.sql.ExecContext(ctx, `
		INSERT INTO agent_providers (id, custom_path, updated_at) VALUES (?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(id) DO UPDATE SET custom_path = excluded.custom_path, updated_at = CURRENT_TIMESTAMP;`,
		string(id), customPath)
	if err != nil {
		return fmt.Errorf("sqlite: set agent custom path for %s: %w", id, err)
	}
	return nil
}

// AppendChatMessage persists a single chat message in DezuxkStudio's isolated history.
func (r *AgentRepository) AppendChatMessage(ctx context.Context, msg agent.ChatMessage) error {
	_, err := r.db.sql.ExecContext(ctx, `
		INSERT INTO agent_chat_messages (id, provider_id, role, text, thought, tool, created_at, conversation_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			text = excluded.text,
			thought = excluded.thought,
			tool = excluded.tool,
			role = excluded.role;`,
		msg.ID, string(msg.ProviderID), msg.Role, msg.Text, msg.Thought, msg.Tool, msg.CreatedAt, msg.ConversationID)
	if err != nil {
		return fmt.Errorf("sqlite: append agent chat message: %w", err)
	}
	return nil
}

// ListChatMessages returns the most recent chat messages in chronological order.
func (r *AgentRepository) ListChatMessages(ctx context.Context, limit int) ([]agent.ChatMessage, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := r.db.sql.QueryContext(ctx, `
		SELECT id, provider_id, role, text, thought, tool, created_at
		FROM (
			SELECT id, provider_id, role, text, thought, tool, created_at
			FROM agent_chat_messages
			ORDER BY created_at DESC, id DESC
			LIMIT ?
		) sub
		ORDER BY created_at ASC, id ASC;`, limit)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list agent chat messages: %w", err)
	}
	defer rows.Close()

	var out []agent.ChatMessage
	for rows.Next() {
		var m agent.ChatMessage
		var pid string
		if err := rows.Scan(&m.ID, &pid, &m.Role, &m.Text, &m.Thought, &m.Tool, &m.CreatedAt); err != nil {
			return nil, fmt.Errorf("sqlite: scan agent chat message: %w", err)
		}
		m.ProviderID = agent.ProviderID(pid)
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: iterate agent chat messages: %w", err)
	}
	return out, nil
}

// SaveConversation creates a conversation or refreshes its provider and updated_at.
func (r *AgentRepository) SaveConversation(ctx context.Context, c agent.Conversation) error {
	_, err := r.db.sql.ExecContext(ctx, `
		INSERT INTO agent_conversations (id, title, provider_id, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			title = CASE WHEN agent_conversations.title = '' THEN excluded.title ELSE agent_conversations.title END,
			provider_id = excluded.provider_id,
			updated_at = excluded.updated_at;`,
		c.ID, c.Title, string(c.ProviderID), c.CreatedAt, c.UpdatedAt)
	if err != nil {
		return fmt.Errorf("sqlite: save agent conversation: %w", err)
	}
	return nil
}

// ListConversations returns conversations, most recently active first.
func (r *AgentRepository) ListConversations(ctx context.Context, limit int) ([]agent.Conversation, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := r.db.sql.QueryContext(ctx, `
		SELECT c.id, c.title, c.provider_id, c.created_at, c.updated_at,
		       (SELECT COUNT(*) FROM agent_chat_messages m WHERE m.conversation_id = c.id)
		FROM agent_conversations c
		ORDER BY c.updated_at DESC, c.id DESC
		LIMIT ?;`, limit)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list agent conversations: %w", err)
	}
	defer rows.Close()
	var out []agent.Conversation
	for rows.Next() {
		var c agent.Conversation
		var pid string
		if err := rows.Scan(&c.ID, &c.Title, &pid, &c.CreatedAt, &c.UpdatedAt, &c.MessageCount); err != nil {
			return nil, fmt.Errorf("sqlite: scan agent conversation: %w", err)
		}
		c.ProviderID = agent.ProviderID(pid)
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: iterate agent conversations: %w", err)
	}
	return out, nil
}

// ListConversationMessages returns the newest messages of one conversation in chronological order.
func (r *AgentRepository) ListConversationMessages(ctx context.Context, conversationID string, limit int) ([]agent.ChatMessage, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := r.db.sql.QueryContext(ctx, `
		SELECT id, provider_id, role, text, thought, tool, created_at, conversation_id
		FROM (
			SELECT id, provider_id, role, text, thought, tool, created_at, conversation_id
			FROM agent_chat_messages
			WHERE conversation_id = ?
			ORDER BY created_at DESC, id DESC
			LIMIT ?
		) sub
		ORDER BY created_at ASC, id ASC;`, conversationID, limit)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list conversation messages: %w", err)
	}
	defer rows.Close()
	var out []agent.ChatMessage
	for rows.Next() {
		var m agent.ChatMessage
		var pid string
		if err := rows.Scan(&m.ID, &pid, &m.Role, &m.Text, &m.Thought, &m.Tool, &m.CreatedAt, &m.ConversationID); err != nil {
			return nil, fmt.Errorf("sqlite: scan conversation message: %w", err)
		}
		m.ProviderID = agent.ProviderID(pid)
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: iterate conversation messages: %w", err)
	}
	return out, nil
}

// DeleteConversation removes a conversation and its messages.
func (r *AgentRepository) DeleteConversation(ctx context.Context, conversationID string) error {
	tx, err := r.db.sql.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sqlite: delete agent conversation: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM agent_chat_messages WHERE conversation_id = ?`, conversationID); err != nil {
		return fmt.Errorf("sqlite: delete conversation messages: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM agent_conversations WHERE id = ?`, conversationID); err != nil {
		return fmt.Errorf("sqlite: delete conversation: %w", err)
	}
	return tx.Commit()
}

// ClearChatMessages removes all persisted DezuxkStudio agent chat messages.
func (r *AgentRepository) ClearChatMessages(ctx context.Context) error {
	if _, err := r.db.sql.ExecContext(ctx, `DELETE FROM agent_chat_messages`); err != nil {
		return fmt.Errorf("sqlite: clear agent chat messages: %w", err)
	}
	return nil
}

// ListAllowedDirs returns every user-authorized workspace directory sorted alphabetically.
func (r *AgentRepository) ListAllowedDirs(ctx context.Context) ([]string, error) {
	rows, err := r.db.sql.QueryContext(ctx, `SELECT path FROM agent_allowed_dirs ORDER BY path ASC`)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list agent allowed dirs: %w", err)
	}
	defer rows.Close()

	var dirs []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, fmt.Errorf("sqlite: scan agent allowed dir: %w", err)
		}
		dirs = append(dirs, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: iterate agent allowed dirs: %w", err)
	}
	return dirs, nil
}

// AddAllowedDir grants agent workspace access to dir.
func (r *AgentRepository) AddAllowedDir(ctx context.Context, dir string) error {
	if _, err := r.db.sql.ExecContext(ctx, `
		INSERT OR IGNORE INTO agent_allowed_dirs (path) VALUES (?)`, dir); err != nil {
		return fmt.Errorf("sqlite: add agent allowed dir %q: %w", dir, err)
	}
	return nil
}

// RemoveAllowedDir revokes agent workspace access from dir.
func (r *AgentRepository) RemoveAllowedDir(ctx context.Context, dir string) error {
	if _, err := r.db.sql.ExecContext(ctx, `
		DELETE FROM agent_allowed_dirs WHERE path = ?`, dir); err != nil {
		return fmt.Errorf("sqlite: remove agent allowed dir %q: %w", dir, err)
	}
	return nil
}

// GetModelSelection returns the persisted model choice for a provider (empty when unset).
func (r *AgentRepository) GetModelSelection(ctx context.Context, id agent.ProviderID) (agent.ModelSelection, error) {
	var sel agent.ModelSelection
	err := r.db.sql.QueryRowContext(ctx, "SELECT model, effort FROM agent_providers WHERE id = ?", string(id)).Scan(&sel.Model, &sel.Effort)
	if errors.Is(err, sql.ErrNoRows) {
		return agent.ModelSelection{}, nil
	}
	if err != nil {
		return agent.ModelSelection{}, fmt.Errorf("sqlite: get agent model for %s: %w", id, err)
	}
	return sel, nil
}

// SetModelSelection saves the model choice for a provider.
func (r *AgentRepository) SetModelSelection(ctx context.Context, id agent.ProviderID, sel agent.ModelSelection) error {
	_, err := r.db.sql.ExecContext(ctx, `
		INSERT INTO agent_providers (id, model, effort, updated_at) VALUES (?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(id) DO UPDATE SET model = excluded.model, effort = excluded.effort, updated_at = CURRENT_TIMESTAMP;`,
		string(id), sel.Model, sel.Effort)
	if err != nil {
		return fmt.Errorf("sqlite: set agent model for %s: %w", id, err)
	}
	return nil
}
