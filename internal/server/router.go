package server

import (
	"net/http"
	"time"

	"app/internal/realtime"
	"app/internal/server/handlers"
	"app/internal/server/middleware"
)

// Route paths.
const (
	PathWS            = "/ws"
	PathHealth        = "/health"
	PathRuntimeConfig = "/runtime-config.json"
	PathBroadcast     = "/api/events/broadcast"
	PathMediaUpload   = "/api/media/upload"
	PathMediaDownload = "/api/media/download"
)

// RouterOptions configures NewRouter.
type RouterOptions struct {
	Hub                   *realtime.Hub
	Mode                  string
	Version               string
	StartedAt             time.Time
	AuthToken             string
	AllowAnonymous        bool
	AllowedOrigins        []string
	EnableBroadcast       bool
	MaxBroadcastBodyBytes int64
	DeclaredEvents        []string
	WSURL                 func() string
	Timeouts              handlers.Timeouts
	MediaDir              string
	MaxUploadBytes        int64
}

// NewRouter builds the HTTP routes. Endpoints exposing state never reveal the token.
func NewRouter(o RouterOptions) http.Handler {
	mux := http.NewServeMux()
	cors := middleware.CORS(o.AllowedOrigins)

	mux.Handle(PathWS, o.Hub)
	mux.Handle("GET "+PathHealth, handlers.Health(o.Version, o.StartedAt, o.Hub.ClientCount))
	mux.Handle(PathRuntimeConfig, cors(handlers.RuntimeConfig(o.Mode, o.Version, o.Timeouts, o.WSURL)))

	if o.EnableBroadcast {
		mux.Handle(PathBroadcast, cors(
			middleware.RequireToken(o.AuthToken)(
				middleware.RequireJSON(
					handlers.Broadcast(o.Hub, o.DeclaredEvents, o.MaxBroadcastBodyBytes)))))
	}
	if o.MediaDir != "" {
		auth := middleware.RequireAuth(o.AuthToken, o.AllowAnonymous)
		mux.Handle(PathMediaUpload, cors(auth(handlers.MediaUpload(o.MediaDir, o.MaxUploadBytes))))
		mux.Handle(PathMediaDownload, cors(auth(handlers.MediaDownload(o.MediaDir))))
	}
	return mux
}
