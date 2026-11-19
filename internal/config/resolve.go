package config

import (
	"math"
	"strings"
	"time"
)

// WatermarkMaxWorkers returns the configured batch worker concurrency, falling back to Default(ModeDesktop) if <= 0 or c is nil.
func (c *Config) WatermarkMaxWorkers() int {
	def := Default(ModeDesktop).Watermark
	if c == nil || c.Watermark.MaxWorkers <= 0 {
		return def.MaxWorkers
	}
	return c.Watermark.MaxWorkers
}

// WatermarkMaxImagePixels returns the configured max pixel count (width*height) for decode, falling back to Default(ModeDesktop) if <= 0 or c is nil.
func (c *Config) WatermarkMaxImagePixels() int64 {
	def := Default(ModeDesktop).Watermark
	if c == nil || c.Watermark.MaxImagePixels <= 0 {
		return def.MaxImagePixels
	}
	return c.Watermark.MaxImagePixels
}

// WatermarkDecodeConcurrency returns the configured concurrency limit for image decoding, falling back to Default(ModeDesktop) if <= 0 or c is nil.
func (c *Config) WatermarkDecodeConcurrency() int {
	def := Default(ModeDesktop).Watermark
	if c == nil || c.Watermark.DecodeConcurrency <= 0 {
		return def.DecodeConcurrency
	}
	return c.Watermark.DecodeConcurrency
}

// WatermarkOrDefault returns the configured WatermarkConfig with any zero/unset fields populated from Default(ModeDesktop).Watermark.
func (c *Config) WatermarkOrDefault() WatermarkConfig {
	if c == nil {
		return Default(ModeDesktop).Watermark
	}
	return ResolveWatermarkConfig(c.Watermark)
}

// ResolveWatermarkConfig fills unset fields on cfg from Default(ModeDesktop).Watermark.
func ResolveWatermarkConfig(cfg WatermarkConfig) WatermarkConfig {
	def := Default(ModeDesktop).Watermark
	if cfg.MaxWorkers <= 0 {
		cfg.MaxWorkers = def.MaxWorkers
	}
	if cfg.MaxImagePixels <= 0 {
		cfg.MaxImagePixels = def.MaxImagePixels
	}
	if cfg.DecodeConcurrency <= 0 {
		cfg.DecodeConcurrency = def.DecodeConcurrency
	}
	if cfg.VideoDefaultWidth <= 0 {
		cfg.VideoDefaultWidth = def.VideoDefaultWidth
	}
	if cfg.VideoDefaultHeight <= 0 {
		cfg.VideoDefaultHeight = def.VideoDefaultHeight
	}
	if cfg.VideoDefaultCropPx <= 0 {
		cfg.VideoDefaultCropPx = def.VideoDefaultCropPx
	}
	return cfg
}

// BinariesDownloadResume reports whether resumable downloads are enabled.
func (c *Config) BinariesDownloadResume() bool {
	if c == nil || c.Binaries == (BinariesConfig{}) {
		return Default(ModeDesktop).Binaries.DownloadResume
	}
	return c.Binaries.DownloadResume
}

// BinariesDownloadRetryMax returns the max retry attempts for binary downloads, falling back to Default(ModeDesktop) if c is nil or uninitialized.
func (c *Config) BinariesDownloadRetryMax() int {
	def := Default(ModeDesktop).Binaries
	if c == nil || c.Binaries == (BinariesConfig{}) {
		return def.DownloadRetryMax
	}
	if c.Binaries.DownloadRetryMax < 0 {
		return 0
	}
	return c.Binaries.DownloadRetryMax
}

// BinariesDownloadRetryDelay returns the backoff delay between download retries, falling back to Default(ModeDesktop) if <= 0 or c is nil.
func (c *Config) BinariesDownloadRetryDelay() time.Duration {
	def := Default(ModeDesktop).Binaries
	if c == nil || c.Binaries.DownloadRetryDelay <= 0 {
		return def.DownloadRetryDelay
	}
	return c.Binaries.DownloadRetryDelay
}

