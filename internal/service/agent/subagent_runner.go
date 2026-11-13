package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	domainagent "app/internal/domain/agent"
	"github.com/google/uuid"
)

var subagentDefaultReadOnlyTools = []string{
	"skill_read",
	"workspace_list_files",
	"workspace_read_file",
	"workspace_read_document",
	"workspace_search_text",
	"studio_list_capabilities",
	"workflow_list_specs",
	"binaries_list",
}

func (s *Service) effectiveSubagentTools(requested []string) []string {
	availSlice := s.availableSubagentTools()
	avail := make(map[string]bool, len(availSlice))
	for _, t := range availSlice {
		avail[t] = true
	}
	source := requested
	if len(source) == 0 {
		source = subagentDefaultReadOnlyTools
	}
	seen := make(map[string]bool, len(source))
	out := make([]string, 0, len(source))
	for _, name := range source {
		name = strings.TrimSpace(name)
		if name == "" || name == "subagent_run" || !avail[name] || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out
}

func (s *Service) runSubagentTool(ctx context.Context, in subagentRunToolInput) (map[string]any, error) {
	if s.customizations == nil {
		return nil, fmt.Errorf("customizations storage is unavailable")
	}
	name := strings.TrimSpace(strings.ToLower(in.Name))
	if name == "" {
		return nil, fmt.Errorf("subagent name is required")
	}
	task := strings.TrimSpace(in.Task)
	if task == "" {
		return nil, fmt.Errorf("subagent task is required")
	}

	spec, boundSkills, err := s.customizations.ResolveSubagent(name)
	if err != nil {
		return nil, err
	}
	effectiveTools := s.effectiveSubagentTools(spec.Tools)
	if len(effectiveTools) == 0 {
		return nil, fmt.Errorf("subagent %q has no valid allowed tools", spec.Name)
	}

	parentTurn := s.turnFor("")
	subCtx, cancelSub := context.WithCancel(ctx)
	defer cancelSub()
	if parentTurn != nil && parentTurn.ctx != nil {
		stopParentWatch := context.AfterFunc(parentTurn.ctx, cancelSub)
		defer stopParentWatch()
	}

	if s.subagentSem != nil {
		select {
		case s.subagentSem <- struct{}{}:
			defer func() { <-s.subagentSem }()
		case <-subCtx.Done():
			return nil, subCtx.Err()
		}
	}

	targetProvider, resolvedExe, modelSel, err := s.resolveSubagentExecutionTarget(subCtx, parentTurn, spec)
	if err != nil {
		return nil, err
	}

	workDir, err := s.ensureWorkspaceDir()
	if err != nil {
		return nil, err
	}
	mcpSrv, err := s.ensureMCPServer()
	if err != nil {
		return nil, err
	}

	runID := "sub-" + uuid.NewString()
	turnID := ""
	if parentTurn != nil {
		turnID = parentTurn.id
	}

	scopedToken, err := mcpSrv.registerScopedSession(effectiveTools, subagentRunMarker{
		ID:       runID,
		Name:     spec.Name,
		Provider: targetProvider,
		TurnID:   turnID,
	})
	if err != nil {
		return nil, err
	}
	defer mcpSrv.unregisterScopedSession(scopedToken)

	sysPrompt := s.buildSubagentSystemPrompt(subCtx, spec, boundSkills, effectiveTools)
	userBrief := task
	if extra := strings.TrimSpace(in.Context); extra != "" {
		userBrief = "<parent_context>\n" + extra + "\n</parent_context>\n\n<task>\n" + task + "\n</task>"
	}

	guard := newTurnOutputGuard(runID)
	if s.developerMode {
		guard.disable()
	}

	emitParent := func(evt domainagent.ChatEvent) {
		evt.ProviderID = targetProvider
		if parentTurn != nil {
			evt.MessageID = parentTurn.id
		}
		evt.SubagentID = runID
		evt.SubagentName = spec.Name
		evt.SubagentProvider = targetProvider
		s.emitChat(evt)
	}

	emitParent(domainagent.ChatEvent{
		Type: domainagent.ChatEventSubagentStart,
		Text: task,
	})

	subEmit := func(ev domainagent.ChatEvent) {
		switch ev.Type {
		case domainagent.ChatEventDelta:
			safe := guard.filterText(ev.Text)
			if safe != "" {
				emitParent(domainagent.ChatEvent{
					Type: domainagent.ChatEventSubagentDelta,
					Text: safe,
				})
			}
		case domainagent.ChatEventToolCall:
			emitParent(domainagent.ChatEvent{
				Type: domainagent.ChatEventSubagentToolCall,
				Tool: ev.Tool,
				Text: ev.Text,
			})
		case domainagent.ChatEventToolResult:
			emitParent(domainagent.ChatEvent{
				Type:  domainagent.ChatEventSubagentToolResult,
				Tool:  ev.Tool,
				Text:  ev.Text,
				Error: ev.Error,
			})
		}
	}

	var runErr error
	switch targetProvider {
	case domainagent.ProviderClaude:
		runErr = s.runClaudeSubagent(subCtx, resolvedExe, workDir, mcpSrv.url, scopedToken, sysPrompt, userBrief, effectiveTools, modelSel, subEmit)
	case domainagent.ProviderCodex:
		runErr = s.runCodexSubagent(subCtx, runID, resolvedExe, workDir, mcpSrv.url, scopedToken, sysPrompt, userBrief, modelSel, subEmit)
	case domainagent.ProviderAntigravity:
		runErr = s.runAntigravitySubagent(subCtx, runID, resolvedExe, workDir, mcpSrv.url, scopedToken, sysPrompt, userBrief, effectiveTools, modelSel, subEmit)
	default:
		runErr = fmt.Errorf("subagent provider %q is unsupported", targetProvider)
	}

	tail, _ := guard.finalize()
	if tail != "" && runErr == nil {
		emitParent(domainagent.ChatEvent{
			Type: domainagent.ChatEventSubagentDelta,
			Text: tail,
		})
	}
	if guard.blocked() {
		s.log.Warn("agent: output guard blocked source code in subagent reply", "subagent", spec.Name, "provider", targetProvider, "run_id", runID)
	}

	emitParent(domainagent.ChatEvent{
		Type:  domainagent.ChatEventSubagentDone,
		Error: errText(runErr),
	})

	if runErr != nil {
		return nil, runErr
	}
	return map[string]any{
		"ok":       true,
		"subagent": spec.Name,
		"provider": targetProvider,
		"model":    modelSel.Model,
		"tools":    effectiveTools,
		"output":   guard.sanitizedText(),
	}, nil
}

func (s *Service) resolveSubagentExecutionTarget(
	ctx context.Context,
	parentTurn *turn,
	spec domainagent.SubagentInfo,
) (domainagent.ProviderID, string, domainagent.ModelSelection, error) {
	s.mu.Lock()
	rootProvider := s.activeID
	s.mu.Unlock()
	if parentTurn != nil && parentTurn.providerID != "" {
		rootProvider = parentTurn.providerID
	}
	if rootProvider == "" {
		return "", "", domainagent.ModelSelection{}, fmt.Errorf("no active root provider available for subagent")
	}

	targetProvider := rootProvider
	fellBackFromOverride := false
	var targetExe string

	if override := spec.Provider; override != "" && override != rootProvider {
		ok := false
		if domainagent.ValidateProviderID(override) == nil {
			pSpec := domainagent.Registry[override]
			if pSpec.Supported {
				exe, _, _, resErr := s.resolveExecutable(ctx, pSpec)
				if resErr == nil && exe != "" {
					authState, _ := s.probeProviderAuth(ctx, override, pSpec, exe)
					if authState == domainagent.AuthAuthenticated {
						targetProvider = override
						targetExe = exe
						ok = true
					}
				}
			}
		}
		if !ok {
			fellBackFromOverride = true
			s.log.Warn("agent: subagent override provider unavailable or unauthenticated; falling back to root provider",
				"subagent", spec.Name, "requested_provider", override, "fallback_provider", rootProvider)
		}
	}

	if targetExe == "" {
		pSpec := domainagent.Registry[targetProvider]
		exe, _, _, resErr := s.resolveExecutable(ctx, pSpec)
		if resErr != nil || exe == "" {
			return "", "", domainagent.ModelSelection{}, fmt.Errorf("provider %s executable is not available for subagent: %v", targetProvider, resErr)
		}
		targetExe = exe
	}

	sel := s.effectiveSelection(targetProvider)
	requestedModel := strings.TrimSpace(spec.Model)
	if requestedModel != "" && !fellBackFromOverride {
		if modelMatchesProvider(targetProvider, requestedModel) {
			sel = domainagent.ModelSelection{Model: requestedModel}
		} else {
			s.log.Warn("agent: subagent model override incompatible with provider; falling back to active model",
				"subagent", spec.Name, "provider", targetProvider, "requested_model", requestedModel)
		}
	}
	return targetProvider, targetExe, sel, nil
}

func modelMatchesProvider(id domainagent.ProviderID, model string) bool {
	lower := strings.ToLower(strings.TrimSpace(model))
	if lower == "" {
		return true
	}
	switch id {
	case domainagent.ProviderClaude:
		return !strings.HasPrefix(lower, "gpt-") && !strings.HasPrefix(lower, "o1") && !strings.HasPrefix(lower, "o3") && !strings.HasPrefix(lower, "o4") && !strings.HasPrefix(lower, "codex") && !strings.HasPrefix(lower, "gemini-")
	case domainagent.ProviderCodex:
		return !strings.HasPrefix(lower, "claude-") && !strings.HasPrefix(lower, "gemini-")
	case domainagent.ProviderAntigravity:
		return !strings.HasPrefix(lower, "gpt-") && !strings.HasPrefix(lower, "claude-") && !strings.HasPrefix(lower, "o1") && !strings.HasPrefix(lower, "o3") && !strings.HasPrefix(lower, "codex")
	default:
		return false
	}
}

func (s *Service) buildSubagentSystemPrompt(
	ctx context.Context,
	spec domainagent.SubagentInfo,
	boundSkills []domainagent.SkillInfo,
	effectiveTools []string,
) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "You are an isolated DezuxkStudio specialist subagent (%s).\n", spec.Name)
	sb.WriteString("Complete ONLY the delegated task assigned by the parent coordinator and return clear, concise, factual results.\n")
	if !s.developerMode {
		sb.WriteString("Assistant Mode Rule: Never output programming source code or fenced code blocks in your final answer.\n")
	}
	sb.WriteString("Security & Approval Rule: You can only call your scoped DezuxkStudio MCP tools. You cannot call subagent_run. If any tool returns status \"approval_blocked_in_subagent\", do NOT retry or claim it ran — include the exact recommended action and parameters in your report to the parent coordinator.\n")

	if s.preferences != nil {
		if prefs, err := s.preferences.Get(ctx); err == nil && prefs.Language != "" {
			fmt.Fprintf(&sb, "\n<application>\ninterface_language: %s\n", prefs.Language)
			if s.workspaceDir != "" {
				fmt.Fprintf(&sb, "workspace_dir: %s\n", s.workspaceDir)
			}
			sb.WriteString("</application>")
		}
	}

	if body := strings.TrimSpace(spec.Body); body != "" {
		sb.WriteString("\n\n<subagent_instructions>\n")
		sb.WriteString(body)
		sb.WriteString("\n</subagent_instructions>")
	}

	for _, sk := range boundSkills {
		if strings.TrimSpace(sk.Body) == "" {
			continue
		}
		fmt.Fprintf(&sb, "\n\n<skill name=%q>\n%s\n</skill>", sk.Name, strings.TrimSpace(sk.Body))
	}

	if len(effectiveTools) > 0 {
		sb.WriteString("\n\n<allowed_tools>\n")
		for _, toolName := range effectiveTools {
			sb.WriteString("- ")
			sb.WriteString(toolName)
			sb.WriteByte('\n')
		}
		sb.WriteString("</allowed_tools>")
	}
	return sb.String()
}

