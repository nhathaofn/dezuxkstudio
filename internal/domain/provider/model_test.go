package provider

import (
	"reflect"
	"testing"
)

func TestNormalizeModelsCustom(t *testing.T) {
	all := Capabilities()
	// capability derived from the endpoint
	got, err := NormalizeModels([]Model{{ModelID: " m ", DisplayName: " M ", Endpoint: EndpointOpenAIImages, Enabled: true}}, all, DiscoveryOpenAI)
	if err != nil || got[0].Capability != CapabilityImage || got[0].ModelID != "m" || got[0].DisplayName != "M" {
		t.Fatalf("%v %+v", err, got)
	}
	// protocol mismatch / unknown endpoint / empty endpoint
	for _, ep := range []Endpoint{EndpointGeminiImage, "nope", ""} {
		if _, err := NormalizeModels([]Model{{ModelID: "m", Endpoint: ep}}, all, DiscoveryOpenAI); err == nil {
			t.Fatalf("endpoint %q must fail", ep)
		}
	}
	// empty id
	if _, err := NormalizeModels([]Model{{ModelID: "  ", Endpoint: EndpointOpenAIChat}}, all, DiscoveryOpenAI); err == nil {
		t.Fatal("empty id must fail")
	}
}

func TestNormalizeModelsBuiltin(t *testing.T) {
	allowed := []Capability{CapabilityLLM, CapabilityImage}
	if _, err := NormalizeModels([]Model{{ModelID: "m", Capability: CapabilityLLM, Endpoint: EndpointOpenAIChat}}, allowed, ""); err == nil {
		t.Fatal("endpoint on built-in must fail")
	}
	if _, err := NormalizeModels([]Model{{ModelID: "m", Capability: CapabilityVideo}}, allowed, ""); err == nil {
		t.Fatal("capability not offered must fail")
	}
	if _, err := NormalizeModels([]Model{{ModelID: "m", Capability: ""}}, allowed, ""); err == nil {
		t.Fatal("empty capability must fail")
	}
	if _, err := NormalizeModels([]Model{{ModelID: "m", Capability: CapabilityLLM}, {ModelID: "m", Capability: CapabilityLLM}}, allowed, ""); err == nil {
		t.Fatal("duplicate must fail")
	}
	// same id under another capability is not a duplicate
	if _, err := NormalizeModels([]Model{{ModelID: "m", Capability: CapabilityLLM}, {ModelID: "m", Capability: CapabilityImage}}, allowed, ""); err != nil {
		t.Fatal(err)
	}
}

func TestNormalizeModelsDefaults(t *testing.T) {
	allowed := Capabilities()
	two := []Model{
		{ModelID: "a", Capability: CapabilityLLM, Enabled: true, IsDefault: true},
		{ModelID: "b", Capability: CapabilityLLM, Enabled: true, IsDefault: true},
	}
	if _, err := NormalizeModels(two, allowed, ""); err == nil {
		t.Fatal("two defaults must fail")
	}
	if _, err := NormalizeModels([]Model{{ModelID: "a", Capability: CapabilityLLM, IsDefault: true}}, allowed, ""); err == nil {
		t.Fatal("disabled default must fail")
	}
	ok := []Model{
		{ModelID: "a", Capability: CapabilityLLM, Enabled: true, IsDefault: true},
		{ModelID: "b", Capability: CapabilityImage, Enabled: true, IsDefault: true},
	}
	if _, err := NormalizeModels(ok, allowed, ""); err != nil {
		t.Fatal(err)
	}
}

