package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"

	domainagent "app/internal/domain/agent"
	"app/internal/proc"
	"app/internal/safego"
)

// Codex app-server driver.
//
// `codex exec --json` only reports an assistant message once it is complete. `codex app-server` speaks a
// JSON-RPC protocol (the one the Codex IDE extensions use) that streams `item/agentMessage/delta` and
// reasoning deltas, so DezuxkStudio can show the reply token by token. One app-server process is kept per
// connection and reused across turns; `codex exec` remains the fallback when the CLI has no usable
// app-server (older versions, protocol changes). The app-server protocol is marked experimental by Codex.

const (
	codexAppServerMaxLine = 16 << 20 // largest JSON-RPC line accepted from the app-server
)

// errCodexAppServerUnavailable means the app-server could not be started or initialized; the caller
// falls back to `codex exec`.
var errCodexAppServerUnavailable = errors.New("codex app-server unavailable")

type codexRPCResult struct {
	result json.RawMessage
	err    error
}

// codexAppServer is one running `codex app-server` process speaking JSON-RPC over stdio.
type codexAppServer struct {
	key    string // executable|profile|mcp endpoint the process was started for
	stdin  io.WriteCloser
	stop   func() // kills the process (nil for in-memory test connections)
	nextID atomic.Int64

	writeMu sync.Mutex
	mu      sync.Mutex
	pending map[int64]chan codexRPCResult
	onEvent func(method string, params json.RawMessage) // the active turn's notification handler
	threads map[string]bool                             // threads loaded in this process
	done    chan struct{}
	exitErr error
	stderr  strings.Builder
}

func newCodexAppServerConn(key string, stdin io.WriteCloser, stdout, stderr io.Reader, stop func()) *codexAppServer {
	c := &codexAppServer{
		key:     key,
		stdin:   stdin,
		stop:    stop,
		pending: make(map[int64]chan codexRPCResult),
		threads: make(map[string]bool),
		done:    make(chan struct{}),
	}
	if stderr != nil {
		safego.Go("agent.codex.appserver.stderr", func() {
			sc := bufio.NewScanner(stderr)
			sc.Buffer(make([]byte, 4096), 256*1024)
			for sc.Scan() {
				c.mu.Lock()
				if c.stderr.Len() < 8192 {
					c.stderr.WriteString(redactSecrets(sc.Text()))
					c.stderr.WriteByte('\n')
				}
				c.mu.Unlock()
			}
		})
	}
	safego.Go("agent.codex.appserver.stdout", func() { c.readLoop(stdout) })
	return c
}

// startCodexAppServer launches `codex app-server` with DezuxkStudio's isolated profile and policy.
func startCodexAppServer(key, executable string, args, env []string, workDir string) (*codexAppServer, error) {
	cmd := proc.Command(context.Background(), executable, args...)
	cmd.Dir = workDir
	cmd.Env = env
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	c := newCodexAppServerConn(key, stdin, stdout, stderr, func() { proc.KillTree(cmd) })
	safego.Go("agent.codex.appserver.wait", func() { c.markExited(waitErr(cmd)) })
	return c, nil
}

func waitErr(cmd *exec.Cmd) error {
	if err := cmd.Wait(); err != nil {
		return err
	}
	return errors.New("codex app-server exited")
}

func (c *codexAppServer) readLoop(stdout io.Reader) {
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 64*1024), codexAppServerMaxLine)
	for sc.Scan() {
		c.handleLine(sc.Bytes())
	}
	err := sc.Err()
	if err == nil {
		err = io.EOF
	}
	c.markExited(fmt.Errorf("codex app-server output closed: %w", err))
}

func (c *codexAppServer) markExited(err error) {
	c.mu.Lock()
	select {
	case <-c.done:
		c.mu.Unlock()
		return
	default:
	}
	if tail := strings.TrimSpace(c.stderr.String()); tail != "" {
		err = fmt.Errorf("%w: %s", err, tail)
	}
	c.exitErr = err
	pending := c.pending
	c.pending = make(map[int64]chan codexRPCResult)
	close(c.done)
	c.mu.Unlock()
	for _, ch := range pending {
		ch <- codexRPCResult{err: err}
	}
}

func (c *codexAppServer) alive() bool {
	select {
	case <-c.done:
		return false
	default:
		return true
	}
}

