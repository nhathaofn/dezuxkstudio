// Package core is the composition root shared by the desktop and headless binaries.
// It wires storage → services → realtime modules → HTTP server and owns their lifecycle.
//
// Adding a feature normally means one new entry in modules() below.
package core

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"app/internal/buildinfo"
	"app/internal/config"
	"app/internal/contracts"
	domainagent "app/internal/domain/agent"
	domainprefs "app/internal/domain/preferences"
	domainprovider "app/internal/domain/provider"
	"app/internal/engine"
	"app/internal/httpclient"
	"app/internal/paths"
	"app/internal/realtime"
	"app/internal/realtime/modules"
	"app/internal/safego"
	"app/internal/secrets"
	"app/internal/server"
	"app/internal/server/handlers"
	agentsvc "app/internal/service/agent"
	agentclisvc "app/internal/service/agentcli"
	binariessvc "app/internal/service/binaries"
	"app/internal/service/diagnostics"
	mediagensvc "app/internal/service/mediagen"
	"app/internal/service/preferences"
	providersvc "app/internal/service/provider"
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
	log      *slog.Logger
	db       *sqlite.DB
	hub      *realtime.Hub
	srv      *server.Server
	agentSvc *agentsvc.Service
	provSvc  *providersvc.Service
	// background work tied to the core lifetime (e.g. the start-up provider key check)
	bgCtx     context.Context
	bgCancel  context.CancelFunc
	bgWG      sync.WaitGroup
	shutdown  time.Duration
	startedAt time.Time
}

// New opens storage, builds services and mounts every module. On error, everything
// opened so far is released.
func New(ctx context.Context, o Options) (_ *Core, err error) {
	if err := o.validate(); err != nil {
		return nil, err
	}
	cfg := o.Config
	c := &Core{log: o.Logger, shutdown: cfg.Server.ShutdownTimeout, startedAt: time.Now()}
	defer func() {
		if err != nil && c.db != nil {
			_ = c.db.Close()
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

	mods, err := c.modules(o)
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
			MediaDir: o.Paths.Media(),
		}),
	})
	if err != nil {
		return nil, err
	}
	if mediaDir := o.Paths.Media(); mediaDir != "" {
		c.background("media-staging-cleanup", func(ctx context.Context) {
			handlers.RunStagingCleanup(ctx, mediaDir, o.Logger)
		})
	}
	return c, nil
}

