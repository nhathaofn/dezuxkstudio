// Package middleware contains composable HTTP middlewares.
package middleware

import (
	"bufio"
	"errors"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"regexp"
	"time"

	"app/internal/security"
)

var (
	ticketPattern = regexp.MustCompile(`(?i)(ticket=)[^&; #]*`)
	tokenPattern  = regexp.MustCompile(`(?i)(token=)[^&; #]*`)
)

// SanitizeURI redacts sensitive query parameters like ticket and token from a request URI.
func SanitizeURI(uri string) string {
	redacted := ticketPattern.ReplaceAllString(uri, "${1}[REDACTED]")
	redacted = tokenPattern.ReplaceAllString(redacted, "${1}[REDACTED]")
	return redacted
}

// RedactTicket redacts the ticket query parameter from a URI or URL string.
func RedactTicket(uri string) string {
	return ticketPattern.ReplaceAllString(uri, "${1}[REDACTED]")
}

type responseRecorder struct {
	http.ResponseWriter
	status int
}

func (r *responseRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (r *responseRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hj, ok := r.ResponseWriter.(http.Hijacker); ok {
		return hj.Hijack()
	}
	return nil, nil, errors.New("underlying ResponseWriter does not implement http.Hijacker")
}

func (r *responseRecorder) Flush() {
	if fl, ok := r.ResponseWriter.(http.Flusher); ok {
		fl.Flush()
	}
}

func (r *responseRecorder) Unwrap() http.ResponseWriter {
	return r.ResponseWriter
}

// RequestLogger logs incoming HTTP requests using slog, redacting sensitive parameters
// like ticket= and token= from the request URI before outputting to logs.
func RequestLogger(log *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if log == nil {
				next.ServeHTTP(w, r)
				return
			}
			start := time.Now()
			rec := &responseRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rec, r)

			uri := r.RequestURI
			if uri == "" {
				uri = r.URL.String()
			}
			sanitizedURI := SanitizeURI(uri)

			log.Info("http request",
				"method", r.Method,
				"uri", sanitizedURI,
				"status", rec.status,
				"duration", time.Since(start),
			)
		})
	}
}

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
				h.Set("Access-Control-Expose-Headers", "Content-Disposition")
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

// RequireAuth enforces Bearer authentication unless allowAnonymous is true and token is empty.
func RequireAuth(token string, allowAnonymous bool) Middleware {
	if token == "" && allowAnonymous {
		return func(next http.Handler) http.Handler { return next }
	}
	return RequireToken(token)
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
