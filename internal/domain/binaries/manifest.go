// Package binaries defines the domain models and specifications for external tools (FFmpeg, yt-dlp, etc.).
package binaries

import (
	"fmt"
	"regexp"
	"runtime"
)

// Supported binary version constants.
const (
	FFmpegVersion  = "7.1"
	FFprobeVersion = "7.1"
	YtDlpVersion   = "2025.01.26"
)

// Centralized binary download URL constants.
const (
	FFmpegDownloadURL  = "https://github.com/GyanD/codexffmpeg/releases/download/" + FFmpegVersion + "/ffmpeg-" + FFmpegVersion + "-essentials_build.zip"
	FFprobeDownloadURL = "https://github.com/GyanD/codexffmpeg/releases/download/" + FFprobeVersion + "/ffmpeg-" + FFprobeVersion + "-essentials_build.zip"
	YtDlpDownloadURL   = "https://github.com/yt-dlp/yt-dlp/releases/download/" + YtDlpVersion + "/yt-dlp.exe"
)

// Verified SHA256 checksums for binary release packages.
// On Windows ARM64, Windows 11 provides native x64 emulation, utilizing the verified x64 release binaries.
const (
	FFmpegSHA256 = "fa7d4d7e795db0e2503f49f105f46ed5852386f0cfdd819899be3b65ebde24fc"
	YtDlpSHA256  = "423eec9b60ab7910d97eb74cdb5daea90128850752d4aad6ccabaf8648d6387c"
)

// Centralized regular expression patterns for version probing.
const (
	FFmpegVersionPattern  = `ffmpeg version ([^\s]+)`
	FFprobeVersionPattern = `ffprobe version ([^\s]+)`
	YtDlpVersionPattern   = `(\d{4}\.\d{2}\.\d{2}(?:\.\d+)?)`
)

// Centralized regular expression patterns for runner progress and output scraping.
const (
	FFmpegTimePattern    = `time=(\d{2}):(\d{2}):(\d{2}(?:\.\d+)?)`
	FFmpegSpeedPattern   = `speed=\s*([^\s]+)`
	YtDlpProgressPattern = `\[download\]\s+(\d+\.?\d*)%\s+of\s+([^\s]+)\s+at\s+([^\s]+)`
)

// Precompiled regular expressions for version detection and progress parsing.
var (
	FFmpegVersionRe  = regexp.MustCompile(FFmpegVersionPattern)
	FFprobeVersionRe = regexp.MustCompile(FFprobeVersionPattern)
	YtDlpVersionRe   = regexp.MustCompile(YtDlpVersionPattern)

	FFmpegTimeRe    = regexp.MustCompile(FFmpegTimePattern)
	FFmpegSpeedRe   = regexp.MustCompile(FFmpegSpeedPattern)
	YtDlpProgressRe = regexp.MustCompile(YtDlpProgressPattern)
)

// Platform identifiers.
const (
	PlatformWindowsAmd64 = "windows_amd64"
	PlatformWindowsArm64 = "windows_arm64"
)

// Archive format identifiers.
const (
	ArchiveFormatZip = "zip"
	ArchiveFormatRaw = "raw"
)

// SupportedPlatforms lists all platform keys currently targeted for automated downloads.
var SupportedPlatforms = []string{
	PlatformWindowsAmd64,
	PlatformWindowsArm64,
}

// ProgressPatterns encapsulates precompiled regular expressions used by process runners for progress tracking.
type ProgressPatterns struct {
	Time     *regexp.Regexp
	Speed    *regexp.Regexp
	Progress *regexp.Regexp
}

// ManifestEntry declares the centralized declarative specification for an external binary tool.
type ManifestEntry struct {
	ID            BinaryID
	Name          string
	Version       string
	Description   string
	Executable    string
	ProbeArgs     []string
	VersionRegex  *regexp.Regexp
	Downloads     map[string]DownloadSpec
	ProgressRegex ProgressPatterns
}

// ExecutableName returns the OS-specific file name (e.g. ffmpeg.exe on Windows).
func (e ManifestEntry) ExecutableName() string {
	if runtime.GOOS == "windows" {
		return e.Executable + ".exe"
	}
	return e.Executable
}

