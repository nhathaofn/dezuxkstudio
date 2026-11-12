package contracts

import (
	"time"

	"app/internal/apperr"
	domain "app/internal/domain/provider"
	"app/internal/secrets"
)

// Provider actions and events.
const (
	ActionProvidersList             = "providers:list"
	ActionProvidersCreateCredential = "providers:create_credential"
	ActionProvidersUpdateCredential = "providers:update_credential"
	ActionProvidersDeleteCredential = "providers:delete_credential"
	ActionProvidersActivate         = "providers:activate_credential"
	ActionProvidersTestCredential   = "providers:test_credential"
	ActionProvidersCreateCustom     = "providers:create_custom"
	ActionProvidersUpdateCustom     = "providers:update_custom"
	ActionProvidersDeleteCustom     = "providers:delete_custom"
	ActionProvidersTestCustom       = "providers:test_custom"
	ActionProvidersDiscoverModels   = "providers:discover_models"
	ActionProvidersSetModels        = "providers:set_models"
	ActionProvidersDiscoverBuiltin  = "providers:discover_builtin_models"
	ActionProvidersUpdateSettings   = "providers:update_settings"
	ActionProvidersDefaults         = "providers:defaults"
	ActionProvidersSetDefault       = "providers:set_default"

	EventProvidersChanged = "providers:changed"
)

// ProviderField is a credential input of a built-in provider.
type ProviderField struct {
	Key         string           `json:"key"`
	Kind        domain.FieldKind `json:"kind"`
	Secret      bool             `json:"secret"`
	Required    bool             `json:"required"`
	Placeholder string           `json:"placeholder"`
}

// ProviderHealth is the latest connectivity test result. Tested=false means never tested (or the key
// changed since). Dead marks keys that are unusable (auth failed, invalid, secret lost).
type ProviderHealth struct {
	Tested   bool           `json:"tested"`
	Outcome  domain.Outcome `json:"outcome"`
	Detail   string         `json:"detail"`
	TestedAt time.Time      `json:"tested_at"`
	Dead     bool           `json:"dead"`
}

// NewProviderHealth converts a health record.
func NewProviderHealth(h domain.Health) ProviderHealth {
	return ProviderHealth{Tested: !h.TestedAt.IsZero(), Outcome: h.Outcome, Detail: h.Detail, TestedAt: h.TestedAt, Dead: h.Outcome.Dead()}
}

// ProviderCredential is a saved credential; secrets appear only as masked hints.
type ProviderCredential struct {
	ID          string            `json:"id"`
	ProviderID  string            `json:"provider_id"`
	Name        string            `json:"name"`
	Values      map[string]string `json:"values"`
	SecretHints map[string]string `json:"secret_hints"`
	Active      bool              `json:"active"`
	Health      ProviderHealth    `json:"health"`
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
}

// BuiltinProvider is a registry entry with its credentials.
type BuiltinProvider struct {
	ID               string               `json:"id"`
	DisplayName      string               `json:"display_name"`
	Capabilities     []domain.Capability  `json:"capabilities"`
	Fields           []ProviderField      `json:"fields"`
	DefaultBaseURL   string               `json:"default_base_url"`
	CredentialGroups [][]string           `json:"credential_groups"`
	DocsURL          string               `json:"docs_url"`
	Configured       bool                 `json:"configured"`
	Credentials      []ProviderCredential `json:"credentials"`
	Models           []ProviderModel      `json:"models"`
	// Settings holds only the values the user set; unset keys use SettingDefaults.
	Settings        map[string]int `json:"settings"`
	SettingDefaults map[string]int `json:"setting_defaults"`
	// Discoverable: the provider API lists its models (otherwise models are added by id).
	Discoverable bool `json:"discoverable"`
}

// ModelFacts are the editable capabilities of a model.
type ModelFacts struct {
	Durations       []int    `json:"durations"`
	Resolutions     []string `json:"resolutions"`
	Audio           bool     `json:"audio"`
	MaxOutputTokens int      `json:"max_output_tokens"`
}

