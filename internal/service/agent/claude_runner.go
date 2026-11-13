package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	domainagent "app/internal/domain/agent"
	"app/internal/fsutil"
	"app/internal/proc"

	"github.com/google/uuid"
)

const (
	claudeMCPServerName   = mcpServerName
	claudeMCPToolPrefix   = mcpToolPrefix
	claudeMCPAllowedTools = mcpToolPrefix + "*"
)

func claudeAPIKeyPath(profileRoot string) string {
	return filepath.Join(profileRoot, "anthropic_api_key")
}

func (ps profileSecrets) loadClaudeAPIKey(profileRoot string) string {
	if profileRoot == "" {
		return ""
	}
	return ps.read(claudeAPIKeyPath(profileRoot)) // OS credential store
}

func (ps profileSecrets) prepareClaudeProfile(profileRoot string) (map[string]string, error) {
	if profileRoot == "" {
		return nil, fmt.Errorf("agent: claude profile root is required")
	}
	for _, dir := range []string{profileRoot, filepath.Join(profileRoot, "projects")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("agent: create claude profile dir %s: %w", dir, err)
		}
	}
	envMap := map[string]string{
		"CLAUDE_CONFIG_DIR": profileRoot,
	}
	if apiKey := ps.loadClaudeAPIKey(profileRoot); apiKey != "" {
		envMap["ANTHROPIC_API_KEY"] = apiKey
	}
	return envMap, nil
}

func (ps profileSecrets) claudeIsolatedEnv(profileRoot string) ([]string, error) {
	envMap, err := ps.prepareClaudeProfile(profileRoot)
	if err != nil {
		return nil, err
	}
	return providerEnv(domainagent.ProviderClaude, envMap), nil
}

type claudeAuthStatusJSON struct {
	LoggedIn         bool   `json:"loggedIn"`
	AuthMethod       string `json:"authMethod"`
	Email            string `json:"email"`
	SubscriptionType string `json:"subscriptionType"`
	APIKeySource     string `json:"apiKeySource"`
}

func formatClaudeAccountInfo(st claudeAuthStatusJSON) string {
	email := strings.TrimSpace(st.Email)
	sub := strings.TrimSpace(st.SubscriptionType)
	if email != "" {
		if sub != "" {
			return email + " (" + sub + ")"
		}
		return email
	}
	if strings.TrimSpace(st.APIKeySource) != "" || strings.EqualFold(st.AuthMethod, "api_key") {
		return "Isolated Anthropic API key"
	}
	if method := firstLineTrimmed(st.AuthMethod); method != "" && method != "none" {
		return "Signed in via " + method
	}
	return "Signed in"
}

func (ps profileSecrets) probeClaudeAuth(ctx context.Context, executable, profileRoot string) (domainagent.AuthState, string) {
	if executable == "" || profileRoot == "" {
		return domainagent.AuthUnknown, ""
	}
	env, err := ps.claudeIsolatedEnv(profileRoot)
	if err != nil {
		return domainagent.AuthUnknown, ""
	}
	cmd := proc.Command(ctx, executable, "auth", "status", "--json") // the caller bounds ctx
	cmd.Env = env
	out, cmdErr := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return domainagent.AuthUnknown, "" // a slow probe says nothing about the sign-in
	}

	var parsed claudeAuthStatusJSON
	if jsonErr := json.Unmarshal(out, &parsed); jsonErr == nil {
		if parsed.LoggedIn {
			return domainagent.AuthAuthenticated, formatClaudeAccountInfo(parsed)
		}
		return domainagent.AuthUnauthenticated, ""
	}

	lower := strings.ToLower(strings.TrimSpace(string(out)))
	if cmdErr == nil && strings.Contains(lower, `"loggedin":true`) {
		return domainagent.AuthAuthenticated, firstLineTrimmed(string(out))
	}
	return domainagent.AuthUnauthenticated, ""
}

func (ps profileSecrets) loginClaude(ctx context.Context, executable, profileRoot, apiKey string) error {
	if _, err := ps.prepareClaudeProfile(profileRoot); err != nil {
		return err
	}
	trimmedKey := strings.TrimSpace(apiKey)
	if trimmedKey != "" {
		if err := ps.write(claudeAPIKeyPath(profileRoot), trimmedKey); err != nil {
			return fmt.Errorf("claude: save isolated api key: %w", err)
		}
		if state, _ := ps.probeClaudeAuth(ctx, executable, profileRoot); state != domainagent.AuthAuthenticated {
			ps.remove(claudeAPIKeyPath(profileRoot))
			return fmt.Errorf("claude api key login check failed")
		}
		return nil
	}

	return fmt.Errorf("claude: an API key is required (interactive sign-in is loginClaudeInteractive)")
}

