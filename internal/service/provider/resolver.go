package provider

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"app/internal/apperr"
	domainagent "app/internal/domain/agent"
	"app/internal/secrets"
)

// AgentProviderLink re-exports domainagent.ProviderLink for service usage.
type AgentProviderLink = domainagent.ProviderLink

// ProviderLinkRepository defines the persistence operations for agent provider links.
type ProviderLinkRepository = domainagent.ProviderLinkRepository

// CompatibilityRule defines an allowed combination of agent provider, runner driver,
// target API provider, and authentication method.
type CompatibilityRule struct {
	AgentProviderID  string   `json:"agent_provider_id"`
	RunnerDriver     string   `json:"runner_driver"`      // "acp", "cli", etc.
	TargetProviderID string   `json:"target_provider_id"` // "gemini-aistudio", "openai", "anthropic"
	AuthMethod       string   `json:"auth_method"`        // "api_key"
	EnvVars          []string `json:"env_vars"`           // environment variable names for injection
}

// CompatibilityMatrix defines the authoritative rules for linking agent runners to API providers.
// Note: Same vendor does NOT imply compatibility (e.g. gemini-vertex uses service account JSON,
// which Antigravity ACP runner does not support).
var CompatibilityMatrix = []CompatibilityRule{
	{
		AgentProviderID:  "antigravity",
		RunnerDriver:     "acp",
		TargetProviderID: "gemini-aistudio",
		AuthMethod:       "api_key",
		EnvVars:          []string{"GEMINI_API_KEY", "GOOGLE_API_KEY"},
	},
	{
		AgentProviderID:  "codex",
		RunnerDriver:     "cli",
		TargetProviderID: "openai",
		AuthMethod:       "api_key",
		EnvVars:          []string{"OPENAI_API_KEY"},
	},
	{
		AgentProviderID:  "claude",
		RunnerDriver:     "cli",
		TargetProviderID: "anthropic",
		AuthMethod:       "api_key",
		EnvVars:          []string{"ANTHROPIC_API_KEY"},
	},
}

// normalizeDriver maps driver aliases to canonical runner drivers ("acp" or "cli").
func normalizeDriver(driver string) string {
	d := strings.ToLower(strings.TrimSpace(driver))
	switch d {
	case "acp", "session", "sessiondriver":
		return "acp"
	case "cli", "turn", "turndriver", "app-server", "appserver":
		return "cli"
	default:
		return d
	}
}

// NormalizeAgentID cleans an agent provider ID.
func NormalizeAgentID(id string) string {
	return strings.ToLower(strings.TrimSpace(id))
}

// FindCompatibilityRule looks up the compatibility rule matching the parameters.
// If runnerDriver is empty, it checks against the default driver for the agent provider.
func FindCompatibilityRule(agentProviderID, runnerDriver, targetProviderID, authMethod string) (CompatibilityRule, bool) {
	aid := NormalizeAgentID(agentProviderID)
	rdrv := normalizeDriver(runnerDriver)
	tpid := strings.ToLower(strings.TrimSpace(targetProviderID))
	am := strings.ToLower(strings.TrimSpace(authMethod))
	if am == "" {
		am = "api_key" // default credential auth method
	}

	for _, rule := range CompatibilityMatrix {
		if rule.AgentProviderID != aid {
			continue
		}
		if rdrv != "" && rule.RunnerDriver != rdrv {
			continue
		}
		if rule.TargetProviderID != tpid {
			continue
		}
		if rule.AuthMethod != am {
			continue
		}
		return rule, true
	}
	return CompatibilityRule{}, false
}

// IsCompatible checks if an agent provider can link to a target provider under the given driver and auth method.
func IsCompatible(agentProviderID, runnerDriver, targetProviderID, authMethod string) bool {
	_, ok := FindCompatibilityRule(agentProviderID, runnerDriver, targetProviderID, authMethod)
	return ok
}

