package sqlite

import (
	"context"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	domainagent "app/internal/domain/agent"
)

func TestAgentConversationSessionPersistence(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(context.Background(), Options{
		Path:          filepath.Join(dir, "sessions.db"),
		BackupDir:     filepath.Join(dir, "backups"),
		BackupsToKeep: 1,
		BusyTimeout:   time.Second,
		Logger:        slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	repo := NewAgentRepository(db)
	ctx := context.Background()

	if err := repo.SaveConversation(ctx, domainagent.Conversation{
		ID: "conv-1", ProviderID: domainagent.ProviderClaude, Title: "Test", CreatedAt: 1, UpdatedAt: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SetConversationSession(ctx, "conv-1", domainagent.ProviderClaude, "sess-claude-1"); err != nil {
		t.Fatal(err)
	}
	if err := repo.SetConversationSession(ctx, "conv-1", domainagent.ProviderCodex, "thread-codex-1"); err != nil {
		t.Fatal(err)
	}
	// Upsert same provider replaces session id cleanly.
	if err := repo.SetConversationSession(ctx, "conv-1", domainagent.ProviderClaude, "sess-claude-2"); err != nil {
		t.Fatal(err)
	}

	got, err := repo.GetConversationSessions(ctx, "conv-1")
	if err != nil {
		t.Fatal(err)
	}
	if got[domainagent.ProviderClaude] != "sess-claude-2" || got[domainagent.ProviderCodex] != "thread-codex-1" {
		t.Fatalf("unexpected sessions: %#v", got)
	}

	// Empty session clears just that provider.
	if err := repo.SetConversationSession(ctx, "conv-1", domainagent.ProviderCodex, ""); err != nil {
		t.Fatal(err)
	}
	got, err = repo.GetConversationSessions(ctx, "conv-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := got[domainagent.ProviderCodex]; exists || got[domainagent.ProviderClaude] != "sess-claude-2" {
		t.Fatalf("expected codex cleared and claude kept, got %#v", got)
	}

	// Deleting conversation wipes remaining persisted provider sessions.
	if err := repo.DeleteConversation(ctx, "conv-1"); err != nil {
		t.Fatal(err)
	}
	got, err = repo.GetConversationSessions(ctx, "conv-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("expected all sessions deleted, got %#v", got)
	}
}

func TestAgentMessageSubagentsMetadataPersistence(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(context.Background(), Options{
		Path:          filepath.Join(dir, "meta.db"),
		BackupDir:     filepath.Join(dir, "backups"),
		BackupsToKeep: 1,
		BusyTimeout:   time.Second,
		Logger:        slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	repo := NewAgentRepository(db)
	ctx := context.Background()

	if err := repo.SaveConversation(ctx, domainagent.Conversation{
		ID: "conv-meta", ProviderID: domainagent.ProviderAntigravity, Title: "Delegated", CreatedAt: 10, UpdatedAt: 10,
	}); err != nil {
		t.Fatal(err)
	}

	msg := domainagent.ChatMessage{
		ID:             "m-meta-1",
		ConversationID: "conv-meta",
		ProviderID:     domainagent.ProviderAntigravity,
		Role:           "assistant",
		Text:           "Combined answer.",
		CreatedAt:      11,
		Subagents: []domainagent.SubagentInvocation{
			{
				ID:       "sub-1",
				Name:     "workspace-reviewer",
				Provider: domainagent.ProviderClaude,
				Status:   "done",
				Text:     "Reviewed cleanly",
				Tools:    []string{"workspace_read_file", "workspace_search_text"},
			},
		},
	}
	if err := repo.AppendChatMessage(ctx, msg); err != nil {
		t.Fatalf("AppendChatMessage: %v", err)
	}

	convMsgs, err := repo.ListConversationMessages(ctx, "conv-meta", 50)
	if err != nil {
		t.Fatalf("ListConversationMessages: %v", err)
	}
	if len(convMsgs) != 1 || len(convMsgs[0].Subagents) != 1 {
		t.Fatalf("expected 1 message with 1 persisted subagent invocation, got %+v", convMsgs)
	}
	gotSub := convMsgs[0].Subagents[0]
	if gotSub.Name != "workspace-reviewer" || gotSub.Provider != domainagent.ProviderClaude || len(gotSub.Tools) != 2 {
		t.Fatalf("unexpected persisted subagent payload: %+v", gotSub)
	}
}
