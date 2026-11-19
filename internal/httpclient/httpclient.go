// Package httpclient builds the one outbound HTTP client the app shares (update checks, tool
// downloads, ...), so every request uses the same proxy settings and connection pool.
package httpclient

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
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

var (
	cgnatPrefix      = netip.MustParsePrefix("100.64.0.0/10")
	ietfProtoPrefix  = netip.MustParsePrefix("192.0.0.0/24")
	ipv6ULAPrefix    = netip.MustParsePrefix("fc00::/7")
	sensitiveHeaders = []string{"Authorization", "X-Goog-Api-Key", "X-Api-Key", "Proxy-Authorization", "Cookie"}
)

func isUnsafeDialAddr(addr netip.Addr) bool {
	if !addr.IsValid() {
		return true
	}
	addr = addr.WithZone("").Unmap()
	return addr.IsLinkLocalUnicast() ||
		addr.IsLinkLocalMulticast() ||
		addr.IsUnspecified() ||
		addr.IsMulticast() ||
		cgnatPrefix.Contains(addr) ||
		ietfProtoPrefix.Contains(addr) ||
		ipv6ULAPrefix.Contains(addr)
}

func rejectLinkLocalControl(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("httpclient: invalid dial address %q: %w", address, err)
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return fmt.Errorf("httpclient: invalid dial IP %q: %w", host, err)
	}
	if isUnsafeDialAddr(addr) {
		return fmt.Errorf("httpclient: blocked outbound connection to %s", addr.WithZone("").Unmap())
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

func sameURLOrigin(a, b *url.URL) bool {
	if a == nil || b == nil {
		return false
	}
	return strings.EqualFold(a.Scheme, b.Scheme) && strings.EqualFold(a.Host, b.Host)
}

func isLocalOrPrivateRedirectHost(rawHost string) bool {
	h := strings.TrimSuffix(strings.ToLower(rawHost), ".")
	if h == "" {
		return true
	}
	if h == "localhost" || strings.HasSuffix(h, ".localhost") {
		return true
	}
	if addr, err := netip.ParseAddr(h); err == nil {
		addr = addr.WithZone("").Unmap()
		return addr.IsLoopback() || addr.IsPrivate() || isUnsafeDialAddr(addr)
	}
	return false
}

// WithRedirectGuard clones base and validates every redirect hop (capped at 10 hops).
// Cross-origin redirects strip sensitive auth headers and block hops to loopback or private hosts.
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
		if len(via) > 0 && !sameURLOrigin(req.URL, via[len(via)-1].URL) {
			if isLocalOrPrivateRedirectHost(req.URL.Hostname()) {
				return fmt.Errorf("blocked redirect target: cross-origin redirect to local or private host %q", req.URL.Hostname())
			}
			for _, hdr := range sensitiveHeaders {
				req.Header.Del(hdr)
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
