// Package httpclient builds the one outbound HTTP client the app shares (update checks, tool
// downloads, ...), so every request uses the same proxy settings and connection pool.
package httpclient

import (
	"net/http"
	"time"

	"app/internal/config"
)

// New returns a streaming client: no overall timeout, because downloads are bounded by their
// context (the action idle timeout); callers needing a hard limit use WithTimeout.
func New(c config.NetworkConfig) *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			Proxy:               http.ProxyFromEnvironment,
			MaxIdleConns:        c.MaxIdleConns,
			MaxIdleConnsPerHost: c.MaxIdleConnsPerHost,
			IdleConnTimeout:     c.IdleConnTimeout,
		},
	}
}

// WithTimeout returns a copy of base with an overall request timeout, sharing base's transport
// (and so its proxy settings and connection pool).
func WithTimeout(base *http.Client, timeout time.Duration) *http.Client {
	c := *base
	c.Timeout = timeout
	return &c
}
