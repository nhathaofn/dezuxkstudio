package middleware

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

var ok = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

func serve(h http.Handler, method, origin, contentType, auth string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "/", strings.NewReader("{}"))
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	if contentType != "" {
		r.Header.Set("Content-Type", contentType)
	}
	if auth != "" {
		r.Header.Set("Authorization", "Bearer "+auth)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestCORS(t *testing.T) {
	h := CORS([]string{"wails://wails"})(ok)
	if w := serve(h, "GET", "wails://wails", "", ""); w.Code != 200 || w.Header().Get("Access-Control-Allow-Origin") != "wails://wails" {
		t.Errorf("allowed origin: %d %v", w.Code, w.Header())
	}
	if w := serve(h, "GET", "https://evil.com", "", ""); w.Code != http.StatusForbidden {
		t.Errorf("foreign origin: %d", w.Code)
	}
	if w := serve(h, "OPTIONS", "wails://wails", "", ""); w.Code != http.StatusNoContent {
		t.Errorf("preflight: %d", w.Code)
	}
	if w := serve(h, "GET", "", "", ""); w.Code != 200 {
		t.Errorf("same-origin/non-browser: %d", w.Code)
	}
}

func TestRequireTokenFailsClosed(t *testing.T) {
	if w := serve(RequireToken("")(ok), "POST", "", "", ""); w.Code != http.StatusUnauthorized {
		t.Errorf("empty token must reject: %d", w.Code)
	}
	if w := serve(RequireToken("t")(ok), "POST", "", "", "x"); w.Code != http.StatusUnauthorized {
		t.Errorf("wrong token: %d", w.Code)
	}
	if w := serve(RequireToken("t")(ok), "POST", "", "", "t"); w.Code != 200 {
		t.Errorf("right token: %d", w.Code)
	}
}

func TestRequireAuth(t *testing.T) {
	if w := serve(RequireAuth("", false)(ok), "POST", "", "", ""); w.Code != http.StatusUnauthorized {
		t.Errorf("empty token without anonymous must fail closed: %d", w.Code)
	}
	if w := serve(RequireAuth("", true)(ok), "POST", "", "", ""); w.Code != http.StatusOK {
		t.Errorf("anonymous without configured token should pass: %d", w.Code)
	}
	if w := serve(RequireAuth("secret", true)(ok), "POST", "", "", ""); w.Code != http.StatusUnauthorized {
		t.Errorf("configured token must still be enforced even if anonymous flag set: %d", w.Code)
	}
	if w := serve(RequireAuth("secret", true)(ok), "POST", "", "", "secret"); w.Code != http.StatusOK {
		t.Errorf("valid token should pass: %d", w.Code)
	}
}

func TestRequireJSON(t *testing.T) {
	if w := serve(RequireJSON(ok), "POST", "", "text/plain", ""); w.Code != http.StatusUnsupportedMediaType {
		t.Errorf("text/plain: %d", w.Code)
	}
	if w := serve(RequireJSON(ok), "POST", "", "application/json; charset=utf-8", ""); w.Code != 200 {
		t.Errorf("json: %d", w.Code)
	}
}

func TestSanitizeURI(t *testing.T) {
	cases := []struct {
		input    string
		expected string
	}{
		{
			input:    "/api/media/download?asset_id=123&ticket=secret_ticket_123&purpose=preview",
			expected: "/api/media/download?asset_id=123&ticket=[REDACTED]&purpose=preview",
		},
		{
			input:    "/api/media/download?ticket=raw.ticket.signature",
			expected: "/api/media/download?ticket=[REDACTED]",
		},
		{
			input:    "/api/media/download?ticket=",
			expected: "/api/media/download?ticket=[REDACTED]",
		},
		{
			input:    "/api/events/broadcast?token=master_secret_val",
			expected: "/api/events/broadcast?token=[REDACTED]",
		},
		{
			input:    "/api/media/download?asset_id=xyz&token=secret&ticket=signature",
			expected: "/api/media/download?asset_id=xyz&token=[REDACTED]&ticket=[REDACTED]",
		},
		{
			input:    "/api/media/download?path=normal/path.png",
			expected: "/api/media/download?path=normal/path.png",
		},
	}

	for _, tc := range cases {
		got := SanitizeURI(tc.input)
		if got != tc.expected {
			t.Errorf("SanitizeURI(%q) = %q; want %q", tc.input, got, tc.expected)
		}
	}
}

func TestRequestLoggerRedactsTicketAndToken(t *testing.T) {
	var buf strings.Builder
	log := slog.New(slog.NewTextHandler(&buf, nil))

	handler := RequestLogger(log)(ok)

	rawURI := "/api/media/download?asset_id=test-123&ticket=sensitive-hmac-ticket&token=secret-token"
	req := httptest.NewRequest(http.MethodGet, rawURI, nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	logged := buf.String()
	if strings.Contains(logged, "sensitive-hmac-ticket") {
		t.Fatalf("logger leaked raw ticket string: %s", logged)
	}
	if strings.Contains(logged, "secret-token") {
		t.Fatalf("logger leaked raw token string: %s", logged)
	}
	if !strings.Contains(logged, "ticket=[REDACTED]") {
		t.Fatalf("logger did not contain ticket=[REDACTED]: %s", logged)
	}
	if !strings.Contains(logged, "token=[REDACTED]") {
		t.Fatalf("logger did not contain token=[REDACTED]: %s", logged)
	}
}
