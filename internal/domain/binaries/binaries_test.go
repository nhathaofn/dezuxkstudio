package binaries

import (
	"fmt"
	"regexp"
	"runtime"
	"testing"
)

func TestBinarySpecAndManifestEntryParity_ExecutableName(t *testing.T) {
	want := "custom-tool"
	if runtime.GOOS == "windows" {
		want += ".exe"
	}

	spec := BinarySpec{ExecutableBase: "custom-tool"}
	entry := ManifestEntry{Executable: "custom-tool"}
	if got := spec.ExecutableName(); got != want {
		t.Fatalf("BinarySpec.ExecutableName() = %q, want %q", got, want)
	}
	if got := entry.ExecutableName(); got != want {
		t.Fatalf("ManifestEntry.ExecutableName() = %q, want %q", got, want)
	}
	if spec.ExecutableName() != entry.ExecutableName() {
		t.Fatalf("parity mismatch: spec=%q entry=%q", spec.ExecutableName(), entry.ExecutableName())
	}

	for _, id := range AllIDs() {
		regSpec := Registry[id]
		manEntry := Manifest[id]
		if regSpec.ExecutableName() != manEntry.ExecutableName() {
			t.Errorf("%s: Registry.ExecutableName()=%q != Manifest.ExecutableName()=%q", id, regSpec.ExecutableName(), manEntry.ExecutableName())
		}
	}
}

func TestBinarySpecAndManifestEntryParity_CurrentDownloadSpec(t *testing.T) {
	currentKey := fmt.Sprintf("%s_%s", runtime.GOOS, runtime.GOARCH)
	expected := DownloadSpec{
		URL:           "https://example.com/tool.zip",
		ArchiveFormat: ArchiveFormatZip,
		ExtractFile:   "bin/tool.exe",
		SHA256:        "deadbeef",
	}

	t.Run("matching platform", func(t *testing.T) {
		downloads := map[string]DownloadSpec{currentKey: expected}
		spec := BinarySpec{Downloads: downloads}
		entry := ManifestEntry{Downloads: downloads}

		gotSpec, okSpec := spec.CurrentDownloadSpec()
		gotEntry, okEntry := entry.CurrentDownloadSpec()
		if !okSpec || !okEntry {
			t.Fatalf("expected both specs found: spec=%v entry=%v", okSpec, okEntry)
		}
		if gotSpec != expected || gotEntry != expected || gotSpec != gotEntry {
			t.Fatalf("download spec mismatch: spec=%+v entry=%+v want=%+v", gotSpec, gotEntry, expected)
		}
	})

	t.Run("missing platform and nil map", func(t *testing.T) {
		for _, downloads := range []map[string]DownloadSpec{
			nil,
			{"nonexistent_arch": expected},
		} {
			spec := BinarySpec{Downloads: downloads}
			entry := ManifestEntry{Downloads: downloads}

			gotSpec, okSpec := spec.CurrentDownloadSpec()
			gotEntry, okEntry := entry.CurrentDownloadSpec()
			if okSpec || okEntry {
				t.Fatalf("expected missing spec for %v: spec=%v entry=%v", downloads, okSpec, okEntry)
			}
			if gotSpec != (DownloadSpec{}) || gotEntry != (DownloadSpec{}) {
				t.Fatalf("expected zero DownloadSpec: spec=%+v entry=%+v", gotSpec, gotEntry)
			}
		}
	})

	t.Run("registry and manifest parity", func(t *testing.T) {
		for _, id := range AllIDs() {
			regDL, regOK := Registry[id].CurrentDownloadSpec()
			manDL, manOK := Manifest[id].CurrentDownloadSpec()
			if regOK != manOK || regDL != manDL {
				t.Errorf("%s: Registry.CurrentDownloadSpec()=(%+v, %v) != Manifest.CurrentDownloadSpec()=(%+v, %v)",
					id, regDL, regOK, manDL, manOK)
			}
		}
	})
}

func TestBinarySpecAndManifestEntryParity_ExtractVersion(t *testing.T) {
	t.Run("precompiled regex parity", func(t *testing.T) {
		cases := []struct {
			name   string
			re     *regexp.Regexp
			output string
			want   string
		}{
			{
				name:   "capture group",
				re:     regexp.MustCompile(`tool v([0-9]+\.[0-9]+\.[0-9]+)`),
				output: "tool v1.2.3 (build 42)",
				want:   "1.2.3",
			},
			{
				name:   "multiple capture groups returns first group",
				re:     regexp.MustCompile(`([a-z]+)-([0-9.]+)`),
				output: "app-2.4.1",
				want:   "app",
			},
			{
				name:   "non-capturing regex returns full match",
				re:     regexp.MustCompile(`\d+\.\d+\.\d+`),
				output: "release 3.4.5 ready",
				want:   "3.4.5",
			},
			{
				name:   "no match",
				re:     regexp.MustCompile(`version ([0-9.]+)`),
				output: "nothing useful here",
				want:   "",
			},
			{
				name:   "nil regex",
				re:     nil,
				output: "version 1.0.0",
				want:   "",
			},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				spec := BinarySpec{VersionRe: tc.re}
				entry := ManifestEntry{VersionRegex: tc.re}

				gotSpec := spec.ExtractVersion(tc.output)
				gotEntry := entry.ExtractVersion(tc.output)
				if gotSpec != tc.want {
					t.Errorf("BinarySpec.ExtractVersion() = %q, want %q", gotSpec, tc.want)
				}
				if gotEntry != tc.want {
					t.Errorf("ManifestEntry.ExtractVersion() = %q, want %q", gotEntry, tc.want)
				}
				if gotSpec != gotEntry {
					t.Errorf("parity mismatch: spec=%q entry=%q", gotSpec, gotEntry)
				}
			})
		}
	})

	t.Run("BinarySpec raw VersionRegex fallback", func(t *testing.T) {
		cases := []struct {
			name   string
			spec   BinarySpec
			output string
			want   string
		}{
			{
				name:   "capture group",
				spec:   BinarySpec{VersionRegex: `v([0-9.]+)`},
				output: "cli v9.8.7",
				want:   "9.8.7",
			},
			{
				name:   "non-capturing",
				spec:   BinarySpec{VersionRegex: `[0-9]+\.[0-9]+\.[0-9]+`},
				output: "cli 4.5.6",
				want:   "4.5.6",
			},
			{
				name:   "no match",
				spec:   BinarySpec{VersionRegex: `v([0-9.]+)`},
				output: "no version",
				want:   "",
			},
			{
				name:   "empty regex",
				spec:   BinarySpec{VersionRegex: ""},
				output: "cli v1.2.3",
				want:   "",
			},
			{
				name:   "invalid regex returns empty",
				spec:   BinarySpec{VersionRegex: "[unclosed"},
				output: "cli v1.2.3",
				want:   "",
			},
			{
				name: "precompiled takes precedence over raw regex",
				spec: BinarySpec{
					VersionRe:    regexp.MustCompile(`pre-(\d+)`),
					VersionRegex: `raw-(\d+)`,
				},
				output: "pre-11 raw-22",
				want:   "11",
			},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				if got := tc.spec.ExtractVersion(tc.output); got != tc.want {
					t.Errorf("BinarySpec.ExtractVersion() = %q, want %q", got, tc.want)
				}
			})
		}
	})
}
