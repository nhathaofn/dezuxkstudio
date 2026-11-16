package provider

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	domain "app/internal/domain/provider"
)

func TestProbeLatencyMeasurement(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(10 * time.Millisecond)
		writeJSON(w, 200, map[string]any{"data": []map[string]string{{"id": "gpt-4o"}}})
	}))
	defer srv.Close()

	e := newEnv(t, 2*time.Second)
	res := e.testCred(t, domain.IDOpenAI, srv.URL+"/v1", map[string]string{"api_key": probeKey})
	if !res.OK() {
		t.Fatalf("expected OK, got outcome %v, detail: %s", res.Outcome, res.Detail)
	}
	if res.LatencyMs < 10 {
		t.Fatalf("expected LatencyMs >= 10ms, got %dms", res.LatencyMs)
	}
}
