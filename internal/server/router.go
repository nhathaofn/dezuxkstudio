package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"app/internal/realtime"
)

// BuildRouter configures the HTTP multiplexer with all server endpoints.
func BuildRouter(
	hub *realtime.Hub,
	startTime time.Time,
	getPort func() int,
	getWSURL func() string,
	authToken string,
) http.Handler {
	mux := http.NewServeMux()

	// 1. Realtime WebSocket endpoint
	mux.Handle("/ws", hub)

	// 2. Health check endpoint
	health := NewHealthHandler(startTime, getPort)
	mux.Handle("/health", health)

	// 3. Dynamic Runtime Config endpoint for clients
	mux.HandleFunc("/runtime-config.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"ws_url": getWSURL(),
			"port":   getPort(),
		})
	})

	// 4. Asynchronous Event Broadcasting Ingestion endpoint
	mux.HandleFunc("/api/events/broadcast", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Auth-Token")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		if r.Method != http.MethodPost {
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}

		// Security: Verify AuthToken if configured
		if authToken != "" {
			token := r.URL.Query().Get("token")
			if token == "" {
				token = r.Header.Get("X-Auth-Token")
			}
			if token == "" {
				authHeader := r.Header.Get("Authorization")
				if len(authHeader) > 7 && authHeader[:7] == "Bearer " {
					token = authHeader[7:]
				}
			}
			if token != authToken {
				http.Error(w, "Unauthorized: invalid auth token", http.StatusUnauthorized)
				return
			}
		}

		// Security & Stability: Guard against OOM attacks with MaxBytesReader (limit 1MB)
		r.Body = http.MaxBytesReader(w, r.Body, 1024*1024)

		var payload struct {
			Event string      `json:"event"`
			RefID string      `json:"ref_id"`
			Data  interface{} `json:"data"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, fmt.Sprintf("invalid payload: %v", err), http.StatusBadRequest)
			return
		}
		if payload.Event == "" {
			http.Error(w, "event name is required", http.StatusBadRequest)
			return
		}
		hub.Broadcast(payload.Event, payload.RefID, payload.Data)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
	})

	return mux
}
