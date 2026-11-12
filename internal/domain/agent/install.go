package agent

import "fmt"

// InstallScheme names how a provider's official release channel is read. The channel's addresses and
// file names live in the provider's InstallSpec, so the service code never mentions a vendor.
type InstallScheme string

const (
	// SchemeVersionFile: BaseURL/<LatestPath> holds the newest version as plain text; BaseURL/<version>/manifest.json
	// holds each platform's SHA-256; the binary is BaseURL/<version>/<platform>/<Executable>.
	SchemeVersionFile InstallScheme = "version_file"
	// SchemeManifest: BaseURL/manifests/<platform>.json is {version, url, sha512} for that platform.
	SchemeManifest InstallScheme = "manifest"
	// SchemeReleasePackage: the newest GitHub release lists a tar.gz package per platform plus a SHA256SUMS file.
	SchemeReleasePackage InstallScheme = "release_package"
)

// InstallSpec declares where a provider's CLI is published so the app can keep its own private copy.
type InstallSpec struct {
	Scheme  InstallScheme
	BaseURL string
	// Platforms maps GOOS_GOARCH to the platform token the channel uses; a host that is not listed
	// has no managed copy and keeps using whatever CLI is on the machine.
	Platforms map[string]string
	// Executable is the CLI's path inside one version's folder.
	Executable string
	// LatestPath (SchemeVersionFile) is the file that names the newest version.
	LatestPath string
	// LatestAPI (SchemeReleasePackage) is the JSON endpoint of the newest release; TagPrefix is stripped from its tag.
	LatestAPI string
	TagPrefix string
	// PackageAsset / ChecksumAsset (SchemeReleasePackage) are asset names; {platform} is replaced.
	PackageAsset  string
	ChecksumAsset string
	// Env is set on every process of this CLI so its own updater and installer stay out of the way: the
	// app keeps its copy current itself and must not let the CLI rewrite itself or the user's shell setup.
	Env map[string]string
}

// Platform returns the channel's token for the host, if the channel publishes for it.
func (i InstallSpec) Platform(goos, goarch string) (string, bool) {
	p, ok := i.Platforms[fmt.Sprintf("%s_%s", goos, goarch)]
	return p, ok && p != ""
}

// Managed reports whether the app can keep its own copy of this provider's CLI.
func (i InstallSpec) Managed() bool { return i.Scheme != "" && i.BaseURL != "" }

// ManagedCopy is a CLI copy the app downloaded itself and keeps up to date.
type ManagedCopy struct {
	Version string
	Path    string
}
