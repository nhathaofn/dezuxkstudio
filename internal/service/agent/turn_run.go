package agent

// Turn lifecycle: sending a prompt, running it through the driver, streaming events and ending it.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"app/internal/apperr"
	domainagent "app/internal/domain/agent"
	"app/internal/progress"
	"app/internal/safego"

	"github.com/google/uuid"
)

// SendPrompt submits a chat message to the currently active provider and streams events asynchronously.
func (s *Service) SendPrompt(ctx context.Context, text string) (string, domainagent.ProviderID, error) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return "", "", userError(apperr.CodeValidation, msgPromptEmpty, "prompt cannot be empty", nil)
	}

	s.mu.Lock()
	providerID := s.activeID
	isConnected := providerID != "" && s.states[providerID] == domainagent.StateConnected && (s.isPerTurn(providerID) || s.activeClient != nil)
	if !isConnected {
		s.mu.Unlock()
		return "", "", userError(apperr.CodeUnavailable, msgNotConnected, "no agent is currently connected", nil)
	}
	if s.turn != nil {
		s.mu.Unlock()
		return "", "", userError(apperr.CodeConflict, msgBusy, "agent is already processing a prompt", nil)
	}
	client := s.activeClient
	sessionID := s.activeSessionID
	uiCtx := s.uiContext
	connToken := s.connSeq.Load()
	seq := s.msgSeq.Add(1)
	messageID := fmt.Sprintf("msg-%d-%d", time.Now().UnixMilli(), seq)
	userMsgID := fmt.Sprintf("user-%d-%d", time.Now().UnixMilli(), seq)

	// The first message of a chat creates its conversation; later messages join it.
	now := time.Now().UnixMilli()
	newConversation := s.conversationID == ""
	if newConversation {
		s.conversationID = uuid.NewString()
	}
	conversationID := s.conversationID

	// Like actions, a turn times out after agent.turn_timeout WITHOUT activity (streamed text,
	// tool events, workflow progress from an MCP tool), not after a fixed total duration.
	turnCtx, touchTurn, cancelTurn := progress.NewIdleContext(context.Background(), s.limits.TurnTimeout)
	t := newTurn(messageID, conversationID, providerID, connToken, touchTurn, context.CancelFunc(cancelTurn))
	if s.developerMode {
		t.guard.disable()
	}
	s.turn = t
	s.turns.Add(1)
	s.mu.Unlock()

	conv := domainagent.Conversation{ID: conversationID, ProviderID: providerID, CreatedAt: now, UpdatedAt: now}
	if newConversation {
		conv.Title = conversationTitle(trimmed)
	}
	if err := s.repo.SaveConversation(ctx, conv); err != nil {
		s.log.Warn("agent: failed to save conversation", "error", err)
	}
	// Read twice the replay size: error/empty rows are filtered out before the newest N are kept.
	history, err := s.repo.ListConversationMessages(ctx, conversationID, s.limits.ContextMessages*2)
	if err != nil {
		s.log.Warn("agent: failed to read history before turn", "error", err)
	}
	if err := s.repo.AppendChatMessage(ctx, domainagent.ChatMessage{
		ID:             userMsgID,
		ProviderID:     providerID,
		Role:           "user",
		Text:           trimmed,
		CreatedAt:      now,
		ConversationID: conversationID,
	}); err != nil {
		s.log.Warn("agent: failed to persist user message", "error", err)
	}

	s.emitStatusOf(ctx, providerID)

	safego.Go("agent.turn", func() {
		defer s.turns.Done()
		defer t.cancel()
		err := s.runTurn(turnCtx, t, client, sessionID, trimmed, history, uiCtx)
		s.endTurn(t, turnResult(turnCtx, err, s.limits.TurnTimeout))
	})

	return messageID, providerID, nil
}

// CancelPrompt cancels the currently running prompt turn if one is active.
func (s *Service) CancelPrompt(_ context.Context) error {
	s.mu.Lock()
	client := s.activeClient
	sessionID := s.activeSessionID
	t := s.turn
	s.mu.Unlock()

	if t == nil {
		return nil
	}
	if client != nil && sessionID != "" {
		_ = client.Cancel(sessionID)
	}
	t.cancel()
	return nil
}

func (s *Service) turnPromptOptions(id domainagent.ProviderID, replayedHistory []domainagent.ChatMessage) promptOptions {
	if s.developerMode {
		return promptOptions{skipReminder: true, developerMode: true}
	}
	// Providers with a dedicated, non-appendable system prompt (Claude/Codex) only need a reminder when
	// replaying long raw conversation logs across a cold session start; short/resumed turns keep clean prompts.
	hasStrongSystemPrompt := id == domainagent.ProviderClaude || id == domainagent.ProviderCodex
	skip := hasStrongSystemPrompt && len(replayedHistory) < 8
	return promptOptions{skipReminder: skip, developerMode: false}
}

