package gateway_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"app/internal/domain/preferences"
	"app/internal/engine"
	"app/internal/secrets"
	"app/internal/tools/gateway"
)

func TestNode_Spec(t *testing.T) {
	node := gateway.NewNode(gateway.Options{})
	spec := node.Spec()

	if spec.Type != gateway.NodeType {
		t.Fatalf("expected type %s, got %s", gateway.NodeType, spec.Type)
	}
	if spec.Provider != "tool" {
		t.Fatalf("expected provider tool, got %s", spec.Provider)
	}

	// Verify Inputs
	hasPrompt := false
	hasNegPrompt := false
	for _, in := range spec.Inputs {
		if in.Name == "prompt" {
			hasPrompt = true
			if in.Type != engine.PortText || in.Optional {
				t.Errorf("prompt port should be required PortText")
			}
		}
		if in.Name == "negative_prompt" {
			hasNegPrompt = true
			if in.Type != engine.PortText || !in.Optional {
				t.Errorf("negative_prompt port should be optional PortText")
			}
		}
	}
	if !hasPrompt || !hasNegPrompt {
		t.Fatalf("missing required input ports: prompt=%v, negPrompt=%v", hasPrompt, hasNegPrompt)
	}

	// Verify Outputs
	hasImage := false
	for _, out := range spec.Outputs {
		if out.Name == "image" {
			hasImage = true
			if out.Type != engine.PortImage {
				t.Errorf("image output port should be PortImage")
			}
		}
	}
	if !hasImage {
		t.Fatalf("missing image output port")
	}

	// Verify Params
	hasAspectRatio := false
	hasGatewayURL := false
	for _, p := range spec.Params {
		if p.Name == "aspect_ratio" && p.Kind == engine.ParamSelect {
			hasAspectRatio = true
		}
		if p.Name == "gateway_url" && p.Kind == engine.ParamText {
			hasGatewayURL = true
		}
	}
	if !hasAspectRatio || !hasGatewayURL {
		t.Fatalf("missing required params: aspect_ratio=%v, gateway_url=%v", hasAspectRatio, hasGatewayURL)
	}
}

func TestNode_B64Generation(t *testing.T) {
	sampleImageData := []byte("FAKE_PNG_BINARY_DATA_FOR_TESTING")
	b64Image := base64.StdEncoding.EncodeToString(sampleImageData)

	var authHeader string
	var reqBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/images/generations" {
			http.NotFound(w, r)
			return
		}
		authHeader = r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&reqBody)

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(gateway.GenerationResponse{
			Created:  1730000000,
			Provider: "flow",
			Data: []gateway.GenerationItem{
				{B64JSON: b64Image},
			},
		})
	}))
	defer srv.Close()

	mediaDir := t.TempDir()
	node := gateway.NewNode(gateway.Options{
		MediaDir:      mediaDir,
		GatewayURL:    srv.URL,
		GatewayAPIKey: "secret-token",
		HTTPClient:    srv.Client(),
	})

	input := json.RawMessage(`{
		"prompt": "a beautiful digital sunset",
		"negative_prompt": "blurry, low quality",
		"aspect_ratio": "16:9"
	}`)

	res, err := node.Run(context.Background(), input)
	if err != nil {
		t.Fatalf("node.Run failed: %v", err)
	}

	out, ok := res.(gateway.NodeOutput)
	if !ok {
		t.Fatalf("expected gateway.NodeOutput, got %T", res)
	}

	if out.Image.Kind != engine.MediaImage {
		t.Fatalf("expected media kind image, got %s", out.Image.Kind)
	}
	if !filepath.IsAbs(out.Image.LocalURL) {
		t.Fatalf("expected absolute path, got %s", out.Image.LocalURL)
	}

	// Verify written file exists and content matches
	data, err := os.ReadFile(out.Image.LocalURL)
	if err != nil {
		t.Fatalf("failed to read generated image file: %v", err)
	}
	if string(data) != string(sampleImageData) {
		t.Fatalf("file content mismatch, expected %s, got %s", sampleImageData, data)
	}

	// Verify request headers and payload
	if authHeader != "Bearer secret-token" {
		t.Fatalf("expected Authorization header 'Bearer secret-token', got %q", authHeader)
	}
	if reqBody["prompt"] != "a beautiful digital sunset" {
		t.Fatalf("unexpected prompt in request body: %v", reqBody["prompt"])
	}
	if reqBody["negative_prompt"] != "blurry, low quality" {
		t.Fatalf("unexpected negative_prompt in request body: %v", reqBody["negative_prompt"])
	}
	if reqBody["aspect_ratio"] != "16:9" {
		t.Fatalf("unexpected aspect_ratio in request body: %v", reqBody["aspect_ratio"])
	}
}

