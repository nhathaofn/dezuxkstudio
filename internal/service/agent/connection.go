package agent

// Connection lifecycle: connect, restore, disconnect, and the events of a live ACP session.

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"app/internal/apperr"
	domainagent "app/internal/domain/agent"
	"app/internal/progress"
)

func (s *Service) acpArgsFor(spec domainagent.ProviderSpec) []string {
	if s.developerMode && spec.ID == domainagent.ProviderAntigravity {
		return []string{"acp", "--include-default-customizations=true"}
	}
	return spec.ACPArgs
}

// Connect starts the local MCP server and activates the selected isolated provider.
func (s *Service) Connect(ctx context.Context, id domainagent.ProviderID) (domainagent.ProviderInfo, error) {
	if err := domainagent.ValidateProviderID(id); err != nil {
		return domainagent.ProviderInfo{}, err
	}
	// Spawn + handshake are bounded by agent.initialize_timeout / auth and version probe timeouts.
	defer progress.KeepAlive(ctx)()
	spec := domainagent.Registry[id]
	if !spec.Supported {
		return domainagent.ProviderInfo{}, apperr.Validation(spec.UnsupportedReason)
	}
	resolved, source, customPath, err := s.resolveExecutable(ctx, spec)
	if err != nil {
		return domainagent.ProviderInfo{}, err
	}
	if resolved == "" {
		s.mu.Lock()
		s.states[id] = domainagent.StateMissing
		s.lastErrors[id] = localize(fmt.Errorf("executable %q not found", spec.ExecutableLabel), msgExecutableNotFound, map[string]string{"name": spec.Name})
		info := s.buildInfoLocked(spec, "", source, customPath, "")
		s.mu.Unlock()
		s.emitStatus(info)
		return info, userError(apperr.CodeNotFound, msgExecutableNotFound, fmt.Sprintf("%s executable not found on PATH or custom path", spec.Name), map[string]string{"name": spec.Name})
	}

	if err := s.Disconnect(ctx); err != nil {
		return domainagent.ProviderInfo{}, err
	}
	// A concurrent Connect/Disconnect after this point bumps connSeq and supersedes this attempt.
	startToken := s.connSeq.Load()
	// An explicit connect always re-checks credentials instead of trusting the status cache.
	s.mu.Lock()
	delete(s.authProbes, id)
	s.mu.Unlock()

	workDir, err := s.ensureWorkspaceDir()
	if err != nil {
		return domainagent.ProviderInfo{}, apperr.Unavailable(err.Error())
	}

	mcpSrv, err := s.ensureMCPServer()
	if err != nil {
		return domainagent.ProviderInfo{}, apperr.Unavailable(err.Error())
	}

	if td, ok := s.turnDriverFor(id); ok {
		return s.connectPerTurnCLI(ctx, startToken, id, td, spec, resolved, source, customPath)
	}
	sd, ok := s.driver(id).(sessionDriver)
	if !ok {
		return domainagent.ProviderInfo{}, apperr.Validation(fmt.Sprintf("%s cannot be connected", spec.Name))
	}

	// The isolated profile must be signed in before the ACP process is spawned.
	authState, accountInfo := sd.probeAuth(ctx, resolved)
	s.mu.Lock()
	s.authStates[id] = authState
	s.accountInfos[id] = accountInfo
	s.mu.Unlock()
	if authState != domainagent.AuthAuthenticated {
		err := localize(fmt.Errorf("isolated %s profile is not signed in; click Sign in or enter an API key first", spec.Name), msgNotSignedIn, map[string]string{"name": spec.Name})
		return s.failConnect(id, spec, resolved, source, customPath, domainagent.StateUnauthenticated, err), toUserError(apperr.CodeUnauthorized, err)
	}

	connToken := s.connSeq.Add(1)
	s.mu.Lock()
	s.activeID = id
	s.states[id] = domainagent.StateConnecting
	delete(s.lastErrors, id)
	connectingInfo := s.buildInfoLocked(spec, resolved, source, customPath, s.cachedVersionLocked(id))
	s.mu.Unlock()
	s.emitStatus(connectingInfo)

	procEnv, err := sd.sessionEnv(mcpSrv.url, mcpSrv.token)
	if err != nil {
		return s.failConnect(id, spec, resolved, source, customPath, domainagent.StateError, err), apperr.Unavailable(err.Error())
	}

	transport, err := s.spawnTransport(
		ctx,
		resolved,
		s.acpArgsFor(spec),
		workDir,
		procEnv,
		func(upd SessionUpdate) { s.handleSessionUpdate(connToken, upd) },
		func(exitErr error) { s.handleProcessExit(connToken, id, exitErr) },
	)
	if err != nil {
		return s.failConnect(id, spec, resolved, source, customPath, domainagent.StateError, err), apperr.Unavailable(err.Error())
	}

	initCtx, cancel := context.WithTimeout(ctx, s.limits.InitializeTimeout)
	defer cancel()
	initRes, err := transport.Initialize(initCtx, clientName, s.appVersion)
	if err != nil {
		err = classifyProviderError(err)
		_ = transport.Close()
		state := domainagent.StateError
		if isAuthError(err) {
			state = domainagent.StateUnauthenticated
		}
		return s.failConnect(id, spec, resolved, source, customPath, state, err), apperr.Unavailable(err.Error())
	}

	// Open the session right away so the provider's model list (and the user's model choice) is ready
	// before the first message; a failure here is not fatal, the first turn retries lazily.
	eagerSessionID, sessErr := transport.NewSession(initCtx, workDir, s.systemPrompt(initCtx, id))
	if sessErr != nil {
		s.log.Warn("agent: eager session/new failed", "provider", id, "error", sessErr)
		eagerSessionID = ""
	} else {
		s.applyACPSessionModels(initCtx, id, transport, eagerSessionID)
	}

	s.mu.Lock()
	if s.connSeq.Load() != connToken || s.closing {
		s.mu.Unlock()
		_ = transport.Close()
		return domainagent.ProviderInfo{}, userError(apperr.CodeConflict, msgSuperseded, "connection attempt was superseded", nil)
	}
	version := s.cachedVersionLocked(id)
	if version == "" && initRes.AgentInfo.Version != "" {
		version = initRes.AgentInfo.Version
	}
	s.activeClient = transport
	s.activeInit = initRes
	s.activeVersion = version
	s.activeSessionID = eagerSessionID
	s.sessionPrimed = false
	s.states[id] = domainagent.StateConnected
	s.authStates[id] = domainagent.AuthAuthenticated
	delete(s.lastErrors, id)
	info := s.buildInfoLocked(spec, resolved, source, customPath, version)
	s.mu.Unlock()

	s.emitStatus(info)
	s.rememberConnection(ctx, id)
	return info, nil
}

