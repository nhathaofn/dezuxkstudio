package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"

	domainagent "app/internal/domain/agent"
	"app/internal/safego"
)

var (
	// Real bearer tokens are long; requiring 8+ characters keeps prose like "bearer or basic" readable.
	reAuthBearer = regexp.MustCompile(`(?i)(bearer\s+)[a-z0-9._~+/=-]{8,}`)
	reSKKey      = regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{8,}\b`)
	reTokenParam = regexp.MustCompile(`(?i)((?:api[_ -]?key|token)\s*[:=]\s*)(?:"[^"]*"|'[^']*'|[^\s,;]+)`)
)

// redactSecrets strips obvious credentials from subprocess stderr before logging.
func redactSecrets(text string) string {
	text = reAuthBearer.ReplaceAllString(text, "$1[REDACTED]")
	text = reSKKey.ReplaceAllString(text, "[REDACTED]")
	text = reTokenParam.ReplaceAllString(text, "$1[REDACTED]")
	return text
}

// InitializeResult holds the subset of ACP initialize metadata used by DezuxkStudio.
type InitializeResult struct {
	ProtocolVersion int `json:"protocolVersion"`
	AgentInfo       struct {
		Name    string `json:"name"`
		Title   string `json:"title"`
		Version string `json:"version"`
	} `json:"agentInfo"`
	AuthMethods []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		Type string `json:"type"`
	} `json:"authMethods"`
}

// SessionUpdate describes a streamed ACP session/update notification payload.
type SessionUpdate struct {
	SessionID string
	Kind      string
	MessageID string
	Text      string
	Tool      string
	Status    string
}

