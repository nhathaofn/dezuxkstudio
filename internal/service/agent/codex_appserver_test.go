package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	domainagent "app/internal/domain/agent"
)

// fakeCodexExe never exists, so a test can never fall through to a real `codex exec`.
const fakeCodexExe = "dezuxk-test-no-such-codex"

// fakeCodexAppServer emulates `codex app-server` over in-memory pipes.
type fakeCodexAppServer struct {
	t       *testing.T
	in      *bufio.Scanner // client → server
	out     io.Writer      // server → client
	mu      sync.Mutex
	methods []string
	// onTurn streams the notifications of one turn; it may block (e.g. until interrupted).
	onTurn    func(f *fakeCodexAppServer, threadID, turnID string)
	interrupt chan struct{}
}

func (f *fakeCodexAppServer) send(v map[string]any) {
	raw, _ := json.Marshal(v)
	_, _ = f.out.Write(append(raw, '\n'))
}

func (f *fakeCodexAppServer) notify(method string, params map[string]any) {
	f.send(map[string]any{"method": method, "params": params})
}

func (f *fakeCodexAppServer) seen() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.methods...)
}

func (f *fakeCodexAppServer) serve() {
	turnSeq := 0
	for f.in.Scan() {
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params map[string]any  `json:"params"`
		}
		if json.Unmarshal(f.in.Bytes(), &req) != nil {
			continue
		}
		f.mu.Lock()
		f.methods = append(f.methods, req.Method)
		f.mu.Unlock()
		reply := func(result any) { f.send(map[string]any{"id": req.ID, "result": result}) }
		switch req.Method {
		case "initialize":
			reply(map[string]any{"userAgent": "fake"})
		case "thread/start":
			reply(map[string]any{"thread": map[string]any{"id": "thread-1"}})
		case "thread/resume":
			reply(map[string]any{"thread": map[string]any{"id": req.Params["threadId"]}})
		case "turn/start":
			turnSeq++
			turnID := "turn-" + string(rune('0'+turnSeq))
			reply(map[string]any{"turn": map[string]any{"id": turnID, "status": "inProgress"}})
			threadID, _ := req.Params["threadId"].(string)
			go f.onTurn(f, threadID, turnID)
		case "turn/interrupt":
			reply(map[string]any{})
			close(f.interrupt)
		}
	}
}

