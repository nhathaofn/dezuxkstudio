package provider

import (
	"net"
	"regexp"
	"testing"
)

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
	want := map[Purpose]Capability{
		PurposeText:    CapabilityLLM,
		PurposeImage:   CapabilityImage,
		PurposeVideo:   CapabilityVideo,
		PurposeTTS:     CapabilityTTS,
		PurposeLipSync: CapabilityLipSync,
	}
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

func TestRegistryOpenAITTSAndBaseURLs(t *testing.T) {
	if err := ValidateRegistry(); err != nil {
		t.Fatalf("ValidateRegistry failed: %v", err)
	}

	openaiMeta, ok := Lookup(IDOpenAI)
	if !ok {
		t.Fatal("missing openai registry entry")
	}
	if !openaiMeta.Has(CapabilityTTS) {
		t.Fatal("expected built-in openai provider to advertise CapabilityTTS")
	}
	re := regexp.MustCompile(openaiMeta.Probe.ModelFilter)
	for _, mod := range []string{"tts-1", "tts-1-hd", "gpt-4o-mini-tts", "sora-2", "gpt-image-1"} {
		if !re.MatchString(mod) {
			t.Errorf("expected openai ModelFilter %q to match %q", openaiMeta.Probe.ModelFilter, mod)
		}
	}

	if got := DefaultBaseURLFor(IDGeminiVertex); got != VertexAIBaseURL {
		t.Fatalf("DefaultBaseURLFor(IDGeminiVertex) = %q, want %q", got, VertexAIBaseURL)
	}
	if got := DefaultBaseURLFor(IDArk); got == "" {
		t.Fatal("expected non-empty DefaultBaseURLFor(IDArk)")
	}
	if got := DefaultBaseURLFor("nonexistent"); got != "" {
		t.Fatalf("expected empty DefaultBaseURLFor unknown provider, got %q", got)
	}
}

func TestValidateBaseURLAndSSRFGuards(t *testing.T) {
	if err := ValidateBaseURL("https://api.example.com/v1"); err != nil {
		t.Fatalf("valid https URL rejected: %v", err)
	}
	if err := ValidateBaseURL("http://127.0.0.1:11434/v1"); err != nil {
		t.Fatalf("local Ollama loopback URL rejected: %v", err)
	}
	if err := ValidateOutboundURL("https://cdn.example.com/out.mp4?token=abc"); err != nil {
		t.Fatalf("valid outbound signed URL rejected: %v", err)
	}
	for _, bad := range []string{
		"",
		"ftp://example.com",
		"https://user:pass@example.com/v1",
		"https://example.com/v1?x=1",
		"http://169.254.169.254/latest/meta-data",
		"http://[fe80::1%25eth0]/v1",
		"http://[fd00:ec2::254]/latest",
		"http://100.100.100.200/latest",
		"http://[::ffff:169.254.169.254]/latest",
		"http://192.0.0.192/v1",
		"http://metadata.google.internal/computeMetadata/v1",
		"http://service.internal/v1",
	} {
		if err := ValidateBaseURL(bad); err == nil {
			t.Errorf("ValidateBaseURL(%q) must fail", bad)
		}
	}
	if !IsUnsafeOutboundIP(net.ParseIP("169.254.169.254")) || !IsUnsafeOutboundIP(net.ParseIP("0.0.0.0")) || !IsUnsafeOutboundIP(nil) {
		t.Fatal("expected link-local, unspecified, and nil IPs to be unsafe")
	}
}
