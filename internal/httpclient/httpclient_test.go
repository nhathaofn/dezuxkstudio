package httpclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"app/internal/config"
)

func TestNewBlocksLinkLocalDial(t *testing.T) {
	c := New(config.NetworkConfig{MaxIdleConns: 4, MaxIdleConnsPerHost: 2, IdleConnTimeout: time.Second})
	if err := rejectLinkLocalControl("tcp", "169.254.169.254:80", nil); err == nil {
		t.Fatal("expected 169.254.169.254 to be blocked")
	}
	if err := rejectLinkLocalControl("tcp", "[fe80::1]:80", nil); err == nil {
		t.Fatal("expected fe80::1 to be blocked")
	}
	if err := rejectLinkLocalControl("tcp", "0.0.0.0:80", nil); err == nil {
		t.Fatal("expected 0.0.0.0 to be blocked")
	}
	if err := rejectLinkLocalControl("tcp", "127.0.0.1:8080", nil); err != nil {
		t.Fatalf("expected loopback 127.0.0.1 to remain allowed, got %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("expected loopback httptest server to succeed, got %v", err)
	}
	resp.Body.Close()
}
