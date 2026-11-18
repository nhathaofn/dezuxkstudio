// Package core is the composition root shared by the desktop and headless binaries.
// It wires storage → services → realtime modules → HTTP server and owns their lifecycle.
//
// Adding a feature normally means one new entry in modules() below.
package core

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"app/internal/apperr"
	"app/internal/buildinfo"
	"app/internal/config"
	"app/internal/contracts"
	domainagent "app/internal/domain/agent"
	domainprefs "app/internal/domain/preferences"
	domainprovider "app/internal/domain/provider"
	domainstudio "app/internal/domain/studio"
	"app/internal/engine"
	"app/internal/httpclient"
	"app/internal/paths"
	"app/internal/realtime"
	"app/internal/realtime/modules"
	"app/internal/safego"
	"app/internal/secrets"
	"app/internal/security"
	"app/internal/server"
	"app/internal/server/handlers"
	agentsvc "app/internal/service/agent"
	agentclisvc "app/internal/service/agentcli"
	binariessvc "app/internal/service/binaries"
	"app/internal/service/diagnostics"
	mediasvc "app/internal/service/media"
	mediagensvc "app/internal/service/mediagen"
	"app/internal/service/preferences"
	providersvc "app/internal/service/provider"
	studiosvc "app/internal/service/studio"
	"app/internal/service/system"
	"app/internal/service/update"
	"app/internal/service/workflow"
	"app/internal/storage/sqlite"
)

// Options are the externally provided dependencies.
type Options struct {
	Config         *config.Config
	Paths          paths.Paths
	Logger         *slog.Logger
	AuthToken      string // effective access token (desktop: per-session random)
	AllowAnonymous bool   // server mode on loopback without token
	OpenDirectory  func(dir string) error
	HTTPClient     *http.Client
	// Secrets is the app secret store; nil builds secrets.NewDurableStore in the data dir. The desktop
	// shell passes its own so the bridge and the core share one store (one owner of the encrypted file).
	Secrets secrets.Store
}

func (o Options) validate() error {
	if o.Config == nil || o.Logger == nil || o.OpenDirectory == nil || o.HTTPClient == nil {
		return errors.New("core: incomplete options")
	}
	return nil
}

// Core is a fully wired, not yet started application core.
type Core struct {
	log           *slog.Logger
	db            *sqlite.DB
	hub           *realtime.Hub
	srv           *server.Server
	agentSvc      *agentsvc.Service
	provSvc       *providersvc.Service
	mediaSvc      *mediasvc.Service
	mediaGenSvc   *mediagensvc.Service
	studioSvc     *studiosvc.Service
	prefSvc       *preferences.Service
	chatCoalescer *chatEventCoalescer
	workspaceTree atomic.Pointer[paths.WorkspaceTree]
	// background work tied to the core lifetime (e.g. the start-up provider key check)
	bgCtx     context.Context
	bgCancel  context.CancelFunc
	bgWG      sync.WaitGroup
	shutdown  time.Duration
	startedAt time.Time
}

// WorkspaceTree returns the active workspace tree snapshot.
func (c *Core) WorkspaceTree() paths.WorkspaceTree {
	if ptr := c.workspaceTree.Load(); ptr != nil {
		return *ptr
	}
	return paths.WorkspaceTree{}
}

// Preferences returns the core preferences service.
func (c *Core) Preferences() *preferences.Service { return c.prefSvc }

// Agent returns the core agent service.
func (c *Core) Agent() *agentsvc.Service { return c.agentSvc }

// MediaGen returns the core media generation service.
func (c *Core) MediaGen() *mediagensvc.Service { return c.mediaGenSvc }

// Studio returns the core AI film studio service.
func (c *Core) Studio() *studiosvc.Service { return c.studioSvc }

