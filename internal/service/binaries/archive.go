package binaries

import (
	"archive/zip"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"app/internal/fsutil"
)

// extractFileFromZip extracts a specific entry from a zip archive into targetPath atomically.
// It matches either exact path or nested folder path preserving manifest directory structure (e.g. ".../bin/ffmpeg.exe"),
// while rejecting directory traversal attempts (Zip Slip).
func extractFileFromZip(zipPath, targetEntry, targetPath string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer r.Close()

	targetClean := strings.TrimPrefix(filepath.ToSlash(targetEntry), "/")

	var matchedFile *zip.File
	for _, f := range r.File {
		// Prevent Zip Slip directory traversal attacks
		if strings.Contains(f.Name, "..") {
			continue
		}

		cleanName := strings.TrimPrefix(filepath.ToSlash(f.Name), "/")
		if cleanName == targetClean || strings.HasSuffix(cleanName, "/"+targetClean) {
			matchedFile = f
			break
		}
	}

	if matchedFile == nil {
		return fmt.Errorf("entry %q not found in zip archive", targetEntry)
	}

	rc, err := matchedFile.Open()
	if err != nil {
		return err
	}
	defer rc.Close()

	return fsutil.WriteAtomic(targetPath, 0755, func(w io.Writer) error {
		_, err := io.Copy(w, rc)
		return err
	})
}
