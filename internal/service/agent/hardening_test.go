package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"app/internal/apperr"
	domainagent "app/internal/domain/agent"
	"app/internal/secrets"
)

func TestApprovalStoreExpiresAndClearsPerProvider(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	store := newApprovalStore(time.Minute)
	store.now = func() time.Time { return now }
	noop := func(context.Context) (any, error) { return nil, nil }

	codexReq := store.queue(domainagent.ProviderCodex, "t", "title", "sum", noop)
	claudeReq := store.queue(domainagent.ProviderClaude, "t", "title", "sum", noop)

	if !store.clearProvider(domainagent.ProviderCodex) {
		t.Fatal("clearProvider should report removal")
	}
	if _, ok := store.consume(codexReq.ID); ok {
		t.Fatal("approval of a disconnected provider must not be executable")
	}
	if got := store.list(); len(got) != 1 || got[0].ID != claudeReq.ID {
		t.Fatalf("other provider's approval must stay pending, got %+v", got)
	}

	now = now.Add(2 * time.Minute)
	if _, ok := store.consume(claudeReq.ID); ok {
		t.Fatal("expired approval must not be executable")
	}
	if got := store.list(); len(got) != 0 {
		t.Fatalf("expired approvals must be pruned, got %+v", got)
	}
}

func TestClassifyProviderErrorAndTurnResult(t *testing.T) {
	if !isAuthError(classifyProviderError(errors.New("Error: Not logged in. Please run /login"))) {
		t.Fatal("provider auth failure must be classified")
	}
	if isAuthError(classifyProviderError(errors.New("rate limited"))) {
		t.Fatal("non-auth provider error must not be classified as auth")
	}
	// An unclassified error mentioning "unauthorized" (e.g. from a tool or file name) is not an auth error.
	if isAuthError(errors.New("unauthorized.png not found")) {
		t.Fatal("only provider-boundary errors may be auth errors")
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := turnResult(cancelled, context.Canceled, time.Minute); !errors.Is(err, errTurnCancelled) {
		t.Fatalf("user cancel must map to errTurnCancelled, got %v", err)
	}
	expired, cancel2 := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel2()
	if err := turnResult(expired, context.DeadlineExceeded, 3*time.Minute); err == nil || !strings.Contains(err.Error(), "timed out after 3m0s") {
		t.Fatalf("expired turn must report a readable timeout, got %v", err)
	}
}

func TestPickAuthMethod(t *testing.T) {
	spec := domainagent.ProviderSpec{Name: "Agent", AuthMethodPreference: []string{"oauth-personal"}}
	mk := func(ids ...string) *InitializeResult {
		r := &InitializeResult{}
		for _, id := range ids {
			r.AuthMethods = append(r.AuthMethods, struct {
				ID   string `json:"id"`
				Name string `json:"name"`
				Type string `json:"type"`
			}{ID: id})
		}
		return r
	}
	cases := []struct {
		init *InitializeResult
		want string
		err  bool
	}{
		{mk(), "oauth-personal", false},
		{mk("gemini-api-key", "oauth-personal"), "oauth-personal", false},
		{mk("gemini-api-key", "oauth-google"), "oauth-google", false},
		{mk("gemini-api-key"), "", true},
	}
	for i, c := range cases {
		got, err := pickAuthMethod(spec, c.init)
		if (err != nil) != c.err || got != c.want {
			t.Errorf("case %d: got %q, %v; want %q (err=%v)", i, got, err, c.want, c.err)
		}
	}
}

func TestHistoryPromptKeepsNewestConversationMessages(t *testing.T) {
	var history []domainagent.ChatMessage
	for i := range 4 {
		history = append(history,
			domainagent.ChatMessage{Role: "user", Text: fmt.Sprintf("question-%d", i)},
			domainagent.ChatMessage{Role: "error", Text: "boom"},
		)
	}
	got := buildHistoryContextPrompt(history, UIContext{}, "now", Limits{ContextMessages: 3, ContextMessageChars: 5})
	if strings.Contains(got, "boom") || strings.Contains(got, "quest-0") {
		t.Fatalf("errors must be skipped and only the newest messages kept:\n%s", got)
	}
	for _, want := range []string{"User: quest...", "now"} {
		if !strings.Contains(got, want) {
			t.Fatalf("prompt missing %q:\n%s", want, got)
		}
	}
	if n := strings.Count(got, "User: "); n != 3 {
		t.Fatalf("want 3 replayed messages after filtering errors, got %d:\n%s", n, got)
	}
}

func TestStatusCacheAvoidsRepeatedCLIProbesAndCancelIsNotAnError(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "codex.exe")
	if err := os.WriteFile(exe, []byte("x"), 0o700); err != nil {
		t.Fatal(err)
	}
	var probes atomic.Int32
	started := make(chan struct{})
	var mu sync.Mutex
	var events []domainagent.ChatEvent
	done := make(chan struct{}, 1)
	repo := newFakeRepo()
	svc, err := New(Options{
		Repo:         repo,
		DataDir:      dir,
		WorkspaceDir: filepath.Join(dir, "ws"),
		ProfilesDir:  filepath.Join(dir, "profiles"),
		Logger:       slog.New(slog.DiscardHandler),
		OnChatEvent: func(evt domainagent.ChatEvent) {
			mu.Lock()
			events = append(events, evt)
			mu.Unlock()
			if evt.Type == domainagent.ChatEventDone || evt.Type == domainagent.ChatEventError {
				done <- struct{}{}
			}
		},
		ProbeCodexAuth: func(context.Context, string, string) (domainagent.AuthState, string) {
			probes.Add(1)
			return domainagent.AuthAuthenticated, "ok"
		},
		RunCodexTurn: func(ctx context.Context, _, _, _, _, _, _, _ string) error {
			close(started)
			<-ctx.Done()
			return ctx.Err()
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
	ctx := context.Background()
	if _, err := svc.SetCustomPath(ctx, domainagent.ProviderCodex, exe); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Connect(ctx, domainagent.ProviderCodex); err != nil {
		t.Fatal(err)
	}
	before := probes.Load()
	for range 3 {
		if _, err := svc.Probe(ctx, domainagent.ProviderCodex); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := svc.SendPrompt(ctx, "hello"); err != nil {
		t.Fatal(err)
	}
	<-started
	if err := svc.CancelPrompt(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("turn did not finish after cancel")
	}
	if got := probes.Load(); got != before {
		t.Fatalf("auth CLI must not be re-probed within the cache TTL, probes went %d -> %d", before, got)
	}
	mu.Lock()
	last := events[len(events)-1]
	mu.Unlock()
	if last.Type != domainagent.ChatEventDone {
		t.Fatalf("cancel must end the turn as done, got %+v", last)
	}
	msgs, _ := repo.ListChatMessages(ctx, 50)
	for _, m := range msgs {
		if m.Role == "error" {
			t.Fatalf("cancel must not persist an error message: %+v", m)
		}
	}
}

func TestWindowsPathProblems(t *testing.T) {
	cases := []struct {
		path    string
		network bool
		blocked bool
	}{
		{`D:\Photos\a.png`, false, false},
		{`\\?\C:\Windows\win.ini`, false, true},
		{`\\.\PhysicalDrive0`, false, true},
		{`//?/C:/Windows`, false, true},
		{`\\attacker\share\x.txt`, false, true},
		{`\\nas\media\clip.mp4`, true, false}, // inside a share the user trusted
		{`D:\x\run.bat:payload.txt`, false, true},
	}
	for _, c := range cases {
		if got := windowsPathProblemFor(c.path, c.network) != ""; got != c.blocked {
			t.Errorf("%s (network=%v): blocked=%v, want %v", c.path, c.network, got, c.blocked)
		}
	}
}

func TestProviderAPIKeysLiveInCredentialStore(t *testing.T) {
	store := secrets.NewMemory()
	setProfileSecretStore(store)
	t.Cleanup(func() { setProfileSecretStore(secrets.NewMemory()) })
	profile := t.TempDir()

	// A key written as a plain file by an older build is migrated on first read.
	legacy := agyAPIKeyPath(profile)
	if err := os.WriteFile(legacy, []byte("AIza-legacy\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := loadAgyAPIKey(profile); got != "AIza-legacy" {
		t.Fatalf("legacy key not read: %q", got)
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatal("legacy plain-text key file must be removed after migration")
	}

	if err := saveAgyAPIKey(profile, "AIza-new"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatal("saving a key must not write it to disk")
	}
	if got := loadAgyAPIKey(profile); got != "AIza-new" {
		t.Fatalf("got %q", got)
	}
	clearAgyAuth(profile)
	if got := loadAgyAPIKey(profile); got != "" {
		t.Fatalf("key must be gone after sign-out, got %q", got)
	}
}

func TestUserFacingErrorsCarryI18nKeys(t *testing.T) {
	err := toUserError(apperr.CodeUnauthorized, localize(errors.New("not signed in"), msgNotSignedIn, map[string]string{"name": "Claude"}))
	if err.Details["msg_key"] != msgNotSignedIn || err.Message != "not signed in" {
		t.Fatalf("details = %+v, message = %q", err.Details, err.Message)
	}
	timeout := turnResult(expiredContext(t), nil, time.Minute)
	if key, params := messageOf(timeout); key != msgTurnTimeout || params["duration"] != "1m0s" {
		t.Fatalf("timeout key = %q %v", key, params)
	}
	// Classification as an auth error keeps the key reachable.
	if key, _ := messageOf(classifyProviderError(localize(errors.New("not logged in"), msgNotSignedIn, nil))); key != msgNotSignedIn {
		t.Fatalf("key lost through classification: %q", key)
	}
}

func expiredContext(t *testing.T) context.Context {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	t.Cleanup(cancel)
	return ctx
}