// New opens storage, builds services and mounts every module. On error, everything
// opened so far is released.
func New(ctx context.Context, o Options) (_ *Core, err error) {
	if err := o.validate(); err != nil {
		return nil, err
	}
	cfg := o.Config
	c := &Core{log: o.Logger, shutdown: cfg.Server.ShutdownTimeout, startedAt: time.Now()}
	defer func() {
		if err != nil {
			_ = c.Close()
		}
	}()

	c.db, err = sqlite.Open(ctx, sqlite.Options{
		Path:          filepath.Join(o.Paths.Data, cfg.Storage.DBFile),
		BackupDir:     o.Paths.Backups(),
		BackupsToKeep: cfg.Storage.BackupsToKeep,
		BusyTimeout:   cfg.Storage.BusyTimeout,
		Logger:        o.Logger,
	})
	if err != nil {
		return nil, fmt.Errorf("core: storage: %w", err)
	}

	c.hub, err = realtime.New(realtime.Options{
		AuthToken:            o.AuthToken,
		AllowAnonymous:       o.AllowAnonymous,
		AllowedOrigins:       cfg.Server.AllowedOrigins,
		MaxConcurrentActions: cfg.Realtime.MaxConcurrentActions,
		ActionTimeout:        cfg.Realtime.ActionTimeout,
		LongActionTimeout:    cfg.Realtime.LongActionTimeout,
		QueueTimeout:         cfg.Realtime.QueueTimeout,
		ClientBufferSize:     cfg.Realtime.ClientBufferSize,
		MaxMessageBytes:      cfg.Realtime.MaxMessageBytes,
		PingInterval:         cfg.Realtime.PingInterval,
		WriteTimeout:         cfg.Realtime.WriteTimeout,
		KeepAliveInterval:    cfg.Realtime.KeepAliveInterval,
		DeclaredActions:      contracts.ActionNames(),
		DeclaredEvents:       contracts.EventNames(),
		Logger:               o.Logger,
	})
	if err != nil {
		return nil, err
	}

	ticketSecret := make([]byte, 32)
	if _, err := rand.Read(ticketSecret); err != nil {
		return nil, fmt.Errorf("core: ticket secret: %w", err)
	}
	ticketSigner := security.NewTicketSigner(ticketSecret)

	mods, err := c.modules(ctx, o, ticketSigner)
	if err != nil {
		return nil, err
	}
	if err := c.hub.Mount(mods...); err != nil {
		return nil, err
	}
	if err := c.hub.VerifyComplete(); err != nil {
		return nil, err
	}

	c.srv, err = server.New(server.Options{
		Host:              cfg.Server.Host,
		Port:              cfg.Server.Port,
		ReadHeaderTimeout: cfg.Server.ReadHeaderTimeout,
		IdleTimeout:       cfg.Server.IdleTimeout,
		Logger:            o.Logger,
		Handler: server.NewRouter(server.RouterOptions{
			Hub:                   c.hub,
			Mode:                  string(cfg.Mode),
			Version:               buildinfo.Version,
			StartedAt:             c.startedAt,
			AuthToken:             o.AuthToken,
			AllowAnonymous:        o.AllowAnonymous,
			AllowedOrigins:        cfg.Server.AllowedOrigins,
			EnableBroadcast:       cfg.Server.EnableBroadcast,
			MaxBroadcastBodyBytes: cfg.Server.MaxBroadcastBodyBytes,
			DeclaredEvents:        contracts.EventNames(),
			WSURL:                 func() string { return c.srv.WSURL() },
			Timeouts: handlers.Timeouts{
				Action:       cfg.Realtime.ActionTimeout,
				LongAction:   cfg.Realtime.LongActionTimeout,
				PingInterval: cfg.Realtime.PingInterval,
			},
			MediaDir:       c.WorkspaceTree().MediaDir,
			MediaDirFunc:   func() string { return c.WorkspaceTree().MediaDir },
			StagingDir:     o.Paths.Staging(),
			MaxUploadBytes: cfg.Server.MaxMediaUploadBytes,
			TicketSigner:   ticketSigner,
			AssetResolver:  c.mediaSvc,
			FileGuard:      workflowFileGuard(cfg.Mode, c.agentSvc),
			Logger:         o.Logger,
		}),
	})
	if err != nil {
		return nil, err
	}
	if stagingDir := o.Paths.Staging(); stagingDir != "" {
		mediaGenStagingDir := filepath.Clean(o.Paths.MediaGenStaging())
		c.background("media-staging-cleanup", func(ctx context.Context) {
			handlers.RunStagingCleanupWithGuard(
				ctx,
				stagingDir,
				cfg.Server.MediaStagingTTL,
				cfg.Server.MediaStagingCleanupInterval,
				o.Logger,
				func(p string) bool {
					if strings.EqualFold(filepath.Clean(p), mediaGenStagingDir) {
						return true
					}
					return c.mediaGenSvc != nil && c.mediaGenSvc.IsLeased(p)
				},
			)
		})
	}
	return c, nil
}

