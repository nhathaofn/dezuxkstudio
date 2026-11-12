package agent

import (
	"context"
	"log/slog"
	"path/filepath"
	"testing"

	domainagent "app/internal/domain/agent"
)

func newRestoreService(t *testing.T, repo *fakeRepo) *Service {
	t.Helper()
	svc, err := New(Options{Repo: repo, DataDir: t.TempDir(), Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Close() })
	return svc
}

func firstSupported(t *testing.T) domainagent.ProviderID {
	t.Helper()
	for _, id := range domainagent.AllProviderIDs() {
		if domainagent.Registry[id].Supported {
			return id
		}
	}
	t.Skip("no supported agent provider")
	return ""
}

func TestRestoreForgetsUnknownProvider(t *testing.T) {
	repo := newFakeRepo()
	repo.remembered = "no-such-agent"
	newRestoreService(t, repo).RestoreConnection(context.Background())
	if repo.remembered != "" {
		t.Fatalf("unknown provider must be forgotten, got %q", repo.remembered)
	}
}

func TestRestoreForgetsProviderThatCannotConnect(t *testing.T) {
	repo := newFakeRepo()
	id := firstSupported(t)
	repo.remembered = id
	repo.paths[id] = filepath.Join(t.TempDir(), "missing-cli.exe") // custom path that does not exist
	t.Setenv("PATH", t.TempDir())
	newRestoreService(t, repo).RestoreConnection(context.Background())
	if repo.remembered != "" {
		t.Fatalf("a provider that no longer connects must be forgotten, got %q", repo.remembered)
	}
}

func TestRestoreKeepsProviderWhenAppIsClosing(t *testing.T) {
	repo := newFakeRepo()
	id := firstSupported(t)
	repo.remembered = id
	repo.paths[id] = filepath.Join(t.TempDir(), "missing-cli.exe")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	newRestoreService(t, repo).RestoreConnection(ctx)
	if repo.remembered != id {
		t.Fatalf("closing must keep the remembered provider, got %q", repo.remembered)
	}
}

func TestDisconnectByUserForgets(t *testing.T) {
	repo := newFakeRepo()
	repo.remembered = firstSupported(t)
	if err := newRestoreService(t, repo).DisconnectByUser(context.Background()); err != nil {
		t.Fatal(err)
	}
	if repo.remembered != "" {
		t.Fatal("user disconnect must forget the provider")
	}
}

func TestRestoreWithNothingRememberedIsNoop(t *testing.T) {
	repo := newFakeRepo()
	newRestoreService(t, repo).RestoreConnection(context.Background())
	if repo.remembered != "" {
		t.Fatal("unexpected remembered provider")
	}
}
