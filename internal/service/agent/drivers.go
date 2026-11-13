package agent

import (
	"context"
	"fmt"
	"strings"

	domainagent "app/internal/domain/agent"
)

// providerDriver holds everything that differs between providers, so Service's lifecycle code (probe,
// login, logout, connect, turns) has no per-provider branches. A provider is either a turnDriver (each
// chat turn is its own CLI run) or a sessionDriver (turns run over one long-lived ACP connection).
// Adding a provider means a Registry entry plus a driver in newDrivers.
type providerDriver interface {
	// cheapAuthProbe reports whether probeAuth only reads local files (no status cache needed).
	cheapAuthProbe() bool
	probeAuth(ctx context.Context, executable string) (domainagent.AuthState, string)
	login(ctx context.Context, executable, apiKey string) error
	logout(ctx context.Context, executable string) error
	// release frees long-lived resources (processes, tokens) after disconnect, sign-in or sign-out.
	release()
	// toolCallHint returns how the model reaches the app's tools when they are NOT listed to it natively
	// (e.g. Codex code mode, where tools are discovered at runtime); "" when the provider lists them.
	// A non-empty hint makes the system prompt include a generated <tools> layer.
	toolCallHint() string
}

// turnDriver runs every chat turn as its own CLI invocation (Codex, Claude).
type turnDriver interface {
	providerDriver
	// prepare readies the isolated profile before the provider is marked connected.
	prepare() error
	// loadModels reads the models the CLI offers to the signed-in account.
	loadModels(ctx context.Context, executable string) ([]domainagent.ModelOption, error)
	runTurn(ctx context.Context, executable, workDir, mcpURL, mcpToken, messageID, prompt string) error
}

// sessionDriver runs turns over a long-lived ACP connection (Antigravity).
type sessionDriver interface {
	providerDriver
	// sessionEnv prepares the isolated profile wired to DezuxkStudio's MCP server and returns the
	// environment of the ACP process.
	sessionEnv(mcpURL, mcpToken string) ([]string, error)
}

func newDrivers(s *Service) map[domainagent.ProviderID]providerDriver {
	return map[domainagent.ProviderID]providerDriver{
		domainagent.ProviderCodex:       codexDriver{s},
		domainagent.ProviderClaude:      claudeDriver{s},
		domainagent.ProviderAntigravity: agyDriver{s},
	}
}

func (s *Service) driver(id domainagent.ProviderID) providerDriver {
	if d, ok := s.drivers[id]; ok {
		return d
	}
	return unsupportedDriver{id}
}

// turnDriverFor returns id's driver when it runs turns as CLI invocations.
func (s *Service) turnDriverFor(id domainagent.ProviderID) (turnDriver, bool) {
	d, ok := s.driver(id).(turnDriver)
	return d, ok
}

func (s *Service) isPerTurn(id domainagent.ProviderID) bool {
	_, ok := s.turnDriverFor(id)
	return ok
}

type codexDriver struct{ s *Service }

func (d codexDriver) profile() string    { return d.s.profileDirFor(domainagent.ProviderCodex) }
func (codexDriver) cheapAuthProbe() bool { return false }
func (d codexDriver) release()           { d.s.closeCodexAppServer() }

// toolCallHint depends on the selected model: code-mode models (GPT-5.6/GPT-6 today) only reach MCP tools
// through the code runner, other models get them as regular tool definitions and need no <tools> layer.
func (d codexDriver) toolCallHint() string {
	call := "tools." + mcpToolPrefix + "<name>(args)"
	switch d.s.toolsVisibility(domainagent.ProviderCodex) {
	case "native":
		return ""
	case "code":
		return "Call them from the code runner as " + call + "."
	}
	return "If they are not in your tool list, call them from the code runner as " + call + "."
}
func (d codexDriver) loadModels(ctx context.Context, exe string) ([]domainagent.ModelOption, error) {
	return d.s.loadCodexModels(ctx, exe)
}
func (d codexDriver) prepare() error { _, err := prepareCodexProfile(d.profile()); return err }
func (d codexDriver) login(ctx context.Context, exe, apiKey string) error {
	if strings.TrimSpace(apiKey) == "" {
		return d.s.loginCodexInteractive(ctx, exe, d.profile())
	}
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
func (claudeDriver) cheapAuthProbe() bool { return false }
func (claudeDriver) release()             {}
func (claudeDriver) toolCallHint() string { return "" }
func (d claudeDriver) prepare() error {
	_, err := d.s.secrets.prepareClaudeProfile(d.profile())
	return err
}
func (claudeDriver) loadModels(ctx context.Context, exe string) ([]domainagent.ModelOption, error) {
	return loadClaudeModels(ctx, exe)
}
func (d claudeDriver) login(ctx context.Context, exe, apiKey string) error {
	if strings.TrimSpace(apiKey) == "" {
		return d.s.loginClaudeInteractive(ctx, exe, d.profile())
	}
	return d.s.secrets.loginClaude(ctx, exe, d.profile(), apiKey)
}
func (d claudeDriver) probeAuth(ctx context.Context, exe string) (domainagent.AuthState, string) {
	return d.s.probeClaude(ctx, exe, d.profile())
}

// logout always runs: it also removes the isolated API key, which needs no executable.
func (d claudeDriver) logout(ctx context.Context, exe string) error {
	return d.s.secrets.logoutClaude(ctx, exe, d.profile())
}
func (d claudeDriver) runTurn(ctx context.Context, exe, workDir, mcpURL, mcpToken, messageID, prompt string) error {
	return d.s.runClaude(ctx, exe, d.profile(), workDir, mcpURL, mcpToken, messageID, prompt)
}

type agyDriver struct{ s *Service }

func (d agyDriver) profile() string    { return d.s.profileDirFor(domainagent.ProviderAntigravity) }
func (agyDriver) cheapAuthProbe() bool { return true }
func (d agyDriver) release()           { cleanupAgyProfileToken(d.profile()) }
func (agyDriver) toolCallHint() string { return "" }
func (d agyDriver) probeAuth(context.Context, string) (domainagent.AuthState, string) {
	return d.s.secrets.probeAgyAuth(d.profile())
}
func (d agyDriver) login(ctx context.Context, exe, apiKey string) error {
	if strings.TrimSpace(apiKey) != "" {
		return d.s.secrets.saveAgyAPIKey(d.profile(), apiKey)
	}
	return d.s.loginAgyOAuth(ctx, domainagent.Registry[domainagent.ProviderAntigravity], exe)
}
func (d agyDriver) logout(context.Context, string) error {
	d.s.secrets.clearAgyAuth(d.profile())
	cleanupAgyProfileToken(d.profile())
	return nil
}
func (d agyDriver) sessionEnv(mcpURL, mcpToken string) ([]string, error) {
	_, env, err := d.s.secrets.prepareAgyProfileMode(d.profile(), mcpURL, mcpToken, d.s.developerMode)
	if err != nil {
		return nil, err
	}
	return providerEnv(domainagent.ProviderAntigravity, env), nil
}

type unsupportedDriver struct{ id domainagent.ProviderID }

func (unsupportedDriver) cheapAuthProbe() bool { return true }
func (unsupportedDriver) release()             {}
func (unsupportedDriver) toolCallHint() string { return "" }
func (unsupportedDriver) probeAuth(context.Context, string) (domainagent.AuthState, string) {
	return domainagent.AuthUnknown, ""
}
func (d unsupportedDriver) login(context.Context, string, string) error {
	return fmt.Errorf("unsupported provider %s", d.id)
}
func (unsupportedDriver) logout(context.Context, string) error { return nil }
