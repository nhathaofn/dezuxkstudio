package agentcli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"app/internal/apperr"
	domainagent "app/internal/domain/agent"
)

// maxMetadata bounds a metadata response; real ones are a few KB.
const maxMetadata = 4 << 20

func (m *Manager) get(ctx context.Context, rawURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", m.ua)
	resp, err := m.client.Do(req)
	if err != nil {
		return nil, apperr.Wrap(apperr.CodeUnavailable, "could not reach the download server; check the network connection", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, apperr.Wrap(apperr.CodeUnavailable, fmt.Sprintf("download server returned HTTP %d", resp.StatusCode), nil)
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxMetadata))
}

func (m *Manager) getJSON(ctx context.Context, rawURL string, out any) error {
	raw, err := m.get(ctx, rawURL)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return apperr.Wrap(apperr.CodeIntegrity, "the release information is not in the expected format", err)
	}
	return nil
}

func join(base string, parts ...string) string {
	u := strings.TrimRight(base, "/")
	for _, p := range parts {
		u += "/" + strings.Trim(p, "/")
	}
	return u
}

func checkVersion(v string) (string, error) {
	v = strings.TrimSpace(v)
	if !versionPattern.MatchString(v) {
		return "", apperr.Wrap(apperr.CodeIntegrity, "the release information names an invalid version", nil)
	}
	return v, nil
}

// Latest asks the vendor's official channel for the newest release of id.
func (m *Manager) Latest(ctx context.Context, id domainagent.ProviderID) (Release, error) {
	spec := m.specs[id]
	platform, ok := spec.Platform(m.goos, m.goarch)
	if !ok {
		return Release{}, apperr.New(apperr.CodeBadRequest, fmt.Sprintf("no managed copy of %s for %s/%s", id, m.goos, m.goarch))
	}
	switch spec.Scheme {
	case domainagent.SchemeVersionFile:
		raw, err := m.get(ctx, join(spec.BaseURL, spec.LatestPath))
		if err != nil {
			return Release{}, err
		}
		version, err := checkVersion(string(raw))
		if err != nil {
			return Release{}, err
		}
		var manifest struct {
			Platforms map[string]struct {
				Checksum string `json:"checksum"`
			} `json:"platforms"`
		}
		if err := m.getJSON(ctx, join(spec.BaseURL, version, "manifest.json"), &manifest); err != nil {
			return Release{}, err
		}
		sum := manifest.Platforms[platform].Checksum
		if sum == "" {
			return Release{}, apperr.Wrap(apperr.CodeIntegrity, "the release has no checksum for this platform", nil)
		}
		return Release{Version: version, URL: join(spec.BaseURL, version, platform, spec.Executable), Algo: "sha256", Checksum: sum}, nil

	case domainagent.SchemeManifest:
		var manifest struct {
			Version string `json:"version"`
			URL     string `json:"url"`
			SHA512  string `json:"sha512"`
		}
		if err := m.getJSON(ctx, join(spec.BaseURL, "manifests", platform+".json"), &manifest); err != nil {
			return Release{}, err
		}
		version, err := checkVersion(manifest.Version)
		if err != nil {
			return Release{}, err
		}
		if manifest.URL == "" || manifest.SHA512 == "" {
			return Release{}, apperr.Wrap(apperr.CodeIntegrity, "the release has no download address or checksum", nil)
		}
		return Release{Version: version, URL: manifest.URL, Algo: "sha512", Checksum: manifest.SHA512}, nil

	case domainagent.SchemeReleasePackage:
		var latest struct {
			Tag string `json:"tag_name"`
		}
		if err := m.getJSON(ctx, spec.LatestAPI, &latest); err != nil {
			return Release{}, err
		}
		version, err := checkVersion(strings.TrimPrefix(latest.Tag, spec.TagPrefix))
		if err != nil {
			return Release{}, err
		}
		tag := url.PathEscape(latest.Tag)
		asset := strings.ReplaceAll(spec.PackageAsset, "{platform}", platform)
		sums, err := m.get(ctx, join(spec.BaseURL, tag, spec.ChecksumAsset))
		if err != nil {
			return Release{}, err
		}
		sum := checksumFor(sums, asset)
		if sum == "" {
			return Release{}, apperr.Wrap(apperr.CodeIntegrity, "the release has no checksum for this platform", nil)
		}
		return Release{Version: version, URL: join(spec.BaseURL, tag, asset), Algo: "sha256", Checksum: sum, Archive: true}, nil
	}
	return Release{}, fmt.Errorf("agentcli: unknown install scheme %q", spec.Scheme)
}

// checksumFor reads "<hex>  <name>" lines (sha256sum format) and returns the hash of name.
func checksumFor(sums []byte, name string) string {
	sc := bufio.NewScanner(bytes.NewReader(sums))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == name {
			return f[0]
		}
	}
	return ""
}
