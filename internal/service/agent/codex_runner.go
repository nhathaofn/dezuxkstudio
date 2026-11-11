package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	domainagent "app/internal/domain/agent"
	"app/internal/fsutil"
)

func prepareCodexProfile(profileRoot string) (map[string]string, error) {
	if profileRoot == "" {
		return nil, fmt.Errorf("agent: codex profile root is required")
	}
	if err := os.MkdirAll(profileRoot, 0o700); err != nil {
		return nil, fmt.Errorf("agent: create codex profile dir: %w", err)
	}
	return map[string]string{
		"CODEX_HOME": profileRoot,
	}, nil
}

func probeCodexAuth(ctx context.Context, executable, profileRoot string) (domainagent.AuthState, string) {
	if executable == "" || profileRoot == "" {
		return domainagent.AuthUnknown, ""
	}
	envMap, err := prepareCodexProfile(profileRoot)
	if err != nil {
		return domainagent.AuthUnknown, ""
	}
	// The caller bounds ctx (Limits.AuthProbeTimeout).
	cmd := buildCommand(ctx, executable, "login", "status")
	cmd.Env = providerEnv(domainagent.ProviderCodex, envMap)
	out, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(out))
	lower := strings.ToLower(text)
	if err == nil && (strings.Contains(lower, "logged in") && !strings.Contains(lower, "not logged in")) {
		return domainagent.AuthAuthenticated, firstLineTrimmed(text)
	}
	if strings.Contains(lower, "not logged in") {
		return domainagent.AuthUnauthenticated, ""
	}
	if err != nil {
		return domainagent.AuthUnauthenticated, ""
	}
	return domainagent.AuthAuthenticated, firstLineTrimmed(text)
}

func loginCodex(ctx context.Context, executable, profileRoot, apiKey string) error {
	envMap, err := prepareCodexProfile(profileRoot)
	if err != nil {
		return err
	}
	trimmedKey := strings.TrimSpace(apiKey)
	var args []string
	if trimmedKey != "" {
		args = []string{"login", "--with-api-key"}
	} else {
		args = []string{"login"}
	}
	cmd := buildCommand(ctx, executable, args...) // the caller bounds ctx (Limits.LoginTimeout)
	cmd.Env = providerEnv(domainagent.ProviderCodex, envMap)
	if trimmedKey != "" {
		cmd.Stdin = strings.NewReader(trimmedKey + "\n")
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := redactSecrets(strings.TrimSpace(string(out)))
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("codex login failed: %s", msg)
	}
	return nil
}