func (s *Service) runClaudeSubagent(
	ctx context.Context,
	executable, workDir, mcpURL, mcpToken, sysPrompt, userBrief string,
	effectiveTools []string,
	sel domainagent.ModelSelection,
	emit func(domainagent.ChatEvent),
) error {
	profileRoot := s.profileDirFor(domainagent.ProviderClaude)
	env, err := s.secrets.claudeIsolatedEnv(profileRoot)
	if err != nil {
		return err
	}
	subDir := filepath.Join(profileRoot, "subagents")
	if err := os.MkdirAll(subDir, 0o700); err != nil {
		return fmt.Errorf("claude: create subagent dir: %w", err)
	}
	mcpCfgPath, cleanup, err := writeClaudeMCPConfig(subDir, mcpURL, mcpToken)
	if err != nil {
		return err
	}
	defer cleanup()
	env = append(env, claudeMCPTimeoutEnv(s.limits.MCPToolTimeout)...)

	args := buildClaudeExecArgsWithTools(mcpCfgPath, sysPrompt, effectiveTools)
	args = append(args, claudeModelArgs(sel)...)
	args = append(args, "--session-id", uuid.NewString())

	acc := &claudeStreamAccumulator{
		providerID: domainagent.ProviderClaude,
		messageID:  "",
		emit:       emit,
	}
	return runStreamJSONCommand(
		ctx,
		"claude-subagent",
		executable,
		args,
		workDir,
		env,
		userBrief,
		"check isolated Claude login",
		acc,
	)
}