// rememberConnection stores the provider the user left connected so the next app start reconnects it
// (see RestoreConnection). Every provider kind (ACP and per-turn CLI) must go through here.
func (s *Service) rememberConnection(ctx context.Context, id domainagent.ProviderID) {
	if err := s.repo.SetRememberedConnection(context.WithoutCancel(ctx), id); err != nil {
		s.log.Warn("agent: remember connection", "error", err)
	}
}

func (s *Service) connectPerTurnCLI(
	ctx context.Context,
	startToken int64,
	id domainagent.ProviderID,
	drv turnDriver,
	spec domainagent.ProviderSpec,
	resolved string,
	source domainagent.Source,
	customPath string,
) (domainagent.ProviderInfo, error) {
	authState, accountInfo := s.probeProviderAuth(ctx, id, spec, resolved)
	s.mu.Lock()
	s.authStates[id] = authState
	s.accountInfos[id] = accountInfo
	s.mu.Unlock()
	if authState != domainagent.AuthAuthenticated {
		err := localize(fmt.Errorf("isolated %s profile is not signed in; click Sign in first", spec.Name), msgNotSignedIn, map[string]string{"name": spec.Name})
		return s.failConnect(id, spec, resolved, source, customPath, domainagent.StateUnauthenticated, err), toUserError(apperr.CodeUnauthorized, err)
	}
	if prepErr := drv.prepare(); prepErr != nil {
		return s.failConnect(id, spec, resolved, source, customPath, domainagent.StateError, prepErr), apperr.Unavailable(prepErr.Error())
	}
	version := s.probeVersionCached(ctx, id, resolved, spec.VersionArgs)
	s.mu.Lock()
	if s.closing {
		s.mu.Unlock()
		return domainagent.ProviderInfo{}, apperr.Unavailable("agent service is closing")
	}
	if s.connSeq.Load() != startToken {
		s.mu.Unlock()
		return domainagent.ProviderInfo{}, userError(apperr.CodeConflict, msgSuperseded, "connection attempt was superseded", nil)
	}
	s.connSeq.Add(1)
	s.activeID = id
	s.activeClient = nil
	s.activeSessionID = ""
	s.activeInit = nil
	s.activeVersion = version
	s.states[id] = domainagent.StateConnected
	delete(s.lastErrors, id)
	info := s.buildInfoLocked(spec, resolved, source, customPath, version)
	s.mu.Unlock()
	s.emitStatus(info)
	// The model list of a per-turn CLI is read from the CLI itself. Start that now (in the background,
	// and push the status when it arrives) so the picker is filled without the user pressing "Check".
	s.refreshModelCatalogAsync(id, resolved)
	s.rememberConnection(ctx, id)
	return info, nil
}