// CurrentDownloadSpec returns the download specification for the host OS and architecture, if configured.
func (e ManifestEntry) CurrentDownloadSpec() (DownloadSpec, bool) {
	key := fmt.Sprintf("%s_%s", runtime.GOOS, runtime.GOARCH)
	spec, ok := e.Downloads[key]
	return spec, ok
}

// ExtractVersion parses the version identifier from probe output using the precompiled regex.
func (e ManifestEntry) ExtractVersion(output string) string {
	if e.VersionRegex == nil {
		return ""
	}
	matches := e.VersionRegex.FindStringSubmatch(output)
	if len(matches) > 1 {
		return matches[1]
	}
	if len(matches) == 1 {
		return matches[0]
	}
	return ""
}

// Manifest is the centralized registry of declarative specifications for all external binaries.
var Manifest = map[BinaryID]ManifestEntry{
	IDFFmpeg: {
		ID:           IDFFmpeg,
		Name:         "FFmpeg",
		Version:      FFmpegVersion,
		Description:  "Universal media converter, transcoder, and video filter engine.",
		Executable:   "ffmpeg",
		ProbeArgs:    []string{"-version"},
		VersionRegex: FFmpegVersionRe,
		Downloads: map[string]DownloadSpec{
			PlatformWindowsAmd64: {
				URL:           FFmpegDownloadURL,
				ArchiveFormat: ArchiveFormatZip,
				ExtractFile:   "bin/ffmpeg.exe",
				SHA256:        FFmpegSHA256,
			},
			PlatformWindowsArm64: {
				URL:           FFmpegDownloadURL,
				ArchiveFormat: ArchiveFormatZip,
				ExtractFile:   "bin/ffmpeg.exe",
				SHA256:        FFmpegSHA256,
			},
		},
		ProgressRegex: ProgressPatterns{
			Time:  FFmpegTimeRe,
			Speed: FFmpegSpeedRe,
		},
	},
	IDFFprobe: {
		ID:           IDFFprobe,
		Name:         "FFprobe",
		Version:      FFprobeVersion,
		Description:  "Multimedia stream analyzer and container metadata extractor.",
		Executable:   "ffprobe",
		ProbeArgs:    []string{"-version"},
		VersionRegex: FFprobeVersionRe,
		Downloads: map[string]DownloadSpec{
			PlatformWindowsAmd64: {
				URL:           FFprobeDownloadURL,
				ArchiveFormat: ArchiveFormatZip,
				ExtractFile:   "bin/ffprobe.exe",
				SHA256:        FFmpegSHA256,
			},
			PlatformWindowsArm64: {
				URL:           FFprobeDownloadURL,
				ArchiveFormat: ArchiveFormatZip,
				ExtractFile:   "bin/ffprobe.exe",
				SHA256:        FFmpegSHA256,
			},
		},
		ProgressRegex: ProgressPatterns{},
	},
	IDYtDlp: {
		ID:           IDYtDlp,
		Name:         "yt-dlp",
		Version:      YtDlpVersion,
		Description:  "Video and audio stream downloader and extractor from web platforms.",
		Executable:   "yt-dlp",
		ProbeArgs:    []string{"--version"},
		VersionRegex: YtDlpVersionRe,
		Downloads: map[string]DownloadSpec{
			PlatformWindowsAmd64: {
				URL:           YtDlpDownloadURL,
				ArchiveFormat: ArchiveFormatRaw,
				ExtractFile:   "yt-dlp.exe",
				SHA256:        YtDlpSHA256,
			},
			PlatformWindowsArm64: {
				URL:           YtDlpDownloadURL,
				ArchiveFormat: ArchiveFormatRaw,
				ExtractFile:   "yt-dlp.exe",
				SHA256:        YtDlpSHA256,
			},
		},
		ProgressRegex: ProgressPatterns{
			Progress: YtDlpProgressRe,
		},
	},
}

// GetManifest retrieves a ManifestEntry by its string identifier.
func GetManifest(id string) (ManifestEntry, bool) {
	entry, ok := Manifest[BinaryID(id)]
	return entry, ok
}

// ManifestFor retrieves a ManifestEntry by its typed BinaryID identifier.
func ManifestFor(id BinaryID) (ManifestEntry, bool) {
	entry, ok := Manifest[id]
	return entry, ok
}
