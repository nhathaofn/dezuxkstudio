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
