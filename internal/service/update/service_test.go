package update

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

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
