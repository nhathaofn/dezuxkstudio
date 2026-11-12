package provider

import "testing"

func TestRegistryIsValid(t *testing.T) {
	if err := ValidateRegistry(); err != nil {
		t.Fatal(err)
	}
	if len(Registry()) != 12 {
		t.Fatalf("expected 12 built-in providers, got %d", len(Registry()))
	}
}

func TestValidateRejectsBadGroups(t *testing.T) {
	m := Meta{ID: "x", DisplayName: "X", Capabilities: []Capability{CapabilityLLM},
		Fields:           []Field{apiKey(), {Key: "b", Kind: FieldText, Secret: true, Required: true}},
		CredentialGroups: [][]string{{FieldAPIKey}}}
	if m.Validate() == nil {
		t.Fatal("uncovered required secret must fail")
	}
	m.CredentialGroups = [][]string{{FieldAPIKey}, {}}
	if m.Validate() == nil {
		t.Fatal("empty group must fail")
	}
}

func TestCheckCompleteKlingGroups(t *testing.T) {
	k, _ := Lookup(IDKling)
	have := map[string]bool{}
	present := func(key string) bool { return have[key] }
	if k.CheckComplete(nil, present) == nil {
		t.Fatal("empty must fail")
	}
	have[FieldAccessKey] = true
	if k.CheckComplete(nil, present) == nil {
		t.Fatal("half a group must fail")
	}
	have[FieldSecretKey] = true
	if err := k.CheckComplete(nil, present); err != nil {
		t.Fatal(err)
	}
	if err := k.CheckComplete(map[string]string{"bogus": "1"}, present); err == nil {
		t.Fatal("unknown field must fail")
	}
}

func TestCheckCompleteSimple(t *testing.T) {
	o, _ := Lookup(IDOpenAI)
	if o.CheckComplete(map[string]string{FieldBaseURL: "https://x"}, func(string) bool { return false }) == nil {
		t.Fatal("missing api key must fail")
	}
	if err := o.CheckComplete(nil, func(k string) bool { return k == FieldAPIKey }); err != nil {
		t.Fatal(err)
	}
}

func TestCustomInputValidate(t *testing.T) {
	in := CustomInput{Name: " a ", DiscoveryFormat: DiscoveryOpenAI, BaseURL: "https://x/v1/",
		Models: []Model{{ModelID: "m", Endpoint: EndpointGeminiImage, Enabled: true}}}
	if in.Validate() == nil {
		t.Fatal("gemini endpoint on openai protocol must fail")
	}
	in.Models[0].Endpoint = EndpointOpenAITTS
	if err := in.Validate(); err != nil || in.BaseURL != "https://x/v1" {
		t.Fatalf("%v %q", err, in.BaseURL)
	}
	in.BaseURL = "ftp://x"
	if in.Validate() == nil {
		t.Fatal("non-http url must fail")
	}
}

func TestMaskSecret(t *testing.T) {
	if MaskSecret("short") != "••••" || MaskSecret("sk-1234567890abcd") != "sk-…abcd" {
		t.Fatal(MaskSecret("sk-1234567890abcd"))
	}
}
