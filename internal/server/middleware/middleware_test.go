package middleware

import (
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

func TestRequireJSON(t *testing.T) {
	if w := serve(RequireJSON(ok), "POST", "", "text/plain", ""); w.Code != http.StatusUnsupportedMediaType {
		t.Errorf("text/plain: %d", w.Code)
	}
	if w := serve(RequireJSON(ok), "POST", "", "application/json; charset=utf-8", ""); w.Code != 200 {
		t.Errorf("json: %d", w.Code)
	}
}
