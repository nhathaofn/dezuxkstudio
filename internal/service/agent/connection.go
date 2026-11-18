package agent

// Connection lifecycle: connect, restore, disconnect, and the events of a live ACP session.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"app/internal/apperr"
	domainagent "app/internal/domain/agent"
	"app/internal/progress"
	providersvc "app/internal/service/provider"
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

	// Check if an opt-in provider link is configured.
	var linkedSecret *providersvc.ResolvedLink
	if store := s.providerLinksStore(); store != nil {
		var lErr error
		linkedSecret, lErr = providersvc.ResolveLinkedSecret(ctx, string(id), "acp", store, s.secrets.store)
		if lErr != nil {
			// CRITICAL DESIGN RULE: NO AUTO-FALLBACK.
			// When an opt-in link is configured but is incompatible or secret is missing,
			// fail immediately with the error and NEVER silently fall back to OAuth.
			return s.failConnect(id, spec, resolved, source, customPath, domainagent.StateError, lErr), lErr
		}
	}

	var authState domainagent.AuthState
	var accountInfo string
	if linkedSecret != nil {
		authState = domainagent.AuthAuthenticated
		accountInfo = fmt.Sprintf("Linked API Key: %s", linkedSecret.ProviderID)
	} else {
		// The isolated profile must be signed in before the ACP process is spawned.
		authState, accountInfo = sd.probeAuth(ctx, resolved)
	}
	s.mu.Lock()
	s.authStates[id] = authState
	s.accountInfos[id] = accountInfo
	s.mu.Unlock()
	if authState != domainagent.AuthAuthenticated {
		// CRITICAL DESIGN RULE: NO AUTO-FALLBACK.
		// Absolutely NEVER automatically fallback from OAuth/Subscription to API key.
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
	if linkedSecret != nil {
		for k, v := range linkedSecret.EnvOverrides {
			procEnv = isolatedEnv(procEnv, map[string]string{k: v})
		}
	}
	s.setActiveLinkedSecret(linkedSecret)

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
	initSysPrompt := s.systemPrompt(initCtx, id)
	eagerSessionID, sessErr := transport.NewSession(initCtx, workDir, initSysPrompt)
	initPromptHash := ""
	if sessErr != nil {
		if !isTransportAlive(transport) || s.connSeq.Load() != connToken {
			_ = transport.Close()
			if isAuthError(sessErr) {
				return s.failConnect(id, spec, resolved, source, customPath, domainagent.StateUnauthenticated, sessErr), apperr.New(apperr.CodeUnauthorized, sessErr.Error())
			}
			return s.failConnect(id, spec, resolved, source, customPath, domainagent.StateError, sessErr), apperr.Unavailable(sessErr.Error())
		}
		s.log.WarnContext(initCtx, "agent: eager session/new failed", "provider", id, "error", sessErr)
		eagerSessionID = ""
	} else {
		initPromptHash = promptDigest(initSysPrompt)
		s.applyACPSessionModels(initCtx, id, transport, eagerSessionID)
	}

	s.mu.Lock()
	if s.connSeq.Load() != connToken || s.closing || !isTransportAlive(transport) {
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
	s.activeSessionPromptHash = initPromptHash
	s.sessionPrimed = false
	s.resetConversationOnProviderChangeLocked(id)
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
		s.log.WarnContext(ctx, "agent: remember connection", "error", err)
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
	// Check if an opt-in provider link is configured.
	var linkedSecret *providersvc.ResolvedLink
	if store := s.providerLinksStore(); store != nil {
		var lErr error
		linkedSecret, lErr = providersvc.ResolveLinkedSecret(ctx, string(id), "cli", store, s.secrets.store)
		if lErr != nil {
			// CRITICAL DESIGN RULE: NO AUTO-FALLBACK.
			// When an opt-in link is configured but is incompatible or secret is missing,
			// fail immediately with the error and NEVER silently fall back to OAuth.
			return s.failConnect(id, spec, resolved, source, customPath, domainagent.StateError, lErr), lErr
		}
	}

	var authState domainagent.AuthState
	var accountInfo string
	if linkedSecret != nil {
		authState = domainagent.AuthAuthenticated
		accountInfo = fmt.Sprintf("Linked API Key: %s", linkedSecret.ProviderID)
	} else {
		authState, accountInfo = s.probeProviderAuth(ctx, id, spec, resolved)
	}
	s.mu.Lock()
	s.authStates[id] = authState
	s.accountInfos[id] = accountInfo
	s.mu.Unlock()
	if authState != domainagent.AuthAuthenticated {
		// CRITICAL DESIGN RULE: NO AUTO-FALLBACK.
		// Absolutely NEVER automatically fallback from OAuth/Subscription to API key.
		err := localize(fmt.Errorf("isolated %s profile is not signed in; click Sign in first", spec.Name), msgNotSignedIn, map[string]string{"name": spec.Name})
		return s.failConnect(id, spec, resolved, source, customPath, domainagent.StateUnauthenticated, err), toUserError(apperr.CodeUnauthorized, err)
	}
	s.setActiveLinkedSecret(linkedSecret)
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
	s.activeSessionPromptHash = ""
	s.activeInit = nil
	s.activeVersion = version
	s.resetConversationOnProviderChangeLocked(id)
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
		s.log.WarnContext(ctx, "agent: restore connection failed", "provider", id, "error", err)
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
	pending := s.pendingTurnsLocked()
	s.connSeq.Add(1)
	s.clearConnectionLocked()
	if prevID != "" {
		s.states[prevID] = domainagent.StateDisconnected
		delete(s.lastErrors, prevID)
	}
	s.mu.Unlock()

	for _, p := range pending {
		p.cancel()
	}
	if t != nil {
		t.cancel()
	}
	if client != nil {
		_ = client.Close()
	}
	// Wait for cancelled turn goroutines to finish endTurn/storeReply before releasing the driver
	// or letting a reconnect read/append conversation history.
	s.waitTurnsDone(ctx, pending)
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

func (s *Service) pendingTurnsLocked() []*turn {
	if len(s.runningTurns) == 0 {
		return nil
	}
	out := make([]*turn, 0, len(s.runningTurns))
	for t := range s.runningTurns {
		out = append(out, t)
	}
	return out
}

func (s *Service) waitTurnsDone(ctx context.Context, pending []*turn) {
	if len(pending) == 0 {
		return
	}
	waitCtx, cancel := context.WithTimeout(ctx, s.limits.InterruptWait)
	defer cancel()
	for _, p := range pending {
		select {
		case <-p.done:
		case <-waitCtx.Done():
			s.log.WarnContext(ctx, "agent: timed out waiting for cancelled turn to stop", "turn_id", p.id, "error", waitCtx.Err())
			return
		}
	}
}

// clearConnectionLocked forgets the active connection and its running turn. Caller holds s.mu.
func (s *Service) clearConnectionLocked() {
	s.activeClient = nil
	s.activeID = ""
	s.activeSessionID = ""
	s.activeSessionPromptHash = ""
	s.activeInit = nil
	s.activeVersion = ""
	s.turn = nil
	s.setActiveLinkedSecret(nil)
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
	activeSecret := s.activeLinkedSecret()
	switch upd.Kind {
	case "agent_message_chunk":
		evt.Type, evt.Text = domainagent.ChatEventDelta, upd.Text
		if activeSecret != nil && activeSecret.Secret != "" {
			evt.Text = RedactSecret(evt.Text, activeSecret.Secret)
		}
		s.emitChat(evt)
	case "agent_thought_chunk", "thought_message_chunk":
		evt.Type, evt.Text = domainagent.ChatEventThought, upd.Text
		if activeSecret != nil && activeSecret.Secret != "" {
			evt.Text = RedactSecret(evt.Text, activeSecret.Secret)
		}
		s.emitChat(evt)
	case "tool_call", "tool_call_update":
		evt.Tool = t.trackACPTool(upd)
		if evt.Tool == "" {
			return
		}
		if activeSecret != nil && activeSecret.Secret != "" {
			evt.Tool = RedactSecret(evt.Tool, activeSecret.Secret)
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

func isTransportAlive(t Transport) bool {
	if t == nil {
		return false
	}
	if alive, ok := t.(interface{ Alive() bool }); ok {
		return alive.Alive()
	}
	return true
}

// handleProcessExit handles an ACP agent process that exited on its own: the connection is dropped and a
// running turn ends right away with the exit as its (stored, translatable) error.
func (s *Service) handleProcessExit(connToken int64, id domainagent.ProviderID, exitErr error) {
	s.mu.Lock()
	if s.connSeq.Load() != connToken || s.closing {
		s.mu.Unlock()
		return
	}
	exitSeq := s.connSeq.Add(1)
	t := s.turn
	activeSecret := s.activeLinkedSecret()
	s.clearConnectionLocked()
	var turnErr error
	if exitErr != nil {
		if activeSecret != nil && activeSecret.Secret != "" {
			exitErr = RedactError(exitErr, activeSecret.Secret)
		}
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
		s.endTurnAtSeq(t, turnErr, exitSeq)
		t.cancel()
	}
	if s.approvals.clearProvider(id) {
		s.emitApprovals()
	}
	s.emitStatusOf(context.Background(), id)
}

// SetProviderLink sets an explicit opt-in provider link for agentProviderID.
// CRITICAL: Rejects incompatible combinations. Never auto-fallbacks.
func (s *Service) SetProviderLink(ctx context.Context, agentProviderID domainagent.ProviderID, targetProviderID, credentialID string) error {
	store := s.providerLinksStore()
	if store == nil {
		return errors.New("agent provider link storage is unavailable")
	}
	runnerDriver := "cli"
	if _, ok := s.driver(agentProviderID).(sessionDriver); ok {
		runnerDriver = "acp"
	}
	if !providersvc.IsCompatible(string(agentProviderID), runnerDriver, targetProviderID, "api_key") {
		return apperr.Validation(fmt.Sprintf(
			"target provider %q is not compatible with agent %q (runner driver: %s)",
			targetProviderID, agentProviderID, runnerDriver,
		))
	}
	link := domainagent.ProviderLink{
		AgentProviderID: string(agentProviderID),
		ProviderID:      targetProviderID,
		CredentialID:    credentialID,
		AuthMethod:      "api_key",
		UpdatedAt:       time.Now(),
	}
	if err := store.SetProviderLink(ctx, link); err != nil {
		return err
	}
	s.mu.Lock()
	delete(s.authProbes, agentProviderID)
	s.mu.Unlock()
	s.emitStatusOf(ctx, agentProviderID)
	return nil
}

// DeleteProviderLink removes the opt-in provider link for agentProviderID.
// CRITICAL: Immediately resets to the isolated profile's native auth state without falling back to any key.
func (s *Service) DeleteProviderLink(ctx context.Context, agentProviderID domainagent.ProviderID) error {
	store := s.providerLinksStore()
	if store == nil {
		return errors.New("agent provider link storage is unavailable")
	}
	if err := store.DeleteProviderLink(ctx, string(agentProviderID)); err != nil {
		return err
	}
	s.mu.Lock()
	delete(s.authProbes, agentProviderID)
	s.mu.Unlock()
	s.emitStatusOf(ctx, agentProviderID)
	return nil
}

// GetProviderLink returns the opt-in link for agentProviderID, if any.
func (s *Service) GetProviderLink(ctx context.Context, agentProviderID domainagent.ProviderID) (*domainagent.ProviderLink, error) {
	store := s.providerLinksStore()
	if store == nil {
		return nil, nil
	}
	l, err := store.GetProviderLink(ctx, string(agentProviderID))
	if err != nil {
		return nil, nil
	}
	return &l, nil
}

// ListProviderLinks returns all configured agent provider links.
func (s *Service) ListProviderLinks(ctx context.Context) ([]domainagent.ProviderLink, error) {
	store := s.providerLinksStore()
	if store == nil {
		return []domainagent.ProviderLink{}, nil
	}
	return store.ListProviderLinks(ctx)
}

// CompatibleProviders returns the allowed target providers for agentProviderID.
func (s *Service) CompatibleProviders(agentProviderID domainagent.ProviderID) []string {
	runnerDriver := "cli"
	if _, ok := s.driver(agentProviderID).(sessionDriver); ok {
		runnerDriver = "acp"
	}
	return providersvc.ResolveCompatibleProviders(string(agentProviderID), runnerDriver)
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
