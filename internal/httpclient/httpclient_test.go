package httpclient

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
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

func TestWithRedirectGuardNilBase(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/start":
			http.Redirect(w, r, "/ok", http.StatusFound)
		case "/start-blocked":
			http.Redirect(w, r, "/blocked", http.StatusFound)
		case "/ok":
			w.WriteHeader(http.StatusOK)
		default:
			t.Errorf("unexpected request to %s", r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer srv.Close()

	c := WithRedirectGuard(nil, func(raw string) error {
		if strings.HasSuffix(raw, "/blocked") {
			return errors.New("disallowed path")
		}
		return nil
	})
	if c == nil {
		t.Fatal("expected non-nil client for nil base")
	}

	resp, err := c.Get(srv.URL + "/start")
	if err != nil {
		t.Fatalf("expected allowed redirect to succeed, got %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	resp, err = c.Get(srv.URL + "/start-blocked")
	if err == nil {
		resp.Body.Close()
		t.Fatal("expected blocked redirect to fail")
	}
	if !strings.Contains(err.Error(), "blocked redirect target") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestWithRedirectGuardBlocksValidationTargetAndClonesBase(t *testing.T) {
	var blockedTargetHit atomic.Bool
	blockedSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		blockedTargetHit.Store(true)
		w.WriteHeader(http.StatusOK)
	}))
	defer blockedSrv.Close()

	srcSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, blockedSrv.URL+"/secret", http.StatusFound)
	}))
	defer srcSrv.Close()

	sentinel := errors.New("unsafe target")
	base := &http.Client{Timeout: 7 * time.Second}
	guarded := WithRedirectGuard(base, func(raw string) error {
		if strings.HasPrefix(raw, blockedSrv.URL) {
			return sentinel
		}
		return nil
	})

	if base.CheckRedirect != nil {
		t.Fatal("expected base.CheckRedirect to remain unmodified")
	}
	if guarded.Timeout != 7*time.Second {
		t.Fatalf("guarded.Timeout = %v, want 7s", guarded.Timeout)
	}

	resp, err := guarded.Get(srcSrv.URL)
	if err == nil {
		resp.Body.Close()
		t.Fatal("expected redirect to blockedSrv to fail")
	}
	if !errors.Is(err, sentinel) || !strings.Contains(err.Error(), "blocked redirect target:") {
		t.Fatalf("expected wrapped sentinel error, got %v", err)
	}
	if blockedTargetHit.Load() {
		t.Fatal("blocked redirect target must not be dialed")
	}
}

func TestWithRedirectGuardPrevChaining(t *testing.T) {
	var seenHeader atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/hop":
			http.Redirect(w, r, "/final", http.StatusFound)
		case "/hop-blocked":
			http.Redirect(w, r, "/forbidden", http.StatusFound)
		case "/final":
			seenHeader.Store(r.Header.Get("X-Prev-Chained"))
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer srv.Close()

	var prevCalls atomic.Int32
	base := &http.Client{
		CheckRedirect: func(req *http.Request, _ []*http.Request) error {
			prevCalls.Add(1)
			req.Header.Set("X-Prev-Chained", "yes")
			return nil
		},
	}
	guarded := WithRedirectGuard(base, func(raw string) error {
		if strings.HasSuffix(raw, "/forbidden") {
			return errors.New("blocked by validator")
		}
		return nil
	})

	resp, err := guarded.Get(srv.URL + "/hop")
	if err != nil {
		t.Fatalf("expected chained redirect to succeed, got %v", err)
	}
	resp.Body.Close()
	if prevCalls.Load() != 1 {
		t.Fatalf("prevCalls = %d, want 1", prevCalls.Load())
	}
	if got, _ := seenHeader.Load().(string); got != "yes" {
		t.Fatalf("X-Prev-Chained = %q, want yes", got)
	}

	// Validator failure must short-circuit before prev runs.
	resp, err = guarded.Get(srv.URL + "/hop-blocked")
	if err == nil {
		resp.Body.Close()
		t.Fatal("expected validator failure to block redirect")
	}
	if prevCalls.Load() != 1 {
		t.Fatalf("prevCalls changed to %d after blocked target, want 1", prevCalls.Load())
	}

	// prev returning http.ErrUseLastResponse must be preserved.
	stopClient := WithRedirectGuard(&http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}, func(string) error { return nil })

	resp, err = stopClient.Get(srv.URL + "/hop")
	if err != nil {
		t.Fatalf("expected ErrUseLastResponse to return 302 without error, got %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status = %d, want 302", resp.StatusCode)
	}
}

func TestWithRedirectGuardTenHopCap(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/chain/"):
			var step int
			_, _ = fmt.Sscanf(r.URL.Path, "/chain/%d", &step)
			if step < 9 {
				http.Redirect(w, r, fmt.Sprintf("/chain/%d", step+1), http.StatusFound)
				return
			}
			w.WriteHeader(http.StatusOK)
		case r.URL.Path == "/loop":
			hits.Add(1)
			http.Redirect(w, r, "/loop", http.StatusFound)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	for _, tc := range []struct {
		name string
		base *http.Client
	}{
		{name: "nil-prev", base: &http.Client{}},
		{name: "permissive-prev", base: &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return nil }}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hits.Store(0)
			c := WithRedirectGuard(tc.base, func(string) error { return nil })

			// 9 redirects (10 total requests) remain within the cap.
			resp, err := c.Get(srv.URL + "/chain/0")
			if err != nil {
				t.Fatalf("expected 9 redirects to succeed, got %v", err)
			}
			resp.Body.Close()

			// >=10 redirects must stop at 10.
			resp, err = c.Get(srv.URL + "/loop")
			if err == nil {
				resp.Body.Close()
				t.Fatal("expected redirect loop to stop after 10 redirects")
			}
			if !strings.Contains(err.Error(), "stopped after 10 redirects") {
				t.Fatalf("unexpected error: %v", err)
			}
			if hits.Load() != 10 {
				t.Fatalf("hits = %d, want 10", hits.Load())
			}
		})
	}
}
