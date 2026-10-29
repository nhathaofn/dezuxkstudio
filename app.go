package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"app/internal/config"
	"app/internal/realtime"
	"app/internal/server"
)

// App manages the application runtime lifecycle.
type App struct {
	ctx      context.Context
	cfg      *config.Config
	server   *server.Server
	hub      *realtime.Hub
	mu       sync.RWMutex
	activeWS string
}

// NewApp creates a new App application struct.
func NewApp(cfg *config.Config) *App {
	return &App{
		cfg:      cfg,
		activeWS: cfg.RemoteServerURL,
	}
}

// SetActiveWS updates the active websocket URL thread-safely.
func (a *App) SetActiveWS(url string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.activeWS = url
}

// GetActiveWS returns current websocket URL thread-safely, waiting up to 4s if server is starting up.
func (a *App) GetActiveWS() string {
	for i := 0; i < 40; i++ {
		a.mu.RLock()
		ws := a.activeWS
		a.mu.RUnlock()
		if ws != "" {
			return ws
		}
		time.Sleep(100 * time.Millisecond)
	}
	return ""
}

// startup is called when the desktop window starts.
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx

	// In All-in-One mode, start Go Core Server dynamically
	if a.cfg.Mode == config.ModeAllInOne {
		a.hub = realtime.NewHub(a.cfg.AllowedOrigins, a.cfg.AuthToken)
		a.server = server.NewServer(a.cfg, a.hub)

		if err := a.server.Start(ctx); err != nil {
			slog.Error("[App] Failed to start internal server", "error", err)
			return
		}

		a.SetActiveWS(a.server.GetWSURL())
	}
}

// shutdown is called when the desktop app window is closing.
func (a *App) shutdown(ctx context.Context) {
	if a.server != nil {
		_ = a.server.Stop(ctx)
	}
}

// RuntimeConfigHandler serves dynamic configuration to frontend without exposing methods to Wails Bind.
type RuntimeConfigHandler struct {
	app *App
}

// NewRuntimeConfigHandler creates the handler instance.
func NewRuntimeConfigHandler(app *App) *RuntimeConfigHandler {
	return &RuntimeConfigHandler{app: app}
}

// ServeHTTP dynamically handles runtime configuration requests.
func (h *RuntimeConfigHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/runtime-config.json" {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"ws_url": h.app.GetActiveWS(),
			"mode":   string(h.app.cfg.Mode),
		})
		return
	}
	http.NotFound(w, r)
}
