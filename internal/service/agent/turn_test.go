package agent

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"

	domainagent "app/internal/domain/agent"
)

func newTurnTestService(t *testing.T) (*Service, *fakeRepo, func() []domainagent.ChatEvent) {
	t.Helper()
	repo := newFakeRepo()
	var mu sync.Mutex
	var events []domainagent.ChatEvent
	svc, err := New(Options{Repo: repo, DataDir: t.TempDir(), Logger: slog.New(slog.DiscardHandler),
		OnChatEvent: func(e domainagent.ChatEvent) { mu.Lock(); events = append(events, e); mu.Unlock() }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Close() })
	return svc, repo, func() []domainagent.ChatEvent {
		mu.Lock()
		defer mu.Unlock()
		return append([]domainagent.ChatEvent(nil), events...)
	}
}

// An ACP process that dies mid-turn ends the turn at once: its ctx is cancelled, the partial reply and a
// translatable error are stored in the turn's chat, and the service is idle again.
func TestProcessExitEndsRunningTurn(t *testing.T) {
	svc, repo, events := newTurnTestService(t)
	ctx, cancel := context.WithCancel(context.Background())
	token := svc.connSeq.Add(1)
	tr := newTurn("msg-1", "conv-1", domainagent.ProviderAntigravity, token, nil, cancel)
	svc.mu.Lock()
	svc.activeID, svc.turn = domainagent.ProviderAntigravity, tr
	svc.states[domainagent.ProviderAntigravity] = domainagent.StateConnected
	svc.mu.Unlock()
	svc.emitChat(domainagent.ChatEvent{Type: domainagent.ChatEventDelta, MessageID: "msg-1", Text: "Đang xử lý yêu cầu của bạn\n"})

	svc.handleProcessExit(token, domainagent.ProviderAntigravity, errors.New("exit status 1"))

	if ctx.Err() == nil {
		t.Fatal("turn context must be cancelled")
	}
	if svc.turnFor("") != nil {
		t.Fatal("service must be idle")
	}
	var gotErr *domainagent.ChatEvent
	for _, e := range events() {
		if e.Type == domainagent.ChatEventError {
			gotErr = &e
		}
	}
	if gotErr == nil || gotErr.ErrorKey != msgProcessExited || gotErr.MessageID != "msg-1" {
		t.Fatalf("error event = %+v", gotErr)
	}
	repo.mu.Lock()
	defer repo.mu.Unlock()
	if len(repo.messages) != 2 || repo.messages[0].Role != "assistant" || repo.messages[1].Role != "error" ||
		repo.messages[1].ConversationID != "conv-1" {
		t.Fatalf("stored = %+v", repo.messages)
	}
	// The turn goroutine ending afterwards must not end it twice.
	svc.endTurn(tr, errTurnCancelled)
	if len(repo.messages) != 2 {
		t.Fatalf("turn ended twice: %+v", repo.messages)
	}
}

// After a disconnect, output still written by the cancelled turn's process never reaches the UI.
func TestOutputAfterDisconnectIsDropped(t *testing.T) {
	svc, _, events := newTurnTestService(t)
	tr := newTurn("msg-1", "", domainagent.ProviderCodex, svc.connSeq.Load(), nil, func() {})
	svc.mu.Lock()
	svc.activeID, svc.turn = domainagent.ProviderCodex, tr
	svc.mu.Unlock()
	if err := svc.Disconnect(context.Background()); err != nil {
		t.Fatal(err)
	}
	svc.emitChat(domainagent.ChatEvent{Type: domainagent.ChatEventDelta, MessageID: "msg-1", Text: "late text that must not show\n"})
	for _, e := range events() {
		if e.Type == domainagent.ChatEventDelta {
			t.Fatalf("late delta reached the UI: %+v", e)
		}
	}
}

// A turn cut off by a disconnect keeps its partial reply but sends no done/error: the UI may already be
// waiting for the next turn on a new connection.
func TestSupersededTurnStoresButDoesNotReport(t *testing.T) {
	svc, repo, events := newTurnTestService(t)
	tr := newTurn("msg-1", "conv-1", domainagent.ProviderCodex, svc.connSeq.Load(), nil, func() {})
	svc.mu.Lock()
	svc.activeID, svc.turn = domainagent.ProviderCodex, tr
	svc.mu.Unlock()
	svc.emitChat(domainagent.ChatEvent{Type: domainagent.ChatEventDelta, MessageID: "msg-1", Text: "Câu trả lời dở dang\n"})
	if err := svc.Disconnect(context.Background()); err != nil {
		t.Fatal(err)
	}
	svc.endTurn(tr, errTurnCancelled)
	for _, e := range events() {
		if e.Type == domainagent.ChatEventDone || e.Type == domainagent.ChatEventError {
			t.Fatalf("superseded turn reported %s", e.Type)
		}
	}
	repo.mu.Lock()
	defer repo.mu.Unlock()
	if len(repo.messages) != 1 || repo.messages[0].Text != "Câu trả lời dở dang\n" {
		t.Fatalf("partial reply not kept: %+v", repo.messages)
	}
}
