package agent

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	domainagent "app/internal/domain/agent"
	"app/internal/engine"
	"app/internal/tools/watermark"
)

func TestLiveIsolatedAgents(t *testing.T) {
	if os.Getenv("DEZUXK_LIVE_AGENT_TEST") != "1" {
		t.Skip("set DEZUXK_LIVE_AGENT_TEST=1 to run live CLI agent verification")
	}

	base := t.TempDir()
	ws := filepath.Join(base, "agent-workspace")
	media := filepath.Join(base, "media")
	profiles := filepath.Join(base, "agent-profiles")
	_ = os.MkdirAll(ws, 0o700)
	_ = os.MkdirAll(media, 0o700)
	_ = os.MkdirAll(profiles, 0o700)

	reg := engine.NewRegistry()
	if err := reg.Register(watermark.NewNode(watermark.Options{MediaDir: media})); err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	var events []domainagent.ChatEvent
	doneCh := make(chan domainagent.ChatEvent, 8)

	svc, err := New(Options{
		Repo:         newFakeRepo(),
		DataDir:      base,
		WorkspaceDir: ws,
		MediaDir:     media,
		ProfilesDir:  profiles,
		AppVersion:   "0.1.0-test",
		Logger:       slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})),
		Workflow:     &fakeWorkflow{specs: reg.Specs()},
		Binaries:     fakeBinaries{},
		Preferences:  fakePrefs{},
		System:       fakeSys{dir: base},
		Diagnostics:  fakeDiag{},
		OnChatEvent: func(evt domainagent.ChatEvent) {
			mu.Lock()
			events = append(events, evt)
			mu.Unlock()
			if evt.Type == domainagent.ChatEventDone || evt.Type == domainagent.ChatEventError {
				doneCh <- evt
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	// 1. Verify Codex isolated CODEX_HOME does NOT reuse user's external ~/.codex login.
	codexInfo, err := svc.Probe(ctx, domainagent.ProviderCodex)
	if err != nil {
		t.Fatalf("Probe codex failed: %v", err)
	}
	if codexInfo.ResolvedPath != "" {
		t.Logf("Codex probe: state=%s auth=%s profile=%s", codexInfo.State, codexInfo.AuthState, codexInfo.IsolatedProfileDir)
		if codexInfo.AuthState != domainagent.AuthUnauthenticated {
			t.Fatalf("expected isolated fresh CODEX_HOME to be unauthenticated, got %s", codexInfo.AuthState)
		}
		if _, err := svc.Connect(ctx, domainagent.ProviderCodex); err == nil {
			t.Fatal("expected Connect codex on unauthenticated isolated profile to fail")
		}
	}

	// 2. Verify Claude isolated CLAUDE_CONFIG_DIR does NOT reuse user's external ~/.claude login.
	claudeInfo, err := svc.Probe(ctx, domainagent.ProviderClaude)
	if err != nil {
		t.Fatalf("Probe claude failed: %v", err)
	}
	if claudeInfo.ResolvedPath != "" {
		t.Logf("Claude probe: state=%s auth=%s profile=%s", claudeInfo.State, claudeInfo.AuthState, claudeInfo.IsolatedProfileDir)
		if claudeInfo.AuthState != domainagent.AuthUnauthenticated {
			t.Fatalf("expected isolated fresh CLAUDE_CONFIG_DIR to be unauthenticated, got %s", claudeInfo.AuthState)
		}
		if _, err := svc.Connect(ctx, domainagent.ProviderClaude); err == nil {
			t.Fatal("expected Connect claude on unauthenticated isolated profile to fail")
		}
	}

	// 3. Verify Antigravity isolated profile does NOT reuse Windows OS Credential Manager keyring.
	agyProbe, err := svc.Probe(ctx, domainagent.ProviderAntigravity)
	if err != nil {
		t.Fatalf("Probe antigravity failed: %v", err)
	}
	if agyProbe.ResolvedPath != "" {
		t.Logf("Agy probe: state=%s auth=%s profile=%s", agyProbe.State, agyProbe.AuthState, agyProbe.IsolatedProfileDir)
		if agyProbe.AuthState != domainagent.AuthUnauthenticated {
			t.Fatalf("expected fresh isolated Antigravity profile to be unauthenticated, got %s", agyProbe.AuthState)
		}
		if _, err := svc.Connect(ctx, domainagent.ProviderAntigravity); err == nil {
			t.Fatal("expected Connect antigravity without isolated Gemini API key to fail")
		}
	}

	if geminiKey := strings.TrimSpace(os.Getenv("DEZUXK_TEST_GEMINI_API_KEY")); geminiKey != "" && agyProbe.ResolvedPath != "" {
		if _, err := svc.Login(ctx, domainagent.ProviderAntigravity, geminiKey); err != nil {
			t.Fatalf("Login antigravity with test Gemini API key failed: %v", err)
		}
		agyInfo, err := svc.Connect(ctx, domainagent.ProviderAntigravity)
		if err != nil {
			t.Fatalf("Connect antigravity failed: %v", err)
		}
		t.Logf("Agy connected: version=%s agent=%s", agyInfo.Version, agyInfo.AgentName)

		runTurn := func(prompt string) (string, []string) {
			mu.Lock()
			events = nil
			mu.Unlock()
			if _, _, err := svc.SendPrompt(ctx, prompt); err != nil {
				t.Fatalf("SendPrompt(%q) failed: %v", prompt, err)
			}
			select {
			case evt := <-doneCh:
				if evt.Type == domainagent.ChatEventError {
					t.Fatalf("Turn(%q) failed: %s", prompt, evt.Error)
				}
			case <-ctx.Done():
				t.Fatalf("Turn(%q) timed out", prompt)
			}
			mu.Lock()
			defer mu.Unlock()
			var sb strings.Builder
			var tools []string
			for _, e := range events {
				if e.Type == domainagent.ChatEventDelta {
					sb.WriteString(e.Text)
				}
				if e.Type == domainagent.ChatEventToolCall && e.Tool != "" {
					tools = append(tools, e.Tool)
				}
			}
			return sb.String(), tools
		}

		capReply, capTools := runTurn("Bạn có thể làm được gì trong ứng dụng này?")
		t.Logf("Capabilities tools=%v\nCapabilities reply:\n%s", capTools, capReply)

		codeReply, codeTools := runTurn("Hãy đọc hoặc sửa code main.go của ứng dụng giúp tôi.")
		t.Logf("Code refusal tools=%v\nCode refusal reply:\n%s", codeTools, codeReply)
	}
}
