package contracts

import (
	"encoding/json"
	"reflect"
	"testing"

	domain "app/internal/domain/provider"
	"app/internal/secrets"
)

func listResult(t *testing.T, def domain.LimitDefaults, custom []domain.CustomProvider) ProvidersListResult {
	t.Helper()
	var builtins []BuiltinWithCredentials
	for _, m := range domain.Registry() {
		builtins = append(builtins, BuiltinWithCredentials{Meta: m})
	}
	return NewProvidersListResult(secrets.BackendMemory, builtins, custom, def)
}

func TestProvidersListSettingSpecsAndDefaults(t *testing.T) {
	def := domain.LimitDefaults{MaxWorkers: 3, RPM: 20, RequestGapMs: 50}
	res := listResult(t, def, nil)

	if len(res.SettingSpecs) != len(domain.SettingSpecs()) {
		t.Fatalf("specs = %+v", res.SettingSpecs)
	}
	for i, sp := range domain.SettingSpecs() {
		got := res.SettingSpecs[i]
		if got.Key != sp.Key || got.Capability != sp.Capability || got.Min != sp.Min || got.Max != sp.Max {
			t.Errorf("spec %d = %+v, want %+v", i, got, sp)
		}
	}

	byID := map[string]BuiltinProvider{}
	for _, b := range res.Builtins {
		byID[b.ID] = b
		for _, sp := range domain.SettingSpecs() {
			if _, ok := b.SettingDefaults[sp.Key]; !ok {
				t.Errorf("%s: no default for %s", b.ID, sp.Key)
			}
		}
		if b.Settings == nil || b.Models == nil {
			t.Errorf("%s: nil settings/models would serialize as null", b.ID)
		}
	}
	if len(res.Builtins) != len(domain.Registry()) {
		t.Fatalf("builtins = %d", len(res.Builtins))
	}

	// Agnes: one video job by Meta.DefaultConcurrency, everything else from config
	ag := byID[domain.IDAgnes].SettingDefaults
	if ag[domain.SettingVideoMaxWorkers] != 1 || ag[domain.SettingImageMaxWorkers] != 3 || ag[domain.SettingTTSMaxWorkers] != 3 ||
		ag[domain.SettingImageRPM] != 20 || ag[domain.SettingVideoRPM] != 20 || ag[domain.SettingRequestGapMs] != 50 {
		t.Fatalf("agnes defaults = %v", ag)
	}
	if byID[domain.IDOpenAI].SettingDefaults[domain.SettingVideoMaxWorkers] != 3 {
		t.Fatalf("openai defaults = %v", byID[domain.IDOpenAI].SettingDefaults)
	}
}

func TestProvidersListDiscoverable(t *testing.T) {
	res := listResult(t, domain.LimitDefaults{MaxWorkers: 1}, nil)
	want := map[string]bool{
		domain.IDOpenAI: true, domain.IDGeminiAIStudio: true, domain.IDAnthropic: true,
		domain.IDGrok: true, domain.IDAgnes: true, domain.IDDashScope: true, domain.IDMiniMax: true,
		domain.IDKling: false, domain.IDVidu: false, domain.IDArk: false, domain.IDArkAgentPlan: false, domain.IDGeminiVertex: false,
	}
	for _, b := range res.Builtins {
		if w, ok := want[b.ID]; !ok || b.Discoverable != w {
			t.Errorf("%s discoverable = %v, want %v", b.ID, b.Discoverable, w)
		}
	}
}

func TestProvidersListCustomAndJSONShape(t *testing.T) {
	cp := domain.CustomProvider{ID: "cp1", Name: "C", DiscoveryFormat: domain.DiscoveryOpenAI, BaseURL: "https://a.test",
		Models:   []domain.Model{{ModelID: "m", Capability: domain.CapabilityImage, Endpoint: domain.EndpointOpenAIImages, Enabled: true}},
		Settings: domain.Settings{domain.SettingImageMaxWorkers: 2}}
	empty := domain.CustomProvider{ID: "cp2", Name: "E"}
	res := listResult(t, domain.LimitDefaults{MaxWorkers: 4, RPM: 0, RequestGapMs: 0}, []domain.CustomProvider{cp, empty})

	if len(res.Custom) != 2 || res.Custom[0].Settings[domain.SettingImageMaxWorkers] != 2 ||
		res.Custom[0].SettingDefaults[domain.SettingVideoMaxWorkers] != 4 ||
		!reflect.DeepEqual(res.Custom[0].Capabilities, []domain.Capability{domain.CapabilityImage}) {
		t.Fatalf("%+v", res.Custom)
	}
	if res.Custom[1].Capabilities == nil || res.Custom[1].Models == nil || res.Custom[1].Settings == nil {
		t.Fatalf("empty custom provider must serialize arrays/objects, not null: %+v", res.Custom[1])
	}
	raw, _ := json.Marshal(res)
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"secret_backend", "builtins", "custom", "endpoints", "setting_specs"} {
		if m[k] == nil {
			t.Errorf("missing %s", k)
		}
	}
	first := m["builtins"].([]any)[0].(map[string]any)
	for _, k := range []string{"models", "settings", "setting_defaults", "discoverable", "credentials", "fields", "credential_groups"} {
		if first[k] == nil {
			t.Errorf("builtin missing %s", k)
		}
	}
}

func TestProviderModelRoundTrip(t *testing.T) {
	in := domain.Model{ModelID: "veo", DisplayName: "Veo", Capability: domain.CapabilityVideo, Enabled: true, IsDefault: true, UserAdded: true,
		Facts: domain.ModelFacts{Durations: []int{4, 8}, Resolutions: []string{"720p"}, Audio: true}}
	if got := NewProviderModel(in).ToDomain(); !reflect.DeepEqual(got, in) {
		t.Fatalf("%+v != %+v", got, in)
	}
	// nil facts serialize as empty arrays
	pm := NewProviderModel(domain.Model{ModelID: "m", Capability: domain.CapabilityLLM})
	if pm.Facts.Durations == nil || pm.Facts.Resolutions == nil {
		t.Fatalf("%+v", pm.Facts)
	}
	if ms := NewProviderModels(nil); ms == nil || len(ms) != 0 {
		t.Fatal("nil list")
	}
}

func TestSetDefaultAndSettingsPayloads(t *testing.T) {
	if (SetDefaultPayload{}).Validate() == nil {
		t.Fatal("purpose required")
	}
	if err := (SetDefaultPayload{Purpose: domain.PurposeText}).Validate(); err != nil {
		t.Fatal(err)
	}
	if (UpdateSettingsPayload{}).Validate() == nil {
		t.Fatal("owner required")
	}
	if err := (UpdateSettingsPayload{OwnerID: "openai"}).Validate(); err != nil {
		t.Fatal(err)
	}
	if r := NewProviderSettingsResult(nil); r.Settings == nil {
		t.Fatal("nil settings")
	}
}
