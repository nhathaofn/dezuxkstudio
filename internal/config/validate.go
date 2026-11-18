package config

import (
	"errors"
	"fmt"
	"maps"
	"math"
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
	maxStudioFPS       = 120
	minThumbHeightPx   = 32
	maxThumbHeightPx   = 360
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
			if o == "*" && (c.Mode == ModeDesktop || (c.Mode == ModeServer && c.Server.AuthToken == "")) {
				add("server.allowed_origins must not contain \"*\" in %s mode without auth_token", c.Mode)
			}
		}
		positiveDurations(add, map[string]time.Duration{
			"server.read_header_timeout":            c.Server.ReadHeaderTimeout,
			"server.read_timeout":                   c.Server.ReadTimeout,
			"server.write_timeout":                  c.Server.WriteTimeout,
			"server.idle_timeout":                   c.Server.IdleTimeout,
			"server.shutdown_timeout":               c.Server.ShutdownTimeout,
			"server.media_staging_ttl":              c.Server.MediaStagingTTL,
			"server.media_staging_cleanup_interval": c.Server.MediaStagingCleanupInterval,
			"realtime.action_timeout":               c.Realtime.ActionTimeout,
			"realtime.long_action_timeout":          c.Realtime.LongActionTimeout,
			"realtime.queue_timeout":                c.Realtime.QueueTimeout,
			"realtime.ping_interval":                c.Realtime.PingInterval,
			"realtime.write_timeout":                c.Realtime.WriteTimeout,
			"realtime.keepalive_interval":           c.Realtime.KeepAliveInterval,
			"storage.busy_timeout":                  c.Storage.BusyTimeout,
			"binaries.probe_timeout":                c.Binaries.ProbeTimeout,
			"binaries.download_retry_delay":         c.Binaries.DownloadRetryDelay,
			"providers.test_timeout":                c.Providers.TestTimeout,
			"mediagen.request_timeout":              c.MediaGen.RequestTimeout,
			"mediagen.video_timeout":                c.MediaGen.VideoTimeout,
			"mediagen.video_poll_interval":          c.MediaGen.VideoPollInterval,
			"mediagen.text_timeout":                 c.MediaGen.TextTimeout,
			"mediagen.retry_initial_delay":          c.MediaGen.RetryInitialDelay,
			"mediagen.retry_max_delay":              c.MediaGen.RetryMaxDelay,
			"mediagen.limiter_poll_interval":        c.MediaGen.LimiterPollInterval,
			"mediagen.oauth_refresh_skew":           c.MediaGen.OAuthRefreshSkew,
			"mediagen.oauth_fallback_ttl":           c.MediaGen.OAuthFallbackTTL,
			"media.library_retention":               c.Media.LibraryRetention,
			"media.ticket_ttl":                      c.Media.TicketTTL,
			"media.ticket_renew_skew":               c.Media.TicketRenewSkew,
			"media.staging_ttl":                     c.Media.StagingTTL,
			"media.staging_gc_interval":             c.Media.StagingGCInterval,
			"studio.ffmpeg_timeout":                 c.Studio.FFmpegTimeout,
			"studio.ffprobe_timeout":                c.Studio.FFprobeTimeout,
			"studio.queue_poll_interval":            c.Studio.QueuePollInterval,
			"studio.queue_poll_lease_min":           c.Studio.QueuePollLeaseMin,
			"studio.queue_poll_backoff_max":         c.Studio.QueuePollBackoffMax,
			"studio.queue_max_poll_age":             c.Studio.QueueMaxPollAge,
			"network.idle_conn_timeout":             c.Network.IdleConnTimeout,
			"agent.turn_timeout":                    c.Agent.TurnTimeout,
			"agent.initialize_timeout":              c.Agent.InitializeTimeout,
			"agent.version_probe_timeout":           c.Agent.VersionProbeTimeout,
			"agent.auth_probe_timeout":              c.Agent.AuthProbeTimeout,
			"agent.login_timeout":                   c.Agent.LoginTimeout,
			"agent.model_catalog_timeout":           c.Agent.ModelCatalogTimeout,
			"agent.interrupt_wait":                  c.Agent.InterruptWait,
			"agent.status_cache_ttl":                c.Agent.StatusCacheTTL,
			"agent.cli_session_persist_timeout":     c.Agent.CLISessionPersistTimeout,
			"agent.approval_ttl":                    c.Agent.ApprovalTTL,
		})
		positiveInts(add, map[string]int{
			"providers.check_concurrency":             c.Providers.CheckConcurrency,
			"providers.default_max_workers":           c.Providers.DefaultMaxWorkers,
			"realtime.max_concurrent_actions":         c.Realtime.MaxConcurrentActions,
			"realtime.client_buffer_size":             c.Realtime.ClientBufferSize,
			"storage.backups_to_keep":                 c.Storage.BackupsToKeep,
			"diagnostics.keep":                        c.Diagnostics.Keep,
			"network.max_idle_conns":                  c.Network.MaxIdleConns,
			"network.max_idle_conns_per_host":         c.Network.MaxIdleConnsPerHost,
			"agent.history_limit":                     c.Agent.HistoryLimit,
			"agent.max_pending_approvals":             c.Agent.MaxPendingApprovals,
			"agent.max_approval_continuations":        c.Agent.MaxApprovalContinuations,
			"agent.max_concurrent_subagents":          c.Agent.MaxConcurrentSubagents,
			"mediagen.text_max_tokens":                c.MediaGen.TextMaxTokens,
			"media.library_max_items":                 c.Media.LibraryMaxItems,
			"media.registered_roots_max":              c.Media.RegisteredRootsMax,
			"watermark.max_workers":                   c.Watermark.MaxWorkers,
			"watermark.decode_concurrency":            c.Watermark.DecodeConcurrency,
			"watermark.video_default_crop_px":         c.Watermark.VideoDefaultCropPx,
			"studio.default_fps":                      c.Studio.DefaultFPS,
			"studio.ffmpeg_audio_sample_rate":         c.Studio.FFmpegAudioSampleRate,
			"studio.max_video_dimension":              c.Studio.MaxVideoDimension,
			"studio.default_shot_duration_sec":        c.Studio.DefaultShotDurationSec,
			"studio.max_shot_duration_sec":            c.Studio.MaxShotDurationSec,
			"studio.grid_split_max_count":             c.Studio.GridSplitMaxCount,
			"studio.grid_split_max_margin_px":         c.Studio.GridSplitMaxMarginPx,
			"studio.grid_split_max_gutter_px":         c.Studio.GridSplitMaxGutterPx,
			"studio.extract_frame_default_count":      c.Studio.ExtractFrameDefaultCount,
			"studio.extract_frame_max_count":          c.Studio.ExtractFrameMaxCount,
			"studio.qc_sample_frame_default":          c.Studio.QCSampleFrameDefault,
			"studio.qc_sample_frame_max":              c.Studio.QCSampleFrameMax,
			"studio.qc_max_reference_images":          c.Studio.QCMaxReferenceImages,
			"studio.lipsync_max_face_index":           c.Studio.LipSyncMaxFaceIndex,
			"studio.preflight_max_items":              c.Studio.PreflightMaxItems,
			"studio.preflight_large_batch_warn_count": c.Studio.PreflightLargeBatchWarnCount,
			"studio.default_list_limit":               c.Studio.DefaultListLimit,
			"studio.max_concurrent_batch":             c.Studio.MaxConcurrentBatch,
			"studio.queue_workers":                    c.Studio.QueueWorkers,
			"studio.queue_max_attempts":               c.Studio.QueueMaxAttempts,
			"studio.queue_max_poll_errors":            c.Studio.QueueMaxPollErrors,
			"studio.timeline_thumb_height":            c.Studio.TimelineThumbHeight,
			"studio.timeline_max_strip_thumbs":        c.Studio.TimelineMaxStripThumbs,
			"studio.timeline_wave_sample_rate":        c.Studio.TimelineWaveSampleRate,
			"studio.timeline_wave_block_samples":      c.Studio.TimelineWaveBlockSamples,
			"studio.timeline_revision_limit":          c.Studio.TimelineRevisionLimit,
		})
		switch strings.ToLower(strings.TrimSpace(c.Agent.Mode)) {
		case "", "assistant":
		case "developer":
			if c.Mode == ModeServer {
				add("agent.mode cannot be \"developer\" when mode is %q", ModeServer)
			}
		default:
			add("invalid agent.mode %q: expected \"assistant\" or \"developer\"", c.Agent.Mode)
		}
		if c.Realtime.KeepAliveInterval >= c.Realtime.ActionTimeout {
			add("realtime.keepalive_interval must be < realtime.action_timeout")
		}
		if c.Server.MaxBroadcastBodyBytes <= 0 {
			add("server.max_broadcast_body_bytes must be > 0")
		}
		if c.Server.MaxMediaUploadBytes <= 0 {
			add("server.max_media_upload_bytes must be > 0")
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
		mins := map[string]int{"desktop.min_width": d.MinWidth, "desktop.min_height": d.MinHeight}
		for _, name := range slices.Sorted(maps.Keys(mins)) {
			if v := mins[name]; v < minWindowSize || v > maxWindowSize {
				add("%s must be %d-%d, got %d", name, minWindowSize, maxWindowSize, v)
			}
		}
		dims := map[string]int{"desktop.width": d.Width, "desktop.height": d.Height}
		for _, name := range slices.Sorted(maps.Keys(dims)) {
			if v := dims[name]; v != 0 && (v < minWindowSize || v > maxWindowSize) {
				add("%s must be %d-%d (or 0 for auto), got %d", name, minWindowSize, maxWindowSize, v)
			}
		}
		if (d.Width != 0 && d.Width < d.MinWidth) || (d.Height != 0 && d.Height < d.MinHeight) {
			add("desktop size %dx%d is smaller than minimum %dx%d", d.Width, d.Height, d.MinWidth, d.MinHeight)
		}
		if _, err := ParseHexColor(d.Background); err != nil {
			add("desktop.background: %v", err)
		}
		if d.ReadyTimeout <= 0 {
			add("desktop.ready_timeout must be > 0")
		}
	}

	if c.Providers.DefaultRPM < 0 || c.Providers.DefaultRequestGapMs < 0 {
		add("providers.default_rpm and providers.default_request_gap_ms must be >= 0")
	}
	for _, entry := range []struct {
		key string
		val string
	}{
		{"log.dir", c.Log.Dir},
		{"storage.data_dir", c.Storage.DataDir},
		{"storage.db_file", c.Storage.DBFile},
		{"server.auth_token_file", c.Server.AuthTokenFile},
		{"agent.workspace_dir", c.Agent.WorkspaceDir},
	} {
		if strings.ContainsRune(entry.val, 0) {
			add("%s must not contain NUL bytes", entry.key)
		}
	}
	if c.Mode != ModeClient && (c.MediaGen.MaxOutputBytes <= 0 || c.MediaGen.MaxReferenceBytes <= 0 || c.MediaGen.MaxJSONResponseBytes <= 0) {
		add("mediagen.max_output_bytes, mediagen.max_reference_bytes, and mediagen.max_json_response_bytes must be > 0")
	}
	if c.Mode != ModeClient {
		if c.MediaGen.RetryMax < 0 {
			add("mediagen.retry_max must be >= 0, got %d", c.MediaGen.RetryMax)
		}
		if c.Binaries.DownloadRetryMax < 0 {
			add("binaries.download_retry_max must be >= 0, got %d", c.Binaries.DownloadRetryMax)
		}
		if c.Network.MaxIdleConnsPerHost > c.Network.MaxIdleConns {
			add("network.max_idle_conns_per_host must be <= network.max_idle_conns")
		}
		if c.Media.TicketRenewSkew >= c.Media.TicketTTL {
			add("media.ticket_renew_skew must be < media.ticket_ttl")
		}
		if c.Watermark.MaxImagePixels <= 0 {
			add("watermark.max_image_pixels must be > 0")
		}
		if c.Watermark.VideoDefaultWidth < 2 || c.Watermark.VideoDefaultHeight < 2 {
			add("watermark.video_default_width and watermark.video_default_height must be >= 2")
		}
		if c.MediaGen.VideoPollInterval >= c.MediaGen.VideoTimeout {
			add("mediagen.video_poll_interval must be < mediagen.video_timeout")
		}
		if c.MediaGen.OAuthRefreshSkew >= c.MediaGen.OAuthFallbackTTL {
			add("mediagen.oauth_refresh_skew must be < mediagen.oauth_fallback_ttl")
		}
		for _, v := range []struct {
			key string
			val string
		}{
			{"mediagen.tts_default_voice_openai", c.MediaGen.TTSDefaultVoiceOpenAI},
			{"mediagen.tts_default_voice_gemini", c.MediaGen.TTSDefaultVoiceGemini},
			{"mediagen.tts_default_voice_dashscope", c.MediaGen.TTSDefaultVoiceDashScope},
			{"mediagen.tts_default_voice_minimax", c.MediaGen.TTSDefaultVoiceMiniMax},
		} {
			if strings.TrimSpace(v.val) == "" {
				add("%s must not be empty", v.key)
			}
		}
		if math.IsNaN(c.MediaGen.TextTemperature) || math.IsInf(c.MediaGen.TextTemperature, 0) || c.MediaGen.TextTemperature < 0 || c.MediaGen.TextTemperature > 2 {
			add("mediagen.text_temperature must be between 0 and 2, got %v", c.MediaGen.TextTemperature)
		}
		if math.IsNaN(c.MediaGen.RetryBackoffFactor) || math.IsInf(c.MediaGen.RetryBackoffFactor, 0) || c.MediaGen.RetryBackoffFactor <= 1 {
			add("mediagen.retry_backoff_factor must be > 1, got %v", c.MediaGen.RetryBackoffFactor)
		}
		if c.MediaGen.RetryMaxDelay < c.MediaGen.RetryInitialDelay {
			add("mediagen.retry_max_delay must be >= mediagen.retry_initial_delay")
		}
		if c.Studio.DefaultFPS > maxStudioFPS {
			add("studio.default_fps must be <= %d, got %d", maxStudioFPS, c.Studio.DefaultFPS)
		}
		if c.Studio.FFmpegCRF < 0 || c.Studio.FFmpegCRF > 51 {
			add("studio.ffmpeg_crf must be 0-51, got %d", c.Studio.FFmpegCRF)
		}
		preset := strings.ToLower(strings.TrimSpace(c.Studio.FFmpegPreset))
		if preset == "" || !slices.Contains(ValidFFmpegPresets(), preset) {
			add("studio.ffmpeg_preset must be a valid x264 preset, got %q", c.Studio.FFmpegPreset)
		}
		if strings.TrimSpace(c.Studio.FFmpegVideoCodec) == "" || strings.TrimSpace(c.Studio.FFmpegPixelFormat) == "" || strings.TrimSpace(c.Studio.FFmpegAudioCodec) == "" || strings.TrimSpace(c.Studio.FFmpegAudioBitrate) == "" {
			add("studio FFmpeg codec, pixel format, and audio bitrate fields must not be empty")
		}
		if c.Studio.MaxVideoDimension < 64 {
			add("studio.max_video_dimension must be >= 64, got %d", c.Studio.MaxVideoDimension)
		}
		if !validResolutionWxH(c.Studio.DefaultResolution, c.Studio.MaxVideoDimension) {
			add("studio.default_resolution must be WxH within bounds (64..%d), got %q", c.Studio.MaxVideoDimension, c.Studio.DefaultResolution)
		}
		if c.Studio.MaxShotDurationSec < c.Studio.DefaultShotDurationSec {
			add("studio.max_shot_duration_sec must be >= studio.default_shot_duration_sec")
		}
		if c.Studio.GridSplitMaxCount < 1 || c.Studio.GridSplitMaxCount > 32 {
			add("studio.grid_split_max_count must be between 1 and 32, got %d", c.Studio.GridSplitMaxCount)
		}
		if c.Studio.ExtractFrameDefaultCount > c.Studio.ExtractFrameMaxCount {
			add("studio.extract_frame_default_count must be <= studio.extract_frame_max_count")
		}
		if c.Studio.ExtractFrameEOFOffsetSec <= 0 || c.Studio.ExtractFrameEndPadSec <= 0 || c.Studio.ExtractFrameLastPadSec <= 0 {
			add("studio frame extract offset/pad values must be > 0")
		}
		if strings.TrimSpace(c.Studio.SilenceDetectNoiseDB) == "" || c.Studio.SilenceDetectMinSec <= 0 {
			add("studio.silence_detect_noise_db must not be empty and silence_detect_min_sec must be > 0")
		}
		if math.IsNaN(c.Studio.QCSilenceWarnRatio) || math.IsInf(c.Studio.QCSilenceWarnRatio, 0) || c.Studio.QCSilenceWarnRatio <= 0 || c.Studio.QCSilenceWarnRatio > 1 {
			add("studio.qc_silence_warn_ratio must be in (0, 1], got %v", c.Studio.QCSilenceWarnRatio)
		}
		if c.Studio.QCSampleFrameDefault > c.Studio.QCSampleFrameMax {
			add("studio.qc_sample_frame_default must be <= studio.qc_sample_frame_max")
		}
		if c.Studio.MaxMaskBytes <= 0 {
			add("studio.max_mask_bytes must be > 0")
		}
		if math.IsNaN(c.Studio.AudioOverflowToleranceSec) || math.IsInf(c.Studio.AudioOverflowToleranceSec, 0) || c.Studio.AudioOverflowToleranceSec < 0 || c.Studio.AudioOverflowToleranceSec > 10 {
			add("studio.audio_overflow_tolerance_sec must be between 0 and 10, got %v", c.Studio.AudioOverflowToleranceSec)
		}
		if c.Studio.PreflightLargeBatchWarnCount > c.Studio.PreflightMaxItems {
			add("studio.preflight_large_batch_warn_count must be <= studio.preflight_max_items")
		}
		if c.Studio.QueuePollLeaseMin < c.Studio.QueuePollInterval {
			add("studio.queue_poll_lease_min must be >= studio.queue_poll_interval")
		}
		if c.Studio.QueuePollBackoffMax < c.Studio.QueuePollInterval {
			add("studio.queue_poll_backoff_max must be >= studio.queue_poll_interval")
		}
		if c.Studio.ExportMaxZipBytes <= 0 {
			add("studio.export_max_zip_bytes must be > 0")
		}
		if math.IsNaN(c.Studio.ExportLoudnormLUFS) || math.IsInf(c.Studio.ExportLoudnormLUFS, 0) || c.Studio.ExportLoudnormLUFS < -36 || c.Studio.ExportLoudnormLUFS > -5 {
			add("studio.export_loudnorm_lufs must be between -36 and -5, got %v", c.Studio.ExportLoudnormLUFS)
		}
		if c.Studio.ExportSubtitleFontSize < 8 || c.Studio.ExportSubtitleFontSize > 96 {
			add("studio.export_subtitle_font_size must be between 8 and 96, got %d", c.Studio.ExportSubtitleFontSize)
		}
		if strings.TrimSpace(c.Studio.ExportSubtitleFontFamily) == "" {
			add("studio.export_subtitle_font_family must not be empty")
		}
		if c.Studio.TimelineDefaultTransitionMs < 0 {
			add("studio.timeline_default_transition_ms must be >= 0")
		}
		if c.Studio.TimelineDefaultBGMVolume < 0 || c.Studio.TimelineDefaultBGMVolume > 2 || c.Studio.TimelineAudioDuckThreshold <= 0 || c.Studio.TimelineAudioDuckOrigVol < 0 || c.Studio.TimelineAudioDuckOrigVol > 2 {
			add("studio timeline BGM volume and audio ducking settings are out of range")
		}
		if math.IsNaN(c.Studio.TimelineThumbIntervalSec) || math.IsInf(c.Studio.TimelineThumbIntervalSec, 0) || c.Studio.TimelineThumbIntervalSec <= 0 {
			add("studio.timeline_thumb_interval_sec must be > 0, got %v", c.Studio.TimelineThumbIntervalSec)
		}
		if c.Studio.TimelineThumbHeight < minThumbHeightPx || c.Studio.TimelineThumbHeight > maxThumbHeightPx {
			add("studio.timeline_thumb_height must be between %d and %d, got %d", minThumbHeightPx, maxThumbHeightPx, c.Studio.TimelineThumbHeight)
		}
		if c.Studio.TimelinePeakBins < 16 || c.Studio.TimelinePeakBins > 4096 {
			add("studio.timeline_peak_bins must be between 16 and 4096, got %d", c.Studio.TimelinePeakBins)
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
	if c.Mode == ModeServer && c.Server.AllowHostAdmin {
		w = append(w, "server.allow_host_admin is enabled; remote clients can mutate host paths, agent customizations, or open folders on the server")
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
	if hasUnsafeHostChars(u.Host) || hasUnsafeHostChars(u.Hostname()) {
		return errors.New("host contains invalid characters")
	}
	if u.Hostname() == "" {
		return errors.New("hostname is required")
	}
	if p := u.Port(); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 65535 {
			return fmt.Errorf("invalid port %q", p)
		}
	}
	return nil
}

func hasUnsafeHostChars(s string) bool {
	for i := 0; i < len(s); i++ {
		b := s[i]
		if b <= 0x20 || b == 0x7f || b == ';' || b == ',' {
			return true
		}
	}
	return false
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

func validResolutionWxH(raw string, maxDim int) bool {
	if maxDim < 64 {
		maxDim = Default(ModeDesktop).Studio.MaxVideoDimension
	}
	parts := strings.Split(strings.ToLower(strings.TrimSpace(raw)), "x")
	if len(parts) != 2 {
		return false
	}
	w, err1 := strconv.Atoi(strings.TrimSpace(parts[0]))
	h, err2 := strconv.Atoi(strings.TrimSpace(parts[1]))
	return err1 == nil && err2 == nil && w >= 64 && h >= 64 && w <= maxDim && h <= maxDim
}
