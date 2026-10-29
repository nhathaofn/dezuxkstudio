// Command server is the headless core server (server mode).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"syscall"

	"app/internal/buildinfo"
	"app/internal/config"
	"app/internal/core"
	"app/internal/logger"
	"app/internal/paths"
	"app/internal/platform"
	"app/internal/safego"
)

func main() { os.Exit(run()) }

func run() (code int) {
	log := logger.Fallback()
	slog.SetDefault(log)

	appPaths, err := paths.Resolve(buildinfo.ServerAppID, paths.Overrides{})
	if err != nil {
		log.Error("cannot resolve application directories", "error", err)
		return 1
	}
	defer func() {
		if r := recover(); r != nil {
			reportCrash(appPaths.Logs, "main", r, debug.Stack())
			code = 2
		}
	}()

	envPrefix := config.EnvPrefixFor(buildinfo.AppID)
	exeDir := ""
	if exe, err := os.Executable(); err == nil {
		exeDir = filepath.Dir(exe)
	}
	res, err := config.Load(config.Source{
		Mode:      config.ModeServer,
		Args:      os.Args[1:],
		LookupEnv: os.LookupEnv,
		EnvPrefix: envPrefix,
		ConfigDir: appPaths.Config,
		ExeDir:    exeDir,
	})
	if errors.Is(err, flag.ErrHelp) {
		config.Usage(os.Stdout, config.ModeServer, envPrefix)
		return 0
	}
	if err != nil {
		log.Error("configuration error", "error", err)
		return 1
	}
	cfg := res.Config
	if cfg.Mode != config.ModeServer {
		log.Error("this binary only supports mode=server", "mode", cfg.Mode)
		return 1
	}

	if appPaths, err = paths.Resolve(buildinfo.ServerAppID, paths.Overrides{DataDir: cfg.Storage.DataDir, LogDir: cfg.Log.Dir}); err != nil {
		log.Error("cannot resolve application directories", "error", err)
		return 1
	}
	if err := appPaths.Ensure(); err != nil {
		log.Error("cannot create application directories", "error", err)
		return 1
	}

	lg, err := logger.New(logger.Options{
		Level:        cfg.Log.Level,
		Format:       cfg.Log.Format,
		Dir:          appPaths.Logs,
		MaxSizeBytes: cfg.LogMaxSizeBytes(),
		MaxBackups:   cfg.Log.MaxBackups,
		Stdout:       cfg.Log.Stdout,
	})
	if err != nil {
		log.Error("cannot initialise logging", "error", err)
		return 1
	}
	defer func() { _ = lg.Close() }()
	slog.SetDefault(lg.Logger)
	safego.SetPanicHandler(func(name string, v any, stack []byte) { reportCrash(appPaths.Logs, name, v, stack) })

	info := buildinfo.Get()
	lg.Info("starting", "product", info.ProductName, "version", info.Version, "commit", info.Commit,
		"config_file", res.File, "data_dir", appPaths.Data)
	for _, w := range res.Warnings {
		lg.Warn("configuration warning", "detail", w)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	c, err := core.New(ctx, core.Options{
		Config:         cfg,
		Paths:          appPaths,
		Logger:         lg.Logger,
		AuthToken:      cfg.Server.AuthToken,
		AllowAnonymous: cfg.Server.AuthToken == "",
		OpenDirectory:  platform.OpenDirectory,
		HTTPClient:     &http.Client{Timeout: cfg.Update.Timeout},
	})
	if err != nil {
		lg.Error("startup failed", "error", err)
		return 1
	}
	if err := c.Start(); err != nil {
		lg.Error("startup failed", "error", err)
		_ = c.Close()
		return 1
	}
	lg.Info("ready", "ws_url", c.WSURL())

	select {
	case <-ctx.Done():
		lg.Info("shutting down")
	case err, ok := <-c.Errors():
		if ok && err != nil {
			lg.Error("server stopped unexpectedly", "error", err)
			code = 1
		}
	}
	if err := c.Close(); err != nil {
		lg.Warn("shutdown incomplete", "error", err)
	}
	lg.Info("stopped")
	return code
}

func reportCrash(dir, source string, v any, stack []byte) {
	path, err := logger.WriteCrashDump(dir, source, v, stack)
	if err != nil {
		fmt.Fprintf(os.Stderr, "panic in %s: %v (crash dump failed: %v)\n%s\n", source, v, err, stack)
		return
	}
	fmt.Fprintf(os.Stderr, "panic in %s: %v (crash dump: %s)\n", source, v, path)
}