// DisconnectByUser ends the connection the user chose to close: unlike app shutdown or a reconnect, the
// provider is no longer reconnected at the next start.
func (s *Service) DisconnectByUser(ctx context.Context) error {
	if err := s.Disconnect(ctx); err != nil {
		return err
	}
	return s.repo.SetRememberedConnection(ctx, "")
}

// RestoreConnection reconnects the provider the user left connected when the app last ran. If it can no
// longer connect (CLI removed, sign-in expired...), the provider is forgotten and its error state shown,
// so the user reconnects by hand once instead of on every start.
func (s *Service) RestoreConnection(ctx context.Context) {
	id, err := s.repo.RememberedConnection(ctx)
	if err != nil || id == "" {
		return
	}
	if domainagent.ValidateProviderID(id) != nil {
		_ = s.repo.SetRememberedConnection(ctx, "")
		return
	}
	if _, err := s.Connect(ctx, id); err != nil {
		if ctx.Err() != nil {
			return // app is closing: keep it for the next start
		}
		s.log.Warn("agent: restore connection failed", "provider", id, "error", err)
		_ = s.repo.SetRememberedConnection(context.WithoutCancel(ctx), "")
	}
}

// Disconnect terminates the currently active agent connection, if any. A running turn is cancelled; it
// ends (and keeps its partial reply) through its own goroutine.
func (s *Service) Disconnect(ctx context.Context) error {
	s.mu.Lock()
	client := s.activeClient
	prevID := s.activeID
	t := s.turn
	s.connSeq.Add(1)
	s.clearConnectionLocked()
	if prevID != "" {
		s.states[prevID] = domainagent.StateDisconnected
		delete(s.lastErrors, prevID)
	}
	s.mu.Unlock()

	if t != nil {
		t.cancel()
	}
	if client != nil {
		_ = client.Close()
	}
	if prevID != "" {
		s.driver(prevID).release()
		// Pending actions belong to the session that proposed them.
		if s.approvals.clearProvider(prevID) {
			s.emitApprovals()
		}
		s.emitStatusOf(ctx, prevID)
	}
	return nil
}

// clearConnectionLocked forgets the active connection and its running turn. Caller holds s.mu.
func (s *Service) clearConnectionLocked() {
	s.activeClient = nil
	s.activeID = ""
	s.activeSessionID = ""
	s.activeInit = nil
	s.activeVersion = ""
	s.turn = nil
}