func (s *Service) runCodexSubagent(
	ctx context.Context,
	runID, executable, workDir, mcpURL, mcpToken, sysPrompt, userBrief string,
	sel domainagent.ModelSelection,
	emit func(domainagent.ChatEvent),
) error {
	profileRoot := s.profileDirFor(domainagent.ProviderCodex)
	envMap, err := prepareCodexProfile(profileRoot)
	if err != nil {
		return err
	}
	envMap[mcpTokenEnvVar] = mcpToken

	instrFile, cleanup, err := writeCodexEphemeralInstructions(profileRoot, runID, sysPrompt)
	if err != nil {
		return err
	}
	defer cleanup()

	args := buildCodexExecArgsForSubagent(mcpURL, sysPrompt, instrFile, s.limits.MCPToolTimeout)
	if modelArgs := codexModelArgs(sel); len(modelArgs) > 0 {
		args = append(args[:len(args)-1:len(args)-1], append(modelArgs, "-")...)
	}
	env := providerEnv(domainagent.ProviderCodex, envMap)
	acc := &codexStreamAccumulator{
		providerID: domainagent.ProviderCodex,
		messageID:  "",
		emit:       emit,
	}
	return runStreamJSONCommand(
		ctx,
		"codex-subagent",
		executable,
		args,
		workDir,
		env,
		userBrief,
		"check isolated Codex login",
		acc,
	)
}

