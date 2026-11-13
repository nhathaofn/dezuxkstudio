package agent

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	domainagent "app/internal/domain/agent"
	"app/internal/engine"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestFrontmatterParseRenderAndSlugValidation(t *testing.T) {
	valid := []string{"a", "brief-structuring", "reviewer-2", strings.Repeat("a", 64)}
	for _, s := range valid {
		if err := validateCustomizationSlug(s); err != nil {
			t.Errorf("expected slug %q valid, got %v", s, err)
		}
	}
	invalid := []string{"", " ", "../evil", "Upper", "a_b", "a b", "-start", "end-", strings.Repeat("a", 65)}
	for _, s := range invalid {
		if err := validateCustomizationSlug(s); err == nil {
			t.Errorf("expected slug %q rejected", s)
		}
	}

	skillMD := renderSkillMarkdown("my-skill", "Desc: #1", true, false, "# Heading\n\nBody line")
	parsedSkill, err := parseMarkdownFrontmatter([]byte(skillMD))
	if err != nil {
		t.Fatalf("parse skill markdown: %v", err)
	}
	if got := fmString(parsedSkill.Fields, "name"); got != "my-skill" {
		t.Fatalf("skill name = %q, want my-skill", got)
	}
	if got := fmString(parsedSkill.Fields, "description"); got != "Desc: #1" {
		t.Fatalf("skill description = %q, want %q", got, "Desc: #1")
	}
	if !fmBool(parsedSkill.Fields, "user-invocable", false) {
		t.Fatal("expected user-invocable = true")
	}
	if fmBool(parsedSkill.Fields, "enabled", true) {
		t.Fatal("expected enabled = false")
	}
	if parsedSkill.Body != "# Heading\n\nBody line" {
		t.Fatalf("unexpected skill body: %q", parsedSkill.Body)
	}

	subMD := renderSubagentMarkdown(
		"code-checker",
		"Checks files cleanly",
		[]string{"review-checklist", "brief-structuring"},
		[]string{"workspace_read_file", "workspace_search_text"},
		"claude",
		"claude-sonnet-4-5",
		true,
		"Inspect carefully.",
	)
	parsedSub, err := parseMarkdownFrontmatter([]byte(subMD))
	if err != nil {
		t.Fatalf("parse subagent markdown: %v", err)
	}
	if got := fmStringSlice(parsedSub.Fields, "skills"); len(got) != 2 || got[0] != "review-checklist" {
		t.Fatalf("skills slice mismatch: %v", got)
	}
	if got := fmStringSlice(parsedSub.Fields, "tools"); len(got) != 2 || got[1] != "workspace_search_text" {
		t.Fatalf("tools slice mismatch: %v", got)
	}
	if fmString(parsedSub.Fields, "provider") != "claude" || fmString(parsedSub.Fields, "model") != "claude-sonnet-4-5" {
		t.Fatalf("provider/model mismatch: %+v", parsedSub.Fields)
	}
}

