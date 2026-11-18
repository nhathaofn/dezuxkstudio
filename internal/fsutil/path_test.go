package fsutil

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestIsSubpath(t *testing.T) {
	base := filepath.Join(t.TempDir(), "BaseDir")
	if !IsSubpath(base, base, false) {
		t.Fatal("non-strict IsSubpath must allow base == target")
	}
	if IsSubpath(base, base, true) {
		t.Fatal("strict IsSubpath must reject base == target")
	}
	child := filepath.Join(base, "sub", "file.txt")
	if !IsSubpath(base, child, true) {
		t.Fatal("strict IsSubpath must allow child")
	}
	if IsSubpath(base, filepath.Join(base, "..", "escape.txt"), false) {
		t.Fatal("IsSubpath must reject parent traversal")
	}
	if runtime.GOOS == "windows" {
		upperChild := filepath.Join(filepath.Dir(base), "basedir", "SUB", "File.txt")
		if !IsSubpath(base, upperChild, true) {
			t.Fatal("Windows IsSubpath must be case-insensitive")
		}
	}
}

func TestResolveSubpath(t *testing.T) {
	root := t.TempDir()
	base := filepath.Join(root, "allowed")
	outside := filepath.Join(root, "outside")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}

	target := filepath.Join(base, "nested", "new.txt")
	realTarget, realBase, err := ResolveSubpath(base, target, true)
	if err != nil {
		t.Fatalf("ResolveSubpath for new file inside base failed: %v", err)
	}
	if !IsSubpath(realBase, realTarget, true) {
		t.Fatalf("expected %q inside %q", realTarget, realBase)
	}

	if _, _, err := ResolveSubpath(base, filepath.Join(outside, "secret.txt"), false); err == nil {
		t.Fatal("ResolveSubpath must reject path outside base")
	}

	linkPath := filepath.Join(base, "escape_link")
	if err := os.Symlink(outside, linkPath); err == nil {
		if _, _, err := ResolveSubpath(base, filepath.Join(linkPath, "secret.txt"), true); err == nil {
			t.Fatal("ResolveSubpath must reject symlink escaping base")
		}
	}
}
