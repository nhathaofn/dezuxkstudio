package server

import (
	"net"
	"net/http"
	"sync"
	"time"
)

type ipBucket struct {
	tokens   float64
	lastSeen time.Time
}

// ipRateLimiter implements a bounded per-IP token bucket for HTTP endpoints.
type ipRateLimiter struct {
	ratePerSec float64
	burst      float64
	maxEntries int

	mu      sync.Mutex
	buckets map[string]*ipBucket
	lastGC  time.Time
}

func newIPRateLimiter(ratePerSec, burst float64) *ipRateLimiter {
	return &ipRateLimiter{
		ratePerSec: ratePerSec,
		burst:      burst,
		maxEntries: 4096,
		buckets:    make(map[string]*ipBucket),
		lastGC:     time.Now(),
	}
}

func (l *ipRateLimiter) allow(ip string) bool {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()

	if now.Sub(l.lastGC) > 2*time.Minute || len(l.buckets) > l.maxEntries {
		for k, b := range l.buckets {
			if now.Sub(b.lastSeen) > 5*time.Minute {
				delete(l.buckets, k)
			}
		}
		if len(l.buckets) > l.maxEntries {
			// Clear oldest map if still overflowing under extreme IP churn.
			l.buckets = make(map[string]*ipBucket)
		}
		l.lastGC = now
	}

	b, ok := l.buckets[ip]
	if !ok {
		b = &ipBucket{tokens: l.burst, lastSeen: now}
		l.buckets[ip] = b
	} else {
		elapsed := now.Sub(b.lastSeen).Seconds()
		if elapsed > 0 {
			b.tokens = min(l.burst, b.tokens+elapsed*l.ratePerSec)
			b.lastSeen = now
		}
	}
	if b.tokens < 1.0 {
		return false
	}
	b.tokens -= 1.0
	return true
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil && host != "" {
		return host
	}
	if r.RemoteAddr != "" {
		return r.RemoteAddr
	}
	return "unknown"
}

func rateLimitMiddleware(lim *ipRateLimiter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodOptions && !lim.allow(clientIP(r)) {
				w.Header().Set("Retry-After", "1")
				http.Error(w, "too many requests", http.StatusTooManyRequests)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
