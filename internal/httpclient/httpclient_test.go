package httpclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"app/internal/config"
)

func TestNewUsesConfigAndWithTimeoutSharesThePool(t *testing.T) {
	cfg := config.Default(config.ModeDesktop).Network
	cfg.IdleConnTimeout = 42 * time.Second
	base := New(cfg)
	tr := base.Transport.(*http.Transport)
	if tr.IdleConnTimeout != 42*time.Second || tr.MaxIdleConns != cfg.MaxIdleConns || tr.Proxy == nil {
		t.Fatalf("transport not built from config: %+v", tr)
	}
	if base.Timeout != 0 {
		t.Fatal("the shared client must not cap streaming downloads")
	}
	u := WithTimeout(base, 10*time.Second)
	if u.Timeout != 10*time.Second || u.Transport != base.Transport {
		t.Fatal("WithTimeout must set a timeout and reuse the same transport")
	}
	if base.Timeout != 0 {
		t.Fatal("WithTimeout must not modify the base client")
	}
}

func TestGitHubWebLatestURL(t *testing.T) {
	got, ok := GitHubWebLatestURL("https://api.github.com/repos/nhathaofn/dezuxkstudio/releases/latest")
	if !ok || got != "https://github.com/nhathaofn/dezuxkstudio/releases/latest" {
		t.Fatalf("unexpected web latest URL: got=%q ok=%v", got, ok)
	}
	for _, bad := range []string{
		"",
		"http://api.github.com/repos/a/b/releases/latest",
		"https://example.com/repos/a/b/releases/latest",
		"https://api.github.com/repos/a/b/releases",
	} {
		if _, ok := GitHubWebLatestURL(bad); ok {
			t.Errorf("expected %q not to match GitHub latest API URL", bad)
		}
	}
}

func TestResolveGitHubLatestTagAndRateLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok/releases/latest":
			if r.Method != http.MethodHead {
				t.Errorf("expected HEAD request, got %s", r.Method)
			}
			w.Header().Set("Location", "/ok/releases/tag/v0.2.1")
			w.WriteHeader(http.StatusFound)
		case "/ok/releases/tag/v0.2.1":
			t.Fatal("client should stop at 302 redirect without fetching target page")
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)

	tag, relURL, err := ResolveGitHubLatestTag(context.Background(), srv.Client(), srv.URL+"/ok/releases/latest", "test/1")
	if err != nil || tag != "v0.2.1" || relURL != srv.URL+"/ok/releases/tag/v0.2.1" {
		t.Fatalf("tag=%q url=%q err=%v", tag, relURL, err)
	}
	if _, _, err := ResolveGitHubLatestTag(context.Background(), srv.Client(), srv.URL+"/missing", "test/1"); err == nil {
		t.Fatal("expected error for 404")
	}

	hdr := http.Header{}
	if IsRateLimitResponse(http.StatusForbidden, hdr) {
		t.Fatal("bare 403 without rate-limit headers must be false")
	}
	hdr.Set("X-RateLimit-Remaining", "0")
	if !IsRateLimitResponse(http.StatusForbidden, hdr) || !IsRateLimitResponse(http.StatusTooManyRequests, nil) {
		t.Fatal("expected rate limit detection for 403+remaining=0 and 429")
	}
}
