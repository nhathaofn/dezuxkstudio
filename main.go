package main

import (
	"embed"
	"fmt"
	"log/slog"
	"os"

	"app/internal/config"
	"app/internal/logger"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// Metadata variables dynamically overridable at build time via -ldflags:
// -ldflags="-X main.AppName=... -X main.AppVersion=... -X main.GitCommit=... -X main.BuildDate=..."
var (
	AppName    = "DezuxkStudio"
	AppVersion = "1.0.0"
	GitCommit  = "dev"
	BuildDate  = ""
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	cfg, err := config.LoadConfig(os.Args[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "[Main] Failed to load config: %v\n", err)
		os.Exit(1)
	}

	logPath := cfg.LogFile
	if logPath == "" {
		logPath = logger.GetDefaultLogPath(AppName)
	}
	logger.Init(logger.ParseLevel(cfg.LogLevel), cfg.LogFormat == "json", logPath)
	defer logger.Close()

	application := NewApp(cfg)
	runtimeHandler := NewRuntimeConfigHandler(application)

	windowTitle := cfg.WindowTitle
	if windowTitle == "" {
		windowTitle = fmt.Sprintf("%s v%s", AppName, AppVersion)
	}

	err = wails.Run(&options.App{
		Title:       windowTitle,
		Width:       cfg.WindowWidth,
		Height:      cfg.WindowHeight,
		Frameless:   cfg.WindowFrameless,
		StartHidden: cfg.WindowStartHidden,
		AssetServer: &assetserver.Options{
			Assets:  assets,
			Handler: runtimeHandler, // Serves /runtime-config.json dynamically
		},
		BackgroundColour: &options.RGBA{R: 15, G: 17, B: 23, A: 1},
		OnStartup:        application.startup,
		OnShutdown:       application.shutdown,
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId: fmt.Sprintf("com.%s.lock", AppName),
			OnSecondInstanceLaunch: func(secondInstanceData options.SecondInstanceData) {
				if application.ctx != nil {
					wailsruntime.WindowUnminimise(application.ctx)
					wailsruntime.Show(application.ctx)
				}
			},
		},
		Bind: []interface{}{
			application,
		},
	})

	if err != nil {
		slog.Error("[Main] Application runtime error", "error", err)
	}
}