// ProviderModel is a model of a built-in or custom provider (endpoint is "" for built-in providers).
type ProviderModel struct {
	ModelID     string            `json:"model_id"`
	DisplayName string            `json:"display_name"`
	Capability  domain.Capability `json:"capability"`
	Endpoint    domain.Endpoint   `json:"endpoint"`
	Enabled     bool              `json:"enabled"`
	IsDefault   bool              `json:"is_default"`
	UserAdded   bool              `json:"user_added"`
	Facts       ModelFacts        `json:"facts"`
}

// NewProviderModel converts a model.
func NewProviderModel(m domain.Model) ProviderModel {
	f := ModelFacts{Durations: m.Facts.Durations, Resolutions: m.Facts.Resolutions, Audio: m.Facts.Audio, MaxOutputTokens: m.Facts.MaxOutputTokens}
	if f.Durations == nil {
		f.Durations = []int{}
	}
	if f.Resolutions == nil {
		f.Resolutions = []string{}
	}
	return ProviderModel{ModelID: m.ModelID, DisplayName: m.DisplayName, Capability: m.Capability, Endpoint: m.Endpoint,
		Enabled: m.Enabled, IsDefault: m.IsDefault, UserAdded: m.UserAdded, Facts: f}
}

// NewProviderModels converts a model list (never nil).
func NewProviderModels(ms []domain.Model) []ProviderModel {
	out := make([]ProviderModel, 0, len(ms))
	for _, m := range ms {
		out = append(out, NewProviderModel(m))
	}
	return out
}

// ToDomain converts a model row.
func (m ProviderModel) ToDomain() domain.Model {
	return domain.Model{ModelID: m.ModelID, DisplayName: m.DisplayName, Capability: m.Capability, Endpoint: m.Endpoint,
		Enabled: m.Enabled, IsDefault: m.IsDefault, UserAdded: m.UserAdded,
		Facts: domain.ModelFacts{Durations: m.Facts.Durations, Resolutions: m.Facts.Resolutions, Audio: m.Facts.Audio, MaxOutputTokens: m.Facts.MaxOutputTokens}}
}

func modelsToDomain(ms []ProviderModel) []domain.Model {
	out := make([]domain.Model, 0, len(ms))
	for _, m := range ms {
		out = append(out, m.ToDomain())
	}
	return out
}

// SettingSpec describes a provider setting for the UI.
type SettingSpec struct {
	Key        string            `json:"key"`
	Capability domain.Capability `json:"capability"`
	Min        int               `json:"min"`
	Max        int               `json:"max"`
}

// CustomProvider is a user-defined provider.
type CustomProvider struct {
	ID              string                 `json:"id"`
	Name            string                 `json:"name"`
	DiscoveryFormat domain.DiscoveryFormat `json:"discovery_format"`
	BaseURL         string                 `json:"base_url"`
	KeyHint         string                 `json:"key_hint"`
	Health          ProviderHealth         `json:"health"`
	Capabilities    []domain.Capability    `json:"capabilities"`
	Models          []ProviderModel        `json:"models"`
	Settings        map[string]int         `json:"settings"`
	SettingDefaults map[string]int         `json:"setting_defaults"`
	CreatedAt       time.Time              `json:"created_at"`
	UpdatedAt       time.Time              `json:"updated_at"`
}

// CustomEndpoint describes an endpoint a custom-provider model can use.
type CustomEndpoint struct {
	Key        domain.Endpoint          `json:"key"`
	Capability domain.Capability        `json:"capability"`
	Method     string                   `json:"method"`
	Path       string                   `json:"path"`
	Formats    []domain.DiscoveryFormat `json:"formats"`
}

