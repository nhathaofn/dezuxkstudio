// Package config defines the typed runtime configuration and loads it from layered sources.
//
// Precedence (lowest → highest): defaults → config.json → environment → CLI flags.
// Every option is declared exactly once in fields.go; its file key, environment
// variable and flag name are all derived from that declaration.
package config

import (
	"fmt"
	"strings"
	"time"

	"app/internal/buildinfo"
)

// Mode selects which components run in the process.
type Mode string

const (
	// ModeDesktop runs the GUI with an embedded loopback core server (all-in-one).
	ModeDesktop Mode = "desktop"
	// ModeClient runs the GUI only and connects to a remote core server.
	ModeClient Mode = "client"
	// ModeServer runs the headless core server only.
	ModeServer Mode = "server"
)

// Modes lists every valid mode.
func Modes() []Mode { return []Mode{ModeDesktop, ModeClient, ModeServer} }

// ParseMode accepts canonical names and the legacy aliases used by earlier builds.
func ParseMode(s string) (Mode, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "desktop", "all-in-one", "dezuxkstudio":
		return ModeDesktop, nil
	case "client", "dezuxk-client":
		return ModeClient, nil
	case "server", "dezuxk-server":
		return ModeServer, nil
	}
	return "", fmt.Errorf("unknown mode %q (allowed: desktop, client, server)", s)
}

// Config is the complete runtime configuration.
type Config struct {
	Mode        Mode
	Server      ServerConfig
	Client      ClientConfig
	Realtime    RealtimeConfig
	Log         LogConfig
	Storage     StorageConfig
	Diagnostics DiagnosticsConfig
	Update      UpdateConfig
	Binaries    BinariesConfig
	Providers   ProvidersConfig
	MediaGen    MediaGenConfig
	Media       MediaConfig
	Studio      StudioConfig
	Watermark   WatermarkConfig
	Network     NetworkConfig
	Desktop     DesktopConfig
	Agent       AgentConfig
}

// ServerConfig configures the HTTP/WebSocket core server.
type ServerConfig struct {
	Host                        string
	Port                        int
	AuthToken                   string
	AuthTokenFile               string
	AllowedOrigins              []string
	ReadHeaderTimeout           time.Duration
	IdleTimeout                 time.Duration
	ShutdownTimeout             time.Duration
	EnableBroadcast             bool
	MaxBroadcastBodyBytes       int64
	MaxMediaUploadBytes         int64         // max body size of POST /api/media/upload in bytes
	MediaStagingTTL             time.Duration // retention period before expired staging directories are pruned
	MediaStagingCleanupInterval time.Duration // how often expired staging directories are checked
	AllowHostAdmin              bool          // allow remote clients in server mode to mutate host binary/CLI paths or open host folders
}

// ClientConfig configures client mode (GUI connected to a remote server).
type ClientConfig struct {
	RemoteURL string
	AuthToken string
}

// RealtimeConfig tunes the action dispatcher and WebSocket sessions.
type RealtimeConfig struct {
	MaxConcurrentActions int
	ActionTimeout        time.Duration
	LongActionTimeout    time.Duration
	QueueTimeout         time.Duration
	ClientBufferSize     int
	MaxMessageBytes      int64
	PingInterval         time.Duration
	WriteTimeout         time.Duration
	// KeepAliveInterval is how often an action waiting on bounded external work (CLI sign-in,
	// handshake) re-arms its idle timeout; it must stay below action_timeout.
	KeepAliveInterval time.Duration
}

// LogConfig configures structured logging.
type LogConfig struct {
	Level      string
	Format     string
	Dir        string
	MaxSizeMB  int
	MaxBackups int
	Stdout     bool
}

// StorageConfig configures the local database.
type StorageConfig struct {
	DataDir       string
	DBFile        string
	BackupsToKeep int
	BusyTimeout   time.Duration
}

// DiagnosticsConfig configures diagnostic bundle exports.
type DiagnosticsConfig struct {
	Keep int
}