func logoutCodex(ctx context.Context, executable, profileRoot string) error {
	envMap, err := prepareCodexProfile(profileRoot)
	if err != nil {
		return err
	}
	cmd := buildCommand(ctx, executable, "logout") // the caller bounds ctx (Limits.AuthProbeTimeout)
	cmd.Env = providerEnv(domainagent.ProviderCodex, envMap)
	out, err := cmd.CombinedOutput()
	if err != nil && !strings.Contains(strings.ToLower(string(out)), "not logged in") {
		return fmt.Errorf("codex logout failed: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

// writeCodexBaseInstructions stores DezuxkStudio's profile as Codex's model base instructions, replacing
// Codex's built-in "coding agent" prompt so the model does not start from a software-engineering persona.
func writeCodexBaseInstructions(profileRoot, instructions string) (string, error) {
	path := filepath.Join(profileRoot, "dezuxk-instructions.md")
	if err := fsutil.WriteFileAtomic(path, []byte(instructions+"\n"), 0o600); err != nil {
		return "", fmt.Errorf("codex: write base instructions: %w", err)
	}
	return path, nil
}

// buildCodexExecArgs builds a `codex exec` (first turn) or `codex exec resume <id>` (later turns) command line.
// The working directory is set through cmd.Dir because `exec resume` has no -C flag; the sandbox is set through
// config so both forms stay identical. Sessions are persisted only inside the isolated CODEX_HOME.
func buildCodexExecArgs(mcpURL, instructions, baseInstructionsFile, resumeID string) []string {
	args := []string{"exec"}
	if resumeID != "" {
		args = append(args, "resume", resumeID)
	}
	args = append(args,
		"--json",
		"--skip-git-repo-check",
		"--ignore-user-config",
		"--ignore-rules",
		"-c", "developer_instructions="+strconv.Quote(instructions),
	)
	args = append(args, codexPolicyArgs(mcpURL, baseInstructionsFile)...)
	return append(args, "-")
}

// codexPolicyArgs are the security settings shared by `codex exec` and `codex app-server`: read-only
// sandbox, no approvals, no shell or apps, and DezuxkStudio's MCP server as the only tool source.
func codexPolicyArgs(mcpURL, baseInstructionsFile string) []string {
	args := []string{
		"-c", `sandbox_mode="read-only"`,
		"-c", `approval_policy="never"`,
		"-c", "features.shell_tool=false",
		"-c", "features.apps=false",
		"-c", "mcp_servers." + mcpServerName + ".url=" + strconv.Quote(mcpURL),
		"-c", "mcp_servers." + mcpServerName + ".bearer_token_env_var=" + strconv.Quote(mcpTokenEnvVar),
	}
	if baseInstructionsFile != "" {
		args = append(args, "-c", "model_instructions_file="+strconv.Quote(baseInstructionsFile))
	}
	return args
}

type streamJSONAccumulator interface {
	handleLine(raw string)
	assistantOutput() string
	thoughtOutput() string
	failureMessage() string
}

type codexStreamAccumulator struct {
	providerID    domainagent.ProviderID
	messageID     string
	emit          func(domainagent.ChatEvent)
	assistantText strings.Builder
	thoughtText   strings.Builder
	lastFailure   string
	textChunks    int
	threadID      string
}

func (a *codexStreamAccumulator) assistantOutput() string { return a.assistantText.String() }
func (a *codexStreamAccumulator) thoughtOutput() string   { return a.thoughtText.String() }
func (a *codexStreamAccumulator) failureMessage() string  { return a.lastFailure }

func (a *codexStreamAccumulator) handleLine(raw string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return
	}
	var evt map[string]any
	if json.Unmarshal([]byte(raw), &evt) != nil {
		return
	}
	typ, _ := evt["type"].(string)
	switch typ {
	case "error":
		msg, _ := evt["message"].(string)
		if msg != "" && msg != a.lastFailure {
			a.lastFailure = msg
		}
	case "thread.started":
		if id, _ := evt["thread_id"].(string); id != "" {
			a.threadID = id
		}
	case "turn.failed":
		if errObj, ok := evt["error"].(map[string]any); ok {
			if msg, _ := errObj["message"].(string); msg != "" {
				a.lastFailure = msg
			}
		}
	case "item.started", "item.completed", "item.updated":
		item, ok := evt["item"].(map[string]any)
		if !ok {
			return
		}
		itemType, _ := item["type"].(string)
		completed := typ == "item.completed"
		switch itemType {
		case "reasoning":
			if completed {
				txt := extractCodexText(item)
				if txt != "" {
					if a.thoughtText.Len() > 0 {
						a.thoughtText.WriteByte('\n')
					}
					a.thoughtText.WriteString(txt)
					if a.emit != nil {
						a.emit(domainagent.ChatEvent{
							Type:       domainagent.ChatEventThought,
							ProviderID: a.providerID,
							MessageID:  a.messageID,
							Text:       txt + "\n",
						})
					}
				}
			}
		case "mcp_tool_call":
			toolName, _ := item["tool"].(string)
			if typ == "item.started" && toolName != "" && a.emit != nil {
				a.emit(domainagent.ChatEvent{
					Type:       domainagent.ChatEventToolCall,
					ProviderID: a.providerID,
					MessageID:  a.messageID,
					Tool:       toolName,
					Text:       toolName,
				})
			} else if completed && toolName != "" && a.emit != nil {
				evt := domainagent.ChatEvent{
					Type:       domainagent.ChatEventToolResult,
					ProviderID: a.providerID,
					MessageID:  a.messageID,
					Tool:       toolName,
					Text:       toolStatusCompleted,
				}
				if item["error"] != nil {
					evt.Text = toolStatusFailed
					evt.Error = toolStatusFailed
				}
				a.emit(evt)
			}
		case "agent_message":
			if completed {
				txt := extractCodexText(item)
				if txt != "" {
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
			}
		}
	}
}

// runCodexExecTurn runs one turn through `codex exec --json`. It is the fallback for CLIs without a
// working app-server: replies arrive only when each message is complete (no token streaming).
func (s *Service) runCodexExecTurn(ctx context.Context, executable, profileRoot, workspaceDir, mcpURL, mcpToken, messageID, prompt string) error {
	envMap, err := prepareCodexProfile(profileRoot)
	if err != nil {
		return err
	}
	envMap[mcpTokenEnvVar] = mcpToken

	baseFile, err := writeCodexBaseInstructions(profileRoot, SystemInstructions())
	if err != nil {
		return err
	}
	resumeID := s.cliSessionFor(domainagent.ProviderCodex)
	args := buildCodexExecArgs(mcpURL, SystemInstructions(), baseFile, resumeID)
	if modelArgs := codexModelArgs(s.effectiveSelection(domainagent.ProviderCodex)); len(modelArgs) > 0 {
		args = append(args[:len(args)-1:len(args)-1], append(modelArgs, "-")...) // keep "-" (stdin prompt) last
	}
	env := providerEnv(domainagent.ProviderCodex, envMap)
	acc := &codexStreamAccumulator{
		providerID: domainagent.ProviderCodex,
		messageID:  messageID,
		emit:       s.emitChat,
	}
	err = s.runStreamJSONCommand(
		ctx,
		domainagent.ProviderCodex,
		"codex",
		executable,
		args,
		workspaceDir,
		env,
		prompt,
		messageID,
		"check isolated Codex login",
		acc,
	)
	if err == nil && acc.threadID != "" {
		s.setCLISession(domainagent.ProviderCodex, acc.threadID)
	}
	return err
}

func (s *Service) runStreamJSONCommand(
	ctx context.Context,
	providerID domainagent.ProviderID,
	label string,
	executable string,
	args []string,
	workDir string,
	env []string,
	prompt string,
	messageID string,
	emptyHint string,
	acc streamJSONAccumulator,
) error {
	cmd := buildCommand(ctx, executable, args...)
	cmd.Dir = workDir
	cmd.Stdin = strings.NewReader(prompt)
	cmd.Env = env

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("%s: stdout pipe: %w", label, err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("%s: stderr pipe: %w", label, err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("%s: start: %w", label, err)
	}

	var stderrBuf strings.Builder
	stderrDone := make(chan struct{})
	go func() {
		defer close(stderrDone)
		sc := bufio.NewScanner(stderr)
		sc.Buffer(make([]byte, 4096), 256*1024)
		for sc.Scan() {
			line := redactSecrets(sc.Text())
			if stderrBuf.Len() < 8192 {
				stderrBuf.WriteString(line)
				stderrBuf.WriteByte('\n')
			}
		}
	}()

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		acc.handleLine(scanner.Text())
	}

	scanErr := scanner.Err()
	<-stderrDone
	waitErr := cmd.Wait()

	assistantText := acc.assistantOutput()
	thoughtText := acc.thoughtOutput()
	if assistantText != "" || thoughtText != "" {
		s.persistAssistantMessage(providerID, messageID, assistantText, thoughtText)
	}

	if ctx.Err() != nil {
		return ctx.Err()
	}
	if failure := acc.failureMessage(); failure != "" {
		return fmt.Errorf("%s", redactSecrets(failure))
	}
	if scanErr != nil {
		return fmt.Errorf("%s: read output: %w", label, scanErr)
	}
	if waitErr != nil {
		errMsg := strings.TrimSpace(stderrBuf.String())
		if errMsg != "" {
			return fmt.Errorf("%s", errMsg)
		}
		return fmt.Errorf("%s exited: %w", label, waitErr)
	}
	if assistantText == "" {
		return fmt.Errorf("%s completed without returning a message; %s", label, emptyHint)
	}
	return nil
}

func extractCodexText(item map[string]any) string {
	if s, ok := item["text"].(string); ok && strings.TrimSpace(s) != "" {
		return s
	}
	if summary, ok := item["summary"].([]any); ok {
		var parts []string
		for _, el := range summary {
			if s, ok := el.(string); ok && strings.TrimSpace(s) != "" {
				parts = append(parts, s)
			} else if m, ok := el.(map[string]any); ok {
				if t, ok := m["text"].(string); ok && strings.TrimSpace(t) != "" {
					parts = append(parts, t)
				}
			}
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

func firstLineTrimmed(s string) string {
	s = strings.TrimSpace(s)
	if idx := strings.IndexAny(s, "\r\n"); idx >= 0 {
		return strings.TrimSpace(s[:idx])
	}
	return s
}