func (c *codexAppServer) close() {
	if c.stop != nil {
		c.stop()
	}
	_ = c.stdin.Close()
	c.markExited(errors.New("codex app-server closed"))
}

func (c *codexAppServer) handleLine(line []byte) {
	var msg struct {
		ID     *json.RawMessage `json:"id"`
		Method string           `json:"method"`
		Params json.RawMessage  `json:"params"`
		Result json.RawMessage  `json:"result"`
		Error  *rpcError        `json:"error"`
	}
	if json.Unmarshal(line, &msg) != nil {
		return
	}
	switch {
	case msg.ID != nil && msg.Method != "":
		// A server→client request (approval, elicitation, user input). DezuxkStudio runs Codex with
		// approvals disabled and its own approval queue, so every such request is refused.
		_ = c.write(map[string]any{
			"id":    msg.ID,
			"error": map[string]any{"code": -32601, "message": "not supported by DezuxkStudio"},
		})
	case msg.ID != nil:
		var id int64
		if json.Unmarshal(*msg.ID, &id) != nil {
			return
		}
		c.mu.Lock()
		ch := c.pending[id]
		delete(c.pending, id)
		c.mu.Unlock()
		if ch == nil {
			return
		}
		if msg.Error != nil {
			ch <- codexRPCResult{err: msg.Error}
		} else {
			ch <- codexRPCResult{result: msg.Result}
		}
	case msg.Method != "":
		c.mu.Lock()
		handler := c.onEvent
		c.mu.Unlock()
		if handler != nil {
			handler(msg.Method, msg.Params)
		}
	}
}

func (c *codexAppServer) write(v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_, err = c.stdin.Write(append(raw, '\n'))
	return err
}

func (c *codexAppServer) call(ctx context.Context, method string, params any, out any) error {
	id := c.nextID.Add(1)
	ch := make(chan codexRPCResult, 1)
	c.mu.Lock()
	if !c.alive() {
		err := c.exitErr
		c.mu.Unlock()
		return err
	}
	c.pending[id] = ch
	c.mu.Unlock()
	if err := c.write(map[string]any{"id": id, "method": method, "params": params}); err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return fmt.Errorf("codex %s: %w", method, err)
	}
	select {
	case res := <-ch:
		if res.err != nil {
			return fmt.Errorf("codex %s failed: %w", method, res.err)
		}
		if out != nil && len(res.result) > 0 {
			if err := json.Unmarshal(res.result, out); err != nil {
				return fmt.Errorf("codex %s: decode result: %w", method, err)
			}
		}
		return nil
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return ctx.Err()
	}
}

func (c *codexAppServer) initialize(ctx context.Context, version string) error {
	if err := c.call(ctx, "initialize", map[string]any{
		"clientInfo": map[string]any{"name": clientName, "version": version},
	}, nil); err != nil {
		return err
	}
	return c.write(map[string]any{"method": "initialized"})
}

func (c *codexAppServer) setHandler(h func(method string, params json.RawMessage)) {
	c.mu.Lock()
	c.onEvent = h
	c.mu.Unlock()
}

// ensureCodexAppServer returns a running, initialized app-server for this executable/profile/MCP endpoint,
// restarting it when any of them changed or the process died.
func (s *Service) ensureCodexAppServer(ctx context.Context, executable, profileRoot, workspaceDir, mcpURL, mcpToken string) (*codexAppServer, error) {
	// The base instructions are fixed when the process starts, so a changed prompt (interface
	// language, installed features) starts a new process.
	instructions := s.systemPrompt(ctx, domainagent.ProviderCodex)
	key := strings.Join([]string{executable, profileRoot, mcpURL, mcpToken, instructions}, "|")
	s.mu.Lock()
	srv := s.codexServer
	if srv != nil && srv.key == key && srv.alive() {
		s.mu.Unlock()
		return srv, nil
	}
	s.codexServer = nil
	s.mu.Unlock()
	if srv != nil {
		srv.close()
	}

	envMap, err := prepareCodexProfile(profileRoot)
	if err != nil {
		return nil, err
	}
	envMap[mcpTokenEnvVar] = mcpToken
	baseFile, err := writeCodexBaseInstructions(profileRoot, instructions)
	if err != nil {
		return nil, err
	}
	args := append([]string{"app-server"}, codexPolicyArgs(mcpURL, baseFile, s.limits.MCPToolTimeout)...)
	srv, err = s.spawnCodexAppServer(key, executable, args, providerEnv(domainagent.ProviderCodex, envMap), workspaceDir)
	if err != nil {
		return nil, fmt.Errorf("%w: start: %v", errCodexAppServerUnavailable, err)
	}
	initCtx, cancel := context.WithTimeout(ctx, s.limits.InitializeTimeout)
	defer cancel()
	if err := srv.initialize(initCtx, s.appVersion); err != nil {
		srv.close()
		return nil, fmt.Errorf("%w: initialize: %v", errCodexAppServerUnavailable, err)
	}

	s.mu.Lock()
	if s.closing {
		s.mu.Unlock()
		srv.close()
		return nil, errors.New("agent service closing")
	}
	s.codexServer = srv
	s.mu.Unlock()
	return srv, nil
}

