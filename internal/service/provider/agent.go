package provider

import (
	"context"
	"strings"

	domain "app/internal/domain/provider"
)

// The methods in this file serve the DezuxkStudio agent (MCP tools). They return JSON-ready maps that
// never contain a secret value: only masked hints.

// AgentOverview lists providers, what each needs and what is configured.
func (s *Service) AgentOverview(ctx context.Context) (any, error) {
	snap, err := s.List(ctx)
	if err != nil {
		return nil, err
	}
	type field struct {
		Key      string `json:"key"`
		Secret   bool   `json:"secret"`
		Required bool   `json:"required"`
	}
	type cred struct {
		ID          string            `json:"id"`
		Name        string            `json:"name"`
		Active      bool              `json:"active"`
		Values      map[string]string `json:"values,omitempty"`
		SecretHints map[string]string `json:"secret_hints,omitempty"`
		Health      map[string]any    `json:"health,omitempty"`
	}
	type builtin struct {
		ID               string              `json:"id"`
		Name             string              `json:"name"`
		Capabilities     []domain.Capability `json:"capabilities"`
		DefaultBaseURL   string              `json:"default_base_url,omitempty"`
		Fields           []field             `json:"fields"`
		CredentialGroups [][]string          `json:"credential_groups,omitempty"`
		Configured       bool                `json:"configured"`
		Credentials      []cred              `json:"credentials,omitempty"`
	}
	type custom struct {
		ID              string                 `json:"id"`
		Name            string                 `json:"name"`
		DiscoveryFormat domain.DiscoveryFormat `json:"discovery_format"`
		BaseURL         string                 `json:"base_url"`
		KeyHint         string                 `json:"key_hint,omitempty"`
		Capabilities    []domain.Capability    `json:"capabilities"`
		DefaultModels   map[string]string      `json:"default_models,omitempty"`
		EnabledModels   int                    `json:"enabled_models"`
		Health          map[string]any         `json:"health,omitempty"`
	}
	var bs []builtin
	for _, b := range snap.Builtins {
		v := builtin{ID: b.Meta.ID, Name: b.Meta.DisplayName, Capabilities: b.Meta.Capabilities,
			DefaultBaseURL: b.Meta.DefaultBaseURL, CredentialGroups: b.Meta.CredentialGroups, Configured: b.Configured()}
		for _, f := range b.Meta.Fields {
			v.Fields = append(v.Fields, field{Key: f.Key, Secret: f.Secret, Required: f.Required})
		}
		for _, c := range b.Credentials {
			v.Credentials = append(v.Credentials, cred{ID: c.ID, Name: c.Name, Active: c.Active, Values: c.Values, SecretHints: c.SecretHints, Health: agentHealth(c.Health)})
		}
		bs = append(bs, v)
	}
	var cs []custom
	for _, c := range snap.Custom {
		v := custom{ID: c.ID, Name: c.Name, DiscoveryFormat: c.DiscoveryFormat, BaseURL: c.BaseURL, KeyHint: c.KeyHint,
			Capabilities: c.Capabilities(), DefaultModels: map[string]string{}, Health: agentHealth(c.Health)}
		for _, m := range c.Models {
			if m.Enabled {
				v.EnabledModels++
				if m.IsDefault {
					v.DefaultModels[string(m.Capability())] = m.ModelID
				}
			}
		}
		cs = append(cs, v)
	}
	return map[string]any{
		"secret_backend": snap.SecretBackend,
		"builtin":        bs,
		"custom":         cs,
		"note":           "Secrets are masked. Use providers_setup to add a key, providers_test to check one.",
	}, nil
}

// PlanSetup resolves where a setup request goes without saving anything.
func (s *Service) PlanSetup(req domain.SetupRequest) (domain.SetupPlan, error) {
	return domain.PlanSetup(req)
}

