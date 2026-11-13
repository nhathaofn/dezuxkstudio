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
		{key: "realtime.long_action_timeout", usage: "max duration of long-running actions (downloads, workflows)", val: durationValue{&c.Realtime.LongActionTimeout}},
		{key: "realtime.queue_timeout", usage: "max wait for a free action slot", val: durationValue{&c.Realtime.QueueTimeout}},
		{key: "realtime.client_buffer_size", usage: "outbound message queue per client", val: intValue{&c.Realtime.ClientBufferSize}},
		{key: "realtime.max_message_bytes", usage: "max inbound WebSocket message size", val: int64Value{&c.Realtime.MaxMessageBytes}},
		{key: "realtime.ping_interval", usage: "WebSocket keep-alive ping interval", val: durationValue{&c.Realtime.PingInterval}},
		{key: "realtime.write_timeout", usage: "WebSocket write/ping timeout", val: durationValue{&c.Realtime.WriteTimeout}},
		{key: "realtime.keepalive_interval", usage: "heartbeat interval of actions waiting on bounded external work (< action_timeout)", val: durationValue{&c.Realtime.KeepAliveInterval}},

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

		{key: "network.idle_conn_timeout", usage: "how long idle outbound HTTP connections are kept", val: durationValue{&c.Network.IdleConnTimeout}},
		{key: "network.max_idle_conns", usage: "max idle outbound HTTP connections", val: intValue{&c.Network.MaxIdleConns}},
		{key: "network.max_idle_conns_per_host", usage: "max idle outbound HTTP connections per host", val: intValue{&c.Network.MaxIdleConnsPerHost}},
		{key: "providers.test_timeout", usage: "max duration of a provider connectivity test or model discovery", val: durationValue{&c.Providers.TestTimeout}},
		{key: "providers.check_on_startup", usage: "test every saved provider key in the background at start-up", val: boolValue{&c.Providers.CheckOnStartup}},
		{key: "providers.auto_check", usage: "check a provider key (alive? which models?) in the background when it is saved, changed or activated", val: boolValue{&c.Providers.AutoCheck}},
		{key: "providers.check_concurrency", usage: "max provider key tests running at once during the start-up check", val: intValue{&c.Providers.CheckConcurrency}},
		{key: "providers.default_max_workers", usage: "generation requests per provider and capability at once (unless set per provider)", val: intValue{&c.Providers.DefaultMaxWorkers}},
		{key: "providers.default_rpm", usage: "requests per minute per provider and capability, 0 = unlimited (unless set per provider)", val: intValue{&c.Providers.DefaultRPM}},
		{key: "providers.default_request_gap_ms", usage: "minimum pause between requests to a provider in ms (unless set per provider)", val: intValue{&c.Providers.DefaultRequestGapMs}},
		{key: "mediagen.request_timeout", usage: "HTTP timeout for image/TTS requests and async submit/poll calls", val: durationValue{&c.MediaGen.RequestTimeout}},
		{key: "mediagen.video_timeout", usage: "total timeout for polling and downloading an async video or image task", val: durationValue{&c.MediaGen.VideoTimeout}},
		{key: "mediagen.video_poll_interval", usage: "interval between status polls for async image/video generation", val: durationValue{&c.MediaGen.VideoPollInterval}},
		{key: "mediagen.max_output_bytes", usage: "max size of a downloaded generated media artifact in bytes", val: int64Value{&c.MediaGen.MaxOutputBytes}},
		{key: "mediagen.max_reference_bytes", usage: "max size of each local reference image file in bytes", val: int64Value{&c.MediaGen.MaxReferenceBytes}},
		{key: "binaries.probe_timeout", usage: "max duration of an external tool version probe", val: durationValue{&c.Binaries.ProbeTimeout}},

		{key: "agent.turn_timeout", usage: "an agent chat turn is stopped after this long without activity (text, tool calls, tool progress)", val: durationValue{&c.Agent.TurnTimeout}},
		{key: "agent.initialize_timeout", usage: "max wait for an ACP agent handshake", val: durationValue{&c.Agent.InitializeTimeout}},
		{key: "agent.version_probe_timeout", usage: "max duration of an agent CLI --version probe", val: durationValue{&c.Agent.VersionProbeTimeout}},
		{key: "agent.restore_connection", usage: "reconnect the agent left connected when the app last ran", val: boolValue{&c.Agent.RestoreConnection}},
		{key: "agent.auth_probe_timeout", usage: "max duration of an agent CLI auth status check", val: durationValue{&c.Agent.AuthProbeTimeout}},
		{key: "agent.login_timeout", usage: "max duration of an agent sign-in (browser OAuth)", val: durationValue{&c.Agent.LoginTimeout}},
		{key: "agent.model_catalog_timeout", usage: "max duration of reading an agent CLI model catalog", val: durationValue{&c.Agent.ModelCatalogTimeout}},
		{key: "agent.interrupt_wait", usage: "how long a cancelled agent turn waits for the CLI to stop", val: durationValue{&c.Agent.InterruptWait}},
		{key: "agent.status_cache_ttl", usage: "reuse agent CLI auth/version probe results for this long", val: durationValue{&c.Agent.StatusCacheTTL}},
		{key: "agent.auto_install_cli", usage: "download the agent CLIs (claude, codex, agy) into the app folder when missing; never touches CLIs already on the machine", val: boolValue{&c.Agent.AutoInstallCLI}},
		{key: "agent.auto_update_cli", usage: "update installed agent CLIs once at startup in release builds", val: boolValue{&c.Agent.AutoUpdateCLI}},
		{key: "agent.approval_ttl", usage: "pending agent approvals expire after this long", val: durationValue{&c.Agent.ApprovalTTL}},
		{key: "agent.history_limit", usage: "agent chat messages shown in the UI", val: intValue{&c.Agent.HistoryLimit}},
		{key: "agent.mode", usage: "agent runtime mode: assistant (default, restricted) or developer (opt-in coding/tools)", val: stringValue{&c.Agent.Mode}},
		{key: "agent.workspace_dir", usage: "override directory used as the agent workspace (empty = app default)", val: stringValue{&c.Agent.WorkspaceDir}},
		{key: "agent.max_pending_approvals", usage: "agent actions waiting for approval at most", val: intValue{&c.Agent.MaxPendingApprovals}},

		{key: "desktop.width", usage: "initial window width (0 = 80% of primary screen)", val: intValue{&c.Desktop.Width}},
		{key: "desktop.height", usage: "initial window height (0 = 80% of primary screen)", val: intValue{&c.Desktop.Height}},
		{key: "desktop.min_width", usage: "minimum window width", val: intValue{&c.Desktop.MinWidth}},
		{key: "desktop.min_height", usage: "minimum window height", val: intValue{&c.Desktop.MinHeight}},
		{key: "desktop.title", usage: "window title (default: product name and version)", val: stringValue{&c.Desktop.Title}},
		{key: "desktop.frameless", usage: "frameless window", val: boolValue{&c.Desktop.Frameless}},
		{key: "desktop.start_hidden", usage: "start with the window hidden", val: boolValue{&c.Desktop.StartHidden}},
		{key: "desktop.background", usage: "window background colour #RRGGBB or #RRGGBBAA", val: stringValue{&c.Desktop.Background}},
		{key: "desktop.ready_timeout", usage: "how long one bridge call waits for the core; a slower start is retried, not fatal", val: durationValue{&c.Desktop.ReadyTimeout}},
	}
}