// NetworkConfig tunes the shared outbound HTTP client (update checks, tool downloads).
type NetworkConfig struct {
	IdleConnTimeout     time.Duration
	MaxIdleConns        int
	MaxIdleConnsPerHost int
}

// ProvidersConfig configures API provider credential checks.
type ProvidersConfig struct {
	TestTimeout         time.Duration // max duration of a provider connectivity test or model discovery
	CheckOnStartup      bool          // test every saved key in the background when the app starts
	AutoCheck           bool          // check a key (alive? which models?) in the background when it is saved, changed or activated
	CheckConcurrency    int           // max start-up key tests running at once
	DefaultMaxWorkers   int           // generation requests per provider and capability running at once, unless set per provider
	DefaultRPM          int           // requests per minute per provider and capability (0 = unlimited), unless set per provider
	DefaultRequestGapMs int           // minimum pause between two requests to a provider (0 = none), unless set per provider
}

// BinariesConfig configures external tool (ffmpeg, yt-dlp...) inspection and downloads.
type BinariesConfig struct {
	ProbeTimeout       time.Duration // max duration of `<tool> -version`; a cold start under antivirus can take seconds
	DownloadResume     bool          // enable HTTP Range .part resumable downloads
	DownloadRetryMax   int           // max retry attempts for network failures during download
	DownloadRetryDelay time.Duration // backoff delay between download retries
}

// MediaGenConfig tunes text, image, video, and TTS media generation requests.
type MediaGenConfig struct {
	RequestTimeout            time.Duration // HTTP timeout for synchronous generation and submit/poll RPCs
	VideoTimeout              time.Duration // total deadline for async generation polling + artifact download
	VideoPollInterval         time.Duration // delay between async task status polls
	MaxOutputBytes            int64         // safety cap on downloaded image, video, or audio payloads
	MaxReferenceBytes         int64         // max bytes allowed per local reference image
	MaxJSONResponseBytes      int64         // safety cap on parsed JSON HTTP responses from media providers
	TextTimeout               time.Duration // HTTP timeout for structured LLM text generation calls
	TextMaxTokens             int           // default max output tokens for LLM text generation
	TextTemperature           float64       // default sampling temperature for LLM text generation
	RetryMax                  int           // max HTTP retry attempts on transient 429/5xx failures
	RetryInitialDelay         time.Duration // initial exponential backoff delay between HTTP retries
	RetryMaxDelay             time.Duration // ceiling backoff delay between HTTP retries
	RetryBackoffFactor        float64       // exponential multiplier for HTTP retry backoff
	LimiterPollInterval       time.Duration // polling interval while waiting on local provider rate limiter slots
	TTSDefaultVoiceOpenAI     string        // default OpenAI TTS voice when caller leaves voice empty
	TTSDefaultVoiceGemini     string        // default Gemini TTS prebuilt voice name when caller leaves voice empty
	TTSDefaultVoiceDashScope  string        // default DashScope TTS voice when caller leaves voice empty
	TTSDefaultVoiceMiniMax    string        // default MiniMax TTS voice_id when caller leaves voice empty
}

