package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"app/internal/apperr"
	"app/internal/contracts"
	domainagent "app/internal/domain/agent"
	domainbinaries "app/internal/domain/binaries"
	domainprefs "app/internal/domain/preferences"
	"app/internal/engine"
)

type fakeRepo struct {
	remembered  domainagent.ProviderID
	mu          sync.Mutex
	paths       map[domainagent.ProviderID]string
	messages    []domainagent.ChatMessage
	allowedDirs []string
	models      map[domainagent.ProviderID]domainagent.ModelSelection

	conversations map[string]domainagent.Conversation
	cliSessions   map[string]map[domainagent.ProviderID]string
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{paths: make(map[domainagent.ProviderID]string), models: make(map[domainagent.ProviderID]domainagent.ModelSelection)}
}

func (r *fakeRepo) GetModelSelection(_ context.Context, id domainagent.ProviderID) (domainagent.ModelSelection, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.models[id], nil
}

func (r *fakeRepo) SetModelSelection(_ context.Context, id domainagent.ProviderID, sel domainagent.ModelSelection) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.models[id] = sel
	return nil
}

func (r *fakeRepo) GetCustomPath(_ context.Context, id domainagent.ProviderID) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.paths[id], nil
}

func (r *fakeRepo) GetAllCustomPaths(_ context.Context) (map[domainagent.ProviderID]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := make(map[domainagent.ProviderID]string, len(r.paths))
	for k, v := range r.paths {
		cp[k] = v
	}
	return cp, nil
}

func (r *fakeRepo) SetCustomPath(_ context.Context, id domainagent.ProviderID, customPath string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.paths[id] = customPath
	return nil
}

func (r *fakeRepo) AppendChatMessage(_ context.Context, msg domainagent.ChatMessage) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.messages = append(r.messages, msg)
	return nil
}

func (r *fakeRepo) RedactChatMessages(_ context.Context, secret, replacement string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.messages {
		r.messages[i].Text = strings.ReplaceAll(r.messages[i].Text, secret, replacement)
		r.messages[i].Thought = strings.ReplaceAll(r.messages[i].Thought, secret, replacement)
		r.messages[i].Tool = strings.ReplaceAll(r.messages[i].Tool, secret, replacement)
	}
	return nil
}

func (r *fakeRepo) RememberedConnection(_ context.Context) (domainagent.ProviderID, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.remembered, nil
}

func (r *fakeRepo) SetRememberedConnection(_ context.Context, id domainagent.ProviderID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.remembered = id
	return nil
}

func (r *fakeRepo) ClearChatMessages(_ context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.messages = nil
	return nil
}

func (r *fakeRepo) SaveConversation(_ context.Context, c domainagent.Conversation) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.conversations == nil {
		r.conversations = make(map[string]domainagent.Conversation)
	}
	if old, ok := r.conversations[c.ID]; ok {
		if old.Title != "" {
			c.Title = old.Title
		}
		c.CreatedAt = old.CreatedAt
	}
	r.conversations[c.ID] = c
	return nil
}

func (r *fakeRepo) ListConversations(_ context.Context, _ int) ([]domainagent.Conversation, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []domainagent.Conversation
	for _, c := range r.conversations {
		for _, m := range r.messages {
			if m.ConversationID == c.ID {
				c.MessageCount++
			}
		}
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt > out[j].UpdatedAt })
	return out, nil
}

func (r *fakeRepo) ListConversationMessages(_ context.Context, id string, limit int) ([]domainagent.ChatMessage, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []domainagent.ChatMessage
	for _, m := range r.messages {
		if m.ConversationID == id {
			out = append(out, m)
		}
	}
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out, nil
}

func (r *fakeRepo) GetConversationSessions(_ context.Context, conversationID string) (map[domainagent.ProviderID]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	src := r.cliSessions[conversationID]
	out := make(map[domainagent.ProviderID]string, len(src))
	for k, v := range src {
		if v != "" {
			out[k] = v
		}
	}
	return out, nil
}

func (r *fakeRepo) SetConversationSession(_ context.Context, conversationID string, providerID domainagent.ProviderID, sessionID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cliSessions == nil {
		r.cliSessions = make(map[string]map[domainagent.ProviderID]string)
	}
	if sessionID == "" {
		if r.cliSessions[conversationID] != nil {
			delete(r.cliSessions[conversationID], providerID)
		}
		return nil
	}
	if r.cliSessions[conversationID] == nil {
		r.cliSessions[conversationID] = make(map[domainagent.ProviderID]string)
	}
	r.cliSessions[conversationID][providerID] = sessionID
	return nil
}

func (r *fakeRepo) DeleteConversation(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.conversations, id)
	delete(r.cliSessions, id)
	kept := r.messages[:0]
	for _, m := range r.messages {
		if m.ConversationID != id {
			kept = append(kept, m)
		}
	}
	r.messages = kept
	return nil
}

func (r *fakeRepo) ListAllowedDirs(_ context.Context) ([]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := make([]string, len(r.allowedDirs))
	copy(cp, r.allowedDirs)
	return cp, nil
}

