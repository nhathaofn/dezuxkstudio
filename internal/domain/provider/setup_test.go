package provider

import "testing"

func TestPlanSetup(t *testing.T) {
	cases := []struct {
		name       string
		req        SetupRequest
		wantErr    bool
		kind       SetupKind
		providerID string
		baseURL    string
		plName     string
		format     DiscoveryFormat
	}{
		{name: "openai default base", req: SetupRequest{APIKey: "sk-x", BaseURL: "https://api.openai.com/v1"}, kind: SetupBuiltin, providerID: IDOpenAI},
		{name: "openai trailing slash", req: SetupRequest{APIKey: "sk-x", BaseURL: "https://api.openai.com/v1/"}, kind: SetupBuiltin, providerID: IDOpenAI},
		{name: "minimax", req: SetupRequest{APIKey: "k", BaseURL: "https://api.minimax.io/v1"}, kind: SetupBuiltin, providerID: IDMiniMax, baseURL: "https://api.minimax.io/v1"},
		{name: "ark agent plan", req: SetupRequest{APIKey: "k", BaseURL: "https://ark.cn-beijing.volces.com/api/plan/v3"}, kind: SetupBuiltin, providerID: IDArkAgentPlan},
		{name: "anthropic key", req: SetupRequest{APIKey: "sk-ant-abc"}, kind: SetupBuiltin, providerID: IDAnthropic},
		{name: "gemini key", req: SetupRequest{APIKey: "AIzaXYZ"}, kind: SetupBuiltin, providerID: IDGeminiAIStudio},
		{name: "grok key", req: SetupRequest{APIKey: "xai-abc"}, kind: SetupBuiltin, providerID: IDGrok},
		{name: "kling secrets", req: SetupRequest{Secrets: map[string]string{FieldAccessKey: "a", FieldSecretKey: "b"}}, kind: SetupBuiltin, providerID: IDKling},
		{name: "plain key no base", req: SetupRequest{APIKey: "sk-xxx"}, wantErr: true},
		{name: "local ollama", req: SetupRequest{APIKey: "k", BaseURL: "http://localhost:11434/v1"}, kind: SetupCustom, plName: "localhost", baseURL: "http://localhost:11434/v1", format: DiscoveryOpenAI},
		{name: "custom anthropic", req: SetupRequest{APIKey: "k", BaseURL: "https://foo.example/anthropic"}, kind: SetupCustom, plName: "foo.example", baseURL: "https://foo.example/anthropic", format: DiscoveryAnthropic},
		{name: "custom gemini", req: SetupRequest{APIKey: "k", BaseURL: "https://x.example/v1beta"}, kind: SetupCustom, plName: "x.example", baseURL: "https://x.example/v1beta", format: DiscoveryGemini},
		{name: "custom explicit name and format", req: SetupRequest{APIKey: "k", Name: "Mine", BaseURL: "https://x.example/api", DiscoveryFormat: DiscoveryGemini}, kind: SetupCustom, plName: "Mine", baseURL: "https://x.example/api", format: DiscoveryGemini},
		{name: "unknown forced id", req: SetupRequest{ProviderID: "nope", APIKey: "k"}, wantErr: true},
		{name: "forced grok with base", req: SetupRequest{ProviderID: IDGrok, APIKey: "k", BaseURL: "https://proxy.example/v1"}, kind: SetupBuiltin, providerID: IDGrok},
		{name: "invalid url", req: SetupRequest{APIKey: "k", BaseURL: "ftp://x"}, wantErr: true},
		{name: "garbage url", req: SetupRequest{APIKey: "k", BaseURL: "not a url"}, wantErr: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p, err := PlanSetup(c.req)
			if c.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %+v", p)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if p.Kind != c.kind || p.ProviderID != c.providerID || p.BaseURL != c.baseURL {
				t.Fatalf("plan = %+v", p)
			}
			if c.kind == SetupCustom && (p.Name != c.plName || p.DiscoveryFormat != c.format) {
				t.Fatalf("custom plan = %+v", p)
			}
			if c.kind == SetupBuiltin && p.Name == "" {
				t.Fatal("builtin plan must have a name")
			}
		})
	}
}
