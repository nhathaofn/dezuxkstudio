package server

import (
	"encoding/json"
	"net/http"
	"time"
)

// HealthHandler returns runtime status of the server.
type HealthHandler struct {
	startTime time.Time
	getPort   func() int
}

// NewHealthHandler constructs the health endpoint handler.
func NewHealthHandler(startTime time.Time, getPort func() int) *HealthHandler {
	return &HealthHandler{
		startTime: startTime,
		getPort:   getPort,
	}
}

// ServeHTTP writes the health JSON response.
func (h *HealthHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"status":     "ok",
		"uptime_sec": time.Since(h.startTime).Seconds(),
		"port":       h.getPort(),
	})
}