func TestCustomizationManagerSHA256SyncMatrix(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "agent-customizations")
	mgr := newCustomizationManager(dir)
	if err := mgr.EnsureSynced(); err != nil {
		t.Fatalf("EnsureSynced: %v", err)
	}

	snap, err := mgr.Snapshot([]string{"workspace_read_file"})
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(snap.Skills) < 2 || len(snap.Subagents) < 2 {
		t.Fatalf("expected built-in starter skills & subagents, got %d skills / %d subagents", len(snap.Skills), len(snap.Subagents))
	}

	// Verify built-in skill starts as managed.
	var foundBrief *domainagent.SkillInfo
	for i := range snap.Skills {
		if snap.Skills[i].Name == "brief-structuring" {
			foundBrief = &snap.Skills[i]
			break
		}
	}
	if foundBrief == nil || foundBrief.SourceState != domainagent.CustomizationManaged || !foundBrief.BuiltIn {
		t.Fatalf("expected managed built-in brief-structuring, got %+v", foundBrief)
	}

	// Simulate user editing the built-in file directly on disk -> user_modified, preserved across EnsureSynced.
	_, skillDiskPath := mgr.targetFor(domainagent.CustomizationSkill, "brief-structuring")
	customBody := renderSkillMarkdown("brief-structuring", "User edited", true, true, "User custom instructions")
	if err := os.WriteFile(skillDiskPath, []byte(customBody), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := mgr.EnsureSynced(); err != nil {
		t.Fatal(err)
	}
	readBack, err := os.ReadFile(skillDiskPath)
	if err != nil || !strings.Contains(string(readBack), "User custom instructions") {
		t.Fatalf("EnsureSynced overwrote user_modified built-in: err=%v body=%q", err, string(readBack))
	}
	snap, _ = mgr.Snapshot(nil)
	for _, sk := range snap.Skills {
		if sk.Name == "brief-structuring" && sk.SourceState != domainagent.CustomizationUserModified {
			t.Fatalf("expected user_modified state after external edit, got %s", sk.SourceState)
		}
	}

	// Delete built-in -> tombstone persists across EnsureSynced until RestoreBuiltIn.
	if err := mgr.Delete(domainagent.CustomizationSkill, "brief-structuring"); err != nil {
		t.Fatalf("Delete built-in: %v", err)
	}
	if _, err := os.Stat(skillDiskPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected deleted built-in removed from disk, stat err=%v", err)
	}
	if err := mgr.EnsureSynced(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(skillDiskPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("tombstoned built-in resurrected by EnsureSynced!")
	}
	snap, _ = mgr.Snapshot(nil)
	var tombstoned bool
	for _, sk := range snap.Skills {
		if sk.Name == "brief-structuring" {
			tombstoned = sk.Tombstoned && sk.SourceState == domainagent.CustomizationTombstone
		}
	}
	if !tombstoned {
		t.Fatal("expected brief-structuring to report tombstoned=true, state=tombstone")
	}

	// RestoreBuiltIn brings back the built-in with managed state.
	if err := mgr.RestoreBuiltIn(domainagent.CustomizationSkill, "brief-structuring"); err != nil {
		t.Fatalf("RestoreBuiltIn: %v", err)
	}
	snap, _ = mgr.Snapshot(nil)
	var restoredManaged bool
	for _, sk := range snap.Skills {
		if sk.Name == "brief-structuring" {
			restoredManaged = !sk.Tombstoned && sk.SourceState == domainagent.CustomizationManaged
		}
	}
	if !restoredManaged {
		t.Fatal("expected restored built-in to return to managed state")
	}

	// Custom (user_only) skill save & clean delete.
	if err := mgr.SaveSkill("my-custom-skill", "Custom desc", true, true, "Custom steps"); err != nil {
		t.Fatalf("SaveSkill: %v", err)
	}
	snap, _ = mgr.Snapshot(nil)
	var foundCustom bool
	for _, sk := range snap.Skills {
		if sk.Name == "my-custom-skill" && sk.SourceState == domainagent.CustomizationUserOnly && !sk.BuiltIn {
			foundCustom = true
		}
	}
	if !foundCustom {
		t.Fatal("expected user_only custom skill in snapshot")
	}
	if err := mgr.Delete(domainagent.CustomizationSkill, "my-custom-skill"); err != nil {
		t.Fatalf("Delete custom skill: %v", err)
	}
	snap, _ = mgr.Snapshot(nil)
	for _, sk := range snap.Skills {
		if sk.Name == "my-custom-skill" {
			t.Fatalf("custom skill should disappear cleanly after Delete, got %+v", sk)
		}
	}
}

func TestAccessPolicyBlocksCustomizationsDir(t *testing.T) {
	f := newAccessFixture(t)
	custDir := filepath.Join(f.base, "data", "agent-customizations")
	skillFile := filepath.Join(custDir, "skills", "brief-structuring", "SKILL.md")
	subFile := filepath.Join(custDir, "subagents", "workspace-reviewer.md")

	for _, target := range []string{skillFile, subFile, filepath.Join(custDir, "notes.md")} {
		if _, err := f.svc.policy.ValidateRead(target); err == nil {
			t.Errorf("expected ValidateRead denied inside agent-customizations for %s", target)
		}
		if _, err := f.svc.policy.ValidateWrite(target); err == nil {
			t.Errorf("expected ValidateWrite denied inside agent-customizations for %s", target)
		}
	}
	if _, err := f.svc.policy.ValidateDir(custDir); err == nil {
		t.Errorf("expected ValidateDir denied for %s", custDir)
	}
}

func TestPromptCatalogAndSlashExpansion(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "agent-customizations")
	mgr := newCustomizationManager(dir)
	if err := mgr.EnsureSynced(); err != nil {
		t.Fatal(err)
	}
	if err := mgr.SaveSkill("disabled-skill", "Hidden", true, false, "Should not run"); err != nil {
		t.Fatal(err)
	}
	if err := mgr.SaveSkill("background-only", "Agent only", false, true, "Only agent reads"); err != nil {
		t.Fatal(err)
	}

	skillsBlock, subagentsBlock := mgr.PromptCatalogBlocks()
	if !strings.Contains(skillsBlock, "<skills>") || !strings.Contains(skillsBlock, "brief-structuring") {
		t.Fatalf("expected skills block to list brief-structuring, got:\n%s", skillsBlock)
	}
	if strings.Contains(skillsBlock, "disabled-skill") {
		t.Fatalf("disabled skill leaked into PromptCatalogBlocks:\n%s", skillsBlock)
	}
	if !strings.Contains(subagentsBlock, "<subagents>") || !strings.Contains(subagentsBlock, "workspace-reviewer") {
		t.Fatalf("expected subagents block to list workspace-reviewer, got:\n%s", subagentsBlock)
	}

	expanded := mgr.ExpandSlashInvocation("/brief-structuring Build landing page copy")
	if !strings.Contains(expanded, `<invoked_skill name="brief-structuring">`) || !strings.Contains(expanded, "Build landing page copy") {
		t.Fatalf("expected slash expansion, got:\n%s", expanded)
	}

	for _, unchanged := range []string{
		"regular message /brief-structuring",
		"/nonexistent-skill hello",
		"/disabled-skill hello",
		"/background-only hello",
	} {
		if got := mgr.ExpandSlashInvocation(unchanged); got != unchanged {
			t.Fatalf("expected prompt %q unmodified, got %q", unchanged, got)
		}
	}
}

