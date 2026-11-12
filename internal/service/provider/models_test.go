package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"app/internal/apperr"
	domain "app/internal/domain/provider"
)

// openAIModelList serves GET .../models with the given ids (any key accepted).
func openAIModelList(t *testing.T, hits *atomic.Int32, ids ...string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/models") {
			http.NotFound(w, r)
			return
		}
		if hits != nil {
			hits.Add(1)
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

func (e *env) withLimits(t *testing.T, def domain.LimitDefaults) {
	t.Helper()
	svc, err := New(Options{Repo: e.repo, Secrets: e.store, Client: http.DefaultClient, TestTimeout: 2 * time.Second, LimitDefaults: def})
	if err != nil {
		t.Fatal(err)
	}
	e.svc = svc
}

func byID(models []domain.Model, id string) (domain.Model, bool) {
	for _, m := range models {
		if m.ModelID == id {
			return m, true
		}
	}
	return domain.Model{}, false
}

func ref(p, m string) domain.ModelRef { return domain.ModelRef{ProviderID: p, ModelID: m} }

func TestSetModelsBuiltin(t *testing.T) {
	e := newEnv(t, 2*time.Second)
	ctx := context.Background()

	// endpoint is rejected for built-in providers
	_, err := e.svc.SetModels(ctx, domain.IDOpenAI, []domain.Model{{ModelID: "gpt-4o", Capability: domain.CapabilityLLM, Endpoint: domain.EndpointOpenAIChat, Enabled: true}})
	wantCode(t, err, apperr.CodeValidation)
	// capability the provider lacks (Anthropic is LLM only)
	_, err = e.svc.SetModels(ctx, domain.IDAnthropic, []domain.Model{{ModelID: "x", Capability: domain.CapabilityImage, Enabled: true}})
	wantCode(t, err, apperr.CodeValidation)
	// nothing was saved by the failures
	if got, _ := e.repo.ListModels(ctx, domain.IDAnthropic); len(got) != 0 {
		t.Fatalf("saved despite error: %+v", got)
	}
	// unknown owner
	_, err = e.svc.SetModels(ctx, "nope", nil)
	wantCode(t, err, apperr.CodeNotFound)

	before := *e.changed
	got, err := e.svc.SetModels(ctx, domain.IDOpenAI, []domain.Model{
		{ModelID: " gpt-4o ", Capability: domain.CapabilityLLM, Enabled: true, IsDefault: true},
		{ModelID: "sora-2", Capability: domain.CapabilityVideo, Enabled: true, Facts: domain.ModelFacts{Durations: []int{4, 8}}},
	})
	if err != nil || len(got) != 2 || got[0].ModelID != "gpt-4o" {
		t.Fatalf("%+v %v", got, err)
	}
	if *e.changed != before+1 {
		t.Fatal("OnChanged not called")
	}
	saved, _ := e.repo.ListModels(ctx, domain.IDOpenAI)
	if len(saved) != 2 || saved[1].Facts.Durations[1] != 8 {
		t.Fatalf("%+v", saved)
	}
	// empty list clears
	got, err = e.svc.SetModels(ctx, domain.IDOpenAI, nil)
	if err != nil || got == nil || len(got) != 0 {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestSetModelsCustom(t *testing.T) {
	e := newEnv(t, 2*time.Second)
	ctx := context.Background()
	p, err := e.svc.CreateCustom(ctx, domain.CustomInput{Name: "c", DiscoveryFormat: domain.DiscoveryOpenAI, BaseURL: "https://a.test"})
	if err != nil {
		t.Fatal(err)
	}
	// endpoint required and must match the protocol
	_, err = e.svc.SetModels(ctx, p.ID, []domain.Model{{ModelID: "m", Capability: domain.CapabilityLLM}})
	wantCode(t, err, apperr.CodeValidation)
	_, err = e.svc.SetModels(ctx, p.ID, []domain.Model{{ModelID: "m", Endpoint: domain.EndpointGeminiGenerate, Enabled: true}})
	wantCode(t, err, apperr.CodeValidation)

	got, err := e.svc.SetModels(ctx, p.ID, []domain.Model{
		{ModelID: "img", Endpoint: domain.EndpointOpenAIImages, Enabled: true, DisplayName: "Image"},
	})
	if err != nil || got[0].Capability != domain.CapabilityImage {
		t.Fatalf("capability must follow the endpoint: %+v %v", got, err)
	}
	cp, _ := e.repo.GetCustom(ctx, p.ID)
	if len(cp.Models) != 1 || cp.Models[0].DisplayName != "Image" || cp.Capabilities()[0] != domain.CapabilityImage {
		t.Fatalf("%+v", cp)
	}
}

func TestDiscoverBuiltinModelsOpenAI(t *testing.T) {
	srv := openAIModelList(t, nil, "gpt-4o", "gpt-image-1", "text-embedding-3-small", "whisper-1", "sora-2")
	e := newEnv(t, 2*time.Second)
	ctx := context.Background()

	// no credential yet
	_, err := e.svc.DiscoverBuiltinModels(ctx, domain.IDOpenAI)
	wantCode(t, err, apperr.CodeValidation)
	if detail(err, "not_configured") != true {
		t.Fatalf("%v", apperr.From(err).Details)
	}
	_, err = e.svc.DiscoverBuiltinModels(ctx, "nope")
	wantCode(t, err, apperr.CodeNotFound)

	e.create(t, domain.IDOpenAI, apiKeyIn(testKey, map[string]string{"base_url": srv.URL + "/v1"}))
	// user already disabled gpt-4o and edited its name
	if _, err := e.svc.SetModels(ctx, domain.IDOpenAI, []domain.Model{
		{ModelID: "gpt-4o", DisplayName: "My 4o", Capability: domain.CapabilityLLM, Enabled: false, UserAdded: true},
	}); err != nil {
		t.Fatal(err)
	}

	got, err := e.svc.DiscoverBuiltinModels(ctx, domain.IDOpenAI)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"text-embedding-3-small", "whisper-1"} {
		if _, ok := byID(got, id); ok {
			t.Fatalf("non-generative %s must be dropped: %+v", id, got)
		}
	}
	if len(got) != 3 {
		t.Fatalf("%+v", got)
	}
	for _, m := range got {
		if m.Endpoint != "" {
			t.Fatalf("built-in model must have no endpoint: %+v", m)
		}
	}
	gpt, _ := byID(got, "gpt-4o")
	if gpt.Enabled || gpt.DisplayName != "My 4o" || !gpt.UserAdded || gpt.IsDefault {
		t.Fatalf("user choices lost: %+v", gpt)
	}
	img, _ := byID(got, "gpt-image-1")
	vid, _ := byID(got, "sora-2")
	if img.Capability != domain.CapabilityImage || !img.Enabled || !img.IsDefault || vid.Capability != domain.CapabilityVideo || !vid.Enabled || !vid.IsDefault {
		t.Fatalf("new models: %+v %+v", img, vid)
	}
	// persisted
	saved, _ := e.repo.ListModels(ctx, domain.IDOpenAI)
	if len(saved) != 3 {
		t.Fatalf("%+v", saved)
	}
	// second run is idempotent
	again, err := e.svc.DiscoverBuiltinModels(ctx, domain.IDOpenAI)
	if err != nil || len(again) != 3 {
		t.Fatalf("%+v %v", again, err)
	}
}

func TestDiscoverBuiltinModelsOnlyOfferedCapabilities(t *testing.T) {
	// Agnes offers llm/image/video; a TTS id served by its model list must be dropped.
	srv := openAIModelList(t, nil, "grok-3", "tts-1")
	e := newEnv(t, 2*time.Second)
	e.create(t, domain.IDAgnes, apiKeyIn(testKey, map[string]string{"base_url": srv.URL + "/v1"}))
	got, err := e.svc.DiscoverBuiltinModels(context.Background(), domain.IDAgnes)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := byID(got, "tts-1"); ok {
		t.Fatalf("agnes has no tts: %+v", got)
	}
	if _, ok := byID(got, "grok-3"); !ok {
		t.Fatalf("%+v", got)
	}
}

func TestDiscoverBuiltinModelsNoDiscovery(t *testing.T) {
	e := newEnv(t, 2*time.Second)
	ctx := context.Background()
	e.create(t, domain.IDKling, apiKeyIn(testKey, nil))
	_, err := e.svc.DiscoverBuiltinModels(ctx, domain.IDKling)
	wantCode(t, err, apperr.CodeValidation)
	if detail(err, "no_discovery") != true {
		t.Fatalf("%v", apperr.From(err).Details)
	}
}

func TestDiscoverBuiltinModelsAuthFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad key "+testKey, http.StatusUnauthorized)
	}))
	defer srv.Close()
	e := newEnv(t, 2*time.Second)
	e.create(t, domain.IDOpenAI, apiKeyIn(testKey, map[string]string{"base_url": srv.URL + "/v1"}))
	_, err := e.svc.DiscoverBuiltinModels(context.Background(), domain.IDOpenAI)
	wantCode(t, err, apperr.CodeUnavailable)
	if strings.Contains(err.Error(), testKey) || strings.Contains(apperr.From(err).Message, testKey) {
		t.Fatal("error leaks the key")
	}
}