// loginClaudeInteractive signs the isolated CLAUDE_CONFIG_DIR in through the browser: the CLI opens the
// page and waits for the code it shows, which the user pastes in the app.
func (s *Service) loginClaudeInteractive(ctx context.Context, executable, profileRoot string) error {
	if _, err := s.secrets.prepareClaudeProfile(profileRoot); err != nil {
		return err
	}
	s.secrets.remove(claudeAPIKeyPath(profileRoot))
	env, err := s.secrets.claudeIsolatedEnv(profileRoot)
	if err != nil {
		return err
	}
	if err := s.interactiveLogin(ctx, domainagent.ProviderClaude, executable, env); err != nil {
		return err
	}
	if state, _ := s.probeClaude(ctx, executable, profileRoot); state != domainagent.AuthAuthenticated {
		return fmt.Errorf("claude login completed without active credentials in isolated profile")
	}
	return nil
}

func (ps profileSecrets) logoutClaude(ctx context.Context, executable, profileRoot string) error {
	if profileRoot == "" {
		return nil
	}
	ps.remove(claudeAPIKeyPath(profileRoot))
	if executable == "" {
		return nil
	}
	env, err := ps.claudeIsolatedEnv(profileRoot)
	if err != nil {
		return err
	}
	cmd := proc.Command(ctx, executable, "auth", "logout") // the caller bounds ctx
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	lower := strings.ToLower(string(out))
	if err != nil && !strings.Contains(lower, "not logged in") && !strings.Contains(lower, "logged out") {
		return fmt.Errorf("claude logout failed: %s", redactSecrets(strings.TrimSpace(string(out))))
	}
	return nil
}

func writeClaudeMCPConfig(profileRoot, mcpURL, mcpToken string) (string, func(), error) {
	if profileRoot == "" || mcpURL == "" || mcpToken == "" {
		return "", func() {}, fmt.Errorf("claude: profileRoot, mcpURL, and mcpToken are required")
	}
	cfg := map[string]any{
		"mcpServers": map[string]any{
			claudeMCPServerName: map[string]any{
				"type": "http",
				"url":  mcpURL,
				"headers": map[string]string{
					"Authorization": "Bearer " + mcpToken,
				},
			},
		},
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return "", func() {}, err
	}
	cfgPath := filepath.Join(profileRoot, fmt.Sprintf("claude-mcp-%d.json", time.Now().UnixNano()))
	if err := fsutil.WriteFileAtomic(cfgPath, append(raw, '\n'), 0o600); err != nil {
		return "", func() {}, fmt.Errorf("claude: write ephemeral mcp config: %w", err)
	}
	return cfgPath, func() { _ = os.Remove(cfgPath) }, nil
}

func buildClaudeExecArgs(mcpConfigPath, instructions string) []string {
	return []string{
		"-p",
		"--output-format", "stream-json",
		"--include-partial-messages", // token-level stream_event deltas instead of whole messages only
		"--verbose",
		"--restricted",
		"--tools", "",
		"--disable-slash-commands",
		"--setting-sources", "",
		"--strict-mcp-config",
		"--mcp-config", mcpConfigPath,
		"--allowedTools", claudeMCPAllowedTools,
		"--permission-mode", "dontAsk",
		"--system-prompt", instructions,
	}
}

type claudeContentBlock struct {
	Type      string `json:"type"`
	Text      string `json:"text"`
	Thinking  string `json:"thinking"`
	ID        string `json:"id"`
	Name      string `json:"name"`
	ToolUseID string `json:"tool_use_id"`
	IsError   bool   `json:"is_error"`
	Content   any    `json:"content"`
}

type claudeStreamEventJSON struct {
	Type              string `json:"type"`
	Error             string `json:"error"`
	IsAPIErrorMessage bool   `json:"is_api_error_message"`
	IsError           bool   `json:"is_error"`
	Result            string `json:"result"`
	Message           *struct {
		ID      string               `json:"id"`
		Content []claudeContentBlock `json:"content"`
	} `json:"message"`
	Event *claudePartialEvent `json:"event"` // set on type "stream_event" (--include-partial-messages)
}

// claudePartialEvent is one raw Anthropic streaming event forwarded by Claude Code.
type claudePartialEvent struct {
	Type    string `json:"type"`
	Message *struct {
		ID string `json:"id"`
	} `json:"message"`
	ContentBlock *struct {
		Type string `json:"type"`
	} `json:"content_block"`
	Delta *struct {
		Type     string `json:"type"`
		Text     string `json:"text"`
		Thinking string `json:"thinking"`
	} `json:"delta"`
}

