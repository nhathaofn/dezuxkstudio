// Package handlers contains the auxiliary HTTP endpoints of the core server.
package handlers

import (
	"encoding/json"
	"net/http"
	"slices"
	"time"

	"app/internal/apperr"
	"app/internal/contracts"
)

// Health reports liveness and basic metrics.
func Health(version string, started time.Time, clients func() int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"status":     "ok",
			"version":    version,
			"uptime_sec": int64(time.Since(started).Seconds()),
			"clients":    clients(),
		})
	})
}

// Timeouts are the server-side action limits a browser client mirrors so it never gives up
// on an action the server is still running.
type Timeouts struct {
	Action       time.Duration
	LongAction   time.Duration
	PingInterval time.Duration
}

// RuntimeConfig tells browser-hosted clients where the WebSocket lives and which
// timeouts the server applies. It never returns credentials.
func RuntimeConfig(mode, version string, timeouts Timeouts, wsURL func() string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, apperr.New(apperr.CodeBadRequest, "method not allowed"))
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, map[string]any{
			"ws_url":                 wsURL(),
			"mode":                   mode,
			"version":                version,
			"action_timeout_ms":      timeouts.Action.Milliseconds(),
			"long_action_timeout_ms": timeouts.LongAction.Milliseconds(),
			"ping_interval_ms":       timeouts.PingInterval.Milliseconds(),
		})
	})
}

// Publisher is the subset of the hub used by Broadcast.
type Publisher interface {
	Publish(event string, data any)
}

// Broadcast lets trusted local processes push a declared event to every client.
func Broadcast(pub Publisher, declared []string, maxBody int64) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, apperr.New(apperr.CodeBadRequest, "method not allowed"))
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxBody)
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		var in struct {
			Event string          `json:"event"`
			Data  json.RawMessage `json:"data"`
		}
		if err := dec.Decode(&in); err != nil {
			writeError(w, http.StatusBadRequest, apperr.Validation("invalid JSON body"))
			return
		}
		if !slices.Contains(declared, in.Event) {
			writeError(w, http.StatusBadRequest, apperr.Validation("event is not declared in contracts"))
			return
		}
		pub.Publish(in.Event, in.Data)
		writeJSON(w, http.StatusAccepted, map[string]bool{"ok": true})
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]any{"error": contracts.NewErrorBody(err)})
}
