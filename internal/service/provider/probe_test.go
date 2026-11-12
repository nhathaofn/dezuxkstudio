package provider

import (
	"context"
	"crypto"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"app/internal/apperr"
	domain "app/internal/domain/provider"
)

const probeKey = "probe-key-ABCDEFGH-12345"

// testCred creates a credential whose base_url points at the test server and runs TestCredential.
func (e *env) testCred(t *testing.T, id string, base string, sec map[string]string) TestResult {
	t.Helper()
	var values map[string]string
	if base != "" {
		values = map[string]string{"base_url": base}
	}
	c := e.create(t, id, domain.Input{Values: values, Secrets: sec})
	res, err := e.svc.TestCredential(context.Background(), c.ID)
	if err != nil {
		t.Fatalf("TestCredential: %v", err)
	}
	return res
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func TestProbeOpenAI(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+probeKey {
			http.Error(w, "bad key "+r.Header.Get("Authorization"), http.StatusUnauthorized)
			return
		}
		writeJSON(w, 200, map[string]any{"data": []map[string]string{{"id": "gpt-4o"}, {"id": "foo"}, {"id": "dall-e-3"}}})
	}))
	defer srv.Close()

	e := newEnv(t, 2*time.Second)
	res := e.testCred(t, domain.IDOpenAI, srv.URL+"/v1", map[string]string{"api_key": probeKey})
	if !res.OK() || res.Status != 0 && res.Status != 200 {
		t.Fatalf("result = %+v", res)
	}
	if !slices.Equal(res.Models, []string{"dall-e-3", "gpt-4o"}) {
		t.Fatalf("models = %v", res.Models)
	}
}

func TestProbeOpenAIAuthFailedDoesNotLeakKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "invalid key "+strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "), http.StatusUnauthorized)
	}))
	defer srv.Close()

	e := newEnv(t, 2*time.Second)
	res := e.testCred(t, domain.IDOpenAI, srv.URL+"/v1", map[string]string{"api_key": probeKey})
	if res.Outcome != OutcomeAuthFailed || res.Status != 401 {
		t.Fatalf("result = %+v", res)
	}
	if strings.Contains(res.Detail, probeKey) {
		t.Fatalf("detail leaks key: %q", res.Detail)
	}
	if !strings.Contains(res.Detail, "[redacted]") {
		t.Fatalf("detail not redacted: %q", res.Detail)
	}
}

func TestProbeRateLimited(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "slow down", http.StatusTooManyRequests)
	}))
	defer srv.Close()
	e := newEnv(t, 2*time.Second)
	res := e.testCred(t, domain.IDOpenAI, srv.URL+"/v1", map[string]string{"api_key": probeKey})
	if res.Outcome != OutcomeRateLimited || res.Status != 429 {
		t.Fatalf("result = %+v", res)
	}
}

func TestProbeNotFoundAndHTTPError(t *testing.T) {
	status := http.StatusNotFound
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
	}))
	defer srv.Close()
	e := newEnv(t, 2*time.Second)
	res := e.testCred(t, domain.IDOpenAI, srv.URL+"/v1", map[string]string{"api_key": probeKey})
	if res.Outcome != OutcomeNotFound {
		t.Fatalf("result = %+v", res)
	}
	status = http.StatusInternalServerError
	res = e.testCred(t, domain.IDAgnes, srv.URL, map[string]string{"api_key": probeKey})
	if res.Outcome != OutcomeHTTPError || res.Status != 500 {
		t.Fatalf("result = %+v", res)
	}
}

func TestProbeNetworkError(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	base := srv.URL
	srv.Close()
	e := newEnv(t, 2*time.Second)
	res := e.testCred(t, domain.IDOpenAI, base+"/v1", map[string]string{"api_key": probeKey})
	if res.Outcome != OutcomeNetwork {
		t.Fatalf("result = %+v", res)
	}
}

func TestProbeTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(time.Second):
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	e := newEnv(t, 100*time.Millisecond)
	start := time.Now()
	res := e.testCred(t, domain.IDOpenAI, srv.URL+"/v1", map[string]string{"api_key": probeKey})
	if res.Outcome != OutcomeTimeout {
		t.Fatalf("result = %+v", res)
	}
	if time.Since(start) > 900*time.Millisecond {
		t.Fatalf("timeout not enforced, took %v", time.Since(start))
	}
}

