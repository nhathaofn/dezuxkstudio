package mediagen

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"app/internal/apperr"
	"app/internal/config"
	domainmedia "app/internal/domain/mediagen"
	domainprovider "app/internal/domain/provider"
)

func TestGenerateTextAllProtocols(t *testing.T) {
	schema := json.RawMessage(`{
		"type": "object",
		"additionalProperties": false,
		"required": ["title", "scenes"],
		"properties": {
			"title": {"type": "string"},
			"scenes": {"type": "array", "items": {"type": "string"}}
		}
	}`)

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/v1/chat/completions":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			rf, _ := body["response_format"].(map[string]any)
			if rf != nil && rf["type"] == "json_schema" && body["model"] == "custom-fallback" {
				// Simulate a custom OpenAI-compatible gateway that rejects strict json_schema
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":{"message":"json_schema not supported"}}`))
				return
			}
			_, _ = w.Write([]byte("{\"choices\":[{\"message\":{\"content\":\"```json\\n{\\\"title\\\":\\\"Test\\\",\\\"scenes\\\":[\\\"S1\\\"]}\\n```\"}}],\"usage\":{\"prompt_tokens\":12,\"completion_tokens\":24}}"))

		case r.URL.Path == "/v1/responses":
			_, _ = w.Write([]byte(`{"output_text":"{\"title\":\"Resp\",\"scenes\":[\"S1\"]}","usage":{"input_tokens":10,"output_tokens":15}}`))

		case r.URL.Path == "/v1beta/models/gemini-2.5-flash:generateContent":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			gc, _ := body["generationConfig"].(map[string]any)
			if rs, ok := gc["responseSchema"].(map[string]any); ok {
				if _, hasAdd := rs["additionalProperties"]; hasAdd {
					t.Fatalf("expected additionalProperties stripped from Gemini responseSchema")
				}
			}
			_, _ = w.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":"thinking...", "thought":true},{"text":"{\"title\":\"Gem\",\"scenes\":[\"S1\"]}"}]}}],"usageMetadata":{"promptTokenCount":8,"candidatesTokenCount":16}}`))

		case r.URL.Path == "/v1beta1/projects/test-proj/locations/global/publishers/google/models/gemini-2.5-pro:generateContent":
			_, _ = w.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":"{\"title\":\"Vtx\",\"scenes\":[\"S1\"]}"}]}}]}`))

		case r.URL.Path == "/v1/messages":
			if r.Header.Get("x-api-key") != "ant-key" {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte(`{"content":[{"type":"tool_use","id":"t1","name":"script","input":{"title":"Ant","scenes":["S1"]}}],"usage":{"input_tokens":20,"output_tokens":30}}`))

		case r.URL.Path == "/v1/text/chatcompletion_v2":
			_, _ = w.Write([]byte(`{"base_resp":{"status_code":0},"choices":[{"message":{"content":"{\"title\":\"MM\",\"scenes\":[\"S1\"]}"}}],"usage":{"prompt_tokens":5,"completion_tokens":7}}`))

		case r.URL.Path == "/api/v3/chat/completions":
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"title\":\"Ark\",\"scenes\":[\"S1\"]}"}}]}`))

		case r.URL.Path == "/compatible-mode/v1/chat/completions":
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"title\":\"Dash\",\"scenes\":[\"S1\"]}"}}]}`))

		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	resolver := &fakeResolver{
		resolveFn: func(_ context.Context, p domainprovider.Purpose, ref string) (domainprovider.ResolvedModel, error) {
			if p != domainprovider.PurposeText {
				t.Fatalf("unexpected purpose %s", p)
			}
			switch ref {
			case "openai/gpt-4o":
				return domainprovider.ResolvedModel{
					Purpose: p,
					Model:   domainprovider.Model{ModelID: "gpt-4o", Capability: domainprovider.CapabilityLLM},
					Builtin: &domainprovider.Resolved{ProviderID: domainprovider.IDOpenAI, BaseURL: srv.URL + "/v1", Secrets: map[string]string{"api_key": "oa-key"}},
				}, nil
			case "custom/fallback":
				return domainprovider.ResolvedModel{
					Purpose: p,
					Model:   domainprovider.Model{ModelID: "custom-fallback", Capability: domainprovider.CapabilityLLM, Endpoint: domainprovider.EndpointOpenAIChat},
					Custom:  &domainprovider.ResolvedCustom{Provider: domainprovider.CustomProvider{ID: "c1", Name: "Custom", DiscoveryFormat: domainprovider.DiscoveryOpenAI, BaseURL: srv.URL + "/v1"}, APIKey: "c-key"},
				}, nil
			case "custom/responses":
				return domainprovider.ResolvedModel{
					Purpose: p,
					Model:   domainprovider.Model{ModelID: "gpt-4.1", Capability: domainprovider.CapabilityLLM, Endpoint: domainprovider.EndpointOpenAIResponses},
					Custom:  &domainprovider.ResolvedCustom{Provider: domainprovider.CustomProvider{ID: "c2", Name: "CustomResp", DiscoveryFormat: domainprovider.DiscoveryOpenAI, BaseURL: srv.URL + "/v1"}, APIKey: "c-key"},
				}, nil
			case "gemini/flash":
				return domainprovider.ResolvedModel{
					Purpose: p,
					Model:   domainprovider.Model{ModelID: "gemini-2.5-flash", Capability: domainprovider.CapabilityLLM},
					Builtin: &domainprovider.Resolved{ProviderID: domainprovider.IDGeminiAIStudio, BaseURL: srv.URL, Secrets: map[string]string{"api_key": "gem-key"}},
				}, nil
			case "vertex/pro":
				return domainprovider.ResolvedModel{
					Purpose: p,
					Model:   domainprovider.Model{ModelID: "gemini-2.5-pro", Capability: domainprovider.CapabilityLLM},
					Builtin: &domainprovider.Resolved{ProviderID: domainprovider.IDGeminiVertex, BaseURL: srv.URL, Secrets: map[string]string{"credentials_file": "{}"}},
				}, nil
			case "anthropic/sonnet":
				return domainprovider.ResolvedModel{
					Purpose: p,
					Model:   domainprovider.Model{ModelID: "claude-sonnet-4", Capability: domainprovider.CapabilityLLM},
					Builtin: &domainprovider.Resolved{ProviderID: domainprovider.IDAnthropic, BaseURL: srv.URL, Secrets: map[string]string{"api_key": "ant-key"}},
				}, nil
			case "minimax/m1":
				return domainprovider.ResolvedModel{
					Purpose: p,
					Model:   domainprovider.Model{ModelID: "MiniMax-Text-01", Capability: domainprovider.CapabilityLLM},
					Builtin: &domainprovider.Resolved{ProviderID: domainprovider.IDMiniMax, BaseURL: srv.URL + "/v1", Secrets: map[string]string{"api_key": "mm-key"}},
				}, nil
			case "ark/doubao":
				return domainprovider.ResolvedModel{
					Purpose: p,
					Model:   domainprovider.Model{ModelID: "doubao-pro", Capability: domainprovider.CapabilityLLM},
					Builtin: &domainprovider.Resolved{ProviderID: domainprovider.IDArk, BaseURL: srv.URL + "/api/v3", Secrets: map[string]string{"api_key": "ark-key"}},
				}, nil
			case "dashscope/qwen":
				return domainprovider.ResolvedModel{
					Purpose: p,
					Model:   domainprovider.Model{ModelID: "qwen-max", Capability: domainprovider.CapabilityLLM},
					Builtin: &domainprovider.Resolved{ProviderID: domainprovider.IDDashScope, BaseURL: srv.URL, Secrets: map[string]string{"api_key": "ds-key"}},
				}, nil
			default:
				t.Fatalf("unexpected modelRef %q", ref)
				return domainprovider.ResolvedModel{}, nil
			}
		},
	}

	svc := New(Options{
		Providers: resolver,
		MediaDir:  t.TempDir(),
		Config: config.MediaGenConfig{
			RequestTimeout:  5 * time.Second,
			TextTimeout:     5 * time.Second,
			TextMaxTokens:   2048,
			TextTemperature: 0.4,
		},
	})

	refs := []string{
		"openai/gpt-4o",
		"custom/fallback",
		"custom/responses",
		"gemini/flash",
		"vertex/pro",
		"anthropic/sonnet",
		"minimax/m1",
		"ark/doubao",
		"dashscope/qwen",
	}
	for _, ref := range refs {
		res, err := svc.GenerateText(context.Background(), domainmedia.TextRequest{
			ModelRef:       ref,
			SystemPrompt:   "You are a screenplay analyzer.",
			UserPrompt:     "Normalize this script",
			JSONSchemaName: "script",
			JSONSchema:     schema,
		})
		if err != nil {
			t.Fatalf("%s failed: %v", ref, err)
		}
		if !json.Valid(res.ParsedJSON) || !strings.Contains(res.Text, `"scenes"`) {
			t.Fatalf("%s returned invalid parsed json: %s", ref, res.Text)
		}
	}
}

