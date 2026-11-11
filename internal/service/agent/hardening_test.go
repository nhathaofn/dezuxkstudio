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
	"app/internal/engine"
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

func TestCodexPreApprovesDezuxkMCPTools(t *testing.T) {
	args := strings.Join(codexPolicyArgs("http://127.0.0.1:1/mcp", ""), " ")
	if !strings.Contains(args, `mcp_servers.dezuxk.default_tools_approval_mode="approve"`) {
		t.Fatalf("Codex would ask for approvals this session cannot grant: %s", args)
	}
	for _, want := range []string{`sandbox_mode="read-only"`, "features.shell_tool=false"} {
		if !strings.Contains(args, want) {
			t.Fatalf("policy arg %q missing: %s", want, args)
		}
	}
}

func TestModelCatalogFollowsTheSignedInAccount(t *testing.T) {
	dir := t.TempDir()
	svc, err := New(Options{Repo: newFakeRepo(), DataDir: dir, ProfilesDir: filepath.Join(dir, "p"), Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
	id := domainagent.ProviderCodex
	plus := []domainagent.ModelOption{{ID: "gpt-6-sol", Efforts: []string{"high"}}, {ID: "gpt-6-luna"}}
	svc.models.mu.Lock()
	svc.models.catalogs[id] = plus
	svc.models.catalogOf[id] = "codex.exe"
	svc.models.loadedAt[id] = time.Now()
	svc.models.selection[id] = domainagent.ModelSelection{Model: "gpt-6-sol", Effort: "high"}
	svc.models.mu.Unlock()

	// Signing in with another account (free plan) must drop the cached Plus list.
	svc.invalidateStatus(id)
	svc.models.mu.Lock()
	_, stale := svc.models.catalogs[id]
	svc.models.catalogs[id] = []domainagent.ModelOption{{ID: "gpt-6-luna"}}
	svc.models.mu.Unlock()
	if stale {
		t.Fatal("model catalog survived an account change")
	}

	info := domainagent.ProviderInfo{ID: id}
	svc.fillModelInfo(&info)
	if info.SelectedModel != "" || info.SelectedEffort != "" {
		t.Fatalf("a model the account does not offer must not show as selected: %+v", info)
	}
	if sel := svc.effectiveSelection(id); sel.Model != "" {
		t.Fatalf("turns must fall back to the provider default, got %+v", sel)
	}
}

func TestPolicyIsRulesOnly(t *testing.T) {
	policy := SystemInstructions()
	if strings.Contains(policy, "{{app}}") || !strings.Contains(policy, clientName) {
		t.Fatal("the product name must be filled in at runtime")
	}
	// Rules only: no hardcoded language, tool names or feature lists.
	for _, r := range policy {
		if r > 0x7f && !strings.ContainsRune("—…", r) {
			t.Fatalf("policy must be language-neutral English, found %q", r)
		}
	}
	for _, name := range []string{"workflow_list_specs", "workspace_", "studio_list_capabilities", "watermark", "ffmpeg"} {
		if strings.Contains(strings.ToLower(policy), name) {
			t.Fatalf("policy must not hardcode %q; tools and features describe themselves", name)
		}
	}
}

func TestSystemPromptLayers(t *testing.T) {
	dir := t.TempDir()
	wf := &fakeWorkflow{specs: []engine.Spec{{Type: "tool.demo", Label: "Demo", Description: "Does a\n demo.",
		Inputs: []engine.Port{{Name: "images", Type: engine.PortImage}}, Params: []engine.Param{{Name: "mode"}}}}}
	svc, err := New(Options{Repo: newFakeRepo(), DataDir: dir, ProfilesDir: filepath.Join(dir, "p"), Logger: slog.New(slog.DiscardHandler), Workflow: wf, AppVersion: "1.2.3"})
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
	prompt := svc.systemPrompt(context.Background(), domainagent.ProviderAntigravity)
	policyAt := strings.Index(prompt, "<policy>")
	appAt := strings.Index(prompt, "\n<application>\n")
	featAt := strings.Index(prompt, "\n<features>\n")
	if policyAt != 0 || appAt < policyAt || featAt < appAt {
		t.Fatalf("layers must be policy → application → features (stable prefix first):\n%s", prompt)
	}
	for _, want := range []string{"version: 1.2.3", "- tool.demo (Demo): Does a demo. | inputs: images:image | settings: mode"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestScreenContextIsSentOncePerSession(t *testing.T) {
	dir := t.TempDir()
	svc, err := New(Options{Repo: newFakeRepo(), DataDir: dir, ProfilesDir: filepath.Join(dir, "p"), Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
	ui := UIContext{Screen: "Demo", SelectedFiles: []string{`D:\a.png`}}
	if got := svc.screenContextForTurn("acp:s1", ui); got.empty() {
		t.Fatal("first turn of a session must carry the screen context")
	}
	svc.rememberScreenContext("acp:s1", ui)
	if got := svc.screenContextForTurn("acp:s1", ui); !got.empty() {
		t.Fatal("an unchanged screen must not be resent")
	}
	changed := ui
	changed.SelectedFiles = []string{`D:\b.png`}
	if got := svc.screenContextForTurn("acp:s1", changed); got.empty() {
		t.Fatal("a changed screen must be sent")
	}
	if got := svc.screenContextForTurn("", ui); got.empty() {
		t.Fatal("a new session (no key) always gets the screen context")
	}
}

func TestToolsLayerOnlyForCodeModeProviders(t *testing.T) {
	dir := t.TempDir()
	svc, err := New(Options{Repo: newFakeRepo(), DataDir: dir, ProfilesDir: filepath.Join(dir, "p"), Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
	ctx := context.Background()
	codex := svc.systemPrompt(ctx, domainagent.ProviderCodex)
	for _, want := range []string{"\n<tools>\n", "tools." + mcpToolPrefix + "<name>", "- preferences_update: ", "- workflow_run_node: "} {
		if !strings.Contains(codex, want) {
			t.Fatalf("Codex (code mode) must be told which app tools exist; missing %q:\n%s", want, codex)
		}
	}
	for _, id := range []domainagent.ProviderID{domainagent.ProviderClaude, domainagent.ProviderAntigravity} {
		if strings.Contains(svc.systemPrompt(ctx, id), "\n<tools>\n") {
			t.Fatalf("%s lists tools natively; the <tools> layer would only cost tokens", id)
		}
	}
}

func TestCodexToolsLayerFollowsTheModelsToolMode(t *testing.T) {
	dir := t.TempDir()
	svc, err := New(Options{Repo: newFakeRepo(), DataDir: dir, ProfilesDir: filepath.Join(dir, "p"), Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
	ctx := context.Background()
	catalog := []byte(`{"models":[
		{"slug":"gpt-6-luna","visibility":"list","tool_mode":"code_mode_only"},
		{"slug":"gpt-5.5","visibility":"list"}]}`)
	models, _ := parseCodexCatalog(catalog)
	svc.models.mu.Lock()
	svc.models.catalogs[domainagent.ProviderCodex] = models
	svc.models.codeMode[domainagent.ProviderCodex] = parseCodexCodeModeModels(catalog)
	svc.models.mu.Unlock()
	pick := func(model string) string {
		svc.models.mu.Lock()
		svc.models.selection[domainagent.ProviderCodex] = domainagent.ModelSelection{Model: model}
		svc.models.mu.Unlock()
		return svc.systemPrompt(ctx, domainagent.ProviderCodex)
	}
	if p := pick("gpt-6-luna"); !strings.Contains(p, "Call them from the code runner") {
		t.Fatalf("code-mode model must get the <tools> layer:\n%s", p)
	}
	if p := pick("gpt-5.5"); strings.Contains(p, "<tools>") {
		t.Fatalf("a model with native tool definitions must not get the <tools> layer:\n%s", p)
	}
	if p := pick(""); !strings.Contains(p, "If they are not in your tool list") {
		t.Fatalf("an undecidable default model gets the conditional hint:\n%s", p)
	}
}

func TestConversationsStartFreshAndCanBeReopened(t *testing.T) {
	dir := t.TempDir()
	repo := newFakeRepo()
	_ = repo.SaveConversation(context.Background(), domainagent.Conversation{ID: "old", Title: "earlier", CreatedAt: 1, UpdatedAt: 1})
	_ = repo.AppendChatMessage(context.Background(), domainagent.ChatMessage{ID: "m", Role: "user", Text: "earlier", ConversationID: "old"})
	svc, err := New(Options{Repo: repo, DataDir: dir, ProfilesDir: filepath.Join(dir, "p"), Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
	ctx := context.Background()

	// Every start is a new, empty chat; earlier ones are in the list.
	if id, msgs, _ := svc.History(ctx); id != "" || len(msgs) != 0 {
		t.Fatalf("app start must open a new chat, got %q %+v", id, msgs)
	}
	if list, _ := svc.ListConversations(ctx); len(list) != 1 || list[0].ID != "old" {
		t.Fatalf("earlier chats must be listed: %+v", list)
	}
	id, msgs, err := svc.OpenConversation(ctx, "old")
	if err != nil || id != "old" || len(msgs) != 1 {
		t.Fatalf("reopen failed: %q %+v %v", id, msgs, err)
	}
	if cur, _, _ := svc.History(ctx); cur != "old" {
		t.Fatalf("an opened chat becomes current, got %q", cur)
	}
	if err := svc.NewConversation(ctx); err != nil {
		t.Fatal(err)
	}
	if cur, _, _ := svc.History(ctx); cur != "" {
		t.Fatal("new chat must leave the earlier one in history, not reuse it")
	}
	if list, _ := svc.ListConversations(ctx); len(list) != 1 {
		t.Fatalf("starting a new chat must not delete history: %+v", list)
	}
	if _, _, err := svc.OpenConversation(ctx, "missing"); err == nil {
		t.Fatal("opening an unknown chat must fail")
	}
}
