// Package provider is the domain model for API providers (LLM, image, video, TTS) and the
// credentials the user configures for them.
//
// Built-in providers are declared in code (Registry). Each can hold several credentials, exactly one
// of which is active (picked by the user, never rotated automatically). Custom providers are endpoints
// the user defines (OpenAI-compatible and similar protocols).
//
// Secret field values never live in this model's persisted form: the repository stores metadata only
// and secrets go to the secret store (internal/secrets).
package provider

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"app/internal/apperr"
)

// Capability is a kind of generation a provider offers.
type Capability string

const (
	CapabilityLLM   Capability = "llm"
	CapabilityImage Capability = "image"
	CapabilityVideo Capability = "video"
	CapabilityTTS   Capability = "tts"
)

// Capabilities lists every capability in display order.
func Capabilities() []Capability {
	return []Capability{CapabilityLLM, CapabilityImage, CapabilityVideo, CapabilityTTS}
}

// FieldKind tells the UI how to render a credential field.
type FieldKind string

const (
	FieldText FieldKind = "text"
	FieldURL  FieldKind = "url"
	// FieldFile holds the contents of a file the user picks (e.g. a service-account JSON).
	FieldFile FieldKind = "file"
)

// FieldKinds lists valid field kinds.
func FieldKinds() []FieldKind { return []FieldKind{FieldText, FieldURL, FieldFile} }

// Well-known field keys shared by many providers.
const (
	FieldAPIKey  = "api_key"
	FieldBaseURL = "base_url"
)

// Field declares one credential input of a built-in provider.
type Field struct {
	Key      string
	Kind     FieldKind
	Secret   bool
	Required bool
	// Placeholder is a non-translated hint (e.g. a default URL).
	Placeholder string
}

// Meta is the code-level description of a built-in provider.
type Meta struct {
	ID           string
	DisplayName  string
	Capabilities []Capability
	Fields       []Field
	// DefaultBaseURL is used when the credential leaves base_url empty.
	DefaultBaseURL string
	// CredentialGroups: when non-empty, a credential is complete if ANY group has all its fields
	// filled (e.g. Kling: api_key OR access_key+secret_key). Otherwise every required field is needed.
	CredentialGroups [][]string
	// DocsURL points the user to where the key is created.
	DocsURL string
	// Probe declares how the connectivity test talks to this provider (ArcReel's per-provider check).
	Probe Probe
	// DefaultConcurrency overrides the app-wide max-workers default for some capabilities (a provider
	// known to accept fewer parallel jobs); user settings still win.
	DefaultConcurrency map[Capability]int
	// Hosts are extra API hosts (regional endpoints) that identify this provider from a base URL; the
	// host of DefaultBaseURL is always included.
	Hosts []string
}

// ProbeKind selects a connectivity-test protocol.
type ProbeKind string

const (
	ProbeOpenAIModels    ProbeKind = "openai_models"    // GET {base}/models, Bearer
	ProbeAnthropicModels ProbeKind = "anthropic_models" // GET {base}/v1/models, x-api-key (Bearer retry)
	ProbeGeminiModels    ProbeKind = "gemini_models"    // GET {base}/v1beta/models, x-goog-api-key
	ProbeVertexToken     ProbeKind = "vertex_token"     // service-account JWT -> OAuth token
	ProbeArkTasks        ProbeKind = "ark_tasks"        // GET {base}/contents/generations/tasks?page_size=1, Bearer
	ProbeKlingCosts      ProbeKind = "kling_costs"      // GET {root}/account/costs, Bearer api_key or HS256 JWT
	ProbeViduTasks       ProbeKind = "vidu_tasks"       // GET {base}/tasks/0/creations, Token; 404 = valid
)

// ProbeKinds lists every probe kind.
func ProbeKinds() []ProbeKind {
	return []ProbeKind{ProbeOpenAIModels, ProbeAnthropicModels, ProbeGeminiModels, ProbeVertexToken, ProbeArkTasks, ProbeKlingCosts, ProbeViduTasks}
}

