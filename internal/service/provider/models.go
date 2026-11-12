package provider

import (
	"context"
	"slices"

	"app/internal/apperr"
	domain "app/internal/domain/provider"
)

// Models, settings, default models and ResolveModel: the management layer generation features build on.
// Built-in and custom providers share one model list and one settings table, keyed by owner id.

// LimitDefaults returns the app-wide defaults used for unset provider settings.
func (s *Service) LimitDefaults() domain.LimitDefaults { return s.limitDefaults }

// owner resolves an owner id to its capabilities and (for custom providers) protocol.
type owner struct {
	builtin  bool
	meta     domain.Meta
	custom   domain.CustomProvider
	caps     []domain.Capability
	format   domain.DiscoveryFormat
	metaConc map[domain.Capability]int
}

func (s *Service) owner(ctx context.Context, id string) (owner, error) {
	if m, ok := domain.Lookup(id); ok {
		return owner{builtin: true, meta: m, caps: m.Capabilities, metaConc: m.DefaultConcurrency}, nil
	}
	p, err := s.repo.GetCustom(ctx, id)
	if err != nil {
		return owner{}, err
	}
	return owner{custom: p, caps: domain.Capabilities(), format: p.DiscoveryFormat}, nil
}

// SetModels replaces the model list of a built-in or custom provider (enable/disable, defaults, display
// names, facts, user-added ids).
func (s *Service) SetModels(ctx context.Context, ownerID string, models []domain.Model) ([]domain.Model, error) {
	o, err := s.owner(ctx, ownerID)
	if err != nil {
		return nil, err
	}
	norm, err := domain.NormalizeModels(models, o.caps, o.format)
	if err != nil {
		return nil, err
	}
	if norm == nil {
		norm = []domain.Model{}
	}
	if err := s.repo.ReplaceModels(ctx, ownerID, norm); err != nil {
		return nil, err
	}
	s.changed()
	return norm, nil
}

// DiscoverBuiltinModels lists the models of a built-in provider with its active key, adds the new ones
// (enabled) to the saved list without touching the user's choices, and returns the merged list.
// Providers whose API has no model list (Kling, Vidu, Ark tasks, Vertex token) report not supported:
// the user adds model ids by hand.
func (s *Service) DiscoverBuiltinModels(ctx context.Context, providerID string) ([]domain.Model, error) {
	m, err := lookup(providerID)
	if err != nil {
		return nil, err
	}
	r, err := s.Resolve(ctx, providerID)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, s.testTimeout)
	defer cancel()
	host := stripFirstSuffix(r.BaseURL, m.Probe.StripSuffixes) + m.Probe.Path
	key := r.Secrets[domain.FieldAPIKey]
	var found []DiscoveredModel
	switch m.Probe.Kind {
	case domain.ProbeOpenAIModels:
		// The same URL the connectivity test used (a built-in provider's base URL is stored as given):
		// discover() would add /v1 for custom endpoints, and then test and model list could disagree.
		var ids []string
		if ids, err = s.openAIModels(ctx, host, key, nil); err == nil {
			for _, id := range ids {
				if Usable(id) {
					found = append(found, discovered(id, InferOpenAIEndpoint(id)))
				}
			}
		}
	case domain.ProbeGeminiModels:
		found, err = s.discover(ctx, domain.DiscoveryGemini, host, key)
	case domain.ProbeAnthropicModels:
		found, err = s.discover(ctx, domain.DiscoveryAnthropic, host, key)
	default:
		return nil, apperr.New(apperr.CodeValidation, m.DisplayName+" has no model list; add model ids by hand").
			WithDetails(map[string]any{"provider_id": providerID, "no_discovery": true}).WithMsg("errors.provider.no_discovery", map[string]any{"provider": m.DisplayName})
	}
	if err != nil {
		return nil, discoveryError(err, key)
	}
	var models []domain.Model
	for _, d := range found {
		if m.Has(d.Capability) {
			dm := d.AsModel()
			dm.Endpoint = "" // built-in providers speak their own API
			models = append(models, dm)
		}
	}
	saved, err := s.repo.ListModels(ctx, providerID)
	if err != nil {
		return nil, err
	}
	return s.SetModels(ctx, providerID, domain.MergeDiscovered(saved, models))
}

