package provider

import (
	"strings"

	"app/internal/apperr"
)

// Purpose is what a generation feature needs a model for. Each purpose names the capability its model
// must have; the user picks a default model per purpose (or leaves it on auto).
type Purpose string

const (
	PurposeText  Purpose = "text"
	PurposeImage Purpose = "image"
	PurposeVideo Purpose = "video"
	PurposeTTS   Purpose = "tts"
)

// PurposeSpec declares a purpose.
type PurposeSpec struct {
	Purpose    Purpose
	Capability Capability
}

// PurposeSpecs is the single list of purposes (new features add theirs here, e.g. "image_edit").
func PurposeSpecs() []PurposeSpec {
	return []PurposeSpec{
		{PurposeText, CapabilityLLM},
		{PurposeImage, CapabilityImage},
		{PurposeVideo, CapabilityVideo},
		{PurposeTTS, CapabilityTTS},
	}
}

// Purposes lists every purpose.
func Purposes() []Purpose {
	out := make([]Purpose, 0, len(PurposeSpecs()))
	for _, p := range PurposeSpecs() {
		out = append(out, p.Purpose)
	}
	return out
}

// PurposeCapability returns the capability a purpose needs.
func PurposeCapability(p Purpose) (Capability, bool) {
	for _, s := range PurposeSpecs() {
		if s.Purpose == p {
			return s.Capability, true
		}
	}
	return "", false
}

// ModelRef names a model of a provider: "<provider id>/<model id>". Provider ids are built-in registry
// ids or custom provider ids; model ids may themselves contain "/" (e.g. OpenRouter), so only the first
// separator splits.
type ModelRef struct {
	ProviderID string
	ModelID    string
}

// String formats the ref.
func (r ModelRef) String() string {
	if r.ProviderID == "" {
		return ""
	}
	return r.ProviderID + "/" + r.ModelID
}

// ParseModelRef parses "<provider>/<model>"; "" is the zero ref (auto).
func ParseModelRef(s string) (ModelRef, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return ModelRef{}, nil
	}
	p, m, ok := strings.Cut(s, "/")
	if !ok || p == "" || strings.TrimSpace(m) == "" {
		return ModelRef{}, apperr.New(apperr.CodeValidation, "A model must be written provider/model").
			WithDetails(map[string]any{"field": "model"}).WithMsg("errors.provider.model_ref_format", nil)
	}
	return ModelRef{ProviderID: p, ModelID: strings.TrimSpace(m)}, nil
}
