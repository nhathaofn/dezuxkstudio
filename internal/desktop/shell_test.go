package desktop

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"app/internal/config"
)

func TestContentSecurityPolicyAndMiddleware(t *testing.T) {
	cfg := config.Default(config.ModeDesktop)
	cfg.Server.Host = "127.0.0.1"
	cfg.Server.Port = 8080

	csp := contentSecurityPolicy(cfg)
	if !strings.Contains(csp, "style-src 'self'; style-src-attr 'unsafe-inline'") {
		t.Fatalf("expected hardened style-src CSP in desktop mode, got: %s", csp)
	}
	if !strings.Contains(csp, "ws://127.0.0.1:8080") || !strings.Contains(csp, "http://127.0.0.1:8080") {
		t.Fatalf("expected tightly pinned loopback port in CSP, got: %s", csp)
	}

	// Adversarial remote URL with semicolon/whitespace injection in Client mode must be dropped from CSP.
	clientCfg := config.Default(config.ModeClient)
	clientCfg.Client.RemoteURL = "wss://evil.com;script-src 'unsafe-inline'"
	clientCSP := contentSecurityPolicy(clientCfg)
	if strings.Contains(clientCSP, "evil.com") {
		t.Fatalf("CSP header injection was not stripped: %s", clientCSP)
	}

	// Middleware wraps HTTP response with Content-Security-Policy & X-Content-Type-Options.
	mw := securityHeaders(cfg)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok")
	}))
	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))
	if rr.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("missing nosniff header")
	}
	if rr.Header().Get("Content-Security-Policy") == "" {
		t.Fatalf("missing CSP response header")
	}
}