func TestScopedMCPAndSubagentApprovalBlocking(t *testing.T) {
	base := t.TempDir()
	dataDir := filepath.Join(base, "data")
	ws := filepath.Join(dataDir, "agent-workspace")
	media := filepath.Join(dataDir, "media")
	profiles := filepath.Join(dataDir, "agent-profiles")
	for _, d := range []string{ws, media, profiles} {
		_ = os.MkdirAll(d, 0o700)
	}
	noteFile := filepath.Join(ws, "draft.md")
	_ = os.WriteFile(noteFile, []byte("keep me safe\n"), 0o600)

	var approvalsMu sync.Mutex
	var approvalCount int
	svc, err := New(Options{
		Repo:         newFakeRepo(),
		DataDir:      dataDir,
		WorkspaceDir: ws,
		MediaDir:     media,
		ProfilesDir:  profiles,
		Logger:       slog.New(slog.DiscardHandler),
		Workflow:     &fakeWorkflow{specs: []engine.Spec{{Type: "tool.demo", Label: "Demo"}}},
		Binaries:     fakeBinaries{},
		Preferences:  fakePrefs{},
		System:       fakeSys{dir: dataDir},
		Diagnostics:  fakeDiag{},
		OnApprovalsChanged: func(reqs []domainagent.ApprovalRequest) {
			approvalsMu.Lock()
			approvalCount += len(reqs)
			approvalsMu.Unlock()
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	srv, err := svc.ensureMCPServer()
	if err != nil {
		t.Fatal(err)
	}

	// Register a scoped session attempting to ask for subagent_run as well as read + binaries_download.
	// subagent_run must be stripped unconditionally; unrequested tools must be absent.
	scopedToken, err := srv.registerScopedSession(
		[]string{"workspace_read_file", "binaries_download", "subagent_run"},
		subagentRunMarker{
			ID:       "sub-test-1",
			Name:     "workspace-reviewer",
			Provider: domainagent.ProviderAntigravity,
		},
	)
	if err != nil {
		t.Fatalf("registerScopedSession: %v", err)
	}
	defer srv.unregisterScopedSession(scopedToken)

	transport := &mcp.StreamableClientTransport{
		Endpoint:   srv.url,
		HTTPClient: &http.Client{Transport: bearer{scopedToken}},
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "subagent-test"}, nil).Connect(ctx, transport, nil)
	if err != nil {
		t.Fatalf("connect scoped MCP: %v", err)
	}
	defer cs.Close()

	toolsRes, err := cs.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	listed := make(map[string]bool)
	for _, tl := range toolsRes.Tools {
		listed[tl.Name] = true
	}
	if !listed["workspace_read_file"] || !listed["binaries_download"] {
		t.Fatalf("expected whitelisted tools present, got %+v", listed)
	}
	for _, forbidden := range []string{"subagent_run", "binaries_list", "workflow_run", "workspace_write_file", "workspace_delete_file"} {
		if listed[forbidden] {
			t.Fatalf("forbidden/unwhitelisted tool %q exposed in scoped subagent MCP session", forbidden)
		}
	}

	// Reading via whitelisted tool works.
	readRes, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "workspace_read_file",
		Arguments: map[string]any{"path": noteFile},
	})
	if err != nil || readRes.IsError {
		t.Fatalf("workspace_read_file failed: err=%v res=%+v", err, readRes)
	}

	// Calling an approval-gated tool inside a subagent session must NOT queue approval or execute —
	// it returns approval_blocked_in_subagent instead.
	dlRes, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "binaries_download",
		Arguments: map[string]any{"id": "ffmpeg"},
	})
	if err != nil {
		t.Fatalf("binaries_download call error: %v", err)
	}
	var payload map[string]any
	if len(dlRes.Content) > 0 {
		if tc, ok := dlRes.Content[0].(*mcp.TextContent); ok {
			_ = json.Unmarshal([]byte(tc.Text), &payload)
		}
	}
	if payload["status"] != "approval_blocked_in_subagent" {
		t.Fatalf("expected status=approval_blocked_in_subagent, got %+v", payload)
	}
	if payload["subagent"] != "workspace-reviewer" || payload["tool"] != "binaries_download" {
		t.Fatalf("unexpected block metadata: %+v", payload)
	}
	approvalsMu.Lock()
	if approvalCount != 0 {
		t.Fatalf("expected 0 queued approvals from subagent session, got %d", approvalCount)
	}
	approvalsMu.Unlock()
}