func TestDiscoverModelsForCustom(t *testing.T) {
	srv := openAIModelList(t, nil, "gpt-4o", "tts-1", "text-embedding-3-small")
	e := newEnv(t, 2*time.Second)
	ctx := context.Background()
	p, err := e.svc.CreateCustom(ctx, domain.CustomInput{Name: "c", DiscoveryFormat: domain.DiscoveryOpenAI, BaseURL: srv.URL, APIKey: testKey,
		Models: []domain.Model{{ModelID: "gpt-4o", Endpoint: domain.EndpointOpenAIResponses, Enabled: false}}})
	if err != nil {
		t.Fatal(err)
	}
	out, err := e.svc.DiscoverModelsFor(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	m := asMap(t, out)
	if m["ok"] != true || m["models"] != 2 {
		t.Fatalf("%v", m)
	}
	cp, _ := e.repo.GetCustom(ctx, p.ID)
	g, _ := byID(cp.Models, "gpt-4o")
	if g.Enabled || g.Endpoint != domain.EndpointOpenAIResponses {
		t.Fatalf("saved model changed: %+v", g)
	}
	tts, ok := byID(cp.Models, "tts-1")
	if !ok || tts.Endpoint != domain.EndpointOpenAITTS || tts.Capability != domain.CapabilityTTS || !tts.Enabled {
		t.Fatalf("%+v", tts)
	}
	_, err = e.svc.DiscoverModelsFor(ctx, "missing")
	wantCode(t, err, apperr.CodeNotFound)
}

func TestSettingsAndLimits(t *testing.T) {
	e := newEnv(t, 2*time.Second)
	e.withLimits(t, domain.LimitDefaults{MaxWorkers: 3, RPM: 10, RequestGapMs: 100})
	ctx := context.Background()

	l, err := e.svc.Limits(ctx, domain.IDOpenAI)
	if err != nil || l.MaxWorkers[domain.CapabilityVideo] != 3 || l.RPM[domain.CapabilityImage] != 10 || l.RequestGapMs != 100 {
		t.Fatalf("%+v %v", l, err)
	}
	// Agnes declares one video job at a time
	l, _ = e.svc.Limits(ctx, domain.IDAgnes)
	if l.MaxWorkers[domain.CapabilityVideo] != 1 || l.MaxWorkers[domain.CapabilityImage] != 3 {
		t.Fatalf("%+v", l)
	}

	set, err := e.svc.UpdateSettings(ctx, domain.IDAgnes, map[string]string{domain.SettingVideoMaxWorkers: "4", domain.SettingImageRPM: "0"})
	if err != nil || set[domain.SettingVideoMaxWorkers] != 4 || set[domain.SettingImageRPM] != 0 || len(set) != 2 {
		t.Fatalf("%v %v", set, err)
	}
	l, _ = e.svc.Limits(ctx, domain.IDAgnes)
	if l.MaxWorkers[domain.CapabilityVideo] != 4 || l.RPM[domain.CapabilityImage] != 0 {
		t.Fatalf("%+v", l)
	}
	// empty value clears back to default
	set, err = e.svc.UpdateSettings(ctx, domain.IDAgnes, map[string]string{domain.SettingVideoMaxWorkers: "", domain.SettingImageRPM: ""})
	if err != nil || len(set) != 0 {
		t.Fatalf("%v %v", set, err)
	}
	l, _ = e.svc.Limits(ctx, domain.IDAgnes)
	if l.MaxWorkers[domain.CapabilityVideo] != 1 || l.RPM[domain.CapabilityImage] != 10 {
		t.Fatalf("%+v", l)
	}

	_, err = e.svc.UpdateSettings(ctx, domain.IDAgnes, map[string]string{"bogus": "1"})
	wantCode(t, err, apperr.CodeValidation)
	_, err = e.svc.UpdateSettings(ctx, domain.IDAgnes, map[string]string{domain.SettingVideoMaxWorkers: "999"})
	wantCode(t, err, apperr.CodeValidation)
	_, err = e.svc.UpdateSettings(ctx, "nope", map[string]string{})
	wantCode(t, err, apperr.CodeNotFound)
	_, err = e.svc.Limits(ctx, "nope")
	wantCode(t, err, apperr.CodeNotFound)

	// custom providers have settings too, with no meta default
	p, _ := e.svc.CreateCustom(ctx, domain.CustomInput{Name: "c", DiscoveryFormat: domain.DiscoveryOpenAI, BaseURL: "https://a.test"})
	if _, err := e.svc.UpdateSettings(ctx, p.ID, map[string]string{domain.SettingImageMaxWorkers: "2"}); err != nil {
		t.Fatal(err)
	}
	l, err = e.svc.Limits(ctx, p.ID)
	if err != nil || l.MaxWorkers[domain.CapabilityImage] != 2 || l.MaxWorkers[domain.CapabilityVideo] != 3 {
		t.Fatalf("%+v %v", l, err)
	}
}

// ---- defaults / ResolveModel ----

func setModels(t *testing.T, e *env, owner string, models ...domain.Model) {
	t.Helper()
	if _, err := e.svc.SetModels(context.Background(), owner, models); err != nil {
		t.Fatalf("SetModels %s: %v", owner, err)
	}
}

func llm(id string, enabled bool, def bool) domain.Model {
	return domain.Model{ModelID: id, Capability: domain.CapabilityLLM, Enabled: enabled, IsDefault: def}
}

func purpose(t *testing.T, e *env, p domain.Purpose) PurposeDefault {
	t.Helper()
	defs, err := e.svc.Defaults(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(defs) != len(domain.PurposeSpecs()) {
		t.Fatalf("defs = %d", len(defs))
	}
	for _, d := range defs {
		if d.Purpose == p {
			return d
		}
	}
	t.Fatalf("no purpose %s", p)
	return PurposeDefault{}
}

func TestDefaultsCandidates(t *testing.T) {
	e := newEnv(t, 2*time.Second)
	ctx := context.Background()

	// nothing configured
	d := purpose(t, e, domain.PurposeText)
	if d.Candidates == nil || len(d.Candidates) != 0 || d.Effective.ProviderID != "" || d.Chosen.ProviderID != "" || d.ChosenUnavailable {
		t.Fatalf("%+v", d)
	}

	// models without an active credential are not candidates
	setModels(t, e, domain.IDOpenAI, llm("gpt-4o", true, true))
	if d = purpose(t, e, domain.PurposeText); len(d.Candidates) != 0 {
		t.Fatalf("no credential: %+v", d)
	}

	openai := e.create(t, domain.IDOpenAI, apiKeyIn(testKey, nil))
	d = purpose(t, e, domain.PurposeText)
	if len(d.Candidates) != 1 || d.Candidates[0].Ref != ref("openai", "gpt-4o") || d.Candidates[0].Custom || !d.Candidates[0].IsProviderDefault ||
		d.Candidates[0].ProviderName != "OpenAI" {
		t.Fatalf("%+v", d)
	}
	if d.Effective != ref("openai", "gpt-4o") || d.Capability != domain.CapabilityLLM {
		t.Fatalf("%+v", d)
	}
	// other purposes stay empty
	if d = purpose(t, e, domain.PurposeImage); len(d.Candidates) != 0 {
		t.Fatalf("%+v", d)
	}

	// disabled models are not candidates; the provider default model comes first
	setModels(t, e, domain.IDOpenAI, llm("a", true, false), llm("b", true, true), llm("off", false, false))
	d = purpose(t, e, domain.PurposeText)
	if len(d.Candidates) != 2 || d.Candidates[0].Ref.ModelID != "b" || d.Candidates[1].Ref.ModelID != "a" || d.Effective.ModelID != "b" {
		t.Fatalf("%+v", d.Candidates)
	}

	// a dead active key removes the provider from the candidates; a network failure does not
	if err := e.repo.SetCredentialHealth(ctx, openai.ID, domain.Health{Outcome: domain.OutcomeTimeout, TestedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if d = purpose(t, e, domain.PurposeText); len(d.Candidates) != 2 {
		t.Fatalf("timeout is not a dead key: %+v", d.Candidates)
	}
	if err := e.repo.SetCredentialHealth(ctx, openai.ID, domain.Health{Outcome: domain.OutcomeAuthFailed, TestedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if d = purpose(t, e, domain.PurposeText); len(d.Candidates) != 0 || d.Effective.ProviderID != "" {
		t.Fatalf("dead key: %+v", d)
	}

	// only the ACTIVE credential counts: a healthy spare does not revive the provider
	spare := e.create(t, domain.IDOpenAI, apiKeyIn("sk-spare-1234567890", nil))
	if spare.Active {
		t.Fatal("spare must not be active")
	}
	if d = purpose(t, e, domain.PurposeText); len(d.Candidates) != 0 {
		t.Fatalf("spare key must not count: %+v", d.Candidates)
	}
	if err := e.svc.ActivateCredential(ctx, spare.ID); err != nil {
		t.Fatal(err)
	}
	if d = purpose(t, e, domain.PurposeText); len(d.Candidates) != 2 {
		t.Fatalf("activated healthy key: %+v", d.Candidates)
	}
}

func TestDefaultsAutoOrder(t *testing.T) {
	e := newEnv(t, 2*time.Second)
	ctx := context.Background()

	// custom provider, created first, plus two built-ins; registry order is openai < anthropic
	cp, err := e.svc.CreateCustom(ctx, domain.CustomInput{Name: "Custom", DiscoveryFormat: domain.DiscoveryOpenAI, BaseURL: "https://a.test",
		Models: []domain.Model{{ModelID: "c-chat", Endpoint: domain.EndpointOpenAIChat, Enabled: true, IsDefault: true}}})
	if err != nil {
		t.Fatal(err)
	}
	e.create(t, domain.IDAnthropic, apiKeyIn(testKey, nil))
	e.create(t, domain.IDOpenAI, apiKeyIn("sk-openai-1234567890", nil))
	setModels(t, e, domain.IDAnthropic, llm("claude", true, true))
	setModels(t, e, domain.IDOpenAI, llm("gpt-4o", true, true))

	d := purpose(t, e, domain.PurposeText)
	if len(d.Candidates) != 3 {
		t.Fatalf("%+v", d.Candidates)
	}
	if d.Candidates[0].Ref != ref("openai", "gpt-4o") || d.Candidates[1].Ref != ref("anthropic", "claude") ||
		d.Candidates[2].Ref != ref(cp.ID, "c-chat") || !d.Candidates[2].Custom || d.Candidates[2].ProviderName != "Custom" {
		t.Fatalf("order: %+v", d.Candidates)
	}
	if d.Effective != ref("openai", "gpt-4o") || d.Chosen.ProviderID != "" {
		t.Fatalf("auto pick: %+v", d)
	}

	// without built-ins the custom provider is picked
	e2 := newEnv(t, 2*time.Second)
	cp2, _ := e2.svc.CreateCustom(ctx, domain.CustomInput{Name: "Only", DiscoveryFormat: domain.DiscoveryOpenAI, BaseURL: "https://a.test",
		Models: []domain.Model{{ModelID: "m", Endpoint: domain.EndpointOpenAIChat, Enabled: true}}})
	if d = purpose(t, e2, domain.PurposeText); d.Effective != ref(cp2.ID, "m") {
		t.Fatalf("%+v", d)
	}

	// a custom provider with a dead key is skipped
	if err := e.repo.SetCustomHealth(ctx, cp.ID, domain.Health{Outcome: domain.OutcomeInvalid, TestedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if d = purpose(t, e, domain.PurposeText); len(d.Candidates) != 2 {
		t.Fatalf("%+v", d.Candidates)
	}
}

func TestDefaultsChosenUnavailableFallsBackToAuto(t *testing.T) {
	e := newEnv(t, 2*time.Second)
	ctx := context.Background()
	oa := e.create(t, domain.IDOpenAI, apiKeyIn("sk-openai-1234567890", nil))
	e.create(t, domain.IDAnthropic, apiKeyIn(testKey, nil))
	setModels(t, e, domain.IDOpenAI, llm("gpt-4o", true, true))
	setModels(t, e, domain.IDAnthropic, llm("claude", true, true), llm("haiku", true, false))

	if err := e.svc.SetDefault(ctx, domain.PurposeText, "anthropic/haiku"); err != nil {
		t.Fatal(err)
	}
	d := purpose(t, e, domain.PurposeText)
	if d.Chosen != ref("anthropic", "haiku") || d.Effective != ref("anthropic", "haiku") || d.ChosenUnavailable {
		t.Fatalf("%+v", d)
	}

	// the chosen model gets disabled
	setModels(t, e, domain.IDAnthropic, llm("claude", true, true), llm("haiku", false, false))
	d = purpose(t, e, domain.PurposeText)
	if !d.ChosenUnavailable || d.Chosen != ref("anthropic", "haiku") || d.Effective != ref("openai", "gpt-4o") {
		t.Fatalf("disabled: %+v", d)
	}
	// ResolveModel without ref follows the fallback
	rm, err := e.svc.ResolveModel(ctx, domain.PurposeText, "")
	if err != nil || rm.Ref != ref("openai", "gpt-4o") {
		t.Fatalf("%+v %v", rm, err)
	}

	// re-enable, then the provider's key dies
	setModels(t, e, domain.IDAnthropic, llm("claude", true, true), llm("haiku", true, false))
	if d = purpose(t, e, domain.PurposeText); d.ChosenUnavailable {
		t.Fatalf("%+v", d)
	}
	anth, _ := e.repo.ListCredentials(ctx)
	for _, c := range anth {
		if c.ProviderID == domain.IDAnthropic {
			_ = e.repo.SetCredentialHealth(ctx, c.ID, domain.Health{Outcome: domain.OutcomeAuthFailed, TestedAt: time.Now()})
		}
	}
	d = purpose(t, e, domain.PurposeText)
	if !d.ChosenUnavailable || d.Effective != ref("openai", "gpt-4o") {
		t.Fatalf("dead key: %+v", d)
	}

	// the provider is deleted entirely: still unavailable, nothing crashes
	_ = e.svc.DeleteCredential(ctx, oa.ID)
	d = purpose(t, e, domain.PurposeText)
	if !d.ChosenUnavailable || d.Effective.ProviderID != "" {
		t.Fatalf("%+v", d)
	}
}

func TestSetDefault(t *testing.T) {
	e := newEnv(t, 2*time.Second)
	ctx := context.Background()
	e.create(t, domain.IDOpenAI, apiKeyIn(testKey, nil))
	setModels(t, e, domain.IDOpenAI,
		llm("gpt-4o", true, true),
		domain.Model{ModelID: "gpt-image-1", Capability: domain.CapabilityImage, Enabled: true, IsDefault: true},
		domain.Model{ModelID: "off", Capability: domain.CapabilityLLM, Enabled: false})

	before := *e.changed
	if err := e.svc.SetDefault(ctx, domain.PurposeImage, "openai/gpt-image-1"); err != nil {
		t.Fatal(err)
	}
	if *e.changed != before+1 {
		t.Fatal("OnChanged not called")
	}
	if d := purpose(t, e, domain.PurposeImage); d.Chosen != ref("openai", "gpt-image-1") {
		t.Fatalf("%+v", d)
	}

	for name, tc := range map[string]struct {
		p   domain.Purpose
		ref string
	}{
		"wrong capability": {domain.PurposeText, "openai/gpt-image-1"},
		"disabled model":   {domain.PurposeText, "openai/off"},
		"unknown model":    {domain.PurposeText, "openai/nope"},
		"no credential":    {domain.PurposeText, "anthropic/claude"},
		"unknown purpose":  {"bogus", "openai/gpt-4o"},
		"malformed ref":    {domain.PurposeText, "openai"},
	} {
		if err := e.svc.SetDefault(ctx, tc.p, tc.ref); code(err) != apperr.CodeValidation {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	// failures did not change the stored choice
	if d := purpose(t, e, domain.PurposeText); d.Chosen.ProviderID != "" {
		t.Fatalf("%+v", d)
	}

	// "" goes back to auto
	if err := e.svc.SetDefault(ctx, domain.PurposeImage, ""); err != nil {
		t.Fatal(err)
	}
	if d := purpose(t, e, domain.PurposeImage); d.Chosen.ProviderID != "" || d.Effective != ref("openai", "gpt-image-1") {
		t.Fatalf("%+v", d)
	}

	// SetDefaultModel returns the resulting state
	out, err := e.svc.SetDefaultModel(ctx, domain.PurposeText, "openai/gpt-4o")
	m := asMap(t, out)
	if err != nil || m["chosen"] != "openai/gpt-4o" || m["effective"] != "openai/gpt-4o" || m["ok"] != true {
		t.Fatalf("%v %v", m, err)
	}
}

func TestSetDefaultModelIdWithSlash(t *testing.T) {
	e := newEnv(t, 2*time.Second)
	ctx := context.Background()
	cp, err := e.svc.CreateCustom(ctx, domain.CustomInput{Name: "OR", DiscoveryFormat: domain.DiscoveryOpenAI, BaseURL: "https://a.test",
		Models: []domain.Model{{ModelID: "meta/llama-3:free", Endpoint: domain.EndpointOpenAIChat, Enabled: true}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.svc.SetDefault(ctx, domain.PurposeText, cp.ID+"/meta/llama-3:free"); err != nil {
		t.Fatal(err)
	}
	rm, err := e.svc.ResolveModel(ctx, domain.PurposeText, cp.ID+"/meta/llama-3:free")
	if err != nil || rm.Ref.ModelID != "meta/llama-3:free" || rm.Custom == nil {
		t.Fatalf("%+v %v", rm, err)
	}
}

func TestResolveModel(t *testing.T) {
	e := newEnv(t, 2*time.Second)
	e.withLimits(t, domain.LimitDefaults{MaxWorkers: 3, RPM: 10, RequestGapMs: 100})
	ctx := context.Background()

	// nothing configured
	_, err := e.svc.ResolveModel(ctx, domain.PurposeVideo, "")
	wantCode(t, err, apperr.CodeValidation)
	if detail(err, "not_configured") != true {
		t.Fatalf("%v", apperr.From(err).Details)
	}
	_, err = e.svc.ResolveModel(ctx, "bogus", "")
	wantCode(t, err, apperr.CodeValidation)
	_, err = e.svc.ResolveModel(ctx, domain.PurposeText, "bad")
	wantCode(t, err, apperr.CodeValidation)

	cp, _ := e.svc.CreateCustom(ctx, domain.CustomInput{Name: "Custom", DiscoveryFormat: domain.DiscoveryOpenAI, BaseURL: "https://a.test", APIKey: "sk-custom-key-123456",
		Models: []domain.Model{{ModelID: "c-vid", Endpoint: domain.EndpointOpenAIVideo, Enabled: true, IsDefault: true}}})
	cred := e.create(t, domain.IDAgnes, apiKeyIn(testKey, nil))
	setModels(t, e, domain.IDAgnes,
		domain.Model{ModelID: "agnes-vid", Capability: domain.CapabilityVideo, Enabled: true, IsDefault: true, Facts: domain.ModelFacts{Durations: []int{5}}},
		llm("agnes-chat", true, true))

	// auto: built-in beats custom; Builtin credentials and limits (Agnes video default 1) come with it
	rm, err := e.svc.ResolveModel(ctx, domain.PurposeVideo, "")
	if err != nil {
		t.Fatal(err)
	}
	if rm.Ref != ref("agnes", "agnes-vid") || rm.Builtin == nil || rm.Custom != nil || rm.Purpose != domain.PurposeVideo ||
		rm.Builtin.Secrets["api_key"] != testKey || rm.Builtin.CredentialID != cred.ID || rm.Model.Facts.Durations[0] != 5 {
		t.Fatalf("%+v", rm)
	}
	if rm.Limits.MaxWorkers[domain.CapabilityVideo] != 1 || rm.Limits.MaxWorkers[domain.CapabilityImage] != 3 || rm.Limits.RPM[domain.CapabilityVideo] != 10 {
		t.Fatalf("limits %+v", rm.Limits)
	}
	// stored settings flow into the limits
	if _, err := e.svc.UpdateSettings(ctx, domain.IDAgnes, map[string]string{domain.SettingVideoMaxWorkers: "2"}); err != nil {
		t.Fatal(err)
	}
	if rm, _ = e.svc.ResolveModel(ctx, domain.PurposeVideo, ""); rm.Limits.MaxWorkers[domain.CapabilityVideo] != 2 {
		t.Fatalf("%+v", rm.Limits)
	}

	// explicit ref to a custom model
	rm, err = e.svc.ResolveModel(ctx, domain.PurposeVideo, cp.ID+"/c-vid")
	if err != nil || rm.Custom == nil || rm.Builtin != nil || rm.Custom.APIKey != "sk-custom-key-123456" || rm.Custom.Provider.ID != cp.ID {
		t.Fatalf("%+v %v", rm, err)
	}
	if rm.Limits.MaxWorkers[domain.CapabilityVideo] != 3 {
		t.Fatalf("custom limits %+v", rm.Limits)
	}

	// the user's default wins over auto
	if err := e.svc.SetDefault(ctx, domain.PurposeVideo, cp.ID+"/c-vid"); err != nil {
		t.Fatal(err)
	}
	if rm, _ = e.svc.ResolveModel(ctx, domain.PurposeVideo, ""); rm.Ref != ref(cp.ID, "c-vid") {
		t.Fatalf("%+v", rm.Ref)
	}
	// ...but an explicit ref wins over the default
	if rm, _ = e.svc.ResolveModel(ctx, domain.PurposeVideo, "agnes/agnes-vid"); rm.Ref != ref("agnes", "agnes-vid") {
		t.Fatalf("%+v", rm.Ref)
	}

	// explicit ref of the wrong purpose / unknown / disabled
	for _, bad := range []string{"agnes/agnes-chat", "agnes/nope", "kling/x"} {
		_, err = e.svc.ResolveModel(ctx, domain.PurposeVideo, bad)
		wantCode(t, err, apperr.CodeValidation)
		if detail(err, "not_configured") != true {
			t.Fatalf("%s: %v", bad, apperr.From(err).Details)
		}
	}

	// secret lost from the store: the resolution surfaces the integrity error instead of using a blank key
	_ = e.store.Delete(credSecretKey(cred.ID, "api_key"))
	_, err = e.svc.ResolveModel(ctx, domain.PurposeText, "agnes/agnes-chat")
	wantCode(t, err, apperr.CodeIntegrity)
}

func TestModelOptions(t *testing.T) {
	e := newEnv(t, 2*time.Second)
	ctx := context.Background()
	e.create(t, domain.IDOpenAI, apiKeyIn(testKey, nil))
	setModels(t, e, domain.IDOpenAI, llm("gpt-4o", true, true),
		domain.Model{ModelID: "gpt-image-1", DisplayName: "GPT Image", Capability: domain.CapabilityImage, Enabled: true, IsDefault: true})

	all, err := e.svc.ModelOptions(ctx, "")
	if err != nil || len(all) != 2 {
		t.Fatalf("%+v %v", all, err)
	}
	img, _ := e.svc.ModelOptions(ctx, domain.PurposeImage)
	if len(img) != 1 || img[0].Value != "openai/gpt-image-1" || img[0].Label != "OpenAI · GPT Image" {
		t.Fatalf("%+v", img)
	}
	if none, _ := e.svc.ModelOptions(ctx, domain.PurposeTTS); none == nil || len(none) != 0 {
		t.Fatalf("%+v", none)
	}

	owners, err := e.svc.OwnerOptions(ctx)
	if err != nil || len(owners) != len(domain.Registry()) {
		t.Fatalf("%d %v", len(owners), err)
	}
	cp, _ := e.svc.CreateCustom(ctx, domain.CustomInput{Name: "Mine", DiscoveryFormat: domain.DiscoveryOpenAI, BaseURL: "https://a.test"})
	owners, _ = e.svc.OwnerOptions(ctx)
	last := owners[len(owners)-1]
	if last.Value != cp.ID || last.Label != "Mine (custom)" {
		t.Fatalf("%+v", last)
	}
}
