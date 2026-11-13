package agent

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"app/internal/config"
	domainagent "app/internal/domain/agent"
	domainbinaries "app/internal/domain/binaries"
	"app/internal/progress"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// slowCLI writes a fake CLI that takes ~2s to answer, like a cold-started Node shim on Windows.
func slowCLI(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if runtime.GOOS == "windows" {
		p := filepath.Join(dir, "slow.cmd")
		if err := os.WriteFile(p, []byte("@echo off\r\nping -n 3 127.0.0.1 >nul\r\necho Usage: claude --model <model>\r\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		return p
	}
	p := filepath.Join(dir, "slow.sh")
	if err := os.WriteFile(p, []byte("#!/bin/sh\nsleep 2\necho 'Usage: claude --model <model>'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// Reading the model catalog must not hold up agent:list / agent:probe: it runs in the background,
// a second refresh while one is in flight does not spawn another CLI, and the flag clears afterwards.
func TestModelCatalogRefreshDoesNotBlock(t *testing.T) {
	limits := DefaultLimits()
	svc := &Service{models: newModelState(), repo: newFakeRepo(), log: slog.New(slog.DiscardHandler), limits: limits}
	svc.drivers = newDrivers(svc)
	exe := slowCLI(t)

	start := time.Now()
	svc.refreshModelCatalogAsync(domainagent.ProviderClaude, exe)
	svc.refreshModelCatalogAsync(domainagent.ProviderClaude, exe)
	if d := time.Since(start); d > 300*time.Millisecond {
		t.Fatalf("refresh blocked the caller for %v", d)
	}
	svc.models.mu.Lock()
	inflight := svc.models.loading[domainagent.ProviderClaude]
	svc.models.mu.Unlock()
	if !inflight {
		t.Fatal("expected a background catalog load in flight")
	}

	deadline := time.Now().Add(limits.ModelCatalogTimeout + 5*time.Second)
	for time.Now().Before(deadline) {
		svc.models.mu.Lock()
		inflight = svc.models.loading[domainagent.ProviderClaude]
		svc.models.mu.Unlock()
		if !inflight {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("background catalog load never finished")
}

func TestVersionProbeDefaultToleratesSlowShims(t *testing.T) {
	if d := DefaultLimits().VersionProbeTimeout; d < 3*time.Second {
		t.Fatalf("version probe timeout %v is too short for cold-started CLI shims", d)
	}
}

// Codex gives up on an MCP tool call after 60s by default; a workflow run through a tool must get
// the long action timeout instead, or the agent reports failure while the workflow keeps running.
func TestCodexArgsRaiseMCPToolTimeout(t *testing.T) {
	args := strings.Join(codexPolicyArgs("http://127.0.0.1:1/mcp", "", 15*time.Minute), " ")
	if !strings.Contains(args, "mcp_servers."+mcpServerName+".tool_timeout_sec=900") {
		t.Fatalf("tool timeout missing from codex args: %s", args)
	}
	exec := strings.Join(buildCodexExecArgs("http://x/mcp", "i", "", "", 2*time.Minute), " ")
	if !strings.Contains(exec, ".tool_timeout_sec=120") || !strings.HasSuffix(exec, " -") {
		t.Fatalf("codex exec args: %s", exec)
	}
	if strings.Contains(strings.Join(codexPolicyArgs("http://x/mcp", "", 0), " "), "tool_timeout_sec") {
		t.Fatal("zero timeout must keep the CLI default")
	}
}

func TestClaudeEnvRaisesMCPToolTimeout(t *testing.T) {
	env := claudeMCPTimeoutEnv(15 * time.Minute)
	if len(env) != 1 || env[0] != "MCP_TOOL_TIMEOUT=900000" {
		t.Fatalf("claude env: %v", env)
	}
	if claudeMCPTimeoutEnv(0) != nil {
		t.Fatal("zero timeout must keep the CLI default")
	}
}

func TestDefaultLimitsFillNewTimeouts(t *testing.T) {
	l := Limits{}.withDefaults()
	if l.MCPToolTimeout <= 0 || l.MCPShutdownTimeout <= 0 || l.InterruptWait <= 0 {
		t.Fatalf("defaults not applied: %+v", l)
	}
}

// progressingBinaries reports progress from inside the tool, like a workflow run does.
type progressingBinaries struct{ fakeBinaries }

func (progressingBinaries) List(ctx context.Context) ([]domainbinaries.BinaryInfo, error) {
	for i := 0; i < 3; i++ {
		progress.Report(ctx, progress.Update{Percent: float64(i * 30)})
	}
	return nil, nil
}

type bearer struct{ token string }

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(r)
}

// Over the real HTTP MCP server: a tool call and the progress the tool reports re-arm the
// running chat turn's idle timeout, so a long workflow started by the agent does not get the
// turn cut off while it is still working.
func TestMCPToolActivityKeepsTurnAlive(t *testing.T) {
	base := t.TempDir()
	svc, err := New(Options{
		Repo: newFakeRepo(), DataDir: base,
		WorkspaceDir: filepath.Join(base, "ws"), MediaDir: filepath.Join(base, "media"), ProfilesDir: filepath.Join(base, "p"),
		Logger: slog.New(slog.DiscardHandler), Binaries: progressingBinaries{},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()

	var touches atomic.Int32
	svc.mu.Lock()
	svc.turn = newTurn("msg-1", "", "", 0, func() { touches.Add(1) }, func() {})
	svc.mu.Unlock()

	srv, err := svc.ensureMCPServer()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	transport := &mcp.StreamableClientTransport{Endpoint: srv.url, HTTPClient: &http.Client{Transport: bearer{srv.token}}}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(ctx, transport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	before := touches.Load()
	if _, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "binaries_list", Arguments: map[string]any{}}); err != nil {
		t.Fatal(err)
	}
	// One touch for the request itself plus one per progress report made inside the tool.
	if got := touches.Load() - before; got < 4 {
		t.Fatalf("turn touched %d times by the tool call, want >= 4 (request + 3 progress reports)", got)
	}
}

// Streamed chat events for the active message re-arm the turn; events of another message do not.
func TestChatEventsTouchOnlyTheActiveTurn(t *testing.T) {
	svc := &Service{models: newModelState(), log: slog.New(slog.DiscardHandler)}
	var touches atomic.Int32
	svc.turn = newTurn("msg-1", "", "", 0, func() { touches.Add(1) }, func() {})
	svc.emitChatRaw(domainagent.ChatEvent{MessageID: "msg-1", Type: domainagent.ChatEventDelta, Text: "hi"})
	svc.emitChatRaw(domainagent.ChatEvent{MessageID: "msg-0", Type: domainagent.ChatEventDelta, Text: "old"})
	if touches.Load() != 1 {
		t.Fatalf("touches = %d, want 1", touches.Load())
	}
}

// Every Limits field must come from config: a new field left out of LimitsFrom would silently
// run with zero (then a hidden fallback) instead of the configured value.
func TestLimitsFromConfigFillsEveryField(t *testing.T) {
	l := LimitsFrom(config.Default(config.ModeDesktop))
	v := reflect.ValueOf(l)
	for i := 0; i < v.NumField(); i++ {
		if v.Field(i).IsZero() {
			t.Errorf("Limits.%s is not mapped from config", v.Type().Field(i).Name)
		}
	}
	cfg := config.Default(config.ModeDesktop)
	cfg.Agent.TurnTimeout = 42 * time.Second
	cfg.Realtime.LongActionTimeout = 7 * time.Minute
	got := LimitsFrom(cfg)
	if got.TurnTimeout != 42*time.Second || got.MCPToolTimeout != 7*time.Minute {
		t.Fatalf("config values not used: %+v", got)
	}
}
