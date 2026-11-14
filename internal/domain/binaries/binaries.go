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
	SHA256        string // Expected hex-encoded SHA256 checksum
}

// BinarySpec declares a registered binary tool.
type BinarySpec struct {
	ID             BinaryID
	Name           string
	Description    string
	ExecutableBase string
	ProbeArgs      []string
	VersionRegex   string
	VersionRe      *regexp.Regexp
	Downloads      map[string]DownloadSpec // key: "<os>_<arch>" e.g. "windows_amd64"
}

// ExecutableName returns the OS-specific file name (e.g. ffmpeg.exe on Windows).
func (s BinarySpec) ExecutableName() string {
	return executableName(s.ExecutableBase)
}

// CurrentDownloadSpec returns the download specification for the current OS and architecture, if any.
func (s BinarySpec) CurrentDownloadSpec() (DownloadSpec, bool) {
	return currentDownloadSpec(s.Downloads)
}

// ExtractVersion extracts a version string from output using the spec's precompiled or compiled regex.
func (s BinarySpec) ExtractVersion(output string) string {
	return extractVersion(s.VersionRe, s.VersionRegex, output)
}

func executableName(base string) string {
	if runtime.GOOS == "windows" {
		return base + ".exe"
	}
	return base
}

func currentDownloadSpec(downloads map[string]DownloadSpec) (DownloadSpec, bool) {
	key := fmt.Sprintf("%s_%s", runtime.GOOS, runtime.GOARCH)
	spec, ok := downloads[key]
	return spec, ok
}

func extractVersion(re *regexp.Regexp, rawRegex, output string) string {
	if re == nil {
		if rawRegex == "" {
			return ""
		}
		compiled, err := regexp.Compile(rawRegex)
		if err != nil {
			return ""
		}
		re = compiled
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

// Registry holds the official binary specifications, derived from the centralized Manifest.
var Registry = map[BinaryID]BinarySpec{
	IDFFmpeg:  toBinarySpec(Manifest[IDFFmpeg]),
	IDFFprobe: toBinarySpec(Manifest[IDFFprobe]),
	IDYtDlp:   toBinarySpec(Manifest[IDYtDlp]),
}

func toBinarySpec(entry ManifestEntry) BinarySpec {
	var versionRegexStr string
	if entry.VersionRegex != nil {
		versionRegexStr = entry.VersionRegex.String()
	}
	return BinarySpec{
		ID:             entry.ID,
		Name:           entry.Name,
		Description:    entry.Description,
		ExecutableBase: entry.Executable,
		ProbeArgs:      entry.ProbeArgs,
		VersionRegex:   versionRegexStr,
		VersionRe:      entry.VersionRegex,
		Downloads:      entry.Downloads,
	}
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

// Download progress message keys (translated by the frontend under the same names).
const (
	ProgressConnecting  = "tools.progress.connecting"  // {name}
	ProgressDownloaded  = "tools.progress.downloaded"  // {done} {total} (MB)
	ProgressDownloadedN = "tools.progress.downloadedN" // {done} (MB, size unknown)
	ProgressExtracting  = "tools.progress.extracting"
	ProgressWriting     = "tools.progress.writing"
	ProgressInstalled   = "tools.progress.installed" // {name}
)
