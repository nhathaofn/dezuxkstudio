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
	ReadTimeout                 time.Duration
	WriteTimeout                time.Duration
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
	RequestTimeout           time.Duration // HTTP timeout for synchronous generation and submit/poll RPCs
	VideoTimeout             time.Duration // total deadline for async generation polling + artifact download
	VideoPollInterval        time.Duration // delay between async task status polls
	MaxOutputBytes           int64         // safety cap on downloaded image, video, or audio payloads
	MaxReferenceBytes        int64         // max bytes allowed per local reference image
	MaxJSONResponseBytes     int64         // safety cap on parsed JSON HTTP responses from media providers
	TextTimeout              time.Duration // HTTP timeout for structured LLM text generation calls
	TextMaxTokens            int           // default max output tokens for LLM text generation
	TextTemperature          float64       // default sampling temperature for LLM text generation
	RetryMax                 int           // max HTTP retry attempts on transient 429/5xx failures
	RetryInitialDelay        time.Duration // initial exponential backoff delay between HTTP retries
	RetryMaxDelay            time.Duration // ceiling backoff delay between HTTP retries
	RetryBackoffFactor       float64       // exponential multiplier for HTTP retry backoff
	LimiterPollInterval      time.Duration // polling interval while waiting on local provider rate limiter slots
	TTSDefaultVoiceOpenAI    string        // default OpenAI TTS voice when caller leaves voice empty
	TTSDefaultVoiceGemini    string        // default Gemini TTS prebuilt voice name when caller leaves voice empty
	TTSDefaultVoiceDashScope string        // default DashScope TTS voice when caller leaves voice empty
	TTSDefaultVoiceMiniMax   string        // default MiniMax TTS voice_id when caller leaves voice empty
	OAuthRefreshSkew         time.Duration // clock/expiry safety window before refreshing cached Vertex OAuth tokens
	OAuthFallbackTTL         time.Duration // fallback token cache lifetime when OAuth response omits expires_in
}

