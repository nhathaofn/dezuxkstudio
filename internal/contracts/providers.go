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

// ProviderCredential is a saved credential; secrets appear only as masked hints.
type ProviderCredential struct {
	ID          string            `json:"id"`
	ProviderID  string            `json:"provider_id"`
	Name        string            `json:"name"`
	Values      map[string]string `json:"values"`
	SecretHints map[string]string `json:"secret_hints"`
	Active      bool              `json:"active"`
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
}

// CustomProviderModel is a model of a custom provider.
type CustomProviderModel struct {
	ModelID    string            `json:"model_id"`
	Endpoint   domain.Endpoint   `json:"endpoint"`
	Enabled    bool              `json:"enabled"`
	IsDefault  bool              `json:"is_default"`
	Capability domain.Capability `json:"capability"`
}

// CustomProvider is a user-defined provider.
type CustomProvider struct {
	ID              string                 `json:"id"`
	Name            string                 `json:"name"`
	DiscoveryFormat domain.DiscoveryFormat `json:"discovery_format"`
	BaseURL         string                 `json:"base_url"`
	KeyHint         string                 `json:"key_hint"`
	Capabilities    []domain.Capability    `json:"capabilities"`
	Models          []CustomProviderModel  `json:"models"`
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
}

// BuiltinWithCredentials is the input of NewProvidersListResult.
type BuiltinWithCredentials struct {
	Meta        domain.Meta
	Configured  bool
	Credentials []domain.Credential
}

// NewProvidersListResult builds the list result.
func NewProvidersListResult(backend secrets.Backend, builtins []BuiltinWithCredentials, custom []domain.CustomProvider) ProvidersListResult {
	out := ProvidersListResult{SecretBackend: backend, Builtins: []BuiltinProvider{}, Custom: []CustomProvider{}, Endpoints: []CustomEndpoint{}}
	for _, b := range builtins {
		m := b.Meta
		bp := BuiltinProvider{ID: m.ID, DisplayName: m.DisplayName, Capabilities: m.Capabilities, DefaultBaseURL: m.DefaultBaseURL,
			CredentialGroups: m.CredentialGroups, DocsURL: m.DocsURL, Configured: b.Configured,
			Fields: []ProviderField{}, Credentials: []ProviderCredential{}}
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
		out.Custom = append(out.Custom, NewCustomProvider(c))
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
		SecretHints: nonNilMap(c.SecretHints), Active: c.Active, CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt}
}

// NewCustomProvider converts a custom provider.
func NewCustomProvider(p domain.CustomProvider) CustomProvider {
	out := CustomProvider{ID: p.ID, Name: p.Name, DiscoveryFormat: p.DiscoveryFormat, BaseURL: p.BaseURL, KeyHint: p.KeyHint,
		Capabilities: p.Capabilities(), Models: []CustomProviderModel{}, CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt}
	if out.Capabilities == nil {
		out.Capabilities = []domain.Capability{}
	}
	for _, m := range p.Models {
		out.Models = append(out.Models, CustomProviderModel{ModelID: m.ModelID, Endpoint: m.Endpoint, Enabled: m.Enabled,
			IsDefault: m.IsDefault, Capability: m.Capability()})
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
		return apperr.New(apperr.CodeBadRequest, "provider_id is required")
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
		return apperr.New(apperr.CodeBadRequest, "id is required")
	}
	return nil
}

// CustomModelInput is a model row in a custom-provider form.
type CustomModelInput struct {
	ModelID   string          `json:"model_id"`
	Endpoint  domain.Endpoint `json:"endpoint"`
	Enabled   bool            `json:"enabled"`
	IsDefault bool            `json:"is_default"`
}

// CustomProviderInput is the custom-provider form. An empty api_key on update keeps the stored key.
type CustomProviderInput struct {
	Name            string                 `json:"name"`
	DiscoveryFormat domain.DiscoveryFormat `json:"discovery_format"`
	BaseURL         string                 `json:"base_url"`
	APIKey          string                 `json:"api_key"`
	ClearAPIKey     bool                   `json:"clear_api_key"`
	Models          []CustomModelInput     `json:"models"`
}

// ToDomain converts the input.
func (in CustomProviderInput) ToDomain() domain.CustomInput {
	out := domain.CustomInput{Name: in.Name, DiscoveryFormat: in.DiscoveryFormat, BaseURL: in.BaseURL, APIKey: in.APIKey, ClearAPIKey: in.ClearAPIKey}
	for _, m := range in.Models {
		out.Models = append(out.Models, domain.CustomModel{ModelID: m.ModelID, Endpoint: m.Endpoint, Enabled: m.Enabled, IsDefault: m.IsDefault})
	}
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
