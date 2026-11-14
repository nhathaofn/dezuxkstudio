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
	t            *testing.T
	in           *bufio.Scanner // client → server
	out          io.Writer      // server → client
	mu           sync.Mutex
	methods      []string
	turnStarts   []map[string]any
	onInitialize func()
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
			if f.onInitialize != nil {
				f.onInitialize()
			}
			reply(map[string]any{"userAgent": "fake"})
		case "thread/start":
			reply(map[string]any{"thread": map[string]any{"id": "thread-1"}})
		case "thread/resume":
			reply(map[string]any{"thread": map[string]any{"id": req.Params["threadId"]}})
		case "turn/start":
			turnSeq++
			turnID := "turn-" + string(rune('0'+turnSeq))
			f.mu.Lock()
			f.turnStarts = append(f.turnStarts, req.Params)
			f.mu.Unlock()
			reply(map[string]any{"turn": map[string]any{"id": turnID, "status": "inProgress"}})
			threadID, _ := req.Params["threadId"].(string)
			go f.onTurn(f, threadID, turnID)
		case "turn/interrupt":
			reply(map[string]any{})
			close(f.interrupt)
		}
	}
}

func newTestCodexService(t *testing.T) (*Service, *[]domainagent.ChatEvent, *sync.Mutex, *fakeRepo) {
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
	return svc, &events, &mu, repo
}

func assertFakeCodexArgsAndEnv(t *testing.T, args, env []string) {
	t.Helper()
	if args[0] != "app-server" || !strings.Contains(strings.Join(args, " "), `sandbox_mode="read-only"`) {
		t.Errorf("app-server must run with the read-only policy, got %v", args)
	}
	if !strings.Contains(strings.Join(env, "\n"), mcpTokenEnvVar+"=tok") {
		t.Errorf("app-server env must carry the MCP token")
	}
}

func startFakeCodexServer(t *testing.T, onInitialize func(), onTurn func(f *fakeCodexAppServer, threadID, turnID string)) (*fakeCodexAppServer, func(key string) *codexAppServer) {
	clientR, clientW := io.Pipe() // client → server
	serverR, serverW := io.Pipe() // server → client
	fake := &fakeCodexAppServer{
		t:            t,
		in:           bufio.NewScanner(clientR),
		out:          serverW,
		onInitialize: onInitialize,
		onTurn:       onTurn,
		interrupt:    make(chan struct{}),
	}
	go fake.serve()
	return fake, func(key string) *codexAppServer {
		return newCodexAppServerConn(key, clientW, serverR, nil, func() { _ = serverW.Close() })
	}
}

func newFakeCodexService(t *testing.T, onTurn func(f *fakeCodexAppServer, threadID, turnID string)) (*Service, *fakeCodexAppServer, *[]domainagent.ChatEvent, *sync.Mutex, *fakeRepo) {
	t.Helper()
	svc, events, mu, repo := newTestCodexService(t)
	fake, connect := startFakeCodexServer(t, nil, onTurn)
	svc.spawnCodexAppServer = func(key, _ string, args, env []string, _ string) (*codexAppServer, error) {
		assertFakeCodexArgsAndEnv(t, args, env)
		return connect(key), nil
	}
	return svc, fake, events, mu, repo
}

func waitForChatEvent(t *testing.T, mu *sync.Mutex, events *[]domainagent.ChatEvent, eventType domainagent.ChatEventType, text, failMsg string) {
	t.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		mu.Lock()
		for _, e := range *events {
			if e.Type == eventType && e.Text == text {
				mu.Unlock()
				return
			}
		}
		mu.Unlock()
		select {
		case <-timer.C:
			t.Fatal(failMsg)
		case <-ticker.C:
		}
	}
}

