// Package buildinfo is the single source of product identity and build metadata.
//
// Every value is injected at link time by the Taskfile:
//
//	-ldflags "-X app/internal/buildinfo.Version=1.2.3 -X app/internal/buildinfo.Commit=abc123 ..."
//
// The literals below are only development fallbacks.
package buildinfo

import "runtime"

var (
	// AppID is the stable machine identifier of the desktop product. It names the
	// per-user data/config/log directories and the single-instance lock, so it must
	// never change after the first public release.
	AppID = "DezuxkStudio"

	// ServerAppID is the stable machine identifier of the headless server binary.
	ServerAppID = "DezuxkStudio-server"

	// ProductName is the human-readable product name shown in the UI.
	ProductName = "DezuxkStudio"

	// Version is the semantic version (without leading "v").
	Version = "0.0.0-dev"

	// Commit is the short git commit hash.
	Commit = "unknown"

	// BuildDate is the RFC 3339 UTC build timestamp.
	BuildDate = ""

	// UpdateFeedURL is the default release feed (GitHub "latest release" API format).
	// Empty disables update checks unless overridden by configuration.
	UpdateFeedURL = ""
)

// Info is a serialisable snapshot of the build metadata.
type Info struct {
	AppID       string `json:"app_id"`
	ProductName string `json:"product_name"`
	Version     string `json:"version"`
	Commit      string `json:"commit"`
	BuildDate   string `json:"build_date"`
	GoVersion   string `json:"go_version"`
	OS          string `json:"os"`
	Arch        string `json:"arch"`
	Dev         bool   `json:"dev"`
}

// Get returns the current build metadata.
func Get() Info {
	return Info{
		AppID:       AppID,
		ProductName: ProductName,
		Version:     Version,
		Commit:      Commit,
		BuildDate:   BuildDate,
		GoVersion:   runtime.Version(),
		OS:          runtime.GOOS,
		Arch:        runtime.GOARCH,
		Dev:         IsDev(),
	}
}

// IsDev reports whether the binary was built with the "dev" build tag (wails dev).
func IsDev() bool { return devBuild }