// AgentSetup saves a key the user handed over, activates it and tests it.
// Built-in: a new credential that becomes active. Custom: models are discovered first (so a wrong
// key or URL saves nothing), every model is enabled and the first of each capability is the default.
func (s *Service) AgentSetup(ctx context.Context, req domain.SetupRequest) (any, error) {
	plan, err := domain.PlanSetup(req)
	if err != nil {
		return nil, err
	}
	key := strings.TrimSpace(req.APIKey)
	if plan.Kind == domain.SetupBuiltin {
		in := domain.Input{Name: plan.Name, Values: map[string]string{}, Secrets: map[string]string{}}
		for k, v := range req.Values {
			in.Values[k] = v
		}
		for k, v := range req.Secrets {
			in.Secrets[k] = v
		}
		if key != "" {
			in.Secrets[domain.FieldAPIKey] = key
		}
		if plan.BaseURL != "" {
			in.Values[domain.FieldBaseURL] = plan.BaseURL
		}
		c, err := s.CreateCredential(ctx, plan.ProviderID, in)
		if err != nil {
			return nil, err
		}
		if !c.Active {
			if err := s.ActivateCredential(ctx, c.ID); err != nil {
				return nil, err
			}
		}
		res, err := s.TestCredential(ctx, c.ID)
		if err != nil {
			return nil, err
		}
		m, _ := domain.Lookup(plan.ProviderID)
		return map[string]any{
			"saved": true, "kind": plan.Kind, "provider_id": plan.ProviderID, "credential_id": c.ID,
			"name": c.Name, "active": true, "capabilities": m.Capabilities, "secret_hints": c.SecretHints,
			"test": agentTest(res),
		}, nil
	}

	models, err := s.DiscoverModels(ctx, DiscoverRequest{DiscoveryFormat: plan.DiscoveryFormat, BaseURL: plan.BaseURL, APIKey: key})
	if err != nil {
		return nil, err
	}
	in := domain.CustomInput{Name: plan.Name, DiscoveryFormat: plan.DiscoveryFormat, BaseURL: plan.BaseURL, APIKey: key}
	hasDefault := map[domain.Capability]bool{}
	for _, d := range models {
		cm := domain.CustomModel{ModelID: d.ModelID, Endpoint: d.Endpoint, Enabled: true}
		if c := cm.Capability(); !hasDefault[c] {
			cm.IsDefault, hasDefault[c] = true, true
		}
		in.Models = append(in.Models, cm)
	}
	p, err := s.CreateCustom(ctx, in)
	if err != nil {
		return nil, err
	}
	defaults := map[string]string{}
	for _, m := range p.Models {
		if m.IsDefault {
			defaults[string(m.Capability())] = m.ModelID
		}
	}
	return map[string]any{
		"saved": true, "kind": plan.Kind, "custom_id": p.ID, "name": p.Name, "discovery_format": p.DiscoveryFormat,
		"base_url": p.BaseURL, "key_hint": p.KeyHint, "capabilities": p.Capabilities(), "models": len(p.Models),
		"default_models": defaults,
		"test":           map[string]any{"ok": true, "outcome": OutcomeOK, "detail": "models listed"},
	}, nil
}

// AgentTest tests a credential id or a custom provider id.
func (s *Service) AgentTest(ctx context.Context, id string) (any, error) {
	if _, err := s.repo.GetCredential(ctx, id); err == nil {
		res, err := s.TestCredential(ctx, id)
		if err != nil {
			return nil, err
		}
		return agentTest(res), nil
	}
	res, err := s.TestCustom(ctx, id)
	if err != nil {
		return nil, err
	}
	return agentTest(res), nil
}

// AgentDelete deletes a credential id or a custom provider id.
func (s *Service) AgentDelete(ctx context.Context, id string) error {
	if _, err := s.repo.GetCredential(ctx, id); err == nil {
		return s.DeleteCredential(ctx, id)
	}
	return s.DeleteCustom(ctx, id)
}

// Describe names a credential or custom provider id for approval texts.
func (s *Service) Describe(ctx context.Context, id string) (string, error) {
	if c, err := s.repo.GetCredential(ctx, id); err == nil {
		m, _ := domain.Lookup(c.ProviderID)
		return m.DisplayName + " / " + c.Name, nil
	}
	p, err := s.repo.GetCustom(ctx, id)
	if err != nil {
		return "", err
	}
	return p.Name + " (custom)", nil
}

func agentTest(r TestResult) map[string]any {
	out := map[string]any{"ok": r.OK(), "outcome": r.Outcome, "latency_ms": r.LatencyMs}
	if r.Status != 0 {
		out["status"] = r.Status
	}
	if r.Detail != "" {
		out["detail"] = r.Detail
	}
	if n := len(r.Models); n > 0 {
		if n > 10 {
			out["models_sample"], out["models_total"] = r.Models[:10], n
		} else {
			out["models"] = r.Models
		}
	}
	return out
}

func agentHealth(h domain.Health) map[string]any {
	if h.TestedAt.IsZero() {
		return nil
	}
	return map[string]any{"outcome": h.Outcome, "dead": h.Outcome.Dead(), "tested_at": h.TestedAt, "detail": h.Detail}
}
