package agent

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	domainagent "app/internal/domain/agent"
)

// writeFakeCLI writes an executable that answers `--version`, `--help` and `debug models` like the
// real agent CLIs, so the model catalog can be read without any of them installed.
func writeFakeCLI(t *testing.T, dir, name string) string {
	t.Helper()
	const catalog = `{"models":[{"slug":"gpt-x","display_name":"GPT X","visibility":"list","priority":1,"default_reasoning_level":"low","supported_reasoning_levels":[{"effort":"low"}]}]}`
	const help = "  --effort <level>   Effort level\n                     (low, medium, high)\n"
	if runtime.GOOS == "windows" {
		p := filepath.Join(dir, name+".cmd")
		script := "@echo off\r\n" +
			"if \"%1\"==\"--version\" (echo " + name + " 1.0.0& exit /b 0)\r\n" +
			"if \"%1\"==\"debug\" (echo " + catalog + "& exit /b 0)\r\n" +
			"if \"%1\"==\"--help\" (echo " + "  --effort ^<level^>   Effort level& echo                      ^(low, medium, high^)& exit /b 0)\r\n" +
			"exit /b 1\r\n"
		if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
		return p
	}
	p := filepath.Join(dir, name)
	script := "#!/bin/sh\ncase \"$1\" in\n--version) echo " + name + " 1.0.0;;\ndebug) echo '" + catalog + "';;\n--help) printf '%s' '" + help + "';;\nesac\n"
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

type restoreEnv struct {
	svc    *Service
	repo   *fakeRepo
	mu     sync.Mutex
	events []domainagent.ProviderInfo
}

func (e *restoreEnv) lastEvent(id domainagent.ProviderID) (domainagent.ProviderInfo, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for i := len(e.events) - 1; i >= 0; i-- {
		if e.events[i].ID == id {
			return e.events[i], true
		}
	}
	return domainagent.ProviderInfo{}, false
}

// newRestoreEnv builds a service that remembers `id` as connected, as after quitting the app.
func newRestoreEnv(t *testing.T, id domainagent.ProviderID) *restoreEnv {
	t.Helper()
	dir := t.TempDir()
	e := &restoreEnv{repo: newFakeRepo()}
	e.repo.remembered = id
	authed := func(context.Context, string, string) (domainagent.AuthState, string) {
		return domainagent.AuthAuthenticated, "test@example.com"
	}
	opts := Options{
		Repo: e.repo, DataDir: dir, WorkspaceDir: filepath.Join(dir, "ws"), MediaDir: filepath.Join(dir, "media"),
		ProfilesDir: filepath.Join(dir, "profiles"), Logger: slog.New(slog.DiscardHandler),
		ProbeCodexAuth: authed, ProbeClaudeAuth: authed,
		OnStatusChanged: func(info domainagent.ProviderInfo) {
			e.mu.Lock()
			e.events = append(e.events, info)
			e.mu.Unlock()
		},
	}
	switch id {
	case domainagent.ProviderAntigravity:
		opts.SpawnTransport = func(context.Context, string, []string, string, []string, func(SessionUpdate), func(error)) (Transport, error) {
			return &mockTransport{
				initRes:      &InitializeResult{ProtocolVersion: 1},
				models:       []domainagent.ModelOption{{ID: "gemini-4-argon-medium", Name: "Argon"}, {ID: "gemini-3.8-flash-medium", Name: "Flash"}},
				currentModel: "gemini-4-argon-medium",
			}, nil
		}
	}
	svc, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Close() })
	e.svc = svc
	switch id {
	case domainagent.ProviderAntigravity:
		stub := filepath.Join(dir, "agy.exe")
		if err := os.WriteFile(stub, []byte("stub"), 0o644); err != nil {
			t.Fatal(err)
		}
		e.repo.paths[id] = stub
		if err := saveAgyAPIKey(svc.profileDirFor(id), "AIza-test"); err != nil {
			t.Fatal(err)
		}
	case domainagent.ProviderCodex:
		e.repo.paths[id] = writeFakeCLI(t, dir, "codex")
	case domainagent.ProviderClaude:
		e.repo.paths[id] = writeFakeCLI(t, dir, "claude")
	}
	return e
}

func waitModels(e *restoreEnv, id domainagent.ProviderID, within time.Duration) bool {
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if info, err := e.svc.statusInfo(context.Background(), id); err == nil && len(info.Models) > 0 {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

var restoreProviders = []domainagent.ProviderID{domainagent.ProviderCodex, domainagent.ProviderClaude, domainagent.ProviderAntigravity}

// After the app restarts, a restored connection must already know its models: the picker must not
// wait for the user to press "Check".
func TestRestoredConnectionLoadsItsModelsWithoutAProbe(t *testing.T) {
	for _, id := range restoreProviders {
		t.Run(string(id), func(t *testing.T) {
			e := newRestoreEnv(t, id)
			e.svc.RestoreConnection(context.Background())
			info, err := e.svc.statusInfo(context.Background(), id)
			if err != nil || info.State != domainagent.StateConnected {
				t.Fatalf("restore must connect: state=%v err=%v", info.State, err)
			}
			if !waitModels(e, id, 5*time.Second) {
				t.Fatal("connected after restore but the model list is still empty (it only appears after a probe)")
			}
		})
	}
}

// The status pushed to the UI when the models arrive must contain them, or a UI that connected
// earlier keeps showing an empty picker.
func TestRestoredConnectionPushesModelsToTheUI(t *testing.T) {
	for _, id := range restoreProviders {
		t.Run(string(id), func(t *testing.T) {
			e := newRestoreEnv(t, id)
			e.svc.RestoreConnection(context.Background())
			deadline := time.Now().Add(5 * time.Second)
			for time.Now().Before(deadline) {
				if info, ok := e.lastEvent(id); ok && info.State == domainagent.StateConnected && len(info.Models) > 0 {
					return
				}
				time.Sleep(50 * time.Millisecond)
			}
			last, _ := e.lastEvent(id)
			t.Fatalf("the last status event for %s has %d models (state %v)", id, len(last.Models), last.State)
		})
	}
}

// A UI that opens after the restore asks for the list: the connected provider must come with its models.
func TestListAfterRestoreShowsModels(t *testing.T) {
	for _, id := range restoreProviders {
		t.Run(string(id), func(t *testing.T) {
			e := newRestoreEnv(t, id)
			e.svc.RestoreConnection(context.Background())
			waitModels(e, id, 5*time.Second)
			list, err := e.svc.List(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			for _, info := range list {
				if info.ID == id {
					if info.State != domainagent.StateConnected || len(info.Models) == 0 {
						t.Fatalf("list shows state=%v models=%d", info.State, len(info.Models))
					}
					return
				}
			}
			t.Fatal("provider missing from the list")
		})
	}
}

// Connecting a provider by hand must be remembered for every kind of provider, so the next start
// reconnects the one the user left connected (not whichever was remembered before).
func TestConnectRemembersTheProviderTheUserConnected(t *testing.T) {
	for _, id := range restoreProviders {
		t.Run(string(id), func(t *testing.T) {
			e := newRestoreEnv(t, id)
			other := domainagent.ProviderCodex
			if id == other {
				other = domainagent.ProviderClaude
			}
			e.repo.remembered = other // an earlier session left another provider connected
			if _, err := e.svc.Connect(context.Background(), id); err != nil {
				t.Fatal(err)
			}
			if e.repo.remembered != id {
				t.Fatalf("remembered %q after connecting %q", e.repo.remembered, id)
			}
		})
	}
}