// ProvidersListResult answers providers:list and is the providers:changed payload.
type ProvidersListResult struct {
	SecretBackend secrets.Backend   `json:"secret_backend"`
	Builtins      []BuiltinProvider `json:"builtins"`
	Custom        []CustomProvider  `json:"custom"`
	Endpoints     []CustomEndpoint  `json:"endpoints"`
	SettingSpecs  []SettingSpec     `json:"setting_specs"`
}

// BuiltinWithCredentials is the input of NewProvidersListResult.
type BuiltinWithCredentials struct {
	Meta        domain.Meta
	Configured  bool
	Credentials []domain.Credential
	Models      []domain.Model
	Settings    domain.Settings
}

// settingDefaults lists the effective value of every setting when nothing is stored.
func settingDefaults(def domain.LimitDefaults, meta map[domain.Capability]int) map[string]int {
	l := domain.EffectiveLimits(nil, def, meta)
	return map[string]int{
		domain.SettingImageMaxWorkers: l.MaxWorkers[domain.CapabilityImage],
		domain.SettingVideoMaxWorkers: l.MaxWorkers[domain.CapabilityVideo],
		domain.SettingTTSMaxWorkers:   l.MaxWorkers[domain.CapabilityTTS],
		domain.SettingImageRPM:        l.RPM[domain.CapabilityImage],
		domain.SettingVideoRPM:        l.RPM[domain.CapabilityVideo],
		domain.SettingRequestGapMs:    l.RequestGapMs,
	}
}

func settingsMap(s domain.Settings) map[string]int {
	out := map[string]int{}
	for k, v := range s {
		out[k] = v
	}
	return out
}

// NewProvidersListResult builds the list result.
func NewProvidersListResult(backend secrets.Backend, builtins []BuiltinWithCredentials, custom []domain.CustomProvider, def domain.LimitDefaults) ProvidersListResult {
	out := ProvidersListResult{SecretBackend: backend, Builtins: []BuiltinProvider{}, Custom: []CustomProvider{}, Endpoints: []CustomEndpoint{}, SettingSpecs: []SettingSpec{}}
	for _, sp := range domain.SettingSpecs() {
		out.SettingSpecs = append(out.SettingSpecs, SettingSpec{Key: sp.Key, Capability: sp.Capability, Min: sp.Min, Max: sp.Max})
	}
	for _, b := range builtins {
		m := b.Meta
		bp := BuiltinProvider{ID: m.ID, DisplayName: m.DisplayName, Capabilities: m.Capabilities, DefaultBaseURL: m.DefaultBaseURL,
			CredentialGroups: m.CredentialGroups, DocsURL: m.DocsURL, Configured: b.Configured,
			Fields: []ProviderField{}, Credentials: []ProviderCredential{}, Models: NewProviderModels(b.Models),
			Settings: settingsMap(b.Settings), SettingDefaults: settingDefaults(def, m.DefaultConcurrency),
			Discoverable: m.Probe.Kind == domain.ProbeOpenAIModels || m.Probe.Kind == domain.ProbeGeminiModels || m.Probe.Kind == domain.ProbeAnthropicModels}
		if bp.CredentialGroups == nil {
			bp.CredentialGroups = [][]string{}
		}
		for _, f := range m.Fields {
			bp.Fields = append(bp.Fields, ProviderField{Key: f.Key, Kind: f.Kind, Secret: f.Secret, Required: f.Required, Placeholder: f.Placeholder})
		}
		for _, c := range b.Credentials {
			bp.Credentials = append(bp.Credentials, NewProviderCredential(c))
		}
		out.Builtins = append(out.Builtins, bp)
	}
	for _, c := range custom {
		cp := NewCustomProvider(c)
		cp.SettingDefaults = settingDefaults(def, nil)
		out.Custom = append(out.Custom, cp)
	}
	for _, e := range domain.Endpoints() {
		out.Endpoints = append(out.Endpoints, CustomEndpoint{Key: e.Key, Capability: e.Capability, Method: e.Method, Path: e.Path, Formats: e.Formats})
	}
	return out
}