func (s *Service) handleSessionUpdate(connToken int64, upd SessionUpdate) {
	s.mu.Lock()
	t := s.turn
	current := s.connSeq.Load() == connToken
	s.mu.Unlock()
	if !current || t == nil {
		return // updates outside a turn (or of a replaced connection) are not shown
	}
	evt := domainagent.ChatEvent{ProviderID: t.providerID, MessageID: t.id}
	switch upd.Kind {
	case "agent_message_chunk":
		evt.Type, evt.Text = domainagent.ChatEventDelta, upd.Text
		s.emitChat(evt)
	case "agent_thought_chunk", "thought_message_chunk":
		evt.Type, evt.Text = domainagent.ChatEventThought, upd.Text
		s.emitChat(evt)
	case "tool_call", "tool_call_update":
		evt.Tool = t.trackACPTool(upd)
		if evt.Tool == "" {
			return
		}
		if upd.Kind == "tool_call" {
			evt.Type, evt.Text = domainagent.ChatEventToolCall, evt.Tool
			s.emitChat(evt)
		}
		// Some agents report a tool that already finished in the tool_call itself (no update follows).
		if upd.Status == toolStatusCompleted || upd.Status == toolStatusFailed {
			evt.Type, evt.Text = domainagent.ChatEventToolResult, upd.Status
			if upd.Status == toolStatusFailed {
				evt.Error = toolStatusFailed // the UI reads failure from Error, never from the result text
			}
			s.emitChat(evt)
		}
	}
}

// handleProcessExit handles an ACP agent process that exited on its own: the connection is dropped and a
// running turn ends right away with the exit as its (stored, translatable) error.
func (s *Service) handleProcessExit(connToken int64, id domainagent.ProviderID, exitErr error) {
	s.mu.Lock()
	if s.connSeq.Load() != connToken || s.closing {
		s.mu.Unlock()
		return
	}
	t := s.turn
	s.clearConnectionLocked()
	var turnErr error
	if exitErr != nil {
		turnErr = localize(fmt.Errorf("agent process exited: %w", exitErr), msgProcessExited, map[string]string{"detail": exitErr.Error()})
		s.states[id] = domainagent.StateError
		s.lastErrors[id] = turnErr
	} else {
		turnErr = localize(errors.New("agent connection closed unexpectedly"), msgConnectionClosed, nil)
		s.states[id] = domainagent.StateDisconnected
		delete(s.lastErrors, id)
	}
	s.mu.Unlock()

	if t != nil {
		s.endTurn(t, turnErr)
		t.cancel()
	}
	if s.approvals.clearProvider(id) {
		s.emitApprovals()
	}
	s.emitStatusOf(context.Background(), id)
}

func (s *Service) failConnect(id domainagent.ProviderID, spec domainagent.ProviderSpec, resolved string, source domainagent.Source, customPath string, state domainagent.ConnectionState, err error) domainagent.ProviderInfo {
	s.mu.Lock()
	s.connSeq.Add(1)
	if s.activeID == id {
		s.clearConnectionLocked()
	}
	if isAuthError(err) || state == domainagent.StateUnauthenticated {
		s.states[id] = domainagent.StateUnauthenticated
		s.authStates[id] = domainagent.AuthUnauthenticated
		delete(s.authProbes, id)
	} else {
		s.states[id] = state
	}
	s.lastErrors[id] = err
	info := s.buildInfoLocked(spec, resolved, source, customPath, s.cachedVersionLocked(id))
	s.mu.Unlock()
	s.emitStatus(info)
	return info
}

// pickAuthMethod chooses the ACP sign-in method: the provider's preferred IDs first, as long as the
// agent advertises them, otherwise the first advertised OAuth-style method.
func pickAuthMethod(spec domainagent.ProviderSpec, init *InitializeResult) (string, error) {
	var advertised []string
	if init != nil {
		for _, m := range init.AuthMethods {
			if m.ID != "" {
				advertised = append(advertised, m.ID)
			}
		}
	}
	if len(advertised) == 0 {
		// Older agents do not list their methods; fall back to the documented preference.
		if len(spec.AuthMethodPreference) > 0 {
			return spec.AuthMethodPreference[0], nil
		}
		return "", fmt.Errorf("%s does not advertise any sign-in method", spec.Name)
	}
	for _, want := range spec.AuthMethodPreference {
		for _, id := range advertised {
			if id == want {
				return id, nil
			}
		}
	}
	for _, id := range advertised {
		if strings.Contains(strings.ToLower(id), "oauth") {
			return id, nil
		}
	}
	return "", fmt.Errorf("%s offers no supported sign-in method (got %s)", spec.Name, strings.Join(advertised, ", "))
}
