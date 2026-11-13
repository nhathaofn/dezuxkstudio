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
	"strings"
	"sync"
	"time"

	domainagent "app/internal/domain/agent"
	"app/internal/progress"
	"app/internal/safego"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// HTTP limits of the local MCP server. Clients are the agent CLIs on this machine: headers arrive at once,
// connections are reused between tool calls of a turn, and a request is one JSON-RPC call.
const (
	mcpReadHeaderTimeout = 5 * time.Second
	mcpIdleTimeout       = 30 * time.Second
	mcpMaxRequestBytes   = 4 << 20
	// mcpHeartbeatsPerTurnTimeout: a running tool re-arms the turn this many times per agent.turn_timeout.
	mcpHeartbeatsPerTurnTimeout = 4
)

type subagentContextKey struct{}

type subagentRunMarker struct {
	ID       string
	Name     string
	Provider domainagent.ProviderID
	TurnID   string
}

func withSubagentContext(ctx context.Context, m subagentRunMarker) context.Context {
	return context.WithValue(ctx, subagentContextKey{}, m)
}

func subagentFromContext(ctx context.Context) (subagentRunMarker, bool) {
	if ctx == nil {
		return subagentRunMarker{}, false
	}
	m, ok := ctx.Value(subagentContextKey{}).(subagentRunMarker)
	return m, ok && m.ID != ""
}

type scopedMCPSession struct {
	handler http.Handler
	marker  subagentRunMarker
}

type localMCPServer struct {
	svc             *Service
	http            *http.Server
	shutdownTimeout time.Duration
	url             string
	token           string

	scopedMu sync.RWMutex
	scoped   map[string]*scopedMCPSession
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
		svc:             s,
		url:             "http://" + addr + "/mcp",
		token:           token,
		shutdownTimeout: s.limits.MCPShutdownTimeout,
		scoped:          make(map[string]*scopedMCPSession),
	}
	srv.http = &http.Server{
		ReadHeaderTimeout: mcpReadHeaderTimeout,
		IdleTimeout:       mcpIdleTimeout,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Host != addr || r.URL.Path != "/mcp" {
				http.Error(w, "Forbidden", http.StatusForbidden)
				return
			}
			if origin := r.Header.Get("Origin"); origin != "" && origin != "http://"+addr {
				http.Error(w, "Forbidden origin", http.StatusForbidden)
				return
			}
			authHeader := r.Header.Get("Authorization")
			var targetHandler http.Handler = handler
			turnID := ""
			reqCtx := r.Context()
			if subtle.ConstantTimeCompare([]byte(authHeader), []byte("Bearer "+token)) != 1 {
				scopedSess := srv.matchScopedSession(authHeader)
				if scopedSess == nil {
					http.Error(w, "Unauthorized", http.StatusUnauthorized)
					return
				}
				targetHandler = scopedSess.handler
				turnID = scopedSess.marker.TurnID
				reqCtx = withSubagentContext(reqCtx, scopedSess.marker)
			}
			r.Body = http.MaxBytesReader(w, r.Body, mcpMaxRequestBytes)
			// A tool call is activity of the running chat turn, and so is any progress the tool
			// reports while it runs (e.g. a workflow): both re-arm the turn's idle timeout.
			s.touchTurn(turnID)
			touch := func() { s.touchTurn(turnID) }
			reqCtx = progress.With(reqCtx, progress.Sink{
				Report:   func(progress.Update) { touch() },
				Beat:     touch,
				Interval: s.limits.TurnTimeout / mcpHeartbeatsPerTurnTimeout,
			})
			targetHandler.ServeHTTP(w, r.WithContext(reqCtx))
		}),
	}

	safego.Go("agent.mcp.http", func() {
		if err := srv.http.Serve(listener); err != nil && err != http.ErrServerClosed {
			log.Warn("agent mcp server stopped", "error", err)
		}
	})
	return srv, nil
}

func (m *localMCPServer) matchScopedSession(authHeader string) *scopedMCPSession {
	rawTok, ok := strings.CutPrefix(authHeader, "Bearer ")
	if !ok || rawTok == "" {
		return nil
	}
	m.scopedMu.RLock()
	defer m.scopedMu.RUnlock()
	for tok, sess := range m.scoped {
		if subtle.ConstantTimeCompare([]byte(rawTok), []byte(tok)) == 1 {
			return sess
		}
	}
	return nil
}

func (m *localMCPServer) registerScopedSession(allowedTools []string, marker subagentRunMarker) (string, error) {
	if m == nil || m.svc == nil {
		return "", fmt.Errorf("agent mcp server is unavailable")
	}
	var secret [32]byte
	if _, err := rand.Read(secret[:]); err != nil {
		return "", fmt.Errorf("agent mcp: generate scoped token: %w", err)
	}
	tok := hex.EncodeToString(secret[:])
	scopedSrv := m.svc.buildMCPServerForTools(allowedTools)
	handler := mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return scopedSrv },
		&mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true},
	)
	m.scopedMu.Lock()
	if m.scoped == nil {
		m.scoped = make(map[string]*scopedMCPSession)
	}
	m.scoped[tok] = &scopedMCPSession{handler: handler, marker: marker}
	m.scopedMu.Unlock()
	return tok, nil
}

func (m *localMCPServer) unregisterScopedSession(tok string) {
	if m == nil || tok == "" {
		return
	}
	m.scopedMu.Lock()
	delete(m.scoped, tok)
	m.scopedMu.Unlock()
}

func (m *localMCPServer) close() {
	if m == nil {
		return
	}
	m.scopedMu.Lock()
	clear(m.scoped)
	m.scopedMu.Unlock()
	if m.http == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), m.shutdownTimeout)
	defer cancel()
	_ = m.http.Shutdown(ctx)
	_ = m.http.Close()
}
