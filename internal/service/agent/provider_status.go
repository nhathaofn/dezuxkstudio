package agent

// Provider status: listing, probing, executable/version lookup, sign-in and sign-out.

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"app/internal/apperr"
	domainagent "app/internal/domain/agent"
	"app/internal/lookup"
	"app/internal/proc"
	"app/internal/progress"
	"app/internal/safego"
)

// List inspects all registered agent providers concurrently and returns their current info.
func (s *Service) List(ctx context.Context) ([]domainagent.ProviderInfo, error) {
	ids := domainagent.AllProviderIDs()
	out := make([]domainagent.ProviderInfo, len(ids))
	var wg sync.WaitGroup
	for i, id := range ids {
		wg.Add(1)
		safego.Go("agent.list.probe", func() {
			defer wg.Done()
			info, err := s.Probe(ctx, id)
			if err != nil {
				s.log.WarnContext(ctx, "agent: probe failed", "provider", id, "error", err)
				spec := domainagent.Registry[id]
				out[i] = domainagent.ProviderInfo{
					ID:                id,
					Name:              spec.Name,
					Executable:        spec.ExecutableLabel,
					Source:            domainagent.SourceNone,
					State:             domainagent.StateError,
					Supported:         spec.Supported,
					UnsupportedReason: spec.UnsupportedReason,
					AuthState:         domainagent.AuthUnknown,
					LastError:         err.Error(),
				}
				return
			}
			out[i] = info
		})
	}
	wg.Wait()
	return out, nil
}

// Probe resolves a provider's executable, extracts its CLI version, checks isolated auth, and merges live state.
func (s *Service) Probe(ctx context.Context, id domainagent.ProviderID) (domainagent.ProviderInfo, error) {
	if err := domainagent.ValidateProviderID(id); err != nil {
		return domainagent.ProviderInfo{}, err
	}
	// Every CLI spawn below has its own bound (version/auth probe timeouts).
	defer progress.KeepAlive(ctx)()
	spec := domainagent.Registry[id]
	resolved, source, customPath, err := s.resolveExecutable(ctx, spec)
	if err != nil {
		return domainagent.ProviderInfo{}, err
	}

	var version string
	if resolved != "" {
		version = s.probeVersionCached(ctx, id, resolved, spec.VersionArgs)
	}

	authState, accountInfo := s.probeProviderAuth(ctx, id, spec, resolved)
	s.refreshModelCatalogAsync(id, resolved)

	s.mu.Lock()
	if authState != domainagent.AuthUnknown {
		s.authStates[id] = authState
		s.accountInfos[id] = accountInfo
	}
	info := s.buildInfoLocked(spec, resolved, source, customPath, version)
	s.mu.Unlock()
	return info, nil
}

func (s *Service) probeProviderAuth(ctx context.Context, id domainagent.ProviderID, spec domainagent.ProviderSpec, resolved string) (domainagent.AuthState, string) {
	if !spec.Supported {
		return domainagent.AuthUnsupported, ""
	}
	if resolved == "" {
		return domainagent.AuthUnknown, ""
	}
	drv := s.driver(id)
	if drv.cheapAuthProbe() {
		return drv.probeAuth(ctx, resolved) // a local file read, cheap enough to run every time
	}
	// CLI auth checks spawn the CLI; reuse a fresh result instead of re-running it on every call.
	s.mu.Lock()
	cached, ok := s.authProbes[id]
	if ok && cached.fresh(resolved, s.limits.StatusCacheTTL) {
		state, account := s.authStates[id], s.accountInfos[id]
		s.mu.Unlock()
		return state, account
	}
	s.mu.Unlock()

	probeCtx, cancel := context.WithTimeout(ctx, s.limits.AuthProbeTimeout)
	defer cancel()
	state, account := drv.probeAuth(probeCtx, resolved)
	if state != domainagent.AuthUnknown {
		s.mu.Lock()
		s.authProbes[id] = cachedProbe{executable: resolved, at: time.Now()}
		s.mu.Unlock()
	}
	return state, account
}