type subagentTestTransport struct {
	mu            sync.Mutex
	onEvent       func(SessionUpdate)
	sessions      []string
	systemPrompts []string
	setModels     []string
	promptHook    func(ctx context.Context, sessionID, text string, onEvent func(SessionUpdate))
}

func (m *subagentTestTransport) Initialize(context.Context, string, string) (*InitializeResult, error) {
	return &InitializeResult{ProtocolVersion: 1}, nil
}

func (m *subagentTestTransport) Authenticate(context.Context, string) (string, error) {
	return "Google AI Pro", nil
}

func (m *subagentTestTransport) NewSession(_ context.Context, _ string, sysPrompt string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.systemPrompts = append(m.systemPrompts, sysPrompt)
	id := "sub-mock-sess"
	m.sessions = append(m.sessions, id)
	return id, nil
}

func (m *subagentTestTransport) AvailableModels() ([]domainagent.ModelOption, string) {
	return []domainagent.ModelOption{{ID: "gemini-2.5-pro", Name: "Gemini 2.5 Pro"}}, "gemini-2.5-pro"
}

func (m *subagentTestTransport) SetModel(_ context.Context, _ string, modelID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.setModels = append(m.setModels, modelID)
	return nil
}

func (m *subagentTestTransport) Prompt(ctx context.Context, sessionID, text string) error {
	if m.promptHook != nil {
		m.promptHook(ctx, sessionID, text, m.onEvent)
	}
	return nil
}

func (m *subagentTestTransport) Cancel(string) error { return nil }
func (m *subagentTestTransport) Close() error        { return nil }

