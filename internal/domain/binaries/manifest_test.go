package binaries_test

import (
	"net/url"
	"runtime"
	"strings"
	"testing"

	"app/internal/domain/binaries"
)

// TestManifest_AllURLsParseAsValidHTTPS validates that every download URL in the manifest
// parses as an absolute HTTPS URL with a non-empty host and path.
func TestManifest_AllURLsParseAsValidHTTPS(t *testing.T) {
	for id, entry := range binaries.Manifest {
		if len(entry.Downloads) == 0 {
			t.Errorf("manifest entry %q has no download specs defined", id)
		}
		for platform, dlSpec := range entry.Downloads {
			if dlSpec.URL == "" {
				t.Errorf("manifest entry %q platform %q has empty URL", id, platform)
				continue
			}
			parsed, err := url.ParseRequestURI(dlSpec.URL)
			if err != nil {
				t.Errorf("manifest entry %q platform %q URL %q is not valid: %v", id, platform, dlSpec.URL, err)
				continue
			}
			if parsed.Scheme != "https" {
				t.Errorf("manifest entry %q platform %q URL %q must use HTTPS scheme, got %q", id, platform, dlSpec.URL, parsed.Scheme)
			}
			if parsed.Host == "" {
				t.Errorf("manifest entry %q platform %q URL %q has empty host", id, platform, dlSpec.URL)
			}
			if parsed.Path == "" || parsed.Path == "/" {
				t.Errorf("manifest entry %q platform %q URL %q has missing resource path", id, platform, dlSpec.URL)
			}
		}
	}
}

// TestManifest_SupportedPlatformsSpecifications verifies that all supported platform targets
// have complete download specifications in each manifest entry.
func TestManifest_SupportedPlatformsSpecifications(t *testing.T) {
	expectedPlatforms := []string{
		binaries.PlatformWindowsAmd64,
		binaries.PlatformWindowsArm64,
	}

	for _, id := range binaries.AllIDs() {
		entry, ok := binaries.GetManifest(string(id))
		if !ok {
			t.Fatalf("expected GetManifest(%q) to succeed", id)
		}

		if entry.ID != id {
			t.Errorf("expected entry.ID %q, got %q", id, entry.ID)
		}
		if entry.Name == "" {
			t.Errorf("manifest entry %q has empty Name", id)
		}
		if entry.Executable == "" {
			t.Errorf("manifest entry %q has empty Executable", id)
		}
		if entry.Version == "" {
			t.Errorf("manifest entry %q has empty Version", id)
		}
		if len(entry.ProbeArgs) == 0 {
			t.Errorf("manifest entry %q has no ProbeArgs defined", id)
		}
		if entry.VersionRegex == nil {
			t.Errorf("manifest entry %q has nil VersionRegex", id)
		}

		for _, p := range expectedPlatforms {
			dlSpec, exists := entry.Downloads[p]
			if !exists {
				t.Errorf("manifest entry %q missing download spec for platform %q", id, p)
				continue
			}
			if dlSpec.URL == "" {
				t.Errorf("manifest entry %q platform %q has empty URL", id, p)
			}
			if dlSpec.ArchiveFormat != binaries.ArchiveFormatZip && dlSpec.ArchiveFormat != binaries.ArchiveFormatRaw {
				t.Errorf("manifest entry %q platform %q has unexpected ArchiveFormat %q", id, p, dlSpec.ArchiveFormat)
			}
			if dlSpec.ExtractFile == "" {
				t.Errorf("manifest entry %q platform %q has empty ExtractFile", id, p)
			}
		}
	}
}

// TestManifest_PrecompiledVersionRegexes tests that precompiled regex patterns
// correctly match expected probe output formats and reject non-matching outputs.
func TestManifest_PrecompiledVersionRegexes(t *testing.T) {
	tests := []struct {
		name        string
		id          binaries.BinaryID
		sample      string
		expectedVer string
	}{
		{
			name:        "FFmpeg standard output",
			id:          binaries.IDFFmpeg,
			sample:      "ffmpeg version 7.1-essentials_build-www.gyan.dev Copyright (c) 2000-2024 the FFmpeg developers\nbuilt with gcc",
			expectedVer: "7.1-essentials_build-www.gyan.dev",
		},
		{
			name:        "FFmpeg clean version",
			id:          binaries.IDFFmpeg,
			sample:      "ffmpeg version 7.1 Copyright (c) 2000-2024",
			expectedVer: "7.1",
		},
		{
			name:        "FFprobe standard output",
			id:          binaries.IDFFprobe,
			sample:      "ffprobe version 7.1 Copyright (c) 2007-2024 the FFmpeg developers",
			expectedVer: "7.1",
		},
		{
			name:        "FFprobe alternate version",
			id:          binaries.IDFFprobe,
			sample:      "ffprobe version 7.0.2-full_build Copyright (c) 2007-2024",
			expectedVer: "7.0.2-full_build",
		},
		{
			name:        "yt-dlp standard output",
			id:          binaries.IDYtDlp,
			sample:      "2024.11.04\n",
			expectedVer: "2024.11.04",
		},
		{
			name:        "yt-dlp dot-release output",
			id:          binaries.IDYtDlp,
			sample:      "2024.10.22.1",
			expectedVer: "2024.10.22.1",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			entry, ok := binaries.ManifestFor(tc.id)
			if !ok {
				t.Fatalf("binary %s not found in manifest", tc.id)
			}
			ver := entry.ExtractVersion(tc.sample)
			if ver != tc.expectedVer {
				t.Errorf("manifest entry %s ExtractVersion: expected %q, got %q", tc.id, tc.expectedVer, ver)
			}

			// Also verify that the derived Registry entry produces the exact same result
			regSpec := binaries.Registry[tc.id]
			regVer := regSpec.ExtractVersion(tc.sample)
			if regVer != tc.expectedVer {
				t.Errorf("registry entry %s ExtractVersion: expected %q, got %q", tc.id, tc.expectedVer, regVer)
			}
		})
	}

	// Verify rejection of non-matching strings
	negativeTests := []struct {
		id     binaries.BinaryID
		sample string
	}{
		{binaries.IDFFmpeg, "unrelated command stdout without version"},
		{binaries.IDFFprobe, "an error occurred starting binary"},
		{binaries.IDYtDlp, "version unknown string"},
	}

	for _, tc := range negativeTests {
		entry := binaries.Manifest[tc.id]
		if v := entry.ExtractVersion(tc.sample); v != "" {
			t.Errorf("expected empty version for non-matching sample on %s, got %q", tc.id, v)
		}
	}
}