func awaitTurnError(t *testing.T, done <-chan error, failMsg string) error {
	t.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	select {
	case err := <-done:
		return err
	case <-timer.C:
		t.Fatal(failMsg)
		return nil
	}
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
	waitForChatEvent(t, mu, events, domainagent.ChatEventDelta, "Xin chào bạn ", "first delta was not streamed before the turn finished")
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
	msgs, _ := repo.ListConversationMessages(context.Background(), svc.CurrentConversationID(), 10)
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
	t.Run("during_turn", func(t *testing.T) {
		svc, fake, events, mu, repo := newFakeCodexService(t, func(f *fakeCodexAppServer, threadID, turnID string) {
			f.notify("item/agentMessage/delta", map[string]any{"threadId": threadID, "turnId": turnID, "itemId": "m1", "delta": "partial"})
			<-f.interrupt
			f.notify("turn/completed", map[string]any{"threadId": threadID, "turn": map[string]any{"id": turnID, "status": "interrupted"}})
		})
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		tr := startTestTurn(svc, "msg-1")
		done := make(chan error, 1)
		go func() {
			done <- svc.runCodexTurn(ctx, fakeCodexExe, t.TempDir(), t.TempDir(), "http://127.0.0.1:1/mcp", "tok", "msg-1", "hello")
		}()
		waitForChatEvent(t, mu, events, domainagent.ChatEventDelta, "partial", "partial delta was not streamed before cancel")
		cancel()
		err := awaitTurnError(t, done, "cancelled turn did not return")
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled turn must return context.Canceled, got %v", err)
		}
		if !strings.Contains(strings.Join(fake.seen(), ","), "turn/interrupt") {
			t.Fatal("cancel must send turn/interrupt")
		}
		svc.endTurn(tr, errTurnCancelled)
		if msgs, _ := repo.ListConversationMessages(context.Background(), svc.CurrentConversationID(), 10); len(msgs) != 1 || msgs[0].Text != "partial" {
			t.Fatalf("partial reply must be kept, got %+v", msgs)
		}
	})

	t.Run("during_initialize", func(t *testing.T) {
		svc, events, mu, repo := newTestCodexService(t)
		initStarted := make(chan struct{})
		releaseInit := make(chan struct{})
		var releaseOnce sync.Once
		release := func() { releaseOnce.Do(func() { close(releaseInit) }) }
		t.Cleanup(release)

		var spawnsMu sync.Mutex
		spawns := 0
		svc.spawnCodexAppServer = func(key, _ string, args, env []string, _ string) (*codexAppServer, error) {
			assertFakeCodexArgsAndEnv(t, args, env)
			spawnsMu.Lock()
			spawns++
			n := spawns
			spawnsMu.Unlock()
			if n == 1 {
				_, connect := startFakeCodexServer(t, func() {
					close(initStarted)
					<-releaseInit
				}, nil)
				return connect(key), nil
			}
			_, connect := startFakeCodexServer(t, nil, func(f *fakeCodexAppServer, threadID, turnID string) {
				f.notify("item/agentMessage/delta", map[string]any{"threadId": threadID, "turnId": turnID, "itemId": "m2", "delta": "ok"})
				f.notify("turn/completed", map[string]any{"threadId": threadID, "turn": map[string]any{"id": turnID, "status": "completed"}})
			})
			return connect(key), nil
		}

		profile, ws := t.TempDir(), t.TempDir()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		tr1 := startTestTurn(svc, "msg-1")
		done := make(chan error, 1)
		go func() {
			done <- svc.runCodexTurn(ctx, fakeCodexExe, profile, ws, "http://127.0.0.1:1/mcp", "tok", "msg-1", "hello")
		}()

		timer := time.NewTimer(5 * time.Second)
		defer timer.Stop()
		select {
		case <-initStarted:
		case <-timer.C:
			t.Fatal("app-server did not reach initialize")
		}
		cancel()
		err := awaitTurnError(t, done, "turn cancelled during initialize did not return")
		release()
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled initialize must return context.Canceled, got %v", err)
		}
		svc.endTurn(tr1, errTurnCancelled)

		svc.mu.Lock()
		broken := svc.codexAppServerBroken[fakeCodexExe]
		svc.mu.Unlock()
		if broken {
			t.Fatal("cancel during initialize must not mark app-server broken")
		}

		tr2 := startTestTurn(svc, "msg-2")
		if err := svc.runCodexTurn(context.Background(), fakeCodexExe, profile, ws, "http://127.0.0.1:1/mcp", "tok", "msg-2", "again"); err != nil {
			t.Fatalf("subsequent turn after cancelled initialize failed: %v", err)
		}
		svc.endTurn(tr2, nil)

		spawnsMu.Lock()
		gotSpawns := spawns
		spawnsMu.Unlock()
		if gotSpawns != 2 {
			t.Fatalf("subsequent turn must respawn app-server, got spawns = %d", gotSpawns)
		}
		waitForChatEvent(t, mu, events, domainagent.ChatEventDelta, "ok", "subsequent turn delta missing")
		if msgs, _ := repo.ListConversationMessages(context.Background(), svc.CurrentConversationID(), 10); len(msgs) != 1 || msgs[0].Text != "ok" {
			t.Fatalf("subsequent turn reply = %+v, want single 'ok' message", msgs)
		}
	})

	t.Run("during_start", func(t *testing.T) {
		svc, _, _, _ := newTestCodexService(t)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		svc.spawnCodexAppServer = func(string, string, []string, []string, string) (*codexAppServer, error) {
			cancel()
			return nil, errors.New("start failed")
		}
		err := svc.runCodexTurn(ctx, fakeCodexExe, t.TempDir(), t.TempDir(), "http://127.0.0.1:1/mcp", "tok", "msg-1", "hello")
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled start must return context.Canceled, got %v", err)
		}
		svc.mu.Lock()
		broken := svc.codexAppServerBroken[fakeCodexExe]
		svc.mu.Unlock()
		if broken {
			t.Fatal("cancel during start must not mark app-server broken")
		}
	})
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