// StudioConfig tunes film studio FFmpeg/FFprobe processing, batch concurrency, queue workers, and timeline exports.
type StudioConfig struct {
	DefaultFPS                   int           // default output frame rate (e.g. 24)
	DefaultResolution            string        // default output WxH resolution (e.g. "1920x1080")
	FFmpegPreset                 string        // libx264 preset (e.g. "medium")
	FFmpegCRF                    int           // constant rate factor 0..51 (e.g. 20)
	FFmpegVideoCodec             string        // output video codec (e.g. "libx264")
	FFmpegPixelFormat            string        // output video pixel format (e.g. "yuv420p")
	FFmpegAudioCodec             string        // output audio codec (e.g. "aac")
	FFmpegAudioSampleRate        int           // output audio sample rate in Hz (e.g. 48000)
	FFmpegAudioBitrate           string        // output audio bitrate (e.g. "192k")
	FFmpegTimeout                time.Duration // max duration for FFmpeg studio commands
	FFprobeTimeout               time.Duration // max duration for FFprobe media inspection commands
	MaxVideoDimension            int           // maximum allowed width or height in pixels for studio video/grid ops
	DefaultShotDurationSec       int           // default duration in seconds for newly authored shots
	MaxShotDurationSec           int           // upper bound in seconds allowed per single shot
	GridSplitMaxCount            int           // maximum rows or columns allowed in interactive grid split
	GridSplitMaxMarginPx         int           // maximum outer margin in pixels allowed in grid split
	GridSplitMaxGutterPx         int           // maximum inter-cell gutter in pixels allowed in grid split
	ExtractFrameDefaultCount     int           // default sampled frame count when extracting multiple frames
	ExtractFrameMaxCount         int           // maximum sampled frame count allowed in frame extraction
	ExtractFrameEOFOffsetSec     float64       // FFmpeg -sseof offset in seconds when seeking near end of clip
	ExtractFrameEndPadSec        float64       // safety margin in seconds from video end when sampling multiple frames
	ExtractFrameLastPadSec       float64       // safety margin in seconds from video end when extracting final keyframe
	SilenceDetectNoiseDB         string        // FFmpeg silencedetect noise floor threshold (e.g. "-45dB")
	SilenceDetectMinSec          float64       // minimum continuous silence duration in seconds for silencedetect
	QCSilenceWarnRatio           float64       // threshold (0..1) above which QC warns that audio is mostly silent
	QCSampleFrameDefault         int           // default sampled video frames passed to multimodal Vision QC
	QCSampleFrameMax             int           // maximum sampled video frames passed to multimodal Vision QC
	QCMaxReferenceImages         int           // maximum character/style reference images attached per QC review
	MaxMaskBytes                 int64         // safety cap in bytes on decoded canvas inpaint masks
	LipSyncMaxFaceIndex          int           // maximum selectable face index for lip-sync generation
	AudioOverflowToleranceSec    float64       // tolerance in seconds before dub mux triggers speed/freeze overflow handling
	PreflightMaxItems            int           // maximum batch item count permitted in batch preflight
	PreflightLargeBatchWarnCount int           // item count threshold above which preflight warns about large batch spend/latency
	DefaultListLimit             int           // default row limit for studio queue and cost ledger queries
	MaxConcurrentBatch           int           // max parallel items during studio batch generation
	QueueWorkers                 int           // concurrent studio queue worker goroutines
	QueuePollInterval            time.Duration // polling/backoff interval for active queue jobs
	QueuePollLeaseMin            time.Duration // minimum lease extension while polling remote video jobs
	QueuePollBackoffMax          time.Duration // maximum adaptive delay between remote job status polls
	QueueMaxAttempts             int           // max submission retry attempts before marking job failed
	QueueMaxPollErrors           int           // max consecutive transient poll errors before failing a remote job
	QueueMaxPollAge              time.Duration // max age of a submitted remote job before poll timeout failure
	ExportMaxZipBytes            int64         // safety cap on packed NLE draft zip archives in bytes
	ExportLoudnormLUFS           float64       // integrated loudness target in LUFS for timeline renders (e.g. -14.0)
	ExportSubtitleFontSize       int           // default subtitle font size for timeline .ass burn-in
	ExportSubtitleFontFamily     string        // default subtitle font family for timeline .ass burn-in
	TimelineDefaultTransitionMs  int           // default transition overlap duration in ms for auto-assemble
	TimelineDefaultBGMVolume     float64       // default background music track gain (0..2.0)
	TimelineAudioDuckThreshold   float64       // sidechaincompress threshold for voice-over BGM ducking
	TimelineAudioDuckOrigVol     float64       // fallback attenuation factor for original audio behind TTS dub
	TimelineThumbIntervalSec     float64       // default thumbnail sampling interval in seconds for timeline strips
	TimelineThumbHeight          int           // pixel height of extracted timeline thumbnail sprite strips
	TimelineMaxStripThumbs       int           // maximum thumbnail frames extracted per clip sprite strip
	TimelinePeakBins             int           // default waveform peak bins count per media clip
	TimelineWaveSampleRate       int           // PCM sample rate in Hz when computing timeline waveform peaks
	TimelineWaveBlockSamples     int           // samples per RMS window block during peak extraction
	TimelineRevisionLimit        int           // default number of timeline revisions returned by timeline queries
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
	MaxWorkers         int   // max CPU worker threads for batch watermark processing
	MaxImagePixels     int64 // max allowed image pixel area (width * height) before decode
	DecodeConcurrency  int   // max concurrent image decoding operations
	VideoDefaultWidth  int   // default delogo box width in pixels for video watermark removal
	VideoDefaultHeight int   // default delogo box height in pixels for video watermark removal
	VideoDefaultCropPx int   // default edge crop thickness in pixels for video watermark removal
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
	CLISessionPersistTimeout time.Duration // timeout when persisting CLI session IDs to SQLite
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

// ValidFFmpegPresets lists all supported x264/x265 encoder speed presets.
func ValidFFmpegPresets() []string {
	return []string{"ultrafast", "superfast", "veryfast", "faster", "fast", "medium", "slow", "slower", "veryslow"}
}