func TestGenerateTextRejectsMissingRequiredSchemaKeys(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"title\":\"OnlyTitle\"}"}}]}`))
	}))
	defer srv.Close()

	resolver := &fakeResolver{
		resolveFn: func(_ context.Context, p domainprovider.Purpose, _ string) (domainprovider.ResolvedModel, error) {
			return domainprovider.ResolvedModel{
				Purpose: p,
				Model:   domainprovider.Model{ModelID: "gpt-4o", Capability: domainprovider.CapabilityLLM},
				Builtin: &domainprovider.Resolved{ProviderID: domainprovider.IDOpenAI, BaseURL: srv.URL + "/v1", Secrets: map[string]string{"api_key": "k"}},
			}, nil
		},
	}
	svc, _ := testService(t, resolver)
	_, err := svc.GenerateText(context.Background(), domainmedia.TextRequest{
		UserPrompt: "test",
		JSONSchema: json.RawMessage(`{"type":"object","required":["title","scenes"]}`),
	})
	if err == nil || !strings.Contains(err.Error(), "missing \"scenes\"") {
		t.Fatalf("expected missing required field error, got %v", err)
	}
	if ae := apperr.From(err); strings.Contains(ae.Message, "missing \"scenes\"") {
		t.Fatalf("apperr.Message must not include raw Go cause: %q", ae.Message)
	}
}

func TestGenerateTextMultimodalAnthropicBearerRetryAndTimeout(t *testing.T) {
	imgPath := writeTempPNG(t)
	var sawMultimodalImage bool
	var sawAnthropicBearer bool

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/chat/completions":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			msgs, _ := body["messages"].([]any)
			if len(msgs) > 0 {
				m0, _ := msgs[len(msgs)-1].(map[string]any)
				if parts, ok := m0["content"].([]any); ok && len(parts) >= 2 {
					sawMultimodalImage = true
				}
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"frame ok"}}]}`))
		case "/v1/messages":
			if r.Header.Get("Authorization") != "Bearer ant-oauth-tok" {
				http.Error(w, `{"error":{"message":"x-api-key rejected"}}`, http.StatusUnauthorized)
				return
			}
			sawAnthropicBearer = true
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"bearer ok"}],"usage":{"input_tokens":4,"output_tokens":2}}`))
		case "/slow/v1/chat/completions":
			select {
			case <-r.Context().Done():
				return
			case <-time.After(200 * time.Millisecond):
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"late"}}]}`))
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	resolver := &fakeResolver{
		resolveFn: func(_ context.Context, p domainprovider.Purpose, ref string) (domainprovider.ResolvedModel, error) {
			switch ref {
			case "anthropic/bearer":
				return domainprovider.ResolvedModel{
					Purpose: p,
					Model:   domainprovider.Model{ModelID: "claude-sonnet-4", Capability: domainprovider.CapabilityLLM},
					Builtin: &domainprovider.Resolved{ProviderID: domainprovider.IDAnthropic, BaseURL: srv.URL, Secrets: map[string]string{"api_key": "ant-oauth-tok"}},
				}, nil
			case "openai/slow":
				return domainprovider.ResolvedModel{
					Purpose: p,
					Model:   domainprovider.Model{ModelID: "gpt-4o", Capability: domainprovider.CapabilityLLM},
					Builtin: &domainprovider.Resolved{ProviderID: domainprovider.IDOpenAI, BaseURL: srv.URL + "/slow/v1", Secrets: map[string]string{"api_key": "k"}},
				}, nil
			default:
				return domainprovider.ResolvedModel{
					Purpose: p,
					Model:   domainprovider.Model{ModelID: "gpt-4o", Capability: domainprovider.CapabilityLLM},
					Builtin: &domainprovider.Resolved{ProviderID: domainprovider.IDOpenAI, BaseURL: srv.URL + "/v1", Secrets: map[string]string{"api_key": "k"}},
				}, nil
			}
		},
	}

	svc := New(Options{
		Providers: resolver,
		MediaDir:  t.TempDir(),
		Config: config.MediaGenConfig{
			TextTimeout:    35 * time.Millisecond,
			RequestTimeout: 35 * time.Millisecond,
			RetryMax:       0,
		},
	})

	// 1. Multimodal ImageInputs
	res, err := svc.GenerateText(context.Background(), domainmedia.TextRequest{
		ModelRef:    "openai/gpt-4o",
		UserPrompt:  "Inspect frame",
		ImageInputs: []string{imgPath},
	})
	if err != nil || !sawMultimodalImage || res.Text != "frame ok" {
		t.Fatalf("multimodal GenerateText failed: sawImage=%v res=%+v err=%v", sawMultimodalImage, res, err)
	}

	// 2. Anthropic 401 -> Bearer fallback
	resAnt, err := svc.GenerateText(context.Background(), domainmedia.TextRequest{
		ModelRef:    "anthropic/bearer",
		UserPrompt:  "Inspect frame",
		ImageInputs: []string{imgPath},
	})
	if err != nil || !sawAnthropicBearer || resAnt.Text != "bearer ok" {
		t.Fatalf("Anthropic Bearer fallback failed: sawBearer=%v res=%+v err=%v", sawAnthropicBearer, resAnt, err)
	}

	// 3. TextTimeout deadline
	if _, err := svc.GenerateText(context.Background(), domainmedia.TextRequest{
		ModelRef:   "openai/slow",
		UserPrompt: "Slow call",
	}); !apperr.IsCode(err, apperr.CodeTimeout) {
		t.Fatalf("expected CodeTimeout on TextTimeout expiry, got %v", err)
	}
}
