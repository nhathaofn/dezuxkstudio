package agent

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	domainagent "app/internal/domain/agent"
)

func TestCodexResumesSessionAndFallsBackWhenMissing(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "codex.exe")
	if err := os.WriteFile(exe, []byte("stub"), 0o644); err != nil {
		t.Fatal(err)
	}
	repo := newFakeRepo()
	_ = repo.SaveConversation(context.Background(), domainagent.Conversation{ID: "c1", CreatedAt: 1, UpdatedAt: 1})
	_ = repo.AppendChatMessage(context.Background(), domainagent.ChatMessage{ID: "old", Role: "user", Text: "câu hỏi cũ", CreatedAt: 1, ConversationID: "c1"})

	var mu sync.Mutex
	var prompts []string
	failResume := false
	done := make(chan struct{}, 8)
	var svc *Service
	var err error
	svc, err = New(Options{
		Repo: repo, DataDir: dir, WorkspaceDir: filepath.Join(dir, "ws"), MediaDir: filepath.Join(dir, "media"),
		ProfilesDir: filepath.Join(dir, "profiles"), Logger: slog.New(slog.DiscardHandler),
		OnChatEvent: func(e domainagent.ChatEvent) {
			if e.Type == domainagent.ChatEventDone || e.Type == domainagent.ChatEventError {
				done <- struct{}{}
			}
		},
		ProbeCodexAuth: func(context.Context, string, string) (domainagent.AuthState, string) {
			return domainagent.AuthAuthenticated, "test"
		},
		RunCodexTurn: func(_ context.Context, _, _, _, _, _, _, prompt string) error {
			mu.Lock()
			prompts = append(prompts, prompt)
			fail := failResume && svc.cliSessionFor(domainagent.ProviderCodex) != ""
			mu.Unlock()
			if fail {
				return errors.New("thread/resume: thread/resume failed: no rollout found for thread id x")
			}
			svc.setCLISession(domainagent.ProviderCodex, "thread-1") // what the real runner does after a turn
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
	ctx := context.Background()
	// Continue the earlier chat: its history must be replayed into the first turn of the new session.
	if _, _, err := svc.OpenConversation(ctx, "c1"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SetCustomPath(ctx, domainagent.ProviderCodex, exe); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Connect(ctx, domainagent.ProviderCodex); err != nil {
		t.Fatal(err)
	}
	send := func(q string) {
		t.Helper()
		if _, _, err := svc.SendPrompt(ctx, q); err != nil {
			t.Fatal(err)
		}
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("turn timed out")
		}
	}

	send("lượt 1")
	send("lượt 2")
	mu.Lock()
	if !strings.Contains(prompts[0], "câu hỏi cũ") {
		t.Fatalf("first turn must carry history, got %q", prompts[0])
	}
	if strings.Contains(prompts[1], "câu hỏi cũ") || strings.Contains(prompts[1], "[Recent conversation]") {
		t.Fatalf("resumed turn must not resend history, got %q", prompts[1])
	}
	failResume = true
	mu.Unlock()

	send("lượt 3")
	mu.Lock()
	defer mu.Unlock()
	if len(prompts) != 4 || !strings.Contains(prompts[3], "[Recent conversation]") {
		t.Fatalf("expected a fresh retry with history after a missing session, got %d prompts", len(prompts))
	}
	if prompts[2] == prompts[3] || strings.Contains(prompts[2], "[Recent conversation]") {
		t.Fatal("expected the failed resume attempt without history, then a fresh attempt with history")
	}

	_ = svc.NewConversation(ctx)
	svc.setCLISession(domainagent.ProviderClaude, "c1")
	_ = svc.NewConversation(ctx)
	if svc.cliSessionFor(domainagent.ProviderClaude) != "" {
		t.Fatal("clearing the chat must drop CLI sessions")
	}
}

func TestCodexResumeArgs(t *testing.T) {
	first := strings.Join(buildCodexExecArgs("http://x/mcp", "i", "", "", 0), " ")
	resumed := strings.Join(buildCodexExecArgs("http://x/mcp", "i", "", "thread-9", 0), " ")
	if strings.Contains(first, "resume") || !strings.HasPrefix(resumed, "exec resume thread-9 --json") {
		t.Fatalf("unexpected args:\n%s\n%s", first, resumed)
	}
	for _, a := range []string{first, resumed} {
		if strings.Contains(a, "--ephemeral") || strings.Contains(a, " -C ") || strings.Contains(a, " -s ") || !strings.HasSuffix(a, " -") {
			t.Fatalf("args incompatible with exec resume: %s", a)
		}
	}
	acc := &codexStreamAccumulator{}
	acc.handleLine(`{"type":"thread.started","thread_id":"01a1c936-2acd"}`)
	if acc.threadID != "01a1c936-2acd" {
		t.Fatalf("thread id not captured: %q", acc.threadID)
	}
}
