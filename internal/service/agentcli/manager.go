// Package agentcli keeps the app's own private copy of each agent CLI (claude, codex, agy): it downloads
// the newest official release, verifies its checksum, and switches to it without touching any CLI,
// settings or account the user already has on the machine. Everything lives under one app folder.
package agentcli

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"sync"

	"app/internal/apperr"
	domainagent "app/internal/domain/agent"
	"app/internal/fsutil"
	"app/internal/httpclient"
)

// versionPattern bounds what a remote server may name a version, because it becomes a folder name.
var versionPattern = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z._+-]{0,63}$`)

// Options configures a Manager.
type Options struct {
	Dir       string // root folder of all managed CLIs
	Client    *http.Client
	Logger    *slog.Logger
	UserAgent string
	// Keep is how many installed versions stay on disk (the current one included), so a bad release can be left.
	Keep int
	// Specs overrides the registry's install specs (tests point them at a local server).
	Specs map[domainagent.ProviderID]domainagent.InstallSpec
	// GOOS/GOARCH override the host (tests).
	GOOS, GOARCH string
}

// Manager downloads, verifies and switches the app's private CLI copies.
type Manager struct {
	dir          string
	client       *http.Client
	log          *slog.Logger
	ua           string
	keep         int
	specs        map[domainagent.ProviderID]domainagent.InstallSpec
	goos         string
	goarch       string
	webLatestURL func(string) (string, bool) // overridable in unit tests

	mu    sync.Mutex
	locks map[domainagent.ProviderID]*sync.Mutex // one install per provider at a time
}

// Release is what the vendor's channel says is newest.
type Release struct {
	Version  string
	URL      string
	Algo     string // "sha256" | "sha512"
	Checksum string
	Archive  bool // tar.gz package (false: the URL is the executable itself)
}

// New creates a Manager.
func New(o Options) *Manager {
	m := &Manager{
		dir: o.Dir, client: o.Client, log: o.Logger, ua: o.UserAgent, keep: o.Keep,
		specs: map[domainagent.ProviderID]domainagent.InstallSpec{}, goos: o.GOOS, goarch: o.GOARCH,
		webLatestURL: httpclient.GitHubWebLatestURL,
		locks:        map[domainagent.ProviderID]*sync.Mutex{},
	}
	if m.client == nil {
		m.client = &http.Client{}
	}
	if m.log == nil {
		m.log = slog.New(slog.DiscardHandler)
	}
	if m.keep < 1 {
		m.keep = 2
	}
	if m.goos == "" {
		m.goos = runtime.GOOS
	}
	if m.goarch == "" {
		m.goarch = runtime.GOARCH
	}
	for id, spec := range domainagent.Registry {
		m.specs[id] = spec.Install
	}
	for id, spec := range o.Specs {
		m.specs[id] = spec
	}
	return m
}

// Supported reports whether a managed copy can exist for id on this machine.
func (m *Manager) Supported(id domainagent.ProviderID) bool {
	spec := m.specs[id]
	if !spec.Managed() {
		return false
	}
	_, ok := spec.Platform(m.goos, m.goarch)
	return ok
}

func (m *Manager) idDir(id domainagent.ProviderID) string { return filepath.Join(m.dir, string(id)) }
func (m *Manager) versionsDir(id domainagent.ProviderID) string {
	return filepath.Join(m.idDir(id), "versions")
}
func (m *Manager) pointerPath(id domainagent.ProviderID) string {
	return filepath.Join(m.idDir(id), "current.json")
}

type pointer struct {
	Version string `json:"version"`
}

// Current returns the managed copy the app runs, if one is installed and intact.
func (m *Manager) Current(id domainagent.ProviderID) (domainagent.ManagedCopy, bool) {
	if !m.Supported(id) {
		return domainagent.ManagedCopy{}, false
	}
	raw, err := os.ReadFile(m.pointerPath(id))
	if err != nil {
		return domainagent.ManagedCopy{}, false
	}
	var p pointer
	if json.Unmarshal(raw, &p) != nil || !versionPattern.MatchString(p.Version) {
		return domainagent.ManagedCopy{}, false
	}
	exe := filepath.Join(m.versionsDir(id), p.Version, filepath.FromSlash(m.specs[id].Executable))
	if !fsutil.IsFile(exe) {
		return domainagent.ManagedCopy{}, false
	}
	return domainagent.ManagedCopy{Version: p.Version, Path: exe}, true
}

func (m *Manager) lockFor(id domainagent.ProviderID) *sync.Mutex {
	m.mu.Lock()
	defer m.mu.Unlock()
	l, ok := m.locks[id]
	if !ok {
		l = &sync.Mutex{}
		m.locks[id] = l
	}
	return l
}

// Install makes the newest official release the app's copy. changed is false when that version was
// already current. The previous version stays on disk and keeps working until the switch.
func (m *Manager) Install(ctx context.Context, id domainagent.ProviderID) (cur domainagent.ManagedCopy, changed bool, err error) {
	if !m.Supported(id) {
		return domainagent.ManagedCopy{}, false, apperr.New(apperr.CodeBadRequest, fmt.Sprintf("no managed copy of %s for %s/%s", id, m.goos, m.goarch))
	}
	l := m.lockFor(id)
	l.Lock()
	defer l.Unlock()

	rel, err := m.Latest(ctx, id)
	if err != nil {
		return domainagent.ManagedCopy{}, false, err
	}
	if cur, ok := m.Current(id); ok && cur.Version == rel.Version {
		return cur, false, nil
	}
	spec := m.specs[id]
	name := domainagent.Registry[id].Name
	if err := m.fetch(ctx, id, name, spec, rel); err != nil {
		return domainagent.ManagedCopy{}, false, err
	}
	raw, _ := json.Marshal(pointer{Version: rel.Version})
	if err := fsutil.WriteFileAtomic(m.pointerPath(id), raw, 0o644); err != nil {
		return domainagent.ManagedCopy{}, false, fmt.Errorf("agentcli: switch to %s: %w", rel.Version, err)
	}
	m.prune(id, rel.Version)
	cur, _ = m.Current(id)
	return cur, true, nil
}

// prune removes the oldest installed versions beyond Keep. A copy still running is locked by the OS
// and simply stays until a later pass.
func (m *Manager) prune(id domainagent.ProviderID, current string) {
	entries, err := os.ReadDir(m.versionsDir(id))
	if err != nil {
		return
	}
	type ver struct {
		name string
		mod  int64
	}
	var vs []ver
	for _, e := range entries {
		if !e.IsDir() || e.Name() == current {
			continue
		}
		if info, err := e.Info(); err == nil {
			vs = append(vs, ver{e.Name(), info.ModTime().UnixNano()})
		}
	}
	sort.Slice(vs, func(i, j int) bool { return vs[i].mod > vs[j].mod })
	for i := m.keep - 1; i < len(vs); i++ {
		_ = os.RemoveAll(filepath.Join(m.versionsDir(id), vs[i].name))
	}
}