type claudeStreamAccumulator struct {
	providerID    domainagent.ProviderID
	messageID     string
	emit          func(domainagent.ChatEvent)
	toolNames     map[string]string
	assistantText strings.Builder
	thoughtText   strings.Builder
	lastFailure   string
	textChunks    int

	// Partial streaming: the message currently streaming and the messages whose text/thinking already
	// reached the UI as deltas, so their final "assistant" event does not repeat it.
	partialMsgID     string
	streamedText     map[string]bool
	streamedThinking map[string]bool
	blockOpen        bool // a text block of the current message has started and emitted its separator
}

func (a *claudeStreamAccumulator) assistantOutput() string { return a.assistantText.String() }
func (a *claudeStreamAccumulator) thoughtOutput() string   { return a.thoughtText.String() }
func (a *claudeStreamAccumulator) failureMessage() string  { return a.lastFailure }

func (a *claudeStreamAccumulator) handleLine(raw string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return
	}
	var evt claudeStreamEventJSON
	if json.Unmarshal([]byte(raw), &evt) != nil {
		return
	}
	if a.toolNames == nil {
		a.toolNames = make(map[string]string)
		a.streamedText = make(map[string]bool)
		a.streamedThinking = make(map[string]bool)
	}

	switch evt.Type {
	case "stream_event":
		a.handlePartial(evt.Event)
	case "assistant":
		if evt.Message == nil {
			return
		}
		if evt.IsAPIErrorMessage || evt.Error != "" {
			if txt := extractClaudeMessageText(evt.Message.Content); txt != "" {
				a.lastFailure = txt
			} else if evt.Error != "" {
				a.lastFailure = evt.Error
			}
			return
		}
		id := evt.Message.ID
		for _, block := range evt.Message.Content {
			if (block.Type == "text" && a.streamedText[id]) || (block.Type == "thinking" && a.streamedThinking[id]) {
				continue // already delivered token by token
			}
			a.handleAssistantBlock(block)
		}
	case "user":
		if evt.Message == nil {
			return
		}
		for _, block := range evt.Message.Content {
			if block.Type == "tool_result" {
				a.handleToolResult(block)
			}
		}
	case "result":
		trimmed := strings.TrimSpace(evt.Result)
		if evt.IsError {
			if trimmed != "" {
				a.lastFailure = trimmed
			} else if a.lastFailure == "" {
				a.lastFailure = "claude completed with an error"
			}
			return
		}
		if a.assistantText.Len() == 0 && trimmed != "" {
			a.appendAssistantText(evt.Result)
		}
	}
}

func (a *claudeStreamAccumulator) handleAssistantBlock(block claudeContentBlock) {
	switch block.Type {
	case "thinking":
		if strings.TrimSpace(block.Thinking) == "" {
			return
		}
		if a.thoughtText.Len() > 0 {
			a.thoughtText.WriteByte('\n')
		}
		a.thoughtText.WriteString(block.Thinking)
		if a.emit != nil {
			a.emit(domainagent.ChatEvent{
				Type:       domainagent.ChatEventThought,
				ProviderID: a.providerID,
				MessageID:  a.messageID,
				Text:       block.Thinking + "\n",
			})
		}
	case "text":
		if strings.TrimSpace(block.Text) != "" {
			a.appendAssistantText(block.Text)
		}
	case "tool_use":
		name := stripClaudeMCPPrefix(block.Name)
		if block.ID != "" && name != "" {
			a.toolNames[block.ID] = name
		}
		if name != "" && a.emit != nil {
			a.emit(domainagent.ChatEvent{
				Type:       domainagent.ChatEventToolCall,
				ProviderID: a.providerID,
				MessageID:  a.messageID,
				Tool:       name,
				Text:       name,
			})
		}
	}
}

func (a *claudeStreamAccumulator) handleToolResult(block claudeContentBlock) {
	if a.emit == nil {
		return
	}
	name := a.toolNames[block.ToolUseID]
	if name == "" {
		name = "tool"
	}
	outText := firstLineTrimmedMax(extractClaudeAnyText(block.Content), 240)
	errText := ""
	if block.IsError {
		if outText == "" {
			outText = toolStatusFailed
		}
		errText = outText
	} else if outText == "" {
		outText = toolStatusCompleted
	}
	a.emit(domainagent.ChatEvent{
		Type:       domainagent.ChatEventToolResult,
		ProviderID: a.providerID,
		MessageID:  a.messageID,
		Tool:       name,
		Text:       outText,
		Error:      errText,
	})
}

