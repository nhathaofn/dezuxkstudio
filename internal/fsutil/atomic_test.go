package fsutil

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteAtomicReplacesContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f.json")
	if err := WriteFileAtomic(path, []byte("one"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteFileAtomic(path, []byte("two"), 0o600); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); string(b) != "two" {
		t.Fatalf("content = %q", b)
	}
}

func TestWriteAtomicKeepsOldFileOnFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.json")
	_ = WriteFileAtomic(path, []byte("old"), 0o600)
	err := WriteAtomic(path, 0o600, func(w io.Writer) error {
		_, _ = w.Write([]byte("partial"))
		return errors.New("disk full")
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if b, _ := os.ReadFile(path); string(b) != "old" {
		t.Fatalf("old content lost: %q", b)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("temp file left behind: %v", entries)
	}
}
