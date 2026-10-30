// Package binaries defines the domain models and specifications for external tools (FFmpeg, yt-dlp, etc.).
package binaries

import (
	"context"
	"fmt"
	"regexp"
	"runtime"
	"slices"

	"app/internal/apperr"
)

// BinaryID identifies an external executable.
type BinaryID string

const (
	IDFFmpeg  BinaryID = "ffmpeg"
	IDFFprobe BinaryID = "ffprobe"
	IDYtDlp   BinaryID = "yt_dlp"
)

// AllIDs returns all registered binary identifiers.
func AllIDs() []BinaryID {
	return []BinaryID{IDFFmpeg, IDFFprobe, IDYtDlp}
}

// Status indicates whether a binary is found and functional.
type Status string

const (
	StatusAvailable Status = "available"
	StatusMissing   Status = "missing"
	StatusInvalid   Status = "invalid"
)

// Source indicates where the binary executable was found.
type Source string

const (
	SourceTools  Source = "tools"
	SourceCustom Source = "custom"
	SourceSystem Source = "system"
	SourceNone   Source = "none"
)

// BinaryInfo is the complete status of an external binary.
type BinaryInfo struct {
	ID           BinaryID `json:"id"`
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	Executable   string   `json:"executable"`
	ResolvedPath string   `json:"resolved_path"`
	CustomPath   string   `json:"custom_path"`
	Source       Source   `json:"source"`
	Version      string   `json:"version"`
	Status       Status   `json:"status"`
	CanDownload  bool     `json:"can_download"`
}

// DownloadSpec provides download metadata for a given OS and architecture.
type DownloadSpec struct {
	URL           string // URL to binary or archive
	ArchiveFormat string // "zip" or "raw"
	ExtractFile   string // file name inside zip if applicable
}

// BinarySpec declares a registered binary tool.
type BinarySpec struct {
	ID             BinaryID
	Name           string
	Description    string
	ExecutableBase string
	ProbeArgs      []string
	VersionRegex   string
	Downloads      map[string]DownloadSpec // key: "<os>_<arch>" e.g. "windows_amd64"
}

// ExecutableName returns the OS-specific file name (e.g. ffmpeg.exe on Windows).
func (s BinarySpec) ExecutableName() string {
	if runtime.GOOS == "windows" {
		return s.ExecutableBase + ".exe"
	}
	return s.ExecutableBase
}

// CurrentDownloadSpec returns the download specification for the current OS and architecture, if any.
func (s BinarySpec) CurrentDownloadSpec() (DownloadSpec, bool) {
	key := fmt.Sprintf("%s_%s", runtime.GOOS, runtime.GOARCH)
	spec, ok := s.Downloads[key]
	return spec, ok
}

// ExtractVersion extracts a version string from output using the spec's VersionRegex.
func (s BinarySpec) ExtractVersion(output string) string {
	if s.VersionRegex == "" {
		return ""
	}
	re, err := regexp.Compile(s.VersionRegex)
	if err != nil {
		return ""
	}
	matches := re.FindStringSubmatch(output)
	if len(matches) > 1 {
		return matches[1]
	}
	if len(matches) == 1 {
		return matches[0]
	}
	return ""
}

// Registry holds the official binary specifications.
var Registry = map[BinaryID]BinarySpec{
	IDFFmpeg: {
		ID:             IDFFmpeg,
		Name:           "FFmpeg",
		Description:    "Universal media converter, transcoder, and video filter engine.",
		ExecutableBase: "ffmpeg",
		ProbeArgs:      []string{"-version"},
		VersionRegex:   `ffmpeg version ([^\s]+)`,
		Downloads: map[string]DownloadSpec{
			"windows_amd64": {
				URL:           "https://github.com/GyanD/codexffmpeg/releases/download/7.1/ffmpeg-7.1-essentials_build.zip",
				ArchiveFormat: "zip",
				ExtractFile:   "bin/ffmpeg.exe",
			},
			"windows_arm64": {
				URL:           "https://github.com/GyanD/codexffmpeg/releases/download/7.1/ffmpeg-7.1-essentials_build.zip",
				ArchiveFormat: "zip",
				ExtractFile:   "bin/ffmpeg.exe",
			},
		},
	},
	IDFFprobe: {
		ID:             IDFFprobe,
		Name:           "FFprobe",
		Description:    "Multimedia stream analyzer and container metadata extractor.",
		ExecutableBase: "ffprobe",
		ProbeArgs:      []string{"-version"},
		VersionRegex:   `ffprobe version ([^\s]+)`,
		Downloads: map[string]DownloadSpec{
			"windows_amd64": {
				URL:           "https://github.com/GyanD/codexffmpeg/releases/download/7.1/ffmpeg-7.1-essentials_build.zip",
				ArchiveFormat: "zip",
				ExtractFile:   "bin/ffprobe.exe",
			},
			"windows_arm64": {
				URL:           "https://github.com/GyanD/codexffmpeg/releases/download/7.1/ffmpeg-7.1-essentials_build.zip",
				ArchiveFormat: "zip",
				ExtractFile:   "bin/ffprobe.exe",
			},
		},
	},
	IDYtDlp: {
		ID:             IDYtDlp,
		Name:           "yt-dlp",
		Description:    "Video and audio stream downloader and extractor from web platforms.",
		ExecutableBase: "yt-dlp",
		ProbeArgs:      []string{"--version"},
		VersionRegex:   `(\d{4}\.\d{2}\.\d{2}(?:\.\d+)?)`,
		Downloads: map[string]DownloadSpec{
			"windows_amd64": {
				URL:           "https://github.com/yt-dlp/yt-dlp/releases/latest/download/yt-dlp.exe",
				ArchiveFormat: "raw",
				ExtractFile:   "yt-dlp.exe",
			},
			"windows_arm64": {
				URL:           "https://github.com/yt-dlp/yt-dlp/releases/latest/download/yt-dlp.exe",
				ArchiveFormat: "raw",
				ExtractFile:   "yt-dlp.exe",
			},
		},
	},
}

// ValidateID checks if an ID is valid and registered.
func ValidateID(id BinaryID) error {
	if !slices.Contains(AllIDs(), id) {
		return apperr.Validation(fmt.Sprintf("unsupported binary id %q", id))
	}
	return nil
}

// Repository stores user custom path configurations for external binaries.
type Repository interface {
	GetCustomPath(ctx context.Context, id BinaryID) (string, error)
	GetAllCustomPaths(ctx context.Context) (map[BinaryID]string, error)
	SetCustomPath(ctx context.Context, id BinaryID, customPath string) error
}