// Probe is the connectivity-test declaration of a provider.
type Probe struct {
	Kind ProbeKind
	// StripSuffixes: the first matching suffix is removed from the base URL to get the host.
	StripSuffixes []string
	// Path is appended to the host before the protocol path (e.g. "/compatible-mode/v1").
	Path string
	// ModelFilter (regexp, lower-case ids) keeps the models shown after a successful test; "" = all.
	ModelFilter string
}

// Field returns the field with key.
func (m Meta) Field(key string) (Field, bool) {
	for _, f := range m.Fields {
		if f.Key == key {
			return f, true
		}
	}
	return Field{}, false
}

// Has reports whether the provider offers capability c.
func (m Meta) Has(c Capability) bool { return slices.Contains(m.Capabilities, c) }

// SecretKeys lists the keys of secret fields.
func (m Meta) SecretKeys() []string {
	var out []string
	for _, f := range m.Fields {
		if f.Secret {
			out = append(out, f.Key)
		}
	}
	return out
}

// Validate checks a registry entry; registry errors are programming errors and stop start-up.
func (m Meta) Validate() error {
	if m.ID == "" || m.DisplayName == "" {
		return fmt.Errorf("provider: entry without id or display name")
	}
	if len(m.Capabilities) == 0 {
		return fmt.Errorf("provider %s: no capabilities", m.ID)
	}
	if !slices.Contains(ProbeKinds(), m.Probe.Kind) {
		return fmt.Errorf("provider %s: unknown probe kind %q", m.ID, m.Probe.Kind)
	}
	if m.Probe.ModelFilter != "" {
		if _, err := regexp.Compile(m.Probe.ModelFilter); err != nil {
			return fmt.Errorf("provider %s: bad model filter: %w", m.ID, err)
		}
	}
	for c, n := range m.DefaultConcurrency {
		if !m.Has(c) || c == CapabilityLLM || n < 1 {
			return fmt.Errorf("provider %s: default concurrency %s=%d is invalid", m.ID, c, n)
		}
	}
	for _, c := range m.Capabilities {
		if !slices.Contains(Capabilities(), c) {
			return fmt.Errorf("provider %s: unknown capability %q", m.ID, c)
		}
	}
	seen := map[string]bool{}
	for _, f := range m.Fields {
		if f.Key == "" || seen[f.Key] {
			return fmt.Errorf("provider %s: empty or duplicate field %q", m.ID, f.Key)
		}
		seen[f.Key] = true
		if !slices.Contains(FieldKinds(), f.Kind) {
			return fmt.Errorf("provider %s: field %s has unknown kind %q", m.ID, f.Key, f.Kind)
		}
	}
	if len(m.CredentialGroups) > 0 {
		allowed := map[string]bool{}
		for _, f := range m.Fields {
			if f.Secret && f.Required {
				allowed[f.Key] = true
			}
		}
		covered := map[string]bool{}
		for _, g := range m.CredentialGroups {
			if len(g) == 0 {
				return fmt.Errorf("provider %s: empty credential group", m.ID)
			}
			for _, k := range g {
				if !allowed[k] {
					return fmt.Errorf("provider %s: credential group key %q is not a required secret field", m.ID, k)
				}
				covered[k] = true
			}
		}
		if len(covered) != len(allowed) {
			return fmt.Errorf("provider %s: credential groups do not cover every required secret field", m.ID)
		}
	}
	return nil
}

