// Package httpclient builds the one outbound HTTP client the app shares (update checks, tool
// downloads, ...), so every request uses the same proxy settings and connection pool.
package httpclient

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"syscall"
	"time"

	"app/internal/config"
)

// New returns a streaming client: no overall timeout, because downloads are bounded by their
// context (the action idle timeout); callers needing a hard limit use WithTimeout.
// Dial attempts block link-local, unspecified, and multicast targets to mitigate DNS-rebinding SSRF.
func New(c config.NetworkConfig) *http.Client {
	dialer := &net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
		Control:   rejectLinkLocalControl,
	}
	return &http.Client{
		Transport: &http.Transport{
			Proxy:               http.ProxyFromEnvironment,
			DialContext:         dialer.DialContext,
			MaxIdleConns:        c.MaxIdleConns,
			MaxIdleConnsPerHost: c.MaxIdleConnsPerHost,
			IdleConnTimeout:     c.IdleConnTimeout,
		},
	}
}

func rejectLinkLocalControl(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("httpclient: invalid dial address %q: %w", address, err)
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return nil
	}
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() {
		return fmt.Errorf("httpclient: blocked outbound connection to %s", ip)
	}
	return nil
}

// WithTimeout returns a copy of base with an overall request timeout, sharing base's transport
// (and so its proxy settings and connection pool).
func WithTimeout(base *http.Client, timeout time.Duration) *http.Client {
	c := *base
	c.Timeout = timeout
	return &c
}

// WithRedirectGuard clones base and validates every redirect hop (capped at 10 hops).
func WithRedirectGuard(base *http.Client, validateURL func(raw string) error) *http.Client {
	if base == nil {
		base = &http.Client{}
	}
	c := *base
	prev := base.CheckRedirect
	c.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if validateURL != nil {
			if err := validateURL(req.URL.String()); err != nil {
				return fmt.Errorf("blocked redirect target: %w", err)
			}
		}
		if prev != nil {
			if err := prev(req, via); err != nil {
				return err
			}
		}
		if len(via) >= 10 {
			return errors.New("stopped after 10 redirects")
		}
		return nil
	}
	return &c
}