// closeCodexAppServer stops the running app-server (sign-out, path change, disconnect, shutdown) so the
// next turn starts a fresh process with the current credentials.
func (s *Service) closeCodexAppServer() {
	s.mu.Lock()
	srv := s.codexServer
	s.codexServer = nil
	s.mu.Unlock()
	if srv != nil {
		srv.close()
	}
}

// runCodexTurn streams one turn through `codex app-server`, falling back to `codex exec` when the
// app-server cannot be used with this CLI.
func (s *Service) runCodexTurn(ctx context.Context, executable, profileRoot, workspaceDir, mcpURL, mcpToken, messageID, prompt string) error {
	s.mu.Lock()
	unsupported := s.codexAppServerBroken[executable]
	s.mu.Unlock()
	if !unsupported {
		srv, err := s.ensureCodexAppServer(ctx, executable, profileRoot, workspaceDir, mcpURL, mcpToken)
		if err == nil {
			return s.runCodexAppServerTurn(ctx, srv, workspaceDir, messageID, prompt)
		}
		if !errors.Is(err, errCodexAppServerUnavailable) {
			return err
		}
		// Remember per executable, so an old CLI does not pay the failed start on every turn.
		s.log.Warn("agent: codex app-server unavailable, falling back to codex exec", "error", err)
		s.mu.Lock()
		s.codexAppServerBroken[executable] = true
		s.mu.Unlock()
	}
	return s.runCodexExecTurn(ctx, executable, profileRoot, workspaceDir, mcpURL, mcpToken, messageID, prompt)
}

// codexTurnState accumulates one streamed turn.
type codexTurnState struct {
	s         *Service
	messageID string
	threadID  string

	mu          sync.Mutex
	turnID      string
	text        strings.Builder
	thought     strings.Builder
	lastMsgItem string          // agentMessage item that produced the latest text
	streamed    map[string]bool // items that delivered deltas (their item/completed text is not re-emitted)
	summaryOf   map[string]bool // reasoning items that stream summaries (raw reasoning text is then skipped)
	failure     string
	status      string
	completed   chan struct{}
	once        sync.Once
}

func (t *codexTurnState) emit(evt domainagent.ChatEvent) {
	evt.ProviderID = domainagent.ProviderCodex
	evt.MessageID = t.messageID
	t.s.emitChat(evt)
}

func (t *codexTurnState) appendText(itemID, delta string) {
	if delta == "" {
		return
	}
	t.mu.Lock()
	prefix := ""
	if t.lastMsgItem != itemID && t.text.Len() > 0 {
		prefix = "\n\n" // separate consecutive assistant messages of one turn
	}
	t.lastMsgItem = itemID
	t.text.WriteString(prefix)
	t.text.WriteString(delta)
	t.mu.Unlock()
	t.emit(domainagent.ChatEvent{Type: domainagent.ChatEventDelta, Text: prefix + delta})
}

func (t *codexTurnState) appendThought(delta string) {
	if delta == "" {
		return
	}
	t.mu.Lock()
	t.thought.WriteString(delta)
	t.mu.Unlock()
	t.emit(domainagent.ChatEvent{Type: domainagent.ChatEventThought, Text: delta})
}

func (t *codexTurnState) finish(status, failure string) {
	t.once.Do(func() {
		t.mu.Lock()
		t.status = status
		if failure != "" {
			t.failure = failure
		}
		t.mu.Unlock()
		close(t.completed)
	})
}

type codexDeltaParams struct {
	ThreadID string `json:"threadId"`
	TurnID   string `json:"turnId"`
	ItemID   string `json:"itemId"`
	Delta    string `json:"delta"`
}

