package httpclient

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// Doer abstracts http.Client so packages that inject a mock client can share helpers.
type Doer interface {
	Do(*http.Request) (*http.Response, error)
}

// GitHubWebLatestURL converts a GitHub REST API latest-release URL
// (https://api.github.com/repos/<owner>/<repo>/releases/latest) into the web redirect URL
// (https://github.com/<owner>/<repo>/releases/latest), which is not subject to the unauthenticated
// REST API 60 req/hr limit.
func GitHubWebLatestURL(apiURL string) (string, bool) {
	u, err := url.Parse(strings.TrimSpace(apiURL))
	if err != nil || !strings.EqualFold(u.Scheme, "https") || !strings.EqualFold(u.Host, "api.github.com") {
		return "", false
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) != 5 || parts[0] != "repos" || parts[1] == "" || parts[2] == "" || parts[3] != "releases" || parts[4] != "latest" {
		return "", false
	}
	return "https://github.com/" + parts[1] + "/" + parts[2] + "/releases/latest", true
}

// IsRateLimitResponse reports whether status/headers indicate an upstream rate limit.
func IsRateLimitResponse(statusCode int, hdr http.Header) bool {
	if statusCode == http.StatusTooManyRequests {
		return true
	}
	if statusCode == http.StatusForbidden && hdr != nil {
		return hdr.Get("X-RateLimit-Remaining") == "0" || hdr.Get("Retry-After") != ""
	}
	return false
}

// ResolveGitHubLatestTag fetches webLatestURL with HEAD (stopping on the first redirect when client
// is an *http.Client) and extracts the release tag and canonical HTML URL from /releases/tag/<tag>.
func ResolveGitHubLatestTag(ctx context.Context, client Doer, webLatestURL, userAgent string) (tag string, releaseURL string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, webLatestURL, nil)
	if err != nil {
		return "", "", err
	}
	if userAgent != "" {
		req.Header.Set("User-Agent", userAgent)
	}
	doer := client
	if hc, ok := client.(*http.Client); ok {
		clone := *hc
		clone.CheckRedirect = func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}
		doer = &clone
	}
	resp, err := doer.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode >= 300 && resp.StatusCode < 400:
		loc := resp.Header.Get("Location")
		ref, parseErr := url.Parse(loc)
		if loc == "" || parseErr != nil {
			return "", "", fmt.Errorf("redirect without valid Location header")
		}
		target := req.URL.ResolveReference(ref)
		tag := parseGitHubTagFromPath(target.Path)
		if tag == "" {
			return "", "", fmt.Errorf("unexpected redirect target %q", target.String())
		}
		return tag, target.String(), nil
	case resp.StatusCode == http.StatusOK && resp.Request != nil && resp.Request.URL != nil:
		tag := parseGitHubTagFromPath(resp.Request.URL.Path)
		if tag == "" {
			return "", "", fmt.Errorf("no release tag in final URL %q", resp.Request.URL.String())
		}
		return tag, resp.Request.URL.String(), nil
	default:
		return "", "", fmt.Errorf("web release endpoint returned HTTP %d", resp.StatusCode)
	}
}

func parseGitHubTagFromPath(p string) string {
	idx := strings.Index(p, "/releases/tag/")
	if idx < 0 {
		return ""
	}
	raw := strings.TrimSuffix(p[idx+len("/releases/tag/"):], "/")
	if raw == "" || strings.Contains(raw, "/") {
		return ""
	}
	tag, err := url.PathUnescape(raw)
	if err != nil || tag == "" || strings.Contains(tag, "/") {
		return ""
	}
	return tag
}
