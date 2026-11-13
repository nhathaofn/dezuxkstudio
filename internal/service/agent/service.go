// Package agent coordinates isolated AI agent discovery, authentication, MCP tooling, and chat sessions.
package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	domainagent "app/internal/domain/agent"
	"app/internal/safego"
	"app/internal/secrets"
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
	Repo                    domainagent.Repository
	DataDir                 string
	WorkspaceDir            string
	MediaDir                string
	ProfilesDir             string
	CustomizationsDir       string
	DeveloperMode           bool // opt-in unrestricted developer/coding mode (disabled by default)
	AppVersion              string
	Limits                  Limits        // zero fields use DefaultLimits
	Secrets                 secrets.Store // OS credential store for provider API keys (in-memory when nil)
	Logger                  *slog.Logger
	Workflow                WorkflowRunner
	Binaries                BinariesManager
	Managed                 ManagedCLIs // the app's own agent CLI copies; nil keeps using what is on the machine
	AutoInstall             bool        // install a missing managed copy in the background
	AutoUpdate              bool        // move managed copies to newer official releases
	Preferences             PreferencesManager
	System                  SystemInspector
	Diagnostics             DiagnosticsExporter
	OnStatusChanged         func(domainagent.ProviderInfo)
	OnChatEvent             func(domainagent.ChatEvent)
	OnApprovalsChanged      func([]domainagent.ApprovalRequest)
	OnLoginPrompt           func(domainagent.LoginPrompt) // the page/code the user must use to finish a running sign-in
	OnCustomizationsChanged func(domainagent.CustomizationsSnapshot)
	SpawnTransport          TransportFactory
	RunCodexTurn            CodexTurnRunner
	ProbeCodexAuth          CodexAuthProber
	RunClaudeTurn           CodexTurnRunner
	ProbeClaudeAuth         CodexAuthProber
}