type codexTurnError struct {
	Message           string `json:"message"`
	AdditionalDetails string `json:"additionalDetails"`
}

func (e *codexTurnError) text() string {
	if e == nil {
		return ""
	}
	msg := strings.TrimSpace(e.Message)
	if d := strings.TrimSpace(e.AdditionalDetails); d != "" && !strings.Contains(msg, d) {
		msg = strings.TrimSpace(msg + ": " + d)
	}
	return redactSecrets(msg)
}

type codexItem struct {
	Type   string          `json:"type"`
	ID     string          `json:"id"`
	Text   string          `json:"text"`
	Tool   string          `json:"tool"`
	Server string          `json:"server"`
	Status string          `json:"status"`
	Error  json.RawMessage `json:"error"`
}

func (t *codexTurnState) handle(method string, raw json.RawMessage) {
	var scope struct {
		ThreadID string `json:"threadId"`
		TurnID   string `json:"turnId"`
	}
	_ = json.Unmarshal(raw, &scope)
	if scope.ThreadID != "" && scope.ThreadID != t.threadID {
		return // another thread's event
	}
	t.mu.Lock()
	ownTurn := t.turnID
	t.mu.Unlock()
	if scope.TurnID != "" && ownTurn != "" && scope.TurnID != ownTurn {
		return // a previous turn's late event
	}

	switch method {
	case "item/agentMessage/delta":
		var p codexDeltaParams
		if json.Unmarshal(raw, &p) == nil {
			t.mu.Lock()
			t.streamed[p.ItemID] = true
			t.mu.Unlock()
			t.appendText(p.ItemID, p.Delta)
		}
	case "item/reasoning/summaryTextDelta":
		var p codexDeltaParams
		if json.Unmarshal(raw, &p) == nil {
			t.mu.Lock()
			t.summaryOf[p.ItemID] = true
			t.streamed[p.ItemID] = true
			t.mu.Unlock()
			t.appendThought(p.Delta)
		}
	case "item/reasoning/textDelta":
		var p codexDeltaParams
		if json.Unmarshal(raw, &p) == nil {
			t.mu.Lock()
			skip := t.summaryOf[p.ItemID]
			t.streamed[p.ItemID] = true
			t.mu.Unlock()
			if !skip {
				t.appendThought(p.Delta)
			}
		}
	case "item/started", "item/completed":
		var p struct {
			Item codexItem `json:"item"`
		}
		if json.Unmarshal(raw, &p) != nil {
			return
		}
		t.handleItem(method == "item/completed", p.Item)
	case "error":
		var p struct {
			Error     *codexTurnError `json:"error"`
			WillRetry bool            `json:"willRetry"`
		}
		// Retried errors ("Reconnecting... 2/5") are transient; only a final error fails the turn.
		if json.Unmarshal(raw, &p) == nil && !p.WillRetry {
			t.mu.Lock()
			t.failure = p.Error.text()
			t.mu.Unlock()
		}
	case "turn/completed":
		var p struct {
			Turn struct {
				ID     string          `json:"id"`
				Status string          `json:"status"`
				Error  *codexTurnError `json:"error"`
			} `json:"turn"`
		}
		if json.Unmarshal(raw, &p) == nil {
			if ownTurn != "" && p.Turn.ID != "" && p.Turn.ID != ownTurn {
				return
			}
			t.finish(p.Turn.Status, p.Turn.Error.text())
		}
	}
}

func (t *codexTurnState) handleItem(completed bool, item codexItem) {
	switch item.Type {
	case "agentMessage":
		t.mu.Lock()
		streamed := t.streamed[item.ID]
		t.mu.Unlock()
		if completed && !streamed && strings.TrimSpace(item.Text) != "" {
			t.appendText(item.ID, item.Text) // the CLI sent no deltas for this message
		}
	case "mcpToolCall":
		if item.Tool == "" {
			return
		}
		if !completed {
			t.emit(domainagent.ChatEvent{Type: domainagent.ChatEventToolCall, Tool: item.Tool, Text: item.Tool})
			return
		}
		evt := domainagent.ChatEvent{Type: domainagent.ChatEventToolResult, Tool: item.Tool, Text: toolStatusCompleted}
		if item.Status == toolStatusFailed || (len(item.Error) > 0 && string(item.Error) != "null") {
			evt.Text = toolStatusFailed
			evt.Error = toolStatusFailed
		}
		t.emit(evt)
	}
}

