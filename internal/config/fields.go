package config

import "strings"

// field declares one configuration option. From key "server.port" the loader derives:
//
//	config.json   {"server": {"port": 0}}
//	environment   <PREFIX>SERVER_PORT
//	CLI flag      --server-port
type field struct {
	key    string
	usage  string
	val    value
	secret bool
}

func (f field) envName(prefix string) string {
	return prefix + strings.ToUpper(strings.NewReplacer(".", "_", "-", "_").Replace(f.key))
}

func (f field) flagName() string {
	return strings.NewReplacer(".", "-", "_", "-").Replace(f.key)
}

// fields is the single declaration of every option, bound to c's fields.
func fields(c *Config) []field {
	return []field{
		{key: "mode", usage: "run mode: desktop | client | server", val: modeValue{&c.Mode}},

		{key: "server.host", usage: "core server bind address (desktop mode: loopback only)", val: stringValue{&c.Server.Host}},
		{key: "server.port", usage: "core server port (0 = OS-assigned)", val: intValue{&c.Server.Port}},
		{key: "server.auth_token", usage: "server mode access token (prefer auth_token_file or env)", val: stringValue{&c.Server.AuthToken}, secret: true},
		{key: "server.auth_token_file", usage: "file containing the server mode access token", val: stringValue{&c.Server.AuthTokenFile}},
		{key: "server.allowed_origins", usage: "comma-separated WebSocket/CORS origin patterns", val: listOfStrings{&c.Server.AllowedOrigins}},
		{key: "server.read_header_timeout", usage: "HTTP read header timeout", val: durationValue{&c.Server.ReadHeaderTimeout}},
		{key: "server.idle_timeout", usage: "HTTP keep-alive idle timeout", val: durationValue{&c.Server.IdleTimeout}},
		{key: "server.shutdown_timeout", usage: "graceful shutdown deadline", val: durationValue{&c.Server.ShutdownTimeout}},
		{key: "server.enable_broadcast", usage: "enable POST /api/events/broadcast (requires a token)", val: boolValue{&c.Server.EnableBroadcast}},
		{key: "server.max_broadcast_body_bytes", usage: "max body size of the broadcast endpoint", val: int64Value{&c.Server.MaxBroadcastBodyBytes}},

		{key: "client.remote_url", usage: "client mode: remote server WebSocket URL (ws:// or wss://)", val: stringValue{&c.Client.RemoteURL}},
		{key: "client.auth_token", usage: "client mode: remote token (default: OS credential store)", val: stringValue{&c.Client.AuthToken}, secret: true},

		{key: "realtime.max_concurrent_actions", usage: "max actions executing at once", val: intValue{&c.Realtime.MaxConcurrentActions}},
		{key: "realtime.action_timeout", usage: "max duration of one action", val: durationValue{&c.Realtime.ActionTimeout}},
		{key: "realtime.queue_timeout", usage: "max wait for a free action slot", val: durationValue{&c.Realtime.QueueTimeout}},
		{key: "realtime.client_buffer_size", usage: "outbound message queue per client", val: intValue{&c.Realtime.ClientBufferSize}},
		{key: "realtime.max_message_bytes", usage: "max inbound WebSocket message size", val: int64Value{&c.Realtime.MaxMessageBytes}},
		{key: "realtime.ping_interval", usage: "WebSocket keep-alive ping interval", val: durationValue{&c.Realtime.PingInterval}},
		{key: "realtime.write_timeout", usage: "WebSocket write/ping timeout", val: durationValue{&c.Realtime.WriteTimeout}},

		{key: "log.level", usage: "debug | info | warn | error", val: stringValue{&c.Log.Level}},
		{key: "log.format", usage: "text | json", val: stringValue{&c.Log.Format}},
		{key: "log.dir", usage: "log directory (default: OS log location)", val: stringValue{&c.Log.Dir}},
		{key: "log.max_size_mb", usage: "rotate the log file at this size", val: intValue{&c.Log.MaxSizeMB}},
		{key: "log.max_backups", usage: "rotated log files to keep", val: intValue{&c.Log.MaxBackups}},
		{key: "log.stdout", usage: "also write logs to stdout", val: boolValue{&c.Log.Stdout}},

		{key: "storage.data_dir", usage: "data directory (default: OS data location)", val: stringValue{&c.Storage.DataDir}},
		{key: "storage.db_file", usage: "SQLite file name inside the data directory", val: stringValue{&c.Storage.DBFile}},
		{key: "storage.backups_to_keep", usage: "pre-migration database backups to keep", val: intValue{&c.Storage.BackupsToKeep}},
		{key: "storage.busy_timeout", usage: "SQLite busy timeout", val: durationValue{&c.Storage.BusyTimeout}},

		{key: "diagnostics.keep", usage: "diagnostic bundles to keep", val: intValue{&c.Diagnostics.Keep}},

		{key: "update.feed_url", usage: "release feed URL (GitHub latest-release API); empty disables", val: stringValue{&c.Update.FeedURL}},
		{key: "update.timeout", usage: "update check HTTP timeout", val: durationValue{&c.Update.Timeout}},
		{key: "update.check_on_startup", usage: "check for updates when the UI starts", val: boolValue{&c.Update.CheckOnStartup}},

		{key: "desktop.width", usage: "initial window width", val: intValue{&c.Desktop.Width}},
		{key: "desktop.height", usage: "initial window height", val: intValue{&c.Desktop.Height}},
		{key: "desktop.min_width", usage: "minimum window width", val: intValue{&c.Desktop.MinWidth}},
		{key: "desktop.min_height", usage: "minimum window height", val: intValue{&c.Desktop.MinHeight}},
		{key: "desktop.title", usage: "window title (default: product name and version)", val: stringValue{&c.Desktop.Title}},
		{key: "desktop.frameless", usage: "frameless window", val: boolValue{&c.Desktop.Frameless}},
		{key: "desktop.start_hidden", usage: "start with the window hidden", val: boolValue{&c.Desktop.StartHidden}},
		{key: "desktop.background", usage: "window background colour #RRGGBB or #RRGGBBAA", val: stringValue{&c.Desktop.Background}},
		{key: "desktop.ready_timeout", usage: "max wait for the core to become ready", val: durationValue{&c.Desktop.ReadyTimeout}},
	}
}