func (s *Service) runAntigravitySubagent(
	ctx context.Context,
	runID, executable, workDir, mcpURL, mcpToken, sysPrompt, userBrief string,
	effectiveTools []string,
	sel domainagent.ModelSelection,
	emit func(domainagent.ChatEvent),
) error {
	parentRoot := s.profileDirFor(domainagent.ProviderAntigravity)
	subRoot := filepath.Join(parentRoot, "subagents", runID)
	agyEnvMap, cleanup, err := s.secrets.prepareAgySubProfile(parentRoot, subRoot, mcpURL, mcpToken)
	if err != nil {
		return err
	}
	defer cleanup()

	spec := domainagent.Registry[domainagent.ProviderAntigravity]
	procEnv := providerEnv(domainagent.ProviderAntigravity, agyEnvMap)
	var titlesMu sync.Mutex
	acpTitles := make(map[string]string)

	onEvent := func(upd SessionUpdate) {
		switch upd.Kind {
		case "agent_message_chunk":
			if upd.Text != "" && emit != nil {
				emit(domainagent.ChatEvent{Type: domainagent.ChatEventDelta, Text: upd.Text})
			}
		case "tool_call":
			title := upd.Tool
			if upd.ToolCallID != "" && title != "" {
				titlesMu.Lock()
				acpTitles[upd.ToolCallID] = title
				titlesMu.Unlock()
			}
			if title != "" && emit != nil {
				emit(domainagent.ChatEvent{Type: domainagent.ChatEventToolCall, Tool: title, Text: title})
			}
		case "tool_call_update":
			title := upd.Tool
			if upd.ToolCallID != "" {
				titlesMu.Lock()
				if known, ok := acpTitles[upd.ToolCallID]; ok && known != "" {
					title = known
				}
				if upd.Status == toolStatusCompleted || upd.Status == toolStatusFailed {
					delete(acpTitles, upd.ToolCallID)
				}
				titlesMu.Unlock()
			}
			if title != "" && emit != nil && (upd.Status == toolStatusCompleted || upd.Status == toolStatusFailed) {
				errTxt := ""
				if upd.Status == toolStatusFailed {
					errTxt = toolStatusFailed
				}
				emit(domainagent.ChatEvent{Type: domainagent.ChatEventToolResult, Tool: title, Text: upd.Status, Error: errTxt})
			}
		}
	}

	transport, err := s.spawnSubagentTransport(
		ctx,
		executable,
		s.acpArgsFor(spec),
		workDir,
		procEnv,
		effectiveTools,
		onEvent,
		nil,
	)
	if err != nil {
		return err
	}
	defer func() { _ = transport.Close() }()

	initCtx, initCancel := context.WithTimeout(ctx, s.limits.InitializeTimeout)
	defer initCancel()
	if _, err := transport.Initialize(initCtx, clientName, s.appVersion); err != nil {
		return classifyProviderError(err)
	}
	sessID, err := transport.NewSession(ctx, workDir, sysPrompt)
	if err != nil {
		return err
	}
	if sel.Model != "" {
		availModels, _ := transport.AvailableModels()
		if len(availModels) == 0 {
			_ = transport.SetModel(ctx, sessID, sel.Model)
		} else if _, ok := findModel(availModels, sel.Model); ok {
			_ = transport.SetModel(ctx, sessID, sel.Model)
		}
	}
	return transport.Prompt(ctx, sessID, userBrief)
}