func (s *Service) runCodexAppServerTurn(ctx context.Context, srv *codexAppServer, workspaceDir, messageID, prompt string) error {
	threadID, err := s.codexThread(ctx, srv, workspaceDir)
	if err != nil {
		return classifyProviderError(err)
	}
	st := &codexTurnState{
		s:         s,
		messageID: messageID,
		threadID:  threadID,
		streamed:  make(map[string]bool),
		summaryOf: make(map[string]bool),
		completed: make(chan struct{}),
	}
	srv.setHandler(st.handle)
	defer srv.setHandler(nil)

	params := map[string]any{
		"threadId": threadID,
		"input":    []map[string]any{{"type": "text", "text": prompt}},
	}
	if sel := s.effectiveSelection(domainagent.ProviderCodex); sel.Model != "" || sel.Effort != "" {
		if sel.Model != "" {
			params["model"] = sel.Model
		}
		if sel.Effort != "" {
			params["effort"] = sel.Effort
		}
	}
	var started struct {
		Turn struct {
			ID string `json:"id"`
		} `json:"turn"`
	}
	if err := srv.call(ctx, "turn/start", params, &started); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return classifyProviderError(err)
	}
	st.mu.Lock()
	st.turnID = started.Turn.ID
	st.mu.Unlock()

	select {
	case <-st.completed:
	case <-srv.done:
		srv.mu.Lock()
		exitErr := srv.exitErr
		srv.mu.Unlock()
		st.finish("failed", exitErr.Error())
	case <-ctx.Done():
		// Ask Codex to stop the turn and give it a moment to report what it already produced.
		stopCtx, cancel := context.WithTimeout(context.Background(), s.limits.InterruptWait)
		_ = srv.call(stopCtx, "turn/interrupt", map[string]any{"threadId": threadID, "turnId": started.Turn.ID}, nil)
		select {
		case <-st.completed:
		case <-stopCtx.Done():
		}
		cancel()
	}

	st.mu.Lock()
	text, thought, failure, status := st.text.String(), st.thought.String(), st.failure, st.status
	st.mu.Unlock()
	if text != "" || thought != "" {
		s.persistAssistantMessage(domainagent.ProviderCodex, messageID, text, thought)
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if status == "failed" || failure != "" {
		if failure == "" {
			failure = "codex turn failed"
		}
		return classifyProviderError(errors.New(failure))
	}
	if text == "" {
		return errors.New("codex completed without returning a message; check isolated Codex login")
	}
	s.setCLISession(domainagent.ProviderCodex, threadID)
	return nil
}

// codexThread returns the conversation thread for this turn: the one already loaded in the process, the
// saved thread resumed from the isolated CODEX_HOME, or a new one.
func (s *Service) codexThread(ctx context.Context, srv *codexAppServer, workspaceDir string) (string, error) {
	// The profile is already the base instructions (model_instructions_file); it is not repeated as
	// developer instructions, which would cost ~2k extra tokens on every model call.
	settings := map[string]any{
		"cwd":            workspaceDir,
		"sandbox":        "read-only",
		"approvalPolicy": "never",
	}
	if saved := s.cliSessionFor(domainagent.ProviderCodex); saved != "" {
		srv.mu.Lock()
		loaded := srv.threads[saved]
		srv.mu.Unlock()
		if loaded {
			return saved, nil
		}
		resume := map[string]any{"threadId": saved, "excludeTurns": true}
		for k, v := range settings {
			resume[k] = v
		}
		if err := srv.call(ctx, "thread/resume", resume, nil); err != nil {
			// Matches isMissingCLISessionError, so the service retries with the replayed history.
			return "", fmt.Errorf("thread/resume failed: %w", err)
		}
		srv.mu.Lock()
		srv.threads[saved] = true
		srv.mu.Unlock()
		return saved, nil
	}
	var res struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	if err := srv.call(ctx, "thread/start", settings, &res); err != nil {
		return "", err
	}
	if res.Thread.ID == "" {
		return "", errors.New("codex thread/start returned no thread id")
	}
	srv.mu.Lock()
	srv.threads[res.Thread.ID] = true
	srv.mu.Unlock()
	return res.Thread.ID, nil
}
