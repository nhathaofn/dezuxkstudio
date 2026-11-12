package provider

import (
	"slices"
	"strings"

	"app/internal/apperr"
)

// ModelFacts are the user-editable capabilities of a model, used by generation features and their UI
// (which durations / resolutions a video model accepts, the output-token cap of an LLM...).
// Empty means "unknown": features then fall back to their own defaults.
type ModelFacts struct {
	Durations       []int    `json:"durations,omitempty"`   // video: allowed lengths in seconds
	Resolutions     []string `json:"resolutions,omitempty"` // image/video: e.g. "720p", "1080p", "1024x1024"
	Audio           bool     `json:"audio,omitempty"`       // video: can generate an audio track
	MaxOutputTokens int      `json:"max_output_tokens,omitempty"`
}

// Validate checks the facts against the model's capability.
func (f ModelFacts) Validate(c Capability) error {
	bad := func(msg string) error {
		return apperr.New(apperr.CodeValidation, msg).WithDetails(map[string]any{"field": "facts"})
	}
	for _, d := range f.Durations {
		if d <= 0 {
			return bad("Durations must be positive seconds")
		}
	}
	for _, r := range f.Resolutions {
		if strings.TrimSpace(r) == "" {
			return bad("Resolutions must not be empty")
		}
	}
	if f.MaxOutputTokens < 0 {
		return bad("max_output_tokens must not be negative")
	}
	if c != CapabilityVideo && (len(f.Durations) > 0 || f.Audio) {
		return bad("Durations and audio apply to video models only")
	}
	if c != CapabilityLLM && f.MaxOutputTokens > 0 {
		return bad("max_output_tokens applies to LLM models only")
	}
	return nil
}

// Model is one model of a provider (built-in or custom). Built-in providers fill Endpoint with ""
// (their own API is implied); custom providers name the wire protocol per model.
type Model struct {
	ModelID     string
	DisplayName string
	Capability  Capability
	Endpoint    Endpoint
	Enabled     bool
	// IsDefault marks the provider's default model for Capability (at most one per capability).
	IsDefault bool
	// UserAdded marks models typed in by the user rather than discovered.
	UserAdded bool
	Facts     ModelFacts
}

// Label is the display name, or the id when none is set.
func (m Model) Label() string {
	if m.DisplayName != "" {
		return m.DisplayName
	}
	return m.ModelID
}

// NormalizeModels trims, validates and checks uniqueness / one default per capability.
// forFormat is the custom provider protocol ("" for built-in providers, whose models carry no endpoint).
func NormalizeModels(models []Model, allowed []Capability, forFormat DiscoveryFormat) ([]Model, error) {
	bad := func(msg string) error {
		return apperr.New(apperr.CodeValidation, msg).WithDetails(map[string]any{"field": "models"})
	}
	out := make([]Model, 0, len(models))
	seen := map[string]bool{}
	defaults := map[Capability]bool{}
	for _, m := range models {
		m.ModelID = strings.TrimSpace(m.ModelID)
		m.DisplayName = strings.TrimSpace(m.DisplayName)
		if m.ModelID == "" {
			return nil, bad("Each model needs an id")
		}
		if forFormat != "" {
			spec, ok := EndpointByKey(m.Endpoint)
			if !ok || !slices.Contains(spec.Formats, forFormat) {
				return nil, bad("Endpoint " + string(m.Endpoint) + " does not match the protocol")
			}
			m.Capability = spec.Capability
		} else if m.Endpoint != "" {
			return nil, bad("Built-in provider models have no endpoint")
		}
		if !slices.Contains(allowed, m.Capability) {
			return nil, bad("Model " + m.ModelID + " has a capability this provider does not offer")
		}
		if err := m.Facts.Validate(m.Capability); err != nil {
			return nil, err
		}
		k := m.ModelID + "\x00" + string(m.Capability)
		if seen[k] {
			return nil, bad("Duplicate model " + m.ModelID)
		}
		seen[k] = true
		if m.IsDefault {
			if !m.Enabled || defaults[m.Capability] {
				return nil, bad("Only one enabled default model per capability")
			}
			defaults[m.Capability] = true
		}
		out = append(out, m)
	}
	return out, nil
}

// MergeDiscovered adds newly discovered models to the saved list without touching the user's choices
// (enabled flags, defaults, facts, display names). New models are enabled; a capability with no default
// gets its first enabled model as default.
func MergeDiscovered(saved, discovered []Model) []Model {
	out := slices.Clone(saved)
	has := map[string]bool{}
	for _, m := range saved {
		has[m.ModelID+"\x00"+string(m.Capability)] = true
	}
	for _, d := range discovered {
		if !has[d.ModelID+"\x00"+string(d.Capability)] {
			d.Enabled, d.IsDefault, d.UserAdded = true, false, false
			out = append(out, d)
		}
	}
	return EnsureDefaults(out)
}

// EnsureDefaults gives every capability that has enabled models but no default its first enabled model.
func EnsureDefaults(models []Model) []Model {
	out := slices.Clone(models)
	has := map[Capability]bool{}
	for _, m := range out {
		if m.IsDefault && m.Enabled {
			has[m.Capability] = true
		}
	}
	for i := range out {
		if out[i].Enabled && !has[out[i].Capability] {
			out[i].IsDefault, has[out[i].Capability] = true, true
		}
	}
	return out
}

// DefaultModel returns the default enabled model of capability c.
func DefaultModel(models []Model, c Capability) (Model, bool) {
	for _, m := range models {
		if m.Capability == c && m.Enabled && m.IsDefault {
			return m, true
		}
	}
	return Model{}, false
}

// EnabledCapabilities lists capabilities that have at least one enabled model, in display order.
func EnabledCapabilities(models []Model) []Capability {
	var out []Capability
	for _, c := range Capabilities() {
		if slices.ContainsFunc(models, func(m Model) bool { return m.Enabled && m.Capability == c }) {
			out = append(out, c)
		}
	}
	return out
}