// StudioConfig tunes film studio FFmpeg/FFprobe processing, batch concurrency, queue workers, and timeline exports.
type StudioConfig struct {
	DefaultFPS                 int           // default output frame rate (e.g. 24)
	DefaultResolution          string        // default output WxH resolution (e.g. "1920x1080")
	FFmpegPreset               string        // libx264 preset (e.g. "medium")
	FFmpegCRF                  int           // constant rate factor 0..51 (e.g. 20)
	FFmpegVideoCodec           string        // output video codec (e.g. "libx264")
	FFmpegPixelFormat          string        // output video pixel format (e.g. "yuv420p")
	FFmpegAudioCodec           string        // output audio codec (e.g. "aac")
	FFmpegAudioSampleRate      int           // output audio sample rate in Hz (e.g. 48000)
	FFmpegAudioBitrate         string        // output audio bitrate (e.g. "192k")
	FFmpegTimeout              time.Duration // max duration for FFmpeg studio commands
	FFprobeTimeout             time.Duration // max duration for FFprobe media inspection commands
	MaxVideoDimension          int           // maximum allowed width or height in pixels for studio video/grid ops
	DefaultShotDurationSec     int           // default duration in seconds for newly authored shots
	MaxShotDurationSec         int           // upper bound in seconds allowed per single shot
	GridSplitMaxCount          int           // maximum rows or columns allowed in interactive grid split
	ExtractFrameEOFOffsetSec   float64       // FFmpeg -sseof offset in seconds when seeking near end of clip
	ExtractFrameEndPadSec      float64       // safety margin in seconds from video end when sampling multiple frames
	ExtractFrameLastPadSec     float64       // safety margin in seconds from video end when extracting final keyframe
	SilenceDetectNoiseDB       string        // FFmpeg silencedetect noise floor threshold (e.g. "-45dB")
	SilenceDetectMinSec        float64       // minimum continuous silence duration in seconds for silencedetect
	QCSilenceWarnRatio         float64       // threshold (0..1) above which QC warns that audio is mostly silent
	MaxConcurrentBatch         int           // max parallel items during studio batch generation
	QueueWorkers               int           // concurrent studio queue worker goroutines
	QueuePollInterval          time.Duration // polling/backoff interval for active queue jobs
	QueuePollLeaseMin          time.Duration // minimum lease extension while polling remote video jobs
	QueuePollBackoffMax        time.Duration // maximum adaptive delay between remote job status polls
	QueueMaxAttempts           int           // max submission retry attempts before marking job failed
	ExportMaxZipBytes          int64         // safety cap on packed NLE draft zip archives in bytes
	ExportLoudnormLUFS         float64       // integrated loudness target in LUFS for timeline renders (e.g. -14.0)
	ExportSubtitleFontSize     int           // default subtitle font size for timeline .ass burn-in
	TimelineDefaultTransitionMs int          // default transition overlap duration in ms for auto-assemble
	TimelineDefaultBGMVolume   float64       // default background music track gain (0..2.0)
	TimelineAudioDuckThreshold float64       // sidechaincompress threshold for voice-over BGM ducking
	TimelineAudioDuckOrigVol   float64       // fallback attenuation factor for original audio behind TTS dub
	TimelineThumbIntervalSec   float64       // default thumbnail sampling interval in seconds for timeline strips
	TimelineThumbHeight        int           // pixel height of extracted timeline thumbnail sprite strips
	TimelineMaxStripThumbs     int           // maximum thumbnail frames extracted per clip sprite strip
	TimelinePeakBins           int           // default waveform peak bins count per media clip
	TimelineWaveSampleRate     int           // PCM sample rate in Hz when computing timeline waveform peaks
	TimelineWaveBlockSamples   int           // samples per RMS window block during peak extraction
	TimelineRevisionLimit      int           // default number of timeline revisions returned by timeline queries
}

// MediaConfig tunes the media assets library, signed ticket streaming, and staging GC.
type MediaConfig struct {
	LibraryMaxItems    int           // max items retained in the media library
	LibraryRetention   time.Duration // retention duration before unreferenced media assets are pruned
	TicketTTL          time.Duration // lifetime of a signed media download ticket
	TicketRenewSkew    time.Duration // renewal window before ticket expiration
	RegisteredRootsMax int           // max allowed registered root paths for media file inspection
	StagingTTL         time.Duration // retention period before expired staging media files are pruned
	StagingGCInterval  time.Duration // interval between background garbage collections of staging directories
}

// WatermarkConfig configures watermark detection and removal processing.
type WatermarkConfig struct {
	MaxWorkers        int   // max CPU worker threads for batch watermark processing
	MaxImagePixels    int64 // max allowed image pixel area (width * height) before decode
	DecodeConcurrency int   // max concurrent image decoding operations
}

