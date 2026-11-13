package agent

import (
	"maps"
	"os"
	"time"

	"app/internal/buildinfo"
	"app/internal/config"
	domainagent "app/internal/domain/agent"
)

// clientName is the ACP clientInfo name and the app name reported to agents.
var clientName = buildinfo.ProductName

// Names shared by every provider integration. They identify DezuxkStudio's MCP server and handshake
// to the external CLIs, so they live in one place instead of being repeated per runner.
const (
	mcpServerName  = "dezuxk" // MCP server key in every provider's config
	mcpToolPrefix  = "mcp__" + mcpServerName + "__"
	mcpTokenEnvVar = "DEZUXK_AGENT_TOKEN" // env var carrying the per-process MCP bearer token
)

// Limits tunes timeouts, caches and chat context size. Zero fields fall back to DefaultLimits.
type Limits struct {
	TurnTimeout         time.Duration
	InitializeTimeout   time.Duration
	VersionProbeTimeout time.Duration
	AuthProbeTimeout    time.Duration
	LoginTimeout        time.Duration
	ModelCatalogTimeout time.Duration
	MCPToolTimeout      time.Duration // max duration of one MCP tool call the CLI waits for (workflow runs)
	MCPShutdownTimeout  time.Duration // graceful stop of the local MCP server
	InterruptWait       time.Duration // how long a cancelled Codex turn waits for turn/completed
	StatusCacheTTL      time.Duration
	ApprovalTTL         time.Duration
	MaxPendingApprovals int
	CLIUpdateInterval   time.Duration // how often the app looks for newer agent CLIs
	HistoryLimit        int
	ContextMessages     int
	ContextMessageChars int
}

// LimitsFrom builds the service limits from the application config — the only place their
// values (and defaults, in config.Default) are declared.
func LimitsFrom(c *config.Config) Limits {
	a := c.Agent
	return Limits{
		TurnTimeout:         a.TurnTimeout,
		InitializeTimeout:   a.InitializeTimeout,
		VersionProbeTimeout: a.VersionProbeTimeout,
		AuthProbeTimeout:    a.AuthProbeTimeout,
		LoginTimeout:        a.LoginTimeout,
		ModelCatalogTimeout: a.ModelCatalogTimeout,
		// An MCP tool call may run a whole workflow, which the hub bounds by the long action timeout.
		MCPToolTimeout:      c.Realtime.LongActionTimeout,
		MCPShutdownTimeout:  c.Server.ShutdownTimeout,
		InterruptWait:       a.InterruptWait,
		StatusCacheTTL:      a.StatusCacheTTL,
		ApprovalTTL:         a.ApprovalTTL,
		MaxPendingApprovals: a.MaxPendingApprovals,
		CLIUpdateInterval:   a.CLIUpdateInterval,
		HistoryLimit:        a.HistoryLimit,
		ContextMessages:     a.ContextMessages,
		ContextMessageChars: a.ContextMessageChars,
	}
}

// DefaultLimits are the config defaults, for standalone use (tests, tools).
func DefaultLimits() Limits { return LimitsFrom(config.Default(config.ModeDesktop)) }

func (l Limits) withDefaults() Limits {
	d := DefaultLimits()
	durations := []struct{ v, def *time.Duration }{
		{&l.TurnTimeout, &d.TurnTimeout},
		{&l.InitializeTimeout, &d.InitializeTimeout},
		{&l.VersionProbeTimeout, &d.VersionProbeTimeout},
		{&l.AuthProbeTimeout, &d.AuthProbeTimeout},
		{&l.LoginTimeout, &d.LoginTimeout},
		{&l.ModelCatalogTimeout, &d.ModelCatalogTimeout},
		{&l.MCPToolTimeout, &d.MCPToolTimeout},
		{&l.MCPShutdownTimeout, &d.MCPShutdownTimeout},
		{&l.InterruptWait, &d.InterruptWait},
		{&l.StatusCacheTTL, &d.StatusCacheTTL},
		{&l.ApprovalTTL, &d.ApprovalTTL},
		{&l.CLIUpdateInterval, &d.CLIUpdateInterval},
	}
	for _, f := range durations {
		if *f.v <= 0 {
			*f.v = *f.def
		}
	}
	ints := []struct{ v, def *int }{
		{&l.HistoryLimit, &d.HistoryLimit},
		{&l.ContextMessages, &d.ContextMessages},
		{&l.ContextMessageChars, &d.ContextMessageChars},
		{&l.MaxPendingApprovals, &d.MaxPendingApprovals},
	}
	for _, f := range ints {
		if *f.v <= 0 {
			*f.v = *f.def
		}
	}
	return l
}

// providerEnv returns the process environment for a provider CLI: the inherited environment minus the
// MCP token and the provider's credential variables, plus overrides (isolated profile dirs, token).
func providerEnv(id domainagent.ProviderID, overrides map[string]string) []string {
	strip := append([]string{mcpTokenEnvVar}, domainagent.Registry[id].CredentialEnv...)
	merged := maps.Clone(domainagent.Registry[id].Install.Env)
	if merged == nil {
		merged = map[string]string{}
	}
	maps.Copy(merged, overrides)
	return isolatedEnv(os.Environ(), merged, strip...)
}

// Tool result statuses sent in ChatEvent.Text/Error; the UI maps them to its own localized labels.
const (
	toolStatusCompleted = "completed"
	toolStatusFailed    = "failed"
)