// modules is the single list of feature modules mounted into the hub.
func (c *Core) modules(ctx context.Context, o Options, ticketSigner *security.TicketSigner) ([]realtime.Module, error) {
	cfg := o.Config
	// One secret store for every service: OS keyring, else an encrypted file in the data dir, else memory.
	secretStore := o.Secrets
	if secretStore == nil {
		secretStore = secrets.NewDurableStore(buildinfo.AppID, secrets.Dir(o.Paths.Data))
	}

	var (
		prefMu      sync.Mutex
		lastPrefs   domainprefs.Preferences
		agentSvc    *agentsvc.Service
		mediaSvc    *mediasvc.Service
		mediaGenSvc *mediagensvc.Service
		studioSvc   *studiosvc.Service
	)

	onChange := func(p domainprefs.Preferences) {
		prefMu.Lock()
		oldPrefs := lastPrefs
		lastPrefs = p
		prefMu.Unlock()

		rootChanged := p.WorkspaceRoot != oldPrefs.WorkspaceRoot
		skipChanged := p.AgentSkipTrustedApproval != oldPrefs.AgentSkipTrustedApproval

		if rootChanged {
			prevTree := c.WorkspaceTree()

			// 1. If previous root was custom (prevRoot != ""), persist it in user allowed dirs as well.
			if oldPrefs.WorkspaceRoot != "" && agentSvc != nil {
				if _, err := agentSvc.AddAllowedDir(context.Background(), oldPrefs.WorkspaceRoot); err != nil {
					o.Logger.WarnContext(context.Background(), "preferences: failed to keep previous workspace root in allowed dirs", "error", err)
				}
			}
			// 2. Update atomic pointer to new tree and ensure directories exist.
			newTree := paths.ResolveWorkspaceTree(o.Paths, p.WorkspaceRoot, cfg.Agent.WorkspaceDir)
			if err := newTree.Ensure(); err != nil {
				o.Logger.ErrorContext(context.Background(), "preferences: failed to ensure new workspace tree directories", "error", err)
			}
			c.workspaceTree.Store(&newTree)

			// 3. Retain previous studio/media roots and propagate new workspace roots across services.
			if mediaSvc != nil {
				mediaSvc.AddAllowedRoot(prevTree.MediaDir)
				mediaSvc.AddAllowedRoot(prevTree.WorkspaceDir)
				mediaSvc.AddAllowedRoot(prevTree.ProjectsDir)
				mediaSvc.SetMediaDir(newTree.MediaDir)
				mediaSvc.AddAllowedRoot(newTree.WorkspaceDir)
				mediaSvc.AddAllowedRoot(newTree.ProjectsDir)
			}
			if mediaGenSvc != nil {
				mediaGenSvc.SetMediaDir(newTree.MediaDir)
			}
			if agentSvc != nil {
				agentSvc.AddLegacyRoots(prevTree.WorkspaceDir, prevTree.ProjectsDir, prevTree.MediaDir)
				agentSvc.SetWorkspaceDirs(newTree.WorkspaceDir, newTree.MediaDir)
				agentSvc.SetProjectsDir(newTree.ProjectsDir)
			}
			if studioSvc != nil {
				studioSvc.SetProjectsDir(newTree.ProjectsDir)
			}
		}

		if skipChanged {
			if agentSvc != nil {
				agentSvc.SetSkipTrustedApproval(p.AgentSkipTrustedApproval)
			}
		}

		c.hub.Publish(contracts.EventPreferencesUpdated, contracts.NewPreferences(p))
	}

	validator := func(raw string) (string, error) {
		var norm string
		if trimmed := strings.TrimSpace(raw); trimmed != "" {
			if agentSvc != nil {
				var err error
				norm, err = agentSvc.ValidateWorkspaceRoot(trimmed)
				if err != nil {
					return "", apperr.Validation(err.Error())
				}
			} else if abs, err := filepath.Abs(trimmed); err == nil {
				norm = filepath.Clean(abs)
			} else {
				norm = filepath.Clean(trimmed)
			}
		}
		candidateTree := paths.ResolveWorkspaceTree(o.Paths, norm, cfg.Agent.WorkspaceDir)
		if err := candidateTree.Ensure(); err != nil {
			return "", apperr.Wrap(apperr.CodeValidation, "cannot prepare workspace directory", err)
		}
		return norm, nil
	}

	prefSvc, err := preferences.New(
		sqlite.NewPreferencesRepository(c.db),
		onChange,
		preferences.WithWorkspaceRootValidator(validator),
	)
	if err != nil {
		return nil, fmt.Errorf("core: preferences service: %w", err)
	}
	c.prefSvc = prefSvc

	initPrefs, err := prefSvc.Get(ctx)
	if err != nil {
		return nil, fmt.Errorf("core: preferences initial get: %w", err)
	}
	prefMu.Lock()
	lastPrefs = initPrefs
	prefMu.Unlock()

	tree := paths.ResolveWorkspaceTree(o.Paths, initPrefs.WorkspaceRoot, cfg.Agent.WorkspaceDir)
	if err := tree.Ensure(); err != nil {
		return nil, fmt.Errorf("core: workspace tree: %w", err)
	}
	c.workspaceTree.Store(&tree)

	sysSvc := system.New(cfg.Mode, o.Paths, o.OpenDirectory, system.WithWorkspaceTreeFunc(c.WorkspaceTree))

	updateClient := httpclient.WithTimeout(o.HTTPClient, cfg.Update.Timeout)
	updSvc, err := update.New(update.Options{
		FeedURL:        cfg.Update.FeedURL,
		CurrentVersion: buildinfo.Version,
		UserAgent:      buildinfo.ProductName + "/" + buildinfo.Version,
		Client:         updateClient,
	})
	if err != nil {
		return nil, err
	}

	diagSvc, err := diagnostics.New(diagnostics.Options{
		OutputDir: o.Paths.Diagnostics(),
		LogDir:    o.Paths.Logs,
		Keep:      cfg.Diagnostics.Keep,
		Config:    cfg.Redacted,
		Info:      sysSvc.Info,
	})
	if err != nil {
		return nil, err
	}

	binSvc := binariessvc.New(sqlite.NewBinariesRepository(c.db), o.Paths.Tools(), o.Logger,
		binariessvc.WithProbeTimeout(cfg.Binaries.ProbeTimeout),
		binariessvc.WithHTTPClient(o.HTTPClient),
		binariessvc.WithEventPublisher(c.hub),
		binariessvc.WithConfig(cfg),
	)

	var provSvc *providersvc.Service
	provSvc, err = providersvc.New(providersvc.Options{
		Repo:        sqlite.NewProviderRepository(c.db),
		Secrets:     secretStore,
		Client:      o.HTTPClient,
		TestTimeout: cfg.Providers.TestTimeout,
		AutoCheck:   cfg.Providers.AutoCheck,
		LimitDefaults: domainprovider.LimitDefaults{
			MaxWorkers:   cfg.Providers.DefaultMaxWorkers,
			RPM:          cfg.Providers.DefaultRPM,
			RequestGapMs: cfg.Providers.DefaultRequestGapMs,
		},
		OnChanged: func() {
			// Runs after the mutation committed; a fresh snapshot keeps every client in sync.
			if snap, err := provSvc.List(context.Background()); err == nil {
				c.hub.Publish(contracts.EventProvidersChanged, modules.ListResult(snap))
			}
		},
	})
	if err != nil {
		return nil, err
	}
	c.provSvc = provSvc

	if cfg.Providers.CheckOnStartup {
		c.background("provider-startup-check", func(ctx context.Context) {
			provSvc.CheckAll(ctx, cfg.Providers.CheckConcurrency)
		})
	}

	agentRepo := sqlite.NewAgentRepository(c.db)
	allowedRoots := []string{tree.MediaDir, o.Paths.Staging(), tree.WorkspaceDir, tree.ProjectsDir}
	if storedDirs, err := agentRepo.ListAllowedDirs(ctx); err == nil {
		allowedRoots = append(allowedRoots, storedDirs...)
	}
	var legacyStudioRoots []string
	defaultStudioRoot := o.Paths.DefaultStudioRoot()
	for _, fallbackDir := range []string{
		o.Paths.Media(),
		filepath.Join(defaultStudioRoot, "media"),
		filepath.Join(defaultStudioRoot, "workspace"),
		filepath.Join(defaultStudioRoot, "workspace", "projects"),
	} {
		if fallbackDir != "" {
			if fi, err := os.Stat(fallbackDir); err == nil && fi.IsDir() {
				allowedRoots = append(allowedRoots, fallbackDir)
				legacyStudioRoots = append(legacyStudioRoots, fallbackDir)
			}
		}
	}
	seenRoots := make(map[string]bool)
	var dedupedRoots []string
	for _, root := range allowedRoots {
		cleaned := filepath.Clean(strings.TrimSpace(root))
		if cleaned != "" && !seenRoots[cleaned] {
			seenRoots[cleaned] = true
			dedupedRoots = append(dedupedRoots, cleaned)
		}
	}

	mediaRepo := sqlite.NewMediaRepository(c.db)
	mediaSvc = mediasvc.New(mediasvc.Options{
		Repo:         mediaRepo,
		Signer:       ticketSigner,
		Config:       cfg.Media,
		MediaDir:     tree.MediaDir,
		StagingDir:   o.Paths.Staging(),
		AllowedRoots: dedupedRoots,
		Logger:       o.Logger,
		OnChanged: func() {
			c.hub.Publish(contracts.EventMediaAssetsChanged, contracts.Empty{})
		},
	})
	c.mediaSvc = mediaSvc

	mediaGenSvc = mediagensvc.New(mediagensvc.Options{
		Providers:               provSvc,
		HTTPClient:              o.HTTPClient,
		MediaDir:                tree.MediaDir,
		StagingDir:              o.Paths.MediaGenStaging(),
		Registrar:               mediaSvc,
		Config:                  cfg.MediaGen,
		MaxVideoDurationSec:     cfg.Studio.MaxShotDurationSec,
		DefaultVideoDurationSec: cfg.Studio.DefaultShotDurationSec,
		Logger:                  o.Logger,
		StagingTTL:              cfg.MediaStagingTTL(),
		StagingGCInterval:       cfg.MediaStagingGCInterval(),
	})
	c.mediaGenSvc = mediaGenSvc
	if stagedDir := mediaGenSvc.StagingDir(); stagedDir != "" {
		c.background("mediagen-staging-gc", func(ctx context.Context) {
			mediaGenSvc.StartStagingGCAuto(ctx)
		})
	}

	studioRepo := sqlite.NewStudioRepository(c.db, sqlite.WithStudioConfig(cfg.Studio))
	preflightAdapter := &studioPreflightAdapter{provSvc: provSvc, studioRepo: studioRepo}

	wfSvc, err := workflow.New(workflow.Options{
		Paths:             o.Paths,
		Logger:            o.Logger,
		Config:            cfg,
		Providers:         provSvc,
		MediaGen:          mediaGenSvc,
		Binaries:          binSvc,
		StudioConfig:      cfg.Studio,
		Registrar:         mediaSvc,
		ProviderChecker:   preflightAdapter,
		CostEstimator:     preflightAdapter,
		WorkspaceTreeFunc: c.WorkspaceTree,
	})
	if err != nil {
		return nil, err
	}

	studioSvc = studiosvc.New(studiosvc.Options{
		Repo:          studioRepo,
		Workflow:      wfSvc,
		Media:         mediaSvc,
		VideoMedia:    mediaGenSvc,
		OpenDirectory: o.OpenDirectory,
		StudioConfig:  cfg.Studio,
		ProjectsDir:   tree.ProjectsDir,
		OnEvent: func(eventName string, data any) {
			c.hub.Publish(eventName, data)
		},
		Logger: o.Logger,
	})
	mediaGenSvc.SetUsageHook(studioSvc.HandleMediaUsage)
	c.studioSvc = studioSvc

	// The app's own copies of the agent CLIs live in one folder of its data; nothing installed elsewhere is touched.
	agentCLIs := agentclisvc.New(agentclisvc.Options{
		Dir: o.Paths.AgentCLIs(), Client: o.HTTPClient, Logger: o.Logger, UserAgent: buildinfo.ProductName + "/" + buildinfo.Version,
	})

	c.chatCoalescer = newChatEventCoalescer(35*time.Millisecond, func(evt domainagent.ChatEvent) {
		c.hub.Publish(contracts.EventAgentChatEvent, contracts.NewAgentChatEvent(evt))
	})

	agentSvc, err = agentsvc.New(agentsvc.Options{
		Repo:                agentRepo,
		DataDir:             o.Paths.Data,
		WorkspaceDir:        tree.WorkspaceDir,
		ProjectsDir:         tree.ProjectsDir,
		MediaDir:            tree.MediaDir,
		StagingDir:          o.Paths.Staging(),
		SkipTrustedApproval: initPrefs.AgentSkipTrustedApproval,
		ProfilesDir:         o.Paths.AgentProfiles(),
		CustomizationsDir:   o.Paths.AgentCustomizations(),
		ProtectedDirs:       []string{o.Paths.Config, o.Paths.Logs},
		DeveloperMode:       cfg.DeveloperMode(),
		AppVersion:          buildinfo.Version,
		Secrets:             secretStore,
		Limits:              agentsvc.LimitsFrom(cfg),
		Logger:              o.Logger,
		Workflow:            wfSvc,
		Binaries:            binSvc,
		Managed:             agentCLIs,
		AutoInstall:         cfg.Agent.AutoInstallCLI,
		AutoUpdate:          cfg.Agent.AutoUpdateCLI,
		Preferences:         prefSvc,
		System:              sysSvc,
		Diagnostics:         diagSvc,
		Studio:              studioSvc,
		OnStatusChanged: func(info domainagent.ProviderInfo) {
			c.hub.Publish(contracts.EventAgentStatusChanged, contracts.NewAgentProviderInfo(info))
		},
		OnChatEvent: c.chatCoalescer.Emit,
		OnLoginPrompt: func(p domainagent.LoginPrompt) {
			c.hub.Publish(contracts.EventAgentLoginPrompt, contracts.NewAgentLoginPrompt(p))
		},
		OnApprovalsChanged: func(approvals []domainagent.ApprovalRequest) {
			c.hub.Publish(contracts.EventAgentApprovals, contracts.NewAgentApprovalListResult(approvals))
		},
		OnCustomizationsChanged: func(snap domainagent.CustomizationsSnapshot) {
			c.hub.Publish(contracts.EventAgentCustomizationsChanged, contracts.NewAgentCustomizationsResult(snap))
		},
	})
	if err != nil {
		return nil, err
	}
	prefSvc.SetWorkspaceRootValidator(validator)
	if len(legacyStudioRoots) > 0 {
		agentSvc.AddLegacyRoots(legacyStudioRoots...)
	}
	agentSvc.SetProviderLinks(sqlite.NewProviderRepository(c.db))
	agentSvc.SetMediaInspector(&mediaInspectorAdapter{svc: mediaSvc})
	c.agentSvc = agentSvc
	studioSvc.SetFileGuard(workflowFileGuard(cfg.Mode, agentSvc))
	c.background("studio-queue-worker", studioSvc.RunQueueWorker)
	c.background("agent-provision-clis", agentSvc.ProvisionCLIs)
	if cfg.Agent.RestoreConnection {
		c.background("agent-restore-connection", agentSvc.RestoreConnection)
	}

	lockRemoteHost := cfg.Mode == config.ModeServer && !cfg.Server.AllowHostAdmin

	return []realtime.Module{
		modules.System{Info: sysSvc, Update: updSvc, LockHostAdmin: lockRemoteHost},
		modules.Preferences{
			Service:            prefSvc,
			LockTrustedFolders: cfg.Mode == config.ModeServer,
		},
		modules.Diagnostics{Service: diagSvc, LockHostAdmin: lockRemoteHost},
		modules.Binaries{Service: binSvc, LockHostAdmin: lockRemoteHost},
		modules.Workflow{Service: wfSvc, FileGuard: workflowFileGuard(cfg.Mode, agentSvc)},
		modules.Agent{
			Service:            agentSvc,
			LockTrustedFolders: cfg.Mode == config.ModeServer,
			LockHostAdmin:      lockRemoteHost,
			LockCustomizations: lockRemoteHost,
		},
		modules.Providers{Service: provSvc, LockHostAdmin: lockRemoteHost},
		modules.Media{Service: mediaSvc, FileGuard: workflowFileGuard(cfg.Mode, agentSvc)},
		modules.Studio{Service: studioSvc, FileGuard: workflowFileGuard(cfg.Mode, agentSvc), LockHostAdmin: lockRemoteHost},
	}, nil
}

