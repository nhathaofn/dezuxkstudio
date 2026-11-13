package provider

import (
	"slices"

	"app/internal/engine"
	"context"
	"strings"

	domain "app/internal/domain/provider"
)

// The methods in this file back the provider workflow nodes (internal/tools/provider), which the UI
// workflows and the agent run. They return JSON-ready maps that
// never contain a secret value: only masked hints.

// Overview lists providers, what each needs and what is configured.
func (s *Service) Overview(ctx context.Context) (any, error) {
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
		DefaultModels    map[string]string   `json:"default_models,omitempty"`
		EnabledModels    int                 `json:"enabled_models"`
		Limits           domain.Limits       `json:"limits"`
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
		Limits          domain.Limits          `json:"limits"`
	}
	summarize := func(ms []domain.Model) (map[string]string, int) {
		defs, n := map[string]string{}, 0
		for _, m := range ms {
			if m.Enabled {
				n++
				if m.IsDefault {
					defs[string(m.Capability)] = m.ModelID
				}
			}
		}
		return defs, n
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
		v.DefaultModels, v.EnabledModels = summarize(b.Models)
		v.Limits = domain.EffectiveLimits(b.Settings, snap.LimitDefaults, b.Meta.DefaultConcurrency)
		bs = append(bs, v)
	}
	var cs []custom
	for _, c := range snap.Custom {
		v := custom{ID: c.ID, Name: c.Name, DiscoveryFormat: c.DiscoveryFormat, BaseURL: c.BaseURL, KeyHint: c.KeyHint,
			Capabilities: c.Capabilities(), Health: agentHealth(c.Health),
			Limits: domain.EffectiveLimits(c.Settings, snap.LimitDefaults, nil)}
		v.DefaultModels, v.EnabledModels = summarize(c.Models)
		cs = append(cs, v)
	}
	defs, err := s.Defaults(ctx)
	if err != nil {
		return nil, err
	}
	purposes := map[string]any{}
	for _, d := range defs {
		purposes[string(d.Purpose)] = map[string]any{"chosen": d.Chosen.String(), "effective": d.Effective.String(),
			"chosen_unavailable": d.ChosenUnavailable, "candidates": len(d.Candidates)}
	}
	return map[string]any{
		"secret_backend": snap.SecretBackend,
		"builtin":        bs,
		"custom":         cs,
		"default_models": purposes,
		"note":           "Secrets are masked. Add a key with tool.provider_setup, choose models with tool.provider_set_default.",
	}, nil
}

// PlanSetup resolves where a setup request goes without saving anything.
func (s *Service) PlanSetup(req domain.SetupRequest) (domain.SetupPlan, error) {
	return domain.PlanSetup(req)
}

// Setup saves a key the user handed over, activates it and tests it.
// Built-in: a new credential that becomes active. Custom: models are discovered first (so a wrong
// key or URL saves nothing), every model is enabled and the first of each capability is the default.
func (s *Service) Setup(ctx context.Context, req domain.SetupRequest) (any, error) {
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
		c, err := s.createCredential(ctx, plan.ProviderID, in, false)
		if err != nil {
			return nil, err
		}
		if !c.Active {
			if err := s.activateCredential(ctx, c.ID, false); err != nil {
				return nil, err
			}
		}
		res, err := s.TestCredential(ctx, c.ID)
		if err != nil {
			return nil, err
		}
		m, _ := domain.Lookup(plan.ProviderID)
		if s.autoOn && res.OK() && m.Discoverable() {
			if _, derr := s.DiscoverBuiltinModels(ctx, plan.ProviderID); derr != nil && ctx.Err() == nil {
				_ = s.repo.SetCredentialHealth(ctx, c.ID, modelsFailed(derr))
			}
		}
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
	var found []domain.Model
	for _, d := range models {
		found = append(found, d.AsModel())
	}
	in.Models = domain.MergeDiscovered(nil, found)
	p, err := s.createCustom(ctx, in, false)
	if err != nil {
		return nil, err
	}
	// DiscoverModels already proved the endpoint works and listed its models; record health directly
	// instead of launching a redundant background auto-check.
	s.recordHealth(ctx, func(h domain.Health) error { return s.repo.SetCustomHealth(ctx, p.ID, h) }, TestResult{Outcome: OutcomeOK})
	defaults := map[string]string{}
	for _, m := range p.Models {
		if m.IsDefault {
			defaults[string(m.Capability)] = m.ModelID
		}
	}
	return map[string]any{
		"saved": true, "kind": plan.Kind, "custom_id": p.ID, "name": p.Name, "discovery_format": p.DiscoveryFormat,
		"base_url": p.BaseURL, "key_hint": p.KeyHint, "capabilities": p.Capabilities(), "models": len(p.Models),
		"default_models": defaults,
		"test":           map[string]any{"ok": true, "outcome": OutcomeOK, "detail": "models listed"},
	}, nil
}

