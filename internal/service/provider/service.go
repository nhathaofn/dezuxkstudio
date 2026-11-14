// Package provider manages API provider credentials (built-in registry providers and user-defined
// custom providers): CRUD, the single active credential per provider, connectivity tests, model
// discovery and Resolve, which generation features use to obtain ready-to-use credentials.
//
// Secret values are written to the secret store only; the repository keeps metadata and masked hints.
package provider

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"app/internal/apperr"
	domain "app/internal/domain/provider"
	"app/internal/httpclient"
	"app/internal/secrets"
)

// Options configures the service.
type Options struct {
	Repo        domain.Repository
	Secrets     secrets.Store
	Client      *http.Client
	TestTimeout time.Duration
	// LimitDefaults are the app-wide concurrency / rate defaults for unset provider settings.
	LimitDefaults domain.LimitDefaults
	// AutoCheck verifies keys in the background when they are saved, changed or activated
	// (providers.auto_check). Off, the Test and Refresh buttons are the only way to check.
	AutoCheck bool
	// OnChanged is called after every successful mutation (the core publishes providers:changed).
	OnChanged func()
}

// Service implements the provider use cases.
type Service struct {
	repo          domain.Repository
	secrets       secrets.Store
	client        *http.Client
	testTimeout   time.Duration
	limitDefaults domain.LimitDefaults
	onChanged     func()
	auto          *autoCheck
	autoOn        bool
}

// New validates the registry and builds the service.
func New(o Options) (*Service, error) {
	if err := domain.ValidateRegistry(); err != nil {
		return nil, err
	}
	if o.Repo == nil || o.Secrets == nil || o.Client == nil || o.TestTimeout <= 0 {
		return nil, errors.New("provider: incomplete options")
	}
	return &Service{
		repo:          o.Repo,
		secrets:       o.Secrets,
		client:        httpclient.WithRedirectGuard(o.Client, domain.ValidateOutboundURL),
		testTimeout:   o.TestTimeout,
		limitDefaults: o.LimitDefaults,
		onChanged:     o.OnChanged,
		auto:          newAutoCheck(),
		autoOn:        o.AutoCheck,
	}, nil
}

func (s *Service) changed() {
	if s.onChanged != nil {
		s.onChanged()
	}
}

// Snapshot is everything the settings screen shows.
type Snapshot struct {
	LimitDefaults domain.LimitDefaults
	SecretBackend secrets.Backend
	Builtins      []BuiltinView
	Custom        []domain.CustomProvider
}

// BuiltinView is a registry entry with its saved credentials.
type BuiltinView struct {
	Meta        domain.Meta
	Credentials []domain.Credential
	Models      []domain.Model
	Settings    domain.Settings
}

// Configured reports whether the provider has an active credential.
func (v BuiltinView) Configured() bool {
	return slices.ContainsFunc(v.Credentials, func(c domain.Credential) bool { return c.Active })
}

