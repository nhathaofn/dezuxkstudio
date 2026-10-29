// Package middleware contains composable HTTP middlewares.
package middleware

import (
	"mime"
	"net/http"

	"app/internal/security"
)

// Middleware wraps an http.Handler.
type Middleware func(http.Handler) http.Handler

// CORS echoes allowed origins and rejects preflights from other origins.
func CORS(allowed []string) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin != "" {
				if !security.OriginAllowed(origin, allowed) {
					http.Error(w, "origin not allowed", http.StatusForbidden)
					return
				}
				h := w.Header()
				h.Set("Access-Control-Allow-Origin", origin)
				h.Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
				h.Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
				h.Add("Vary", "Origin")
			}
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RequireToken accepts only "Authorization: Bearer <token>". An empty configured
// token rejects every request (fail closed).
func RequireToken(token string) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !security.TokenEqual(token, security.BearerToken(r)) {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RequireJSON rejects bodies that are not application/json, which also forces a
// CORS preflight for cross-origin browser requests (no "simple request" CSRF).
func RequireJSON(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
			if err != nil || mt != "application/json" {
				http.Error(w, "content type must be application/json", http.StatusUnsupportedMediaType)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
