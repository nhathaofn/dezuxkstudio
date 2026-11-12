package provider

import (
	"fmt"
	"net"
	"net/url"
	"slices"
	"strings"
	"time"

	"app/internal/apperr"
)

// DiscoveryFormat is the API family used to list models and test a custom provider.
type DiscoveryFormat string

const (
	DiscoveryOpenAI    DiscoveryFormat = "openai"
	DiscoveryGemini    DiscoveryFormat = "gemini"
	DiscoveryAnthropic DiscoveryFormat = "anthropic"
)

// DiscoveryFormats lists valid discovery formats.
func DiscoveryFormats() []DiscoveryFormat {
	return []DiscoveryFormat{DiscoveryOpenAI, DiscoveryGemini, DiscoveryAnthropic}
}

// Endpoint is the wire protocol used for one model of a custom provider.
type Endpoint string

const (
	EndpointOpenAIChat              Endpoint = "openai-chat"
	EndpointOpenAIResponses         Endpoint = "openai-responses"
	EndpointGeminiGenerate          Endpoint = "gemini-generate"
	EndpointAnthropicMessages       Endpoint = "anthropic-messages"
	EndpointOpenAIImages            Endpoint = "openai-images"
	EndpointOpenAIImagesGenerations Endpoint = "openai-images-generations"
	EndpointOpenAIImagesEdits       Endpoint = "openai-images-edits"
	EndpointOpenAIChatImage         Endpoint = "openai-chat-image"
	EndpointOpenRouterImages        Endpoint = "openrouter-images"
	EndpointGeminiImage             Endpoint = "gemini-image"
	EndpointOpenAIVideo             Endpoint = "openai-video"
	EndpointOpenAITTS               Endpoint = "openai-tts"
	EndpointGeminiVideo             Endpoint = "gemini-video"
	EndpointGeminiTTS               Endpoint = "gemini-tts"
)

// EndpointSpec describes a custom-provider endpoint.
type EndpointSpec struct {
	Key        Endpoint
	Capability Capability
	Method     string
	Path       string
	// Formats are the discovery formats this endpoint may be attached to.
	Formats []DiscoveryFormat
}

// Endpoints is the catalog of custom-provider endpoints (single source of truth, mirrored in TS).
func Endpoints() []EndpointSpec {
	o := []DiscoveryFormat{DiscoveryOpenAI}
	return []EndpointSpec{
		{EndpointOpenAIChat, CapabilityLLM, "POST", "/chat/completions", o},
		{EndpointOpenAIResponses, CapabilityLLM, "POST", "/responses", o},
		{EndpointGeminiGenerate, CapabilityLLM, "POST", "/models/{model}:generateContent", []DiscoveryFormat{DiscoveryGemini}},
		{EndpointAnthropicMessages, CapabilityLLM, "POST", "/v1/messages", []DiscoveryFormat{DiscoveryAnthropic}},
		{EndpointOpenAIImages, CapabilityImage, "POST", "/images/generations|/images/edits", o},
		{EndpointOpenAIImagesGenerations, CapabilityImage, "POST", "/images/generations", o},
		{EndpointOpenAIImagesEdits, CapabilityImage, "POST", "/images/edits", o},
		{EndpointOpenAIChatImage, CapabilityImage, "POST", "/chat/completions", o},
		{EndpointOpenRouterImages, CapabilityImage, "POST", "/chat/completions", o},
		{EndpointGeminiImage, CapabilityImage, "POST", "/models/{model}:generateContent", []DiscoveryFormat{DiscoveryGemini}},
		{EndpointOpenAIVideo, CapabilityVideo, "POST", "/videos", o},
		{EndpointOpenAITTS, CapabilityTTS, "POST", "/audio/speech", o},
		{EndpointGeminiVideo, CapabilityVideo, "POST", "/models/{model}:predictLongRunning", []DiscoveryFormat{DiscoveryGemini}},
		{EndpointGeminiTTS, CapabilityTTS, "POST", "/models/{model}:generateContent", []DiscoveryFormat{DiscoveryGemini}},
	}
}

// EndpointByKey looks an endpoint up.
func EndpointByKey(k Endpoint) (EndpointSpec, bool) {
	for _, e := range Endpoints() {
		if e.Key == k {
			return e, true
		}
	}
	return EndpointSpec{}, false
}