// Credential is one saved set of credentials for a built-in provider.
type Credential struct {
	ID         string
	ProviderID string
	Name       string
	// Values holds the non-secret field values (base_url, project, ...).
	Values map[string]string
	// SecretHints maps each stored secret field to a masked preview ("sk-…abcd").
	SecretHints map[string]string
	Active      bool
	Health      Health
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Input is a create/update request. Secrets maps a secret field to its new value; for updates a
// missing or empty value keeps the stored secret, and ClearSecrets removes one.
type Input struct {
	Name         string
	Values       map[string]string
	Secrets      map[string]string
	ClearSecrets []string
}

// Normalize trims every value and drops empty non-secret values.
func (in Input) Normalize() Input {
	out := Input{Name: strings.TrimSpace(in.Name), Values: map[string]string{}, Secrets: map[string]string{}}
	for k, v := range in.Values {
		if v = strings.TrimSpace(v); v != "" {
			out.Values[k] = v
		}
	}
	for k, v := range in.Secrets {
		if v = strings.TrimSpace(v); v != "" {
			out.Secrets[k] = v
		}
	}
	out.ClearSecrets = in.ClearSecrets
	return out
}

// CheckComplete verifies that values+stored secrets satisfy the provider's required fields.
// present reports whether a secret field has a value (new or stored).
func (m Meta) CheckComplete(values map[string]string, present func(key string) bool) error {
	has := func(f Field) bool {
		if f.Secret {
			return present(f.Key)
		}
		return values[f.Key] != ""
	}
	for k := range values {
		f, ok := m.Field(k)
		if !ok || f.Secret {
			return apperr.New(apperr.CodeValidation, "Unknown field "+k).
				WithDetails(map[string]any{"field": k}).WithMsg("errors.provider.unknown_field", map[string]any{"field": k})
		}
	}
	if len(m.CredentialGroups) > 0 {
		for _, f := range m.Fields {
			if f.Required && !f.Secret && !has(f) {
				return missing(f.Key)
			}
		}
		for _, g := range m.CredentialGroups {
			ok := true
			for _, k := range g {
				ok = ok && present(k)
			}
			if ok {
				return nil
			}
		}
		return apperr.New(apperr.CodeValidation, "Fill in one complete group of credentials").
			WithDetails(map[string]any{"groups": m.CredentialGroups}).WithMsg("errors.provider.complete_group_required", nil)
	}
	for _, f := range m.Fields {
		if f.Required && !has(f) {
			return missing(f.Key)
		}
	}
	return nil
}

func missing(key string) error {
	return apperr.New(apperr.CodeValidation, "Missing required field "+key).
		WithDetails(map[string]any{"field": key}).WithMsg("errors.provider.missing_field", map[string]any{"field": key})
}

// MaskSecret shows the first 3 and last 4 characters of long secrets, "••••" otherwise.
func MaskSecret(v string) string {
	if len(v) <= 10 {
		return "••••"
	}
	return v[:3] + "…" + v[len(v)-4:]
}

// Repository persists credential metadata. Implementations keep at most one active credential per
// provider and, when the active one is deleted, promote the oldest remaining credential.
type Repository interface {
	ListCredentials(ctx context.Context) ([]Credential, error)
	GetCredential(ctx context.Context, id string) (Credential, error)
	// CreateCredential stores c; it becomes active when the provider has no active credential.
	CreateCredential(ctx context.Context, c Credential) (Credential, error)
	UpdateCredential(ctx context.Context, c Credential) error
	DeleteCredential(ctx context.Context, id string) error
	ActivateCredential(ctx context.Context, id string) error
	SetCredentialHealth(ctx context.Context, id string, h Health) error
	SetCustomHealth(ctx context.Context, id string, h Health) error

	// Models and settings are keyed by owner id: a built-in provider id or a custom provider id.
	ListModels(ctx context.Context, ownerID string) ([]Model, error)
	ReplaceModels(ctx context.Context, ownerID string, models []Model) error
	GetSettings(ctx context.Context, ownerID string) (Settings, error)
	UpdateSettings(ctx context.Context, ownerID string, set Settings, clear []string) error
	// Defaults maps purposes to the user's default model ref ("" or missing = auto).
	GetDefaults(ctx context.Context) (map[Purpose]string, error)
	SetDefault(ctx context.Context, p Purpose, ref string) error

	ListCustom(ctx context.Context) ([]CustomProvider, error)
	GetCustom(ctx context.Context, id string) (CustomProvider, error)
	CreateCustom(ctx context.Context, p CustomProvider) (CustomProvider, error)
	UpdateCustom(ctx context.Context, p CustomProvider) error
	DeleteCustom(ctx context.Context, id string) error
}

// ErrNotFound builds the not-found error for a credential or custom provider id.
func ErrNotFound(id string) error {
	return apperr.New(apperr.CodeNotFound, "Provider credential not found").WithDetails(map[string]any{"id": id}).WithMsg("errors.provider.credential_not_found", nil)
}
