package agent

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"app/internal/safego"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type localMCPServer struct {
	http  *http.Server
	url   string
	token string
}

func startLocalMCPServer(s *Service, log *slog.Logger) (*localMCPServer, error) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("agent mcp: listen: %w", err)
	}
	var secret [32]byte
	if _, err := rand.Read(secret[:]); err != nil {
		_ = listener.Close()
		return nil, fmt.Errorf("agent mcp: generate token: %w", err)
	}
	addr := listener.Addr().String()
	token := hex.EncodeToString(secret[:])
	server := s.buildMCPServer()
	handler := mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return server },
		&mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true},
	)

	srv := &localMCPServer{
		url:   "http://" + addr + "/mcp",
		token: token,
	}
	srv.http = &http.Server{
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       30 * time.Second,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Host != addr || r.URL.Path != "/mcp" {
				http.Error(w, "Forbidden", http.StatusForbidden)
				return
			}
			if origin := r.Header.Get("Origin"); origin != "" && origin != "http://"+addr {
				http.Error(w, "Forbidden origin", http.StatusForbidden)
				return
			}
			if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+token)) != 1 {
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, 4<<20)
			handler.ServeHTTP(w, r)
		}),
	}

	safego.Go("agent.mcp.http", func() {
		if err := srv.http.Serve(listener); err != nil && err != http.ErrServerClosed {
			log.Warn("agent mcp server stopped", "error", err)
		}
	})
	return srv, nil
}

func (m *localMCPServer) close() {
	if m == nil || m.http == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = m.http.Shutdown(ctx)
	_ = m.http.Close()
}