// workflowFileGuard limits which local files nodes run over WebSocket may touch. The desktop app runs
// for the person sitting at the machine, so it needs no guard; a headless server serves remote clients,
// so their node runs may only touch the app's media/workspace and the folders the user trusted.
func workflowFileGuard(mode config.Mode, agent *agentsvc.Service) engine.FileGuard {
	if mode != config.ModeServer {
		return nil
	}
	return agent.CheckTrustedFile
}

// background runs start-up work tied to the core lifetime: Close cancels it and waits (bounded).
func (c *Core) background(name string, fn func(ctx context.Context)) {
	if c.bgCancel == nil {
		c.bgCtx, c.bgCancel = context.WithCancel(context.Background())
	}
	c.bgWG.Add(1)
	safego.Go(name, func() {
		defer c.bgWG.Done()
		fn(c.bgCtx)
	})
}

// Start begins accepting connections.
func (c *Core) Start() error { return c.srv.Start() }

// WSURL returns the WebSocket endpoint (valid after Start).
func (c *Core) WSURL() string { return c.srv.WSURL() }

// Errors delivers a fatal server error.
func (c *Core) Errors() <-chan error { return c.srv.Errors() }

// Close drains sessions and in-flight actions (bounded by server.shutdown_timeout),
// stops the HTTP listener, waits for background workers and services to stop,
// and closes storage last — so no handler or worker ever runs against a closed database.
func (c *Core) Close() error {
	if c.bgCancel != nil {
		c.bgCancel()
	}
	if c.chatCoalescer != nil {
		c.chatCoalescer.Close()
	}

	var hubErr error
	if c.hub != nil {
		hubCtx, hubCancel := context.WithTimeout(context.Background(), c.shutdown)
		hubErr = c.hub.Close(hubCtx)
		hubCancel()
	}

	var srvErr error
	if c.srv != nil {
		srvErr = c.srv.Stop()
	}

	if c.bgCancel != nil {
		bgBudget := c.shutdown
		if bgBudget < 5*time.Second {
			bgBudget = 5 * time.Second
		}
		bgCtx, bgCancel := context.WithTimeout(context.Background(), bgBudget)
		done := make(chan struct{})
		safego.Go("core-background-wait", func() { c.bgWG.Wait(); close(done) })
		select {
		case <-done:
		case <-bgCtx.Done():
			if c.log != nil {
				c.log.Warn("core: background workers did not finish within shutdown deadline", "timeout", bgBudget)
			}
		}
		bgCancel()
	}

	if c.provSvc != nil {
		c.provSvc.Close() // running key checks write to the database: finish them first
	}
	var agentErr error
	if c.agentSvc != nil {
		agentErr = c.agentSvc.Close()
	}
	var dbErr error
	if c.db != nil {
		dbErr = c.db.Close()
	}
	return errors.Join(
		hubErr,
		srvErr,
		agentErr,
		dbErr,
	)
}

