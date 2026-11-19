package desktop

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/wailsapp/wails/v2/pkg/options/assetserver"

	"app/internal/buildinfo"
	"app/internal/config"
)

func isSafeCSPHostPort(u *url.URL) bool {
	if u == nil || u.Host == "" {
		return false
	}
	for i := 0; i < len(u.Host); i++ {
		b := u.Host[i]
		if (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') ||
			b == '.' || b == '-' || b == ':' || b == '[' || b == ']' {
			continue
		}
		return false
	}
	if p := u.Port(); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 65535 {
			return false
		}
	}
	return u.Hostname() != ""
}

func desktopOriginHostPort(cfg *config.Config) string {
	host := strings.TrimSpace(cfg.Server.Host)
	if host == "" || !config.IsLoopbackHost(host) {
		host = "127.0.0.1"
	}
	if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
		host = "[" + host + "]"
	}
	if cfg.Server.Port > 0 && cfg.Server.Port <= 65535 {
		return fmt.Sprintf("%s:%d", host, cfg.Server.Port)
	}
	return host + ":*"
}

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
		if u, err := url.Parse(cfg.Client.RemoteURL); err == nil && isSafeCSPHostPort(u) {
			switch strings.ToLower(u.Scheme) {
			case "wss", "https":
				connect = append(connect, "wss://"+u.Host, "https://"+u.Host)
				mediaOrigins = append(mediaOrigins, "https://"+u.Host)
			case "ws", "http":
				connect = append(connect, "ws://"+u.Host, "http://"+u.Host)
				mediaOrigins = append(mediaOrigins, "http://"+u.Host)
			}
		}
	default:
		hp := desktopOriginHostPort(cfg)
		connect = append(connect, "ws://"+hp, "http://"+hp)
		mediaOrigins = append(mediaOrigins, "http://"+hp)
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
		"style-src 'self'",
		"style-src-attr 'unsafe-inline'",
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