// UpdateConfig configures release update checks.
type UpdateConfig struct {
	FeedURL        string
	Timeout        time.Duration
	CheckOnStartup bool
}

// AgentConfig tunes the external AI agent integration (timeouts, caches, workspace mode).
type AgentConfig struct {
	RestoreConnection        bool          // reconnect the agent left connected when the app last ran
	TurnTimeout              time.Duration // max silence (no streamed output or tool activity) in one chat turn
	InitializeTimeout        time.Duration // max wait for an ACP agent handshake
	VersionProbeTimeout      time.Duration // max duration of `<cli> --version`
	AuthProbeTimeout         time.Duration // max duration of a CLI auth status check
	LoginTimeout             time.Duration // max duration of an interactive sign-in (browser OAuth)
	ModelCatalogTimeout      time.Duration // max duration of reading a CLI's model catalog
	InterruptWait            time.Duration // how long a cancelled turn waits for the CLI to report what it produced
	StatusCacheTTL           time.Duration // how long CLI auth/version probe results are reused
	AutoInstallCLI           bool          // download the agent CLIs into the app's own folder when they are missing
	AutoUpdateCLI            bool          // check once at startup to keep the app's own agent CLIs on the newest official release
	ApprovalTTL              time.Duration // pending approvals expire after this long
	MaxApprovalContinuations int           // max chained continuation turns after approval
	HistoryLimit             int           // messages returned to the chat UI
	MaxPendingApprovals      int           // agent actions waiting for approval at most
	MaxConcurrentSubagents   int           // max parallel subagent_run invocations per turn
	Mode                     string        // "assistant" (default, restricted) or "developer" (opt-in coding/tools)
	WorkspaceDir             string        // optional directory override for the agent workspace
}

// DeveloperMode reports whether the agent runs in opt-in developer mode (not remote server mode).
func (c Config) DeveloperMode() bool {
	return c.Mode != ModeServer && strings.EqualFold(strings.TrimSpace(c.Agent.Mode), "developer")
}

