package agent

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	domainagent "app/internal/domain/agent"
	"app/internal/progress"
)

// fakeManaged is a ManagedCLIs whose installs the test controls.
type fakeManaged struct {
	mu      sync.Mutex
	dir     string
	have    map[domainagent.ProviderID]domainagent.ManagedCopy
	failing bool
	calls   map[domainagent.ProviderID]int
}

func newFakeManaged(t *testing.T) *fakeManaged {
	return &fakeManaged{dir: t.TempDir(), have: map[domainagent.ProviderID]domainagent.ManagedCopy{}, calls: map[domainagent.ProviderID]int{}}
}

func (f *fakeManaged) Supported(domainagent.ProviderID) bool { return true }
func (f *fakeManaged) Current(id domainagent.ProviderID) (domainagent.ManagedCopy, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.have[id]
	return c, ok
}
func (f *fakeManaged) Install(ctx context.Context, id domainagent.ProviderID) (domainagent.ManagedCopy, bool, error) {
	f.mu.Lock()
	f.calls[id]++
	failing := f.failing
	f.mu.Unlock()
	if failing {
		return domainagent.ManagedCopy{}, false, errors.New("offline")
	}
	progress.Report(ctx, progress.Update{Percent: 50}) // the service reads Current while reporting: no lock held here
	path := filepath.Join(f.dir, string(id)+".cmd")
	if err := os.WriteFile(path, []byte("@echo off\r\necho "+string(id)+" 9.9.9\r\n"), 0o755); err != nil {
		return domainagent.ManagedCopy{}, false, err
	}
	cur := domainagent.ManagedCopy{Version: "9.9.9", Path: path}
	f.mu.Lock()
	defer f.mu.Unlock()
	changed := f.have[id] != cur
	f.have[id] = cur
	return cur, changed, nil
}

func newManagedService(t *testing.T, m ManagedCLIs, install, update bool, repo *fakeRepo) *Service {
	t.Helper()
	dir := t.TempDir()
	svc, err := New(Options{
		Repo: repo, DataDir: dir, WorkspaceDir: filepath.Join(dir, "ws"), MediaDir: filepath.Join(dir, "media"),
		ProfilesDir: filepath.Join(dir, "profiles"), Logger: slog.New(slog.DiscardHandler),
		Managed: m, AutoInstall: install, AutoUpdate: update,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Close() })
	return svc
}

