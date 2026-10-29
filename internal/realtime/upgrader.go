package realtime

import (
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/coder/websocket"
)

// Upgrader handles upgrading standard HTTP connections to WebSockets.
type Upgrader struct {
	hub            *Hub
	allowedOrigins []string
	authToken      string
}

// NewUpgrader creates an upgrader instance.
func NewUpgrader(hub *Hub, allowedOrigins []string, authToken string) *Upgrader {
	return &Upgrader{
		hub:            hub,
		allowedOrigins: allowedOrigins,
		authToken:      authToken,
	}
}

// ServeHTTP implements http.Handler to upgrade connections.
func (u *Upgrader) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// 1. Verify AuthToken if configured
	if u.authToken != "" {
		token := r.URL.Query().Get("token")
		if token == "" {
			token = r.Header.Get("X-Auth-Token")
		}
		if token == "" {
			authHeader := r.Header.Get("Authorization")
			if strings.HasPrefix(authHeader, "Bearer ") {
				token = strings.TrimPrefix(authHeader, "Bearer ")
			}
		}
		if token != u.authToken {
			slog.Warn("[Realtime] Unauthorized connection rejected", "remote_addr", r.RemoteAddr)
			http.Error(w, "Unauthorized: invalid auth token", http.StatusUnauthorized)
			return
		}
	}

	// 2. Validate Origin
	origins := u.allowedOrigins
	if len(origins) == 0 {
		origins = []string{"*"}
	}

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns: origins,
	})
	if err != nil {
		slog.Warn("[Realtime] WebSocket upgrade failed", "error", err, "remote_addr", r.RemoteAddr)
		return
	}

	clientID := fmt.Sprintf("client_%d", time.Now().UnixNano())
	client := NewClient(clientID, u.hub, conn)
	u.hub.Register(client)

	ctx := r.Context()
	go client.WritePump(ctx)
	client.ReadPump(ctx)
}