type rpcEnvelope struct {
	JSONRPC string          `json:"jsonrpc,omitempty"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string {
	return fmt.Sprintf("acp error (%d): %s", e.Code, e.Message)
}

type rpcOutcome struct {
	result json.RawMessage
	err    error
}

// ACPClient implements a newline-delimited JSON-RPC 2.0 ACP client over stdio.
type ACPClient struct {
	modelsMu     sync.Mutex
	models       []domainagent.ModelOption
	currentModel string

	stdin   io.WriteCloser
	stdout  io.ReadCloser
	stderr  io.ReadCloser
	cmd     *exec.Cmd
	log     *slog.Logger
	onEvent func(SessionUpdate)
	onExit  func(error)

	nextID   atomic.Int64
	writeMu  sync.Mutex
	pendMu   sync.Mutex
	pending  map[string]chan rpcOutcome
	closeMu  sync.Mutex
	closed   bool
	doneCh   chan struct{}
	exitOnce sync.Once
}

// SpawnACP starts an ACP agent executable inside an isolated working directory and environment.
func SpawnACP(ctx context.Context, executable string, args []string, workDir string, env []string, log *slog.Logger, onEvent func(SessionUpdate), onExit func(error)) (*ACPClient, error) {
	cmd := buildCommand(ctx, executable, args...)
	if workDir != "" {
		cmd.Dir = workDir
	}
	if len(env) > 0 {
		cmd.Env = env
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("acp: stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("acp: stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("acp: stderr pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("acp: start %s: %w", executable, err)
	}

	c := newACPClient(stdin, stdout, stderr, cmd, log, onEvent, onExit)
	c.start()
	return c, nil
}

// NewACPClient creates an ACPClient over caller-supplied streams (primarily for tests).
func NewACPClient(stdin io.WriteCloser, stdout io.ReadCloser, stderr io.ReadCloser, log *slog.Logger, onEvent func(SessionUpdate), onExit func(error)) *ACPClient {
	c := newACPClient(stdin, stdout, stderr, nil, log, onEvent, onExit)
	c.start()
	return c
}

func newACPClient(stdin io.WriteCloser, stdout io.ReadCloser, stderr io.ReadCloser, cmd *exec.Cmd, log *slog.Logger, onEvent func(SessionUpdate), onExit func(error)) *ACPClient {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &ACPClient{
		stdin:   stdin,
		stdout:  stdout,
		stderr:  stderr,
		cmd:     cmd,
		log:     log,
		onEvent: onEvent,
		onExit:  onExit,
		pending: make(map[string]chan rpcOutcome),
		doneCh:  make(chan struct{}),
	}
}

func (c *ACPClient) start() {
	var wg sync.WaitGroup
	wg.Add(1)
	safego.Go("acp.stdout", func() {
		defer wg.Done()
		c.readStdout()
	})

	if c.stderr != nil {
		wg.Add(1)
		safego.Go("acp.stderr", func() {
			defer wg.Done()
			c.readStderr()
		})
	}

	safego.Go("acp.wait", func() {
		wg.Wait()
		var waitErr error
		if c.cmd != nil {
			waitErr = c.cmd.Wait()
		}
		c.failAllPending(errors.New("acp connection closed"))
		close(c.doneCh)
		if c.onExit != nil {
			c.exitOnce.Do(func() {
				c.onExit(waitErr)
			})
		}
	})
}

// Initialize performs the mandatory ACP handshake with IDE/file/terminal capabilities disabled.
func (c *ACPClient) Initialize(ctx context.Context, clientName, clientVersion string) (*InitializeResult, error) {
	params := map[string]any{
		"protocolVersion": 1,
		"clientCapabilities": map[string]any{
			"fs": map[string]any{
				"readTextFile":  false,
				"writeTextFile": false,
			},
			"terminal": false,
		},
		"clientInfo": map[string]any{
			"name":    clientName,
			"version": clientVersion,
		},
	}
	raw, err := c.call(ctx, "initialize", params)
	if err != nil {
		return nil, err
	}
	var res InitializeResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, fmt.Errorf("acp: decode initialize response: %w", err)
	}
	return &res, nil
}

// Authenticate performs interactive or keyring ACP authentication using methodID and returns any tier name.
func (c *ACPClient) Authenticate(ctx context.Context, methodID string) (string, error) {
	raw, err := c.call(ctx, "authenticate", map[string]any{"methodId": methodID})
	if err != nil {
		return "", err
	}
	var res struct {
		Meta struct {
			Antigravity struct {
				Tier struct {
					Name string `json:"name"`
				} `json:"tier"`
			} `json:"antigravity"`
		} `json:"_meta"`
	}
	_ = json.Unmarshal(raw, &res)
	return strings.TrimSpace(res.Meta.Antigravity.Tier.Name), nil
}

// NewSession starts an isolated ACP session with DezuxkStudio's system instructions appended.
func (c *ACPClient) NewSession(ctx context.Context, cwd, systemPrompt string) (string, error) {
	params := map[string]any{
		"cwd":        cwd,
		"mcpServers": []any{},
	}
	if strings.TrimSpace(systemPrompt) != "" {
		params["_meta"] = map[string]any{
			"antigravity": map[string]any{
				"systemPrompt": map[string]any{
					"append": systemPrompt,
				},
			},
		}
	}
	raw, err := c.call(ctx, "session/new", params)
	if err != nil {
		return "", err
	}
	var res struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return "", fmt.Errorf("acp: decode session/new response: %w", err)
	}
	models, current := parseACPSessionModels(raw)
	c.modelsMu.Lock()
	c.models, c.currentModel = models, current
	c.modelsMu.Unlock()
	if res.SessionID == "" {
		return "", errors.New("acp: agent returned empty sessionId")
	}
	return res.SessionID, nil
}

// Prompt submits a user text prompt to an active session and awaits turn completion.
func (c *ACPClient) Prompt(ctx context.Context, sessionID, text string) error {
	params := map[string]any{
		"sessionId": sessionID,
		"prompt": []map[string]any{
			{
				"type": "text",
				"text": text,
			},
		},
	}
	_, err := c.call(ctx, "session/prompt", params)
	return err
}

// Cancel sends a session/cancel notification to interrupt an in-flight prompt turn.
func (c *ACPClient) Cancel(sessionID string) error {
	return c.notify("session/cancel", map[string]any{"sessionId": sessionID})
}

// Close terminates the underlying transport and waits for reader shutdown.
func (c *ACPClient) Close() error {
	c.closeMu.Lock()
	if c.closed {
		c.closeMu.Unlock()
		<-c.doneCh
		return nil
	}
	c.closed = true
	c.closeMu.Unlock()

	if c.stdin != nil {
		_ = c.stdin.Close()
	}
	if c.cmd != nil {
		killCommandTree(c.cmd)
	} else {
		if c.stdout != nil {
			_ = c.stdout.Close()
		}
		if c.stderr != nil {
			_ = c.stderr.Close()
		}
	}
	<-c.doneCh
	return nil
}

func (c *ACPClient) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	idNum := c.nextID.Add(1)
	idRaw, _ := json.Marshal(idNum)
	key := string(idRaw)

	ch := make(chan rpcOutcome, 1)
	c.pendMu.Lock()
	if c.closed {
		c.pendMu.Unlock()
		return nil, errors.New("acp client closed")
	}
	c.pending[key] = ch
	c.pendMu.Unlock()

	msg := map[string]any{
		"jsonrpc": "2.0",
		"id":      idNum,
		"method":  method,
		"params":  params,
	}
	if err := c.writeJSON(msg); err != nil {
		c.pendMu.Lock()
		delete(c.pending, key)
		c.pendMu.Unlock()
		return nil, err
	}

	select {
	case out := <-ch:
		return out.result, out.err
	case <-ctx.Done():
		c.pendMu.Lock()
		delete(c.pending, key)
		c.pendMu.Unlock()
		return nil, ctx.Err()
	}
}

func (c *ACPClient) notify(method string, params any) error {
	return c.writeJSON(map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
	})
}

func (c *ACPClient) replyResult(id json.RawMessage, result any) {
	if len(id) == 0 {
		return
	}
	_ = c.writeJSON(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"result":  result,
	})
}

func (c *ACPClient) writeJSON(v any) error {
	payload, err := json.Marshal(v)
	if err != nil {
		return err
	}
	payload = append(payload, '\n')

	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if c.stdin == nil {
		return errors.New("acp stdin closed")
	}
	_, err = c.stdin.Write(payload)
	return err
}

func (c *ACPClient) readStdout() {
	reader := bufio.NewReaderSize(c.stdout, 64*1024)
	for {
		line, err := reader.ReadString('\n')
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			c.handleLine(trimmed)
		}
		if err != nil {
			return
		}
	}
}

func (c *ACPClient) readStderr() {
	reader := bufio.NewReaderSize(c.stderr, 16*1024)
	for {
		line, err := reader.ReadString('\n')
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			c.log.Debug("acp stderr", "line", redactSecrets(trimmed))
		}
		if err != nil {
			return
		}
	}
}

func (c *ACPClient) handleLine(line string) {
	var env rpcEnvelope
	if err := json.Unmarshal([]byte(line), &env); err != nil {
		c.log.Debug("acp non-json stdout", "line", redactSecrets(line))
		return
	}

	if len(env.ID) > 0 && env.Method == "" {
		key := strings.TrimSpace(string(env.ID))
		c.pendMu.Lock()
		ch, ok := c.pending[key]
		if ok {
			delete(c.pending, key)
		}
		c.pendMu.Unlock()
		if !ok {
			return
		}
		if env.Error != nil {
			ch <- rpcOutcome{err: env.Error}
			return
		}
		ch <- rpcOutcome{result: env.Result}
		return
	}

	if len(env.ID) > 0 && env.Method != "" {
		c.handleServerRequest(env.ID, env.Method, env.Params)
		return
	}

	if env.Method == "session/update" && c.onEvent != nil {
		if upd, ok := parseSessionUpdate(env.Params); ok {
			c.onEvent(upd)
		}
	}
}

func (c *ACPClient) handleServerRequest(id json.RawMessage, method string, params json.RawMessage) {
	if method == "session/request_permission" {
		if optID, allowed := allowedDezuxkPermissionOption(params); allowed {
			c.replyResult(id, map[string]any{
				"outcome": map[string]any{
					"outcome":  "selected",
					"optionId": optID,
				},
			})
			return
		}
		c.replyResult(id, map[string]any{
			"outcome": map[string]any{
				"outcome": "cancelled",
			},
		})
		return
	}
	_ = c.writeJSON(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"error": map[string]any{
			"code":    -32601,
			"message": "method not supported in DezuxkStudio isolated mode",
		},
	})
}

// allowedDezuxkPermissionOption permits ACP tool approval ONLY for calls targeting DezuxkStudio's own MCP tools.
// All built-in coding, shell, or arbitrary file permissions are strictly cancelled.
func allowedDezuxkPermissionOption(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 {
		return "", false
	}
	var req struct {
		ToolCall struct {
			Title string `json:"title"`
			Kind  string `json:"kind"`
		} `json:"toolCall"`
		Options []struct {
			OptionID string `json:"optionId"`
			Kind     string `json:"kind"`
		} `json:"options"`
	}
	if json.Unmarshal(raw, &req) != nil {
		return "", false
	}
	if !isDezuxkToolPermission(req.ToolCall.Title) {
		return "", false
	}
	for _, opt := range req.Options {
		if opt.Kind == "allow_once" || opt.Kind == "allow_always" {
			return opt.OptionID, true
		}
	}
	return "", false
}

func (c *ACPClient) failAllPending(err error) {
	c.pendMu.Lock()
	defer c.pendMu.Unlock()
	for key, ch := range c.pending {
		ch <- rpcOutcome{err: err}
		delete(c.pending, key)
	}
}

func parseSessionUpdate(params json.RawMessage) (SessionUpdate, bool) {
	var raw struct {
		SessionID string `json:"sessionId"`
		Update    struct {
			SessionUpdate string          `json:"sessionUpdate"`
			MessageID     string          `json:"messageId"`
			Content       json.RawMessage `json:"content"`
			Title         string          `json:"title"`
			Status        string          `json:"status"`
			ToolCallID    string          `json:"toolCallId"`
		} `json:"update"`
	}
	if err := json.Unmarshal(params, &raw); err != nil {
		return SessionUpdate{}, false
	}
	kind := raw.Update.SessionUpdate
	if kind == "" {
		return SessionUpdate{}, false
	}
	return SessionUpdate{
		SessionID: raw.SessionID,
		Kind:      kind,
		MessageID: raw.Update.MessageID,
		Text:      extractTextContent(raw.Update.Content),
		Tool:      raw.Update.Title,
		Status:    raw.Update.Status,
	}, true
}

func extractTextContent(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var single struct {
		Type  string `json:"type"`
		Text  string `json:"text"`
		Delta string `json:"delta"`
	}
	if err := json.Unmarshal(raw, &single); err == nil {
		if single.Text != "" {
			return single.Text
		}
		if single.Delta != "" {
			return single.Delta
		}
	}
	var blocks []struct {
		Type  string `json:"type"`
		Text  string `json:"text"`
		Delta string `json:"delta"`
	}
	if err := json.Unmarshal(raw, &blocks); err == nil {
		var sb strings.Builder
		for _, b := range blocks {
			if b.Text != "" {
				sb.WriteString(b.Text)
			} else if b.Delta != "" {
				sb.WriteString(b.Delta)
			}
		}
		return sb.String()
	}
	return ""
}

// dezuxkMCPToolNames is the exact set of tools served by DezuxkStudio's MCP server.
var dezuxkMCPToolNames = map[string]bool{
	"studio_list_capabilities": true, "workflow_list_specs": true, "workflow_run_node": true, "workflow_run_graph": true,
	"binaries_list": true, "binaries_probe": true, "binaries_download": true, "preferences_get": true,
	"preferences_update": true, "system_info": true, "diagnostics_export": true, "workspace_list_files": true,
	"workspace_read_file": true, "workspace_write_file": true, "workspace_move_file": true, "workspace_delete_file": true,
}

var (
	// "MCP Tool workflow_run_node", "dezuxk/workflow_run_node", "mcp__dezuxk__workflow_run_node", "dezuxk.workflow_run_node".
	reDezuxkToolTitle = regexp.MustCompile(`^(?:mcp tool\s+(?:` + mcpServerName + `[/.:]|` + mcpToolPrefix + `)?|` + mcpServerName + `[/.:]|` + mcpToolPrefix + `)([a-z_]+)$`)
	// Antigravity reads the schema files it generated for DezuxkStudio's MCP server inside its own isolated profile,
	// e.g. "File ../../DezuxkStudio/data/agent-profiles/antigravity/home/.gemini/antigravity-cli/mcp/dezuxk/workflow_run_node.json".
	reAgyDezuxkSchemaRead = regexp.MustCompile(`^file\s+\S*[/\\]antigravity-cli[/\\]mcp[/\\]` + mcpServerName + `[/\\]([a-z_]+)\.json$`)
)

// isDezuxkToolPermission matches a permission request title against DezuxkStudio's exact MCP tool names.
// Built-in shell/file/edit tools are never approved, even when a path in their title mentions DezuxkStudio.
func isDezuxkToolPermission(title string) bool {
	t := strings.ToLower(strings.TrimSpace(title))
	if m := reDezuxkToolTitle.FindStringSubmatch(t); m != nil {
		return dezuxkMCPToolNames[m[1]]
	}
	if m := reAgyDezuxkSchemaRead.FindStringSubmatch(t); m != nil {
		return dezuxkMCPToolNames[m[1]] // read-only schema of our own tools
	}
	return false
}

// AvailableModels returns the models offered by the latest session/new response.
func (c *ACPClient) AvailableModels() ([]domainagent.ModelOption, string) {
	c.modelsMu.Lock()
	defer c.modelsMu.Unlock()
	return append([]domainagent.ModelOption(nil), c.models...), c.currentModel
}

// SetModel switches the model of an ACP session (ACP session/set_model).
func (c *ACPClient) SetModel(ctx context.Context, sessionID, modelID string) error {
	_, err := c.call(ctx, "session/set_model", map[string]any{"sessionId": sessionID, "modelId": modelID})
	return err
}
