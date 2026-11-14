package engine

import (
	"path/filepath"
	"runtime"
	"testing"
)

func TestResolveMediaPath(t *testing.T) {
	mediaDir := filepath.Join(t.TempDir(), "media")
	absOutside := filepath.Join(t.TempDir(), "outside", "pic.png")
	absWithMediaSegment := filepath.Join(t.TempDir(), "media", "nested.png")

	absUnclean := "  " + filepath.Dir(absOutside) + string(filepath.Separator) + "sub" + string(filepath.Separator) + ".." + string(filepath.Separator) + "pic.png  "

	tests := []struct {
		name     string
		raw      string
		mediaDir string
		want     string
	}{
		{name: "empty", raw: "", mediaDir: mediaDir, want: ""},
		{name: "whitespace only", raw: "   \t ", mediaDir: mediaDir, want: ""},
		{name: "dot", raw: ".", mediaDir: mediaDir, want: ""},
		{name: "dot slash", raw: "./", mediaDir: mediaDir, want: ""},
		{name: "sub then parent collapses to dot", raw: "sub/..", mediaDir: mediaDir, want: ""},
		{name: "media slash only", raw: "/media/", mediaDir: mediaDir, want: ""},
		{name: "media bare slash only", raw: "media/", mediaDir: mediaDir, want: ""},
		{name: "media dot", raw: "/media/.", mediaDir: mediaDir, want: ""},
		{name: "leading slash media prefix", raw: "/media/generated.png", mediaDir: mediaDir, want: filepath.Join(mediaDir, "generated.png")},
		{name: "leading backslash media prefix", raw: `\media\generated.png`, mediaDir: mediaDir, want: filepath.Join(mediaDir, "generated.png")},
		{name: "relative media slash prefix", raw: "media/generated.png", mediaDir: mediaDir, want: filepath.Join(mediaDir, "generated.png")},
		{name: "relative media backslash prefix", raw: `media\generated.png`, mediaDir: mediaDir, want: filepath.Join(mediaDir, "generated.png")},
		{name: "whitespace around media prefix", raw: "  /media/sub/dir/pic.png  ", mediaDir: mediaDir, want: filepath.Join(mediaDir, "sub", "dir", "pic.png")},
		{name: "double slash after media prefix", raw: "/media//pic.png", mediaDir: mediaDir, want: filepath.Join(mediaDir, "pic.png")},
		{name: "nested media folder preserved", raw: "/media/media/pic.png", mediaDir: mediaDir, want: filepath.Join(mediaDir, "media", "pic.png")},
		{name: "leading double dot filename", raw: "..foo.png", mediaDir: mediaDir, want: filepath.Join(mediaDir, "..foo.png")},
		{name: "leading double dot filename via media prefix", raw: "/media/..foo.png", mediaDir: mediaDir, want: filepath.Join(mediaDir, "..foo.png")},
		{name: "hidden dot filename", raw: ".hidden.png", mediaDir: mediaDir, want: filepath.Join(mediaDir, ".hidden.png")},
		{name: "nested leading double dot filename", raw: "sub/..bar.png", mediaDir: mediaDir, want: filepath.Join(mediaDir, "sub", "..bar.png")},
		{name: "parent cleaning inside mediaDir", raw: "sub/../ok.png", mediaDir: mediaDir, want: filepath.Join(mediaDir, "ok.png")},
		{name: "dot dot rejected", raw: "..", mediaDir: mediaDir, want: ""},
		{name: "parent traversal rejected", raw: "../escape.png", mediaDir: mediaDir, want: ""},
		{name: "backslash traversal rejected", raw: `..\escape.png`, mediaDir: mediaDir, want: ""},
		{name: "nested traversal rejected", raw: "sub/../../escape.png", mediaDir: mediaDir, want: ""},
		{name: "media prefix traversal rejected", raw: "/media/../../secret.png", mediaDir: mediaDir, want: ""},
		{name: "relative media prefix traversal rejected", raw: "media/../../secret.png", mediaDir: mediaDir, want: ""},
		{name: "media prefix dot dot rejected", raw: "/media/..", mediaDir: mediaDir, want: ""},
		{name: "absolute path returned cleaned", raw: absUnclean, mediaDir: mediaDir, want: absOutside},
		{name: "absolute path containing media segment preserved", raw: absWithMediaSegment, mediaDir: mediaDir, want: absWithMediaSegment},
		{name: "empty mediaDir relative path", raw: "  out.png  ", mediaDir: "", want: "out.png"},
		{name: "empty mediaDir strips media prefix", raw: "/media/sub/out.png", mediaDir: "", want: filepath.Join("sub", "out.png")},
		{name: "empty mediaDir leading double dot filename", raw: "..foo.png", mediaDir: "", want: "..foo.png"},
		{name: "empty mediaDir rejects empty and dot", raw: ".", mediaDir: "", want: ""},
		{name: "empty mediaDir rejects dot dot", raw: "..", mediaDir: "", want: ""},
		{name: "empty mediaDir rejects traversal", raw: "../escape.png", mediaDir: "", want: ""},
		{name: "empty mediaDir absolute path", raw: absOutside, mediaDir: "", want: absOutside},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ResolveMediaPath(tc.raw, tc.mediaDir)
			if got != tc.want {
				t.Fatalf("ResolveMediaPath(%q, %q) = %q, want %q", tc.raw, tc.mediaDir, got, tc.want)
			}
		})
	}

	if runtime.GOOS == "windows" {
		if got := ResolveMediaPath(`/media/C:/Windows/System32`, mediaDir); got != "" {
			t.Fatalf("expected absolute path inside /media/ prefix to be rejected, got %q", got)
		}
	}
}