func TestManagedCopyIsPreferredButCustomPathStillWins(t *testing.T) {
	repo := newFakeRepo()
	fm := newFakeManaged(t)
	svc := newManagedService(t, fm, false, false, repo)
	spec := domainagent.Registry[domainagent.ProviderCodex]
	ctx := context.Background()

	if _, _, err := fm.Install(ctx, domainagent.ProviderCodex); err != nil {
		t.Fatal(err)
	}
	path, src, _, err := svc.resolveExecutable(ctx, spec)
	if err != nil || src != domainagent.SourceManaged || path != fm.have[domainagent.ProviderCodex].Path {
		t.Fatalf("the app's own copy must be used: %q %v %v", path, src, err)
	}

	custom := filepath.Join(t.TempDir(), "my-codex.cmd")
	if err := os.WriteFile(custom, []byte("@echo off\r\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	repo.paths[domainagent.ProviderCodex] = custom
	path, src, _, _ = svc.resolveExecutable(ctx, spec)
	if src != domainagent.SourceCustom || path != custom {
		t.Fatalf("an explicit custom path outranks the managed copy: %q %v", path, src)
	}
}

func TestProvisionInstallsMissingAndReportsProgress(t *testing.T) {
	repo := newFakeRepo()
	fm := newFakeManaged(t)
	svc := newManagedService(t, fm, true, false, repo)
	var mu sync.Mutex
	var sawInstalling bool
	svc.onStatusChanged = func(info domainagent.ProviderInfo) {
		mu.Lock()
		defer mu.Unlock()
		if info.ID == domainagent.ProviderClaude && info.Installing {
			sawInstalling = true
		}
	}
	svc.ProvisionCLIs(context.Background())

	for _, id := range domainagent.AllProviderIDs() {
		if _, ok := fm.Current(id); !ok {
			t.Fatalf("%s was not installed", id)
		}
		info, err := svc.statusInfo(context.Background(), id)
		if err != nil || info.Source != domainagent.SourceManaged || info.Installing {
			t.Fatalf("%s: source=%v installing=%v err=%v", id, info.Source, info.Installing, err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if !sawInstalling {
		t.Fatal("the UI was never told that a download is running")
	}
}

func TestProvisionRespectsInstallAndUpdateSwitches(t *testing.T) {
	ctx := context.Background()
	// Off/off: nothing is downloaded at all.
	fm := newFakeManaged(t)
	newManagedService(t, fm, false, false, newFakeRepo()).ProvisionCLIs(ctx)
	if len(fm.calls) != 0 {
		t.Fatalf("both switches off must not download: %v", fm.calls)
	}
	// Install only: a missing CLI is fetched once, an existing one is left alone.
	fm = newFakeManaged(t)
	fm.have[domainagent.ProviderCodex] = domainagent.ManagedCopy{Version: "1.0.0", Path: filepath.Join(fm.dir, "old")}
	newManagedService(t, fm, true, false, newFakeRepo()).ProvisionCLIs(ctx)
	if fm.calls[domainagent.ProviderCodex] != 0 || fm.calls[domainagent.ProviderClaude] != 1 {
		t.Fatalf("install-only: %v", fm.calls)
	}
	// Update only: an existing CLI is refreshed once at startup and finishes without periodic polling.
	fm = newFakeManaged(t)
	fm.have[domainagent.ProviderCodex] = domainagent.ManagedCopy{Version: "1.0.0", Path: filepath.Join(fm.dir, "old")}
	svc := newManagedService(t, fm, false, true, newFakeRepo())
	done := make(chan struct{})
	go func() { svc.ProvisionCLIs(ctx); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("ProvisionCLIs did not return after one startup round")
	}
	if fm.calls[domainagent.ProviderCodex] != 1 || fm.calls[domainagent.ProviderClaude] != 0 {
		t.Fatalf("update-only: %v", fm.calls)
	}
}

func TestFailedDownloadKeepsWorkingCopyAndShowsNoStuckProgress(t *testing.T) {
	repo := newFakeRepo()
	fm := newFakeManaged(t)
	good := filepath.Join(fm.dir, "codex-old.cmd")
	if err := os.WriteFile(good, []byte("@echo off\r\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	fm.have[domainagent.ProviderCodex] = domainagent.ManagedCopy{Version: "1.0.0", Path: good}
	fm.failing = true
	svc := newManagedService(t, fm, true, true, repo)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { svc.ProvisionCLIs(ctx); close(done) }()
	time.Sleep(300 * time.Millisecond)
	cancel()
	<-done

	info, err := svc.statusInfo(context.Background(), domainagent.ProviderCodex)
	if err != nil || info.ResolvedPath != good || info.Installing {
		t.Fatalf("offline update must keep the working copy: path=%q installing=%v err=%v", info.ResolvedPath, info.Installing, err)
	}
}

func TestProviderEnvSilencesTheCLIsOwnUpdater(t *testing.T) {
	// The app keeps its own copy current; a CLI must not rewrite itself or change the machine's setup.
	for id, want := range map[domainagent.ProviderID]string{
		domainagent.ProviderClaude:      "DISABLE_AUTOUPDATER=1",
		domainagent.ProviderAntigravity: "AGY_CLI_DISABLE_AUTO_UPDATE=1",
	} {
		var found bool
		for _, e := range providerEnv(id, nil) {
			if e == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("%s: %s missing from the CLI's environment", id, want)
		}
	}
}