func TestRunSubagentStreamingGuardFallbackAndProviderFlags(t *testing.T) {
	base := t.TempDir()
	ws := filepath.Join(base, "agent-workspace")
	media := filepath.Join(base, "media")
	profiles := filepath.Join(base, "agent-profiles")
	for _, d := range []string{ws, media, profiles} {
		_ = os.MkdirAll(d, 0o700)
	}
	dummyAgy := filepath.Join(base, "agy.exe")
	dummyClaude := filepath.Join(base, "claude.exe")
	for _, p := range []string{dummyAgy, dummyClaude} {
		_ = os.WriteFile(p, []byte("stub"), 0o600)
	}

	var mu sync.Mutex
	var events []domainagent.ChatEvent
	rootMock := &subagentTestTransport{}
	subMock := &subagentTestTransport{
		promptHook: func(_ context.Context, sid, _ string, emit func(SessionUpdate)) {
			if emit == nil {
				return
			}
			emit(SessionUpdate{SessionID: sid, Kind: "tool_call", ToolCallID: "t1", Tool: "workspace_list_files"})
			emit(SessionUpdate{SessionID: sid, Kind: "tool_call_update", ToolCallID: "t1", Tool: "workspace_list_files", Status: toolStatusCompleted})
			emit(SessionUpdate{SessionID: sid, Kind: "agent_message_chunk", Text: "Findings ok.\n\n```go\nfmt.Println(\"hidden\")\n```\n\nSummary complete."})
		},
	}

	var useSubMock atomic.Bool
	svc, err := New(Options{
		Repo:         newFakeRepo(),
		DataDir:      base,
		WorkspaceDir: ws,
		MediaDir:     media,
		ProfilesDir:  profiles,
		Logger:       slog.New(slog.DiscardHandler),
		Preferences:  fakePrefs{},
		OnChatEvent: func(evt domainagent.ChatEvent) {
			mu.Lock()
			events = append(events, evt)
			mu.Unlock()
		},
		ProbeClaudeAuth: func(context.Context, string, string) (domainagent.AuthState, string) {
			return domainagent.AuthUnauthenticated, ""
		},
		SpawnTransport: func(_ context.Context, _ string, _ []string, _ string, _ []string, onEvent func(SessionUpdate), _ func(error)) (Transport, error) {
			if useSubMock.Load() {
				subMock.onEvent = onEvent
				return subMock, nil
			}
			rootMock.onEvent = onEvent
			return rootMock, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()

	ctx := context.Background()
	if _, err := svc.SetCustomPath(ctx, domainagent.ProviderAntigravity, dummyAgy); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SetCustomPath(ctx, domainagent.ProviderClaude, dummyClaude); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Login(ctx, domainagent.ProviderAntigravity, ""); err != nil {
		t.Fatalf("Login antigravity OAuth: %v", err)
	}
	if _, err := svc.Connect(ctx, domainagent.ProviderAntigravity); err != nil {
		t.Fatalf("Connect antigravity: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(profiles, "antigravity", "home", ".gemini"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profiles, "antigravity", "home", ".gemini", "oauth_creds.json"), []byte(`{"token":"test"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	// Custom subagent requesting Claude + Claude model while Claude is unauthenticated -> must fall back
	// cleanly to active Antigravity root provider without forcing claude-sonnet-4-5 onto Gemini!
	if _, err := svc.SaveSubagent(ctx, domainagent.SubagentInfo{
		Name:        "cross-provider-sub",
		Description: "Tests fallback & guard",
		Skills:      []string{"review-checklist"},
		Tools:       []string{"workspace_list_files", "subagent_run"},
		Provider:    domainagent.ProviderClaude,
		Model:       "claude-sonnet-4-5",
		Enabled:     true,
		Body:        "Audit the workspace.",
	}); err != nil {
		t.Fatalf("SaveSubagent: %v", err)
	}

	parentTurn := newTurn("msg-parent-1", "conv-1", domainagent.ProviderAntigravity, 0, func() {}, func() {})
	parentTurn.ctx = ctx
	svc.mu.Lock()
	svc.turn = parentTurn
	svc.mu.Unlock()

	useSubMock.Store(true)
	res, err := svc.runSubagentTool(ctx, subagentRunToolInput{
		Name:    "cross-provider-sub",
		Task:    "Inspect workspace structure",
		Context: "Root conversation context",
	})
	if err != nil {
		t.Fatalf("runSubagentTool: %v", err)
	}
	if res["provider"] != domainagent.ProviderAntigravity {
		t.Fatalf("expected fallback provider antigravity, got %v", res["provider"])
	}
	outText, _ := res["output"].(string)
	if !strings.Contains(outText, "Findings ok.") || strings.Contains(outText, `fmt.Println("hidden")`) {
		t.Fatalf("expected assistant mode guard to keep prose & block fenced go code, got:\n%s", outText)
	}
	for _, sm := range subMock.setModels {
		if strings.Contains(sm, "claude") {
			t.Fatalf("incompatible claude model leaked to antigravity subagent SetModel: %v", subMock.setModels)
		}
	}
	if len(subMock.systemPrompts) == 0 || !strings.Contains(subMock.systemPrompts[0], `<skill name="review-checklist">`) {
		t.Fatalf("expected bound review-checklist skill injected in subagent prompt")
	}

	invocations := parentTurn.subagentList()
	if len(invocations) != 1 || invocations[0].Status != "done" || len(invocations[0].Tools) != 1 || invocations[0].Tools[0] != "workspace_list_files" {
		t.Fatalf("unexpected parentTurn.subagentList(): %+v", invocations)
	}

	mu.Lock()
	var sawStart, sawToolCall, sawToolResult, sawDone bool
	for _, ev := range events {
		switch ev.Type {
		case domainagent.ChatEventSubagentStart:
			sawStart = true
		case domainagent.ChatEventSubagentToolCall:
			sawToolCall = true
		case domainagent.ChatEventSubagentToolResult:
			sawToolResult = true
		case domainagent.ChatEventSubagentDone:
			sawDone = true
		}
	}
	mu.Unlock()
	if !sawStart || !sawToolCall || !sawToolResult || !sawDone {
		t.Fatalf("missing subagent lifecycle events: start=%v call=%v result=%v done=%v", sawStart, sawToolCall, sawToolResult, sawDone)
	}

	// Verify Claude/Codex/Antigravity provider runner helper isolation flags & dirs.
	claudeArgs := buildClaudeExecArgsWithTools("/tmp/mcp.json", "sys", []string{"workspace_read_file", "skill_read"})
	joinedClaude := strings.Join(claudeArgs, " ")
	if !strings.Contains(joinedClaude, "--allowedTools mcp__dezuxk__workspace_read_file,mcp__dezuxk__skill_read") {
		t.Fatalf("unexpected claude --allowedTools args: %s", joinedClaude)
	}

	codexInstr, codexCleanup, err := writeCodexEphemeralInstructions(profiles, "sub-codex-1", "Codex instructions")
	if err != nil {
		t.Fatalf("writeCodexEphemeralInstructions: %v", err)
	}
	codexArgs := buildCodexExecArgsForSubagent("http://127.0.0.1:9999/mcp", "Codex instructions", codexInstr, 30*time.Second)
	codexCleanup()
	if !strings.Contains(strings.Join(codexArgs, " "), "model_instructions_file=") || !strings.Contains(strings.Join(codexArgs, " "), "instructions-sub-codex-1.md") {
		t.Fatalf("expected ephemeral model_instructions_file in codex args: %v", codexArgs)
	}

	agySubRoot := filepath.Join(profiles, "antigravity", "subagents", "sub-agy-1")
	envMap, agyCleanup, err := svc.secrets.prepareAgySubProfile(filepath.Join(profiles, "antigravity"), agySubRoot, "http://127.0.0.1:9999/mcp", "tok-123")
	if err != nil {
		t.Fatalf("prepareAgySubProfile: %v", err)
	}
	if _, err := os.Stat(filepath.Join(agySubRoot, "home", ".gemini", "oauth_creds.json")); err != nil {
		t.Fatalf("expected oauth_creds copied to ephemeral subprofile: %v", err)
	}
	if envMap["HOME"] == "" || !strings.Contains(envMap["HOME"], "sub-agy-1") {
		t.Fatalf("expected isolated HOME inside agy subprofile, got %+v", envMap)
	}
	subMCPCfg, err := os.ReadFile(filepath.Join(agySubRoot, "home", ".gemini", "config", "mcp_config.json"))
	if err != nil || !strings.Contains(string(subMCPCfg), "Bearer tok-123") {
		t.Fatalf("expected scoped bearer token in agy subagent mcp_config.json: err=%v body=%q", err, string(subMCPCfg))
	}
	agyCleanup()
	if _, err := os.Stat(agySubRoot); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected agy subprofile cleaned up, stat err=%v", err)
	}
}