func nonNilMap(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}

// NewProviderCredential converts a credential.
func NewProviderCredential(c domain.Credential) ProviderCredential {
	return ProviderCredential{ID: c.ID, ProviderID: c.ProviderID, Name: c.Name, Values: nonNilMap(c.Values),
		SecretHints: nonNilMap(c.SecretHints), Active: c.Active, Health: NewProviderHealth(c.Health), CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt}
}

// NewCustomProvider converts a custom provider.
func NewCustomProvider(p domain.CustomProvider) CustomProvider {
	out := CustomProvider{ID: p.ID, Name: p.Name, DiscoveryFormat: p.DiscoveryFormat, BaseURL: p.BaseURL, KeyHint: p.KeyHint, Health: NewProviderHealth(p.Health),
		Capabilities: p.Capabilities(), Models: NewProviderModels(p.Models), Settings: settingsMap(p.Settings),
		SettingDefaults: map[string]int{}, CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt}
	if out.Capabilities == nil {
		out.Capabilities = []domain.Capability{}
	}
	return out
}

// CredentialInput is the editable part of a credential. Empty secrets keep the stored value on update.
type CredentialInput struct {
	Name         string            `json:"name"`
	Values       map[string]string `json:"values"`
	Secrets      map[string]string `json:"secrets"`
	ClearSecrets []string          `json:"clear_secrets"`
}

// ToDomain converts the input.
func (in CredentialInput) ToDomain() domain.Input {
	return domain.Input{Name: in.Name, Values: in.Values, Secrets: in.Secrets, ClearSecrets: in.ClearSecrets}
}

// CreateCredentialPayload creates a credential for a built-in provider.
type CreateCredentialPayload struct {
	ProviderID string          `json:"provider_id"`
	Credential CredentialInput `json:"credential"`
}

// Validate checks the provider id.
func (p CreateCredentialPayload) Validate() error {
	if p.ProviderID == "" {
		return apperr.New(apperr.CodeBadRequest, "provider_id is required").WithMsg("errors.provider.provider_id_required", nil)
	}
	return nil
}

// UpdateCredentialPayload updates a credential.
type UpdateCredentialPayload struct {
	ID         string          `json:"id"`
	Credential CredentialInput `json:"credential"`
}

// Validate checks the id.
func (p UpdateCredentialPayload) Validate() error { return requireID(p.ID) }

// ProviderIDPayload targets a credential or custom provider by id.
type ProviderIDPayload struct {
	ID string `json:"id"`
}

// Validate checks the id.
func (p ProviderIDPayload) Validate() error { return requireID(p.ID) }

func requireID(id string) error {
	if id == "" {
		return apperr.New(apperr.CodeBadRequest, "id is required").WithMsg("errors.provider.id_required", nil)
	}
	return nil
}

// CustomProviderInput is the custom-provider form. An empty api_key on update keeps the stored key.
type CustomProviderInput struct {
	Name            string                 `json:"name"`
	DiscoveryFormat domain.DiscoveryFormat `json:"discovery_format"`
	BaseURL         string                 `json:"base_url"`
	APIKey          string                 `json:"api_key"`
	ClearAPIKey     bool                   `json:"clear_api_key"`
	Models          []ProviderModel        `json:"models"`
}

// ToDomain converts the input.
func (in CustomProviderInput) ToDomain() domain.CustomInput {
	out := domain.CustomInput{Name: in.Name, DiscoveryFormat: in.DiscoveryFormat, BaseURL: in.BaseURL, APIKey: in.APIKey, ClearAPIKey: in.ClearAPIKey}
	out.Models = modelsToDomain(in.Models)
	return out
}

// UpdateCustomPayload updates a custom provider.
type UpdateCustomPayload struct {
	ID       string              `json:"id"`
	Provider CustomProviderInput `json:"provider"`
}

