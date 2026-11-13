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
	"time"

	domainagent "app/internal/domain/agent"
	"app/internal/fsutil"
	"app/internal/proc"
	"app/internal/safego"
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
	cmd := proc.Command(ctx, executable, "login", "status")
	cmd.Env = providerEnv(domainagent.ProviderCodex, envMap)
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return domainagent.AuthUnknown, "" // a slow probe says nothing about the sign-in
	}
	text := strings.TrimSpace(string(out))
	// `codex login status` exits non-zero when signed out.
	if err != nil || strings.Contains(strings.ToLower(text), "not logged in") {
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
	if trimmedKey == "" {
		return fmt.Errorf("codex: an API key is required (interactive sign-in is loginCodexInteractive)")
	}
	cmd := proc.Command(ctx, executable, "login", "--with-api-key") // the caller bounds ctx (Limits.LoginTimeout)
	cmd.Env = providerEnv(domainagent.ProviderCodex, envMap)
	cmd.Stdin = strings.NewReader(trimmedKey + "\n")
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

// loginCodexInteractive signs the isolated CODEX_HOME in with the device-code flow: the UI shows the page
// and one-time code, so it works the same on a machine without a browser callback.
func (s *Service) loginCodexInteractive(ctx context.Context, executable, profileRoot string) error {
	envMap, err := prepareCodexProfile(profileRoot)
	if err != nil {
		return err
	}
	return s.interactiveLogin(ctx, domainagent.ProviderCodex, executable, providerEnv(domainagent.ProviderCodex, envMap))
}

func logoutCodex(ctx context.Context, executable, profileRoot string) error {
	envMap, err := prepareCodexProfile(profileRoot)
	if err != nil {
		return err
	}
	cmd := proc.Command(ctx, executable, "logout") // the caller bounds ctx (Limits.AuthProbeTimeout)
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

func writeCodexEphemeralInstructions(profileRoot, runID, instructions string) (string, func(), error) {
	subDir := filepath.Join(profileRoot, "subagents")
	if err := os.MkdirAll(subDir, 0o700); err != nil {
		return "", func() {}, fmt.Errorf("codex: create subagent dir: %w", err)
	}
	path := filepath.Join(subDir, fmt.Sprintf("instructions-%s.md", runID))
	if err := fsutil.WriteFileAtomic(path, []byte(instructions+"\n"), 0o600); err != nil {
		return "", func() {}, fmt.Errorf("codex: write ephemeral subagent instructions: %w", err)
	}
	return path, func() { _ = os.Remove(path) }, nil
}

// buildCodexExecArgs builds a `codex exec` (first turn) or `codex exec resume <id>` (later turns) command line.
// The working directory is set through cmd.Dir because `exec resume` has no -C flag; the sandbox is set through
// config so both forms stay identical. Sessions are persisted only inside the isolated CODEX_HOME.
func buildCodexExecArgs(mcpURL, instructions, baseInstructionsFile, resumeID string, toolTimeout time.Duration) []string {
	return buildCodexExecArgsForMode(mcpURL, instructions, baseInstructionsFile, resumeID, toolTimeout, false)
}

func buildCodexExecArgsForSubagent(mcpURL, instructions, baseInstructionsFile string, toolTimeout time.Duration) []string {
	return buildCodexExecArgsForMode(mcpURL, instructions, baseInstructionsFile, "", toolTimeout, false)
}

func buildCodexExecArgsForMode(mcpURL, instructions, baseInstructionsFile, resumeID string, toolTimeout time.Duration, devMode bool) []string {
	args := []string{"exec"}
	if resumeID != "" {
		args = append(args, "resume", resumeID)
	}
	args = append(args,
		"--json",
		"--skip-git-repo-check",
		"--ignore-user-config",
		"--ignore-rules",
	)
	// The profile is the model's base instructions (model_instructions_file). Sending it again as
	// developer_instructions would double ~2k tokens on every model call, so it is only the fallback.
	if baseInstructionsFile == "" {
		args = append(args, "-c", "developer_instructions="+strconv.Quote(instructions))
	}
	args = append(args, codexPolicyArgsForMode(mcpURL, baseInstructionsFile, toolTimeout, devMode)...)
	return append(args, "-")
}

// codexLeanArgs switch off Codex features DezuxkStudio never uses. Each one adds tool definitions or
// instructions to every model call (multi-agent tools and role prompts alone are ~9k characters), which
// costs the user's quota on every turn. view_image is also off because it would read images outside
// DezuxkStudio's access policy; image files are opened through workspace_read_file instead.
var codexLeanArgs = []string{
	"-c", "agents.enabled=false",
	"-c", "skills.include_instructions=false",
	"-c", "features.multi_agent=false",
	"-c", "features.multi_agent_v2=false",
	"-c", "features.goals=false",
	"-c", "features.plugins=false",
	"-c", "features.hooks=false",
	"-c", "features.skill_search=false",
	"-c", "features.tool_suggest=false",
	"-c", "features.image_generation=false",
	"-c", "features.browser_use=false",
	"-c", "features.computer_use=false",
	"-c", "features.sleep_tool=false",
	"-c", "features.view_image=false",
	// GPT-6 models default to low verbosity, which clips explanations and lists; length is governed by
	// the policy instead ("match the length to the request").
	"-c", `model_verbosity="medium"`,
}

// codexPolicyArgs are the security settings shared by `codex exec` and `codex app-server`: read-only
// sandbox, no approvals, no shell or apps, and DezuxkStudio's MCP server as the only tool source.
//
// DezuxkStudio's MCP tools are pre-approved inside Codex (default_tools_approval_mode="approve"):
// DezuxkStudio enforces its own policy and approval queue for every tool (destructive actions wait
// for Approve in the app's panel). Without this, Codex asks for approval of each MCP call, which this
// headless session can never grant, and the model answers that it "cannot access the tool".
// codexPolicyArgs pins Codex to DezuxkStudio's policy. toolTimeout raises Codex's MCP tool call
// limit (60s by default) so a workflow run through a tool is not abandoned while it still runs.
func codexPolicyArgs(mcpURL, baseInstructionsFile string, toolTimeout time.Duration) []string {
	return codexPolicyArgsForMode(mcpURL, baseInstructionsFile, toolTimeout, false)
}

func codexPolicyArgsForMode(mcpURL, baseInstructionsFile string, toolTimeout time.Duration, devMode bool) []string {
	sandbox := "read-only"
	shellTool := "false"
	if devMode {
		sandbox = "workspace-write"
		shellTool = "true"
	}
	args := []string{
		"-c", "sandbox_mode=" + strconv.Quote(sandbox),
		"-c", `approval_policy="never"`,
		"-c", "features.shell_tool=" + shellTool,
		"-c", "features.apps=false",
		"-c", "mcp_servers." + mcpServerName + ".url=" + strconv.Quote(mcpURL),
		"-c", "mcp_servers." + mcpServerName + ".bearer_token_env_var=" + strconv.Quote(mcpTokenEnvVar),
		"-c", "mcp_servers." + mcpServerName + `.default_tools_approval_mode="approve"`,
	}
	if toolTimeout > 0 {
		args = append(args, "-c", "mcp_servers."+mcpServerName+".tool_timeout_sec="+strconv.FormatInt(int64(toolTimeout.Seconds()), 10))
	}
	if devMode {
		args = append(args, "-c", "features.view_image=true", "-c", `model_verbosity="medium"`)
	} else {
		args = append(args, codexLeanArgs...)
	}
	if baseInstructionsFile != "" {
		args = append(args, "-c", "model_instructions_file="+strconv.Quote(baseInstructionsFile))
	}
	return args
}

type streamJSONAccumulator interface {
	handleLine(raw string)
	assistantOutput() string
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

	instructions := s.systemPrompt(ctx, domainagent.ProviderCodex)
	baseFile, err := writeCodexBaseInstructions(profileRoot, instructions)
	if err != nil {
		return err
	}
	resumeID := s.cliSessionFor(domainagent.ProviderCodex)
	args := buildCodexExecArgsForMode(mcpURL, instructions, baseFile, resumeID, s.limits.MCPToolTimeout, s.developerMode)
	if modelArgs := codexModelArgs(s.effectiveSelection(domainagent.ProviderCodex)); len(modelArgs) > 0 {
		args = append(args[:len(args)-1:len(args)-1], append(modelArgs, "-")...) // keep "-" (stdin prompt) last
	}
	env := providerEnv(domainagent.ProviderCodex, envMap)
	acc := &codexStreamAccumulator{
		providerID: domainagent.ProviderCodex,
		messageID:  messageID,
		emit:       s.emitChat,
	}
	err = runStreamJSONCommand(
		ctx,
		"codex",
		executable,
		args,
		workspaceDir,
		env,
		prompt,
		"check isolated Codex login",
		acc,
	)
	if err == nil && acc.threadID != "" {
		s.setCLISession(domainagent.ProviderCodex, acc.threadID)
	}
	return err
}

// Output limits of a per-turn CLI run. A stream-json line can carry a whole tool result, so stdout lines
// may be large; stderr is only kept as the error text, so a short tail is enough.
const (
	cliStdoutLineMax = 32 << 20
	cliStderrLineMax = 256 << 10
	cliStderrKeep    = 8 << 10
)

// runStreamJSONCommand runs one CLI turn whose stdout is a JSON-lines event stream; acc turns the lines
// into chat events (which reach the turn through emitChat).
func runStreamJSONCommand(
	ctx context.Context,
	label string,
	executable string,
	args []string,
	workDir string,
	env []string,
	prompt string,
	emptyHint string,
	acc streamJSONAccumulator,
) error {
	cmd := proc.Command(ctx, executable, args...)
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
	safego.Go(label+".stderr", func() {
		defer close(stderrDone)
		sc := bufio.NewScanner(stderr)
		sc.Buffer(make([]byte, 4096), cliStderrLineMax)
		for sc.Scan() {
			line := redactSecrets(sc.Text())
			if stderrBuf.Len() < cliStderrKeep {
				stderrBuf.WriteString(line)
				stderrBuf.WriteByte('\n')
			}
		}
	})

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), cliStdoutLineMax)
	for scanner.Scan() {
		acc.handleLine(scanner.Text())
	}

	scanErr := scanner.Err()
	<-stderrDone
	waitErr := cmd.Wait()

	assistantText := acc.assistantOutput()
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
