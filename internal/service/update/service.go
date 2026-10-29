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
	"time"

	"golang.org/x/mod/semver"

	"app/internal/apperr"
	"app/internal/contracts"
)

// maxFeedBytes bounds the feed response size.
const maxFeedBytes = 1 << 20

// Doer is the HTTP client abstraction (http.Client satisfies it).
type Doer interface {
	Do(*http.Request) (*http.Response, error)
}

// Options configures the service.
type Options struct {
	FeedURL        string // GitHub "latest release" API URL; empty disables checks
	CurrentVersion string
	UserAgent      string
	Client         Doer
	Now            func() time.Time
}

// Service checks for updates.
type Service struct{ o Options }

// New creates the service.
func New(o Options) (*Service, error) {
	if o.Client == nil {
		return nil, errors.New("update: http client is required")
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	return &Service{o: o}, nil
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

	resp, err := s.o.Client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return contracts.UpdateInfo{}, ctx.Err()
		}
		return contracts.UpdateInfo{}, apperr.Wrap(apperr.CodeUnavailable, "release feed unreachable", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return contracts.UpdateInfo{}, apperr.Wrap(apperr.CodeUnavailable, "release feed returned an error",
			fmt.Errorf("status %d", resp.StatusCode))
	}

	var rel release
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxFeedBytes)).Decode(&rel); err != nil {
		return contracts.UpdateInfo{}, apperr.Wrap(apperr.CodeUnavailable, "release feed is malformed", err)
	}
	latest := canonical(rel.TagName)
	current := canonical(s.o.CurrentVersion)
	if latest == "" || rel.Draft || rel.Pre {
		return contracts.UpdateInfo{}, apperr.Wrap(apperr.CodeUnavailable, "release feed has no stable version",
			fmt.Errorf("tag %q draft=%v prerelease=%v", rel.TagName, rel.Draft, rel.Pre))
	}

	return contracts.UpdateInfo{
		CurrentVersion: strings.TrimPrefix(s.o.CurrentVersion, "v"),
		LatestVersion:  strings.TrimPrefix(latest, "v"),
		Available:      current == "" || semver.Compare(latest, current) > 0,
		ReleaseURL:     httpsOnly(rel.HTMLURL),
		ReleaseNotes:   rel.Body,
		CheckedAt:      s.o.Now().UnixMilli(),
	}, nil
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