// MediaStagingTTL returns the retention duration for staging files, falling back to Default(ModeDesktop) if <= 0 or c is nil.
func (c *Config) MediaStagingTTL() time.Duration {
	def := Default(ModeDesktop).Media
	if c == nil || c.Media.StagingTTL <= 0 {
		return def.StagingTTL
	}
	return c.Media.StagingTTL
}

// MediaStagingGCInterval returns the interval between staging directory GC passes, falling back to Default(ModeDesktop) if <= 0 or c is nil.
func (c *Config) MediaStagingGCInterval() time.Duration {
	def := Default(ModeDesktop).Media
	if c == nil || c.Media.StagingGCInterval <= 0 {
		return def.StagingGCInterval
	}
	return c.Media.StagingGCInterval
}

// MediaOrDefault returns the configured MediaConfig with any zero/unset fields populated from Default(ModeDesktop).Media.
func (c *Config) MediaOrDefault() MediaConfig {
	if c == nil {
		return Default(ModeDesktop).Media
	}
	return ResolveMediaConfig(c.Media)
}

// ResolveMediaConfig fills unset fields on cfg from Default(ModeDesktop).Media.
func ResolveMediaConfig(cfg MediaConfig) MediaConfig {
	def := Default(ModeDesktop).Media
	if cfg.LibraryMaxItems <= 0 {
		cfg.LibraryMaxItems = def.LibraryMaxItems
	}
	if cfg.LibraryRetention <= 0 {
		cfg.LibraryRetention = def.LibraryRetention
	}
	if cfg.TicketTTL <= 0 {
		cfg.TicketTTL = def.TicketTTL
	}
	if cfg.TicketRenewSkew <= 0 || cfg.TicketRenewSkew >= cfg.TicketTTL {
		cfg.TicketRenewSkew = def.TicketRenewSkew
	}
	if cfg.RegisteredRootsMax <= 0 {
		cfg.RegisteredRootsMax = def.RegisteredRootsMax
	}
	if cfg.StagingTTL <= 0 {
		cfg.StagingTTL = def.StagingTTL
	}
	if cfg.StagingGCInterval <= 0 {
		cfg.StagingGCInterval = def.StagingGCInterval
	}
	return cfg
}

// StudioOrDefault returns the configured StudioConfig with any zero/unset fields populated from Default(ModeDesktop).Studio.
func (c *Config) StudioOrDefault() StudioConfig {
	if c == nil {
		return Default(ModeDesktop).Studio
	}
	return ResolveStudioConfig(c.Studio)
}