func TestCodexAppServerTurnStartAppliesDefaultXHighEffort(t *testing.T) {
	svc, fake, _, _, _ := newFakeCodexService(t, func(f *fakeCodexAppServer, threadID, turnID string) {
		f.notify("item/completed", map[string]any{"threadId": threadID, "turnId": turnID, "item": map[string]any{"type": "agentMessage", "id": "m1", "text": "ok"}})
		f.notify("turn/completed", map[string]any{"threadId": threadID, "turn": map[string]any{"id": turnID, "status": "completed"}})
	})
	svc.models.mu.Lock()
	svc.models.catalogs[domainagent.ProviderCodex] = []domainagent.ModelOption{
		{ID: "gpt-6-luna", Efforts: []string{"low", "medium", "high", "xhigh"}, DefaultEffort: "xhigh"},
	}
	svc.models.mu.Unlock()

	profile, ws := t.TempDir(), t.TempDir()
	runOne := func(sel domainagent.ModelSelection) map[string]any {
		svc.models.mu.Lock()
		svc.models.selection[domainagent.ProviderCodex] = sel
		svc.models.mu.Unlock()
		startTestTurn(svc, "msg")
		if err := svc.runCodexTurn(context.Background(), fakeCodexExe, profile, ws, "http://127.0.0.1:1/mcp", "tok", "msg", "hi"); err != nil {
			t.Fatal(err)
		}
		fake.mu.Lock()
		defer fake.mu.Unlock()
		return fake.turnStarts[len(fake.turnStarts)-1]
	}

	// Provider default (empty model + empty effort) -> sends effort="xhigh".
	if p := runOne(domainagent.ModelSelection{}); p["effort"] != "xhigh" || p["model"] != nil {
		t.Fatalf("expected effort=xhigh without model override, got %+v", p)
	}
	// Specific model with default effort -> sends model + effort="xhigh".
	if p := runOne(domainagent.ModelSelection{Model: "gpt-6-luna"}); p["model"] != "gpt-6-luna" || p["effort"] != "xhigh" {
		t.Fatalf("expected model=gpt-6-luna and effort=xhigh, got %+v", p)
	}
	// Explicit user override -> sends chosen effort.
	if p := runOne(domainagent.ModelSelection{Model: "gpt-6-luna", Effort: "low"}); p["effort"] != "low" {
		t.Fatalf("expected explicit effort=low preserved, got %+v", p)
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