// Service manages isolated external AI agent connections, MCP tooling, approvals, and chat turns.
type Service struct {
	// statusRevision numbers the status snapshots (ProviderInfo.Revision); it only ever grows.
	statusRevision atomic.Int64

	repo                    domainagent.Repository
	dataDir                 string
	workspaceDir            string
	mediaDir                string
	profilesDir             string
	customizationsDir       string
	developerMode           bool
	appVersion              string
	limits                  Limits
	log                     *slog.Logger
	workflow                WorkflowRunner
	binaries                BinariesManager
	managed                 ManagedCLIs
	autoInstall             bool
	autoUpdate              bool
	provisioning            map[domainagent.ProviderID]provisionState
	onLoginPrompt           func(domainagent.LoginPrompt)
	loginMu                 sync.Mutex
	logins                  map[domainagent.ProviderID]*loginSession
	preferences             PreferencesManager
	system                  SystemInspector
	diagnostics             DiagnosticsExporter
	onStatusChanged         func(domainagent.ProviderInfo)
	onChatEvent             func(domainagent.ChatEvent)
	onApprovalsChanged      func([]domainagent.ApprovalRequest)
	onCustomizationsChanged func(domainagent.CustomizationsSnapshot)
	spawnTransport          TransportFactory
	spawnTransportCustom    bool
	runCodex                CodexTurnRunner
	probeCodex              CodexAuthProber
	runClaude               CodexTurnRunner
	probeClaude             CodexAuthProber

	drivers        map[domainagent.ProviderID]providerDriver
	policy         *AccessPolicy
	approvals      *approvalStore
	secrets        profileSecrets // API keys of the isolated provider profiles
	customizations *customizationManager
	subagentSem    chan struct{}

	toolDocsOnce sync.Once
	docs         []toolDoc // registered MCP tools, see toolDocs

	mu                      sync.Mutex
	mcpSrv                  *localMCPServer
	activeID                domainagent.ProviderID
	activeClient            Transport
	activeSessionID         string
	activeSessionPromptHash string
	activeInit              *InitializeResult
	activeVersion           string
	turn                    *turn                             // the running chat turn; nil when idle
	runningTurns            map[*turn]struct{}                // turns whose goroutine has not finished cleanup/storage
	turns                   sync.WaitGroup                    // running turn goroutines, waited for on Close
	sessionPrimed           bool                              // the active ACP session already received the conversation history
	cliSessions             map[domainagent.ProviderID]string // resumable Codex thread / Claude session per provider
	models                  *modelState
	uiContext               UIContext
	states                  map[domainagent.ProviderID]domainagent.ConnectionState
	authStates              map[domainagent.ProviderID]domainagent.AuthState
	accountInfos            map[domainagent.ProviderID]string
	lastErrors              map[domainagent.ProviderID]error       // may carry an i18n key (localizedError)
	cachedVersions          map[domainagent.ProviderID]cachedProbe // CLI --version results
	authProbes              map[domainagent.ProviderID]cachedProbe // when the CLI auth status was last checked
	tempWorkspace           string                                 // fallback workspace when none is configured
	sentScreen              map[string]string                      // screen-context block last sent per provider session
	conversationID          string                                 // current chat; "" until its first message (new chat on every start)
	conversationProvider    domainagent.ProviderID                 // provider owning conversationID; "" when unassigned

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
	custDir := strings.TrimSpace(o.CustomizationsDir)
	if custDir == "" && strings.TrimSpace(o.DataDir) != "" {
		custDir = filepath.Join(o.DataDir, "agent-customizations")
	}
	var custMgr *customizationManager
	if custDir != "" {
		custMgr = newCustomizationManager(custDir)
		if syncErr := custMgr.EnsureSynced(); syncErr != nil {
			o.Logger.Warn("agent: failed to sync customizations", "error", syncErr)
		}
	}
	s := &Service{
		repo:                    o.Repo,
		dataDir:                 o.DataDir,
		workspaceDir:            o.WorkspaceDir,
		mediaDir:                o.MediaDir,
		profilesDir:             o.ProfilesDir,
		customizationsDir:       custDir,
		developerMode:           o.DeveloperMode,
		appVersion:              version,
		limits:                  limits,
		log:                     o.Logger,
		workflow:                o.Workflow,
		binaries:                o.Binaries,
		managed:                 o.Managed,
		autoInstall:             o.AutoInstall,
		autoUpdate:              o.AutoUpdate,
		provisioning:            make(map[domainagent.ProviderID]provisionState),
		onLoginPrompt:           o.OnLoginPrompt,
		logins:                  make(map[domainagent.ProviderID]*loginSession),
		preferences:             o.Preferences,
		system:                  o.System,
		diagnostics:             o.Diagnostics,
		onStatusChanged:         o.OnStatusChanged,
		onChatEvent:             o.OnChatEvent,
		onApprovalsChanged:      o.OnApprovalsChanged,
		onCustomizationsChanged: o.OnCustomizationsChanged,
		spawnTransport:          o.SpawnTransport,
		spawnTransportCustom:    o.SpawnTransport != nil,
		runCodex:                o.RunCodexTurn,
		probeCodex:              o.ProbeCodexAuth,
		runClaude:               o.RunClaudeTurn,
		probeClaude:             o.ProbeClaudeAuth,
		policy:                  NewAccessPolicy(o.DataDir, o.WorkspaceDir, o.MediaDir, o.ProfilesDir, allowedDirs),
		secrets:                 profileSecrets{store: secretStore},
		approvals:               newApprovalStore(limits.ApprovalTTL, limits.MaxPendingApprovals),
		customizations:          custMgr,
		subagentSem:             make(chan struct{}, limits.MaxConcurrentSubagents),
		models:                  newModelState(),
		runningTurns:            make(map[*turn]struct{}),
		cliSessions:             make(map[domainagent.ProviderID]string),
		states:                  make(map[domainagent.ProviderID]domainagent.ConnectionState),
		authStates:              make(map[domainagent.ProviderID]domainagent.AuthState),
		accountInfos:            make(map[domainagent.ProviderID]string),
		lastErrors:              make(map[domainagent.ProviderID]error),
		cachedVersions:          make(map[domainagent.ProviderID]cachedProbe),
		authProbes:              make(map[domainagent.ProviderID]cachedProbe),
		sentScreen:              make(map[string]string),

		codexAppServerBroken: make(map[string]bool),
		spawnCodexAppServer:  startCodexAppServer,
	}
	s.drivers = newDrivers(s)
	if s.spawnTransport == nil {
		// The process outlives the Connect call that starts it, so it is not bound to that call's ctx;
		// Disconnect/Close end it.
		s.spawnTransport = func(_ context.Context, executable string, args []string, workDir string, env []string, onEvent func(SessionUpdate), onExit func(error)) (Transport, error) {
			return s.spawnScopedTransport(context.Background(), executable, args, workDir, env, s.toolNames(), onEvent, onExit)
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
	s.policy.SetDeveloperMode(o.DeveloperMode)
	return s, nil
}

// DeveloperMode reports whether the agent runs in opt-in developer mode.
func (s *Service) DeveloperMode() bool { return s.developerMode }

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

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func promptDigest(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func (s *Service) spawnScopedTransport(ctx context.Context, executable string, args []string, workDir string, env, ownTools []string, onEvent func(SessionUpdate), onExit func(error)) (Transport, error) {
	client, spawnErr := SpawnACP(ctx, executable, args, workDir, env, ownTools, s.log, onEvent, onExit)
	if spawnErr != nil {
		return nil, spawnErr
	}
	if s.developerMode {
		client.SetAllowAllPermissions(true)
	}
	return client, nil
}

func (s *Service) spawnSubagentTransport(ctx context.Context, executable string, args []string, workDir string, env, ownTools []string, onEvent func(SessionUpdate), onExit func(error)) (Transport, error) {
	if s.spawnTransportCustom && s.spawnTransport != nil {
		return s.spawnTransport(ctx, executable, args, workDir, env, onEvent, onExit)
	}
	return s.spawnScopedTransport(ctx, executable, args, workDir, env, ownTools, onEvent, onExit)
}

func (s *Service) availableSubagentTools() []string {
	all := s.toolNames()
	out := make([]string, 0, len(all))
	for _, name := range all {
		if name == "subagent_run" {
			continue
		}
		out = append(out, name)
	}
	return out
}

func (s *Service) sanitizeSubagentTools(tools []string) []string {
	if len(tools) == 0 {
		return nil
	}
	allowed := make(map[string]bool)
	for _, name := range s.availableSubagentTools() {
		allowed[name] = true
	}
	seen := make(map[string]bool)
	var clean []string
	for _, t := range tools {
		t = strings.TrimSpace(t)
		if t == "" || t == "subagent_run" || !allowed[t] || seen[t] {
			continue
		}
		seen[t] = true
		clean = append(clean, t)
	}
	return clean
}

func (s *Service) notifyCustomizationsChanged(snap domainagent.CustomizationsSnapshot) {
	if s.onCustomizationsChanged != nil {
		s.onCustomizationsChanged(snap)
	}
}

// ListCustomizations returns all shared skills and subagents together with available delegatable tools.
func (s *Service) ListCustomizations(_ context.Context) (domainagent.CustomizationsSnapshot, error) {
	avail := s.availableSubagentTools()
	if s.customizations == nil {
		return domainagent.CustomizationsSnapshot{
			Dir:            s.customizationsDir,
			AvailableTools: avail,
		}, nil
	}
	return s.customizations.Snapshot(avail)
}

// ReloadCustomizations re-syncs disk customizations with built-ins and emits an updated snapshot.
func (s *Service) ReloadCustomizations(_ context.Context) (domainagent.CustomizationsSnapshot, error) {
	avail := s.availableSubagentTools()
	if s.customizations == nil {
		return domainagent.CustomizationsSnapshot{
			Dir:            s.customizationsDir,
			AvailableTools: avail,
		}, nil
	}
	if err := s.customizations.EnsureSynced(); err != nil {
		return domainagent.CustomizationsSnapshot{}, err
	}
	snap, err := s.customizations.Snapshot(avail)
	if err != nil {
		return domainagent.CustomizationsSnapshot{}, err
	}
	s.notifyCustomizationsChanged(snap)
	return snap, nil
}

// SaveSkill creates or updates a shared skill markdown file on disk.
func (s *Service) SaveSkill(ctx context.Context, name, description string, userInvocable, enabled bool, body string) (domainagent.CustomizationsSnapshot, error) {
	if s.customizations == nil {
		return domainagent.CustomizationsSnapshot{}, fmt.Errorf("customizations storage is not configured")
	}
	if err := s.customizations.SaveSkill(strings.TrimSpace(strings.ToLower(name)), description, userInvocable, enabled, body); err != nil {
		return domainagent.CustomizationsSnapshot{}, err
	}
	return s.ReloadCustomizations(ctx)
}

// DeleteSkill removes a skill (or tombstones a built-in skill so upgrades do not re-create it).
func (s *Service) DeleteSkill(ctx context.Context, name string) (domainagent.CustomizationsSnapshot, error) {
	if s.customizations == nil {
		return domainagent.CustomizationsSnapshot{}, fmt.Errorf("customizations storage is not configured")
	}
	if err := s.customizations.Delete(domainagent.CustomizationSkill, strings.TrimSpace(strings.ToLower(name))); err != nil {
		return domainagent.CustomizationsSnapshot{}, err
	}
	return s.ReloadCustomizations(ctx)
}

// RestoreSkill restores a built-in skill back to its default content.
func (s *Service) RestoreSkill(ctx context.Context, name string) (domainagent.CustomizationsSnapshot, error) {
	if s.customizations == nil {
		return domainagent.CustomizationsSnapshot{}, fmt.Errorf("customizations storage is not configured")
	}
	if err := s.customizations.RestoreBuiltIn(domainagent.CustomizationSkill, strings.TrimSpace(strings.ToLower(name))); err != nil {
		return domainagent.CustomizationsSnapshot{}, err
	}
	return s.ReloadCustomizations(ctx)
}

// SaveSubagent creates or updates a shared subagent markdown file on disk.
func (s *Service) SaveSubagent(ctx context.Context, spec domainagent.SubagentInfo) (domainagent.CustomizationsSnapshot, error) {
	if s.customizations == nil {
		return domainagent.CustomizationsSnapshot{}, fmt.Errorf("customizations storage is not configured")
	}
	spec.Name = strings.TrimSpace(strings.ToLower(spec.Name))
	spec.Tools = s.sanitizeSubagentTools(spec.Tools)
	if err := s.customizations.SaveSubagent(spec); err != nil {
		return domainagent.CustomizationsSnapshot{}, err
	}
	return s.ReloadCustomizations(ctx)
}

// DeleteSubagent removes a subagent (or tombstones a built-in subagent so upgrades do not re-create it).
func (s *Service) DeleteSubagent(ctx context.Context, name string) (domainagent.CustomizationsSnapshot, error) {
	if s.customizations == nil {
		return domainagent.CustomizationsSnapshot{}, fmt.Errorf("customizations storage is not configured")
	}
	if err := s.customizations.Delete(domainagent.CustomizationSubagent, strings.TrimSpace(strings.ToLower(name))); err != nil {
		return domainagent.CustomizationsSnapshot{}, err
	}
	return s.ReloadCustomizations(ctx)
}

// RestoreSubagent restores a built-in subagent back to its default content.
func (s *Service) RestoreSubagent(ctx context.Context, name string) (domainagent.CustomizationsSnapshot, error) {
	if s.customizations == nil {
		return domainagent.CustomizationsSnapshot{}, fmt.Errorf("customizations storage is not configured")
	}
	if err := s.customizations.RestoreBuiltIn(domainagent.CustomizationSubagent, strings.TrimSpace(strings.ToLower(name))); err != nil {
		return domainagent.CustomizationsSnapshot{}, err
	}
	return s.ReloadCustomizations(ctx)
}
