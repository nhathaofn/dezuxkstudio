package agent

import (
	"context"
	"encoding/json"
	"time"

	providernodes "app/internal/tools/provider"
)

// providerSnapshot is the live provider state placed in studio_list_capabilities, so the agent's
// first look at the app already answers "which providers are set up" with fresh data. It reuses the
// provider list node (the single source of this information) instead of reading the repository, and
// carries no secrets: the node only returns masked hints and this keeps a compact subset.
func (s *Service) providerSnapshot(ctx context.Context) map[string]any {
	if s.workflow == nil {
		return nil
	}
	raw, err := s.workflow.RunNode(ctx, providernodes.NodeList, json.RawMessage(`{}`))
	if err != nil {
		return nil
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	var overview struct {
		SecretBackend string `json:"secret_backend"`
		Builtin       []struct {
			ID            string            `json:"id"`
			Name          string            `json:"name"`
			Capabilities  []string          `json:"capabilities"`
			Configured    bool              `json:"configured"`
			EnabledModels int               `json:"enabled_models"`
			DefaultModels map[string]string `json:"default_models"`
			Credentials   []struct {
				Active bool           `json:"active"`
				Health map[string]any `json:"health"`
			} `json:"credentials"`
		} `json:"builtin"`
		Custom []struct {
			ID            string            `json:"id"`
			Name          string            `json:"name"`
			Capabilities  []string          `json:"capabilities"`
			EnabledModels int               `json:"enabled_models"`
			DefaultModels map[string]string `json:"default_models"`
			Health        map[string]any    `json:"health"`
		} `json:"custom"`
		DefaultModels map[string]any `json:"default_models"`
	}
	if json.Unmarshal(b, &overview) != nil {
		return nil
	}
	type entry struct {
		ID            string            `json:"id"`
		Name          string            `json:"name"`
		Custom        bool              `json:"custom,omitempty"`
		Capabilities  []string          `json:"capabilities"`
		EnabledModels int               `json:"enabled_models"`
		DefaultModels map[string]string `json:"default_models,omitempty"`
		KeyHealth     map[string]any    `json:"key_health,omitempty"`
	}
	configured := []entry{}
	notConfigured := []string{}
	for _, p := range overview.Builtin {
		if !p.Configured {
			notConfigured = append(notConfigured, p.ID)
			continue
		}
		e := entry{ID: p.ID, Name: p.Name, Capabilities: p.Capabilities, EnabledModels: p.EnabledModels, DefaultModels: p.DefaultModels}
		for _, c := range p.Credentials {
			if c.Active {
				e.KeyHealth = c.Health
			}
		}
		configured = append(configured, e)
	}
	for _, p := range overview.Custom {
		configured = append(configured, entry{ID: p.ID, Name: p.Name, Custom: true, Capabilities: p.Capabilities,
			EnabledModels: p.EnabledModels, DefaultModels: p.DefaultModels, KeyHealth: p.Health})
	}
	return map[string]any{
		"note": "LIVE state read just now; it changes while the user works in Settings. " +
			"Details and keys (masked) are in tool.provider_list.",
		"checked_at":           time.Now().Format(time.RFC3339),
		"configured_providers": configured,
		"not_configured":       notConfigured,
		"default_model_by_use": overview.DefaultModels,
		"secret_storage":       overview.SecretBackend,
	}
}