type mediaInspectorAdapter struct {
	svc *mediasvc.Service
}

func (m *mediaInspectorAdapter) InspectAsset(ctx context.Context, assetID string) (*agentsvc.MediaAssetMetadata, error) {
	item, err := m.svc.GetAsset(ctx, assetID)
	if err != nil {
		return nil, err
	}
	if item == nil {
		return nil, fmt.Errorf("media asset %q not found", assetID)
	}
	var metaMap map[string]any
	if strings.TrimSpace(item.MetadataJSON) != "" {
		_ = json.Unmarshal([]byte(item.MetadataJSON), &metaMap)
	}
	return &agentsvc.MediaAssetMetadata{
		AssetID:        item.ID,
		Kind:           item.Kind,
		SourceNode:     item.SourceNode,
		RegisteredRoot: item.RegisteredRoot,
		MIMEType:       item.MIMEType,
		SizeBytes:      item.SizeBytes,
		Width:          item.Width,
		Height:         item.Height,
		DurationSec:    item.DurationSec,
		Metadata:       metaMap,
	}, nil
}

type studioPreflightAdapter struct {
	provSvc    *providersvc.Service
	studioRepo *sqlite.StudioRepository
}

func (a *studioPreflightAdapter) CheckCapability(ctx context.Context, capability domainprovider.Capability, modelRef string) (string, string, error) {
	if a.provSvc == nil {
		return "", "", fmt.Errorf("provider service is unavailable")
	}
	purpose := domainprovider.PurposeImage
	for _, spec := range domainprovider.PurposeSpecs() {
		if spec.Capability == capability {
			purpose = spec.Purpose
			break
		}
	}
	res, err := a.provSvc.ResolveModel(ctx, purpose, modelRef)
	if err != nil {
		return "", "", err
	}
	return res.Ref.ProviderID, res.Model.ModelID, nil
}

