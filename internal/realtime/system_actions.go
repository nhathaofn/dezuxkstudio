package realtime

import (
	"context"
	"encoding/json"
	"runtime"
	"time"
)

var serverStartTime = time.Now()

// RegisterSystemActions registers default low-level actions (ping, echo, system info).
func RegisterSystemActions(registry *ActionRegistry) {
	// 1. system:ping - Latency & heartbeat
	registry.Register("system:ping", func(ctx context.Context, client *Client, payload json.RawMessage) (interface{}, error) {
		return map[string]interface{}{
			"server_time": time.Now().UnixMilli(),
		}, nil
	})

	// 2. system:echo - Round-trip reflection of incoming payload
	registry.Register("system:echo", func(ctx context.Context, client *Client, payload json.RawMessage) (interface{}, error) {
		if len(payload) == 0 || string(payload) == "null" || string(payload) == "{}" {
			return map[string]interface{}{
				"echo":      "ok",
				"payload":   nil,
				"timestamp": time.Now().UnixMilli(),
			}, nil
		}
		var parsed interface{}
		if err := json.Unmarshal(payload, &parsed); err != nil {
			return nil, err
		}
		return map[string]interface{}{
			"echo":      "ok",
			"payload":   parsed,
			"timestamp": time.Now().UnixMilli(),
		}, nil
	})

	// 3. system:info - Native runtime environment metrics (Pure Go, Zero Python)
	registry.Register("system:info", func(ctx context.Context, client *Client, payload json.RawMessage) (interface{}, error) {
		var memStats runtime.MemStats
		runtime.ReadMemStats(&memStats)

		uptimeSec := int64(time.Since(serverStartTime).Seconds())

		return map[string]interface{}{
			"os":             runtime.GOOS,
			"arch":           runtime.GOARCH,
			"go_version":     runtime.Version(),
			"num_cpu":        runtime.NumCPU(),
			"num_goroutine":  runtime.NumGoroutine(),
			"alloc_mb":       float64(memStats.Alloc) / (1024 * 1024),
			"sys_mb":         float64(memStats.Sys) / (1024 * 1024),
			"gc_cycles":      memStats.NumGC,
			"uptime_seconds": uptimeSec,
			"server_time":    time.Now().UnixMilli(),
		}, nil
	})
}