// TestManifest_RunnerProgressRegexes verifies that precompiled runner progress patterns
// properly capture groups from FFmpeg stderr and yt-dlp stdout lines.
func TestManifest_RunnerProgressRegexes(t *testing.T) {
	t.Run("FFmpegTimeRe", func(t *testing.T) {
		line := "frame=  123 fps= 24 q=28.0 size=    1024kB time=00:01:23.45 bitrate= 167.8kbits/s speed=1.5x"
		matches := binaries.FFmpegTimeRe.FindStringSubmatch(line)
		if len(matches) < 4 {
			t.Fatalf("expected at least 4 submatches for time, got %d", len(matches))
		}
		if matches[1] != "00" || matches[2] != "01" || matches[3] != "23.45" {
			t.Errorf("unexpected time submatches: %v", matches[1:4])
		}
	})

	t.Run("FFmpegSpeedRe", func(t *testing.T) {
		line := "frame=  123 fps= 24 q=28.0 size=    1024kB time=00:01:23.45 bitrate= 167.8kbits/s speed= 2.34x"
		matches := binaries.FFmpegSpeedRe.FindStringSubmatch(line)
		if len(matches) < 2 {
			t.Fatalf("expected speed submatch, got %d", len(matches))
		}
		if matches[1] != "2.34x" {
			t.Errorf("expected speed '2.34x', got %q", matches[1])
		}
	})

	t.Run("YtDlpProgressRe", func(t *testing.T) {
		line := "[download]  78.9% of ~45.20MiB at 4.56MiB/s ETA 00:12"
		matches := binaries.YtDlpProgressRe.FindStringSubmatch(line)
		if len(matches) < 4 {
			t.Fatalf("expected at least 4 submatches for yt-dlp progress, got %d", len(matches))
		}
		if matches[1] != "78.9" {
			t.Errorf("expected percent '78.9', got %q", matches[1])
		}
		if matches[2] != "~45.20MiB" {
			t.Errorf("expected size '~45.20MiB', got %q", matches[2])
		}
		if matches[3] != "4.56MiB/s" {
			t.Errorf("expected speed '4.56MiB/s', got %q", matches[3])
		}
	})
}

// TestManifest_LookupHelpers verifies GetManifest and ManifestFor helpers.
func TestManifest_LookupHelpers(t *testing.T) {
	for _, id := range binaries.AllIDs() {
		entry, ok := binaries.GetManifest(string(id))
		if !ok || entry.ID != id {
			t.Errorf("GetManifest(%q) failed or returned mismatched ID", id)
		}
		entryByTyped, ok := binaries.ManifestFor(id)
		if !ok || entryByTyped.ID != id {
			t.Errorf("ManifestFor(%q) failed or returned mismatched ID", id)
		}
	}

	if _, ok := binaries.GetManifest("invalid_binary_tool"); ok {
		t.Error("expected GetManifest for invalid ID to return false")
	}
	if _, ok := binaries.ManifestFor(binaries.BinaryID("invalid_binary_tool")); ok {
		t.Error("expected ManifestFor for invalid ID to return false")
	}
}

// TestManifest_ExecutableNameAndCurrentSpec verifies executable naming conventions on Windows.
func TestManifest_ExecutableNameAndCurrentSpec(t *testing.T) {
	for id, entry := range binaries.Manifest {
		execName := entry.ExecutableName()
		if runtime.GOOS == "windows" {
			if !strings.HasSuffix(execName, ".exe") {
				t.Errorf("binary %s executable name %q on Windows must end with .exe", id, execName)
			}
		}

		if runtime.GOOS == "windows" {
			spec, ok := entry.CurrentDownloadSpec()
			if !ok {
				t.Errorf("expected CurrentDownloadSpec for %s on %s/%s to exist", id, runtime.GOOS, runtime.GOARCH)
			}
			if spec.URL == "" {
				t.Errorf("expected non-empty URL in CurrentDownloadSpec for %s", id)
			}
		}
	}
}