func (r *fakeRepo) AddAllowedDir(_ context.Context, dir string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, d := range r.allowedDirs {
		if d == dir {
			return nil
		}
	}
	r.allowedDirs = append(r.allowedDirs, dir)
	return nil
}

func (r *fakeRepo) RemoveAllowedDir(_ context.Context, dir string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.allowedDirs))
	for _, d := range r.allowedDirs {
		if d != dir {
			out = append(out, d)
		}
	}
	r.allowedDirs = out
	return nil
}

type fakeWorkflow struct {
	specs     []engine.Spec
	lastNode  string
	lastInput json.RawMessage
}

func (w *fakeWorkflow) Specs(context.Context) []engine.Spec { return w.specs }
func (w *fakeWorkflow) RunNode(_ context.Context, nodeType string, input json.RawMessage) (any, error) {
	w.lastNode = nodeType
	w.lastInput = input
	return map[string]any{"status": "done", "node": nodeType}, nil
}
func (w *fakeWorkflow) RunGraph(context.Context, engine.Graph) (engine.GraphResult, error) {
	return engine.GraphResult{}, nil
}

type fakeBinaries struct{}

func (fakeBinaries) List(context.Context) ([]domainbinaries.BinaryInfo, error) {
	return []domainbinaries.BinaryInfo{{ID: "ffmpeg", Name: "FFmpeg", Status: domainbinaries.StatusAvailable}}, nil
}
func (fakeBinaries) Probe(context.Context, domainbinaries.BinaryID) (domainbinaries.BinaryInfo, error) {
	return domainbinaries.BinaryInfo{ID: "ffmpeg", Name: "FFmpeg", Status: domainbinaries.StatusAvailable}, nil
}
func (fakeBinaries) Download(context.Context, domainbinaries.BinaryID) (domainbinaries.BinaryInfo, error) {
	return domainbinaries.BinaryInfo{ID: "ffmpeg", Name: "FFmpeg", Status: domainbinaries.StatusAvailable}, nil
}

type fakePrefs struct{}

func (fakePrefs) Get(context.Context) (domainprefs.Preferences, error) {
	return domainprefs.Defaults(), nil
}
func (fakePrefs) Update(_ context.Context, p domainprefs.Patch) (domainprefs.Preferences, error) {
	return domainprefs.Defaults().Apply(p)
}

type fakeSys struct{ dir string }

func (f fakeSys) Info() contracts.SystemInfo {
	return contracts.SystemInfo{Paths: contracts.PathsInfo{Data: f.dir}}
}

type fakeDiag struct{}

func (fakeDiag) Export(context.Context) (contracts.DiagnosticsBundle, error) {
	return contracts.DiagnosticsBundle{Path: "diag.zip"}, nil
}

func TestRedactSecrets(t *testing.T) {
	in := "Authorization: Bearer abc123.xyz token=my_secret_value sk-1234567890abcdef"
	got := redactSecrets(in)
	if strings.Contains(got, "abc123.xyz") || strings.Contains(got, "my_secret_value") || strings.Contains(got, "sk-1234567890abcdef") {
		t.Fatalf("expected secrets redacted, got %q", got)
	}
	prose := "401 Unauthorized: Missing bearer or basic authentication in header"
	if got := redactSecrets(prose); got != prose {
		t.Fatalf("plain prose must stay readable, got %q", got)
	}
}