func TestNormalizeModelsFacts(t *testing.T) {
	allowed := Capabilities()
	cases := []struct {
		name  string
		cap   Capability
		facts ModelFacts
		ok    bool
	}{
		{"video durations", CapabilityVideo, ModelFacts{Durations: []int{4, 8}, Audio: true, Resolutions: []string{"720p"}}, true},
		{"zero duration", CapabilityVideo, ModelFacts{Durations: []int{0}}, false},
		{"negative duration", CapabilityVideo, ModelFacts{Durations: []int{-1}}, false},
		{"blank resolution", CapabilityImage, ModelFacts{Resolutions: []string{" "}}, false},
		{"image resolution", CapabilityImage, ModelFacts{Resolutions: []string{"1024x1024"}}, true},
		{"durations on image", CapabilityImage, ModelFacts{Durations: []int{5}}, false},
		{"audio on llm", CapabilityLLM, ModelFacts{Audio: true}, false},
		{"tokens on llm", CapabilityLLM, ModelFacts{MaxOutputTokens: 4096}, true},
		{"negative tokens", CapabilityLLM, ModelFacts{MaxOutputTokens: -1}, false},
		{"tokens on video", CapabilityVideo, ModelFacts{MaxOutputTokens: 10}, false},
	}
	for _, c := range cases {
		_, err := NormalizeModels([]Model{{ModelID: "m", Capability: c.cap, Facts: c.facts}}, allowed, "")
		if (err == nil) != c.ok {
			t.Errorf("%s: err=%v want ok=%v", c.name, err, c.ok)
		}
	}
}

func TestMergeDiscovered(t *testing.T) {
	saved := []Model{
		{ModelID: "a", DisplayName: "Alpha", Capability: CapabilityLLM, Enabled: false, UserAdded: true, Facts: ModelFacts{MaxOutputTokens: 100}},
		{ModelID: "b", Capability: CapabilityLLM, Enabled: true, IsDefault: true},
	}
	disc := []Model{
		{ModelID: "a", Capability: CapabilityLLM, Enabled: true, IsDefault: true},
		{ModelID: "b", Capability: CapabilityLLM, DisplayName: "ignored"},
		{ModelID: "c", Capability: CapabilityLLM, IsDefault: true, UserAdded: true},
		{ModelID: "a", Capability: CapabilityImage}, // same id, other capability = new
	}
	got := MergeDiscovered(saved, disc)
	if len(got) != 4 {
		t.Fatalf("%+v", got)
	}
	a := got[0]
	if a.Enabled || a.DisplayName != "Alpha" || a.Facts.MaxOutputTokens != 100 || !a.UserAdded || a.IsDefault {
		t.Fatalf("saved model changed: %+v", a)
	}
	if got[1].DisplayName != "" || !got[1].IsDefault {
		t.Fatalf("saved b changed: %+v", got[1])
	}
	c := got[2]
	if !c.Enabled || c.IsDefault || c.UserAdded {
		t.Fatalf("new model must be enabled, not default, not user-added: %+v", c)
	}
	img := got[3]
	if img.Capability != CapabilityImage || !img.Enabled || !img.IsDefault {
		t.Fatalf("new capability must get a default: %+v", img)
	}
	if saved[0].Enabled || len(saved) != 2 {
		t.Fatal("saved mutated")
	}
}

func TestEnsureDefaults(t *testing.T) {
	in := []Model{
		{ModelID: "off", Capability: CapabilityLLM},
		{ModelID: "x", Capability: CapabilityLLM, Enabled: true},
		{ModelID: "y", Capability: CapabilityLLM, Enabled: true},
		{ModelID: "stale", Capability: CapabilityTTS, IsDefault: true}, // disabled default does not count
		{ModelID: "v", Capability: CapabilityVideo, Enabled: true, IsDefault: true},
		{ModelID: "v2", Capability: CapabilityVideo, Enabled: true},
	}
	got := EnsureDefaults(in)
	if got[0].IsDefault || !got[1].IsDefault || got[2].IsDefault || !got[4].IsDefault || got[5].IsDefault {
		t.Fatalf("%+v", got)
	}
	if in[1].IsDefault {
		t.Fatal("input mutated")
	}
	if m, ok := DefaultModel(got, CapabilityLLM); !ok || m.ModelID != "x" {
		t.Fatalf("%+v", m)
	}
	if _, ok := DefaultModel(got, CapabilityTTS); ok {
		t.Fatal("tts has no enabled default")
	}
	if !reflect.DeepEqual(EnabledCapabilities(got), []Capability{CapabilityLLM, CapabilityVideo}) {
		t.Fatal(EnabledCapabilities(got))
	}
}

func TestModelLabel(t *testing.T) {
	if (Model{ModelID: "id"}).Label() != "id" || (Model{ModelID: "id", DisplayName: "N"}).Label() != "N" {
		t.Fatal("label")
	}
}
