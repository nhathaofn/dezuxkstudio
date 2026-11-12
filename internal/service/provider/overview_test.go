package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	domain "app/internal/domain/provider"
	"app/internal/secrets"
)

const agentKey = "sk-agent-SECRET-9876543210"

func modelsServer(t *testing.T, wantPath string, ids ...string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != wantPath {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+agentKey {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		data := []map[string]string{}
		for _, id := range ids {
			data = append(data, map[string]string{"id": id})
		}
		writeJSON(w, 200, map[string]any{"data": data})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func noKey(t *testing.T, v any, secretVals ...string) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range secretVals {
		if strings.Contains(string(b), s) {
			t.Fatalf("result leaks secret %q: %s", s, b)
		}
	}
	return string(b)
}

func asMap(t *testing.T, v any) map[string]any {
	t.Helper()
	m, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("not a map: %T", v)
	}
	return m
}

func TestAgentSetupBuiltinActivatesAndTests(t *testing.T) {
	srv := modelsServer(t, "/v1/models", "gpt-4o")
	e := newEnv(t, 2*time.Second)
	ctx := context.Background()
	first := e.create(t, domain.IDOpenAI, apiKeyIn("sk-old-OTHER-123456", nil))
	if !first.Active {
		t.Fatal("first should be active")
	}

	out, err := e.svc.Setup(ctx, domain.SetupRequest{ProviderID: domain.IDOpenAI, APIKey: agentKey, BaseURL: srv.URL + "/v1"})
	if err != nil {
		t.Fatal(err)
	}
	noKey(t, out, agentKey)
	m := asMap(t, out)
	if m["saved"] != true || m["active"] != true {
		t.Fatalf("result = %v", m)
	}
	if tm := asMap(t, m["test"]); tm["ok"] != true {
		t.Fatalf("test = %v", tm)
	}
	credID, _ := m["credential_id"].(string)
	got, err := e.repo.GetCredential(ctx, credID)
	if err != nil || !got.Active || got.Values["base_url"] != srv.URL+"/v1" {
		t.Fatalf("cred = %+v, %v", got, err)
	}
	old, _ := e.repo.GetCredential(ctx, first.ID)
	if old.Active {
		t.Fatal("previous credential must no longer be active")
	}
	if v, err := secretOf(t, e.store, credID, "api_key"); err != nil || v != agentKey {
		t.Fatalf("stored key = %q %v", v, err)
	}
}

func TestAgentSetupCustomDiscoversAndSets(t *testing.T) {
	srv := modelsServer(t, "/v1/models", "gpt-4o", "gpt-image-1", "tts-1", "sora-2")
	e := newEnv(t, 2*time.Second)
	ctx := context.Background()

	out, err := e.svc.Setup(ctx, domain.SetupRequest{APIKey: agentKey, BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	noKey(t, out, agentKey)
	customs, _ := e.repo.ListCustom(ctx)
	if len(customs) != 1 {
		t.Fatalf("customs = %d", len(customs))
	}
	p := customs[0]
	if len(p.Models) != 4 {
		t.Fatalf("models = %+v", p.Models)
	}
	defaults := map[domain.Capability]int{}
	for _, m := range p.Models {
		if !m.Enabled {
			t.Fatalf("model %s not enabled", m.ModelID)
		}
		if m.IsDefault {
			defaults[m.Capability]++
		}
	}
	caps := map[domain.Capability]bool{}
	for _, m := range p.Models {
		caps[m.Capability] = true
	}
	for c := range caps {
		if defaults[c] != 1 {
			t.Fatalf("capability %s defaults = %d", c, defaults[c])
		}
	}
	if len(caps) < 3 {
		t.Fatalf("expected several capabilities, got %v", caps)
	}
	if v, err := e.store.Get(customSecretKey(p.ID)); err != nil || v != agentKey {
		t.Fatalf("custom key = %q %v", v, err)
	}
	if asMap(t, out)["custom_id"] != p.ID {
		t.Fatalf("result = %v", out)
	}
}

func TestAgentSetupCustomDiscoveryFailureSavesNothing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusUnauthorized)
	}))
	defer srv.Close()
	e := newEnv(t, 2*time.Second)
	ctx := context.Background()
	_, err := e.svc.Setup(ctx, domain.SetupRequest{APIKey: agentKey, BaseURL: srv.URL})
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), agentKey) {
		t.Fatal("error leaks key")
	}
	if c, _ := e.repo.ListCustom(ctx); len(c) != 0 {
		t.Fatalf("customs saved: %+v", c)
	}
	if _, err := e.store.Get(secrets.Key("provider_custom_x_api_key")); err == nil {
		t.Fatal("unexpected secret")
	}
}

