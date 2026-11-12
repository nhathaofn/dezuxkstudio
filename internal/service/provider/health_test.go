package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	domain "app/internal/domain/provider"
)

func TestCheckAllRecordsHealthAndResetsOnKeyChange(t *testing.T) {
	ctx := context.Background()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer good-key-1234567" {
			_, _ = w.Write([]byte(`{"data":[{"id":"gpt-4o"}]}`))
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	e := newEnv(t, 2*time.Second)

	good, err := e.svc.CreateCredential(ctx, domain.IDOpenAI, domain.Input{Values: map[string]string{"base_url": srv.URL},
		Secrets: map[string]string{"api_key": "good-key-1234567"}})
	if err != nil {
		t.Fatal(err)
	}
	bad, err := e.svc.CreateCredential(ctx, domain.IDOpenAI, domain.Input{Values: map[string]string{"base_url": srv.URL},
		Secrets: map[string]string{"api_key": "bad-key-12345678"}})
	if err != nil {
		t.Fatal(err)
	}
	lost, err := e.svc.CreateCredential(ctx, domain.IDOpenAI, domain.Input{Values: map[string]string{"base_url": srv.URL},
		Secrets: map[string]string{"api_key": "lost-key-1234567"}})
	if err != nil {
		t.Fatal(err)
	}
	_ = e.store.Delete(credSecretKey(lost.ID, "api_key"))

	e.svc.CheckAll(ctx, 2)

	get := func(id string) domain.Health {
		c, err := e.repo.GetCredential(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		return c.Health
	}
	if h := get(good.ID); h.Outcome != domain.OutcomeOK || h.TestedAt.IsZero() {
		t.Fatalf("good: %+v", h)
	}
	if h := get(bad.ID); h.Outcome != domain.OutcomeAuthFailed || !h.Outcome.Dead() {
		t.Fatalf("bad: %+v", h)
	}
	if h := get(lost.ID); h.Outcome != domain.OutcomeSecretMissing || !h.Outcome.Dead() {
		t.Fatalf("lost: %+v", h)
	}
	if _, err := e.svc.UpdateCredential(ctx, bad.ID, domain.Input{Values: map[string]string{"base_url": srv.URL},
		Secrets: map[string]string{"api_key": "good-key-1234567"}}); err != nil {
		t.Fatal(err)
	}
	if h := get(bad.ID); !h.TestedAt.IsZero() || h.Outcome != "" {
		t.Fatalf("health must reset after key change: %+v", h)
	}
}

func TestCheckAllStopsOnCancel(t *testing.T) {
	e := newEnv(t, time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	e.svc.CheckAll(ctx, 1) // must return promptly with nothing to do or a cancelled context
}
