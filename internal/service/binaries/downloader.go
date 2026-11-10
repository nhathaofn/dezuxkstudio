package binaries

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"app/internal/apperr"
	"app/internal/buildinfo"
	domainbinaries "app/internal/domain/binaries"
	"app/internal/fsutil"
)

type readerFunc func(p []byte) (int, error)

func (rf readerFunc) Read(p []byte) (int, error) { return rf(p) }

// Download downloads the static build of the binary, extracts it into toolsDir, and verifies it.
func (s *Service) Download(ctx context.Context, id domainbinaries.BinaryID, onProgress func(pct float64, msg string)) (domainbinaries.BinaryInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	spec, ok := domainbinaries.Registry[id]
	if !ok {
		return domainbinaries.BinaryInfo{}, apperr.Validation(fmt.Sprintf("unknown binary %q", id))
	}
	dlSpec, hasDL := spec.CurrentDownloadSpec()
	if !hasDL || dlSpec.URL == "" {
		return domainbinaries.BinaryInfo{}, apperr.New(apperr.CodeBadRequest, fmt.Sprintf("no automated download available for %s on %s/%s", spec.Name, runtime.GOOS, runtime.GOARCH))
	}

	if err := os.MkdirAll(s.toolsDir, 0755); err != nil {
		return domainbinaries.BinaryInfo{}, fmt.Errorf("binaries: create tools dir: %w", err)
	}

	if onProgress != nil {
		onProgress(0, fmt.Sprintf("Connecting to download %s...", spec.Name))
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, dlSpec.URL, nil)
	if err != nil {
		return domainbinaries.BinaryInfo{}, fmt.Errorf("binaries: request download: %w", err)
	}
	// Add user-agent header
	req.Header.Set("User-Agent", fmt.Sprintf("%s-Downloader/%s", buildinfo.ProductName, buildinfo.Version))

	resp, err := s.client.Do(req)
	if err != nil {
		return domainbinaries.BinaryInfo{}, fmt.Errorf("binaries: download failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return domainbinaries.BinaryInfo{}, fmt.Errorf("binaries: download server returned status %d", resp.StatusCode)
	}

	totalSize := resp.ContentLength
	targetPath := filepath.Join(s.toolsDir, spec.ExecutableName())

	var downloaded int64
	var lastPct float64
	var lastReport time.Time

	// Progress tracking reader wrapper
	progressReader := io.Reader(readerFunc(func(p []byte) (int, error) {
		n, err := resp.Body.Read(p)
		downloaded += int64(n)
		if onProgress != nil {
			now := time.Now()
			if totalSize > 0 {
				pct := (float64(downloaded) / float64(totalSize)) * 100
				if pct-lastPct >= 0.5 || now.Sub(lastReport) > 200*time.Millisecond || err == io.EOF {
					lastPct = pct
					lastReport = now
					msg := fmt.Sprintf("Downloaded %.1f / %.1f MB", float64(downloaded)/(1024*1024), float64(totalSize)/(1024*1024))
					onProgress(pct, msg)
				}
			} else {
				// Fix progress stall when Content-Length <= 0 (e.g. chunked transfer) by emitting periodic downloaded MB updates
				if now.Sub(lastReport) > 200*time.Millisecond || err == io.EOF {
					lastReport = now
					msg := fmt.Sprintf("Downloaded %.1f MB", float64(downloaded)/(1024*1024))
					onProgress(0.0, msg)
				}
			}
		}
		return n, err
	}))

	switch dlSpec.ArchiveFormat {
	case "zip":
		if onProgress != nil {
			onProgress(lastPct, "Downloading archive...")
		}
		tempZip := filepath.Join(s.toolsDir, fmt.Sprintf(".%s-download.zip", id))
		defer os.Remove(tempZip)

		// Download to temp zip while calculating SHA256
		hasher := sha256.New()
		zipFile, err := os.Create(tempZip)
		if err != nil {
			return domainbinaries.BinaryInfo{}, fmt.Errorf("binaries: create temp zip: %w", err)
		}
		if _, err := io.Copy(io.MultiWriter(zipFile, hasher), progressReader); err != nil {
			zipFile.Close()
			_ = os.Remove(tempZip)
			return domainbinaries.BinaryInfo{}, fmt.Errorf("binaries: save temp zip: %w", err)
		}
		zipFile.Close()

		// Verify SHA256 checksum
		if dlSpec.SHA256 != "" {
			actualHash := hex.EncodeToString(hasher.Sum(nil))
			expectedHash := strings.ToLower(strings.TrimSpace(dlSpec.SHA256))
			if actualHash != expectedHash {
				_ = os.Remove(tempZip)
				return domainbinaries.BinaryInfo{}, apperr.Integrity(fmt.Sprintf("binary %s checksum mismatch: expected %s, got %s", spec.Name, expectedHash, actualHash))
			}
		}

		if onProgress != nil {
			onProgress(95, "Extracting binary...")
		}

		if err := extractFileFromZip(tempZip, dlSpec.ExtractFile, targetPath); err != nil {
			return domainbinaries.BinaryInfo{}, fmt.Errorf("binaries: extract from zip: %w", err)
		}

	case "raw":
		if onProgress != nil {
			onProgress(lastPct, "Writing executable...")
		}
		hasher := sha256.New()
		err = fsutil.WriteAtomic(targetPath, 0755, func(w io.Writer) error {
			if _, err := io.Copy(io.MultiWriter(w, hasher), progressReader); err != nil {
				return err
			}
			if dlSpec.SHA256 != "" {
				actualHash := hex.EncodeToString(hasher.Sum(nil))
				expectedHash := strings.ToLower(strings.TrimSpace(dlSpec.SHA256))
				if actualHash != expectedHash {
					return apperr.Integrity(fmt.Sprintf("binary %s checksum mismatch: expected %s, got %s", spec.Name, expectedHash, actualHash))
				}
			}
			return nil
		})
		if err != nil {
			var ae *apperr.Error
			if errors.As(err, &ae) {
				_ = os.Remove(targetPath)
				return domainbinaries.BinaryInfo{}, ae
			}
			return domainbinaries.BinaryInfo{}, fmt.Errorf("binaries: write executable: %w", err)
		}

	default:
		return domainbinaries.BinaryInfo{}, fmt.Errorf("binaries: unsupported archive format %q", dlSpec.ArchiveFormat)
	}

	// Ensure executable permissions
	_ = os.Chmod(targetPath, 0755)

	if onProgress != nil {
		onProgress(100, fmt.Sprintf("%s installed. Probing...", spec.Name))
	}

	return s.Probe(ctx, id)
}
