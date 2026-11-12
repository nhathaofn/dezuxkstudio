package provider

import (
	"cmp"
	"net/url"
	"strings"

	"app/internal/apperr"
)

// SetupRequest is a "here is my key" request, typically from the agent: the user pastes an API key and
// maybe a base URL, and the app decides whether it is a built-in provider or a custom endpoint.
type SetupRequest struct {
	// ProviderID forces a built-in provider; empty means detect.
	ProviderID string
	Name       string
	APIKey     string
	BaseURL    string
	// Secrets holds extra secret fields (e.g. Kling access_key/secret_key, Vertex credentials_file).
	Secrets map[string]string
	// Values holds extra non-secret fields (e.g. gcs_bucket).
	Values map[string]string
	// DiscoveryFormat forces the protocol of a custom provider; empty means guess from the URL.
	DiscoveryFormat DiscoveryFormat
}

// SetupKind says what a setup request becomes.
type SetupKind string

const (
	SetupBuiltin SetupKind = "builtin"
	SetupCustom  SetupKind = "custom"
)

// SetupPlan is the resolved target of a setup request.
type SetupPlan struct {
	Kind            SetupKind
	ProviderID      string // built-in id
	Name            string
	BaseURL         string // custom base URL, or a non-default built-in base URL ("" = default)
	DiscoveryFormat DiscoveryFormat
}

func host(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

// builtinHosts maps API hosts to built-in providers, from the registry (default URL host + Hosts).
func builtinHosts() map[string]string {
	out := map[string]string{}
	for _, m := range registry {
		for _, h := range append([]string{host(m.DefaultBaseURL)}, m.Hosts...) {
			if _, taken := out[h]; h != "" && !taken {
				out[h] = m.ID
			}
		}
	}
	return out
}

// PlanSetup decides where a setup request goes. Ark's two products share a host, so the path decides.
func PlanSetup(req SetupRequest) (SetupPlan, error) {
	req.ProviderID = strings.TrimSpace(req.ProviderID)
	base := strings.TrimRight(strings.TrimSpace(req.BaseURL), "/")
	key := strings.TrimSpace(req.APIKey)
	if base != "" {
		if err := ValidateBaseURL(base); err != nil {
			return SetupPlan{}, err
		}
	}
	id := req.ProviderID
	if id == "" && base != "" {
		id = builtinHosts()[host(base)]
		if id == IDArk && strings.Contains(base, "/api/plan") {
			id = IDArkAgentPlan
		}
	}
	if id == "" && base == "" {
		switch {
		case strings.HasPrefix(key, "sk-ant-"):
			id = IDAnthropic
		case strings.HasPrefix(key, "AIza"):
			id = IDGeminiAIStudio
		case strings.HasPrefix(key, "xai-"):
			id = IDGrok
		case req.Secrets[FieldCredentialsFile] != "":
			id = IDGeminiVertex
		case req.Secrets[FieldAccessKey] != "" && req.Secrets[FieldSecretKey] != "":
			id = IDKling
		default:
			return SetupPlan{}, apperr.New(apperr.CodeValidation,
				"Cannot tell which provider this key belongs to: give provider_id or base_url").
				WithDetails(map[string]any{"field": "provider_id"}).WithMsg("errors.provider.cannot_infer_provider", nil)
		}
	}
	if id != "" {
		m, ok := Lookup(id)
		if !ok {
			return SetupPlan{}, apperr.New(apperr.CodeValidation, "Unknown provider_id "+id).
				WithDetails(map[string]any{"field": "provider_id"}).WithMsg("errors.provider.unknown_provider", map[string]any{"provider": id})
		}
		plan := SetupPlan{Kind: SetupBuiltin, ProviderID: id, Name: cmp.Or(strings.TrimSpace(req.Name), m.DisplayName)}
		if base != "" && base != strings.TrimRight(m.DefaultBaseURL, "/") {
			if _, has := m.Field(FieldBaseURL); has {
				plan.BaseURL = base
			}
		}
		return plan, nil
	}
	format := req.DiscoveryFormat
	if format == "" {
		switch h := host(base); {
		case strings.Contains(base, "anthropic"):
			format = DiscoveryAnthropic
		case strings.Contains(h, "generativelanguage") || strings.Contains(base, "/v1beta"):
			format = DiscoveryGemini
		default:
			format = DiscoveryOpenAI
		}
	}
	return SetupPlan{Kind: SetupCustom, Name: cmp.Or(strings.TrimSpace(req.Name), host(base)),
		BaseURL: base, DiscoveryFormat: format}, nil
}
