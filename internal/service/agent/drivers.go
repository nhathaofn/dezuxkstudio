package agent

import (
	"context"
	"fmt"
	"strings"

	domainagent "app/internal/domain/agent"
)

// providerDriver holds everything that differs between providers, so Service's lifecycle code (probe,
// login, logout, connect, turns) has no per-provider switch statements. Adding a provider means adding a
// Registry entry and a driver.
type providerDriver interface {
	// perTurn reports whether every chat turn runs as its own CLI invocation (Codex, Claude) instead of
	// over a long-lived ACP connection (Antigravity).
	perTurn() bool
	// cheapAuthProbe reports whether probeAuth only reads local files (no status cache needed).
	cheapAuthProbe() bool
	probeAuth(ctx context.Context, executable string) (domainagent.AuthState, string)
	login(ctx context.Context, executable, apiKey string) error
	logout(ctx context.Context, executable string) error
	// prepare readies the isolated profile before a per-turn provider is marked connected.
	prepare() error
	// runTurn executes one per-turn chat turn.
	runTurn(ctx context.Context, executable, workDir, mcpURL, mcpToken, messageID, prompt string) error
	// release frees long-lived resources (processes, tokens) after disconnect, sign-in or sign-out.
	release()
}

func (s *Service) driver(id domainagent.ProviderID) providerDriver {
	switch id {
	case domainagent.ProviderCodex:
		return codexDriver{s}
	case domainagent.ProviderClaude:
		return claudeDriver{s}
	case domainagent.ProviderAntigravity:
		return agyDriver{s}
	}
	return unsupportedDriver{id}
}

func (s *Service) isPerTurn(id domainagent.ProviderID) bool { return s.driver(id).perTurn() }

type codexDriver struct{ s *Service }

func (d codexDriver) profile() string    { return d.s.profileDirFor(domainagent.ProviderCodex) }
func (codexDriver) perTurn() bool        { return true }
func (codexDriver) cheapAuthProbe() bool { return false }
func (d codexDriver) release()           { d.s.closeCodexAppServer() }
func (d codexDriver) prepare() error     { _, err := prepareCodexProfile(d.profile()); return err }
func (d codexDriver) login(ctx context.Context, exe, apiKey string) error {
	return loginCodex(ctx, exe, d.profile(), apiKey)
}
func (d codexDriver) probeAuth(ctx context.Context, exe string) (domainagent.AuthState, string) {
	return d.s.probeCodex(ctx, exe, d.profile())
}
func (d codexDriver) logout(ctx context.Context, exe string) error {
	if exe == "" {
		return nil
	}
	return logoutCodex(ctx, exe, d.profile())
}
func (d codexDriver) runTurn(ctx context.Context, exe, workDir, mcpURL, mcpToken, messageID, prompt string) error {
	return d.s.runCodex(ctx, exe, d.profile(), workDir, mcpURL, mcpToken, messageID, prompt)
}

type claudeDriver struct{ s *Service }

func (d claudeDriver) profile() string    { return d.s.profileDirFor(domainagent.ProviderClaude) }
func (claudeDriver) perTurn() bool        { return true }
func (claudeDriver) cheapAuthProbe() bool { return false }
func (claudeDriver) release()             {}
func (d claudeDriver) prepare() error     { _, err := prepareClaudeProfile(d.profile()); return err }
func (d claudeDriver) login(ctx context.Context, exe, apiKey string) error {
	return loginClaude(ctx, exe, d.profile(), apiKey)
}
func (d claudeDriver) probeAuth(ctx context.Context, exe string) (domainagent.AuthState, string) {
	return d.s.probeClaude(ctx, exe, d.profile())
}

// logout always runs: it also removes the isolated API key, which needs no executable.
func (d claudeDriver) logout(ctx context.Context, exe string) error {
	return logoutClaude(ctx, exe, d.profile())
}
func (d claudeDriver) runTurn(ctx context.Context, exe, workDir, mcpURL, mcpToken, messageID, prompt string) error {
	return d.s.runClaude(ctx, exe, d.profile(), workDir, mcpURL, mcpToken, messageID, prompt)
}

type agyDriver struct{ s *Service }

func (d agyDriver) profile() string    { return d.s.profileDirFor(domainagent.ProviderAntigravity) }
func (agyDriver) perTurn() bool        { return false }
func (agyDriver) cheapAuthProbe() bool { return true }
func (d agyDriver) release()           { cleanupAgyProfileToken(d.profile()) }
func (agyDriver) prepare() error       { return nil }
func (d agyDriver) probeAuth(context.Context, string) (domainagent.AuthState, string) {
	return probeAgyAuth(d.profile())
}
func (d agyDriver) login(ctx context.Context, exe, apiKey string) error {
	if strings.TrimSpace(apiKey) != "" {
		return saveAgyAPIKey(d.profile(), apiKey)
	}
	return d.s.loginAgyOAuth(ctx, domainagent.Registry[domainagent.ProviderAntigravity], exe)
}
func (d agyDriver) logout(context.Context, string) error {
	clearAgyAuth(d.profile())
	cleanupAgyProfileToken(d.profile())
	return nil
}
func (agyDriver) runTurn(context.Context, string, string, string, string, string, string) error {
	return fmt.Errorf("antigravity turns run over the ACP connection")
}

type unsupportedDriver struct{ id domainagent.ProviderID }

func (unsupportedDriver) perTurn() bool        { return false }
func (unsupportedDriver) cheapAuthProbe() bool { return true }
func (unsupportedDriver) release()             {}
func (unsupportedDriver) prepare() error       { return nil }
func (unsupportedDriver) probeAuth(context.Context, string) (domainagent.AuthState, string) {
	return domainagent.AuthUnknown, ""
}
func (d unsupportedDriver) login(context.Context, string, string) error {
	return fmt.Errorf("unsupported provider %s", d.id)
}
func (unsupportedDriver) logout(context.Context, string) error { return nil }
func (d unsupportedDriver) runTurn(context.Context, string, string, string, string, string, string) error {
	return fmt.Errorf("unsupported per-turn provider %s", d.id)
}