func TestProbeCancelled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()
	e := newEnv(t, 5*time.Second)
	c := e.create(t, domain.IDOpenAI, domain.Input{Values: map[string]string{"base_url": srv.URL}, Secrets: map[string]string{"api_key": probeKey}})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := e.svc.TestCredential(ctx, c.ID)
	wantCode(t, err, apperr.CodeCancelled)
}

func TestProbeAnthropic(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("x-api-key") != probeKey || r.Header.Get("anthropic-version") == "" {
			http.Error(w, "unauthorized", 401)
			return
		}
		writeJSON(w, 200, map[string]any{"data": []map[string]string{{"id": "claude-b"}, {"id": "claude-a"}}})
	}))
	defer srv.Close()
	e := newEnv(t, 2*time.Second)
	res := e.testCred(t, domain.IDAnthropic, srv.URL, map[string]string{"api_key": probeKey})
	if !res.OK() || !slices.Equal(res.Models, []string{"claude-a", "claude-b"}) {
		t.Fatalf("result = %+v", res)
	}
}

func TestProbeAnthropicBearerRetry(t *testing.T) {
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("anthropic-version") == "" {
			t.Error("anthropic-version missing")
		}
		if r.Header.Get("x-api-key") != "" {
			calls = append(calls, "x-api-key")
			http.Error(w, "unauthorized", 401)
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+probeKey {
			calls = append(calls, "bad")
			http.Error(w, "unauthorized", 401)
			return
		}
		calls = append(calls, "bearer")
		writeJSON(w, 200, map[string]any{"data": []map[string]string{{"id": "m1"}}})
	}))
	defer srv.Close()
	e := newEnv(t, 2*time.Second)
	res := e.testCred(t, domain.IDAnthropic, srv.URL, map[string]string{"api_key": probeKey})
	if !res.OK() || !slices.Equal(res.Models, []string{"m1"}) {
		t.Fatalf("result = %+v", res)
	}
	if !slices.Equal(calls, []string{"x-api-key", "bearer"}) {
		t.Fatalf("calls = %v", calls)
	}
}

func verifyKlingJWT(t *testing.T, token, ak, sk string) {
	t.Helper()
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Errorf("token parts = %d", len(parts))
		return
	}
	mac := hmac.New(sha256.New, []byte(sk))
	mac.Write([]byte(parts[0] + "." + parts[1]))
	if base64.RawURLEncoding.EncodeToString(mac.Sum(nil)) != parts[2] {
		t.Error("bad HS256 signature")
	}
	hb, _ := base64.RawURLEncoding.DecodeString(parts[0])
	var h map[string]string
	_ = json.Unmarshal(hb, &h)
	if h["alg"] != "HS256" {
		t.Errorf("alg = %q", h["alg"])
	}
	cb, _ := base64.RawURLEncoding.DecodeString(parts[1])
	var c struct {
		Iss string `json:"iss"`
		Exp int64  `json:"exp"`
		Nbf int64  `json:"nbf"`
	}
	_ = json.Unmarshal(cb, &c)
	if c.Iss != ak {
		t.Errorf("iss = %q", c.Iss)
	}
	if d := c.Exp - c.Nbf; d < 1800 || d > 1810 {
		t.Errorf("exp-nbf = %d", d)
	}
	if now := time.Now().Unix(); c.Nbf > now || c.Exp < now {
		t.Errorf("token not currently valid: nbf=%d exp=%d now=%d", c.Nbf, c.Exp, now)
	}
}

