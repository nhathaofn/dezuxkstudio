// Package core is the composition root shared by the desktop and headless binaries.
// It wires storage → services → realtime modules → HTTP server and owns their lifecycle.
//
// Adding a feature normally means one new entry in modules() below.
package core

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
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

	var ticketSecret []byte
	if o.AuthToken != "" {
		h := sha256.Sum256([]byte("ticket-signing-secret:" + o.AuthToken))
		ticketSecret = h[:]
	} else {
		ticketSecret = make([]byte, 32)
		if _, err := rand.Read(ticketSecret); err != nil {
			return nil, fmt.Errorf("core: ticket secret: %w", err)
		}
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
			// 1. If previous root was custom (prevRoot != ""), automatically add it to agentSvc.AddAllowedDir
			if oldPrefs.WorkspaceRoot != "" && agentSvc != nil {
				if _, err := agentSvc.AddAllowedDir(context.Background(), oldPrefs.WorkspaceRoot); err != nil {
					o.Logger.Warn("preferences: failed to keep previous workspace root in allowed dirs", "dir", oldPrefs.WorkspaceRoot, "error", err)
				}
			}
			// 2. Update atomic pointer to new tree and ensure directories exist
			newTree := paths.ResolveWorkspaceTree(o.Paths, p.WorkspaceRoot, cfg.Agent.WorkspaceDir)
			if err := newTree.Ensure(); err != nil {
				o.Logger.Error("preferences: failed to ensure new workspace tree directories", "root", newTree.Root, "error", err)
			}
			c.workspaceTree.Store(&newTree)

			// 3. Call mediaSvc.SetMediaDir, mediaGenSvc.SetMediaDir, agentSvc.SetWorkspaceDirs, and studioSvc.SetProjectsDir
			if mediaSvc != nil {
				mediaSvc.SetMediaDir(newTree.MediaDir)
				mediaSvc.AddAllowedRoot(newTree.WorkspaceDir)
				mediaSvc.AddAllowedRoot(newTree.ProjectsDir)
			}
			if mediaGenSvc != nil {
				mediaGenSvc.SetMediaDir(newTree.MediaDir)
			}
			if agentSvc != nil {
				agentSvc.SetWorkspaceDirs(newTree.WorkspaceDir, newTree.MediaDir)
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
		if strings.TrimSpace(raw) != "" {
			if agentSvc != nil {
				var err error
				norm, err = agentSvc.ValidateWorkspaceRoot(raw)
				if err != nil {
					return "", apperr.Validation(err.Error())
				}
			} else {
				norm = filepath.Clean(strings.TrimSpace(raw))
			}
		}
		candidateTree := paths.ResolveWorkspaceTree(o.Paths, norm, cfg.Agent.WorkspaceDir)
		if err := candidateTree.Ensure(); err != nil {
			return "", apperr.Validation(err.Error())
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
	for _, fallbackMedia := range []string{o.Paths.Media(), filepath.Join(o.Paths.DefaultStudioRoot(), "media")} {
		if fallbackMedia != "" {
			if fi, err := os.Stat(fallbackMedia); err == nil && fi.IsDir() {
				allowedRoots = append(allowedRoots, fallbackMedia)
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
		Providers:         provSvc,
		HTTPClient:        o.HTTPClient,
		MediaDir:          tree.MediaDir,
		StagingDir:        o.Paths.MediaGenStaging(),
		Registrar:         mediaSvc,
		Config:            cfg.MediaGen,
		Logger:            o.Logger,
		StagingTTL:        cfg.MediaStagingTTL(),
		StagingGCInterval: cfg.MediaStagingGCInterval(),
	})
	c.mediaGenSvc = mediaGenSvc
	if stagedDir := mediaGenSvc.StagingDir(); stagedDir != "" {
		c.background("mediagen-staging-gc", func(ctx context.Context) {
			mediaGenSvc.StartStagingGCAuto(ctx)
		})
	}

	wfSvc, err := workflow.New(workflow.Options{
		Paths:             o.Paths,
		Logger:            o.Logger,
		Providers:         provSvc,
		MediaGen:          mediaGenSvc,
		Binaries:          binSvc,
		StudioConfig:      cfg.Studio,
		Registrar:         mediaSvc,
		WorkspaceTreeFunc: c.WorkspaceTree,
	})
	if err != nil {
		return nil, err
	}

	studioSvc = studiosvc.New(studiosvc.Options{
		Repo:         sqlite.NewStudioRepository(c.db),
		Workflow:     wfSvc,
		Media:        mediaSvc,
		StudioConfig: cfg.Studio,
		ProjectsDir:  tree.ProjectsDir,
		OnEvent: func(eventName string, data any) {
			c.hub.Publish(eventName, data)
		},
		Logger: o.Logger,
	})
	c.studioSvc = studioSvc

	// The app's own copies of the agent CLIs live in one folder of its data; nothing installed elsewhere is touched.
	agentCLIs := agentclisvc.New(agentclisvc.Options{
		Dir: o.Paths.AgentCLIs(), Client: o.HTTPClient, Logger: o.Logger, UserAgent: buildinfo.ProductName + "/" + buildinfo.Version,
	})

	agentSvc, err = agentsvc.New(agentsvc.Options{
		Repo:                agentRepo,
		DataDir:             o.Paths.Data,
		WorkspaceDir:        tree.WorkspaceDir,
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
	prefSvc.SetWorkspaceRootValidator(validator)
	agentSvc.SetProviderLinks(sqlite.NewProviderRepository(c.db))
	agentSvc.SetMediaInspector(&mediaInspectorAdapter{svc: mediaSvc})
	c.agentSvc = agentSvc
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
		modules.Media{Service: mediaSvc},
		modules.Studio{Service: studioSvc, FileGuard: workflowFileGuard(cfg.Mode, agentSvc)},
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
