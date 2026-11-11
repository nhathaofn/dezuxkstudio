package agent

import (
	"os"
	"time"

	"app/internal/buildinfo"
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
	StatusCacheTTL      time.Duration
	ApprovalTTL         time.Duration
	HistoryLimit        int
	ContextMessages     int
	ContextMessageChars int
}

// DefaultLimits mirrors config.Default().Agent so the service works standalone (tests, tools).
func DefaultLimits() Limits {
	return Limits{
		TurnTimeout:         10 * time.Minute,
		InitializeTimeout:   15 * time.Second,
		VersionProbeTimeout: 1500 * time.Millisecond,
		AuthProbeTimeout:    10 * time.Second,
		LoginTimeout:        3 * time.Minute,
		ModelCatalogTimeout: 30 * time.Second,
		StatusCacheTTL:      5 * time.Minute,
		ApprovalTTL:         30 * time.Minute,
		HistoryLimit:        200,
		ContextMessages:     10,
		ContextMessageChars: 1200,
	}
}

func (l Limits) withDefaults() Limits {
	d := DefaultLimits()
	durations := []struct{ v, def *time.Duration }{
		{&l.TurnTimeout, &d.TurnTimeout},
		{&l.InitializeTimeout, &d.InitializeTimeout},
		{&l.VersionProbeTimeout, &d.VersionProbeTimeout},
		{&l.AuthProbeTimeout, &d.AuthProbeTimeout},
		{&l.LoginTimeout, &d.LoginTimeout},
		{&l.ModelCatalogTimeout, &d.ModelCatalogTimeout},
		{&l.StatusCacheTTL, &d.StatusCacheTTL},
		{&l.ApprovalTTL, &d.ApprovalTTL},
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
	return isolatedEnv(os.Environ(), overrides, strip...)
}

// Tool result statuses sent in ChatEvent.Text/Error; the UI maps them to its own localized labels.
const (
	toolStatusCompleted = "completed"
	toolStatusFailed    = "failed"
)