func TestProbeKlingAKSK(t *testing.T) {
	const ak, sk = "ak-ABCDEFGH1234", "sk-ABCDEFGH5678"
	respCode := 0
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if r.URL.Path != "/account/costs" {
			http.NotFound(w, r)
			return
		}
		verifyKlingJWT(t, strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "), ak, sk)
		st, err1 := strconv.ParseInt(r.URL.Query().Get("start_time"), 10, 64)
		en, err2 := strconv.ParseInt(r.URL.Query().Get("end_time"), 10, 64)
		if err1 != nil || err2 != nil || en <= st {
			t.Errorf("bad time window: %v %v", r.URL.RawQuery, err1)
		}
		if respCode == 0 {
			writeJSON(w, 200, map[string]any{"code": 0, "message": "ok"})
			return
		}
		writeJSON(w, 200, map[string]any{"code": respCode, "message": "bad"})
	}))
	defer srv.Close()

	e := newEnv(t, 2*time.Second)
	res := e.testCred(t, domain.IDKling, srv.URL+"/v1", map[string]string{"access_key": ak, "secret_key": sk})
	if !res.OK() {
		t.Fatalf("result = %+v (path %s)", res, gotPath)
	}

	respCode = 1001
	res = e.testCred(t, domain.IDKling, srv.URL+"/v1", map[string]string{"access_key": ak, "secret_key": sk})
	if res.Outcome != OutcomeAuthFailed || !strings.Contains(res.Detail, "1001") {
		t.Fatalf("result = %+v", res)
	}
}

func TestProbeKlingAPIKeyUsedAsBearer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+probeKey {
			http.Error(w, "no", 401)
			return
		}
		writeJSON(w, 200, map[string]any{"code": 0})
	}))
	defer srv.Close()
	e := newEnv(t, 2*time.Second)
	res := e.testCred(t, domain.IDKling, srv.URL, map[string]string{"api_key": probeKey})
	if !res.OK() {
		t.Fatalf("result = %+v", res)
	}
}

func TestKlingJWTRequiresBothKeys(t *testing.T) {
	if _, err := klingJWT("ak", "", time.Now()); err == nil {
		t.Fatal("expected error")
	}
}

func TestProbeVidu(t *testing.T) {
	status := http.StatusNotFound
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/tasks/0/creations" || r.Header.Get("Authorization") != "Token "+probeKey {
			w.WriteHeader(http.StatusTeapot)
			return
		}
		w.WriteHeader(status)
	}))
	defer srv.Close()
	e := newEnv(t, 2*time.Second)
	if res := e.testCred(t, domain.IDVidu, srv.URL, map[string]string{"api_key": probeKey}); !res.OK() {
		t.Fatalf("404 should be OK: %+v", res)
	}
	status = http.StatusUnauthorized
	if res := e.testCred(t, domain.IDVidu, srv.URL, map[string]string{"api_key": probeKey}); res.Outcome != OutcomeAuthFailed {
		t.Fatalf("401: %+v", res)
	}
}

func TestProbeArk(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/contents/generations/tasks" || r.Header.Get("Authorization") != "Bearer "+probeKey {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		writeJSON(w, 200, map[string]any{"items": []any{}})
	}))
	defer srv.Close()
	e := newEnv(t, 2*time.Second)
	if res := e.testCred(t, domain.IDArk, srv.URL, map[string]string{"api_key": probeKey}); !res.OK() {
		t.Fatalf("result = %+v", res)
	}
}

func TestProbeDashScope(t *testing.T) {
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		if r.Header.Get("Authorization") != "Bearer "+probeKey {
			http.Error(w, "no", 401)
			return
		}
		writeJSON(w, 200, map[string]any{"data": []map[string]string{{"id": "qwen-max"}, {"id": "other"}, {"id": "wan2.1"}}})
	}))
	defer srv.Close()
	e := newEnv(t, 2*time.Second)
	res := e.testCred(t, domain.IDDashScope, srv.URL+"/api/v1", map[string]string{"api_key": probeKey})
	if !res.OK() || path != "/compatible-mode/v1/models" {
		t.Fatalf("result = %+v path=%s", res, path)
	}
	if !slices.Equal(res.Models, []string{"qwen-max", "wan2.1"}) {
		t.Fatalf("models = %v", res.Models)
	}
}