func TestNode_URLGeneration(t *testing.T) {
	sampleImageData := []byte("ANOTHER_PNG_BINARY_FOR_DOWNLOAD")

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/images/generations":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(gateway.GenerationResponse{
				Created:  1730000000,
				Provider: "flow",
				Data: []gateway.GenerationItem{
					{URL: srv.URL + "/download/img123.png"},
				},
			})
		case "/download/img123.png":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(sampleImageData)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	mediaDir := t.TempDir()
	node := gateway.NewNode(gateway.Options{
		MediaDir:   mediaDir,
		GatewayURL: srv.URL,
		HTTPClient: srv.Client(),
	})

	input := json.RawMessage(`{"prompt": "fantasy landscape", "aspect_ratio": "1:1"}`)
	res, err := node.Run(context.Background(), input)
	if err != nil {
		t.Fatalf("node.Run failed: %v", err)
	}

	out, ok := res.(gateway.NodeOutput)
	if !ok {
		t.Fatalf("expected NodeOutput, got %T", res)
	}

	data, err := os.ReadFile(out.Image.LocalURL)
	if err != nil {
		t.Fatalf("failed to read downloaded image: %v", err)
	}
	if string(data) != string(sampleImageData) {
		t.Fatalf("image content mismatch: expected %s, got %s", sampleImageData, data)
	}
}

func TestNode_InputValidation(t *testing.T) {
	node := gateway.NewNode(gateway.Options{MediaDir: t.TempDir()})

	// Empty prompt
	_, err := node.Run(context.Background(), json.RawMessage(`{"prompt": ""}`))
	if err == nil {
		t.Fatalf("expected error for empty prompt")
	}

	// Whitespace prompt
	_, err = node.Run(context.Background(), json.RawMessage(`{"prompt": "    "}`))
	if err == nil {
		t.Fatalf("expected error for whitespace prompt")
	}
}

func TestNode_AuthHeaderVerification(t *testing.T) {
	var capturedAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAuth = r.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode(gateway.GenerationResponse{
			Data: []gateway.GenerationItem{
				{B64JSON: base64.StdEncoding.EncodeToString([]byte("TEST"))},
			},
		})
	}))
	defer srv.Close()

	// 1. With API key
	nodeWithKey := gateway.NewNode(gateway.Options{
		MediaDir:      t.TempDir(),
		GatewayURL:    srv.URL,
		GatewayAPIKey: "my-custom-key",
		HTTPClient:    srv.Client(),
	})
	_, err := nodeWithKey.Run(context.Background(), json.RawMessage(`{"prompt": "test"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if capturedAuth != "Bearer my-custom-key" {
		t.Fatalf("expected Bearer my-custom-key, got %q", capturedAuth)
	}

	// 2. Without API key
	capturedAuth = ""
	nodeNoKey := gateway.NewNode(gateway.Options{
		MediaDir:   t.TempDir(),
		GatewayURL: srv.URL,
		HTTPClient: srv.Client(),
	})
	_, err = nodeNoKey.Run(context.Background(), json.RawMessage(`{"prompt": "test"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if capturedAuth != "" {
		t.Fatalf("expected empty Authorization header, got %q", capturedAuth)
	}
}

func TestNode_AspectRatioSelection(t *testing.T) {
	var capturedAspectRatio string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		if ar, ok := req["aspect_ratio"].(string); ok {
			capturedAspectRatio = ar
		}
		_ = json.NewEncoder(w).Encode(gateway.GenerationResponse{
			Data: []gateway.GenerationItem{
				{B64JSON: base64.StdEncoding.EncodeToString([]byte("AR_TEST"))},
			},
		})
	}))
	defer srv.Close()

	node := gateway.NewNode(gateway.Options{
		MediaDir:   t.TempDir(),
		GatewayURL: srv.URL,
		HTTPClient: srv.Client(),
	})

	for _, ar := range []string{"16:9", "1:1", "9:16"} {
		capturedAspectRatio = ""
		input, _ := json.Marshal(map[string]string{
			"prompt":       "test",
			"aspect_ratio": ar,
		})
		_, err := node.Run(context.Background(), input)
		if err != nil {
			t.Fatalf("Run failed for AR %s: %v", ar, err)
		}
		if capturedAspectRatio != ar {
			t.Fatalf("expected aspect ratio %s, got %s", ar, capturedAspectRatio)
		}
	}
}