func TestMCPServerAuthAndTools(t *testing.T) {
	base := t.TempDir()
	ws := filepath.Join(base, "agent-workspace")
	media := filepath.Join(base, "media")
	profiles := filepath.Join(base, "agent-profiles")
	_ = os.MkdirAll(ws, 0o700)
	_ = os.MkdirAll(media, 0o700)
	_ = os.MkdirAll(profiles, 0o700)

	wf := &fakeWorkflow{
		specs: []engine.Spec{{Type: "tool.watermark_remover", Label: "Watermark Remover"}},
	}
	var approvalsMu sync.Mutex
	var lastApprovals []domainagent.ApprovalRequest

	svc, err := New(Options{
		Repo:         newFakeRepo(),
		DataDir:      base,
		WorkspaceDir: ws,
		MediaDir:     media,
		ProfilesDir:  profiles,
		AppVersion:   "0.1.0",
		Logger:       slog.New(slog.DiscardHandler),
		Workflow:     wf,
		Binaries:     fakeBinaries{},
		Preferences:  fakePrefs{},
		System:       fakeSys{dir: base},
		Diagnostics:  fakeDiag{},
		OnApprovalsChanged: func(reqs []domainagent.ApprovalRequest) {
			approvalsMu.Lock()
			lastApprovals = reqs
			approvalsMu.Unlock()
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()

	srv, err := svc.ensureMCPServer()
	if err != nil {
		t.Fatalf("ensureMCPServer failed: %v", err)
	}

	// Unauthorized request must fail with 401.
	req, _ := http.NewRequest(http.MethodPost, srv.url, strings.NewReader(`{}`))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 without bearer token, got %d", resp.StatusCode)
	}

	// Capabilities snapshot exposes workflow nodes dynamically.
	snap := svc.buildCapabilitiesSnapshot(context.Background())
	rawSnap, _ := json.Marshal(snap)
	var parsed struct {
		Features struct {
			Workflow []struct {
				Type string `json:"type"`
			} `json:"workflow_features"`
		} `json:"dezuxkstudio_features"`
		Extras struct {
			Note  string   `json:"note"`
			Items []string `json:"items"`
		} `json:"assistant_extras"`
	}
	if err := json.Unmarshal(rawSnap, &parsed); err != nil {
		t.Fatal(err)
	}
	if len(parsed.Features.Workflow) != 1 || parsed.Features.Workflow[0].Type != "tool.watermark_remover" {
		t.Fatalf("expected live workflow features in the app group, got %s", rawSnap)
	}
	if len(parsed.Extras.Items) == 0 || !strings.HasPrefix(parsed.Extras.Note, "NOT DezuxkStudio features") {
		t.Fatalf("expected assistant extras to be a separate, clearly labelled group, got %s", rawSnap)
	}

	// High-impact inplace action queues UI approval card and executes only upon desktop approval.
	qReq, err := svc.queueApproval("workflow_run_node", "Run watermark", "inplace overwrite", func(ctx context.Context) (any, error) {
		return wf.RunNode(ctx, "tool.watermark_remover", json.RawMessage(`{"inplace":true}`))
	})
	if err != nil {
		t.Fatal(err)
	}
	approvalsMu.Lock()
	if len(lastApprovals) != 1 || lastApprovals[0].ID != qReq.ID {
		approvalsMu.Unlock()
		t.Fatalf("expected 1 queued approval, got %+v", lastApprovals)
	}
	approvalsMu.Unlock()

	if err := svc.ResolveApproval(context.Background(), qReq.ID, true); err != nil {
		t.Fatalf("ResolveApproval failed: %v", err)
	}
	if wf.lastNode != "tool.watermark_remover" {
		t.Fatalf("expected approved node to execute, got %q", wf.lastNode)
	}
	// Replay must fail (consumed once).
	if err := svc.ResolveApproval(context.Background(), qReq.ID, true); !apperr.IsCode(err, apperr.CodeNotFound) {
		t.Fatalf("expected second ResolveApproval on consumed card to return CodeNotFound, got %v", err)
	}
}

func TestResolveApproval(t *testing.T) {
	base := t.TempDir()
	svc, err := New(Options{
		Repo:        newFakeRepo(),
		DataDir:     base,
		ProfilesDir: filepath.Join(base, "profiles"),
		Logger:      slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("New service failed: %v", err)
	}
	defer svc.Close()

	ctx := context.Background()

	t.Run("reject returns nil and consumes without executing", func(t *testing.T) {
		called := false
		req, err := svc.queueApproval("workflow_run_node", "Run", "summary", func(context.Context) (any, error) {
			called = true
			return "ok", nil
		})
		if err != nil {
			t.Fatalf("queueApproval failed: %v", err)
		}
		if err := svc.ResolveApproval(ctx, req.ID, false); err != nil {
			t.Fatalf("expected nil on reject, got %v", err)
		}
		if called {
			t.Fatal("rejected approval must not execute action")
		}
		if err := svc.ResolveApproval(ctx, req.ID, true); !apperr.IsCode(err, apperr.CodeNotFound) {
			t.Fatalf("expected CodeNotFound after rejection consumed card, got %v", err)
		}
	})

	t.Run("missing and consumed return CodeNotFound", func(t *testing.T) {
		err := svc.ResolveApproval(ctx, "missing-id", true)
		var ae *apperr.Error
		if !errors.As(err, &ae) || ae.Code != apperr.CodeNotFound {
			t.Fatalf("expected *apperr.Error with CodeNotFound, got %v", err)
		}
		if ae.Details["msg_key"] != msgApprovalGone {
			t.Fatalf("expected msg_key %q, got %+v", msgApprovalGone, ae.Details)
		}

		req, qErr := svc.queueApproval("workflow_run_node", "Run", "summary", func(context.Context) (any, error) {
			return nil, nil
		})
		if qErr != nil {
			t.Fatalf("queueApproval failed: %v", qErr)
		}
		if err := svc.ResolveApproval(ctx, req.ID, true); err != nil {
			t.Fatalf("first ResolveApproval failed: %v", err)
		}
		if err := svc.ResolveApproval(ctx, req.ID, true); !apperr.IsCode(err, apperr.CodeNotFound) {
			t.Fatalf("expected CodeNotFound on already-consumed card, got %v", err)
		}
	})

	t.Run("preserves existing apperr.Error", func(t *testing.T) {
		directErr := apperr.Validation("invalid output folder").WithDetails(map[string]any{"field": "output_dir"})
		reqDirect, err := svc.queueApproval("workflow_run_node", "Run", "direct", func(context.Context) (any, error) {
			return nil, directErr
		})
		if err != nil {
			t.Fatalf("queueApproval failed: %v", err)
		}
		gotDirect := svc.ResolveApproval(ctx, reqDirect.ID, true)
		var ae *apperr.Error
		if !errors.As(gotDirect, &ae) || ae != directErr {
			t.Fatalf("expected preserved direct *apperr.Error (%v), got %v", directErr, gotDirect)
		}

		wrappedInner := apperr.New(apperr.CodeConflict, "target already exists").WithDetails(map[string]any{"msg_key": "conflict"})
		reqWrapped, err := svc.queueApproval("workflow_run_node", "Run", "wrapped", func(context.Context) (any, error) {
			return nil, fmt.Errorf("outer context: %w", wrappedInner)
		})
		if err != nil {
			t.Fatalf("queueApproval failed: %v", err)
		}
		gotWrapped := svc.ResolveApproval(ctx, reqWrapped.ID, true)
		ae = nil
		if !errors.As(gotWrapped, &ae) || ae != wrappedInner {
			t.Fatalf("expected preserved wrapped *apperr.Error (%v), got %v", wrappedInner, gotWrapped)
		}
	})

	t.Run("wraps raw error as CodeUnavailable without leaking details", func(t *testing.T) {
		rawErr := errors.New(`open C:\Users\alice\secret\video.mp4: permission denied`)
		req, err := svc.queueApproval("workflow_run_node", "Run", "raw", func(context.Context) (any, error) {
			return nil, rawErr
		})
		if err != nil {
			t.Fatalf("queueApproval failed: %v", err)
		}
		got := svc.ResolveApproval(ctx, req.ID, true)
		var ae *apperr.Error
		if !errors.As(got, &ae) {
			t.Fatalf("expected *apperr.Error, got %T: %v", got, got)
		}
		if ae.Code != apperr.CodeUnavailable {
			t.Fatalf("expected CodeUnavailable, got %q", ae.Code)
		}
		if ae.Message != "approved action failed" {
			t.Fatalf("expected sanitized message %q, got %q", "approved action failed", ae.Message)
		}
		if strings.Contains(ae.Message, "alice") || strings.Contains(ae.Message, "permission denied") {
			t.Fatalf("user-facing message leaked internal details: %q", ae.Message)
		}
		if !errors.Is(got, rawErr) {
			t.Fatalf("expected wrapped cause to remain accessible via errors.Is, got %v", got)
		}
	})
}

func TestACPClientProtocolOverPipes(t *testing.T) {
	clientToSrvR, clientToSrvW := io.Pipe()
	srvToClientR, srvToClientW := io.Pipe()
	stderrR, stderrW := io.Pipe()

	var eventsMu sync.Mutex
	var updates []SessionUpdate
	exited := make(chan error, 1)

	client := NewACPClient(
		clientToSrvW,
		srvToClientR,
		stderrR,
		[]string{"studio_list_capabilities"},
		slog.New(slog.DiscardHandler),
		func(u SessionUpdate) {
			eventsMu.Lock()
			updates = append(updates, u)
			eventsMu.Unlock()
		},
		func(err error) {
			exited <- err
		},
	)

	var seenInit map[string]any
	var seenNew map[string]any
	var seenPermOutcomes []string

	go func() {
		defer srvToClientW.Close()
		defer stderrW.Close()
		reader := bufio.NewReader(clientToSrvR)
		writeMsg := func(v any) {
			b, _ := json.Marshal(v)
			_, _ = srvToClientW.Write(append(b, '\n'))
		}

		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			var req struct {
				ID     json.RawMessage `json:"id"`
				Method string          `json:"method"`
				Params map[string]any  `json:"params"`
			}
			if err := json.Unmarshal([]byte(line), &req); err != nil {
				continue
			}

			switch req.Method {
			case "initialize":
				seenInit = req.Params
				writeMsg(map[string]any{
					"jsonrpc": "2.0",
					"id":      req.ID,
					"result": map[string]any{
						"protocolVersion": 1,
						"agentInfo": map[string]any{
							"name":    "MockAntigravity",
							"version": "1.2.3",
						},
					},
				})
			case "session/new":
				seenNew = req.Params
				writeMsg(map[string]any{
					"jsonrpc": "2.0",
					"id":      req.ID,
					"result": map[string]any{
						"sessionId": "sess-test-1",
					},
				})
			case "session/prompt":
				// 1. Non-MCP coding/terminal permission -> must be cancelled.
				writeMsg(map[string]any{
					"jsonrpc": "2.0",
					"id":      "perm-code",
					"method":  "session/request_permission",
					"params": map[string]any{
						"toolCall": map[string]any{"title": "run_command: git status"},
						"options":  []map[string]any{{"optionId": "allow-1", "kind": "allow_once"}},
					},
				})
				if permLine, permErr := reader.ReadString('\n'); permErr == nil {
					var resp struct {
						Result struct {
							Outcome struct {
								Outcome string `json:"outcome"`
							} `json:"outcome"`
						} `json:"result"`
					}
					_ = json.Unmarshal([]byte(permLine), &resp)
					seenPermOutcomes = append(seenPermOutcomes, resp.Result.Outcome.Outcome)
				}
				// 2. DezuxkStudio MCP permission -> must be selected.
				writeMsg(map[string]any{
					"jsonrpc": "2.0",
					"id":      "perm-mcp",
					"method":  "session/request_permission",
					"params": map[string]any{
						"toolCall": map[string]any{"title": "dezuxk/studio_list_capabilities"},
						"options":  []map[string]any{{"optionId": "allow-mcp", "kind": "allow_once"}},
					},
				})
				if permLine, permErr := reader.ReadString('\n'); permErr == nil {
					var resp struct {
						Result struct {
							Outcome struct {
								Outcome string `json:"outcome"`
							} `json:"outcome"`
						} `json:"result"`
					}
					_ = json.Unmarshal([]byte(permLine), &resp)
					seenPermOutcomes = append(seenPermOutcomes, resp.Result.Outcome.Outcome)
				}

				writeMsg(map[string]any{
					"jsonrpc": "2.0",
					"method":  "session/update",
					"params": map[string]any{
						"sessionId": "sess-test-1",
						"update": map[string]any{
							"sessionUpdate": "agent_thought_chunk",
							"content":       map[string]any{"type": "text", "text": "thinking..."},
						},
					},
				})
				writeMsg(map[string]any{
					"jsonrpc": "2.0",
					"method":  "session/update",
					"params": map[string]any{
						"sessionId": "sess-test-1",
						"update": map[string]any{
							"sessionUpdate": "agent_message_chunk",
							"content":       map[string]any{"type": "text", "text": "Hello from agent"},
						},
					},
				})
				writeMsg(map[string]any{
					"jsonrpc": "2.0",
					"id":      req.ID,
					"result":  map[string]any{"stopReason": "end_turn"},
				})
			}
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	initRes, err := client.Initialize(ctx, "DezuxkStudio", "0.1.0")
	if err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}
	if initRes.ProtocolVersion != 1 || initRes.AgentInfo.Name != "MockAntigravity" {
		t.Fatalf("unexpected init result: %+v", initRes)
	}
	caps, _ := seenInit["clientCapabilities"].(map[string]any)
	fsCaps, _ := caps["fs"].(map[string]any)
	if fsCaps["readTextFile"] != false || fsCaps["writeTextFile"] != false || caps["terminal"] != false {
		t.Fatalf("expected fs/terminal capabilities disabled, got %+v", caps)
	}

	sessID, err := client.NewSession(ctx, "C:\\workspace", "System instructions")
	if err != nil || sessID != "sess-test-1" {
		t.Fatalf("NewSession got (%q, %v)", sessID, err)
	}
	meta, _ := seenNew["_meta"].(map[string]any)
	agyMeta, _ := meta["antigravity"].(map[string]any)
	sysPrompt, _ := agyMeta["systemPrompt"].(map[string]any)
	if sysPrompt["append"] != "System instructions" {
		t.Fatalf("expected _meta.antigravity.systemPrompt.append, got %+v", seenNew)
	}

	if err := client.Prompt(ctx, sessID, "hi"); err != nil {
		t.Fatalf("Prompt failed: %v", err)
	}
	if len(seenPermOutcomes) != 2 || seenPermOutcomes[0] != "cancelled" || seenPermOutcomes[1] != "selected" {
		t.Fatalf("expected [cancelled, selected] permission outcomes, got %+v", seenPermOutcomes)
	}

	eventsMu.Lock()
	if len(updates) != 2 || updates[0].Kind != "agent_thought_chunk" || updates[1].Text != "Hello from agent" {
		eventsMu.Unlock()
		t.Fatalf("unexpected updates: %+v", updates)
	}
	eventsMu.Unlock()

	if err := client.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
	<-exited
}

type mockTransport struct {
	initRes     *InitializeResult
	initErr     error
	authTier    string
	authErr     error
	sessionErr  error
	promptErr   error
	onEvent     func(SessionUpdate)
	onExit      func(error)
	closed      bool
	cancelled   bool
	mu          sync.Mutex
	promptGate  chan struct{}
	promptCalls int

	models        []domainagent.ModelOption
	currentModel  string
	setModelCalls []string
}

func (m *mockTransport) Initialize(context.Context, string, string) (*InitializeResult, error) {
	if m.initErr != nil {
		return nil, m.initErr
	}
	return m.initRes, nil
}

func (m *mockTransport) Authenticate(context.Context, string) (string, error) {
	if m.authErr != nil {
		return "", m.authErr
	}
	return m.authTier, nil
}

func (m *mockTransport) NewSession(context.Context, string, string) (string, error) {
	if m.sessionErr != nil {
		return "", m.sessionErr
	}
	return "mock-session-1", nil
}

func (m *mockTransport) Prompt(ctx context.Context, sessionID, _ string) error {
	m.mu.Lock()
	m.promptCalls++
	gate := m.promptGate
	m.mu.Unlock()

	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if m.promptErr != nil {
		return m.promptErr
	}
	if m.onEvent != nil {
		m.onEvent(SessionUpdate{SessionID: sessionID, Kind: "agent_thought_chunk", Text: "plan"})
		m.onEvent(SessionUpdate{SessionID: sessionID, Kind: "agent_message_chunk", Text: "pong"})
	}
	return nil
}

func (m *mockTransport) AvailableModels() ([]domainagent.ModelOption, string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.models, m.currentModel
}

func (m *mockTransport) SetModel(_ context.Context, _, modelID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.setModelCalls = append(m.setModelCalls, modelID)
	m.currentModel = modelID
	return nil
}

func (m *mockTransport) Cancel(string) error {
	m.mu.Lock()
	m.cancelled = true
	m.mu.Unlock()
	return nil
}

func (m *mockTransport) Close() error {
	m.mu.Lock()
	m.closed = true
	m.mu.Unlock()
	return nil
}

func TestServiceConnectChatAndIsolatedCLIs(t *testing.T) {
	dir := t.TempDir()
	dummyAgy := filepath.Join(dir, "agy.exe")
	dummyCodex := filepath.Join(dir, "codex.exe")
	dummyClaude := filepath.Join(dir, "claude.exe")
	for _, p := range []string{dummyAgy, dummyCodex, dummyClaude} {
		if err := os.WriteFile(p, []byte("stub"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	repo := newFakeRepo()
	var chats []domainagent.ChatEvent
	var mu sync.Mutex
	doneCh := make(chan domainagent.ChatEvent, 4)
	var currentMock *mockTransport
	var codexPromptSeen string
	var claudePromptSeen string

	svc, err := New(Options{
		Repo:         repo,
		DataDir:      dir,
		WorkspaceDir: filepath.Join(dir, "ws"),
		MediaDir:     filepath.Join(dir, "media"),
		ProfilesDir:  filepath.Join(dir, "agent-profiles"),
		AppVersion:   "0.1.0",
		Logger:       slog.New(slog.DiscardHandler),
		OnChatEvent: func(evt domainagent.ChatEvent) {
			mu.Lock()
			chats = append(chats, evt)
			mu.Unlock()
			if evt.Type == domainagent.ChatEventDone || evt.Type == domainagent.ChatEventError {
				doneCh <- evt
			}
		},
		SpawnTransport: func(_ context.Context, _ string, _ []string, _ string, _ []string, onEvent func(SessionUpdate), onExit func(error)) (Transport, error) {
			currentMock.onEvent = onEvent
			currentMock.onExit = onExit
			return currentMock, nil
		},
		ProbeCodexAuth: func(context.Context, string, string) (domainagent.AuthState, string) {
			return domainagent.AuthAuthenticated, "Logged in as isolated-test"
		},
		RunCodexTurn: func(_ context.Context, _, profileRoot, _, mcpURL, mcpToken, messageID, prompt string) error {
			if !strings.Contains(profileRoot, "codex") || mcpURL == "" || mcpToken == "" {
				return errors.New("expected isolated profileRoot and MCP config")
			}
			codexPromptSeen = prompt
			return nil
		},
		ProbeClaudeAuth: func(context.Context, string, string) (domainagent.AuthState, string) {
			return domainagent.AuthAuthenticated, "isolated-claude@example.com (pro)"
		},
		RunClaudeTurn: func(_ context.Context, _, profileRoot, _, mcpURL, mcpToken, messageID, prompt string) error {
			if !strings.Contains(profileRoot, "claude") || mcpURL == "" || mcpToken == "" {
				return errors.New("expected isolated claude profileRoot and MCP config")
			}
			claudePromptSeen = prompt
			return nil
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
	if _, err := svc.SetCustomPath(ctx, domainagent.ProviderCodex, dummyCodex); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SetCustomPath(ctx, domainagent.ProviderClaude, dummyClaude); err != nil {
		t.Fatal(err)
	}

	currentMock = &mockTransport{
		authTier: "Google AI Pro",
		initRes: &InitializeResult{
			ProtocolVersion: 1,
			AgentInfo: struct {
				Name    string `json:"name"`
				Title   string `json:"title"`
				Version string `json:"version"`
			}{Name: "Antigravity", Version: "2.0.0"},
		},
	}

	// Antigravity must start unauthenticated and refuse connect until Sign in or Gemini API key is set.
	agyProbe, err := svc.Probe(ctx, domainagent.ProviderAntigravity)
	if err != nil || agyProbe.AuthState != domainagent.AuthUnauthenticated {
		t.Fatalf("expected antigravity to start unauthenticated, got info=%+v err=%v", agyProbe, err)
	}
	if _, err := svc.Connect(ctx, domainagent.ProviderAntigravity); err == nil {
		t.Fatal("expected unauthenticated antigravity Connect to fail")
	}

	// Interactive Google OAuth login (empty API key) triggers ACP authenticate and sets OAuth mode.
	agyOAuth, err := svc.Login(ctx, domainagent.ProviderAntigravity, "")
	if err != nil || agyOAuth.AuthState != domainagent.AuthAuthenticated || !strings.Contains(agyOAuth.AccountInfo, "Google AI Pro") {
		t.Fatalf("expected antigravity OAuth Sign in to succeed with tier label, got info=%+v err=%v", agyOAuth, err)
	}
	settingsPath := filepath.Join(dir, "agent-profiles", "antigravity", "home", ".gemini", "antigravity-cli", "settings.json")
	oauthSettings, err := os.ReadFile(settingsPath)
	if err != nil || strings.Contains(string(oauthSettings), `"modelProvider": "gemini"`) {
		t.Fatalf("expected OAuth mode settings.json to omit modelProvider=gemini, got %q err=%v", string(oauthSettings), err)
	}

	// Switching to isolated Gemini API key must switch back to modelProvider=gemini.
	agyLoggedIn, err := svc.Login(ctx, domainagent.ProviderAntigravity, "AIza-isolated-test-key")
	if err != nil || agyLoggedIn.AuthState != domainagent.AuthAuthenticated || agyLoggedIn.AccountInfo != "Isolated Gemini API key" {
		t.Fatalf("expected antigravity Login with API key to succeed, got info=%+v err=%v", agyLoggedIn, err)
	}

	connInfo, err := svc.Connect(ctx, domainagent.ProviderAntigravity)
	if err != nil {
		t.Fatalf("Connect agy failed: %v", err)
	}
	if !connInfo.Active || connInfo.State != domainagent.StateConnected {
		t.Fatalf("unexpected connected info: %+v", connInfo)
	}

	// Verify isolated agy mcp_config.json and settings.json (with modelProvider=gemini) were generated under ProfilesDir.
	mcpFile := filepath.Join(dir, "agent-profiles", "antigravity", "home", ".gemini", "config", "mcp_config.json")
	if _, err := os.Stat(mcpFile); err != nil {
		t.Fatalf("expected isolated agy mcp_config.json at %s: %v", mcpFile, err)
	}
	settingsRaw, err := os.ReadFile(settingsPath)
	if err != nil || !strings.Contains(string(settingsRaw), `"modelProvider": "gemini"`) {
		t.Fatalf("expected agy settings.json to enforce modelProvider=gemini, got %q err=%v", string(settingsRaw), err)
	}

	msgID, provID, err := svc.SendPrompt(ctx, "hello")
	if err != nil || msgID == "" || provID != domainagent.ProviderAntigravity {
		t.Fatalf("SendPrompt got (%q, %q, %v)", msgID, provID, err)
	}
	select {
	case evt := <-doneCh:
		if evt.Type != domainagent.ChatEventDone {
			t.Fatalf("expected ChatEventDone, got %+v", evt)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for turn completion")
	}

	// History persisted in DezuxkStudio's own SQLite repo.
	_, hist, err := svc.History(ctx)
	if err != nil || len(hist) != 2 {
		t.Fatalf("expected 2 persisted messages, got %+v, err=%v", hist, err)
	}

	// Switch to Codex isolated runner.
	codexInfo, err := svc.Connect(ctx, domainagent.ProviderCodex)
	if err != nil || !codexInfo.Active || codexInfo.AuthState != domainagent.AuthAuthenticated {
		t.Fatalf("Connect codex failed: info=%+v err=%v", codexInfo, err)
	}
	_, provID, err = svc.SendPrompt(ctx, "what can you do?")
	if err != nil || provID != domainagent.ProviderCodex {
		t.Fatalf("SendPrompt codex failed: %v", err)
	}
	select {
	case evt := <-doneCh:
		if evt.Type != domainagent.ChatEventDone {
			t.Fatalf("expected ChatEventDone for codex, got %+v", evt)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for codex turn completion")
	}
	if !strings.Contains(codexPromptSeen, "what can you do?") {
		t.Fatalf("expected codex prompt to include user prompt, got %q", codexPromptSeen)
	}

	// Switch to Claude isolated runner.
	claudeInfo, err := svc.Connect(ctx, domainagent.ProviderClaude)
	if err != nil || !claudeInfo.Active || claudeInfo.AuthState != domainagent.AuthAuthenticated {
		t.Fatalf("Connect claude failed: info=%+v err=%v", claudeInfo, err)
	}
	_, provID, err = svc.SendPrompt(ctx, "list dezuxk tools")
	if err != nil || provID != domainagent.ProviderClaude {
		t.Fatalf("SendPrompt claude failed: %v", err)
	}
	select {
	case evt := <-doneCh:
		if evt.Type != domainagent.ChatEventDone {
			t.Fatalf("expected ChatEventDone for claude, got %+v", evt)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for claude turn completion")
	}
	if !strings.Contains(claudePromptSeen, "list dezuxk tools") {
		t.Fatalf("expected claude prompt to include user prompt, got %q", claudePromptSeen)
	}

	// Logout clears Antigravity isolated Gemini API key cleanly.
	agyLoggedOut, err := svc.Logout(ctx, domainagent.ProviderAntigravity)
	if err != nil || agyLoggedOut.AuthState != domainagent.AuthUnauthenticated {
		t.Fatalf("expected antigravity Logout to clear isolated auth, got info=%+v err=%v", agyLoggedOut, err)
	}
}

func TestCodexArgsAndJSONLStream(t *testing.T) {
	args := buildCodexExecArgs("http://127.0.0.1:12345/mcp", "DezuxkStudio only", "C:\\profile\\dezuxk-instructions.md", "", 0)
	joined := strings.Join(args, " ")
	for _, flag := range []string{
		"--ignore-user-config",
		"--ignore-rules",
		`sandbox_mode="read-only"`,
		"features.shell_tool=false",
		"features.apps=false",
		"model_instructions_file=",
		"agents.enabled=false",
		"mcp_servers.dezuxk.url=",
		`mcp_servers.dezuxk.bearer_token_env_var="DEZUXK_AGENT_TOKEN"`,
	} {
		if !strings.Contains(joined, flag) {
			t.Fatalf("expected codex args to include %q, got %q", flag, joined)
		}
	}

	var streamed []domainagent.ChatEvent
	acc := &codexStreamAccumulator{
		providerID: domainagent.ProviderCodex,
		messageID:  "msg-1",
		emit: func(e domainagent.ChatEvent) {
			streamed = append(streamed, e)
		},
	}
	lines := []string{
		`{"type":"item.completed","item":{"type":"reasoning","text":"Checking capabilities"}}`,
		`{"type":"item.started","item":{"type":"mcp_tool_call","tool":"studio_list_capabilities"}}`,
		`{"type":"item.completed","item":{"type":"mcp_tool_call","tool":"studio_list_capabilities"}}`,
		`{"type":"item.completed","item":{"type":"agent_message","text":"DezuxkStudio supports watermark removal."}}`,
	}
	for _, line := range lines {
		acc.handleLine(line)
	}
	if len(streamed) != 4 {
		t.Fatalf("expected 4 streamed events, got %+v", streamed)
	}
	if streamed[0].Type != domainagent.ChatEventThought || streamed[1].Type != domainagent.ChatEventToolCall || streamed[2].Type != domainagent.ChatEventToolResult || streamed[3].Type != domainagent.ChatEventDelta {
		t.Fatalf("unexpected event sequence: %+v", streamed)
	}
	if acc.assistantText.String() != "DezuxkStudio supports watermark removal." {
		t.Fatalf("unexpected accumulated text: %q", acc.assistantText.String())
	}
}

func TestClaudeArgsMCPConfigAndStreamJSON(t *testing.T) {
	profileRoot := t.TempDir()
	cfgPath, cleanup, err := writeClaudeMCPConfig(profileRoot, "http://127.0.0.1:45678/mcp", "secret-token")
	if err != nil {
		t.Fatalf("writeClaudeMCPConfig failed: %v", err)
	}
	rawCfg, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read written claude mcp config: %v", err)
	}
	if !strings.Contains(string(rawCfg), `"type":"http"`) || !strings.Contains(string(rawCfg), `"Authorization":"Bearer secret-token"`) {
		t.Fatalf("unexpected claude mcp config JSON: %s", string(rawCfg))
	}

	args := buildClaudeExecArgs(cfgPath, "DezuxkStudio only")
	joined := strings.Join(args, " ")
	for _, flag := range []string{
		"-p",
		"--output-format stream-json",
		"--restricted",
		"--tools",
		"--disable-slash-commands",
		"--setting-sources",
		"--strict-mcp-config",
		"--mcp-config " + cfgPath,
		"--allowedTools mcp__dezuxk__*",
		"--permission-mode dontAsk",
		"--system-prompt DezuxkStudio only",
	} {
		if !strings.Contains(joined, flag) {
			t.Fatalf("expected claude args to include %q, got %q", flag, joined)
		}
	}
	cleanup()
	if _, err := os.Stat(cfgPath); !os.IsNotExist(err) {
		t.Fatalf("expected cleanup to remove ephemeral mcp config, got err=%v", err)
	}

	var streamed []domainagent.ChatEvent
	acc := &claudeStreamAccumulator{
		providerID: domainagent.ProviderClaude,
		messageID:  "msg-claude-1",
		emit: func(e domainagent.ChatEvent) {
			streamed = append(streamed, e)
		},
	}
	lines := []string{
		`{"type":"assistant","message":{"content":[{"type":"thinking","thinking":"Inspecting live DezuxkStudio tools"},{"type":"tool_use","id":"call-1","name":"mcp__dezuxk__studio_list_capabilities","input":{}}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"call-1","content":[{"type":"text","text":"{\"workflow_nodes\":1}"}]}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"DezuxkStudio can remove watermarks from video."}]}}`,
		`{"type":"result","is_error":false,"result":"DezuxkStudio can remove watermarks from video."}`,
	}
	for _, line := range lines {
		acc.handleLine(line)
	}
	if len(streamed) != 4 {
		t.Fatalf("expected 4 streamed events, got %+v", streamed)
	}
	if streamed[0].Type != domainagent.ChatEventThought ||
		streamed[1].Type != domainagent.ChatEventToolCall ||
		streamed[1].Tool != "studio_list_capabilities" ||
		streamed[2].Type != domainagent.ChatEventToolResult ||
		streamed[2].Error != "" ||
		streamed[3].Type != domainagent.ChatEventDelta {
		t.Fatalf("unexpected claude event sequence: %+v", streamed)
	}
	if acc.assistantOutput() != "DezuxkStudio can remove watermarks from video." {
		t.Fatalf("unexpected accumulated claude text: %q", acc.assistantOutput())
	}

	// Verify API authentication failure surfaces as error rather than normal assistant chat.
	errAcc := &claudeStreamAccumulator{providerID: domainagent.ProviderClaude, messageID: "msg-err"}
	errAcc.handleLine(`{"type":"assistant","error":"authentication_failed","is_api_error_message":true,"message":{"content":[{"type":"text","text":"Not logged in · Please run /login"}]}}`)
	errAcc.handleLine(`{"type":"result","is_error":true,"result":"Not logged in · Please run /login"}`)
	if errAcc.assistantOutput() != "" || !strings.Contains(errAcc.failureMessage(), "Not logged in") {
		t.Fatalf("expected api auth error capture without assistant output, text=%q err=%q", errAcc.assistantOutput(), errAcc.failureMessage())
	}
}
