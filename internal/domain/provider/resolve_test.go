package provider

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestKlingJWT(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	tok, err := KlingJWT("  ak-123  ", "  sk-456  ", now)
	if err != nil {
		t.Fatalf("KlingJWT() error = %v", err)
	}
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("expected 3 JWT parts, got %d (%q)", len(parts), tok)
	}

	hb, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		t.Fatalf("decode header: %v", err)
	}
	var hdr map[string]string
	if err := json.Unmarshal(hb, &hdr); err != nil {
		t.Fatalf("unmarshal header: %v", err)
	}
	if hdr["alg"] != "HS256" || hdr["typ"] != "JWT" {
		t.Fatalf("unexpected header: %+v", hdr)
	}

	cb, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode claims: %v", err)
	}
	var claims struct {
		Iss string `json:"iss"`
		Exp int64  `json:"exp"`
		Nbf int64  `json:"nbf"`
	}
	if err := json.Unmarshal(cb, &claims); err != nil {
		t.Fatalf("unmarshal claims: %v", err)
	}
	if claims.Iss != "ak-123" {
		t.Errorf("iss = %q, want %q", claims.Iss, "ak-123")
	}
	if claims.Exp != now.Unix()+1800 {
		t.Errorf("exp = %d, want %d", claims.Exp, now.Unix()+1800)
	}
	if claims.Nbf != now.Unix()-5 {
		t.Errorf("nbf = %d, want %d", claims.Nbf, now.Unix()-5)
	}

	mac := hmac.New(sha256.New, []byte("sk-456"))
	mac.Write([]byte(parts[0] + "." + parts[1]))
	wantSig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if parts[2] != wantSig {
		t.Errorf("signature mismatch: got %q, want %q", parts[2], wantSig)
	}
}

func TestKlingJWTRequiresBothKeys(t *testing.T) {
	cases := []struct {
		name string
		ak   string
		sk   string
	}{
		{name: "empty ak", ak: "", sk: "sk"},
		{name: "empty sk", ak: "ak", sk: ""},
		{name: "whitespace ak", ak: "   ", sk: "sk"},
		{name: "whitespace sk", ak: "ak", sk: " \t "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := KlingJWT(tc.ak, tc.sk, time.Now()); err == nil {
				t.Fatalf("expected error for (%q, %q)", tc.ak, tc.sk)
			}
		})
	}
}

func TestSignJWT(t *testing.T) {
	var seenUnsigned string
	tok, err := SignJWT(
		map[string]string{"alg": "RS256", "typ": "JWT"},
		map[string]any{"iss": "svc@example.com", "iat": int64(100)},
		func(in []byte) ([]byte, error) {
			seenUnsigned = string(in)
			return []byte("raw-sig"), nil
		},
	)
	if err != nil {
		t.Fatalf("SignJWT() error = %v", err)
	}
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("expected 3 parts, got %d", len(parts))
	}
	if seenUnsigned != parts[0]+"."+parts[1] {
		t.Errorf("signed input = %q, want %q", seenUnsigned, parts[0]+"."+parts[1])
	}
	if parts[2] != base64.RawURLEncoding.EncodeToString([]byte("raw-sig")) {
		t.Errorf("unexpected signature part: %q", parts[2])
	}

	wantErr := errors.New("sign boom")
	if _, err := SignJWT(map[string]string{"alg": "RS256"}, map[string]any{"iss": "x"}, func([]byte) ([]byte, error) {
		return nil, wantErr
	}); !errors.Is(err, wantErr) {
		t.Errorf("err = %v, want %v", err, wantErr)
	}

	if _, err := SignJWT(map[string]string{"alg": "RS256"}, map[string]any{"iss": "x"}, nil); err == nil {
		t.Error("expected error when sign callback is nil")
	}

	if _, err := SignJWT(map[string]string{"alg": "RS256"}, map[string]any{"bad": func() {}}, func([]byte) ([]byte, error) {
		return []byte("sig"), nil
	}); err == nil {
		t.Error("expected error for unmarshalable claim")
	}
}

func TestEnsureOpenAIBase(t *testing.T) {
	cases := map[string]string{
		"https://x.com":        "https://x.com/v1",
		"https://x.com/":       "https://x.com/v1",
		"https://x.com/v1/":    "https://x.com/v1",
		"https://x.com/v4":     "https://x.com/v4",
		"https://x.com/v1beta": "https://x.com/v1beta/v1",
		"https://x.com/api/v3": "https://x.com/api/v3",
	}
	for in, want := range cases {
		if got := EnsureOpenAIBase(in); got != want {
			t.Errorf("EnsureOpenAIBase(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestResolvedModel(t *testing.T) {
	builtin := ResolvedModel{
		Purpose: PurposeImage,
		Ref:     ModelRef{ProviderID: IDOpenAI, ModelID: "gpt-image-1"},
		Model:   Model{ModelID: "gpt-image-1", Capability: CapabilityImage, Enabled: true},
		Limits:  Limits{MaxWorkers: map[Capability]int{CapabilityImage: 2}, RPM: map[Capability]int{CapabilityImage: 10}, RequestGapMs: 100},
		Builtin: &Resolved{
			ProviderID:   IDOpenAI,
			CredentialID: "cred-1",
			BaseURL:      "https://api.openai.com/v1",
			Values:       map[string]string{FieldBaseURL: "https://api.openai.com/v1"},
			Secrets:      map[string]string{FieldAPIKey: "sk-test"},
		},
	}
	if builtin.Builtin == nil || builtin.Custom != nil {
		t.Fatalf("expected only Builtin set: %+v", builtin)
	}
	if builtin.Builtin.Secrets[FieldAPIKey] != "sk-test" {
		t.Errorf("unexpected secret: %q", builtin.Builtin.Secrets[FieldAPIKey])
	}

	custom := ResolvedModel{
		Purpose: PurposeVideo,
		Ref:     ModelRef{ProviderID: "custom-1", ModelID: "sora-2"},
		Model:   Model{ModelID: "sora-2", Capability: CapabilityVideo, Endpoint: EndpointOpenAIVideo, Enabled: true},
		Custom: &ResolvedCustom{
			Provider: CustomProvider{ID: "custom-1", Name: "Proxy", DiscoveryFormat: DiscoveryOpenAI, BaseURL: "https://proxy.example.com"},
			APIKey:   "ck-test",
		},
	}
	if custom.Custom == nil || custom.Builtin != nil {
		t.Fatalf("expected only Custom set: %+v", custom)
	}
	if custom.Custom.Provider.ID != "custom-1" || custom.Custom.APIKey != "ck-test" {
		t.Errorf("unexpected custom resolved: %+v", custom.Custom)
	}
}
