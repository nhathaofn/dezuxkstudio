package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	domain "app/internal/domain/provider"
)

// acModelsServer serves /v1/models and counts hits. list picks the answer from the bearer key; gate (optional)
// is called before answering and may block.
func acModelsServer(t *testing.T, hits *atomic.Int32, gate func(), list func(key string) (int, []string)) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		hits.Add(1)
		if gate != nil {
			gate()
		}
		status, ids := list(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		if status != 200 {
			http.Error(w, "denied", status)
			return
		}
		var data []map[string]string
		for _, id := range ids {
			data = append(data, map[string]string{"id": id})
		}
		writeJSON(w, 200, map[string]any{"data": data})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func fixedList(ids ...string) func(string) (int, []string) {
	return func(string) (int, []string) { return 200, ids }
}

func modelIDs(ms []domain.Model) map[string]domain.Model {
	out := map[string]domain.Model{}
	for _, m := range ms {
		out[m.ModelID] = m
	}
	return out
}

func wantOneDefaultPerCap(t *testing.T, ms []domain.Model) {
	t.Helper()
	n := map[domain.Capability]int{}
	for _, m := range ms {
		if m.IsDefault {
			n[m.Capability]++
		}
	}
	for _, m := range ms {
		if m.Enabled && n[m.Capability] != 1 {
			t.Fatalf("capability %s has %d defaults: %+v", m.Capability, n[m.Capability], ms)
		}
	}
}

func credHealth(t *testing.T, e *env, id string) domain.Health {
	t.Helper()
	c, err := e.repo.GetCredential(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return c.Health
}

func openaiIn(srv *httptest.Server, key string) domain.Input {
	return apiKeyIn(key, map[string]string{"base_url": srv.URL + "/v1"})
}

func checkingOf(t *testing.T, e *env, id string) bool {
	t.Helper()
	snap, err := e.svc.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range snap.Builtins {
		for _, c := range b.Credentials {
			if c.ID == id {
				return c.Checking
			}
		}
	}
	t.Fatalf("credential %s not in snapshot", id)
	return false
}

func TestAutoCheckOnCreateDiscoversModels(t *testing.T) {
	var hits atomic.Int32
	srv := acModelsServer(t, &hits, nil, fixedList("gpt-4o", "gpt-image-1", "text-embedding-3-small"))
	e := newEnvAuto(t, 2*time.Second, true)
	ctx := context.Background()
	c := e.create(t, domain.IDOpenAI, openaiIn(srv, probeKey))
	e.svc.Wait()

	if h := credHealth(t, e, c.ID); h.Outcome != domain.OutcomeOK {
		t.Fatalf("health = %+v", h)
	}
	ms, err := e.repo.ListModels(ctx, domain.IDOpenAI)
	if err != nil {
		t.Fatal(err)
	}
	by := modelIDs(ms)
	if len(ms) != 2 || by["gpt-4o"].Capability != domain.CapabilityLLM || by["gpt-image-1"].Capability != domain.CapabilityImage {
		t.Fatalf("models = %+v", ms)
	}
	if _, ok := by["text-embedding-3-small"]; ok {
		t.Fatal("embedding model must be dropped")
	}
	for _, m := range ms {
		if !m.Enabled || !m.IsDefault {
			t.Errorf("model %+v should be enabled and default", m)
		}
	}
	wantOneDefaultPerCap(t, ms)
	if *e.changed < 2 {
		t.Fatalf("OnChanged calls = %d", *e.changed)
	}
	if checkingOf(t, e, c.ID) {
		t.Fatal("still checking after Wait")
	}
}

func TestAutoCheckReportsCheckingWhileInFlight(t *testing.T) {
	var hits atomic.Int32
	entered, release := make(chan struct{}, 4), make(chan struct{})
	srv := acModelsServer(t, &hits, func() { entered <- struct{}{}; <-release }, fixedList("gpt-4o"))
	e := newEnvAuto(t, 5*time.Second, true)
	c := e.create(t, domain.IDOpenAI, openaiIn(srv, probeKey))
	var once sync.Once
	rel := func() { once.Do(func() { close(release) }) }
	t.Cleanup(rel)

	<-entered
	if !checkingOf(t, e, c.ID) {
		t.Fatal("expected Checking=true while the request is blocked")
	}
	rel()
	e.svc.Wait()
	if checkingOf(t, e, c.ID) {
		t.Fatal("Checking should be false after Wait")
	}
	if h := credHealth(t, e, c.ID); h.Outcome != domain.OutcomeOK {
		t.Fatalf("health = %+v", h)
	}
}

func TestAutoCheckDeadKeyAddsNothing(t *testing.T) {
	var hits atomic.Int32
	srv := acModelsServer(t, &hits, nil, func(string) (int, []string) { return 401, nil })
	e := newEnvAuto(t, 2*time.Second, true)
	ctx := context.Background()
	if _, err := e.svc.SetModels(ctx, domain.IDOpenAI, []domain.Model{
		{ModelID: "gpt-old", Capability: domain.CapabilityLLM, Enabled: false, UserAdded: true}}); err != nil {
		t.Fatal(err)
	}
	c := e.create(t, domain.IDOpenAI, openaiIn(srv, probeKey))
	e.svc.Wait()

	h := credHealth(t, e, c.ID)
	if h.Outcome != domain.OutcomeAuthFailed || !h.Outcome.Dead() {
		t.Fatalf("health = %+v", h)
	}
	ms, _ := e.repo.ListModels(ctx, domain.IDOpenAI)
	if len(ms) != 1 || ms[0].ModelID != "gpt-old" || ms[0].Enabled {
		t.Fatalf("models = %+v", ms)
	}
	if n := hits.Load(); n != 1 {
		t.Fatalf("server hits = %d, want 1 (test only, no discovery)", n)
	}
}

func TestAutoCheckUpdateCredential(t *testing.T) {
	var hits atomic.Int32
	srv := acModelsServer(t, &hits, nil, fixedList("gpt-4o"))
	e := newEnvAuto(t, 2*time.Second, true)
	ctx := context.Background()
	c := e.create(t, domain.IDOpenAI, openaiIn(srv, probeKey))
	e.svc.Wait()
	before := hits.Load()
	vals := map[string]string{"base_url": srv.URL + "/v1"}

	// name only: no re-check, health kept
	if _, err := e.svc.UpdateCredential(ctx, c.ID, domain.Input{Name: "renamed", Values: vals}); err != nil {
		t.Fatal(err)
	}
	e.svc.Wait()
	if hits.Load() != before {
		t.Fatalf("rename triggered a check: %d -> %d", before, hits.Load())
	}
	if h := credHealth(t, e, c.ID); h.Outcome != domain.OutcomeOK {
		t.Fatalf("health after rename = %+v", h)
	}

	// new secret: health reset, then a fresh check
	if _, err := e.svc.UpdateCredential(ctx, c.ID, domain.Input{Name: "renamed", Values: vals,
		Secrets: map[string]string{"api_key": "another-key-ABCDEFGH-999"}}); err != nil {
		t.Fatal(err)
	}
	e.svc.Wait()
	if hits.Load() <= before {
		t.Fatalf("new secret did not trigger a check (hits %d)", hits.Load())
	}
	if h := credHealth(t, e, c.ID); h.Outcome != domain.OutcomeOK || h.TestedAt.IsZero() {
		t.Fatalf("health after new secret = %+v", h)
	}
}

func TestAutoCheckActivateMergesNewKeyModels(t *testing.T) {
	const keyB = "second-key-ABCDEFGH-222"
	var hits atomic.Int32
	srv := acModelsServer(t, &hits, nil, func(key string) (int, []string) {
		if key == keyB {
			return 200, []string{"gpt-4o", "gpt-5", "gpt-image-2"}
		}
		return 200, []string{"gpt-4o", "gpt-image-1"}
	})
	e := newEnvAuto(t, 2*time.Second, true)
	ctx := context.Background()
	e.create(t, domain.IDOpenAI, openaiIn(srv, probeKey))
	e.svc.Wait()
	b := e.create(t, domain.IDOpenAI, openaiIn(srv, keyB))
	e.svc.Wait()

	// B is not active: tested, but its models are not merged
	if credHealth(t, e, b.ID).Outcome != domain.OutcomeOK {
		t.Fatal("inactive key must still be tested")
	}
	ms, _ := e.repo.ListModels(ctx, domain.IDOpenAI)
	if _, ok := modelIDs(ms)["gpt-5"]; ok {
		t.Fatalf("models of an inactive key were merged: %+v", ms)
	}

	// user choices: disable gpt-image-1, custom display name on gpt-4o
	for i := range ms {
		if ms[i].ModelID == "gpt-image-1" {
			ms[i].Enabled, ms[i].IsDefault = false, false
		}
		if ms[i].ModelID == "gpt-4o" {
			ms[i].DisplayName = "My 4o"
		}
	}
	if _, err := e.svc.SetModels(ctx, domain.IDOpenAI, ms); err != nil {
		t.Fatal(err)
	}

	if err := e.svc.ActivateCredential(ctx, b.ID); err != nil {
		t.Fatal(err)
	}
	e.svc.Wait()
	ms, _ = e.repo.ListModels(ctx, domain.IDOpenAI)
	by := modelIDs(ms)
	for _, id := range []string{"gpt-4o", "gpt-5", "gpt-image-1", "gpt-image-2"} {
		if _, ok := by[id]; !ok {
			t.Fatalf("missing %s in %+v", id, ms)
		}
	}
	if by["gpt-image-1"].Enabled {
		t.Fatal("user-disabled model was re-enabled")
	}
	if by["gpt-4o"].DisplayName != "My 4o" {
		t.Fatal("display name was overwritten")
	}
	if !by["gpt-5"].Enabled || !by["gpt-image-2"].Enabled {
		t.Fatalf("new models should be enabled: %+v", ms)
	}
	wantOneDefaultPerCap(t, ms)
}

func TestAutoCheckNonDiscoverableOnlyTests(t *testing.T) {
	const ak, sk = "ak-ABCDEFGH1234", "sk-ABCDEFGH5678"
	var costs, other atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/account/costs" {
			costs.Add(1)
			writeJSON(w, 200, map[string]any{"code": 0})
			return
		}
		other.Add(1)
		http.NotFound(w, r)
	}))
	defer srv.Close()
	e := newEnvAuto(t, 2*time.Second, true)
	c := e.create(t, domain.IDKling, domain.Input{Values: map[string]string{"base_url": srv.URL},
		Secrets: map[string]string{"access_key": ak, "secret_key": sk}})
	e.svc.Wait()
	if h := credHealth(t, e, c.ID); h.Outcome != domain.OutcomeOK {
		t.Fatalf("health = %+v", h)
	}
	if costs.Load() != 1 || other.Load() != 0 {
		t.Fatalf("costs=%d other=%d", costs.Load(), other.Load())
	}
	if ms, _ := e.repo.ListModels(context.Background(), domain.IDKling); len(ms) != 0 {
		t.Fatalf("models = %+v", ms)
	}
}