// invalidateStatus forces the next Probe of id to re-run the CLI auth and version checks.
func (s *Service) invalidateStatus(id domainagent.ProviderID) {
	s.mu.Lock()
	delete(s.authProbes, id)
	delete(s.cachedVersions, id)
	s.mu.Unlock()
	s.invalidateModelCatalog(id)
}

// statusInfo builds a provider's info from in-memory state without spawning any CLI process.
// It is used on hot paths (sending a prompt, finishing a turn) where Probe would add seconds of latency.
func (s *Service) statusInfo(ctx context.Context, id domainagent.ProviderID) (domainagent.ProviderInfo, error) {
	spec := domainagent.Registry[id]
	resolved, source, customPath, err := s.resolveExecutable(ctx, spec)
	if err != nil {
		return domainagent.ProviderInfo{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buildInfoLocked(spec, resolved, source, customPath, s.cachedVersionLocked(id)), nil
}

func (s *Service) emitStatusOf(ctx context.Context, id domainagent.ProviderID) {
	if info, err := s.statusInfo(ctx, id); err == nil {
		s.emitStatus(info)
	}
}

// SetCustomPath saves or clears a custom executable path for a provider and re-probes it.
func (s *Service) SetCustomPath(ctx context.Context, id domainagent.ProviderID, customPath string) (domainagent.ProviderInfo, error) {
	if err := domainagent.ValidateProviderID(id); err != nil {
		return domainagent.ProviderInfo{}, err
	}
	customPath = strings.TrimSpace(customPath)
	if err := s.repo.SetCustomPath(ctx, id, customPath); err != nil {
		return domainagent.ProviderInfo{}, err
	}

	s.invalidateStatus(id)
	s.mu.Lock()
	delete(s.lastErrors, id)
	wasActive := s.activeID == id
	s.mu.Unlock()
	s.driver(id).release() // a running CLI process still belongs to the previous executable

	if wasActive {
		_ = s.Disconnect(ctx)
	}

	info, err := s.Probe(ctx, id)
	if err != nil {
		return domainagent.ProviderInfo{}, err
	}
	s.emitStatus(info)
	return info, nil
}

// Login authenticates the provider's isolated profile inside DezuxkStudio without touching the user's global CLI config.
func (s *Service) Login(ctx context.Context, id domainagent.ProviderID, apiKey string) (domainagent.ProviderInfo, error) {
	if err := domainagent.ValidateProviderID(id); err != nil {
		return domainagent.ProviderInfo{}, err
	}
	spec := domainagent.Registry[id]
	if !spec.Supported {
		return domainagent.ProviderInfo{}, apperr.Validation(spec.UnsupportedReason)
	}
	resolved, _, _, err := s.resolveExecutable(ctx, spec)
	if err != nil {
		return domainagent.ProviderInfo{}, err
	}
	if resolved == "" {
		return domainagent.ProviderInfo{}, apperr.NotFound(fmt.Sprintf("%s executable not found", spec.Name))
	}

	loginCtx, cancel := context.WithTimeout(ctx, s.limits.LoginTimeout)
	defer cancel()
	// The user is signing in in the browser: bounded by agent.login_timeout, so keep the action alive.
	stopKeepAlive := progress.KeepAlive(loginCtx)
	defer stopKeepAlive()
	drv := s.driver(id)
	loginErr := drv.login(loginCtx, resolved, apiKey)
	s.invalidateStatus(id)
	drv.release() // a long-lived CLI process (Codex app-server) still holds the previous credentials
	if loginErr != nil {
		s.mu.Lock()
		s.lastErrors[id] = localize(loginErr, msgSignInFailed, map[string]string{"detail": loginErr.Error()})
		s.mu.Unlock()
		info, _ := s.Probe(ctx, id)
		s.emitStatus(info)
		return info, userError(apperr.CodeUnauthorized, msgSignInFailed, loginErr.Error(), map[string]string{"detail": loginErr.Error()})
	}

	s.mu.Lock()
	delete(s.lastErrors, id)
	if s.states[id] == domainagent.StateUnauthenticated || s.states[id] == domainagent.StateError {
		s.states[id] = domainagent.StateDisconnected
	}
	s.mu.Unlock()

	info, err := s.Probe(ctx, id)
	if err != nil {
		return domainagent.ProviderInfo{}, err
	}
	s.emitStatus(info)
	return info, nil
}

// Logout clears the isolated DezuxkStudio account credentials for id without touching the user's personal CLI config.
func (s *Service) Logout(ctx context.Context, id domainagent.ProviderID) (domainagent.ProviderInfo, error) {
	if err := domainagent.ValidateProviderID(id); err != nil {
		return domainagent.ProviderInfo{}, err
	}
	spec := domainagent.Registry[id]
	if !spec.Supported {
		return domainagent.ProviderInfo{}, apperr.Validation(spec.UnsupportedReason)
	}
	resolved, _, _, err := s.resolveExecutable(ctx, spec)
	if err != nil {
		return domainagent.ProviderInfo{}, err
	}
	s.setCLISession(id, "") // a new account must not resume the previous account's conversation
	// Actions proposed under the old account must never run after it signed out.
	if s.approvals.clearProvider(id) {
		s.emitApprovals()
	}

	s.mu.Lock()
	wasActive := s.activeID == id
	s.mu.Unlock()
	if wasActive {
		_ = s.Disconnect(ctx)
	}

	logoutCtx, cancel := context.WithTimeout(ctx, s.limits.AuthProbeTimeout)
	defer cancel()
	drv := s.driver(id)
	logoutErr := drv.logout(logoutCtx, resolved)
	s.invalidateStatus(id)
	drv.release()

	s.mu.Lock()
	s.states[id] = domainagent.StateDisconnected
	if logoutErr != nil {
		s.lastErrors[id] = logoutErr
	} else {
		delete(s.lastErrors, id)
	}
	s.mu.Unlock()

	// Re-probe even after a failed logout so the UI shows the real remaining auth state.
	info, err := s.Probe(ctx, id)
	if err != nil {
		return domainagent.ProviderInfo{}, err
	}
	s.emitStatus(info)
	if logoutErr != nil {
		return info, apperr.Unavailable(logoutErr.Error())
	}
	return info, nil
}

func (s *Service) buildInfoLocked(spec domainagent.ProviderSpec, resolved string, source domainagent.Source, customPath, version string) domainagent.ProviderInfo {
	isActive := s.activeID == spec.ID && s.states[spec.ID] == domainagent.StateConnected && (s.isPerTurn(spec.ID) || s.activeClient != nil)
	state := s.states[spec.ID]
	if resolved == "" && !isActive && state != domainagent.StateConnecting {
		state = domainagent.StateMissing
	} else if state == "" {
		if resolved != "" {
			state = domainagent.StateDisconnected
		} else {
			state = domainagent.StateMissing
		}
	}

	authState := s.authStates[spec.ID]
	if authState == "" {
		if !spec.Supported {
			authState = domainagent.AuthUnsupported
		} else {
			authState = domainagent.AuthUnknown
		}
	}

	info := domainagent.ProviderInfo{
		ID:                 spec.ID,
		Name:               spec.Name,
		Executable:         spec.ExecutableLabel,
		ResolvedPath:       resolved,
		CustomPath:         customPath,
		Source:             source,
		Version:            version,
		State:              state,
		Supported:          spec.Supported,
		UnsupportedReason:  spec.UnsupportedReason,
		AuthState:          authState,
		AccountInfo:        s.accountInfos[spec.ID],
		IsolatedProfileDir: s.profileDirFor(spec.ID),
		LastError:          errText(s.lastErrors[spec.ID]),
		Active:             isActive,
		Busy:               isActive && s.turn != nil,
	}
	if p, ok := s.provisioning[spec.ID]; ok && p.active {
		info.Installing, info.InstallPercent = true, p.percent
	}
	if isActive && s.activeInit != nil {
		info.ProtocolVersion = s.activeInit.ProtocolVersion
		info.AgentName = s.activeInit.AgentInfo.Name
		info.AgentVersion = s.activeInit.AgentInfo.Version
		if info.Version == "" && s.activeVersion != "" {
			info.Version = s.activeVersion
		}
	}
	info.LastErrorKey, info.LastErrorParams = messageOf(s.lastErrors[spec.ID])
	if info.LastError == "" && customPath != "" && resolved != customPath {
		// The saved custom path no longer exists; say so instead of silently using another executable.
		if resolved != "" {
			info.LastError = fmt.Sprintf("custom path %q not found; using %s", customPath, resolved)
			info.LastErrorKey = msgCustomPathMissing
			info.LastErrorParams = map[string]string{"path": customPath, "fallback": resolved}
		} else {
			info.LastError = fmt.Sprintf("custom path %q not found", customPath)
			info.LastErrorKey = msgCustomPathGone
			info.LastErrorParams = map[string]string{"path": customPath}
		}
	}
	s.fillModelInfo(&info)
	info.Revision = s.statusRevision.Add(1)
	return info
}

func (s *Service) resolveExecutable(ctx context.Context, spec domainagent.ProviderSpec) (resolvedPath string, source domainagent.Source, customPath string, err error) {
	customPath, err = s.repo.GetCustomPath(ctx, spec.ID)
	if err != nil {
		return "", domainagent.SourceNone, "", err
	}
	path, src, ok := lookup.First(
		lookup.Candidate[domainagent.Source]{Source: domainagent.SourceCustom, Find: lookup.File(customPath)},
		lookup.Candidate[domainagent.Source]{Source: domainagent.SourceManaged, Find: s.managedFinder(spec.ID)},
		lookup.Candidate[domainagent.Source]{Source: domainagent.SourceSystem, Find: lookup.OnPath(spec.CandidateExecutables...)},
		lookup.Candidate[domainagent.Source]{Source: domainagent.SourceSystem, Find: lookup.Files(expandKnownPath, spec.KnownPaths...)},
	)
	if !ok {
		return "", domainagent.SourceNone, customPath, nil
	}
	return path, src, customPath, nil
}

// probeVersionCached returns the CLI version, re-running `--version` when the executable changed or the
// cached value is older than Limits.StatusCacheTTL (so an upgraded CLI shows its new version).
func (s *Service) probeVersionCached(ctx context.Context, id domainagent.ProviderID, executable string, versionArgs []string) string {
	s.mu.Lock()
	c, ok := s.cachedVersions[id]
	s.mu.Unlock()
	if ok && c.fresh(executable, s.limits.StatusCacheTTL) {
		return c.value
	}
	v := s.probeVersion(ctx, executable, versionArgs)
	if v != "" {
		s.mu.Lock()
		s.cachedVersions[id] = cachedProbe{executable: executable, value: v, at: time.Now()}
		s.mu.Unlock()
	}
	return v
}

// cachedVersionLocked returns the last known version for display, regardless of age. Caller holds s.mu.
func (s *Service) cachedVersionLocked(id domainagent.ProviderID) string {
	return s.cachedVersions[id].value
}

// cachedProbe is the result of a CLI probe, valid for one executable for a limited time.
type cachedProbe struct {
	executable string
	value      string
	at         time.Time
}

func (c cachedProbe) fresh(executable string, ttl time.Duration) bool {
	return c.executable == executable && time.Since(c.at) < ttl
}

func (s *Service) probeVersion(ctx context.Context, executable string, versionArgs []string) string {
	if len(versionArgs) == 0 {
		return ""
	}
	probeCtx, cancel := context.WithTimeout(ctx, s.limits.VersionProbeTimeout)
	defer cancel()
	cmd := proc.Command(probeCtx, executable, versionArgs...)
	out, err := cmd.CombinedOutput()
	if err != nil && len(out) == 0 {
		return ""
	}
	return firstLineTrimmedMax(string(out), maxVersionLen)
}

func firstLineTrimmedMax(s string, maxLen int) string {
	line := firstLineTrimmed(s)
	if len(line) > maxLen {
		return line[:maxLen]
	}
	return line
}

func (s *Service) emitStatus(info domainagent.ProviderInfo) {
	if s.onStatusChanged != nil {
		s.onStatusChanged(info)
	}
}
