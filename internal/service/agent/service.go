// Package agent coordinates isolated AI agent discovery, authentication, MCP tooling, and chat sessions.
package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"app/internal/apperr"
	domainagent "app/internal/domain/agent"
	"app/internal/lookup"
	"app/internal/proc"
	"app/internal/progress"
	"app/internal/safego"
	"app/internal/secrets"

	"github.com/google/uuid"
)

// Transport abstracts an active ACP connection so unit tests can inject fake pipes.
type Transport interface {
	Initialize(ctx context.Context, clientName, clientVersion string) (*InitializeResult, error)
	Authenticate(ctx context.Context, methodID string) (string, error)
	NewSession(ctx context.Context, cwd, systemPrompt string) (string, error)
	// AvailableModels returns the models offered by the most recent session and the session's current model.
	AvailableModels() ([]domainagent.ModelOption, string)
	SetModel(ctx context.Context, sessionID, modelID string) error
	Prompt(ctx context.Context, sessionID, text string) error
	Cancel(sessionID string) error
	Close() error
}

// TransportFactory launches an isolated ACP transport for a resolved executable and args.
type TransportFactory func(ctx context.Context, executable string, args []string, workDir string, env []string, onEvent func(SessionUpdate), onExit func(error)) (Transport, error)

// CodexTurnRunner executes one isolated per-turn CLI agent against DezuxkStudio's MCP server.
type CodexTurnRunner func(ctx context.Context, executable, profileRoot, workspaceDir, mcpURL, mcpToken, messageID, prompt string) error

// CodexAuthProber checks whether an isolated CLI profile is authenticated.
type CodexAuthProber func(ctx context.Context, executable, profileRoot string) (domainagent.AuthState, string)

// Options configures the agent Service.
type Options struct {
	Repo               domainagent.Repository
	DataDir            string
	WorkspaceDir       string
	MediaDir           string
	ProfilesDir        string
	AppVersion         string
	Limits             Limits        // zero fields use DefaultLimits
	Secrets            secrets.Store // OS credential store for provider API keys (in-memory when nil)
	Logger             *slog.Logger
	Workflow           WorkflowRunner
	Binaries           BinariesManager
	Managed            ManagedCLIs // the app's own agent CLI copies; nil keeps using what is on the machine
	AutoInstall        bool        // install a missing managed copy in the background
	AutoUpdate         bool        // move managed copies to newer official releases
	Preferences        PreferencesManager
	System             SystemInspector
	Diagnostics        DiagnosticsExporter
	OnStatusChanged    func(domainagent.ProviderInfo)
	OnChatEvent        func(domainagent.ChatEvent)
	OnApprovalsChanged func([]domainagent.ApprovalRequest)
	OnLoginPrompt      func(domainagent.LoginPrompt) // the page/code the user must use to finish a running sign-in
	SpawnTransport     TransportFactory
	RunCodexTurn       CodexTurnRunner
	ProbeCodexAuth     CodexAuthProber
	RunClaudeTurn      CodexTurnRunner
	ProbeClaudeAuth    CodexAuthProber
}