func TestAutoCheckCustomProvider(t *testing.T) {
	var hits atomic.Int32
	srv := acModelsServer(t, &hits, nil, fixedList("gpt-4o", "gpt-image-1", "tts-1"))
	e := newEnvAuto(t, 2*time.Second, true)
	ctx := context.Background()
	p, err := e.svc.CreateCustom(ctx, domain.CustomInput{Name: "gw", DiscoveryFormat: domain.DiscoveryOpenAI,
		BaseURL: srv.URL, APIKey: probeKey})
	if err != nil {
		t.Fatal(err)
	}
	e.svc.Wait()
	got, err := e.repo.GetCustom(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Health.Outcome != domain.OutcomeOK {
		t.Fatalf("health = %+v", got.Health)
	}
	if len(got.Models) != 3 {
		t.Fatalf("models = %+v", got.Models)
	}
	for _, m := range got.Models {
		if !m.Enabled {
			t.Errorf("%+v not enabled", m)
		}
	}
	wantOneDefaultPerCap(t, got.Models)

	snap, err := e.svc.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(snap)
	if strings.Contains(string(raw), probeKey) || strings.Contains(fmt.Sprintf("%+v", snap), probeKey) {
		t.Fatal("snapshot leaks the api key")
	}
	if snap.Custom[0].Checking {
		t.Fatal("custom still checking")
	}
}

func TestAutoCheckRerunsExactlyOnce(t *testing.T) {
	var hits atomic.Int32
	entered, release := make(chan struct{}, 16), make(chan struct{})
	var first sync.Once
	srv := acModelsServer(t, &hits, func() {
		entered <- struct{}{}
		first.Do(func() { <-release }) // only the first request blocks
	}, fixedList("gpt-4o"))
	e := newEnvAuto(t, 5*time.Second, true)
	ctx := context.Background()
	c := e.create(t, domain.IDOpenAI, openaiIn(srv, probeKey))
	var once sync.Once
	rel := func() { once.Do(func() { close(release) }) }
	t.Cleanup(rel)
	<-entered

	for _, k := range []string{"new-key-ABCDEFGH-0001", "new-key-ABCDEFGH-0002"} {
		if _, err := e.svc.UpdateCredential(ctx, c.ID, domain.Input{Values: map[string]string{"base_url": srv.URL + "/v1"},
			Secrets: map[string]string{"api_key": k}}); err != nil {
			t.Fatal(err)
		}
	}
	rel()
	e.svc.Wait()

	// one check = test (1 request) + discovery (1 request); two checks = 4 requests.
	if n := hits.Load(); n != 4 {
		t.Fatalf("server hits = %d, want 4 (running check + exactly one rerun)", n)
	}
	if h := credHealth(t, e, c.ID); h.Outcome != domain.OutcomeOK {
		t.Fatalf("health = %+v", h)
	}
}

func TestAutoCheckOffMakesNoRequest(t *testing.T) {
	var hits atomic.Int32
	srv := acModelsServer(t, &hits, nil, fixedList("gpt-4o"))
	e := newEnv(t, 2*time.Second)
	c := e.create(t, domain.IDOpenAI, openaiIn(srv, probeKey))
	if checkingOf(t, e, c.ID) {
		t.Fatal("Checking must stay false with AutoCheck off")
	}
	e.svc.Wait()
	if hits.Load() != 0 {
		t.Fatalf("server hits = %d", hits.Load())
	}
	if h := credHealth(t, e, c.ID); !h.TestedAt.IsZero() {
		t.Fatalf("health = %+v", h)
	}
}

func TestAutoCheckCloseCancelsInFlight(t *testing.T) {
	var hits atomic.Int32
	entered, release := make(chan struct{}, 16), make(chan struct{})
	srv := acModelsServer(t, &hits, func() { entered <- struct{}{}; <-release }, fixedList("gpt-4o"))
	e := newEnvAuto(t, 10*time.Second, true)
	e.create(t, domain.IDOpenAI, openaiIn(srv, probeKey))
	var once sync.Once
	rel := func() { once.Do(func() { close(release) }) }
	t.Cleanup(rel)
	<-entered

	done := make(chan struct{})
	go func() { e.svc.Close(); close(done) }()
	// Close cancels the context, so the client gives up even though the handler still blocks.
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Close did not return while a check was blocked")
	}
	rel()
	n := hits.Load()
	time.Sleep(200 * time.Millisecond)
	if hits.Load() != n {
		t.Fatalf("requests arrived after Close: %d -> %d", n, hits.Load())
	}
}

