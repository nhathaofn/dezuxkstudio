package modules

import (
	"context"

	"app/internal/contracts"
	"app/internal/realtime"
	providersvc "app/internal/service/provider"
)

// Providers exposes providers:* actions.
type Providers struct {
	Service *providersvc.Service
}

func (Providers) Name() string { return "providers" }

// ListResult converts a service snapshot (also used by the core for providers:changed).
func ListResult(s providersvc.Snapshot) contracts.ProvidersListResult {
	builtins := make([]contracts.BuiltinWithCredentials, 0, len(s.Builtins))
	for _, b := range s.Builtins {
		builtins = append(builtins, contracts.BuiltinWithCredentials{Meta: b.Meta, Configured: b.Configured(), Credentials: b.Credentials,
			Models: b.Models, Settings: b.Settings})
	}
	return contracts.NewProvidersListResult(s.SecretBackend, builtins, s.Custom, s.LimitDefaults)
}

func testResult(r providersvc.TestResult) contracts.ProviderTestResult {
	models := r.Models
	if models == nil {
		models = []string{}
	}
	return contracts.ProviderTestResult{OK: r.OK(), Outcome: r.Outcome, Status: r.Status, LatencyMs: r.LatencyMs, Detail: r.Detail, Models: models}
}

func defaultsResult(ds []providersvc.PurposeDefault) contracts.ProviderDefaultsResult {
	out := contracts.ProviderDefaultsResult{Purposes: []contracts.PurposeDefault{}}
	for _, d := range ds {
		pd := contracts.PurposeDefault{Purpose: d.Purpose, Capability: d.Capability, Chosen: d.Chosen.String(),
			Effective: d.Effective.String(), ChosenUnavailable: d.ChosenUnavailable, Candidates: []contracts.ModelCandidate{}}
		for _, c := range d.Candidates {
			pd.Candidates = append(pd.Candidates, contracts.ModelCandidate{Ref: c.Ref.String(), ProviderID: c.Ref.ProviderID,
				ProviderName: c.ProviderName, Custom: c.Custom, Model: contracts.NewProviderModel(c.Model), IsProviderDefault: c.IsProviderDefault})
		}
		out.Purposes = append(out.Purposes, pd)
	}
	return out
}

