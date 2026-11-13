package agent

// Conversations: the active conversation, stored history and per-provider CLI session ids.

import (
	"context"
	"strings"

	"app/internal/apperr"
	domainagent "app/internal/domain/agent"
)

// CurrentConversationID returns the current chat ("" for a new chat without messages).
func (s *Service) CurrentConversationID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.conversationID
}

// NewConversation starts a new chat. Earlier chats stay in history (ListConversations).
func (s *Service) NewConversation(_ context.Context) error {
	return s.switchConversation("")
}

// switchConversation makes id the current chat ("" = a new one) and resets the provider sessions, so the
// next turn starts a fresh session primed with that chat's own history.
func (s *Service) switchConversation(id string) error {
	s.mu.Lock()
	if s.turn != nil {
		s.mu.Unlock()
		return userError(apperr.CodeConflict, msgClearWhileBusy, "cannot switch chats while the agent is busy", nil)
	}
	s.conversationID = id
	s.activeSessionID = ""
	s.sessionPrimed = false
	clear(s.cliSessions)
	clear(s.sentScreen)
	s.mu.Unlock()
	s.approvals.clear()
	s.emitApprovals()
	return nil
}

// History returns the current chat's messages (empty for a new chat) and its ID.
func (s *Service) History(ctx context.Context) (string, []domainagent.ChatMessage, error) {
	s.mu.Lock()
	id := s.conversationID
	s.mu.Unlock()
	if id == "" {
		return "", nil, nil
	}
	msgs, err := s.repo.ListConversationMessages(ctx, id, s.limits.HistoryLimit)
	return id, msgs, err
}

// ListConversations returns earlier chats, most recent first.
func (s *Service) ListConversations(ctx context.Context) ([]domainagent.Conversation, error) {
	return s.repo.ListConversations(ctx, s.limits.HistoryLimit)
}

// OpenConversation makes an earlier chat current so the user can read and continue it.
func (s *Service) OpenConversation(ctx context.Context, id string) (string, []domainagent.ChatMessage, error) {
	id = strings.TrimSpace(id)
	msgs, err := s.repo.ListConversationMessages(ctx, id, s.limits.HistoryLimit)
	if err != nil {
		return "", nil, err
	}
	if len(msgs) == 0 {
		return "", nil, userError(apperr.CodeNotFound, msgConversationGone, "conversation not found", nil)
	}
	if err := s.switchConversation(id); err != nil {
		return "", nil, err
	}
	return id, msgs, nil
}

// DeleteConversation removes a chat; deleting the current one starts a new chat first, so no turn can
// start in (or be running in) the chat being deleted.
func (s *Service) DeleteConversation(ctx context.Context, id string) error {
	s.mu.Lock()
	current := s.conversationID == id
	s.mu.Unlock()
	if current {
		if err := s.switchConversation(""); err != nil {
			return err
		}
	}
	return s.repo.DeleteConversation(ctx, id)
}

// Display bounds for values derived from free text.
const (
	maxConversationTitle = 80 // runes of the first message used as the chat title
	maxVersionLen        = 64 // bytes of a CLI --version line
)

// conversationTitle shortens a chat's first message into its title.
func conversationTitle(prompt string) string {
	title := strings.Join(strings.Fields(prompt), " ")
	if r := []rune(title); len(r) > maxConversationTitle {
		title = string(r[:maxConversationTitle]) + "…"
	}
	return title
}

func cliSessionKey(id domainagent.ProviderID, sessionID string) string {
	if sessionID == "" {
		return ""
	}
	return "cli:" + string(id) + ":" + sessionID
}

func (s *Service) cliSessionFor(id domainagent.ProviderID) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cliSessions[id]
}

func (s *Service) setCLISession(id domainagent.ProviderID, sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sessionID == "" {
		delete(s.cliSessions, id)
		return
	}
	s.cliSessions[id] = sessionID
}