func TestProbeMiniMax(t *testing.T) {
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		writeJSON(w, 200, map[string]any{"data": []map[string]string{{"id": "MiniMax-M1"}, {"id": "zzz"}}})
	}))
	defer srv.Close()
	e := newEnv(t, 2*time.Second)
	res := e.testCred(t, domain.IDMiniMax, srv.URL+"/v1", map[string]string{"api_key": probeKey})
	if !res.OK() || path != "/v1/models" || !slices.Equal(res.Models, []string{"MiniMax-M1"}) {
		t.Fatalf("result = %+v path=%s", res, path)
	}
}

func TestProbeGeminiAIStudio(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1beta/models" || r.Header.Get("x-goog-api-key") != probeKey {
			http.Error(w, "no", 403)
			return
		}
		writeJSON(w, 200, map[string]any{"models": []map[string]string{
			{"name": "models/gemini-2.5-pro"}, {"name": "models/foo"}, {"name": "models/imagen-4"}}})
	}))
	defer srv.Close()
	e := newEnv(t, 2*time.Second)
	res := e.testCred(t, domain.IDGeminiAIStudio, srv.URL, map[string]string{"api_key": probeKey})
	if !res.OK() || !slices.Equal(res.Models, []string{"gemini-2.5-pro", "imagen-4"}) {
		t.Fatalf("result = %+v", res)
	}
}

func newServiceAccountJSON(t *testing.T, tokenURI string) (string, *rsa.PublicKey) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	pemKey := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	raw, _ := json.Marshal(map[string]string{
		"type": "service_account", "project_id": "proj-1", "client_email": "sa@proj-1.iam.test",
		"private_key": string(pemKey), "token_uri": tokenURI,
	})
	return string(raw), &key.PublicKey
}

func TestProbeGeminiVertex(t *testing.T) {
	status := 200
	var pub *rsa.PublicKey
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/token" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		if err := r.ParseForm(); err != nil {
			t.Error(err)
		}
		if r.PostForm.Get("grant_type") != "urn:ietf:params:oauth:grant-type:jwt-bearer" {
			t.Errorf("grant_type = %q", r.PostForm.Get("grant_type"))
		}
		parts := strings.Split(r.PostForm.Get("assertion"), ".")
		if len(parts) != 3 {
			t.Errorf("assertion parts = %d", len(parts))
			http.Error(w, "bad", 400)
			return
		}
		sig, _ := base64.RawURLEncoding.DecodeString(parts[2])
		sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
		if err := rsa.VerifyPKCS1v15(pub, crypto.SHA256, sum[:], sig); err != nil {
			t.Errorf("RS256 signature invalid: %v", err)
		}
		cb, _ := base64.RawURLEncoding.DecodeString(parts[1])
		var claims map[string]any
		_ = json.Unmarshal(cb, &claims)
		if claims["iss"] != "sa@proj-1.iam.test" || !strings.Contains(claims["scope"].(string), "cloud-platform") {
			t.Errorf("claims = %v", claims)
		}
		if status != 200 {
			http.Error(w, `{"error":"invalid_grant"}`, status)
			return
		}
		writeJSON(w, 200, map[string]string{"access_token": "tok"})
	}))
	defer srv.Close()

	var sa string
	sa, pub = newServiceAccountJSON(t, srv.URL+"/token")
	e := newEnv(t, 2*time.Second)
	res := e.testCred(t, domain.IDGeminiVertex, "", map[string]string{"credentials_file": sa})
	if !res.OK() {
		t.Fatalf("result = %+v", res)
	}
	status = 400
	res = e.testCred(t, domain.IDGeminiVertex, "", map[string]string{"credentials_file": sa})
	if res.Outcome != OutcomeAuthFailed {
		t.Fatalf("result = %+v", res)
	}
}

func TestVertexCredentialsValidatedOnCreate(t *testing.T) {
	e := newEnv(t, 2*time.Second)
	ctx := context.Background()
	for _, bad := range []string{"not json", `{"project_id":"p"}`, `{}`} {
		_, err := e.svc.CreateCredential(ctx, domain.IDGeminiVertex, domain.Input{Secrets: map[string]string{"credentials_file": bad}})
		wantCode(t, err, apperr.CodeValidation)
	}
	sa, _ := newServiceAccountJSON(t, "https://example.test/token")
	c := e.create(t, domain.IDGeminiVertex, domain.Input{Secrets: map[string]string{"credentials_file": sa}})
	if strings.Contains(c.SecretHints["credentials_file"], "BEGIN") {
		t.Fatal("hint leaks key material")
	}
}

