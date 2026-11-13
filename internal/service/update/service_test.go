package update

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"app/internal/apperr"
)

func feed(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") == "" {
			t.Error("missing user agent")
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func check(t *testing.T, url, current string) (*Service, error) {
	t.Helper()
	return New(Options{FeedURL: url, CurrentVersion: current, UserAgent: "test/1", Client: http.DefaultClient})
}

func TestUpdateAvailable(t *testing.T) {
	srv := feed(t, 200, `{"tag_name":"v1.3.0","html_url":"https://example.com/r","body":"notes"}`)
	s, _ := check(t, srv.URL, "1.2.9")
	info, err := s.Check(context.Background())
	if err != nil || !info.Available || info.LatestVersion != "1.3.0" || info.ReleaseURL != "https://example.com/r" {
		t.Fatalf("info=%+v err=%v", info, err)
	}
}

func TestUpToDateAndUnsafeURL(t *testing.T) {
	srv := feed(t, 200, `{"tag_name":"1.2.9","html_url":"javascript:alert(1)"}`)
	s, _ := check(t, srv.URL, "1.2.9")
	info, err := s.Check(context.Background())
	if err != nil || info.Available || info.ReleaseURL != "" {
		t.Fatalf("info=%+v err=%v", info, err)
	}
}

func TestFeedFailuresAreUnavailable(t *testing.T) {
	for name, srv := range map[string]*httptest.Server{
		"status":     feed(t, 500, ``),
		"malformed":  feed(t, 200, `{`),
		"prerelease": feed(t, 200, `{"tag_name":"v9.0.0","prerelease":true}`),
		"not semver": feed(t, 200, `{"tag_name":"latest"}`),
	} {
		s, _ := check(t, srv.URL, "1.0.0")
		if _, err := s.Check(context.Background()); !apperr.IsCode(err, apperr.CodeUnavailable) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestNotConfigured(t *testing.T) {
	s, _ := check(t, "", "1.0.0")
	if _, err := s.Check(context.Background()); !apperr.IsCode(err, apperr.CodeUnavailable) {
		t.Fatalf("err = %v", err)
	}
	if _, err := New(Options{}); err == nil {
		t.Fatal("missing client accepted")
	}
}

func TestUpdateUsesETagNotModifiedCache(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.Header.Get("If-None-Match") == `"v130"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"v130"`)
		_, _ = w.Write([]byte(`{"tag_name":"v1.3.0","html_url":"https://example.com/r","body":"notes"}`))
	}))
	t.Cleanup(srv.Close)

	now := time.Unix(1000, 0)
	s, err := New(Options{
		FeedURL:        srv.URL,
		CurrentVersion: "1.2.9",
		UserAgent:      "test/1",
		Client:         srv.Client(),
		Now:            func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.Check(context.Background())
	if err != nil || !first.Available || first.LatestVersion != "1.3.0" {
		t.Fatalf("first check failed: info=%+v err=%v", first, err)
	}

	now = now.Add(5 * time.Minute)
	second, err := s.Check(context.Background())
	if err != nil || second.LatestVersion != "1.3.0" || second.ReleaseNotes != "notes" {
		t.Fatalf("304 cached check failed: info=%+v err=%v", second, err)
	}
	if second.CheckedAt != now.UnixMilli() {
		t.Fatalf("expected refreshed CheckedAt=%d, got %d", now.UnixMilli(), second.CheckedAt)
	}
	if hits.Load() != 2 {
		t.Fatalf("expected 2 conditional requests, got %d", hits.Load())
	}
}

func TestUpdateFallsBackToGitHubWebRedirectOnRateLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/latest":
			w.Header().Set("X-RateLimit-Remaining", "0")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"message":"API rate limit exceeded"}`))
		case "/web/latest":
			w.Header().Set("Location", "https://github.com/example/app/releases/tag/v1.4.0")
			w.WriteHeader(http.StatusFound)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)

	s, err := New(Options{
		FeedURL:        srv.URL + "/api/latest",
		CurrentVersion: "1.2.9",
		UserAgent:      "test/1",
		Client:         srv.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	s.webLatestURL = func(string) (string, bool) { return srv.URL + "/web/latest", true }

	info, err := s.Check(context.Background())
	if err != nil {
		t.Fatalf("expected fallback to succeed, got %v", err)
	}
	if !info.Available || info.LatestVersion != "1.4.0" || info.ReleaseURL != "https://github.com/example/app/releases/tag/v1.4.0" {
		t.Fatalf("unexpected fallback info: %+v", info)
	}

	// Without fallback, a rate-limited response reports a clear rate-limit error.
	s.webLatestURL = func(string) (string, bool) { return "", false }
	_, err = s.Check(context.Background())
	if !apperr.IsCode(err, apperr.CodeUnavailable) || !strings.Contains(err.Error(), "rate limit") {
		t.Fatalf("expected rate-limit unavailable error, got %v", err)
	}
}