// Service manages isolated external AI agent connections, MCP tooling, approvals, and chat turns.
type Service struct {
	// statusRevision numbers the status snapshots (ProviderInfo.Revision); it only ever grows.
	statusRevision atomic.Int64

	repo               domainagent.Repository
	dataDir            string
	workspaceDir       string
	mediaDir           string
	profilesDir        string
	appVersion         string
	limits             Limits
	log                *slog.Logger
	workflow           WorkflowRunner
	binaries           BinariesManager
	managed            ManagedCLIs
	autoInstall        bool
	autoUpdate         bool
	provisioning       map[domainagent.ProviderID]provisionState
	onLoginPrompt      func(domainagent.LoginPrompt)
	loginMu            sync.Mutex
	logins             map[domainagent.ProviderID]*loginSession
	preferences        PreferencesManager
	system             SystemInspector
	diagnostics        DiagnosticsExporter
	onStatusChanged    func(domainagent.ProviderInfo)
	onChatEvent        func(domainagent.ChatEvent)
	onApprovalsChanged func([]domainagent.ApprovalRequest)
	spawnTransport     TransportFactory
	runCodex           CodexTurnRunner
	probeCodex         CodexAuthProber
	runClaude          CodexTurnRunner
	probeClaude        CodexAuthProber

	policy    *AccessPolicy
	approvals *approvalStore
	secrets   profileSecrets // API keys of the isolated provider profiles

	toolDocsOnce sync.Once
	docs         []toolDoc // registered MCP tools, see toolDocs

	mu              sync.Mutex
	mcpSrv          *localMCPServer
	activeID        domainagent.ProviderID
	activeClient    Transport
	activeSessionID string
	activeInit      *InitializeResult
	activeVersion   string
	turn            *turn                             // the running chat turn; nil when idle
	turns           sync.WaitGroup                    // running turn goroutines, waited for on Close
	sessionPrimed   bool                              // the active ACP session already received the conversation history
	cliSessions     map[domainagent.ProviderID]string // resumable Codex thread / Claude session per provider
	models          *modelState
	uiContext       UIContext
	states          map[domainagent.ProviderID]domainagent.ConnectionState
	authStates      map[domainagent.ProviderID]domainagent.AuthState
	accountInfos    map[domainagent.ProviderID]string
	lastErrors      map[domainagent.ProviderID]error       // may carry an i18n key (localizedError)
	cachedVersions  map[domainagent.ProviderID]cachedProbe // CLI --version results
	authProbes      map[domainagent.ProviderID]cachedProbe // when the CLI auth status was last checked
	tempWorkspace   string                                 // fallback workspace when none is configured
	sentScreen      map[string]string                      // screen-context block last sent per provider session
	conversationID  string                                 // current chat; "" until its first message (new chat on every start)

	codexServer          *codexAppServer // long-lived `codex app-server` for streaming Codex turns
	codexAppServerBroken map[string]bool // executables whose app-server failed to start (use codex exec)
	spawnCodexAppServer  func(key, executable string, args, env []string, workDir string) (*codexAppServer, error)
	closing              bool
	msgSeq               atomic.Int64
	connSeq              atomic.Int64
}

// New creates an initialized agent Service with isolated workspace policy and approval queue.
func New(o Options) (*Service, error) {
	if o.Repo == nil || o.Logger == nil {
		return nil, fmt.Errorf("agent: repo and logger are required")
	}
	version := strings.TrimSpace(o.AppVersion)
	if version == "" {
		version = "0.0.0-dev"
	}
	limits := o.Limits.withDefaults()
	secretStore := o.Secrets
	if secretStore == nil {
		secretStore = secrets.NewMemory()
	}
	allowedDirs, err := o.Repo.ListAllowedDirs(context.Background())
	if err != nil {
		o.Logger.Warn("agent: failed to load allowed dirs", "error", err)
	}
	s := &Service{
		repo:               o.Repo,
		dataDir:            o.DataDir,
		workspaceDir:       o.WorkspaceDir,
		mediaDir:           o.MediaDir,
		profilesDir:        o.ProfilesDir,
		appVersion:         version,
		limits:             limits,
		log:                o.Logger,
		workflow:           o.Workflow,
		binaries:           o.Binaries,
		managed:            o.Managed,
		autoInstall:        o.AutoInstall,
		autoUpdate:         o.AutoUpdate,
		provisioning:       make(map[domainagent.ProviderID]provisionState),
		onLoginPrompt:      o.OnLoginPrompt,
		logins:             make(map[domainagent.ProviderID]*loginSession),
		preferences:        o.Preferences,
		system:             o.System,
		diagnostics:        o.Diagnostics,
		onStatusChanged:    o.OnStatusChanged,
		onChatEvent:        o.OnChatEvent,
		onApprovalsChanged: o.OnApprovalsChanged,
		spawnTransport:     o.SpawnTransport,
		runCodex:           o.RunCodexTurn,
		probeCodex:         o.ProbeCodexAuth,
		runClaude:          o.RunClaudeTurn,
		probeClaude:        o.ProbeClaudeAuth,
		policy:             NewAccessPolicy(o.DataDir, o.WorkspaceDir, o.MediaDir, o.ProfilesDir, allowedDirs),
		secrets:            profileSecrets{store: secretStore},
		approvals:          newApprovalStore(limits.ApprovalTTL, limits.MaxPendingApprovals),
		models:             newModelState(),
		cliSessions:        make(map[domainagent.ProviderID]string),
		states:             make(map[domainagent.ProviderID]domainagent.ConnectionState),
		authStates:         make(map[domainagent.ProviderID]domainagent.AuthState),
		accountInfos:       make(map[domainagent.ProviderID]string),
		lastErrors:         make(map[domainagent.ProviderID]error),
		cachedVersions:     make(map[domainagent.ProviderID]cachedProbe),
		authProbes:         make(map[domainagent.ProviderID]cachedProbe),
		sentScreen:         make(map[string]string),

		codexAppServerBroken: make(map[string]bool),
		spawnCodexAppServer:  startCodexAppServer,
	}
	if s.spawnTransport == nil {
		// The process outlives the Connect call that starts it, so it is not bound to that call's ctx;
		// Disconnect/Close end it.
		s.spawnTransport = func(_ context.Context, executable string, args []string, workDir string, env []string, onEvent func(SessionUpdate), onExit func(error)) (Transport, error) {
			return SpawnACP(context.Background(), executable, args, workDir, env, s.toolNames(), s.log, onEvent, onExit)
		}
	}
	if s.runCodex == nil {
		s.runCodex = s.runCodexTurn
	}
	if s.probeCodex == nil {
		s.probeCodex = probeCodexAuth
	}
	if s.runClaude == nil {
		s.runClaude = s.runClaudeTurn
	}
	if s.probeClaude == nil {
		s.probeClaude = s.secrets.probeClaudeAuth
	}
	// Revisions must keep growing across restarts: a UI that stays open (dev reload, reconnect) holds the
	// last revision it saw and drops anything lower.
	s.statusRevision.Store(time.Now().UnixMicro())
	s.loadModelSelections(context.Background())
	return s, nil
}

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
	if id == domainagent.ProviderCodex {
		clear(s.codexAppServerBroken) // a different executable gets a fresh app-server attempt
	}
	s.mu.Unlock()
	if drv := s.driver(id); drv.perTurn() {
		drv.release()
	}

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
	if drv.perTurn() {
		drv.release() // a long-lived CLI process (Codex app-server) still holds the previous credentials
	}
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

