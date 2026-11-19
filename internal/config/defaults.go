package config

import (
	"time"

	"app/internal/buildinfo"
)

// DesktopOrigins are the Origin headers sent by the Wails webview in production:
// macOS/Linux use wails://wails, Windows (WebView2) uses http(s)://wails.localhost.
func DesktopOrigins() []string {
	return []string{"wails://wails", "http://wails.localhost", "https://wails.localhost"}
}

// DevOrigins are added in `wails dev` builds: the webview then loads the app from the
// Wails dev server (Windows: http://wails.localhost:34115) and the browser preview
// uses http://localhost:<port>.
func DevOrigins() []string {
	return []string{"wails://wails:*", "http://wails.localhost:*", "https://wails.localhost:*", "http://localhost:*", "http://127.0.0.1:*"}
}

// Default returns the baseline configuration for mode.
func Default(mode Mode) *Config {
	origins := DesktopOrigins()
	if buildinfo.IsDev() {
		origins = append(origins, DevOrigins()...)
	}
	releaseBuild := buildinfo.IsRelease()

	return &Config{
		Mode: mode,
		Server: ServerConfig{
			Host:                        "127.0.0.1",
			Port:                        0, // OS-assigned free port
			AllowedOrigins:              origins,
			ReadHeaderTimeout:           10 * time.Second,
			ReadTimeout:                 30 * time.Second,
			WriteTimeout:                60 * time.Second,
			IdleTimeout:                 120 * time.Second,
			ShutdownTimeout:             5 * time.Second,
			EnableBroadcast:             false,
			MaxBroadcastBodyBytes:       1 << 20,
			MaxMediaUploadBytes:         64 << 20, // 64 MiB
			MediaStagingTTL:             24 * time.Hour,
			MediaStagingCleanupInterval: 30 * time.Minute,
			AllowHostAdmin:              false,
		},
		Realtime: RealtimeConfig{
			MaxConcurrentActions: 64,
			ActionTimeout:        30 * time.Second,
			LongActionTimeout:    15 * time.Minute,
			QueueTimeout:         5 * time.Second,
			ClientBufferSize:     256,
			MaxMessageBytes:      1 << 20,
			PingInterval:         30 * time.Second,
			WriteTimeout:         5 * time.Second,
			KeepAliveInterval:    5 * time.Second,
		},
		Log: LogConfig{
			Level:      "info",
			Format:     "text",
			MaxSizeMB:  10,
			MaxBackups: 3,
			Stdout:     mode == ModeServer || buildinfo.IsDev(),
		},
		Storage: StorageConfig{
			DBFile:        "app.db",
			BackupsToKeep: 5,
			BusyTimeout:   5 * time.Second,
		},
		Diagnostics: DiagnosticsConfig{Keep: 5},
		Binaries: BinariesConfig{
			ProbeTimeout:       15 * time.Second,
			DownloadResume:     true,
			DownloadRetryMax:   3,
			DownloadRetryDelay: 1 * time.Second,
		},
		Providers: ProvidersConfig{TestTimeout: 15 * time.Second, CheckOnStartup: true, AutoCheck: true, CheckConcurrency: 4,
			DefaultMaxWorkers: 2, DefaultRPM: 0, DefaultRequestGapMs: 0}, // ArcReel _CONNECTIVITY_CHECK_TIMEOUT
		MediaGen: MediaGenConfig{
			RequestTimeout:           2 * time.Minute,
			VideoTimeout:             15 * time.Minute,
			VideoPollInterval:        4 * time.Second,
			MaxOutputBytes:           256 << 20, // 256 MiB
			MaxReferenceBytes:        20 << 20,  // 20 MiB
			MaxJSONResponseBytes:     16 << 20,  // 16 MiB handles inline base64 images/audio inside JSON payloads
			TextTimeout:              120 * time.Second,
			TextMaxTokens:            8192,
			TextTemperature:          0.4,
			RetryMax:                 3,
			RetryInitialDelay:        100 * time.Millisecond,
			RetryMaxDelay:            2 * time.Second,
			RetryBackoffFactor:       2.0,
			LimiterPollInterval:      250 * time.Millisecond,
			TTSDefaultVoiceOpenAI:    "alloy",
			TTSDefaultVoiceGemini:    "Kore",
			TTSDefaultVoiceDashScope: "Cherry",
			TTSDefaultVoiceMiniMax:   "male-qn-qingse",
			OAuthRefreshSkew:         2 * time.Minute,
			OAuthFallbackTTL:         30 * time.Minute,
		},
		Media: MediaConfig{
			LibraryMaxItems:    1000,
			LibraryRetention:   30 * 24 * time.Hour,
			TicketTTL:          15 * time.Minute,
			TicketRenewSkew:    2 * time.Minute,
			RegisteredRootsMax: 32,
			StagingTTL:         24 * time.Hour,
			StagingGCInterval:  1 * time.Hour,
		},
		Studio: StudioConfig{
			DefaultFPS:                   24,
			DefaultResolution:            "1920x1080",
			FFmpegPreset:                 "medium",
			FFmpegCRF:                    20,
			FFmpegVideoCodec:             "libx264",
			FFmpegPixelFormat:            "yuv420p",
			FFmpegAudioCodec:             "aac",
			FFmpegAudioSampleRate:        48000,
			FFmpegAudioBitrate:           "192k",
			FFmpegTimeout:                15 * time.Minute,
			FFprobeTimeout:               30 * time.Second,
			MaxVideoDimension:            16384,
			DefaultShotDurationSec:       5,
			MaxShotDurationSec:           60,
			GridSplitMaxCount:            8,
			GridSplitMaxMarginPx:         512,
			GridSplitMaxGutterPx:         256,
			ExtractFrameDefaultCount:     4,
			ExtractFrameMaxCount:         24,
			ExtractFrameEOFOffsetSec:     0.1,
			ExtractFrameEndPadSec:        0.05,
			ExtractFrameLastPadSec:       0.08,
			SilenceDetectNoiseDB:         "-45dB",
			SilenceDetectMinSec:          0.4,
			QCSilenceWarnRatio:           0.90,
			QCSampleFrameDefault:         3,
			QCSampleFrameMax:             6,
			QCMaxReferenceImages:         2,
			MaxMaskBytes:                 8 << 20, // 8 MiB
			LipSyncMaxFaceIndex:          8,
			AudioOverflowToleranceSec:    0.15,
			PreflightMaxItems:            500,
			PreflightLargeBatchWarnCount: 10,
			DefaultListLimit:             200,
			MaxConcurrentBatch:           4,
			QueueWorkers:                 2,
			QueuePollInterval:            2 * time.Second,
			QueuePollLeaseMin:            15 * time.Second,
			QueuePollBackoffMax:          60 * time.Second,
			QueueMaxAttempts:             3,
			QueueMaxPollErrors:           10,
			QueueMaxPollAge:              2 * time.Hour,
			ExportMaxZipBytes:            512 << 20, // 512 MiB
			ExportLoudnormLUFS:           -14.0,
			ExportSubtitleFontSize:       24,
			ExportSubtitleFontFamily:     "Arial",
			TimelineDefaultTransitionMs:  400,
			TimelineDefaultBGMVolume:     0.22,
			TimelineAudioDuckThreshold:   0.05,
			TimelineAudioDuckOrigVol:     0.35,
			TimelineThumbIntervalSec:     2.0,
			TimelineThumbHeight:          90,
			TimelineMaxStripThumbs:       60,
			TimelinePeakBins:             120,
			TimelineWaveSampleRate:       8000,
			TimelineWaveBlockSamples:     80,
			TimelineRevisionLimit:        30,
			LongScriptChunkChars:         12000,
			MaxBatchShotsUpsert:          100,
			ContactSheetFramesPerSheet:   12,
			ContactSheetMaxFramesPerShot: 24,
			ContactSheetMaxEdgePx:        2000,
			ContactSheetJPEGQuality:      85,
			SignalBlackMinSec:            0.1,
			SignalBlackPixelThresh:       0.98,
			SignalFreezeMinSec:           0.5,
			SignalFreezeNoiseDB:          "-60dB",
			SignalSceneCutThresh:         0.4,
			TimelineOverrunPolicy:        "warn",
			TimelineMaxSpeedFitRatio:     1.35,
			QueueWaitMaxDuration:         10 * time.Minute,
			QueueWaitPollInterval:        2 * time.Second,
		},
		Watermark: WatermarkConfig{
			MaxWorkers:         4,
			MaxImagePixels:     100_000_000,
			DecodeConcurrency:  2,
			VideoDefaultWidth:  160,
			VideoDefaultHeight: 64,
			VideoDefaultCropPx: 64,
		},
		Network: NetworkConfig{IdleConnTimeout: 90 * time.Second, MaxIdleConns: 100, MaxIdleConnsPerHost: 10},
		Update: UpdateConfig{
			FeedURL:        buildinfo.UpdateFeedURL,
			Timeout:        10 * time.Second,
			CheckOnStartup: releaseBuild,
		},
		Agent: AgentConfig{
			RestoreConnection:        true,
			TurnTimeout:              10 * time.Minute,
			InitializeTimeout:        15 * time.Second,
			VersionProbeTimeout:      5 * time.Second, // Node-based CLI shims cold-start slowly on Windows
			AuthProbeTimeout:         10 * time.Second,
			LoginTimeout:             3 * time.Minute,
			ModelCatalogTimeout:      30 * time.Second,
			InterruptWait:            5 * time.Second,
			StatusCacheTTL:           5 * time.Minute,
			CLISessionPersistTimeout: 3 * time.Second,
			AutoInstallCLI:           true,
			AutoUpdateCLI:            releaseBuild,
			ApprovalTTL:              24 * time.Hour,
			MaxApprovalContinuations: 5,
			HistoryLimit:             200,
			MaxPendingApprovals:      20,
			MaxConcurrentSubagents:   4,
			Mode:                     "assistant",
		},
		Desktop: DesktopConfig{
			Width:        0, // 0 = auto (80% of primary screen)
			Height:       0, // 0 = auto (80% of primary screen)
			MinWidth:     720,
			MinHeight:    480,
			Background:   "#0f1117",
			ReadyTimeout: 10 * time.Second,
		},
	}
}
