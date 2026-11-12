package lookup

import (
	"os"
	"path/filepath"
	"testing"
)

func touch(t *testing.T, p string) string {
	t.Helper()
	if err := os.WriteFile(p, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestFirstHonoursPriorityAndFallsThrough(t *testing.T) {
	dir := t.TempDir()
	tools := touch(t, filepath.Join(dir, "tool.exe"))
	missingCustom := filepath.Join(dir, "gone.exe")

	p, src, ok := First(
		Candidate[string]{Source: "custom", Find: File(missingCustom)},
		Candidate[string]{Source: "tools", Find: File(tools)},
		Candidate[string]{Source: "system", Find: func() string { t.Fatal("must stop at the first hit"); return "" }},
	)
	if !ok || p != tools || src != "tools" {
		t.Fatalf("got %q %q %v", p, src, ok)
	}

	custom := touch(t, filepath.Join(dir, "mine.exe"))
	if p, src, _ := First(Candidate[string]{Source: "custom", Find: File(custom)}, Candidate[string]{Source: "tools", Find: File(tools)}); p != custom || src != "custom" {
		t.Fatalf("custom path must win, got %q %q", p, src)
	}
	if _, _, ok := First(Candidate[string]{Source: "custom", Find: File("")}); ok {
		t.Fatal("empty path must not match")
	}
	if File(dir)() != "" {
		t.Fatal("a directory is not an executable")
	}
}

func TestFilesExpandsAndPicksFirstExisting(t *testing.T) {
	dir := t.TempDir()
	b := touch(t, filepath.Join(dir, "b"))
	got := Files(func(p string) string { return filepath.Join(dir, p) }, "a", "b")()
	if got != b {
		t.Fatalf("got %q want %q", got, b)
	}
}