// List returns the registry, saved credentials and custom providers.
func (s *Service) List(ctx context.Context) (Snapshot, error) {
	creds, err := s.repo.ListCredentials(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	custom, err := s.repo.ListCustom(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	for i := range creds {
		creds[i].Checking = s.isChecking(creds[i].ID)
	}
	for i := range custom {
		custom[i].Checking = s.isChecking(custom[i].ID)
	}
	out := Snapshot{LimitDefaults: s.limitDefaults, SecretBackend: secrets.BackendOf(s.secrets), Custom: custom}
	for _, m := range domain.Registry() {
		v := BuiltinView{Meta: m}
		if v.Models, err = s.repo.ListModels(ctx, m.ID); err != nil {
			return Snapshot{}, err
		}
		if v.Settings, err = s.repo.GetSettings(ctx, m.ID); err != nil {
			return Snapshot{}, err
		}
		for _, c := range creds {
			if c.ProviderID == m.ID {
				v.Credentials = append(v.Credentials, c)
			}
		}
		out.Builtins = append(out.Builtins, v)
	}
	return out, nil
}

func credSecretKey(credID, field string) secrets.Key {
	return secrets.Key("provider_cred_" + credID + "_" + field)
}

func customSecretKey(id string) secrets.Key { return secrets.Key("provider_custom_" + id + "_api_key") }

func lookup(id string) (domain.Meta, error) {
	m, ok := domain.Lookup(id)
	if !ok {
		return domain.Meta{}, apperr.New(apperr.CodeNotFound, "Unknown provider").WithDetails(map[string]any{"provider_id": id}).WithMsg("errors.provider.unknown_provider", map[string]any{"provider": id})
	}
	return m, nil
}

func checkSecretFields(m domain.Meta, in domain.Input) error {
	for k := range in.Secrets {
		if f, ok := m.Field(k); !ok || !f.Secret {
			return apperr.New(apperr.CodeValidation, "Unknown secret field "+k).WithDetails(map[string]any{"field": k}).WithMsg("errors.provider.unknown_field", map[string]any{"field": k})
		}
	}
	for k, v := range in.Values {
		if f, _ := m.Field(k); f.Kind == domain.FieldURL {
			if err := domain.ValidateBaseURL(v); err != nil {
				return apperr.New(apperr.CodeValidation, "Invalid URL").WithDetails(map[string]any{"field": k}).WithMsg("errors.provider.invalid_url", map[string]any{"field": k})
			}
		}
	}
	if f, ok := m.Field(domain.FieldCredentialsFile); ok {
		if v, set := in.Secrets[f.Key]; set {
			if err := validateServiceAccount(v); err != nil {
				return err
			}
		}
	}
	return nil
}

// writeSecrets stores the given secrets; on failure it removes what it wrote.
func (s *Service) writeSecrets(credID string, vals map[string]string) error {
	var written []secrets.Key
	for k, v := range vals {
		key := credSecretKey(credID, k)
		if err := s.secrets.Set(key, v); err != nil {
			for _, w := range written {
				_ = s.secrets.Delete(w)
			}
			return apperr.Wrap(apperr.CodeUnavailable, "Could not save the secret in the credential store", err).WithMsg("errors.provider.secret_store_unavailable", nil)
		}
		written = append(written, key)
	}
	return nil
}

func hints(vals map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range vals {
		out[k] = domain.MaskSecret(v)
	}
	return out
}

// CreateCredential saves a new credential; the first one of a provider becomes active.
func (s *Service) CreateCredential(ctx context.Context, providerID string, in domain.Input) (domain.Credential, error) {
	return s.createCredential(ctx, providerID, in, true)
}

func (s *Service) createCredential(ctx context.Context, providerID string, in domain.Input, autoCheck bool) (domain.Credential, error) {
	m, err := lookup(providerID)
	if err != nil {
		return domain.Credential{}, err
	}
	in = in.Normalize()
	if in.Name == "" {
		in.Name = m.DisplayName
	}
	if err := checkSecretFields(m, in); err != nil {
		return domain.Credential{}, err
	}
	if err := checkSingleGroup(m, in.Secrets); err != nil {
		return domain.Credential{}, err
	}
	if err := m.CheckComplete(in.Values, func(k string) bool { return in.Secrets[k] != "" }); err != nil {
		return domain.Credential{}, err
	}
	id := uuid.NewString()
	if err := s.writeSecrets(id, in.Secrets); err != nil {
		return domain.Credential{}, err
	}
	c, err := s.repo.CreateCredential(ctx, domain.Credential{
		ID: id, ProviderID: providerID, Name: in.Name, Values: in.Values, SecretHints: hints(in.Secrets),
	})
	if err != nil {
		s.deleteSecrets(id, m)
		return domain.Credential{}, err
	}
	s.changed()
	if autoCheck {
		s.startAutoCheck(c.ID) // is the key alive, which models does it offer
	}
	return c, nil
}

// UpdateCredential changes name/values; empty secrets keep the stored ones, ClearSecrets removes some.
func (s *Service) UpdateCredential(ctx context.Context, id string, in domain.Input) (domain.Credential, error) {
	c, err := s.repo.GetCredential(ctx, id)
	if err != nil {
		return domain.Credential{}, err
	}
	m, err := lookup(c.ProviderID)
	if err != nil {
		return domain.Credential{}, err
	}
	clear := in.ClearSecrets
	oldValues := c.Values
	in = in.Normalize()
	if in.Name == "" {
		in.Name = c.Name
	}
	if err := checkSecretFields(m, in); err != nil {
		return domain.Credential{}, err
	}
	for _, k := range clear {
		if f, ok := m.Field(k); !ok || !f.Secret {
			return domain.Credential{}, apperr.New(apperr.CodeValidation, "Unknown secret field "+k).WithDetails(map[string]any{"field": k}).WithMsg("errors.provider.unknown_field", map[string]any{"field": k})
		}
	}
	if err := checkSingleGroup(m, in.Secrets); err != nil {
		return domain.Credential{}, err
	}
	// Group switch (ArcReel _resolve_credential_group_switch): completing a group that was not complete
	// before clears the other groups, otherwise the old auth mode would silently keep winning.
	for _, g := range m.CredentialGroups {
		if groupComplete(g, func(k string) bool { return in.Secrets[k] != "" }) &&
			!groupComplete(g, func(k string) bool { return c.SecretHints[k] != "" }) {
			for _, other := range m.CredentialGroups {
				for _, k := range other {
					if !slices.Contains(g, k) && !slices.Contains(clear, k) {
						clear = append(clear, k)
					}
				}
			}
		}
	}
	present := func(k string) bool {
		if in.Secrets[k] != "" {
			return true
		}
		return c.SecretHints[k] != "" && !slices.Contains(clear, k)
	}
	if err := m.CheckComplete(in.Values, present); err != nil {
		return domain.Credential{}, err
	}
	// Snapshot every secret this update touches so a failed DB write can put them back.
	previous := map[string]string{}
	for _, k := range append(slices.Collect(maps.Keys(in.Secrets)), clear...) {
		if v, err := s.secrets.Get(credSecretKey(id, k)); err == nil {
			previous[k] = v
		}
	}
	restore := func() {
		for _, k := range append(slices.Collect(maps.Keys(in.Secrets)), clear...) {
			if v, ok := previous[k]; ok {
				_ = s.secrets.Set(credSecretKey(id, k), v)
			} else {
				_ = s.secrets.Delete(credSecretKey(id, k))
			}
		}
	}
	if err := s.writeSecrets(id, in.Secrets); err != nil {
		return domain.Credential{}, err
	}
	newHints := map[string]string{}
	for k, v := range c.SecretHints {
		if !slices.Contains(clear, k) {
			newHints[k] = v
		}
	}
	for k, v := range hints(in.Secrets) {
		newHints[k] = v
	}
	for _, k := range clear {
		if in.Secrets[k] == "" {
			_ = s.secrets.Delete(credSecretKey(id, k))
		}
	}
	c.Name, c.Values, c.SecretHints = in.Name, in.Values, newHints
	if err := s.repo.UpdateCredential(ctx, c); err != nil {
		restore()
		return domain.Credential{}, err
	}
	if len(in.Secrets) > 0 || len(clear) > 0 || !maps.Equal(c.Values, oldValues) {
		_ = s.repo.SetCredentialHealth(ctx, id, domain.Health{}) // the old result no longer describes this key
		s.changed()
		s.startAutoCheck(id)
	}
	s.changed()
	return s.repo.GetCredential(ctx, id)
}

func groupComplete(g []string, has func(string) bool) bool {
	for _, k := range g {
		if !has(k) {
			return false
		}
	}
	return true
}

// checkSingleGroup rejects a submission whose secrets span several credential groups (ArcReel 422).
func checkSingleGroup(m domain.Meta, secretsIn map[string]string) error {
	touched := 0
	for _, g := range m.CredentialGroups {
		if slices.ContainsFunc(g, func(k string) bool { return secretsIn[k] != "" }) {
			touched++
		}
	}
	if touched > 1 {
		return apperr.New(apperr.CodeValidation, "Fill in only one group of credentials").
			WithDetails(map[string]any{"groups": m.CredentialGroups}).WithMsg("errors.provider.one_group_only", nil)
	}
	return nil
}

func (s *Service) deleteSecrets(credID string, m domain.Meta) {
	for _, k := range m.SecretKeys() {
		_ = s.secrets.Delete(credSecretKey(credID, k))
	}
}

// DeleteCredential removes a credential and its secrets.
func (s *Service) DeleteCredential(ctx context.Context, id string) error {
	c, err := s.repo.GetCredential(ctx, id)
	if err != nil {
		return err
	}
	if err := s.repo.DeleteCredential(ctx, id); err != nil {
		return err
	}
	if m, ok := domain.Lookup(c.ProviderID); ok {
		s.deleteSecrets(id, m)
	}
	s.changed()
	return nil
}

// ActivateCredential makes id the active credential of its provider.
func (s *Service) ActivateCredential(ctx context.Context, id string) error {
	return s.activateCredential(ctx, id, true)
}

func (s *Service) activateCredential(ctx context.Context, id string, autoCheck bool) error {
	if err := s.repo.ActivateCredential(ctx, id); err != nil {
		return err
	}
	s.changed()
	if autoCheck {
		s.startAutoCheck(id) // the models come from the active key: refresh them
	}
	return nil
}

// ---- custom providers ----

func customModelsOrEmpty(m []domain.Model) []domain.Model {
	if m == nil {
		return []domain.Model{}
	}
	return m
}

// CreateCustom saves a custom provider.
func (s *Service) CreateCustom(ctx context.Context, in domain.CustomInput) (domain.CustomProvider, error) {
	return s.createCustom(ctx, in, true)
}

func (s *Service) createCustom(ctx context.Context, in domain.CustomInput, autoCheck bool) (domain.CustomProvider, error) {
	if err := in.Validate(); err != nil {
		return domain.CustomProvider{}, err
	}
	id := uuid.NewString()
	hint := ""
	if in.APIKey != "" {
		if err := s.secrets.Set(customSecretKey(id), in.APIKey); err != nil {
			return domain.CustomProvider{}, apperr.Wrap(apperr.CodeUnavailable, "Could not save the secret in the credential store", err).WithMsg("errors.provider.secret_store_unavailable", nil)
		}
		hint = domain.MaskSecret(in.APIKey)
	}
	p, err := s.repo.CreateCustom(ctx, domain.CustomProvider{
		ID: id, Name: in.Name, DiscoveryFormat: in.DiscoveryFormat, BaseURL: in.BaseURL,
		Models: customModelsOrEmpty(in.Models), KeyHint: hint,
	})
	if err != nil {
		_ = s.secrets.Delete(customSecretKey(id))
		return domain.CustomProvider{}, err
	}
	s.changed()
	if autoCheck {
		s.startAutoCheck(p.ID) // is the endpoint alive, which models does it offer
	}
	return p, nil
}

// UpdateCustom replaces a custom provider's settings and model list.
func (s *Service) UpdateCustom(ctx context.Context, id string, in domain.CustomInput) (domain.CustomProvider, error) {
	p, err := s.repo.GetCustom(ctx, id)
	if err != nil {
		return domain.CustomProvider{}, err
	}
	if err := in.Validate(); err != nil {
		return domain.CustomProvider{}, err
	}
	switch {
	case in.APIKey != "":
		if err := s.secrets.Set(customSecretKey(id), in.APIKey); err != nil {
			return domain.CustomProvider{}, apperr.Wrap(apperr.CodeUnavailable, "Could not save the secret in the credential store", err).WithMsg("errors.provider.secret_store_unavailable", nil)
		}
		p.KeyHint = domain.MaskSecret(in.APIKey)
	case in.ClearAPIKey:
		_ = s.secrets.Delete(customSecretKey(id))
		p.KeyHint = ""
	}
	keyChanged := in.APIKey != "" || in.ClearAPIKey || in.BaseURL != p.BaseURL || in.DiscoveryFormat != p.DiscoveryFormat
	p.Name, p.DiscoveryFormat, p.BaseURL, p.Models = in.Name, in.DiscoveryFormat, in.BaseURL, customModelsOrEmpty(in.Models)
	if err := s.repo.UpdateCustom(ctx, p); err != nil {
		return domain.CustomProvider{}, err
	}
	if keyChanged {
		_ = s.repo.SetCustomHealth(ctx, id, domain.Health{})
		s.changed()
		s.startAutoCheck(id)
	}
	s.changed()
	return s.repo.GetCustom(ctx, id)
}

// DeleteCustom removes a custom provider and its key.
func (s *Service) DeleteCustom(ctx context.Context, id string) error {
	if err := s.repo.DeleteCustom(ctx, id); err != nil {
		return err
	}
	_ = s.secrets.Delete(customSecretKey(id))
	s.changed()
	return nil
}

// ---- resolve (for generation features; never exposed to the UI or the agent) ----

// Resolved is a ready-to-use credential. Secrets holds plain secret values: keep it in memory only.
type Resolved struct {
	ProviderID   string
	CredentialID string
	BaseURL      string
	Values       map[string]string
	Secrets      map[string]string
}

// Resolve returns the active credential of a built-in provider.
func (s *Service) Resolve(ctx context.Context, providerID string) (Resolved, error) {
	m, err := lookup(providerID)
	if err != nil {
		return Resolved{}, err
	}
	creds, err := s.repo.ListCredentials(ctx)
	if err != nil {
		return Resolved{}, err
	}
	for _, c := range creds {
		if c.ProviderID == providerID && c.Active {
			return s.resolveCredential(m, c)
		}
	}
	return Resolved{}, apperr.New(apperr.CodeValidation, m.DisplayName+" has no active credential").
		WithDetails(map[string]any{"provider_id": providerID, "not_configured": true}).WithMsg("errors.provider.no_active_credential", map[string]any{"provider": m.DisplayName})
}

// ResolveFor is Resolve that also checks the provider offers capability c.
func (s *Service) ResolveFor(ctx context.Context, c domain.Capability, providerID string) (Resolved, error) {
	m, err := lookup(providerID)
	if err != nil {
		return Resolved{}, err
	}
	if !m.Has(c) {
		return Resolved{}, apperr.New(apperr.CodeValidation, fmt.Sprintf("%s does not support %s", m.DisplayName, c)).WithMsg("errors.provider.capability_unsupported", map[string]any{"provider": m.DisplayName, "capability": string(c)})
	}
	return s.Resolve(ctx, providerID)
}

func (s *Service) resolveCredential(m domain.Meta, c domain.Credential) (Resolved, error) {
	r := Resolved{ProviderID: m.ID, CredentialID: c.ID, Values: map[string]string{}, Secrets: map[string]string{}}
	for k, v := range c.Values {
		r.Values[k] = v
	}
	for k := range c.SecretHints {
		v, err := s.secrets.Get(credSecretKey(c.ID, k))
		if errors.Is(err, secrets.ErrNotFound) {
			continue
		}
		if err != nil {
			return Resolved{}, apperr.Wrap(apperr.CodeUnavailable, "Could not read the credential store", err).WithMsg("errors.provider.secret_store_unavailable", nil)
		}
		r.Secrets[k] = v
	}
	present := func(k string) bool { return r.Secrets[k] != "" }
	if err := m.CheckComplete(r.Values, present); err != nil {
		// Typically: secrets were held in memory (no keyring) and the app restarted.
		return Resolved{}, apperr.New(apperr.CodeIntegrity, "Stored secret is missing; enter the key again").
			WithDetails(map[string]any{"provider_id": m.ID, "credential_id": c.ID, "secret_missing": true}).WithMsg("errors.provider.secret_missing", nil)
	}
	r.BaseURL = strings.TrimRight(cmp.Or(r.Values[domain.FieldBaseURL], m.DefaultBaseURL), "/")
	return r, nil
}

// ResolvedCustom is a ready-to-use custom provider.
type ResolvedCustom struct {
	Provider domain.CustomProvider
	APIKey   string
}

// ResolveCustom returns a custom provider with its key.
func (s *Service) ResolveCustom(ctx context.Context, id string) (ResolvedCustom, error) {
	p, err := s.repo.GetCustom(ctx, id)
	if err != nil {
		return ResolvedCustom{}, err
	}
	key, err := s.customKey(p)
	if err != nil {
		return ResolvedCustom{}, err
	}
	return ResolvedCustom{Provider: p, APIKey: key}, nil
}

func (s *Service) customKey(p domain.CustomProvider) (string, error) {
	if p.KeyHint == "" {
		return "", nil
	}
	v, err := s.secrets.Get(customSecretKey(p.ID))
	if errors.Is(err, secrets.ErrNotFound) {
		return "", apperr.New(apperr.CodeIntegrity, "Stored secret is missing; enter the key again").
			WithDetails(map[string]any{"custom_id": p.ID, "secret_missing": true}).WithMsg("errors.provider.secret_missing", nil)
	}
	if err != nil {
		return "", apperr.Wrap(apperr.CodeUnavailable, "Could not read the credential store", err).WithMsg("errors.provider.secret_store_unavailable", nil)
	}
	return v, nil
}

// Status is the non-secret view of provider readiness, safe for the agent.
type Status struct {
	ID           string
	Name         string
	Custom       bool
	Capabilities []domain.Capability
	Configured   bool
}

// Statuses lists every provider with whether it is ready to use.
func (s *Service) Statuses(ctx context.Context) ([]Status, error) {
	snap, err := s.List(ctx)
	if err != nil {
		return nil, err
	}
	var out []Status
	for _, b := range snap.Builtins {
		out = append(out, Status{ID: b.Meta.ID, Name: b.Meta.DisplayName, Capabilities: b.Meta.Capabilities, Configured: b.Configured()})
	}
	for _, c := range snap.Custom {
		out = append(out, Status{ID: c.ID, Name: c.Name, Custom: true, Capabilities: c.Capabilities(), Configured: len(c.Capabilities()) > 0})
	}
	return out, nil
}
