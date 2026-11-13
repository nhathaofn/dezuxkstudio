// Package update checks a release feed for newer versions. It only reports; installing
// is left to the signed installer downloaded by the user from the release page.
package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/mod/semver"

	"app/internal/apperr"
	"app/internal/contracts"
	"app/internal/httpclient"
)

// maxFeedBytes bounds the feed response size.
const maxFeedBytes = 1 << 20

// Doer is the HTTP client abstraction (http.Client satisfies it).
type Doer = httpclient.Doer

// Options configures the service.
type Options struct {
	FeedURL        string // GitHub "latest release" API URL; empty disables checks
	CurrentVersion string
	UserAgent      string
	Client         Doer
	Now            func() time.Time
}

// Service checks for updates.
type Service struct {
	o            Options
	webLatestURL func(string) (string, bool) // overridable in unit tests

	mu         sync.Mutex
	cachedETag string
	cachedInfo contracts.UpdateInfo
	hasCache   bool
}

// New creates the service.
func New(o Options) (*Service, error) {
	if o.Client == nil {
		return nil, errors.New("update: http client is required")
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	return &Service{
		o:            o,
		webLatestURL: httpclient.GitHubWebLatestURL,
	}, nil
}

// release is the subset of the GitHub release API used here.
type release struct {
	TagName string `json:"tag_name"`
	HTMLURL string `json:"html_url"`
	Body    string `json:"body"`
	Draft   bool   `json:"draft"`
	Pre     bool   `json:"prerelease"`
}

// Check fetches the latest release and compares it with the running version.
func (s *Service) Check(ctx context.Context) (contracts.UpdateInfo, error) {
	if s.o.FeedURL == "" {
		return contracts.UpdateInfo{}, apperr.Unavailable("update checks are not configured")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.o.FeedURL, nil)
	if err != nil {
		return contracts.UpdateInfo{}, apperr.Internal(err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	if s.o.UserAgent != "" {
		req.Header.Set("User-Agent", s.o.UserAgent)
	}
	if etag := s.etag(); etag != "" {
		req.Header.Set("If-None-Match", etag)
	}

	resp, err := s.o.Client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return contracts.UpdateInfo{}, ctx.Err()
		}
		return contracts.UpdateInfo{}, apperr.Wrap(apperr.CodeUnavailable, "release feed unreachable", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotModified {
		if info, ok := s.cachedResult(); ok {
			return info, nil
		}
	}

	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests {
		if info, ok := s.checkGitHubWebFallback(ctx); ok {
			return info, nil
		}
		if httpclient.IsRateLimitResponse(resp.StatusCode, resp.Header) {
			return contracts.UpdateInfo{}, apperr.Wrap(apperr.CodeUnavailable, "release feed rate limit exceeded",
				fmt.Errorf("status %d", resp.StatusCode))
		}
		return contracts.UpdateInfo{}, apperr.Wrap(apperr.CodeUnavailable, "release feed returned an error",
			fmt.Errorf("status %d", resp.StatusCode))
	}

	if resp.StatusCode != http.StatusOK {
		return contracts.UpdateInfo{}, apperr.Wrap(apperr.CodeUnavailable, "release feed returned an error",
			fmt.Errorf("status %d", resp.StatusCode))
	}

	var rel release
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxFeedBytes)).Decode(&rel); err != nil {
		return contracts.UpdateInfo{}, apperr.Wrap(apperr.CodeUnavailable, "release feed is malformed", err)
	}
	latest := canonical(rel.TagName)
	if latest == "" || rel.Draft || rel.Pre {
		return contracts.UpdateInfo{}, apperr.Wrap(apperr.CodeUnavailable, "release feed has no stable version",
			fmt.Errorf("tag %q draft=%v prerelease=%v", rel.TagName, rel.Draft, rel.Pre))
	}
	info := s.buildInfo(latest, rel.HTMLURL, rel.Body)
	s.storeCache(resp.Header.Get("ETag"), info)
	return info, nil
}

func (s *Service) buildInfo(latest, htmlURL, notes string) contracts.UpdateInfo {
	current := canonical(s.o.CurrentVersion)
	return contracts.UpdateInfo{
		CurrentVersion: strings.TrimPrefix(s.o.CurrentVersion, "v"),
		LatestVersion:  strings.TrimPrefix(latest, "v"),
		Available:      current == "" || semver.Compare(latest, current) > 0,
		ReleaseURL:     httpsOnly(htmlURL),
		ReleaseNotes:   notes,
		CheckedAt:      s.o.Now().UnixMilli(),
	}
}

func (s *Service) etag() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.hasCache {
		return ""
	}
	return s.cachedETag
}

func (s *Service) cachedResult() (contracts.UpdateInfo, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.hasCache {
		return contracts.UpdateInfo{}, false
	}
	s.cachedInfo.CheckedAt = s.o.Now().UnixMilli()
	return s.cachedInfo, true
}

func (s *Service) storeCache(etag string, info contracts.UpdateInfo) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cachedETag = etag
	s.cachedInfo = info
	s.hasCache = true
}

func (s *Service) checkGitHubWebFallback(ctx context.Context) (contracts.UpdateInfo, bool) {
	if s.webLatestURL == nil {
		return contracts.UpdateInfo{}, false
	}
	webURL, ok := s.webLatestURL(s.o.FeedURL)
	if !ok {
		return contracts.UpdateInfo{}, false
	}
	tag, releaseURL, err := httpclient.ResolveGitHubLatestTag(ctx, s.o.Client, webURL, s.o.UserAgent)
	if err != nil {
		return contracts.UpdateInfo{}, false
	}
	latest := canonical(tag)
	if latest == "" {
		return contracts.UpdateInfo{}, false
	}
	info := s.buildInfo(latest, releaseURL, "")
	s.storeCache("", info)
	return info, true
}

// canonical returns "vX.Y.Z[-pre]" or "" if v is not a semantic version.
func canonical(v string) string {
	if !strings.HasPrefix(v, "v") {
		v = "v" + v
	}
	if !semver.IsValid(v) {
		return ""
	}
	return v
}

func httpsOnly(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return ""
	}
	return u.String()
}