func (m Providers) Register(r *realtime.Registry) {
	realtime.Handle(r, contracts.ActionProvidersSetModels,
		func(ctx context.Context, _ *realtime.Request, p contracts.SetModelsPayload) (contracts.ProviderModelsResult, error) {
			ms, err := m.Service.SetModels(ctx, p.OwnerID, p.ToDomain())
			if err != nil {
				return contracts.ProviderModelsResult{}, err
			}
			return contracts.ProviderModelsResult{Models: contracts.NewProviderModels(ms)}, nil
		})
	realtime.Handle(r, contracts.ActionProvidersDiscoverBuiltin,
		func(ctx context.Context, _ *realtime.Request, p contracts.ProviderIDPayload) (contracts.ProviderModelsResult, error) {
			ms, err := m.Service.DiscoverBuiltinModels(ctx, p.ID)
			if err != nil {
				return contracts.ProviderModelsResult{}, err
			}
			return contracts.ProviderModelsResult{Models: contracts.NewProviderModels(ms)}, nil
		})
	realtime.Handle(r, contracts.ActionProvidersUpdateSettings,
		func(ctx context.Context, _ *realtime.Request, p contracts.UpdateSettingsPayload) (contracts.ProviderSettingsResult, error) {
			s, err := m.Service.UpdateSettings(ctx, p.OwnerID, p.Settings)
			if err != nil {
				return contracts.ProviderSettingsResult{}, err
			}
			return contracts.NewProviderSettingsResult(s), nil
		})
	realtime.Handle(r, contracts.ActionProvidersDefaults,
		func(ctx context.Context, _ *realtime.Request, _ contracts.Empty) (contracts.ProviderDefaultsResult, error) {
			ds, err := m.Service.Defaults(ctx)
			if err != nil {
				return contracts.ProviderDefaultsResult{}, err
			}
			return defaultsResult(ds), nil
		})
	realtime.Handle(r, contracts.ActionProvidersSetDefault,
		func(ctx context.Context, _ *realtime.Request, p contracts.SetDefaultPayload) (contracts.ProviderDefaultsResult, error) {
			if err := m.Service.SetDefault(ctx, p.Purpose, p.Model); err != nil {
				return contracts.ProviderDefaultsResult{}, err
			}
			ds, err := m.Service.Defaults(ctx)
			if err != nil {
				return contracts.ProviderDefaultsResult{}, err
			}
			return defaultsResult(ds), nil
		})
	realtime.Handle(r, contracts.ActionProvidersList,
		func(ctx context.Context, _ *realtime.Request, _ contracts.Empty) (contracts.ProvidersListResult, error) {
			s, err := m.Service.List(ctx)
			if err != nil {
				return contracts.ProvidersListResult{}, err
			}
			return ListResult(s), nil
		})
	realtime.Handle(r, contracts.ActionProvidersCreateCredential,
		func(ctx context.Context, _ *realtime.Request, p contracts.CreateCredentialPayload) (contracts.ProviderCredential, error) {
			c, err := m.Service.CreateCredential(ctx, p.ProviderID, p.Credential.ToDomain())
			if err != nil {
				return contracts.ProviderCredential{}, err
			}
			return contracts.NewProviderCredential(c), nil
		})
	realtime.Handle(r, contracts.ActionProvidersUpdateCredential,
		func(ctx context.Context, _ *realtime.Request, p contracts.UpdateCredentialPayload) (contracts.ProviderCredential, error) {
			c, err := m.Service.UpdateCredential(ctx, p.ID, p.Credential.ToDomain())
			if err != nil {
				return contracts.ProviderCredential{}, err
			}
			return contracts.NewProviderCredential(c), nil
		})
	realtime.Handle(r, contracts.ActionProvidersDeleteCredential,
		func(ctx context.Context, _ *realtime.Request, p contracts.ProviderIDPayload) (contracts.Empty, error) {
			return contracts.Empty{}, m.Service.DeleteCredential(ctx, p.ID)
		})
	realtime.Handle(r, contracts.ActionProvidersActivate,
		func(ctx context.Context, _ *realtime.Request, p contracts.ProviderIDPayload) (contracts.Empty, error) {
			return contracts.Empty{}, m.Service.ActivateCredential(ctx, p.ID)
		})
	realtime.Handle(r, contracts.ActionProvidersTestCredential,
		func(ctx context.Context, _ *realtime.Request, p contracts.ProviderIDPayload) (contracts.ProviderTestResult, error) {
			res, err := m.Service.TestCredential(ctx, p.ID)
			if err != nil {
				return contracts.ProviderTestResult{}, err
			}
			return testResult(res), nil
		})
	realtime.Handle(r, contracts.ActionProvidersCreateCustom,
		func(ctx context.Context, _ *realtime.Request, p contracts.CustomProviderInput) (contracts.CustomProvider, error) {
			c, err := m.Service.CreateCustom(ctx, p.ToDomain())
			if err != nil {
				return contracts.CustomProvider{}, err
			}
			return contracts.NewCustomProvider(c), nil
		})
	realtime.Handle(r, contracts.ActionProvidersUpdateCustom,
		func(ctx context.Context, _ *realtime.Request, p contracts.UpdateCustomPayload) (contracts.CustomProvider, error) {
			c, err := m.Service.UpdateCustom(ctx, p.ID, p.Provider.ToDomain())
			if err != nil {
				return contracts.CustomProvider{}, err
			}
			return contracts.NewCustomProvider(c), nil
		})
	realtime.Handle(r, contracts.ActionProvidersDeleteCustom,
		func(ctx context.Context, _ *realtime.Request, p contracts.ProviderIDPayload) (contracts.Empty, error) {
			return contracts.Empty{}, m.Service.DeleteCustom(ctx, p.ID)
		})
	realtime.Handle(r, contracts.ActionProvidersTestCustom,
		func(ctx context.Context, _ *realtime.Request, p contracts.ProviderIDPayload) (contracts.ProviderTestResult, error) {
			res, err := m.Service.TestCustom(ctx, p.ID)
			if err != nil {
				return contracts.ProviderTestResult{}, err
			}
			return testResult(res), nil
		})
	realtime.Handle(r, contracts.ActionProvidersDiscoverModels,
		func(ctx context.Context, _ *realtime.Request, p contracts.DiscoverModelsPayload) (contracts.DiscoverModelsResult, error) {
			ms, err := m.Service.DiscoverModels(ctx, providersvc.DiscoverRequest{
				CustomID: p.CustomID, DiscoveryFormat: p.DiscoveryFormat, BaseURL: p.BaseURL, APIKey: p.APIKey})
			if err != nil {
				return contracts.DiscoverModelsResult{}, err
			}
			out := contracts.DiscoverModelsResult{Models: []contracts.DiscoveredModel{}}
			for _, d := range ms {
				out.Models = append(out.Models, contracts.DiscoveredModel{ModelID: d.ModelID, Endpoint: d.Endpoint, Capability: d.Capability})
			}
			return out, nil
		})
}