// handlePartial forwards text and thinking deltas as they are generated.
func (a *claudeStreamAccumulator) handlePartial(ev *claudePartialEvent) {
	if ev == nil {
		return
	}
	switch ev.Type {
	case "message_start":
		if ev.Message != nil {
			a.partialMsgID = ev.Message.ID
		}
	case "content_block_start":
		a.blockOpen = false
		if ev.ContentBlock != nil && ev.ContentBlock.Type == "thinking" && a.thoughtText.Len() > 0 {
			a.thoughtText.WriteByte('\n')
			a.emitEvent(domainagent.ChatEventThought, "\n")
		}
	case "content_block_delta":
		if ev.Delta == nil || a.partialMsgID == "" {
			return
		}
		switch ev.Delta.Type {
		case "text_delta":
			if ev.Delta.Text == "" {
				return
			}
			a.streamedText[a.partialMsgID] = true
			prefix := ""
			if !a.blockOpen {
				a.blockOpen = true
				if a.textChunks > 0 {
					prefix = "\n\n" // separate this text block from the previous one
				}
				a.textChunks++
			}
			a.assistantText.WriteString(prefix + ev.Delta.Text)
			a.emitEvent(domainagent.ChatEventDelta, prefix+ev.Delta.Text)
		case "thinking_delta":
			if ev.Delta.Thinking == "" {
				return
			}
			a.streamedThinking[a.partialMsgID] = true
			a.thoughtText.WriteString(ev.Delta.Thinking)
			a.emitEvent(domainagent.ChatEventThought, ev.Delta.Thinking)
		}
	}
}

func (a *claudeStreamAccumulator) emitEvent(typ domainagent.ChatEventType, text string) {
	if a.emit != nil {
		a.emit(domainagent.ChatEvent{Type: typ, ProviderID: a.providerID, MessageID: a.messageID, Text: text})
	}
}

func (a *claudeStreamAccumulator) appendAssistantText(txt string) {
	prefix := ""
	if a.textChunks > 0 {
		prefix = "\n\n"
	}
	a.textChunks++
	a.assistantText.WriteString(prefix)
	a.assistantText.WriteString(txt)
	if a.emit != nil {
		a.emit(domainagent.ChatEvent{
			Type:       domainagent.ChatEventDelta,
			ProviderID: a.providerID,
			MessageID:  a.messageID,
			Text:       prefix + txt,
		})
	}
}

func stripClaudeMCPPrefix(name string) string {
	return strings.TrimPrefix(strings.TrimSpace(name), claudeMCPToolPrefix)
}

func extractClaudeMessageText(blocks []claudeContentBlock) string {
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func extractClaudeAnyText(content any) string {
	switch v := content.(type) {
	case string:
		return strings.TrimSpace(v)
	case []any:
		var parts []string
		for _, el := range v {
			if m, ok := el.(map[string]any); ok {
				if txt, ok := m["text"].(string); ok && strings.TrimSpace(txt) != "" {
					parts = append(parts, txt)
				}
			} else if s, ok := el.(string); ok && strings.TrimSpace(s) != "" {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, " ")
	case map[string]any:
		if txt, ok := v["text"].(string); ok {
			return strings.TrimSpace(txt)
		}
	}
	return ""
}

func (s *Service) runClaudeTurn(ctx context.Context, executable, profileRoot, workspaceDir, mcpURL, mcpToken, messageID, prompt string) error {
	env, err := s.secrets.claudeIsolatedEnv(profileRoot)
	if err != nil {
		return err
	}
	mcpCfgPath, cleanup, err := writeClaudeMCPConfig(profileRoot, mcpURL, mcpToken)
	if err != nil {
		return err
	}
	defer cleanup()
	env = append(env, claudeMCPTimeoutEnv(s.limits.MCPToolTimeout)...)

	args := buildClaudeExecArgs(mcpCfgPath, s.systemPrompt(ctx, domainagent.ProviderClaude))
	args = append(args, claudeModelArgs(s.effectiveSelection(domainagent.ProviderClaude))...)
	// Keep one conversation per chat: the first turn names the session, later turns resume it.
	// Sessions are stored only inside the isolated CLAUDE_CONFIG_DIR.
	sessionID := s.cliSessionFor(domainagent.ProviderClaude)
	if sessionID != "" {
		args = append(args, "--resume", sessionID)
	} else {
		sessionID = uuid.NewString()
		args = append(args, "--session-id", sessionID)
	}
	acc := &claudeStreamAccumulator{
		providerID: domainagent.ProviderClaude,
		messageID:  messageID,
		emit:       s.emitChat,
	}
	err = runStreamJSONCommand(
		ctx,
		"claude",
		executable,
		args,
		workspaceDir,
		env,
		prompt,
		"check isolated Claude login",
		acc,
	)
	if err == nil {
		s.setCLISession(domainagent.ProviderClaude, sessionID)
	}
	return err
}

// claudeMCPTimeoutEnv raises Claude Code's MCP tool call limit (MCP_TOOL_TIMEOUT, milliseconds) so a
// workflow run through a tool is not abandoned while it still runs.
func claudeMCPTimeoutEnv(toolTimeout time.Duration) []string {
	if toolTimeout <= 0 {
		return nil
	}
	return []string{"MCP_TOOL_TIMEOUT=" + strconv.FormatInt(toolTimeout.Milliseconds(), 10)}
}