func TestNode_ErrorResponses(t *testing.T) {
	var statusToReturn int32 = http.StatusInternalServerError

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		st := int(atomic.LoadInt32(&statusToReturn))
		if st != http.StatusOK {
			http.Error(w, `{"error": "server error"}`, st)
			return
		}
		// Return invalid JSON if status is OK
		_, _ = w.Write([]byte(`{"invalid-json"`))
	}))
	defer srv.Close()

	node := gateway.NewNode(gateway.Options{
		MediaDir:   t.TempDir(),
		GatewayURL: srv.URL,
		HTTPClient: srv.Client(),
	})

	// 1. HTTP 500
	atomic.StoreInt32(&statusToReturn, http.StatusInternalServerError)
	_, err := node.Run(context.Background(), json.RawMessage(`{"prompt": "test"}`))
	if err == nil || !strings.Contains(err.Error(), "HTTP 500") {
		t.Fatalf("expected HTTP 500 error, got: %v", err)
	}

	// 2. HTTP 401 Unauthorized
	atomic.StoreInt32(&statusToReturn, http.StatusUnauthorized)
	_, err = node.Run(context.Background(), json.RawMessage(`{"prompt": "test"}`))
	if err == nil || !strings.Contains(err.Error(), "HTTP 401") {
		t.Fatalf("expected HTTP 401 error, got: %v", err)
	}

	// 3. Invalid JSON response
	atomic.StoreInt32(&statusToReturn, http.StatusOK)
	_, err = node.Run(context.Background(), json.RawMessage(`{"prompt": "test"}`))
	if err == nil || !strings.Contains(err.Error(), "parse gateway response") {
		t.Fatalf("expected parse response error, got: %v", err)
	}
}

func TestNode_PreferencesFallback(t *testing.T) {
	var capturedAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAuth = r.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode(gateway.GenerationResponse{
			Data: []gateway.GenerationItem{
				{B64JSON: base64.StdEncoding.EncodeToString([]byte("PREF_TEST"))},
			},
		})
	}))
	defer srv.Close()

	memSec := secrets.NewMemory()
	_ = memSec.Set(secrets.KeyGatewayAPIKey, "key-from-secrets")

	prefCalled := false
	node := gateway.NewNode(gateway.Options{
		MediaDir:   t.TempDir(),
		HTTPClient: srv.Client(),
		Secrets:    memSec,
		GetPreferences: func(ctx context.Context) (preferences.Preferences, error) {
			prefCalled = true
			return preferences.Preferences{
				GatewayURL: srv.URL,
			}, nil
		},
	})

	_, err := node.Run(context.Background(), json.RawMessage(`{"prompt": "preferences test"}`))
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if !prefCalled {
		t.Fatalf("expected GetPreferences callback to be invoked")
	}
	if capturedAuth != "Bearer key-from-secrets" {
		t.Fatalf("expected Bearer key-from-secrets, got %q", capturedAuth)
	}
}

func TestNode_SSRFDefense(t *testing.T) {
	var officialAuth string
	officialSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		officialAuth = r.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode(gateway.GenerationResponse{
			Data: []gateway.GenerationItem{
				{B64JSON: base64.StdEncoding.EncodeToString([]byte("OFFICIAL"))},
			},
		})
	}))
	defer officialSrv.Close()

	var attackerAuth string
	attackerSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attackerAuth = r.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode(gateway.GenerationResponse{
			Data: []gateway.GenerationItem{
				{B64JSON: base64.StdEncoding.EncodeToString([]byte("ATTACKER"))},
			},
		})
	}))
	defer attackerSrv.Close()

	memSec := secrets.NewMemory()
	_ = memSec.Set(secrets.KeyGatewayAPIKey, "super-secret-gateway-token")

	node := gateway.NewNode(gateway.Options{
		MediaDir:   t.TempDir(),
		GatewayURL: officialSrv.URL,
		HTTPClient: attackerSrv.Client(),
		Secrets:    memSec,
	})

	// Case 1: Untrusted node input specifies an external/attacker GatewayURL
	attackerPayload, _ := json.Marshal(map[string]string{
		"prompt":      "generate evil image",
		"gateway_url": attackerSrv.URL,
	})

	_, err := node.Run(context.Background(), attackerPayload)
	if err != nil {
		t.Fatalf("run to attacker URL failed: %v", err)
	}

	// CRITICAL SECURITY ASSERTION: Bearer token must NOT be leaked to the external server!
	if attackerAuth != "" {
		t.Fatalf("SSRF VULNERABILITY: Bearer token was leaked to external server: %q", attackerAuth)
	}

	// Case 2: Standard execution to official gateway url receives the Bearer token
	officialPayload, _ := json.Marshal(map[string]string{
		"prompt": "generate official image",
	})
	_, err = node.Run(context.Background(), officialPayload)
	if err != nil {
		t.Fatalf("run to official URL failed: %v", err)
	}
	if officialAuth != "Bearer super-secret-gateway-token" {
		t.Fatalf("expected official server to receive token, got: %q", officialAuth)
	}
}

func TestNode_SchemeValidation(t *testing.T) {
	node := gateway.NewNode(gateway.Options{MediaDir: t.TempDir()})

	for _, badURL := range []string{
		"ftp://malicious.com/endpoint",
		"file:///etc/passwd",
		"javascript:alert(1)",
		"gopher://evil.local:70",
		"not-even-a-url",
	} {
		payload, _ := json.Marshal(map[string]string{
			"prompt":      "test",
			"gateway_url": badURL,
		})
		_, err := node.Run(context.Background(), payload)
		if err == nil {
			t.Fatalf("expected scheme validation error for %q, got nil", badURL)
		}
	}
}