// UpdateSettings applies a settings patch (empty value = back to default) to a provider.
func (s *Service) UpdateSettings(ctx context.Context, ownerID string, raw map[string]string) (domain.Settings, error) {
	if _, err := s.owner(ctx, ownerID); err != nil {
		return nil, err
	}
	set, clear, err := domain.ParseSettings(raw)
	if err != nil {
		return nil, err
	}
	if err := s.repo.UpdateSettings(ctx, ownerID, set, clear); err != nil {
		return nil, err
	}
	s.changed()
	return s.repo.GetSettings(ctx, ownerID)
}

// Limits returns the effective concurrency / rate limits of a provider.
func (s *Service) Limits(ctx context.Context, ownerID string) (domain.Limits, error) {
	o, err := s.owner(ctx, ownerID)
	if err != nil {
		return domain.Limits{}, err
	}
	set, err := s.repo.GetSettings(ctx, ownerID)
	if err != nil {
		return domain.Limits{}, err
	}
	return domain.EffectiveLimits(set, s.limitDefaults, o.metaConc), nil
}

// Candidate is a model usable for a purpose: its provider is ready and the model is enabled.
type Candidate struct {
	Ref          domain.ModelRef
	ProviderName string
	Custom       bool
	Model        domain.Model
	// IsProviderDefault marks the provider's default model for the capability.
	IsProviderDefault bool
}

// PurposeDefault is the default-model state of one purpose.
type PurposeDefault struct {
	Purpose    domain.Purpose
	Capability domain.Capability
	// Chosen is the user's choice ("" = auto); Effective is what generation uses now ("" = nothing usable).
	Chosen     domain.ModelRef
	Effective  domain.ModelRef
	Candidates []Candidate
	// ChosenUnavailable is set when the chosen model is no longer usable (provider removed, key dead,
	// model disabled); Effective then falls back to auto.
	ChosenUnavailable bool
}

// candidates lists ready models per capability, in order: built-in providers (registry order) then
// custom providers; within a provider the default model first.
func (s *Service) candidates(ctx context.Context) (map[domain.Capability][]Candidate, error) {
	snap, err := s.List(ctx)
	if err != nil {
		return nil, err
	}
	out := map[domain.Capability][]Candidate{}
	add := func(pid, name string, custom bool, models []domain.Model) {
		ordered := slices.Clone(models)
		slices.SortStableFunc(ordered, func(a, b domain.Model) int {
			switch {
			case a.IsDefault && !b.IsDefault:
				return -1
			case b.IsDefault && !a.IsDefault:
				return 1
			}
			return 0
		})
		for _, m := range ordered {
			if m.Enabled {
				out[m.Capability] = append(out[m.Capability], Candidate{Ref: domain.ModelRef{ProviderID: pid, ModelID: m.ModelID},
					ProviderName: name, Custom: custom, Model: m, IsProviderDefault: m.IsDefault})
			}
		}
	}
	for _, b := range snap.Builtins {
		if ready(b) {
			add(b.Meta.ID, b.Meta.DisplayName, false, b.Models)
		}
	}
	for _, c := range snap.Custom {
		if !c.Health.Outcome.Dead() {
			add(c.ID, c.Name, true, c.Models)
		}
	}
	return out, nil
}

// ready: the provider has an active credential whose last test did not show a dead key.
func ready(b BuiltinView) bool {
	for _, c := range b.Credentials {
		if c.Active {
			return !c.Health.Outcome.Dead()
		}
	}
	return false
}

// Defaults returns the default-model state of every purpose.
func (s *Service) Defaults(ctx context.Context) ([]PurposeDefault, error) {
	cands, err := s.candidates(ctx)
	if err != nil {
		return nil, err
	}
	chosen, err := s.repo.GetDefaults(ctx)
	if err != nil {
		return nil, err
	}
	var out []PurposeDefault
	for _, spec := range domain.PurposeSpecs() {
		d := PurposeDefault{Purpose: spec.Purpose, Capability: spec.Capability, Candidates: cands[spec.Capability]}
		if d.Candidates == nil {
			d.Candidates = []Candidate{}
		}
		d.Chosen, _ = domain.ParseModelRef(chosen[spec.Purpose])
		if d.Chosen.ProviderID != "" {
			if slices.ContainsFunc(d.Candidates, func(c Candidate) bool { return c.Ref == d.Chosen }) {
				d.Effective = d.Chosen
			} else {
				d.ChosenUnavailable = true
			}
		}
		if d.Effective.ProviderID == "" && len(d.Candidates) > 0 {
			d.Effective = autoPick(d.Candidates)
		}
		out = append(out, d)
	}
	return out, nil
}

