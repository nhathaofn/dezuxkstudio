package desktop

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/wailsapp/wails/v2/pkg/options/assetserver"

	"app/internal/buildinfo"
	"app/internal/config"
)

// contentSecurityPolicy builds the CSP for the webview. connect-src is derived
// from the mode so client mode can reach its configured remote server and nothing else.
func contentSecurityPolicy(cfg *config.Config) string {
	connect := []string{"'self'"}
	script := []string{"'self'"}

	switch cfg.Mode {
	case config.ModeClient:
		if u, err := url.Parse(cfg.Client.RemoteURL); err == nil && u.Host != "" {
			connect = append(connect, u.Scheme+"://"+u.Host)
		}
	default:
		connect = append(connect, "ws://127.0.0.1:*", "ws://localhost:*")
	}
	if buildinfo.IsDev() {
		// Vite dev server: HMR socket and the inline React-refresh preamble.
		connect = append(connect, "ws://localhost:*", "http://localhost:*")
		script = append(script, "'unsafe-inline'")
	}

	directives := []string{
		"default-src 'self'",
		"script-src " + strings.Join(script, " "),
		"style-src 'self' 'unsafe-inline'",
		"img-src 'self' data:",
		"font-src 'self' data:",
		"connect-src " + strings.Join(connect, " "),
		"object-src 'none'",
		"base-uri 'none'",
		"form-action 'none'",
		"frame-ancestors 'none'",
	}
	return strings.Join(directives, "; ")
}

// securityHeaders sets the CSP and hardening headers on every asset response.
func securityHeaders(cfg *config.Config) assetserver.Middleware {
	csp := contentSecurityPolicy(cfg)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Set("Content-Security-Policy", csp)
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("Referrer-Policy", "no-referrer")
			next.ServeHTTP(w, r)
		})
	}
}
