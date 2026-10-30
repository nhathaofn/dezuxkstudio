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
	"path/filepath"
	"time"

	"app/internal/buildinfo"
	"app/internal/config"
	"app/internal/contracts"
	domainprefs "app/internal/domain/preferences"
	"app/internal/paths"
	"app/internal/realtime"
	"app/internal/realtime/modules"
	"app/internal/server"
	binariessvc "app/internal/service/binaries"
	"app/internal/service/diagnostics"
	"app/internal/service/preferences"
	"app/internal/service/system"
	"app/internal/service/update"
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
	HTTPClient     update.Doer
}

func (o Options) validate() error {
	if o.Config == nil || o.Logger == nil || o.OpenDirectory == nil || o.HTTPClient == nil {
		return errors.New("core: incomplete options")
	}
	return nil
}

// Core is a fully wired, not yet started application core.
type Core struct {
	log       *slog.Logger
	db        *sqlite.DB
	hub       *realtime.Hub
	srv       *server.Server
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
		QueueTimeout:         cfg.Realtime.QueueTimeout,
		ClientBufferSize:     cfg.Realtime.ClientBufferSize,
		MaxMessageBytes:      cfg.Realtime.MaxMessageBytes,
		PingInterval:         cfg.Realtime.PingInterval,
		WriteTimeout:         cfg.Realtime.WriteTimeout,
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
			AllowedOrigins:        cfg.Server.AllowedOrigins,
			EnableBroadcast:       cfg.Server.EnableBroadcast,
			MaxBroadcastBodyBytes: cfg.Server.MaxBroadcastBodyBytes,
			DeclaredEvents:        contracts.EventNames(),
			WSURL:                 func() string { return c.srv.WSURL() },
		}),
	})
	if err != nil {
		return nil, err
	}
	return c, nil
}

// modules is the single list of feature modules mounted into the hub.
func (c *Core) modules(o Options) ([]realtime.Module, error) {
	cfg := o.Config

	sysSvc := system.New(cfg.Mode, o.Paths, o.OpenDirectory)

	updSvc, err := update.New(update.Options{
		FeedURL:        cfg.Update.FeedURL,
		CurrentVersion: buildinfo.Version,
		UserAgent:      buildinfo.ProductName + "/" + buildinfo.Version,
		Client:         o.HTTPClient,
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

	binSvc := binariessvc.New(sqlite.NewBinariesRepository(c.db), o.Paths.Tools(), o.Logger)

	return []realtime.Module{
		modules.System{Info: sysSvc, Update: updSvc},
		modules.Preferences{Service: prefSvc},
		modules.Diagnostics{Service: diagSvc},
		modules.Binaries{Service: binSvc},
	}, nil
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
	ctx, cancel := context.WithTimeout(context.Background(), c.shutdown)
	defer cancel()
	return errors.Join(
		c.hub.Close(ctx),
		c.srv.Stop(),
		c.db.Close(),
	)
}