func TestCheckAllRefreshesActiveOnly(t *testing.T) {
	const keyB = "second-key-ABCDEFGH-222"
	var hits atomic.Int32
	var mu sync.Mutex
	keys := map[string]int{}
	srv := acModelsServer(t, &hits, nil, func(key string) (int, []string) {
		mu.Lock()
		keys[key]++
		mu.Unlock()
		if key == keyB {
			return 200, []string{"only-b-model"}
		}
		return 200, []string{"gpt-4o"}
	})
	e := newEnv(t, 2*time.Second)
	ctx := context.Background()
	a := e.create(t, domain.IDOpenAI, openaiIn(srv, probeKey))
	b := e.create(t, domain.IDOpenAI, openaiIn(srv, keyB))
	cp, err := e.svc.CreateCustom(ctx, domain.CustomInput{Name: "gw", DiscoveryFormat: domain.DiscoveryOpenAI, BaseURL: srv.URL, APIKey: probeKey})
	if err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 0 {
		t.Fatal("auto off: no requests expected before CheckAll")
	}

	e.svc.CheckAll(ctx, 2)

	if credHealth(t, e, a.ID).Outcome != domain.OutcomeOK || credHealth(t, e, b.ID).Outcome != domain.OutcomeOK {
		t.Fatal("both credentials should be tested")
	}
	ms, _ := e.repo.ListModels(ctx, domain.IDOpenAI)
	by := modelIDs(ms)
	if _, ok := by["gpt-4o"]; !ok {
		t.Fatalf("active key models missing: %+v", ms)
	}
	if _, ok := by["only-b-model"]; ok {
		t.Fatalf("inactive key models merged: %+v", ms)
	}
	mu.Lock()
	defer mu.Unlock()
	if keys[keyB] != 1 { // tested once, never used to list models
		t.Fatalf("inactive key requests = %d, want 1", keys[keyB])
	}
	got, _ := e.repo.GetCustom(ctx, cp.ID)
	if got.Health.Outcome != domain.OutcomeOK || len(got.Models) != 1 || got.Models[0].ModelID != "gpt-4o" {
		t.Fatalf("custom = %+v", got)
	}
}

func TestAutoCheckUpdateCustomRechecks(t *testing.T) {
	var hits atomic.Int32
	srv := acModelsServer(t, &hits, nil, fixedList("gpt-4o"))
	e := newEnvAuto(t, 2*time.Second, true)
	ctx := context.Background()
	in := domain.CustomInput{Name: "gw", DiscoveryFormat: domain.DiscoveryOpenAI, BaseURL: srv.URL, APIKey: probeKey}
	p, err := e.svc.CreateCustom(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	e.svc.Wait()
	before := hits.Load()
	in.APIKey = ""
	in.Name = "renamed"
	if _, err := e.svc.UpdateCustom(ctx, p.ID, in); err != nil {
		t.Fatal(err)
	}
	e.svc.Wait()
	if hits.Load() != before {
		t.Fatal("rename-only update must not re-check")
	}
	in.APIKey = "different-key-ABCDEFGH-1"
	if _, err := e.svc.UpdateCustom(ctx, p.ID, in); err != nil {
		t.Fatal(err)
	}
	e.svc.Wait()
	if hits.Load() <= before {
		t.Fatal("new key must trigger a check")
	}
}