// ResolveCompatibleProviders returns the list of allowed target provider IDs for an agent provider and driver.
func ResolveCompatibleProviders(agentProviderID, runnerDriver string) []string {
	aid := NormalizeAgentID(agentProviderID)
	rdrv := normalizeDriver(runnerDriver)

	var allowed []string
	seen := make(map[string]bool)
	for _, rule := range CompatibilityMatrix {
		if rule.AgentProviderID != aid {
			continue
		}
		if rdrv != "" && rule.RunnerDriver != rdrv {
			continue
		}
		if !seen[rule.TargetProviderID] {
			seen[rule.TargetProviderID] = true
			allowed = append(allowed, rule.TargetProviderID)
		}
	}
	if allowed == nil {
		allowed = []string{}
	}
	return allowed
}

// ResolvedLink represents a verified, ready-to-inject linked credential with plain secret.
type ResolvedLink struct {
	AgentProviderID string            `json:"agent_provider_id"`
	ProviderID      string            `json:"provider_id"`
	CredentialID    string            `json:"credential_id"`
	AuthMethod      string            `json:"auth_method"`
	Secret          string            `json:"-"` // plain secret, never marshaled to JSON
	EnvOverrides    map[string]string `json:"-"` // injected env vars
}

// ResolveLinkedSecret retrieves the opt-in linked secret for an agent provider.
// Returns (nil, nil) if no link is configured.
// Returns an error if a link is configured but is incompatible, or the secret is missing.
// CRITICAL DESIGN RULE: NO AUTO-FALLBACK. This function never falls back to unlinked credentials or OAuth.
func ResolveLinkedSecret(
	ctx context.Context,
	agentProviderID string,
	runnerDriver string,
	linkRepo ProviderLinkRepository,
	secretStore secrets.Store,
) (*ResolvedLink, error) {
	if linkRepo == nil {
		return nil, nil
	}
	aid := NormalizeAgentID(agentProviderID)
	link, err := linkRepo.GetProviderLink(ctx, aid)
	if err != nil {
		// Not found is normal: no opt-in link set; any other repository error must fail closed (never auto-fallback).
		var ae *apperr.Error
		if errors.As(err, &ae) && ae.Code == apperr.CodeNotFound {
			return nil, nil
		}
		return nil, apperr.Wrap(apperr.CodeUnavailable, "failed to load agent provider link", err)
	}
	if link.AgentProviderID == "" || link.ProviderID == "" || link.CredentialID == "" {
		return nil, nil
	}

	// Verify compatibility with the matrix
	rule, ok := FindCompatibilityRule(aid, runnerDriver, link.ProviderID, link.AuthMethod)
	if !ok {
		return nil, apperr.Validation(fmt.Sprintf(
			"linked provider %q (auth: %s) is not compatible with agent %q (driver: %s)",
			link.ProviderID, link.AuthMethod, aid, runnerDriver,
		))
	}

	if secretStore == nil {
		return nil, apperr.New(apperr.CodeUnavailable, "secret store is unavailable")
	}

	// Read secret from secretStore
	key := secrets.Key("provider_cred_" + link.CredentialID + "_api_key")
	secretVal, err := secretStore.Get(key)
	if errors.Is(err, secrets.ErrNotFound) || strings.TrimSpace(secretVal) == "" {
		// Check custom key fallback if applicable
		customKey := secrets.Key("provider_custom_" + link.ProviderID + "_api_key")
		if cv, cerr := secretStore.Get(customKey); cerr == nil && strings.TrimSpace(cv) != "" {
			secretVal = cv
		} else {
			return nil, apperr.New(apperr.CodeIntegrity, fmt.Sprintf(
				"linked credential secret for %s is missing in credential store; re-enter the API key in Settings > Providers",
				link.ProviderID,
			))
		}
	} else if err != nil {
		return nil, apperr.Wrap(apperr.CodeUnavailable, "failed to read linked credential from secret store", err)
	}

	secretVal = strings.TrimSpace(secretVal)
	envOverrides := make(map[string]string, len(rule.EnvVars))
	for _, envVar := range rule.EnvVars {
		envOverrides[envVar] = secretVal
	}

	return &ResolvedLink{
		AgentProviderID: aid,
		ProviderID:      link.ProviderID,
		CredentialID:    link.CredentialID,
		AuthMethod:      link.AuthMethod,
		Secret:          secretVal,
		EnvOverrides:    envOverrides,
	}, nil
}
