package watermark_test

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"app/internal/fsutil"
	"app/pkg/watermark"
)

// TestAdversarial_WriteAtomic_MidwayFailure simulates a writer returning an error
// after partially streaming bytes into the temp file.
func TestAdversarial_WriteAtomic_MidwayFailure(t *testing.T) {
	dir := t.TempDir()
	targetPath := filepath.Join(dir, "original_image.png")
	originalData := bytes.Repeat([]byte("ORIGINAL_UNTOUCHED_IMAGE_DATA_1234567890"), 1000)

	if err := os.WriteFile(targetPath, originalData, 0o644); err != nil {
		t.Fatalf("failed to write original file: %v", err)
	}
	origHash := sha256.Sum256(originalData)

	// Stream write that fails midway after writing partial content
	simulatedErr := errors.New("simulated disk full or pipe break midway")
	err := fsutil.WriteAtomic(targetPath, 0o644, func(w io.Writer) error {
		partial := bytes.Repeat([]byte("CORRUPTED_PARTIAL_BYTES"), 500)
		if _, writeErr := w.Write(partial); writeErr != nil {
			return writeErr
		}
		return simulatedErr
	})

	if err == nil {
		t.Fatalf("expected simulated write error, got nil")
	}

	// 1. Verify original file is untruncated and completely untouched
	currentData, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("failed to read target file after error: %v", err)
	}
	if len(currentData) != len(originalData) {
		t.Fatalf("file length changed: got %d, want %d", len(currentData), len(originalData))
	}
	currentHash := sha256.Sum256(currentData)
	if currentHash != origHash {
		t.Fatalf("target file corrupted during midway write failure!")
	}

	// 2. Verify NO lingering temporary dot-files remain
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("failed to read directory: %v", err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") {
			t.Fatalf("lingering temporary dot-file found: %s", entry.Name())
		}
	}
	if len(entries) != 1 || entries[0].Name() != "original_image.png" {
		t.Fatalf("unexpected directory contents: %+v", entries)
	}
}

// TestAdversarial_WriteAtomic_LockedDestination verifies behavior when destination
// file is locked on Windows during atomic rename.
func TestAdversarial_WriteAtomic_LockedDestination(t *testing.T) {
	dir := t.TempDir()
	targetPath := filepath.Join(dir, "locked_target.png")
	originalData := []byte("ORIGINAL_FILE_CONTENT_SAFE_FROM_LOCK")

	if err := os.WriteFile(targetPath, originalData, 0o644); err != nil {
		t.Fatalf("failed to write initial file: %v", err)
	}

	// Lock the destination file exclusively
	lockFile, err := os.OpenFile(targetPath, os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("failed to acquire lock handle on destination file: %v", err)
	}

	// Attempt WriteAtomic
	newData := []byte("NEW_DATA_SHOULD_NEVER_OVERWRITE_LOCKED_FILE")
	writeErr := watermark.WriteAtomic(targetPath, newData, 0o644)
	if writeErr == nil {
		_ = lockFile.Close()
		t.Fatalf("expected atomic write to fail when destination is locked")
	}

	// Release lock
	_ = lockFile.Close()

	// 1. Destination must survive unmodified
	afterBytes, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("failed to read target file: %v", err)
	}
	if !bytes.Equal(afterBytes, originalData) {
		t.Fatalf("original file corrupted! got %q, want %q", afterBytes, originalData)
	}

	// 2. No lingering dot-files (.tmp-*)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("failed to read dir: %v", err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") {
			t.Fatalf("lingering temp dot-file found after lock failure: %s", entry.Name())
		}
	}
}

// TestAdversarial_WriteAtomic_ConcurrencyStress tests 100 concurrent workers
// writing and overwriting distinct files in the same directory.
func TestAdversarial_WriteAtomic_ConcurrencyStress(t *testing.T) {
	dir := t.TempDir()
	const numWorkers = 50
	const iterations = 5

	var wg sync.WaitGroup
	errCh := make(chan error, numWorkers*iterations)

	for workerID := 0; workerID < numWorkers; workerID++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			filePath := filepath.Join(dir, fmt.Sprintf("file_%03d.png", id))

			for iter := 0; iter < iterations; iter++ {
				payload := fmt.Appendf(nil, "worker=%03d-iter=%03d-%s", id, iter, strings.Repeat("X", 2048))
				if err := watermark.WriteAtomic(filePath, payload, 0o644); err != nil {
					errCh <- fmt.Errorf("worker %d iter %d failed: %w", id, iter, err)
					return
				}

				// Verify immediate consistency
				readBack, err := os.ReadFile(filePath)
				if err != nil {
					errCh <- fmt.Errorf("worker %d iter %d read failed: %w", id, iter, err)
					return
				}
				if !bytes.Equal(readBack, payload) {
					errCh <- fmt.Errorf("worker %d iter %d content mismatch", id, iter)
					return
				}
			}
		}(workerID)
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Errorf("concurrency error: %v", err)
	}

	// Check directory: exactly numWorkers files, ZERO temp dot-files
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir failed: %v", err)
	}

	if len(entries) != numWorkers {
		t.Fatalf("expected %d files in dir, got %d", numWorkers, len(entries))
	}

	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") {
			t.Fatalf("found lingering temp file: %s", entry.Name())
		}
	}
}

// TestAdversarial_WriteAtomic_RaceOnSameFile verifies that concurrent writers
// racing on the exact same destination file never leave partial corrupted state
// or lingering temp files.
func TestAdversarial_WriteAtomic_RaceOnSameFile(t *testing.T) {
	dir := t.TempDir()
	targetPath := filepath.Join(dir, "shared_target.png")
	initial := []byte("INITIAL_BASE_CONTENT")
	if err := os.WriteFile(targetPath, initial, 0o644); err != nil {
		t.Fatalf("write initial failed: %v", err)
	}

	const numRacers = 20
	var wg sync.WaitGroup

	for i := 0; i < numRacers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			// Each racer writes a distinct repeated byte
			char := byte('A' + (idx % 26))
			data := bytes.Repeat([]byte{char}, 4096)
			// WriteAtomic might fail on Windows if another goroutine has the file open during rename,
			// but it must NEVER leave partial files or corrupt data.
			_ = watermark.WriteAtomic(targetPath, data, 0o644)
		}(i)
	}

	wg.Wait()

	// Read final file content
	finalData, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("failed to read shared target file: %v", err)
	}

	// Content must be either the initial content or completely uniform repeated char (never interleaved)
	if !bytes.Equal(finalData, initial) {
		if len(finalData) != 4096 {
			t.Fatalf("corrupted file length: got %d, expected 4096 or %d", len(finalData), len(initial))
		}
		char := finalData[0]
		for i, b := range finalData {
			if b != char {
				t.Fatalf("corrupted interleaved bytes at offset %d: got %c, expected %c", i, b, char)
			}
		}
	}

	// Verify no lingering temp files
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir failed: %v", err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") {
			t.Fatalf("lingering temp file found after race: %s", entry.Name())
		}
	}
}