func newFakeCodexService(t *testing.T, onTurn func(f *fakeCodexAppServer, threadID, turnID string)) (*Service, *fakeCodexAppServer, *[]domainagent.ChatEvent, *sync.Mutex, *fakeRepo) {
	t.Helper()
	dir := t.TempDir()
	repo := newFakeRepo()
	var mu sync.Mutex
	var events []domainagent.ChatEvent
	svc, err := New(Options{
		Repo:        repo,
		DataDir:     dir,
		ProfilesDir: filepath.Join(dir, "profiles"),
		Logger:      slog.New(slog.DiscardHandler),
		OnChatEvent: func(evt domainagent.ChatEvent) {
			mu.Lock()
			events = append(events, evt)
			mu.Unlock()
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Close() })

	clientR, clientW := io.Pipe() // client → server
	serverR, serverW := io.Pipe() // server → client
	fake := &fakeCodexAppServer{t: t, in: bufio.NewScanner(clientR), out: serverW, onTurn: onTurn, interrupt: make(chan struct{})}
	go fake.serve()
	svc.spawnCodexAppServer = func(key, _ string, args, env []string, _ string) (*codexAppServer, error) {
		if args[0] != "app-server" || !strings.Contains(strings.Join(args, " "), `sandbox_mode="read-only"`) {
			t.Errorf("app-server must run with the read-only policy, got %v", args)
		}
		if !strings.Contains(strings.Join(env, "\n"), mcpTokenEnvVar+"=tok") {
			t.Errorf("app-server env must carry the MCP token")
		}
		return newCodexAppServerConn(key, clientW, serverR, nil, func() { _ = serverW.Close() }), nil
	}
	return svc, fake, &events, &mu, repo
}

func TestCodexAppServerStreamsDeltasAndReusesThread(t *testing.T) {
	release := make(chan struct{})
	svc, fake, events, mu, repo := newFakeCodexService(t, func(f *fakeCodexAppServer, threadID, turnID string) {
		scope := func(m map[string]any) map[string]any { m["threadId"] = threadID; m["turnId"] = turnID; return m }
		f.notify("error", scope(map[string]any{"error": map[string]any{"message": "Reconnecting... 1/5"}, "willRetry": true}))
		f.notify("item/reasoning/summaryTextDelta", scope(map[string]any{"itemId": "r1", "delta": "Thinking"}))
		f.notify("item/started", scope(map[string]any{"item": map[string]any{"type": "mcpToolCall", "id": "c1", "tool": "studio_list_capabilities"}}))
		f.notify("item/completed", scope(map[string]any{"item": map[string]any{"type": "mcpToolCall", "id": "c1", "tool": "studio_list_capabilities", "status": "completed"}}))
		f.notify("item/agentMessage/delta", scope(map[string]any{"itemId": "m1", "delta": "Xin chào bạn "}))
		<-release // the first delta must reach the UI before the turn completes
		f.notify("item/agentMessage/delta", scope(map[string]any{"itemId": "m1", "delta": "nhé"}))
		f.notify("item/completed", scope(map[string]any{"item": map[string]any{"type": "agentMessage", "id": "m1", "text": "Xin chào bạn nhé"}}))
		f.notify("turn/completed", map[string]any{"threadId": threadID, "turn": map[string]any{"id": turnID, "status": "completed"}})
	})

	profile, ws := t.TempDir(), t.TempDir()
	tr := startTestTurn(svc, "msg-1")
	done := make(chan error, 1)
	go func() {
		done <- svc.runCodexTurn(context.Background(), fakeCodexExe, profile, ws, "http://127.0.0.1:1/mcp", "tok", "msg-1", "hello")
	}()
	deadline := time.After(5 * time.Second)
	for {
		mu.Lock()
		got := len(*events) > 0 && (*events)[len(*events)-1].Text == "Xin chào bạn "
		mu.Unlock()
		if got {
			break
		}
		select {
		case <-deadline:
			t.Fatal("first delta was not streamed before the turn finished")
		case <-time.After(10 * time.Millisecond):
		}
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("turn failed: %v", err)
	}
	svc.endTurn(tr, nil)

	mu.Lock()
	var kinds []string
	for _, e := range *events {
		kinds = append(kinds, string(e.Type)+":"+e.Text)
	}
	mu.Unlock()
	// Unfenced non-code reasoning chunks stream immediately without waiting for turn completion.
	want := []string{"thought:Thinking", "tool_call:studio_list_capabilities", "tool_result:completed", "delta:Xin chào bạn ", "delta:nhé", "done:"}
	if strings.Join(kinds, "|") != strings.Join(want, "|") {
		t.Fatalf("events = %v, want %v (retried errors and the completed message must not be re-emitted)", kinds, want)
	}
	msgs, _ := repo.ListChatMessages(context.Background(), 10)
	if len(msgs) != 1 || msgs[0].Text != "Xin chào bạn nhé" || msgs[0].Thought != "Thinking" {
		t.Fatalf("persisted = %+v", msgs)
	}
	if got := svc.cliSessionFor(domainagent.ProviderCodex); got != "thread-1" {
		t.Fatalf("thread id not saved for resume, got %q", got)
	}

	// A second turn reuses the loaded thread and the same process.
	startTestTurn(svc, "msg-2")
	if err := svc.runCodexTurn(context.Background(), fakeCodexExe, profile, ws, "http://127.0.0.1:1/mcp", "tok", "msg-2", "again"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(fake.seen(), ","); got != "initialize,initialized,thread/start,turn/start,turn/start" {
		t.Fatalf("protocol calls = %s", got)
	}
}

func TestCodexAppServerCancelInterruptsTurn(t *testing.T) {
	svc, fake, _, _, repo := newFakeCodexService(t, func(f *fakeCodexAppServer, threadID, turnID string) {
		f.notify("item/agentMessage/delta", map[string]any{"threadId": threadID, "turnId": turnID, "itemId": "m1", "delta": "partial"})
		<-f.interrupt
		f.notify("turn/completed", map[string]any{"threadId": threadID, "turn": map[string]any{"id": turnID, "status": "interrupted"}})
	})
	ctx, cancel := context.WithCancel(context.Background())
	tr := startTestTurn(svc, "msg-1")
	done := make(chan error, 1)
	go func() {
		done <- svc.runCodexTurn(ctx, fakeCodexExe, t.TempDir(), t.TempDir(), "http://127.0.0.1:1/mcp", "tok", "msg-1", "hello")
	}()
	time.Sleep(100 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled turn must return context.Canceled, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled turn did not return")
	}
	if !strings.Contains(strings.Join(fake.seen(), ","), "turn/interrupt") {
		t.Fatal("cancel must send turn/interrupt")
	}
	svc.endTurn(tr, errTurnCancelled)
	if msgs, _ := repo.ListChatMessages(context.Background(), 10); len(msgs) != 1 || msgs[0].Text != "partial" {
		t.Fatalf("partial reply must be kept, got %+v", msgs)
	}
}

func TestCodexAppServerFinalErrorIsClassified(t *testing.T) {
	svc, _, _, _, _ := newFakeCodexService(t, func(f *fakeCodexAppServer, threadID, turnID string) {
		f.notify("error", map[string]any{"threadId": threadID, "turnId": turnID, "willRetry": false,
			"error": map[string]any{"message": "stream failed", "additionalDetails": "unexpected status 401 Unauthorized"}})
		f.notify("turn/completed", map[string]any{"threadId": threadID, "turn": map[string]any{"id": turnID, "status": "failed"}})
	})
	err := svc.runCodexTurn(context.Background(), fakeCodexExe, t.TempDir(), t.TempDir(), "http://127.0.0.1:1/mcp", "tok", "msg-1", "hello")
	if !isAuthError(err) || !strings.Contains(err.Error(), "401") {
		t.Fatalf("401 from Codex must be an auth error with details, got %v", err)
	}
}

func TestCodexAppServerUnavailableFallsBackToExec(t *testing.T) {
	dir := t.TempDir()
	svc, err := New(Options{Repo: newFakeRepo(), DataDir: dir, ProfilesDir: filepath.Join(dir, "p"), Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
	spawns := 0
	svc.spawnCodexAppServer = func(string, string, []string, []string, string) (*codexAppServer, error) {
		spawns++
		return nil, errors.New("unexpected argument 'app-server'")
	}
	missing := filepath.Join(dir, "no-such-codex.exe")
	for range 2 {
		// The exec fallback fails too (the executable does not exist); what matters is that the
		// app-server is not retried on every turn.
		_ = svc.runCodexTurn(context.Background(), missing, filepath.Join(dir, "p", "codex"), dir, "http://127.0.0.1:1/mcp", "tok", "m", "hi")
	}
	if spawns != 1 {
		t.Fatalf("a broken app-server must be tried once per executable, spawned %d times", spawns)
	}
}

// startTestTurn makes id the running turn, as SendPrompt does, so a runner called directly streams into it.
func startTestTurn(svc *Service, id string) *turn {
	tr := newTurn(id, "", domainagent.ProviderCodex, svc.connSeq.Load(), nil, func() {})
	svc.mu.Lock()
	svc.turn = tr
	svc.mu.Unlock()
	return tr
}
