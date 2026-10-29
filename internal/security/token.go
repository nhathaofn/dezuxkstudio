// Package security holds authentication primitives shared by HTTP and WebSocket transports.
package security

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strings"
)

// TokenBytes is the entropy of generated session tokens (256 bit).
const TokenBytes = 32

// NewToken returns a random hex token suitable for URLs, headers and WebSocket subprotocols.
func NewToken() (string, error) {
	b := make([]byte, TokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("security: generate token: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// TokenEqual compares tokens in constant time. An empty expected token never matches.
func TokenEqual(expected, got string) bool {
	if expected == "" || got == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(expected), []byte(got)) == 1
}

// BearerToken extracts the token from an "Authorization: Bearer <token>" header.
func BearerToken(r *http.Request) string {
	const prefix = "Bearer "
	h := r.Header.Get("Authorization")
	if len(h) > len(prefix) && strings.EqualFold(h[:len(prefix)], prefix) {
		return strings.TrimSpace(h[len(prefix):])
	}
	return ""
}

// OriginAllowed reports whether an Origin header value matches any pattern.
// Patterns containing "://" are matched against "scheme://host[:port]", others
// against "host[:port]", using path.Match globbing (same semantics as the WebSocket upgrader).
func OriginAllowed(origin string, patterns []string) bool {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	for _, p := range patterns {
		target := u.Host
		if strings.Contains(p, "://") {
			target = u.Scheme + "://" + u.Host
		}
		if ok, err := path.Match(strings.ToLower(p), strings.ToLower(target)); err == nil && ok {
			return true
		}
	}
	return false
}

// SubprotocolToken extracts a token sent as a WebSocket subprotocol "<prefix><token>".
// Browsers cannot set headers on WebSocket handshakes, so this is the only header-based
// channel available that keeps the token out of URLs (and therefore out of access logs).
func SubprotocolToken(r *http.Request, prefix string) string {
	for _, v := range r.Header.Values("Sec-WebSocket-Protocol") {
		for _, p := range strings.Split(v, ",") {
			p = strings.TrimSpace(p)
			if strings.HasPrefix(p, prefix) {
				return strings.TrimPrefix(p, prefix)
			}
		}
	}
	return ""
}
