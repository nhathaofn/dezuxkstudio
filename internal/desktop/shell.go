package desktop

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"app/internal/buildinfo"
	"app/internal/config"
	"app/internal/contracts"
	"app/internal/core"
	"app/internal/paths"
	"app/internal/platform"
	"app/internal/safego"
	"app/internal/secrets"
	"app/internal/security"
)

// Options configures Run.
type Options struct {
	Config     *config.Config
	Paths      paths.Paths
	Logger     *slog.Logger
	Assets     fs.FS
	Secrets    secrets.Store
	HTTPClient *http.Client
	// StartupError, when set, skips the core and shows the fatal screen with this error
	// (e.g. invalid configuration). The window still opens so the user sees why.
	StartupError error
}

// shell owns the desktop lifecycle.
type shell struct {
	o      Options
	bridge *Bridge
	ctx    atomic.Pointer[context.Context]

	mu      sync.Mutex
	core    *core.Core
	closing bool
}

// Run blocks until the window is closed.
func Run(o Options) error {
	cfg := o.Config
	bg, err := config.ParseHexColor(cfg.Desktop.Background)
	if err != nil {
		return err
	}
	s := &shell{o: o, bridge: newBridge(cfg, o.Secrets)}

	return wails.Run(&options.App{
		Title:            cfg.WindowTitle(),
		Width:            cfg.Desktop.Width,
		Height:           cfg.Desktop.Height,
		MinWidth:         cfg.Desktop.MinWidth,
		MinHeight:        cfg.Desktop.MinHeight,
		Frameless:        cfg.Desktop.Frameless,
		StartHidden:      cfg.Desktop.StartHidden,
		BackgroundColour: &options.RGBA{R: bg.R, G: bg.G, B: bg.B, A: bg.A},
		AssetServer: &assetserver.Options{
			Assets:     o.Assets,
			Middleware: securityHeaders(cfg),
		},
		OnStartup:  s.startup,
		OnShutdown: s.shutdown,
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId:               "com." + strings.ToLower(buildinfo.AppID) + ".desktop",
			OnSecondInstanceLaunch: s.secondInstance,
		},
		ErrorFormatter: func(err error) any { return contracts.NewErrorBody(err) },
		Bind:           []any{s.bridge},
	})
}

func (s *shell) startup(ctx context.Context) {
	s.ctx.Store(&ctx)
	if s.o.StartupError != nil {
		s.fatal(s.o.StartupError)
		return
	}
	// Opening the database and running migrations must not block the UI thread.
	safego.Go("desktop.startup", func() {
		if err := s.startCore(ctx); err != nil {
			s.fatal(err)
			return
		}
		wailsruntime.EventsEmit(ctx, contracts.NativeEventReady, contracts.Empty{})
	})
}

func (s *shell) startCore(ctx context.Context) error {
	cfg := s.o.Config
	if cfg.Mode == config.ModeClient {
		s.bridge.succeed(connectionInfo(cfg, cfg.Client.RemoteURL, cfg.Client.AuthToken))
		return nil
	}

	token, err := security.NewToken()
	if err != nil {
		return err
	}
	c, err := core.New(ctx, core.Options{
		Config:        cfg,
		Paths:         s.o.Paths,
		Logger:        s.o.Logger,
		AuthToken:     token,
		OpenDirectory: platform.OpenDirectory,
		HTTPClient:    s.o.HTTPClient,
	})
	if err != nil {
		return err
	}
	if err := c.Start(); err != nil {
		_ = c.Close()
		return err
	}

	s.mu.Lock()
	if s.closing {
		// The window was closed while the core was starting.
		s.mu.Unlock()
		return c.Close()
	}
	s.core = c
	s.mu.Unlock()

	safego.Go("desktop.watchCore", func() {
		if err, ok := <-c.Errors(); ok && err != nil {
			s.fatal(fmt.Errorf("core server stopped: %w", err))
		}
	})
	s.bridge.succeed(connectionInfo(cfg, c.WSURL(), token))
	return nil
}

func (s *shell) fatal(err error) {
	s.o.Logger.Error("desktop: fatal startup error", "error", err)
	s.bridge.fail(err)
	if ctx := s.ctx.Load(); ctx != nil {
		wailsruntime.EventsEmit(*ctx, contracts.NativeEventFatal, contracts.NewErrorBody(err))
	}
}

func (s *shell) shutdown(context.Context) {
	s.mu.Lock()
	s.closing = true
	c := s.core
	s.core = nil
	s.mu.Unlock()
	if c == nil {
		return
	}
	if err := c.Close(); err != nil {
		s.o.Logger.Warn("desktop: core shutdown incomplete", "error", err)
	}
}

func (s *shell) secondInstance(options.SecondInstanceData) {
	if ctx := s.ctx.Load(); ctx != nil {
		wailsruntime.WindowUnminimise(*ctx)
		wailsruntime.Show(*ctx)
	}
}
