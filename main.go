// Command DezuxkStudio is the desktop application (desktop and client modes).
package main

import (
	"embed"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime/debug"

	"app/internal/buildinfo"
	"app/internal/config"
	"app/internal/desktop"
	"app/internal/logger"
	"app/internal/paths"
	"app/internal/safego"
	"app/internal/secrets"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() { os.Exit(run()) }

func run() (code int) {
	log := logger.Fallback()
	slog.SetDefault(log)

	appPaths, pathErr := paths.Resolve(buildinfo.AppID, paths.Overrides{})
	defer func() {
		if r := recover(); r != nil {
			reportCrash(appPaths.Logs, "main", r, debug.Stack())
			code = 2
		}
	}()
	if pathErr != nil {
		log.Error("cannot resolve application directories", "error", pathErr)
		return 1
	}

	envPrefix := config.EnvPrefixFor(buildinfo.AppID)
	exeDir := ""
	if exe, err := os.Executable(); err == nil {
		exeDir = filepath.Dir(exe)
	}
	res, cfgErr := config.Load(config.Source{
		Mode:      config.ModeDesktop,
		Args:      os.Args[1:],
		LookupEnv: os.LookupEnv,
		EnvPrefix: envPrefix,
		ConfigDir: appPaths.Config,
		ExeDir:    exeDir,
	})
	if errors.Is(cfgErr, flag.ErrHelp) {
		config.Usage(os.Stdout, config.ModeDesktop, envPrefix)
		return 0
	}

	// A broken configuration still opens the window (with defaults) so the user can
	// read the error on the fatal screen instead of seeing nothing.
	cfg := config.Default(config.ModeDesktop)
	if cfgErr == nil {
		cfg = res.Config
		if appPaths, pathErr = paths.Resolve(buildinfo.AppID, paths.Overrides{DataDir: cfg.Storage.DataDir, LogDir: cfg.Log.Dir}); pathErr != nil {
			cfgErr = pathErr
		}
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
		"mode", cfg.Mode, "config_file", fileOf(res), "data_dir", appPaths.Data)
	if res != nil {
		for _, w := range res.Warnings {
			lg.Warn("configuration warning", "detail", w)
		}
	}
	if cfgErr != nil {
		lg.Error("configuration error", "error", cfgErr)
	}

	dist, err := fs.Sub(assets, "frontend/dist")
	if err != nil {
		lg.Error("embedded frontend missing", "error", err)
		return 1
	}

	err = desktop.Run(desktop.Options{
		Config:       cfg,
		Paths:        appPaths,
		Logger:       lg.Logger,
		Assets:       dist,
		Secrets:      secrets.NewKeyring(buildinfo.AppID),
		HTTPClient:   &http.Client{Timeout: cfg.Update.Timeout},
		StartupError: cfgErr,
	})
	if err != nil {
		lg.Error("desktop runtime error", "error", err)
		return 1
	}
	lg.Info("stopped")
	return 0
}

func fileOf(res *config.Result) string {
	if res == nil {
		return ""
	}
	return res.File
}

func reportCrash(dir, source string, v any, stack []byte) {
	if dir == "" {
		fmt.Fprintf(os.Stderr, "panic in %s: %v\n%s\n", source, v, stack)
		return
	}
	path, err := logger.WriteCrashDump(dir, source, v, stack)
	if err != nil {
		fmt.Fprintf(os.Stderr, "panic in %s: %v (crash dump failed: %v)\n%s\n", source, v, err, stack)
		return
	}
	fmt.Fprintf(os.Stderr, "panic in %s: %v (crash dump: %s)\n", source, v, path)
}