// ResolveStudioConfig fills unset fields on cfg from Default(ModeDesktop).Studio while preserving explicit valid zero values
// such as FFmpegCRF == 0, TimelineDefaultTransitionMs == 0, TimelineDefaultBGMVolume == 0, and TimelineAudioDuckOrigVol == 0.
func ResolveStudioConfig(cfg StudioConfig) StudioConfig {
	def := Default(ModeDesktop).Studio
	if cfg == (StudioConfig{}) {
		return def
	}
	if cfg.DefaultFPS <= 0 {
		cfg.DefaultFPS = def.DefaultFPS
	}
	if strings.TrimSpace(cfg.DefaultResolution) == "" {
		cfg.DefaultResolution = def.DefaultResolution
	}
	if strings.TrimSpace(cfg.FFmpegPreset) == "" {
		cfg.FFmpegPreset = def.FFmpegPreset
	}
	if cfg.FFmpegCRF < 0 || cfg.FFmpegCRF > 51 {
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
	if cfg.GridSplitMaxMarginPx <= 0 {
		cfg.GridSplitMaxMarginPx = def.GridSplitMaxMarginPx
	}
	if cfg.GridSplitMaxGutterPx <= 0 {
		cfg.GridSplitMaxGutterPx = def.GridSplitMaxGutterPx
	}
	if cfg.ExtractFrameDefaultCount <= 0 {
		cfg.ExtractFrameDefaultCount = def.ExtractFrameDefaultCount
	}
	if cfg.ExtractFrameMaxCount <= 0 {
		cfg.ExtractFrameMaxCount = def.ExtractFrameMaxCount
	}
	if cfg.ExtractFrameMaxCount < cfg.ExtractFrameDefaultCount {
		cfg.ExtractFrameMaxCount = cfg.ExtractFrameDefaultCount
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
	if cfg.QCSampleFrameDefault <= 0 {
		cfg.QCSampleFrameDefault = def.QCSampleFrameDefault
	}
	if cfg.QCSampleFrameMax <= 0 {
		cfg.QCSampleFrameMax = def.QCSampleFrameMax
	}
	if cfg.QCSampleFrameMax < cfg.QCSampleFrameDefault {
		cfg.QCSampleFrameMax = cfg.QCSampleFrameDefault
	}
	if cfg.QCMaxReferenceImages <= 0 {
		cfg.QCMaxReferenceImages = def.QCMaxReferenceImages
	}
	if cfg.MaxMaskBytes <= 0 {
		cfg.MaxMaskBytes = def.MaxMaskBytes
	}
	if cfg.LipSyncMaxFaceIndex <= 0 {
		cfg.LipSyncMaxFaceIndex = def.LipSyncMaxFaceIndex
	}
	if math.IsNaN(cfg.AudioOverflowToleranceSec) || math.IsInf(cfg.AudioOverflowToleranceSec, 0) || cfg.AudioOverflowToleranceSec < 0 {
		cfg.AudioOverflowToleranceSec = def.AudioOverflowToleranceSec
	}
	if cfg.PreflightMaxItems <= 0 {
		cfg.PreflightMaxItems = def.PreflightMaxItems
	}
	if cfg.PreflightLargeBatchWarnCount <= 0 {
		cfg.PreflightLargeBatchWarnCount = def.PreflightLargeBatchWarnCount
	}
	if cfg.DefaultListLimit <= 0 {
		cfg.DefaultListLimit = def.DefaultListLimit
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
	if cfg.QueueMaxPollErrors <= 0 {
		cfg.QueueMaxPollErrors = def.QueueMaxPollErrors
	}
	if cfg.QueueMaxPollAge <= 0 {
		cfg.QueueMaxPollAge = def.QueueMaxPollAge
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
	if strings.TrimSpace(cfg.ExportSubtitleFontFamily) == "" {
		cfg.ExportSubtitleFontFamily = def.ExportSubtitleFontFamily
	}
	if cfg.TimelineDefaultTransitionMs < 0 {
		cfg.TimelineDefaultTransitionMs = def.TimelineDefaultTransitionMs
	}
	if math.IsNaN(cfg.TimelineDefaultBGMVolume) || math.IsInf(cfg.TimelineDefaultBGMVolume, 0) || cfg.TimelineDefaultBGMVolume < 0 || cfg.TimelineDefaultBGMVolume > 2 {
		cfg.TimelineDefaultBGMVolume = def.TimelineDefaultBGMVolume
	}
	if cfg.TimelineAudioDuckThreshold <= 0 {
		cfg.TimelineAudioDuckThreshold = def.TimelineAudioDuckThreshold
	}
	if math.IsNaN(cfg.TimelineAudioDuckOrigVol) || math.IsInf(cfg.TimelineAudioDuckOrigVol, 0) || cfg.TimelineAudioDuckOrigVol < 0 || cfg.TimelineAudioDuckOrigVol > 2 {
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

// ResolveMediaGenConfig fills unset fields on cfg from Default(ModeDesktop).MediaGen while preserving explicit valid zero values
// such as TextTemperature == 0 and RetryMax == 0.
func ResolveMediaGenConfig(cfg MediaGenConfig) MediaGenConfig {
	def := Default(ModeDesktop).MediaGen
	if cfg == (MediaGenConfig{}) {
		return def
	}
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
	if math.IsNaN(cfg.TextTemperature) || math.IsInf(cfg.TextTemperature, 0) || cfg.TextTemperature < 0 || cfg.TextTemperature > 2 {
		cfg.TextTemperature = def.TextTemperature
	}
	if cfg.RetryMax < 0 {
		cfg.RetryMax = 0
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
	if cfg.OAuthRefreshSkew <= 0 {
		cfg.OAuthRefreshSkew = def.OAuthRefreshSkew
	}
	if cfg.OAuthFallbackTTL <= 0 {
		cfg.OAuthFallbackTTL = def.OAuthFallbackTTL
	}
	return cfg
}