// DesktopConfig configures the native window and desktop startup.
type DesktopConfig struct {
	Width        int
	Height       int
	MinWidth     int
	MinHeight    int
	Title        string
	Frameless    bool
	StartHidden  bool
	Background   string
	ReadyTimeout time.Duration
}

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
			DefaultFPS:                  24,
			DefaultResolution:           "1920x1080",
			FFmpegPreset:                "medium",
			FFmpegCRF:                   20,
			FFmpegVideoCodec:            "libx264",
			FFmpegPixelFormat:           "yuv420p",
			FFmpegAudioCodec:            "aac",
			FFmpegAudioSampleRate:       48000,
			FFmpegAudioBitrate:          "192k",
			FFmpegTimeout:               15 * time.Minute,
			FFprobeTimeout:              30 * time.Second,
			MaxVideoDimension:           16384,
			DefaultShotDurationSec:      5,
			MaxShotDurationSec:          60,
			GridSplitMaxCount:           8,
			ExtractFrameEOFOffsetSec:    0.1,
			ExtractFrameEndPadSec:       0.05,
			ExtractFrameLastPadSec:      0.08,
			SilenceDetectNoiseDB:        "-45dB",
			SilenceDetectMinSec:         0.4,
			QCSilenceWarnRatio:          0.90,
			MaxConcurrentBatch:          4,
			QueueWorkers:                2,
			QueuePollInterval:           2 * time.Second,
			QueuePollLeaseMin:           15 * time.Second,
			QueuePollBackoffMax:         60 * time.Second,
			QueueMaxAttempts:            3,
			ExportMaxZipBytes:           512 << 20, // 512 MiB
			ExportLoudnormLUFS:          -14.0,
			ExportSubtitleFontSize:      24,
			TimelineDefaultTransitionMs: 400,
			TimelineDefaultBGMVolume:    0.22,
			TimelineAudioDuckThreshold:  0.05,
			TimelineAudioDuckOrigVol:    0.35,
			TimelineThumbIntervalSec:    2.0,
			TimelineThumbHeight:         90,
			TimelineMaxStripThumbs:      60,
			TimelinePeakBins:            120,
			TimelineWaveSampleRate:      8000,
			TimelineWaveBlockSamples:    80,
			TimelineRevisionLimit:       30,
		},
		Watermark: WatermarkConfig{
			MaxWorkers:        4,
			MaxImagePixels:    100_000_000,
			DecodeConcurrency: 2,
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

// WindowTitle returns the configured title or "<product> v<version>".
func (c *Config) WindowTitle() string {
	if c.Desktop.Title != "" {
		return c.Desktop.Title
	}
	return fmt.Sprintf("%s v%s", buildinfo.ProductName, buildinfo.Version)
}

// LogMaxSizeBytes converts the configured megabytes to bytes.
func (c *Config) LogMaxSizeBytes() int64 { return int64(c.Log.MaxSizeMB) << 20 }

// Redacted returns every option as "key → value" with secrets masked, for diagnostics.
func (c *Config) Redacted() map[string]string {
	out := make(map[string]string)
	for _, f := range fields(c) {
		v := f.val.String()
		if f.secret && v != "" {
			v = "[REDACTED]"
		}
		out[f.key] = v
	}
	return out
}

// WatermarkMaxWorkers returns the configured batch worker concurrency, or 4 if <= 0 or c is nil.
func (c *Config) WatermarkMaxWorkers() int {
	if c == nil || c.Watermark.MaxWorkers <= 0 {
		return 4
	}
	return c.Watermark.MaxWorkers
}

// WatermarkMaxImagePixels returns the configured max pixel count (width*height) for decode, or 100_000_000 if <= 0 or c is nil.
func (c *Config) WatermarkMaxImagePixels() int64 {
	if c == nil || c.Watermark.MaxImagePixels <= 0 {
		return 100_000_000
	}
	return c.Watermark.MaxImagePixels
}

// WatermarkDecodeConcurrency returns the configured concurrency limit for image decoding, or 2 if <= 0 or c is nil.
func (c *Config) WatermarkDecodeConcurrency() int {
	if c == nil || c.Watermark.DecodeConcurrency <= 0 {
		return 2
	}
	return c.Watermark.DecodeConcurrency
}

// BinariesDownloadResume reports whether resumable downloads are enabled.
func (c *Config) BinariesDownloadResume() bool {
	if c == nil {
		return true
	}
	return c.Binaries.DownloadResume
}

// BinariesDownloadRetryMax returns the max retry attempts for binary downloads, or 3 if c is nil.
func (c *Config) BinariesDownloadRetryMax() int {
	if c == nil {
		return 3
	}
	if c.Binaries.DownloadRetryMax < 0 {
		return 0
	}
	return c.Binaries.DownloadRetryMax
}

// BinariesDownloadRetryDelay returns the backoff delay between download retries, or 1s if <= 0 or c is nil.
func (c *Config) BinariesDownloadRetryDelay() time.Duration {
	if c == nil || c.Binaries.DownloadRetryDelay <= 0 {
		return 1 * time.Second
	}
	return c.Binaries.DownloadRetryDelay
}

// MediaStagingTTL returns the retention duration for staging files, falling back to 24h if <= 0 or c is nil.
func (c *Config) MediaStagingTTL() time.Duration {
	if c == nil || c.Media.StagingTTL <= 0 {
		return 24 * time.Hour
	}
	return c.Media.StagingTTL
}

// MediaStagingGCInterval returns the interval between staging directory GC passes, falling back to 1h if <= 0 or c is nil.
func (c *Config) MediaStagingGCInterval() time.Duration {
	if c == nil || c.Media.StagingGCInterval <= 0 {
		return 1 * time.Hour
	}
	return c.Media.StagingGCInterval
}

// MediaOrDefault returns the configured MediaConfig with any zero/unset fields populated from Default(ModeDesktop).Media.
func (c *Config) MediaOrDefault() MediaConfig {
	def := Default(ModeDesktop).Media
	if c == nil {
		return def
	}
	m := c.Media
	if m.LibraryMaxItems <= 0 {
		m.LibraryMaxItems = def.LibraryMaxItems
	}
	if m.LibraryRetention <= 0 {
		m.LibraryRetention = def.LibraryRetention
	}
	if m.TicketTTL <= 0 {
		m.TicketTTL = def.TicketTTL
	}
	if m.TicketRenewSkew <= 0 {
		m.TicketRenewSkew = def.TicketRenewSkew
	}
	if m.RegisteredRootsMax <= 0 {
		m.RegisteredRootsMax = def.RegisteredRootsMax
	}
	if m.StagingTTL <= 0 {
		m.StagingTTL = def.StagingTTL
	}
	if m.StagingGCInterval <= 0 {
		m.StagingGCInterval = def.StagingGCInterval
	}
	return m
}

// StudioOrDefault returns the configured StudioConfig with any zero/unset fields populated from Default(ModeDesktop).Studio.
func (c *Config) StudioOrDefault() StudioConfig {
	if c == nil {
		return Default(ModeDesktop).Studio
	}
	return ResolveStudioConfig(c.Studio)
}

// ResolveStudioConfig fills unset fields on cfg from Default(ModeDesktop).Studio while preserving explicit valid zero values such as FFmpegCRF == 0 when FFmpegPreset is configured.
func ResolveStudioConfig(cfg StudioConfig) StudioConfig {
	def := Default(ModeDesktop).Studio
	presetWasEmpty := strings.TrimSpace(cfg.FFmpegPreset) == ""
	if cfg.DefaultFPS <= 0 {
		cfg.DefaultFPS = def.DefaultFPS
	}
	if strings.TrimSpace(cfg.DefaultResolution) == "" {
		cfg.DefaultResolution = def.DefaultResolution
	}
	if presetWasEmpty {
		cfg.FFmpegPreset = def.FFmpegPreset
	}
	if cfg.FFmpegCRF < 0 || cfg.FFmpegCRF > 51 || (cfg.FFmpegCRF == 0 && presetWasEmpty) {
		cfg.FFmpegCRF = def.FFmpegCRF
	}
	if strings.TrimSpace(cfg.FFmpegVideoCodec) == "" {
		cfg.FFmpegVideoCodec = def.FFmpegVideoCodec
	}
	if strings.TrimSpace(cfg.FFmpegPixelFormat) == "" {
		cfg.FFmpegPixelFormat = def.FFmpegPixelFormat
	}
	if strings.TrimSpace(cfg.FFmpegAudioCodec) == "" {
		cfg.FFmpegAudioCodec = def.FFmpegAudioCodec
	}
	if cfg.FFmpegAudioSampleRate <= 0 {
		cfg.FFmpegAudioSampleRate = def.FFmpegAudioSampleRate
	}
	if strings.TrimSpace(cfg.FFmpegAudioBitrate) == "" {
		cfg.FFmpegAudioBitrate = def.FFmpegAudioBitrate
	}
	if cfg.FFmpegTimeout <= 0 {
		cfg.FFmpegTimeout = def.FFmpegTimeout
	}
	if cfg.FFprobeTimeout <= 0 {
		cfg.FFprobeTimeout = def.FFprobeTimeout
	}
	if cfg.MaxVideoDimension <= 0 {
		cfg.MaxVideoDimension = def.MaxVideoDimension
	}
	if cfg.DefaultShotDurationSec <= 0 {
		cfg.DefaultShotDurationSec = def.DefaultShotDurationSec
	}
	if cfg.MaxShotDurationSec <= 0 {
		cfg.MaxShotDurationSec = def.MaxShotDurationSec
	}
	if cfg.MaxShotDurationSec < cfg.DefaultShotDurationSec {
		cfg.MaxShotDurationSec = cfg.DefaultShotDurationSec
	}
	if cfg.GridSplitMaxCount <= 0 {
		cfg.GridSplitMaxCount = def.GridSplitMaxCount
	}
	if cfg.ExtractFrameEOFOffsetSec <= 0 {
		cfg.ExtractFrameEOFOffsetSec = def.ExtractFrameEOFOffsetSec
	}
	if cfg.ExtractFrameEndPadSec <= 0 {
		cfg.ExtractFrameEndPadSec = def.ExtractFrameEndPadSec
	}
	if cfg.ExtractFrameLastPadSec <= 0 {
		cfg.ExtractFrameLastPadSec = def.ExtractFrameLastPadSec
	}
	if strings.TrimSpace(cfg.SilenceDetectNoiseDB) == "" {
		cfg.SilenceDetectNoiseDB = def.SilenceDetectNoiseDB
	}
	if cfg.SilenceDetectMinSec <= 0 {
		cfg.SilenceDetectMinSec = def.SilenceDetectMinSec
	}
	if cfg.QCSilenceWarnRatio <= 0 || cfg.QCSilenceWarnRatio > 1 {
		cfg.QCSilenceWarnRatio = def.QCSilenceWarnRatio
	}
	if cfg.MaxConcurrentBatch <= 0 {
		cfg.MaxConcurrentBatch = def.MaxConcurrentBatch
	}
	if cfg.QueueWorkers <= 0 {
		cfg.QueueWorkers = def.QueueWorkers
	}
	if cfg.QueuePollInterval <= 0 {
		cfg.QueuePollInterval = def.QueuePollInterval
	}
	if cfg.QueuePollLeaseMin <= 0 {
		cfg.QueuePollLeaseMin = def.QueuePollLeaseMin
	}
	if cfg.QueuePollBackoffMax <= 0 {
		cfg.QueuePollBackoffMax = def.QueuePollBackoffMax
	}
	if cfg.QueueMaxAttempts <= 0 {
		cfg.QueueMaxAttempts = def.QueueMaxAttempts
	}
	if cfg.ExportMaxZipBytes <= 0 {
		cfg.ExportMaxZipBytes = def.ExportMaxZipBytes
	}
	if cfg.ExportLoudnormLUFS == 0 {
		cfg.ExportLoudnormLUFS = def.ExportLoudnormLUFS
	}
	if cfg.ExportSubtitleFontSize <= 0 {
		cfg.ExportSubtitleFontSize = def.ExportSubtitleFontSize
	}
	if cfg.TimelineDefaultTransitionMs <= 0 {
		cfg.TimelineDefaultTransitionMs = def.TimelineDefaultTransitionMs
	}
	if cfg.TimelineDefaultBGMVolume <= 0 {
		cfg.TimelineDefaultBGMVolume = def.TimelineDefaultBGMVolume
	}
	if cfg.TimelineAudioDuckThreshold <= 0 {
		cfg.TimelineAudioDuckThreshold = def.TimelineAudioDuckThreshold
	}
	if cfg.TimelineAudioDuckOrigVol <= 0 {
		cfg.TimelineAudioDuckOrigVol = def.TimelineAudioDuckOrigVol
	}
	if cfg.TimelineThumbIntervalSec <= 0 {
		cfg.TimelineThumbIntervalSec = def.TimelineThumbIntervalSec
	}
	if cfg.TimelineThumbHeight <= 0 {
		cfg.TimelineThumbHeight = def.TimelineThumbHeight
	}
	if cfg.TimelineMaxStripThumbs <= 0 {
		cfg.TimelineMaxStripThumbs = def.TimelineMaxStripThumbs
	}
	if cfg.TimelinePeakBins <= 0 {
		cfg.TimelinePeakBins = def.TimelinePeakBins
	}
	if cfg.TimelineWaveSampleRate <= 0 {
		cfg.TimelineWaveSampleRate = def.TimelineWaveSampleRate
	}
	if cfg.TimelineWaveBlockSamples <= 0 {
		cfg.TimelineWaveBlockSamples = def.TimelineWaveBlockSamples
	}
	if cfg.TimelineRevisionLimit <= 0 {
		cfg.TimelineRevisionLimit = def.TimelineRevisionLimit
	}
	return cfg
}

// MediaGenOrDefault returns the configured MediaGenConfig with any zero/unset fields populated from Default(ModeDesktop).MediaGen.
func (c *Config) MediaGenOrDefault() MediaGenConfig {
	if c == nil {
		return Default(ModeDesktop).MediaGen
	}
	return ResolveMediaGenConfig(c.MediaGen)
}

// ResolveMediaGenConfig fills unset fields on cfg from Default(ModeDesktop).MediaGen while preserving explicit valid zero temperature when text settings are configured.
func ResolveMediaGenConfig(cfg MediaGenConfig) MediaGenConfig {
	def := Default(ModeDesktop).MediaGen
	textUnset := cfg.TextMaxTokens <= 0 && cfg.TextTimeout <= 0
	if cfg.RequestTimeout <= 0 {
		cfg.RequestTimeout = def.RequestTimeout
	}
	if cfg.VideoTimeout <= 0 {
		cfg.VideoTimeout = def.VideoTimeout
	}
	if cfg.VideoPollInterval <= 0 {
		cfg.VideoPollInterval = def.VideoPollInterval
	}
	if cfg.MaxOutputBytes <= 0 {
		cfg.MaxOutputBytes = def.MaxOutputBytes
	}
	if cfg.MaxReferenceBytes <= 0 {
		cfg.MaxReferenceBytes = def.MaxReferenceBytes
	}
	if cfg.MaxJSONResponseBytes <= 0 {
		cfg.MaxJSONResponseBytes = def.MaxJSONResponseBytes
	}
	if cfg.TextTimeout <= 0 {
		cfg.TextTimeout = def.TextTimeout
	}
	if cfg.TextMaxTokens <= 0 {
		cfg.TextMaxTokens = def.TextMaxTokens
	}
	if cfg.TextTemperature < 0 || cfg.TextTemperature > 2 || (cfg.TextTemperature == 0 && textUnset) {
		cfg.TextTemperature = def.TextTemperature
	}
	if cfg.RetryMax <= 0 {
		cfg.RetryMax = def.RetryMax
	}
	if cfg.RetryInitialDelay <= 0 {
		cfg.RetryInitialDelay = def.RetryInitialDelay
	}
	if cfg.RetryMaxDelay <= 0 {
		cfg.RetryMaxDelay = def.RetryMaxDelay
	}
	if cfg.RetryBackoffFactor <= 1 {
		cfg.RetryBackoffFactor = def.RetryBackoffFactor
	}
	if cfg.LimiterPollInterval <= 0 {
		cfg.LimiterPollInterval = def.LimiterPollInterval
	}
	if strings.TrimSpace(cfg.TTSDefaultVoiceOpenAI) == "" {
		cfg.TTSDefaultVoiceOpenAI = def.TTSDefaultVoiceOpenAI
	}
	if strings.TrimSpace(cfg.TTSDefaultVoiceGemini) == "" {
		cfg.TTSDefaultVoiceGemini = def.TTSDefaultVoiceGemini
	}
	if strings.TrimSpace(cfg.TTSDefaultVoiceDashScope) == "" {
		cfg.TTSDefaultVoiceDashScope = def.TTSDefaultVoiceDashScope
	}
	if strings.TrimSpace(cfg.TTSDefaultVoiceMiniMax) == "" {
		cfg.TTSDefaultVoiceMiniMax = def.TTSDefaultVoiceMiniMax
	}
	return cfg
}