// TestByID tests a credential id or a custom provider id.
func (s *Service) TestByID(ctx context.Context, id string) (any, error) {
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

// DeleteByID deletes a credential id or a custom provider id.
func (s *Service) DeleteByID(ctx context.Context, id string) error {
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

// SavedOptions lists saved credentials (and custom providers unless credentialsOnly) as select options
// for the provider nodes. Labels carry the masked key and a dead-key marker.
func (s *Service) SavedOptions(ctx context.Context, credentialsOnly bool) ([]engine.Option, error) {
	snap, err := s.List(ctx)
	if err != nil {
		return nil, err
	}
	label := func(parts ...string) string {
		return strings.Join(slices.DeleteFunc(parts, func(p string) bool { return p == "" }), " · ")
	}
	mark := func(h domain.Health, active bool) string {
		switch {
		case h.Outcome.Dead():
			return "DEAD"
		case active:
			return "active"
		}
		return ""
	}
	out := []engine.Option{}
	for _, b := range snap.Builtins {
		for _, c := range b.Credentials {
			out = append(out, engine.Option{Value: c.ID, Label: label(b.Meta.DisplayName, c.Name, c.SecretHints[domain.FieldAPIKey], mark(c.Health, c.Active))})
		}
	}
	if !credentialsOnly {
		for _, c := range snap.Custom {
			out = append(out, engine.Option{Value: c.ID, Label: label(c.Name+" (custom)", c.KeyHint, mark(c.Health, false))})
		}
	}
	return out, nil
}

// OwnerOptions lists built-in providers and custom providers (owners of models and settings).
func (s *Service) OwnerOptions(ctx context.Context) ([]engine.Option, error) {
	snap, err := s.List(ctx)
	if err != nil {
		return nil, err
	}
	out := []engine.Option{}
	for _, b := range snap.Builtins {
		out = append(out, engine.Option{Value: b.Meta.ID, Label: b.Meta.DisplayName})
	}
	for _, c := range snap.Custom {
		out = append(out, engine.Option{Value: c.ID, Label: c.Name + " (custom)"})
	}
	return out, nil
}

// ModelOptions lists the usable models for a purpose as "provider/model" options.
func (s *Service) ModelOptions(ctx context.Context, p domain.Purpose) ([]engine.Option, error) {
	defs, err := s.Defaults(ctx)
	if err != nil {
		return nil, err
	}
	out := []engine.Option{}
	for _, d := range defs {
		if p != "" && d.Purpose != p {
			continue
		}
		for _, c := range d.Candidates {
			opt := engine.Option{Value: c.Ref.String(), Label: c.ProviderName + " · " + c.Model.Label()}
			if !slices.ContainsFunc(out, func(o engine.Option) bool { return o.Value == opt.Value }) {
				out = append(out, opt)
			}
		}
	}
	return out, nil
}

// DiscoverModelsFor refreshes the model list of a built-in or custom provider.
func (s *Service) DiscoverModelsFor(ctx context.Context, ownerID string) (any, error) {
	var models []domain.Model
	if _, ok := domain.Lookup(ownerID); ok {
		var err error
		if models, err = s.DiscoverBuiltinModels(ctx, ownerID); err != nil {
			return nil, err
		}
	} else {
		p, err := s.repo.GetCustom(ctx, ownerID)
		if err != nil {
			return nil, err
		}
		found, err := s.DiscoverModels(ctx, DiscoverRequest{CustomID: p.ID, DiscoveryFormat: p.DiscoveryFormat, BaseURL: p.BaseURL})
		if err != nil {
			return nil, err
		}
		var add []domain.Model
		for _, d := range found {
			add = append(add, d.AsModel())
		}
		if models, err = s.SetModels(ctx, ownerID, domain.MergeDiscovered(p.Models, add)); err != nil {
			return nil, err
		}
	}
	enabled := map[string][]string{}
	for _, m := range models {
		if m.Enabled {
			enabled[string(m.Capability)] = append(enabled[string(m.Capability)], m.ModelID)
		}
	}
	return map[string]any{"ok": true, "models": len(models), "enabled_by_capability": enabled}, nil
}

// SetDefaultModel sets a purpose's default model and returns the resulting state.
func (s *Service) SetDefaultModel(ctx context.Context, p domain.Purpose, ref string) (any, error) {
	if err := s.SetDefault(ctx, p, ref); err != nil {
		return nil, err
	}
	defs, err := s.Defaults(ctx)
	if err != nil {
		return nil, err
	}
	for _, d := range defs {
		if d.Purpose == p {
			return map[string]any{"ok": true, "purpose": p, "chosen": d.Chosen.String(), "effective": d.Effective.String()}, nil
		}
	}
	return map[string]any{"ok": true}, nil
}
