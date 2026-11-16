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
// img-src and media-src are matched to the allowed HTTP origins from connect-src
// according to app mode to allow streaming and previewing media assets.
func contentSecurityPolicy(cfg *config.Config) string {
	connect := []string{"'self'"}
	script := []string{"'self'"}
	mediaOrigins := []string{}

	switch cfg.Mode {
	case config.ModeClient:
		if u, err := url.Parse(cfg.Client.RemoteURL); err == nil && u.Host != "" {
			switch strings.ToLower(u.Scheme) {
			case "wss", "https":
				connect = append(connect, "wss://"+u.Host, "https://"+u.Host)
				mediaOrigins = append(mediaOrigins, "https://"+u.Host)
			case "ws", "http":
				connect = append(connect, "ws://"+u.Host, "http://"+u.Host)
				mediaOrigins = append(mediaOrigins, "http://"+u.Host)
			default:
				connect = append(connect, u.Scheme+"://"+u.Host)
				mediaOrigins = append(mediaOrigins, u.Scheme+"://"+u.Host)
			}
		}
	default:
		connect = append(connect, "ws://127.0.0.1:*", "ws://localhost:*", "http://127.0.0.1:*", "http://localhost:*")
		mediaOrigins = append(mediaOrigins, "http://127.0.0.1:*", "http://localhost:*")
	}
	if buildinfo.IsDev() {
		// Vite dev server: HMR socket and the inline React-refresh preamble.
		connect = append(connect, "ws://localhost:*", "http://localhost:*")
		script = append(script, "'unsafe-inline'")
	}

	imgSrc := []string{"'self'", "data:", "blob:"}
	imgSrc = append(imgSrc, mediaOrigins...)

	mediaSrc := []string{"'self'", "data:", "blob:"}
	mediaSrc = append(mediaSrc, mediaOrigins...)

	directives := []string{
		"default-src 'self'",
		"script-src " + strings.Join(script, " "),
		"style-src 'self' 'unsafe-inline'",
		"img-src " + strings.Join(imgSrc, " "),
		"media-src " + strings.Join(mediaSrc, " "),
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