// autoPick: the first provider's default model (candidates are ordered providers-first, default-first).
func autoPick(c []Candidate) domain.ModelRef { return c[0].Ref }

// SetDefault stores the default model of a purpose ("" = auto). The model must be usable now.
func (s *Service) SetDefault(ctx context.Context, p domain.Purpose, ref string) error {
	c, ok := domain.PurposeCapability(p)
	if !ok {
		return apperr.New(apperr.CodeValidation, "Unknown purpose "+string(p)).WithDetails(map[string]any{"field": "purpose"}).WithMsg("errors.provider.unknown_purpose", map[string]any{"purpose": string(p)})
	}
	r, err := domain.ParseModelRef(ref)
	if err != nil {
		return err
	}
	if r.ProviderID != "" {
		cands, err := s.candidates(ctx)
		if err != nil {
			return err
		}
		if !slices.ContainsFunc(cands[c], func(x Candidate) bool { return x.Ref == r }) {
			return apperr.New(apperr.CodeValidation, "That model is not an enabled "+string(c)+" model of a ready provider").
				WithDetails(map[string]any{"field": "model", "capability": string(c)}).WithMsg("errors.provider.model_not_ready", map[string]any{"capability": string(c)})
		}
	}
	if err := s.repo.SetDefault(ctx, p, r.String()); err != nil {
		return err
	}
	s.changed()
	return nil
}

// ResolvedModel is everything a generation feature needs to call a model. Secrets stay in memory only.
type ResolvedModel struct {
	Purpose domain.Purpose
	Ref     domain.ModelRef
	Model   domain.Model
	Limits  domain.Limits
	// Exactly one of Builtin / Custom is set.
	Builtin *Resolved
	Custom  *ResolvedCustom
}

// ResolveModel returns the model to use for a purpose: ref when given ("provider/model"), otherwise the
// user's default for the purpose, otherwise auto. It fails with details.not_configured when nothing fits.
func (s *Service) ResolveModel(ctx context.Context, p domain.Purpose, ref string) (ResolvedModel, error) {
	c, ok := domain.PurposeCapability(p)
	if !ok {
		return ResolvedModel{}, apperr.New(apperr.CodeValidation, "Unknown purpose "+string(p)).WithMsg("errors.provider.unknown_purpose", map[string]any{"purpose": string(p)})
	}
	want, err := domain.ParseModelRef(ref)
	if err != nil {
		return ResolvedModel{}, err
	}
	var pick Candidate
	if want.ProviderID != "" {
		cands, err := s.candidates(ctx)
		if err != nil {
			return ResolvedModel{}, err
		}
		i := slices.IndexFunc(cands[c], func(x Candidate) bool { return x.Ref == want })
		if i < 0 {
			return ResolvedModel{}, apperr.New(apperr.CodeValidation, "Model "+want.String()+" is not usable for "+string(p)).
				WithDetails(map[string]any{"model": want.String(), "not_configured": true}).WithMsg("errors.provider.model_not_usable", map[string]any{"model": want.String(), "purpose": string(p)})
		}
		pick = cands[c][i]
	} else {
		defs, err := s.Defaults(ctx)
		if err != nil {
			return ResolvedModel{}, err
		}
		i := slices.IndexFunc(defs, func(d PurposeDefault) bool { return d.Purpose == p })
		if i < 0 || defs[i].Effective.ProviderID == "" {
			return ResolvedModel{}, apperr.New(apperr.CodeValidation, "No provider is set up for "+string(p)).
				WithDetails(map[string]any{"purpose": string(p), "not_configured": true}).WithMsg("errors.provider.not_configured", map[string]any{"purpose": string(p)})
		}
		e := defs[i].Effective
		pick = defs[i].Candidates[slices.IndexFunc(defs[i].Candidates, func(x Candidate) bool { return x.Ref == e })]
	}
	out := ResolvedModel{Purpose: p, Ref: pick.Ref, Model: pick.Model}
	if out.Limits, err = s.Limits(ctx, pick.Ref.ProviderID); err != nil {
		return ResolvedModel{}, err
	}
	if pick.Custom {
		rc, err := s.ResolveCustom(ctx, pick.Ref.ProviderID)
		if err != nil {
			return ResolvedModel{}, err
		}
		out.Custom = &rc
	} else {
		rb, err := s.Resolve(ctx, pick.Ref.ProviderID)
		if err != nil {
			return ResolvedModel{}, err
		}
		out.Builtin = &rb
	}
	return out, nil
}