func (a *studioPreflightAdapter) EstimateUnitPriceUSD(ctx context.Context, providerID, modelID, capability, unit string) (float64, bool, error) {
	if a.studioRepo == nil {
		return domainstudio.PreflightSpecForCapability(capability).FallbackUnitPriceUSD, true, nil
	}
	entry, ok, err := a.studioRepo.ResolveUnitPrice(ctx, providerID, modelID, capability, unit)
	if err != nil {
		return 0, true, err
	}
	if !ok {
		return domainstudio.PreflightSpecForCapability(capability).FallbackUnitPriceUSD, true, nil
	}
	wildcard := entry.ProviderID == "*" || entry.ModelID == "*"
	return entry.UnitPriceUSD, wildcard, nil
}

const chatCoalesceMaxBytes = 4096

type chatEventCoalescer struct {
	window time.Duration
	emit   func(domainagent.ChatEvent)

	mu      sync.Mutex
	closed  bool
	hasPend bool
	pending domainagent.ChatEvent
	timer   *time.Timer
}

func newChatEventCoalescer(window time.Duration, emit func(domainagent.ChatEvent)) *chatEventCoalescer {
	return &chatEventCoalescer{window: window, emit: emit}
}

func isCoalescibleChatEvent(t domainagent.ChatEventType) bool {
	return t == domainagent.ChatEventDelta ||
		t == domainagent.ChatEventThought ||
		t == domainagent.ChatEventSubagentDelta
}

