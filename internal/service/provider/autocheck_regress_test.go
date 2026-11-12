package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	domain "app/internal/domain/provider"
)

// The connectivity test and the model list must read the SAME URL. A base URL without /v1 used to pass
// the test (GET /models) while the list was read from /v1/models: "key works", empty model list, no error.
func TestAutoCheckListsModelsAtTheSameURLAsTheTest(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if r.URL.Path != "/models" {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, 200, map[string]any{"data": []map[string]string{{"id": "gpt-4o"}, {"id": "gpt-image-1"}}})
	}))
	t.Cleanup(srv.Close)

	e := newEnvAuto(t, 2*time.Second, true)
	c := e.create(t, domain.IDAgnes, domain.Input{Values: map[string]string{"base_url": srv.URL}, Secrets: map[string]string{"api_key": probeKey}})
	e.svc.Wait()

	for _, p := range paths {
		if p != "/models" {
			t.Fatalf("requests must all go to /models, got %v", paths)
		}
	}
	models, _ := e.repo.ListModels(context.Background(), domain.IDAgnes)
	if len(models) != 2 {
		t.Fatalf("models = %+v", models)
	}
	got, _ := e.repo.GetCredential(context.Background(), c.ID)
	if got.Health.Outcome != domain.OutcomeOK {
		t.Fatalf("health = %+v", got.Health)
	}
}

// A key that works but whose model list cannot be loaded must say so (not look healthy with an empty list).
func TestAutoCheckReportsModelListFailure(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) == 1 { // the connectivity test succeeds ...
			writeJSON(w, 200, map[string]any{"data": []map[string]string{{"id": "gpt-4o"}}})
			return
		}
		http.Error(w, "upstream exploded "+probeKey, http.StatusInternalServerError) // ... the model list does not
	}))
	t.Cleanup(srv.Close)

	e := newEnvAuto(t, 2*time.Second, true)
	c := e.create(t, domain.IDAgnes, domain.Input{Values: map[string]string{"base_url": srv.URL}, Secrets: map[string]string{"api_key": probeKey}})
	e.svc.Wait()

	got, _ := e.repo.GetCredential(context.Background(), c.ID)
	if got.Health.Outcome != domain.OutcomeModelsFailed {
		t.Fatalf("outcome = %q, want models_failed", got.Health.Outcome)
	}
	if got.Health.Outcome.Dead() {
		t.Fatal("a working key must not be reported dead")
	}
	if got.Health.Detail == "" || strings.Contains(got.Health.Detail, probeKey) {
		t.Fatalf("detail must explain the failure without leaking the key: %q", got.Health.Detail)
	}
	if models, _ := e.repo.ListModels(context.Background(), domain.IDAgnes); len(models) != 0 {
		t.Fatalf("no models expected, got %+v", models)
	}
	snap, _ := e.svc.List(context.Background())
	for _, b := range snap.Builtins {
		if b.Meta.ID == domain.IDAgnes && !b.Configured() {
			t.Fatal("a key with a failed model list is still configured")
		}
	}
}