// modules is the single list of feature modules mounted into the hub.
func (c *Core) modules(o Options) ([]realtime.Module, error) {
	cfg := o.Config
	// One secret store for every service: OS keyring, else an encrypted file in the data dir, else memory.
	secretStore := o.Secrets
	if secretStore == nil {
		secretStore = secrets.NewDurableStore(buildinfo.AppID, secrets.Dir(o.Paths.Data))
	}

	sysSvc := system.New(cfg.Mode, o.Paths, o.OpenDirectory)

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

	prefSvc, err := preferences.New(sqlite.NewPreferencesRepository(c.db), func(p domainprefs.Preferences) {
		c.hub.Publish(contracts.EventPreferencesUpdated, contracts.NewPreferences(p))
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
		binariessvc.WithProbeTimeout(cfg.Binaries.ProbeTimeout), binariessvc.WithHTTPClient(o.HTTPClient))

	var provSvc *providersvc.Service
	provSvc, err = providersvc.New(providersvc.Options{
		Repo:        sqlite.NewProviderRepository(c.db),
		Secrets:     secretStore,
		Client:      o.HTTPClient,
		TestTimeout: cfg.Providers.TestTimeout,
		AutoCheck:   cfg.Providers.AutoCheck,
		LimitDefaults: domainprovider.LimitDefaults{MaxWorkers: cfg.Providers.DefaultMaxWorkers,
			RPM: cfg.Providers.DefaultRPM, RequestGapMs: cfg.Providers.DefaultRequestGapMs},
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

	mediaGenSvc := mediagensvc.New(mediagensvc.Options{
		Providers:  provSvc,
		HTTPClient: o.HTTPClient,
		MediaDir:   o.Paths.Media(),
		Config:     cfg.MediaGen,
		Logger:     o.Logger,
	})

	wfSvc, err := workflow.New(workflow.Options{
		Paths:     o.Paths,
		Logger:    o.Logger,
		Providers: provSvc,
		MediaGen:  mediaGenSvc,
	})
	if err != nil {
		return nil, err
	}

	// The app's own copies of the agent CLIs live in one folder of its data; nothing installed elsewhere is touched.
	agentCLIs := agentclisvc.New(agentclisvc.Options{
		Dir: o.Paths.AgentCLIs(), Client: o.HTTPClient, Logger: o.Logger, UserAgent: buildinfo.ProductName + "/" + buildinfo.Version,
	})

	workDir := o.Paths.AgentWorkspace()
	if customWS := strings.TrimSpace(cfg.Agent.WorkspaceDir); customWS != "" {
		if !filepath.IsAbs(customWS) {
			workDir = filepath.Join(o.Paths.Data, customWS)
		} else {
			workDir = filepath.Clean(customWS)
		}
	}

	agentSvc, err := agentsvc.New(agentsvc.Options{
		Repo:              sqlite.NewAgentRepository(c.db),
		DataDir:           o.Paths.Data,
		WorkspaceDir:      workDir,
		MediaDir:          o.Paths.Media(),
		ProfilesDir:       o.Paths.AgentProfiles(),
		CustomizationsDir: o.Paths.AgentCustomizations(),
		DeveloperMode:     cfg.DeveloperMode(),
		AppVersion:        buildinfo.Version,
		Secrets:           secretStore,
		Limits:            agentsvc.LimitsFrom(cfg),
		Logger:            o.Logger,
		Workflow:          wfSvc,
		Binaries:          binSvc,
		Managed:           agentCLIs,
		AutoInstall:       cfg.Agent.AutoInstallCLI,
		AutoUpdate:        cfg.Agent.AutoUpdateCLI,
		Preferences:       prefSvc,
		System:            sysSvc,
		Diagnostics:       diagSvc,
		OnStatusChanged: func(info domainagent.ProviderInfo) {
			c.hub.Publish(contracts.EventAgentStatusChanged, contracts.NewAgentProviderInfo(info))
		},
		OnChatEvent: func(evt domainagent.ChatEvent) {
			c.hub.Publish(contracts.EventAgentChatEvent, contracts.NewAgentChatEvent(evt))
		},
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
	c.agentSvc = agentSvc
	c.background("agent-provision-clis", agentSvc.ProvisionCLIs)
	if cfg.Agent.RestoreConnection {
		c.background("agent-restore-connection", agentSvc.RestoreConnection)
	}

	lockRemoteHost := cfg.Mode == config.ModeServer && !cfg.Server.AllowHostAdmin

	return []realtime.Module{
		modules.System{Info: sysSvc, Update: updSvc, LockHostAdmin: lockRemoteHost},
		modules.Preferences{Service: prefSvc},
		modules.Diagnostics{Service: diagSvc},
		modules.Binaries{Service: binSvc, LockHostAdmin: lockRemoteHost},
		modules.Workflow{Service: wfSvc, FileGuard: workflowFileGuard(cfg.Mode, agentSvc)},
		modules.Agent{
			Service:            agentSvc,
			LockTrustedFolders: cfg.Mode == config.ModeServer,
			LockHostAdmin:      lockRemoteHost,
			LockCustomizations: lockRemoteHost,
		},
		modules.Providers{Service: provSvc},
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
// stops the HTTP listener, then closes storage — in that order, so no action ever
// runs against a closed database.
func (c *Core) Close() error {
	if c.bgCancel != nil {
		c.bgCancel()
		// Bound background wait separately so a stalled startup task cannot exhaust the session drain timeout.
		bgBudget := c.shutdown / 2
		if bgBudget <= 0 {
			bgBudget = c.shutdown
		}
		bgCtx, bgCancel := context.WithTimeout(context.Background(), bgBudget)
		done := make(chan struct{})
		safego.Go("core-background-wait", func() { c.bgWG.Wait(); close(done) })
		select {
		case <-done:
		case <-bgCtx.Done():
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
	hubCtx, hubCancel := context.WithTimeout(context.Background(), c.shutdown)
	defer hubCancel()
	return errors.Join(
		agentErr,
		c.hub.Close(hubCtx),
		c.srv.Stop(),
		c.db.Close(),
	)
}
