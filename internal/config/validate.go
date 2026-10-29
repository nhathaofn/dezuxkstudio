package config

import (
	"errors"
	"fmt"
	"maps"
	"net"
	"net/url"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	maxWindowSize      = 16384
	minWindowSize      = 200
	minMaxMessageBytes = 1024
)

// Validate checks every invariant and reports all violations at once.
func (c *Config) Validate() error {
	var errs []error
	add := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }

	if _, err := ParseMode(string(c.Mode)); err != nil {
		add("mode: %v", err)
	}

	// server
	if c.Mode != ModeClient {
		if c.Server.Host == "" {
			add("server.host must not be empty")
		}
		if c.Server.Port < 0 || c.Server.Port > 65535 {
			add("server.port must be 0-65535, got %d", c.Server.Port)
		}
		if c.Mode == ModeDesktop && !IsLoopbackHost(c.Server.Host) {
			add("server.host must be a loopback address in desktop mode, got %q", c.Server.Host)
		}
		if c.Mode == ModeServer && !IsLoopbackHost(c.Server.Host) && c.Server.AuthToken == "" {
			add("server.auth_token (or auth_token_file) is required when binding to a non-loopback host")
		}
		if c.Mode == ModeServer && c.Server.EnableBroadcast && c.Server.AuthToken == "" {
			add("server.enable_broadcast requires server.auth_token")
		}
		for _, o := range c.Server.AllowedOrigins {
			if o == "*" && c.Mode == ModeDesktop {
				add("server.allowed_origins must not contain \"*\" in desktop mode")
			}
		}
		positiveDurations(add, map[string]time.Duration{
			"server.read_header_timeout": c.Server.ReadHeaderTimeout,
			"server.idle_timeout":        c.Server.IdleTimeout,
			"server.shutdown_timeout":    c.Server.ShutdownTimeout,
			"realtime.action_timeout":    c.Realtime.ActionTimeout,
			"realtime.queue_timeout":     c.Realtime.QueueTimeout,
			"realtime.ping_interval":     c.Realtime.PingInterval,
			"realtime.write_timeout":     c.Realtime.WriteTimeout,
			"storage.busy_timeout":       c.Storage.BusyTimeout,
		})
		positiveInts(add, map[string]int{
			"realtime.max_concurrent_actions": c.Realtime.MaxConcurrentActions,
			"realtime.client_buffer_size":     c.Realtime.ClientBufferSize,
			"storage.backups_to_keep":         c.Storage.BackupsToKeep,
			"diagnostics.keep":                c.Diagnostics.Keep,
		})
		if c.Server.MaxBroadcastBodyBytes <= 0 {
			add("server.max_broadcast_body_bytes must be > 0")
		}
		if c.Realtime.MaxMessageBytes < minMaxMessageBytes {
			add("realtime.max_message_bytes must be >= %d", minMaxMessageBytes)
		}
		if c.Storage.DBFile == "" || filepath.Base(c.Storage.DBFile) != c.Storage.DBFile || filepath.Ext(c.Storage.DBFile) != ".db" {
			add("storage.db_file must be a plain file name ending in .db, got %q", c.Storage.DBFile)
		}
		if c.Update.FeedURL != "" {
			if err := validateFeedURL(c.Update.FeedURL); err != nil {
				add("update.feed_url: %v", err)
			}
		}
		if c.Update.Timeout <= 0 {
			add("update.timeout must be > 0")
		}
	}

	// client
	if c.Mode == ModeClient {
		if err := ValidateRemoteURL(c.Client.RemoteURL); err != nil {
			add("client.remote_url: %v", err)
		}
	}

	// logging
	switch strings.ToLower(c.Log.Level) {
	case "debug", "info", "warn", "warning", "error":
	default:
		add("log.level must be debug, info, warn or error, got %q", c.Log.Level)
	}
	if c.Log.Format != "text" && c.Log.Format != "json" {
		add("log.format must be text or json, got %q", c.Log.Format)
	}
	if c.Log.MaxSizeMB < 1 {
		add("log.max_size_mb must be >= 1")
	}
	if c.Log.MaxBackups < 0 {
		add("log.max_backups must be >= 0")
	}

	// desktop window
	if c.Mode != ModeServer {
		d := c.Desktop
		sizes := map[string]int{"desktop.width": d.Width, "desktop.height": d.Height, "desktop.min_width": d.MinWidth, "desktop.min_height": d.MinHeight}
		for _, name := range slices.Sorted(maps.Keys(sizes)) {
			if v := sizes[name]; v < minWindowSize || v > maxWindowSize {
				add("%s must be %d-%d, got %d", name, minWindowSize, maxWindowSize, v)
			}
		}
		if d.Width < d.MinWidth || d.Height < d.MinHeight {
			add("desktop size %dx%d is smaller than minimum %dx%d", d.Width, d.Height, d.MinWidth, d.MinHeight)
		}
		if _, err := ParseHexColor(d.Background); err != nil {
			add("desktop.background: %v", err)
		}
		if d.ReadyTimeout <= 0 {
			add("desktop.ready_timeout must be > 0")
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("invalid configuration:\n%w", errors.Join(errs...))
	}
	return nil
}

