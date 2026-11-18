package server

import (
	"log/slog"
	"net/http"
	"time"

	"app/internal/engine"
	"app/internal/realtime"
	"app/internal/security"
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
	MediaDirFunc          func() string
	StagingDir            string
	MaxUploadBytes        int64
	TicketSigner          *security.TicketSigner
	AssetResolver         handlers.AssetResolver
	FileGuard             engine.FileGuard
	Logger                *slog.Logger
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

	stagingDir := o.StagingDir
	if stagingDir == "" && o.MediaDir != "" {
		stagingDir = o.MediaDir
	}

	mediaDirGetter := o.MediaDirFunc
	if mediaDirGetter == nil && o.MediaDir != "" {
		mediaDirGetter = func() string { return o.MediaDir }
	}

	if stagingDir != "" {
		auth := middleware.RequireAuth(o.AuthToken, o.AllowAnonymous)
		mux.Handle(PathMediaUpload, cors(auth(handlers.MediaUpload(stagingDir, o.MaxUploadBytes))))
	}
	if mediaDirGetter != nil || o.MediaDir != "" {
		opts := []handlers.MediaDownloadOption{
			handlers.WithTicketSigner(o.TicketSigner),
			handlers.WithAssetResolver(o.AssetResolver),
			handlers.WithAuth(o.AuthToken, o.AllowAnonymous),
		}
		if mediaDirGetter != nil {
			opts = append(opts, handlers.WithMediaDirFunc(mediaDirGetter))
		}
		if o.FileGuard != nil {
			opts = append(opts, handlers.WithFileGuard(o.FileGuard))
		}
		mux.Handle(PathMediaDownload, cors(handlers.MediaDownload(
			o.MediaDir,
			opts...,
		)))
	}

	var handler http.Handler = mux
	if o.Logger != nil {
		handler = middleware.RequestLogger(o.Logger)(handler)
	}
	return handler
}