// runTurn runs one turn on its provider. Text and tool events reach the UI (and the turn's guard) through
// emitChat as they stream; endTurn stores the result.
func (s *Service) runTurn(ctx context.Context, t *turn, client Transport, sessionID, prompt string, history []domainagent.ChatMessage, ui UIContext) error {
	if drv, ok := s.turnDriverFor(t.providerID); ok {
		return s.runCLITurn(ctx, drv, t, prompt, history, ui)
	}
	if client == nil {
		return fmt.Errorf("agent transport is not connected")
	}
	if sessionID == "" {
		cwd, err := s.ensureWorkspaceDir()
		if err != nil {
			return err
		}
		newSessionID, err := client.NewSession(ctx, cwd, s.systemPrompt(ctx, t.providerID))
		if err != nil {
			return err
		}
		s.applyACPSessionModels(ctx, t.providerID, client, newSessionID)
		s.mu.Lock()
		if s.connSeq.Load() == t.connToken {
			s.activeSessionID = newSessionID
			s.sessionPrimed = false
		}
		s.mu.Unlock()
		sessionID = newSessionID
	}
	s.mu.Lock()
	primed := s.sessionPrimed
	if s.connSeq.Load() == t.connToken {
		s.sessionPrimed = true
	}
	s.mu.Unlock()
	sessionKey := "acp:" + sessionID
	var turnPrompt string
	if primed {
		turnPrompt = buildTurnPrompt(nil, s.screenContextForTurn(sessionKey, ui), prompt, s.limits, s.turnPromptOptions(t.providerID, nil))
	} else {
		turnPrompt = buildTurnPrompt(history, ui, prompt, s.limits, s.turnPromptOptions(t.providerID, history))
	}
	err := client.Prompt(ctx, sessionID, turnPrompt)
	if err == nil {
		s.rememberScreenContext(sessionKey, ui)
	}
	return err
}

// runCLITurn runs a turn as its own CLI invocation (Codex, Claude), resuming the provider's session when
// there is one and falling back to a fresh session primed with the history when it is gone.
func (s *Service) runCLITurn(ctx context.Context, drv turnDriver, t *turn, prompt string, history []domainagent.ChatMessage, ui UIContext) error {
	mcpSrv, err := s.ensureMCPServer()
	if err != nil {
		return err
	}
	workDir, err := s.ensureWorkspaceDir()
	if err != nil {
		return err
	}
	spec := domainagent.Registry[t.providerID]
	resolved, _, _, err := s.resolveExecutable(ctx, spec)
	if err != nil {
		return err
	}
	if resolved == "" {
		return fmt.Errorf("%s executable not found", spec.ExecutableLabel)
	}
	run := func(withHistory bool) error {
		h := history
		sessionKey := "" // a new session always receives the screen context
		if !withHistory {
			h = nil // a resumed CLI session already holds the conversation
			sessionKey = cliSessionKey(t.providerID, s.cliSessionFor(t.providerID))
		}
		fullPrompt := buildTurnPrompt(h, s.screenContextForTurn(sessionKey, ui), prompt, s.limits, s.turnPromptOptions(t.providerID, h))
		err := drv.runTurn(ctx, resolved, workDir, mcpSrv.url, mcpSrv.token, t.id, fullPrompt)
		if err == nil {
			s.rememberScreenContext(cliSessionKey(t.providerID, s.cliSessionFor(t.providerID)), ui)
		}
		return err
	}
	resumed := s.cliSessionFor(t.providerID) != ""
	err = run(!resumed)
	if err != nil && resumed && spec.IsSessionGone(err) {
		// The saved session is gone (profile reset, CLI update): start a new one with the history.
		s.setCLISession(t.providerID, "")
		err = run(true)
	}
	return err
}