// CustomModel is one model enabled on a custom provider.
type CustomModel struct {
	ModelID  string
	Endpoint Endpoint
	Enabled  bool
	// IsDefault marks the default model for the endpoint's capability on this provider.
	IsDefault bool
}

// Capability returns the capability of the model's endpoint.
func (m CustomModel) Capability() Capability {
	e, _ := EndpointByKey(m.Endpoint)
	return e.Capability
}

// CustomProvider is a user-defined endpoint (OpenAI-compatible and similar).
type CustomProvider struct {
	ID              string
	Name            string
	DiscoveryFormat DiscoveryFormat
	BaseURL         string
	Models          []CustomModel
	// KeyHint is the masked API key ("" when none is stored).
	KeyHint   string
	Health    Health
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Capabilities derives the capabilities from the enabled models.
func (p CustomProvider) Capabilities() []Capability {
	var out []Capability
	for _, c := range Capabilities() {
		for _, m := range p.Models {
			if m.Enabled && m.Capability() == c {
				out = append(out, c)
				break
			}
		}
	}
	return out
}

// CustomInput is a create/update request for a custom provider. An empty APIKey on update keeps the
// stored key; ClearAPIKey removes it.
type CustomInput struct {
	Name            string
	DiscoveryFormat DiscoveryFormat
	BaseURL         string
	APIKey          string
	ClearAPIKey     bool
	Models          []CustomModel
}

// Validate normalizes and checks the input.
func (in *CustomInput) Validate() error {
	in.Name = strings.TrimSpace(in.Name)
	in.BaseURL = strings.TrimRight(strings.TrimSpace(in.BaseURL), "/")
	in.APIKey = strings.TrimSpace(in.APIKey)
	if in.Name == "" {
		return apperr.New(apperr.CodeValidation, "Name is required").WithDetails(map[string]any{"field": "name"})
	}
	if !slices.Contains(DiscoveryFormats(), in.DiscoveryFormat) {
		return apperr.New(apperr.CodeValidation, "Unknown protocol").WithDetails(map[string]any{"field": "discovery_format"})
	}
	if err := ValidateBaseURL(in.BaseURL); err != nil {
		return err
	}
	seen := map[string]bool{}
	defaults := map[Capability]bool{}
	for i := range in.Models {
		m := &in.Models[i]
		m.ModelID = strings.TrimSpace(m.ModelID)
		spec, ok := EndpointByKey(m.Endpoint)
		if m.ModelID == "" || !ok {
			return apperr.New(apperr.CodeValidation, "Each model needs an id and a valid endpoint").
				WithDetails(map[string]any{"field": "models"})
		}
		if !slices.Contains(spec.Formats, in.DiscoveryFormat) {
			return apperr.New(apperr.CodeValidation, fmt.Sprintf("Endpoint %s does not match the protocol", m.Endpoint)).
				WithDetails(map[string]any{"field": "models"})
		}
		k := m.ModelID + "\x00" + string(m.Endpoint)
		if seen[k] {
			return apperr.New(apperr.CodeValidation, "Duplicate model "+m.ModelID).WithDetails(map[string]any{"field": "models"})
		}
		seen[k] = true
		if m.IsDefault {
			if !m.Enabled || defaults[spec.Capability] {
				return apperr.New(apperr.CodeValidation, "Only one enabled default model per capability").
					WithDetails(map[string]any{"field": "models"})
			}
			defaults[spec.Capability] = true
		}
	}
	return nil
}

// ValidateBaseURL accepts absolute http(s) URLs without credentials, query or fragment, and refuses
// link-local / cloud-metadata hosts (ArcReel validate_anthropic_base_url + artifact_http_client).
// Loopback and private ranges stay allowed for local servers (Ollama, LM Studio).
func ValidateBaseURL(raw string) error {
	bad := func(msg string) error {
		return apperr.New(apperr.CodeValidation, msg).WithDetails(map[string]any{"field": "base_url"})
	}
	u, err := url.Parse(raw)
	if raw == "" || err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return bad("Base URL must be an http(s) URL")
	}
	if u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return bad("Base URL must not contain credentials, a query or a fragment")
	}
	h := strings.ToLower(u.Hostname())
	if ip := net.ParseIP(h); ip != nil && (ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified()) {
		return bad("Base URL points at a link-local address")
	}
	if h == "metadata.google.internal" || h == "metadata" || strings.HasSuffix(h, ".internal.") {
		return bad("Base URL points at a cloud metadata service")
	}
	return nil
}