// Validate checks the id.
func (p UpdateCustomPayload) Validate() error { return requireID(p.ID) }

// ProviderTestResult answers a connectivity test. A failed test is a result, not an error.
type ProviderTestResult struct {
	OK        bool           `json:"ok"`
	Outcome   domain.Outcome `json:"outcome"`
	Status    int            `json:"status"`
	LatencyMs int64          `json:"latency_ms"`
	Detail    string         `json:"detail"`
	Models    []string       `json:"models"`
}

// DiscoverModelsPayload lists models of a (possibly unsaved) custom provider.
type DiscoverModelsPayload struct {
	CustomID        string                 `json:"custom_id"`
	DiscoveryFormat domain.DiscoveryFormat `json:"discovery_format"`
	BaseURL         string                 `json:"base_url"`
	APIKey          string                 `json:"api_key"`
}

// DiscoveredModel is one discovered model.
type DiscoveredModel struct {
	ModelID    string            `json:"model_id"`
	Endpoint   domain.Endpoint   `json:"endpoint"`
	Capability domain.Capability `json:"capability"`
}

// DiscoverModelsResult answers providers:discover_models.
type DiscoverModelsResult struct {
	Models []DiscoveredModel `json:"models"`
}

// SetModelsPayload replaces the model list of a provider (built-in id or custom id).
type SetModelsPayload struct {
	OwnerID string          `json:"owner_id"`
	Models  []ProviderModel `json:"models"`
}

// Validate checks the owner.
func (p SetModelsPayload) Validate() error { return requireID(p.OwnerID) }

// ToDomain converts the models.
func (p SetModelsPayload) ToDomain() []domain.Model { return modelsToDomain(p.Models) }

// ProviderModelsResult answers model-list actions.
type ProviderModelsResult struct {
	Models []ProviderModel `json:"models"`
}

// UpdateSettingsPayload patches provider settings; an empty value resets a key to its default.
type UpdateSettingsPayload struct {
	OwnerID  string            `json:"owner_id"`
	Settings map[string]string `json:"settings"`
}

// Validate checks the owner.
func (p UpdateSettingsPayload) Validate() error { return requireID(p.OwnerID) }

// ProviderSettingsResult answers providers:update_settings.
type ProviderSettingsResult struct {
	Settings map[string]int `json:"settings"`
}

// NewProviderSettingsResult converts settings.
func NewProviderSettingsResult(s domain.Settings) ProviderSettingsResult {
	return ProviderSettingsResult{Settings: settingsMap(s)}
}

// ModelCandidate is a model usable for a purpose.
type ModelCandidate struct {
	Ref               string        `json:"ref"`
	ProviderID        string        `json:"provider_id"`
	ProviderName      string        `json:"provider_name"`
	Custom            bool          `json:"custom"`
	Model             ProviderModel `json:"model"`
	IsProviderDefault bool          `json:"is_provider_default"`
}

// PurposeDefault is the default-model state of a purpose.
type PurposeDefault struct {
	Purpose           domain.Purpose    `json:"purpose"`
	Capability        domain.Capability `json:"capability"`
	Chosen            string            `json:"chosen"`
	Effective         string            `json:"effective"`
	ChosenUnavailable bool              `json:"chosen_unavailable"`
	Candidates        []ModelCandidate  `json:"candidates"`
}

// ProviderDefaultsResult answers providers:defaults.
type ProviderDefaultsResult struct {
	Purposes []PurposeDefault `json:"purposes"`
}

// SetDefaultPayload sets the default model of a purpose ("" = auto).
type SetDefaultPayload struct {
	Purpose domain.Purpose `json:"purpose"`
	Model   string         `json:"model"`
}

// Validate checks the purpose is set.
func (p SetDefaultPayload) Validate() error {
	if p.Purpose == "" {
		return apperr.New(apperr.CodeBadRequest, "purpose is required").WithMsg("errors.provider.purpose_required", nil)
	}
	return nil
}