func TestAgentOverviewMasksSecrets(t *testing.T) {
	srv := modelsServer(t, "/v1/models", "gpt-4o")
	e := newEnv(t, 2*time.Second)
	ctx := context.Background()
	if _, err := e.svc.Setup(ctx, domain.SetupRequest{ProviderID: domain.IDOpenAI, APIKey: agentKey, BaseURL: srv.URL + "/v1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Setup(ctx, domain.SetupRequest{APIKey: agentKey, BaseURL: srv.URL}); err != nil {
		t.Fatal(err)
	}
	out, err := e.svc.Overview(ctx)
	if err != nil {
		t.Fatal(err)
	}
	s := noKey(t, out, agentKey)
	for _, want := range []string{domain.MaskSecret(agentKey), `"fields"`, `"api_key"`, `"secret":true`, `"custom"`, `"enabled_models":1`} {
		if !strings.Contains(s, want) {
			t.Errorf("overview lacks %s: %s", want, s)
		}
	}
}

func TestAgentTestDeleteDescribeDispatch(t *testing.T) {
	srv := modelsServer(t, "/v1/models", "gpt-4o")
	e := newEnv(t, 2*time.Second)
	ctx := context.Background()
	b, err := e.svc.Setup(ctx, domain.SetupRequest{ProviderID: domain.IDOpenAI, APIKey: agentKey, BaseURL: srv.URL + "/v1"})
	if err != nil {
		t.Fatal(err)
	}
	credID := asMap(t, b)["credential_id"].(string)
	c, err := e.svc.Setup(ctx, domain.SetupRequest{APIKey: agentKey, BaseURL: srv.URL, Name: "Mine"})
	if err != nil {
		t.Fatal(err)
	}
	customID := asMap(t, c)["custom_id"].(string)

	for _, id := range []string{credID, customID} {
		r, err := e.svc.TestByID(ctx, id)
		if err != nil || asMap(t, r)["ok"] != true {
			t.Fatalf("test %s: %v %v", id, r, err)
		}
	}
	if d, err := e.svc.Describe(ctx, credID); err != nil || !strings.HasPrefix(d, "OpenAI / ") {
		t.Fatalf("describe cred = %q %v", d, err)
	}
	if d, err := e.svc.Describe(ctx, customID); err != nil || d != "Mine (custom)" {
		t.Fatalf("describe custom = %q %v", d, err)
	}
	if _, err := e.svc.Describe(ctx, "missing"); err == nil {
		t.Fatal("expected error for unknown id")
	}
	if _, err := e.svc.TestByID(ctx, "missing"); err == nil {
		t.Fatal("expected test error for unknown id")
	}
	if err := e.svc.DeleteByID(ctx, "missing"); err == nil {
		t.Fatal("expected delete error for unknown id")
	}

	if err := e.svc.DeleteByID(ctx, credID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.repo.GetCredential(ctx, credID); err == nil {
		t.Fatal("credential still present")
	}
	if _, err := secretOf(t, e.store, credID, "api_key"); err == nil {
		t.Fatal("credential secret still present")
	}
	if err := e.svc.DeleteByID(ctx, customID); err != nil {
		t.Fatal(err)
	}
	if cs, _ := e.repo.ListCustom(ctx); len(cs) != 0 {
		t.Fatalf("custom still present: %+v", cs)
	}
	if _, err := e.store.Get(customSecretKey(customID)); err == nil {
		t.Fatal("custom secret still present")
	}
}

func TestSavedOptionsLabelsAndFilter(t *testing.T) {
	e := newEnv(t, 2*time.Second)
	ctx := context.Background()
	live := e.create(t, domain.IDOpenAI, apiKeyIn(testKey, nil))
	deadKey := "sk-dead-SECRET-0987654321"
	dead := e.create(t, domain.IDOpenAI, apiKeyIn(deadKey, nil))
	if err := e.repo.SetCredentialHealth(ctx, dead.ID, domain.Health{Outcome: domain.OutcomeAuthFailed, TestedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	custom, err := e.svc.CreateCustom(ctx, domain.CustomInput{Name: "Mine", DiscoveryFormat: domain.DiscoveryFormats()[0],
		BaseURL: "https://example.test/v1", APIKey: "sk-custom-SECRET-1122334455"})
	if err != nil {
		t.Fatal(err)
	}

	all, err := e.svc.SavedOptions(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]string{}
	for _, o := range all {
		byID[o.Value] = o.Label
	}
	if len(all) != 3 || byID[live.ID] == "" || byID[dead.ID] == "" || byID[custom.ID] == "" {
		t.Fatalf("options = %+v", all)
	}
	if !strings.Contains(byID[live.ID], domain.MaskSecret(testKey)) || strings.Contains(byID[live.ID], "DEAD") {
		t.Errorf("live label = %q", byID[live.ID])
	}
	if !strings.Contains(byID[dead.ID], domain.MaskSecret(deadKey)) || !strings.Contains(byID[dead.ID], "DEAD") {
		t.Errorf("dead label = %q", byID[dead.ID])
	}
	if !strings.Contains(byID[custom.ID], "(custom)") {
		t.Errorf("custom label = %q", byID[custom.ID])
	}
	noKey(t, all, testKey, deadKey, "sk-custom-SECRET-1122334455")

	creds, err := e.svc.SavedOptions(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(creds) != 2 {
		t.Fatalf("credentialsOnly = %+v", creds)
	}
	for _, o := range creds {
		if o.Value == custom.ID {
			t.Error("credentialsOnly must exclude custom providers")
		}
	}
}