// endTurn is the single place a turn ends, whatever ended it: the guard's held text is released, the
// sanitized reply (and the error, if any) is stored in the turn's chat, the UI gets done/error, and the
// service goes idle. It runs once per turn; later calls (e.g. the turn goroutine after a process exit
// already ended it) do nothing.
func (s *Service) endTurn(t *turn, err error) {
	t.endOnce.Do(func() {
		// A turn whose connection was replaced (disconnect, reconnect) only stores what it produced: its
		// last text and done/error must not reach a UI that may already be waiting for the next turn.
		current := s.connSeq.Load() == t.connToken
		s.flushGuard(t, current)
		// A user cancel ends the turn normally: the partial reply is kept and nothing is reported as an error.
		if errors.Is(err, errTurnCancelled) {
			err = nil
		}
		s.storeReply(t)

		s.mu.Lock()
		if s.turn == t {
			s.turn = nil
		}
		current = current && s.connSeq.Load() == t.connToken // a disconnect may have happened while storing
		if err != nil && isAuthError(err) && current {
			s.states[t.providerID] = domainagent.StateUnauthenticated
			s.authStates[t.providerID] = domainagent.AuthUnauthenticated
			s.lastErrors[t.providerID] = err
			delete(s.authProbes, t.providerID) // the next probe must re-check instead of serving the cache
		}
		s.mu.Unlock()
		if !current {
			return
		}

		if err != nil {
			if appendErr := s.repo.AppendChatMessage(context.Background(), domainagent.ChatMessage{
				ID:             t.id + "-err",
				ProviderID:     t.providerID,
				Role:           "error",
				Text:           err.Error(),
				CreatedAt:      time.Now().UnixMilli(),
				ConversationID: t.conversationID,
			}); appendErr != nil {
				s.log.Warn("agent: failed to persist turn error", "error", appendErr)
			}
			key, params := messageOf(err)
			s.emitChatRaw(domainagent.ChatEvent{
				Type:        domainagent.ChatEventError,
				ProviderID:  t.providerID,
				MessageID:   t.id,
				Error:       err.Error(),
				ErrorKey:    key,
				ErrorParams: params,
			})
		} else {
			s.emitChatRaw(domainagent.ChatEvent{Type: domainagent.ChatEventDone, ProviderID: t.providerID, MessageID: t.id})
		}
		s.emitStatusOf(context.Background(), t.providerID)
	})
}

// storeReply saves the turn's reply. Only the guard-sanitized text is ever stored: history is replayed
// into later prompts and shown in the UI.
func (s *Service) storeReply(t *turn) {
	text, thought := t.guard.sanitizedText(), t.guard.sanitizedThought()
	if strings.TrimSpace(text) == "" && strings.TrimSpace(thought) == "" {
		return
	}
	now := time.Now().UnixMilli()
	ctx := context.Background()
	if err := s.repo.AppendChatMessage(ctx, domainagent.ChatMessage{
		ID:             t.id,
		ProviderID:     t.providerID,
		Role:           "assistant",
		Text:           text,
		Thought:        thought,
		Tool:           t.toolList(),
		CreatedAt:      now,
		ConversationID: t.conversationID,
	}); err != nil {
		s.log.Warn("agent: failed to persist assistant message", "error", err)
	}
	if t.conversationID != "" {
		if err := s.repo.SaveConversation(ctx, domainagent.Conversation{ID: t.conversationID, ProviderID: t.providerID, CreatedAt: now, UpdatedAt: now}); err != nil {
			s.log.Warn("agent: failed to save conversation", "error", err)
		}
	}
}

// turnFor returns the running turn when messageID is its ID ("" = whatever turn is running).
func (s *Service) turnFor(messageID string) *turn {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.turn == nil || (messageID != "" && s.turn.id != messageID) {
		return nil
	}
	return s.turn
}

// emitChat is the single exit point for streamed chat events. Answer and reasoning text passes through
// the running turn's output guard so source code never reaches the UI, whichever provider produced it;
// events that belong to no running turn (a cancelled turn's process still writing) are dropped.
func (s *Service) emitChat(evt domainagent.ChatEvent) {
	t := s.turnFor(evt.MessageID)
	if t == nil {
		return
	}
	switch evt.Type {
	case domainagent.ChatEventDelta, domainagent.ChatEventThought:
		if evt.Type == domainagent.ChatEventDelta {
			evt.Text = t.guard.filterText(evt.Text)
		} else {
			evt.Text = t.guard.filterThought(evt.Text)
		}
		if evt.Text == "" {
			return
		}
	case domainagent.ChatEventToolCall:
		t.noteTool(evt.Tool)
	}
	s.emitChatRaw(evt)
}

func (s *Service) emitChatRaw(evt domainagent.ChatEvent) {
	s.touchTurn(evt.MessageID)
	if s.onChatEvent != nil {
		s.onChatEvent(evt)
	}
}

// touchTurn re-arms the running turn's idle timeout; messageID "" means "whatever turn is running"
// (MCP tool calls do not know the message they belong to).
func (s *Service) touchTurn(messageID string) {
	if t := s.turnFor(messageID); t != nil && t.touch != nil {
		t.touch()
	}
}

// flushGuard finalizes the turn's guard and, when emit is set, sends whatever safe text it was still holding.
func (s *Service) flushGuard(t *turn, emit bool) {
	textTail, thoughtTail := t.guard.finalize()
	if !emit {
		textTail, thoughtTail = "", ""
	}
	if thoughtTail != "" {
		s.emitChatRaw(domainagent.ChatEvent{Type: domainagent.ChatEventThought, ProviderID: t.providerID, MessageID: t.id, Text: thoughtTail})
	}
	if textTail != "" {
		s.emitChatRaw(domainagent.ChatEvent{Type: domainagent.ChatEventDelta, ProviderID: t.providerID, MessageID: t.id, Text: textTail})
	}
	if t.guard.blocked() {
		s.log.Warn("agent: output guard blocked source code in assistant reply", "provider", t.providerID, "message_id", t.id)
	}
}