func TestProbeVertexBadPrivateKey(t *testing.T) {
	e := newEnv(t, 2*time.Second)
	raw, _ := json.Marshal(map[string]string{"project_id": "p", "client_email": "a@b.test", "private_key": "not pem"})
	res := e.testCred(t, domain.IDGeminiVertex, "", map[string]string{"credentials_file": string(raw)})
	if res.Outcome != OutcomeInvalid {
		t.Fatalf("result = %+v", res)
	}
}

func TestCustomDiscoveryOpenAIAddsV1(t *testing.T) {
	var path, auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, auth = r.URL.Path, r.Header.Get("Authorization")
		writeJSON(w, 200, map[string]any{"data": []map[string]string{
			{"id": "gpt-4o"}, {"id": "tts-1"}, {"id": "sora-2"}, {"id": "gpt-image-1"}}})
	}))
	defer srv.Close()

	e := newEnv(t, 2*time.Second)
	ms, err := e.svc.DiscoverModels(context.Background(), DiscoverRequest{
		DiscoveryFormat: domain.DiscoveryOpenAI, BaseURL: srv.URL, APIKey: probeKey})
	if err != nil {
		t.Fatal(err)
	}
	if path != "/v1/models" || auth != "Bearer "+probeKey {
		t.Fatalf("path=%s auth=%s", path, auth)
	}
	want := map[string]domain.Endpoint{
		"gpt-4o": domain.EndpointOpenAIChat, "tts-1": domain.EndpointOpenAITTS,
		"sora-2": domain.EndpointOpenAIVideo, "gpt-image-1": domain.EndpointOpenAIImages,
	}
	if len(ms) != len(want) {
		t.Fatalf("models = %+v", ms)
	}
	for _, m := range ms {
		if want[m.ModelID] != m.Endpoint {
			t.Errorf("%s -> %s, want %s", m.ModelID, m.Endpoint, want[m.ModelID])
		}
	}

	// a base that already has a version suffix is used as is
	ms, err = e.svc.DiscoverModels(context.Background(), DiscoverRequest{
		DiscoveryFormat: domain.DiscoveryOpenAI, BaseURL: srv.URL + "/v2/", APIKey: probeKey})
	if err != nil || len(ms) == 0 || path != "/v2/models" {
		t.Fatalf("path=%s err=%v", path, err)
	}
}

func TestInferOpenAIEndpoint(t *testing.T) {
	cases := map[string]domain.Endpoint{
		"tts-1":            domain.EndpointOpenAITTS,
		"gpt-4o-mini-tts":  domain.EndpointOpenAITTS,
		"sora-2":           domain.EndpointOpenAIVideo,
		"kling-v2":         domain.EndpointOpenAIVideo,
		"gpt-image-1":      domain.EndpointOpenAIImages,
		"dall-e-3":         domain.EndpointOpenAIImages,
		"flux-dev":         domain.EndpointOpenAIImages,
		"gpt-4o":           domain.EndpointOpenAIChat,
		"claude-3-opus":    domain.EndpointOpenAIChat,
		"GPT-IMAGE-1":      domain.EndpointOpenAIImages,
		"text-embedding-3": domain.EndpointOpenAIChat,
	}
	for id, want := range cases {
		if got := InferOpenAIEndpoint(id); got != want {
			t.Errorf("%s: got %s want %s", id, got, want)
		}
	}
}