// warnings reports valid but risky settings.
func (c *Config) warnings() []string {
	var w []string
	if c.Mode == ModeClient {
		if u, err := url.Parse(c.Client.RemoteURL); err == nil && u.Scheme == "ws" && !IsLoopbackHost(u.Hostname()) {
			w = append(w, "client.remote_url uses unencrypted ws:// to a non-loopback host; use wss://")
		}
	}
	if c.Mode == ModeServer && c.Server.AuthToken == "" {
		w = append(w, "server mode runs without an auth token (loopback only)")
	}
	return w
}

// IsLoopbackHost reports whether host is localhost or a loopback IP.
func IsLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}

// ValidateRemoteURL checks a ws:// or wss:// endpoint URL.
func ValidateRemoteURL(raw string) error {
	if raw == "" {
		return errors.New("is required")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if u.Scheme != "ws" && u.Scheme != "wss" {
		return fmt.Errorf("scheme must be ws or wss, got %q", u.Scheme)
	}
	if u.Host == "" {
		return errors.New("host is required")
	}
	if u.User != nil {
		return errors.New("credentials must not be embedded in the URL")
	}
	return nil
}

func validateFeedURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if u.Scheme == "https" && u.Host != "" {
		return nil
	}
	if u.Scheme == "http" && IsLoopbackHost(u.Hostname()) {
		return nil
	}
	return errors.New("must be an https URL (http only for loopback)")
}

// RGBA is a parsed colour.
type RGBA struct{ R, G, B, A uint8 }

// ParseHexColor parses #RRGGBB or #RRGGBBAA.
func ParseHexColor(s string) (RGBA, error) {
	if !strings.HasPrefix(s, "#") || (len(s) != 7 && len(s) != 9) {
		return RGBA{}, fmt.Errorf("expected #RRGGBB or #RRGGBBAA, got %q", s)
	}
	n, err := strconv.ParseUint(s[1:], 16, 32)
	if err != nil {
		return RGBA{}, fmt.Errorf("invalid hex colour %q", s)
	}
	if len(s) == 7 {
		return RGBA{R: uint8(n >> 16), G: uint8(n >> 8), B: uint8(n), A: 255}, nil
	}
	return RGBA{R: uint8(n >> 24), G: uint8(n >> 16), B: uint8(n >> 8), A: uint8(n)}, nil
}

func positiveDurations(add func(string, ...any), m map[string]time.Duration) {
	for _, name := range slices.Sorted(maps.Keys(m)) {
		if m[name] <= 0 {
			add("%s must be > 0", name)
		}
	}
}

func positiveInts(add func(string, ...any), m map[string]int) {
	for _, name := range slices.Sorted(maps.Keys(m)) {
		if m[name] <= 0 {
			add("%s must be > 0", name)
		}
	}
}