func sameCoalesceBucket(a, b domainagent.ChatEvent) bool {
	return a.Type == b.Type &&
		a.ProviderID == b.ProviderID &&
		a.MessageID == b.MessageID &&
		a.SubagentID == b.SubagentID &&
		a.SubagentName == b.SubagentName &&
		a.SubagentProvider == b.SubagentProvider
}

func (c *chatEventCoalescer) Emit(evt domainagent.ChatEvent) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	if !isCoalescibleChatEvent(evt.Type) {
		c.flushLocked()
		c.emit(evt)
		return
	}
	if c.hasPend {
		if sameCoalesceBucket(c.pending, evt) {
			c.pending.Text += evt.Text
			if len(c.pending.Text) >= chatCoalesceMaxBytes {
				c.flushLocked()
			}
			return
		}
		c.flushLocked()
	}
	c.pending = evt
	c.hasPend = true
	if c.timer == nil {
		c.timer = time.AfterFunc(c.window, c.flushTimer)
	} else {
		c.timer.Reset(c.window)
	}
}

func (c *chatEventCoalescer) flushTimer() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.closed {
		c.flushLocked()
	}
}

func (c *chatEventCoalescer) flushLocked() {
	if c.timer != nil {
		c.timer.Stop()
	}
	if !c.hasPend {
		return
	}
	evt := c.pending
	c.pending = domainagent.ChatEvent{}
	c.hasPend = false
	c.emit(evt)
}

func (c *chatEventCoalescer) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	c.flushLocked()
	c.closed = true
	if c.timer != nil {
		c.timer.Stop()
	}
}
