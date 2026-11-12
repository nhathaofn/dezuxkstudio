package agent

import (
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	domainagent "app/internal/domain/agent"
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
