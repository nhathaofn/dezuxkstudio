package platform

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOpenDirectoryValidation(t *testing.T) {
	// 1. Relative path rejected
	if err := OpenDirectory("relative/path"); err == nil {
		t.Fatal("expected error for relative path, got nil")
	}

	// 2. Non-existent path rejected
	nonExistent := filepath.Join(t.TempDir(), "does_not_exist_subfolder")
	if err := OpenDirectory(nonExistent); err == nil {
		t.Fatal("expected error for non-existent path, got nil")
	}

	// 3. Regular file rejected (not a directory)
	tempFile := filepath.Join(t.TempDir(), "regular_file.txt")
	if err := os.WriteFile(tempFile, []byte("content"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := OpenDirectory(tempFile); err == nil {
		t.Fatal("expected error when path is a file not a directory, got nil")
	}
}