func TestDiscoverModelsFormats(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1beta/models":
			if r.Header.Get("x-goog-api-key") != probeKey {
				http.Error(w, "no", 403)
				return
			}
			writeJSON(w, 200, map[string]any{"models": []map[string]string{{"name": "models/gemini-pro"}, {"name": "models/gemini-image"}}})
		case "/v1/models":
			writeJSON(w, 200, map[string]any{"data": []map[string]string{{"id": "claude-x"}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	e := newEnv(t, 2*time.Second)
	ms, err := e.svc.DiscoverModels(context.Background(), DiscoverRequest{DiscoveryFormat: domain.DiscoveryGemini, BaseURL: srv.URL, APIKey: probeKey})
	if err != nil || len(ms) != 2 || ms[0].Endpoint != domain.EndpointGeminiGenerate || ms[1].Endpoint != domain.EndpointGeminiImage {
		t.Fatalf("gemini: %+v %v", ms, err)
	}
	ms, err = e.svc.DiscoverModels(context.Background(), DiscoverRequest{DiscoveryFormat: domain.DiscoveryAnthropic, BaseURL: srv.URL, APIKey: probeKey})
	if err != nil || len(ms) != 1 || ms[0].Endpoint != domain.EndpointAnthropicMessages {
		t.Fatalf("anthropic: %+v %v", ms, err)
	}
}

func TestDiscoverModelsErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "denied for "+strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "), http.StatusUnauthorized)
	}))
	defer srv.Close()
	e := newEnv(t, 2*time.Second)
	ctx := context.Background()

	_, err := e.svc.DiscoverModels(ctx, DiscoverRequest{DiscoveryFormat: domain.DiscoveryOpenAI, BaseURL: srv.URL, APIKey: probeKey})
	wantCode(t, err, apperr.CodeUnavailable)
	if strings.Contains(err.Error(), probeKey) {
		t.Fatalf("error leaks key: %v", err)
	}
	if detail(err, "outcome") != string(OutcomeAuthFailed) {
		t.Fatalf("details = %v", apperr.From(err).Details)
	}

	_, err = e.svc.DiscoverModels(ctx, DiscoverRequest{DiscoveryFormat: "bogus", BaseURL: srv.URL})
	wantCode(t, err, apperr.CodeValidation)
	_, err = e.svc.DiscoverModels(ctx, DiscoverRequest{DiscoveryFormat: domain.DiscoveryOpenAI, BaseURL: "nope"})
	wantCode(t, err, apperr.CodeValidation)
	_, err = e.svc.DiscoverModels(ctx, DiscoverRequest{CustomID: "missing", DiscoveryFormat: domain.DiscoveryOpenAI, BaseURL: srv.URL})
	wantCode(t, err, apperr.CodeNotFound)
}

func TestDiscoverModelsUsesStoredKey(t *testing.T) {
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		writeJSON(w, 200, map[string]any{"data": []map[string]string{{"id": "gpt-4o"}}})
	}))
	defer srv.Close()
	e := newEnv(t, 2*time.Second)
	ctx := context.Background()
	p, err := e.svc.CreateCustom(ctx, domain.CustomInput{
		Name: "gw", DiscoveryFormat: domain.DiscoveryOpenAI, BaseURL: srv.URL + "/v1", APIKey: probeKey})
	if err != nil {
		t.Fatal(err)
	}
	ms, err := e.svc.DiscoverModels(ctx, DiscoverRequest{CustomID: p.ID, DiscoveryFormat: domain.DiscoveryOpenAI, BaseURL: srv.URL + "/v1"})
	if err != nil || len(ms) != 1 {
		t.Fatalf("%+v %v", ms, err)
	}
	if auth != "Bearer "+probeKey {
		t.Fatalf("auth = %q", auth)
	}

	// an explicit key wins over the stored one
	_, err = e.svc.DiscoverModels(ctx, DiscoverRequest{CustomID: p.ID, DiscoveryFormat: domain.DiscoveryOpenAI, BaseURL: srv.URL + "/v1", APIKey: "other-key-123456"})
	if err != nil || auth != "Bearer other-key-123456" {
		t.Fatalf("auth = %q err=%v", auth, err)
	}

	// TestCustom uses the stored key too
	res, err := e.svc.TestCustom(ctx, p.ID)
	if err != nil || !res.OK() || !slices.Equal(res.Models, []string{"gpt-4o"}) || auth != "Bearer "+probeKey {
		t.Fatalf("TestCustom = %+v %v auth=%q", res, err, auth)
	}
}

func TestOutcomesListIsComplete(t *testing.T) {
	if len(domain.Outcomes()) != 8 {
		t.Fatalf("outcomes = %v", domain.Outcomes())
	}
}
