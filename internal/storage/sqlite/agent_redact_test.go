package sqlite

import (
	"context"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"app/internal/domain/agent"
)

func TestRedactChatMessages(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, err := Open(ctx, Options{Path: filepath.Join(dir, "t.db"), BackupDir: filepath.Join(dir, "b"), BackupsToKeep: 1,
		BusyTimeout: time.Second, Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	repo := NewAgentRepository(db)

	const convID = "conv-redact"
	if err := repo.SaveConversation(ctx, agent.Conversation{
		ID:         convID,
		Title:      "redact",
		ProviderID: agent.ProviderCodex,
		CreatedAt:  1,
		UpdatedAt:  4,
	}); err != nil {
		t.Fatal(err)
	}

	const secret = "sk-SECRET-123456"
	msgs := []agent.ChatMessage{
		{ID: "1", ConversationID: convID, ProviderID: agent.ProviderCodex, Role: "user", Text: "my key is " + secret + " ok", CreatedAt: 1},
		{ID: "2", ConversationID: convID, ProviderID: agent.ProviderCodex, Role: "assistant", Text: "hi", Thought: "saw " + secret, CreatedAt: 2},
		{ID: "3", ConversationID: convID, ProviderID: agent.ProviderCodex, Role: "tool", Text: "t", Tool: `{"api_key":"` + secret + `"}`, CreatedAt: 3},
		{ID: "4", ConversationID: convID, ProviderID: agent.ProviderCodex, Role: "user", Text: "nothing here", CreatedAt: 4},
	}
	for _, m := range msgs {
		if err := repo.AppendChatMessage(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	if err := repo.RedactChatMessages(ctx, "", "X"); err != nil {
		t.Fatal(err)
	}
	got, err := repo.ListConversationMessages(ctx, convID, 10)
	if err != nil || len(got) != 4 {
		t.Fatalf("list: %v %d", err, len(got))
	}
	if got[0].Text != msgs[0].Text {
		t.Fatalf("empty secret must be a no-op, got %q", got[0].Text)
	}
	if err := repo.RedactChatMessages(ctx, secret, "[secret saved]"); err != nil {
		t.Fatal(err)
	}
	got, err = repo.ListConversationMessages(ctx, convID, 10)
	if err != nil || len(got) != 4 {
		t.Fatalf("list: %v %d", err, len(got))
	}
	if got[0].Text != "my key is [secret saved] ok" {
		t.Fatalf("text = %q", got[0].Text)
	}
	if got[1].Thought != "saw [secret saved]" || got[1].Text != "hi" {
		t.Fatalf("thought = %q text = %q", got[1].Thought, got[1].Text)
	}
	if got[2].Tool != `{"api_key":"[secret saved]"}` {
		t.Fatalf("tool = %q", got[2].Tool)
	}
	if got[3].Text != "nothing here" {
		t.Fatalf("untouched message changed: %q", got[3].Text)
	}
}