func (s *Service) loginAgyOAuth(ctx context.Context, spec domainagent.ProviderSpec, executable string) error {
	workDir, err := s.ensureWorkspaceDir()
	if err != nil {
		return err
	}
	profileRoot := s.profileDirFor(domainagent.ProviderAntigravity)
	_, agyEnvMap, err := s.secrets.prepareAgyProfileWithMode(profileRoot, "", "", true)
	if err != nil {
		return err
	}
	procEnv := providerEnv(domainagent.ProviderAntigravity, agyEnvMap)

	// ctx is already bounded by Login (Limits.LoginTimeout).
	transport, err := s.spawnTransport(ctx, executable, spec.ACPArgs, workDir, procEnv, nil, nil)
	if err != nil {
		return fmt.Errorf("agy login failed: %w", err)
	}
	defer func() { _ = transport.Close() }()

	initCtx, initCancel := context.WithTimeout(ctx, s.limits.InitializeTimeout)
	defer initCancel()
	initRes, err := transport.Initialize(initCtx, clientName, s.appVersion)
	if err != nil {
		return fmt.Errorf("agy login initialize failed: %w", err)
	}
	methodID, err := pickAuthMethod(spec, initRes)
	if err != nil {
		return err
	}

	tierName, err := transport.Authenticate(ctx, methodID)
	if err != nil {
		return fmt.Errorf("agy Google sign-in failed: %w", err)
	}
	return s.secrets.saveAgyOAuth(profileRoot, tierName)
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

	if s.isPerTurn(id) {
		return s.connectPerTurnCLI(ctx, startToken, id, spec, resolved, source, customPath)
	}

	// Antigravity isolated ACP connection: verify isolated Gemini API key before spawning agy acp.
	authState, accountInfo := s.secrets.probeAgyAuth(s.profileDirFor(id))
	s.mu.Lock()
	s.authStates[id] = authState
	s.accountInfos[id] = accountInfo
	s.mu.Unlock()
	if authState != domainagent.AuthAuthenticated {
		err := localize(fmt.Errorf("isolated %s profile is not signed in; click Sign in or enter a Gemini API key first", spec.Name), msgNotSignedIn, map[string]string{"name": spec.Name})
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

	_, agyEnvMap, err := s.secrets.prepareAgyProfile(s.profileDirFor(id), mcpSrv.url, mcpSrv.token)
	if err != nil {
		return s.failConnect(id, spec, resolved, source, customPath, domainagent.StateError, err), apperr.Unavailable(err.Error())
	}
	procEnv := providerEnv(domainagent.ProviderAntigravity, agyEnvMap)

	transport, err := s.spawnTransport(
		ctx,
		resolved,
		spec.ACPArgs,
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
	if prepErr := s.driver(id).prepare(); prepErr != nil {
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

// SendPrompt submits a chat message to the currently active provider and streams events asynchronously.
func (s *Service) SendPrompt(ctx context.Context, text string) (string, domainagent.ProviderID, error) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return "", "", userError(apperr.CodeValidation, msgPromptEmpty, "prompt cannot be empty", nil)
	}

	s.mu.Lock()
	providerID := s.activeID
	isConnected := providerID != "" && s.states[providerID] == domainagent.StateConnected && (s.isPerTurn(providerID) || s.activeClient != nil)
	if !isConnected {
		s.mu.Unlock()
		return "", "", userError(apperr.CodeUnavailable, msgNotConnected, "no agent is currently connected", nil)
	}
	if s.turn != nil {
		s.mu.Unlock()
		return "", "", userError(apperr.CodeConflict, msgBusy, "agent is already processing a prompt", nil)
	}
	client := s.activeClient
	sessionID := s.activeSessionID
	uiCtx := s.uiContext
	connToken := s.connSeq.Load()
	seq := s.msgSeq.Add(1)
	messageID := fmt.Sprintf("msg-%d-%d", time.Now().UnixMilli(), seq)
	userMsgID := fmt.Sprintf("user-%d-%d", time.Now().UnixMilli(), seq)

	// The first message of a chat creates its conversation; later messages join it.
	now := time.Now().UnixMilli()
	newConversation := s.conversationID == ""
	if newConversation {
		s.conversationID = uuid.NewString()
	}
	conversationID := s.conversationID

	// Like actions, a turn times out after agent.turn_timeout WITHOUT activity (streamed text,
	// tool events, workflow progress from an MCP tool), not after a fixed total duration.
	turnCtx, touchTurn, cancelTurn := progress.NewIdleContext(context.Background(), s.limits.TurnTimeout)
	t := newTurn(messageID, conversationID, providerID, connToken, touchTurn, context.CancelFunc(cancelTurn))
	s.turn = t
	s.turns.Add(1)
	s.mu.Unlock()

	conv := domainagent.Conversation{ID: conversationID, ProviderID: providerID, CreatedAt: now, UpdatedAt: now}
	if newConversation {
		conv.Title = conversationTitle(trimmed)
	}
	if err := s.repo.SaveConversation(ctx, conv); err != nil {
		s.log.Warn("agent: failed to save conversation", "error", err)
	}
	// Read twice the replay size: error/empty rows are filtered out before the newest N are kept.
	history, err := s.repo.ListConversationMessages(ctx, conversationID, s.limits.ContextMessages*2)
	if err != nil {
		s.log.Warn("agent: failed to read history before turn", "error", err)
	}
	if err := s.repo.AppendChatMessage(ctx, domainagent.ChatMessage{
		ID:             userMsgID,
		ProviderID:     providerID,
		Role:           "user",
		Text:           trimmed,
		CreatedAt:      now,
		ConversationID: conversationID,
	}); err != nil {
		s.log.Warn("agent: failed to persist user message", "error", err)
	}

	s.emitStatusOf(ctx, providerID)

	safego.Go("agent.turn", func() {
		defer s.turns.Done()
		defer t.cancel()
		err := s.runTurn(turnCtx, t, client, sessionID, trimmed, history, uiCtx)
		s.endTurn(t, turnResult(turnCtx, err, s.limits.TurnTimeout))
	})

	return messageID, providerID, nil
}

// CancelPrompt cancels the currently running prompt turn if one is active.
func (s *Service) CancelPrompt(_ context.Context) error {
	s.mu.Lock()
	client := s.activeClient
	sessionID := s.activeSessionID
	t := s.turn
	s.mu.Unlock()

	if t == nil {
		return nil
	}
	if client != nil && sessionID != "" {
		_ = client.Cancel(sessionID)
	}
	t.cancel()
	return nil
}

// CurrentConversationID returns the current chat ("" for a new chat without messages).
func (s *Service) CurrentConversationID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.conversationID
}

// NewConversation starts a new chat. Earlier chats stay in history (ListConversations).
func (s *Service) NewConversation(_ context.Context) error {
	return s.switchConversation("")
}

// switchConversation makes id the current chat ("" = a new one) and resets the provider sessions, so the
// next turn starts a fresh session primed with that chat's own history.
func (s *Service) switchConversation(id string) error {
	s.mu.Lock()
	if s.turn != nil {
		s.mu.Unlock()
		return userError(apperr.CodeConflict, msgClearWhileBusy, "cannot switch chats while the agent is busy", nil)
	}
	s.conversationID = id
	s.activeSessionID = ""
	s.sessionPrimed = false
	clear(s.cliSessions)
	clear(s.sentScreen)
	s.mu.Unlock()
	s.approvals.clear()
	s.emitApprovals()
	return nil
}

// History returns the current chat's messages (empty for a new chat) and its ID.
func (s *Service) History(ctx context.Context) (string, []domainagent.ChatMessage, error) {
	s.mu.Lock()
	id := s.conversationID
	s.mu.Unlock()
	if id == "" {
		return "", nil, nil
	}
	msgs, err := s.repo.ListConversationMessages(ctx, id, s.limits.HistoryLimit)
	return id, msgs, err
}

// ListConversations returns earlier chats, most recent first.
func (s *Service) ListConversations(ctx context.Context) ([]domainagent.Conversation, error) {
	return s.repo.ListConversations(ctx, s.limits.HistoryLimit)
}

// OpenConversation makes an earlier chat current so the user can read and continue it.
func (s *Service) OpenConversation(ctx context.Context, id string) (string, []domainagent.ChatMessage, error) {
	id = strings.TrimSpace(id)
	msgs, err := s.repo.ListConversationMessages(ctx, id, s.limits.HistoryLimit)
	if err != nil {
		return "", nil, err
	}
	if len(msgs) == 0 {
		return "", nil, userError(apperr.CodeNotFound, msgConversationGone, "conversation not found", nil)
	}
	if err := s.switchConversation(id); err != nil {
		return "", nil, err
	}
	return id, msgs, nil
}

// DeleteConversation removes a chat; deleting the current one starts a new chat first, so no turn can
// start in (or be running in) the chat being deleted.
func (s *Service) DeleteConversation(ctx context.Context, id string) error {
	s.mu.Lock()
	current := s.conversationID == id
	s.mu.Unlock()
	if current {
		if err := s.switchConversation(""); err != nil {
			return err
		}
	}
	return s.repo.DeleteConversation(ctx, id)
}

// conversationTitle shortens a chat's first message into its title.
func conversationTitle(prompt string) string {
	title := strings.Join(strings.Fields(prompt), " ")
	if r := []rune(title); len(r) > 80 {
		title = string(r[:80]) + "…"
	}
	return title
}

// ListApprovals returns pending high-impact actions waiting for user approval in the desktop UI.
func (s *Service) ListApprovals() []domainagent.ApprovalRequest {
	return s.approvals.list()
}

// ResolveApproval approves or rejects a pending action proposed by an agent MCP tool call.
func (s *Service) ResolveApproval(ctx context.Context, id string, approve bool) (any, error) {
	item, ok := s.approvals.consume(id)
	s.emitApprovals()
	if !ok {
		return nil, userError(apperr.CodeNotFound, msgApprovalGone, "approval request is no longer pending", nil)
	}
	if !approve {
		return map[string]any{"approved": false}, nil
	}
	res, err := item.exec(ctx)
	if err != nil {
		return nil, apperr.Unavailable(err.Error())
	}
	return res, nil
}

// SetUIContext updates the active UI screen context visible to the agent via MCP.
func (s *Service) SetUIContext(ui UIContext) {
	s.mu.Lock()
	s.uiContext = ui
	s.mu.Unlock()
}

// ListAllowedDirs returns all user-approved workspace directories.
func (s *Service) ListAllowedDirs(ctx context.Context) ([]string, error) {
	return s.repo.ListAllowedDirs(ctx)
}

// AddAllowedDir validates and persists a user-approved working folder for the agent.
func (s *Service) AddAllowedDir(ctx context.Context, dir string) ([]string, error) {
	validated, err := s.policy.ValidateAllowedDirCandidate(dir)
	if err != nil {
		return nil, apperr.Validation(err.Error())
	}
	if err := s.repo.AddAllowedDir(ctx, validated); err != nil {
		return nil, err
	}
	dirs, err := s.repo.ListAllowedDirs(ctx)
	if err != nil {
		return nil, err
	}
	s.policy.SetAllowedDirs(dirs)
	return dirs, nil
}

// RemoveAllowedDir revokes a user-approved working folder.
func (s *Service) RemoveAllowedDir(ctx context.Context, dir string) ([]string, error) {
	if err := s.repo.RemoveAllowedDir(ctx, dir); err != nil {
		return nil, err
	}
	dirs, err := s.repo.ListAllowedDirs(ctx)
	if err != nil {
		return nil, err
	}
	s.policy.SetAllowedDirs(dirs)
	return dirs, nil
}

// Close shuts down the active agent subprocess, MCP server, and clears ephemeral tokens.
func (s *Service) Close() error {
	s.mu.Lock()
	s.closing = true
	mcpSrv := s.mcpSrv
	s.mcpSrv = nil
	s.mu.Unlock()

	err := s.Disconnect(context.Background())
	s.waitTurns()
	if mcpSrv != nil {
		mcpSrv.close()
	}
	for _, id := range domainagent.AllProviderIDs() {
		s.driver(id).release()
	}
	s.mu.Lock()
	tmp := s.tempWorkspace
	s.tempWorkspace = ""
	s.mu.Unlock()
	if tmp != "" {
		_ = os.RemoveAll(tmp)
	}
	return err
}

// waitTurns waits (bounded by the shutdown timeout) for cancelled turns to store what they produced, so
// nothing is written after the core closes the database.
func (s *Service) waitTurns() {
	done := make(chan struct{})
	safego.Go("agent.turns.wait", func() { s.turns.Wait(); close(done) })
	select {
	case <-done:
	case <-time.After(s.limits.MCPShutdownTimeout):
		s.log.Warn("agent: turns still running at shutdown")
	}
}

// runTurn runs one turn on its provider. Text and tool events reach the UI (and the turn's guard) through
// emitChat as they stream; endTurn stores the result.
func (s *Service) runTurn(ctx context.Context, t *turn, client Transport, sessionID, prompt string, history []domainagent.ChatMessage, ui UIContext) error {
	if s.isPerTurn(t.providerID) {
		return s.runCLITurn(ctx, t, prompt, history, ui)
	}
	if client == nil {
		return fmt.Errorf("agent transport is not connected")
	}
	if sessionID == "" {
		cwd, err := s.ensureWorkspaceDir()
		if err != nil {
			return err
		}
		newSessionID, err := client.NewSession(ctx, cwd, s.systemPrompt(ctx, t.providerID))
		if err != nil {
			return err
		}
		s.applyACPSessionModels(ctx, t.providerID, client, newSessionID)
		s.mu.Lock()
		if s.connSeq.Load() == t.connToken {
			s.activeSessionID = newSessionID
			s.sessionPrimed = false
		}
		s.mu.Unlock()
		sessionID = newSessionID
	}
	s.mu.Lock()
	primed := s.sessionPrimed
	if s.connSeq.Load() == t.connToken {
		s.sessionPrimed = true
	}
	s.mu.Unlock()
	sessionKey := "acp:" + sessionID
	var turnPrompt string
	if primed {
		turnPrompt = buildHistoryContextPrompt(nil, s.screenContextForTurn(sessionKey, ui), prompt, s.limits)
	} else {
		turnPrompt = buildHistoryContextPrompt(history, ui, prompt, s.limits)
	}
	err := client.Prompt(ctx, sessionID, turnPrompt)
	if err == nil {
		s.rememberScreenContext(sessionKey, ui)
	}
	return err
}

// runCLITurn runs a turn as its own CLI invocation (Codex, Claude), resuming the provider's session when
// there is one and falling back to a fresh session primed with the history when it is gone.
func (s *Service) runCLITurn(ctx context.Context, t *turn, prompt string, history []domainagent.ChatMessage, ui UIContext) error {
	mcpSrv, err := s.ensureMCPServer()
	if err != nil {
		return err
	}
	workDir, err := s.ensureWorkspaceDir()
	if err != nil {
		return err
	}
	spec := domainagent.Registry[t.providerID]
	resolved, _, _, err := s.resolveExecutable(ctx, spec)
	if err != nil {
		return err
	}
	if resolved == "" {
		return fmt.Errorf("%s executable not found", spec.ExecutableLabel)
	}
	drv := s.driver(t.providerID)
	run := func(withHistory bool) error {
		h := history
		sessionKey := "" // a new session always receives the screen context
		if !withHistory {
			h = nil // a resumed CLI session already holds the conversation
			sessionKey = cliSessionKey(t.providerID, s.cliSessionFor(t.providerID))
		}
		fullPrompt := buildHistoryContextPrompt(h, s.screenContextForTurn(sessionKey, ui), prompt, s.limits)
		err := drv.runTurn(ctx, resolved, workDir, mcpSrv.url, mcpSrv.token, t.id, fullPrompt)
		if err == nil {
			s.rememberScreenContext(cliSessionKey(t.providerID, s.cliSessionFor(t.providerID)), ui)
		}
		return err
	}
	resumed := s.cliSessionFor(t.providerID) != ""
	err = run(!resumed)
	if err != nil && resumed && spec.IsSessionGone(err) {
		// The saved session is gone (profile reset, CLI update): start a new one with the history.
		s.setCLISession(t.providerID, "")
		err = run(true)
	}
	return err
}

// endTurn is the single place a turn ends, whatever ended it: the guard's held text is released, the
// sanitized reply (and the error, if any) is stored in the turn's chat, the UI gets done/error, and the
// service goes idle. It runs once per turn; later calls (e.g. the turn goroutine after a process exit
// already ended it) do nothing.
func (s *Service) endTurn(t *turn, err error) {
	t.endOnce.Do(func() {
		s.flushGuard(t)
		// A user cancel ends the turn normally: the partial reply is kept and nothing is reported as an error.
		if errors.Is(err, errTurnCancelled) {
			err = nil
		}
		s.storeReply(t)

		s.mu.Lock()
		if s.turn == t {
			s.turn = nil
		}
		if err != nil && isAuthError(err) && s.connSeq.Load() == t.connToken {
			s.states[t.providerID] = domainagent.StateUnauthenticated
			s.authStates[t.providerID] = domainagent.AuthUnauthenticated
			s.lastErrors[t.providerID] = err
			delete(s.authProbes, t.providerID) // the next probe must re-check instead of serving the cache
		}
		s.mu.Unlock()

		if err != nil {
			if appendErr := s.repo.AppendChatMessage(context.Background(), domainagent.ChatMessage{
				ID:             t.id + "-err",
				ProviderID:     t.providerID,
				Role:           "error",
				Text:           err.Error(),
				CreatedAt:      time.Now().UnixMilli(),
				ConversationID: t.conversationID,
			}); appendErr != nil {
				s.log.Warn("agent: failed to persist turn error", "error", appendErr)
			}
			key, params := messageOf(err)
			s.emitChatRaw(domainagent.ChatEvent{
				Type:        domainagent.ChatEventError,
				ProviderID:  t.providerID,
				MessageID:   t.id,
				Error:       err.Error(),
				ErrorKey:    key,
				ErrorParams: params,
			})
		} else {
			s.emitChatRaw(domainagent.ChatEvent{Type: domainagent.ChatEventDone, ProviderID: t.providerID, MessageID: t.id})
		}
		s.emitStatusOf(context.Background(), t.providerID)
	})
}

// storeReply saves the turn's reply. Only the guard-sanitized text is ever stored: history is replayed
// into later prompts and shown in the UI.
func (s *Service) storeReply(t *turn) {
	text, thought := t.guard.sanitizedText(), t.guard.sanitizedThought()
	if strings.TrimSpace(text) == "" && strings.TrimSpace(thought) == "" {
		return
	}
	now := time.Now().UnixMilli()
	ctx := context.Background()
	if err := s.repo.AppendChatMessage(ctx, domainagent.ChatMessage{
		ID:             t.id,
		ProviderID:     t.providerID,
		Role:           "assistant",
		Text:           text,
		Thought:        thought,
		Tool:           t.toolList(),
		CreatedAt:      now,
		ConversationID: t.conversationID,
	}); err != nil {
		s.log.Warn("agent: failed to persist assistant message", "error", err)
	}
	if t.conversationID != "" {
		if err := s.repo.SaveConversation(ctx, domainagent.Conversation{ID: t.conversationID, ProviderID: t.providerID, CreatedAt: now, UpdatedAt: now}); err != nil {
			s.log.Warn("agent: failed to save conversation", "error", err)
		}
	}
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

func (s *Service) ensureMCPServer() (*localMCPServer, error) {
	s.mu.Lock()
	if s.mcpSrv != nil {
		srv := s.mcpSrv
		s.mu.Unlock()
		return srv, nil
	}
	s.mu.Unlock()

	srv, err := startLocalMCPServer(s, s.log)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	if s.closing {
		s.mu.Unlock()
		srv.close()
		return nil, fmt.Errorf("agent service closing")
	}
	if s.mcpSrv != nil {
		existing := s.mcpSrv
		s.mu.Unlock()
		srv.close()
		return existing, nil
	}
	s.mcpSrv = srv
	s.mu.Unlock()
	return srv, nil
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
	return firstLineTrimmedMax(string(out), 64)
}

func firstLineTrimmedMax(s string, maxLen int) string {
	line := firstLineTrimmed(s)
	if len(line) > maxLen {
		return line[:maxLen]
	}
	return line
}

func (s *Service) ensureWorkspaceDir() (string, error) {
	if s.workspaceDir == "" {
		// Created once and reused for every turn (stable cwd for resumed sessions); removed on Close.
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.tempWorkspace != "" {
			if st, err := os.Stat(s.tempWorkspace); err == nil && st.IsDir() {
				return s.tempWorkspace, nil
			}
		}
		dir, err := os.MkdirTemp("", "dezuxk-agent-*")
		if err != nil {
			return "", fmt.Errorf("agent: create temp workspace: %w", err)
		}
		s.tempWorkspace = dir
		return dir, nil
	}
	if err := os.MkdirAll(s.workspaceDir, 0o700); err != nil {
		return "", fmt.Errorf("agent: create workspace dir: %w", err)
	}
	return s.workspaceDir, nil
}

func (s *Service) emitStatus(info domainagent.ProviderInfo) {
	if s.onStatusChanged != nil {
		s.onStatusChanged(info)
	}
}

// turnFor returns the running turn when messageID is its ID ("" = whatever turn is running).
func (s *Service) turnFor(messageID string) *turn {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.turn == nil || (messageID != "" && s.turn.id != messageID) {
		return nil
	}
	return s.turn
}

// emitChat is the single exit point for streamed chat events. Answer and reasoning text passes through
// the running turn's output guard so source code never reaches the UI, whichever provider produced it;
// text that belongs to no running turn (a cancelled turn's process still writing) is dropped.
func (s *Service) emitChat(evt domainagent.ChatEvent) {
	t := s.turnFor(evt.MessageID)
	switch evt.Type {
	case domainagent.ChatEventDelta, domainagent.ChatEventThought:
		if t == nil {
			return
		}
		if evt.Type == domainagent.ChatEventDelta {
			evt.Text = t.guard.filterText(evt.Text)
		} else {
			evt.Text = t.guard.filterThought(evt.Text)
		}
		if evt.Text == "" {
			return
		}
	case domainagent.ChatEventToolCall:
		if t != nil {
			t.noteTool(evt.Tool)
		}
	}
	s.emitChatRaw(evt)
}

func (s *Service) emitChatRaw(evt domainagent.ChatEvent) {
	s.touchTurn(evt.MessageID)
	if s.onChatEvent != nil {
		s.onChatEvent(evt)
	}
}

// touchTurn re-arms the running turn's idle timeout; messageID "" means "whatever turn is running"
// (MCP tool calls do not know the message they belong to).
func (s *Service) touchTurn(messageID string) {
	if t := s.turnFor(messageID); t != nil && t.touch != nil {
		t.touch()
	}
}

// flushGuard finalizes the turn's guard and emits whatever safe text it was still holding.
func (s *Service) flushGuard(t *turn) {
	textTail, thoughtTail := t.guard.finalize()
	if thoughtTail != "" {
		s.emitChatRaw(domainagent.ChatEvent{Type: domainagent.ChatEventThought, ProviderID: t.providerID, MessageID: t.id, Text: thoughtTail})
	}
	if textTail != "" {
		s.emitChatRaw(domainagent.ChatEvent{Type: domainagent.ChatEventDelta, ProviderID: t.providerID, MessageID: t.id, Text: textTail})
	}
	if t.guard.blocked() {
		s.log.Warn("agent: output guard blocked source code in assistant reply", "provider", t.providerID, "message_id", t.id)
	}
}

func (s *Service) emitApprovals() {
	if s.onApprovalsChanged != nil {
		s.onApprovalsChanged(s.approvals.list())
	}
}

func cliSessionKey(id domainagent.ProviderID, sessionID string) string {
	if sessionID == "" {
		return ""
	}
	return "cli:" + string(id) + ":" + sessionID
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func (s *Service) cliSessionFor(id domainagent.ProviderID) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cliSessions[id]
}

func (s *Service) setCLISession(id domainagent.ProviderID, sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sessionID == "" {
		delete(s.cliSessions, id)
		return
	}
	s.cliSessions[id] = sessionID
}
