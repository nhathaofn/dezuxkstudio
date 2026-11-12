package provider

import "testing"

func TestParseModelRef(t *testing.T) {
	r, err := ParseModelRef("")
	if err != nil || r != (ModelRef{}) || r.String() != "" {
		t.Fatal(r, err)
	}
	r, err = ParseModelRef(" openai/gpt-4o ")
	if err != nil || r.ProviderID != "openai" || r.ModelID != "gpt-4o" || r.String() != "openai/gpt-4o" {
		t.Fatal(r, err)
	}
	r, err = ParseModelRef("cp_1/meta-llama/llama-3:free")
	if err != nil || r.ProviderID != "cp_1" || r.ModelID != "meta-llama/llama-3:free" || r.String() != "cp_1/meta-llama/llama-3:free" {
		t.Fatal(r, err)
	}
	for _, bad := range []string{"openai", "/m", "openai/", "openai/  "} {
		if _, err := ParseModelRef(bad); err == nil {
			t.Errorf("%q must fail", bad)
		}
	}
}

func TestPurposes(t *testing.T) {
	want := map[Purpose]Capability{PurposeText: CapabilityLLM, PurposeImage: CapabilityImage, PurposeVideo: CapabilityVideo, PurposeTTS: CapabilityTTS}
	if len(Purposes()) != len(want) {
		t.Fatal(Purposes())
	}
	for p, c := range want {
		if got, ok := PurposeCapability(p); !ok || got != c {
			t.Fatal(p, got)
		}
	}
	if _, ok := PurposeCapability("nope"); ok {
		t.Fatal("unknown purpose")
	}
}
