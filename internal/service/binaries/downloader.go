package binaries

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
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
	"app/internal/progress"
)

type readerFunc func(p []byte) (int, error)

func (rf readerFunc) Read(p []byte) (int, error) { return rf(p) }

// Download downloads the static build of the binary, extracts it into toolsDir, and verifies it.
// Progress goes to the context's progress sink (internal/progress); each report re-arms the
// action's idle timeout, which is what lets a large download run as long as data keeps flowing.
func (s *Service) Download(ctx context.Context, id domainbinaries.BinaryID) (domainbinaries.BinaryInfo, error) {
	report := func(pct float64, key string, params map[string]string, text string) {
		progress.Report(ctx, progress.Update{Percent: pct, Key: key, Params: params, Message: text})
	}
	// One download at a time (they share toolsDir); a waiting request honours cancellation
	// instead of blocking silently until the running download ends.
	select {
	case s.dlSlot <- struct{}{}:
		defer func() { <-s.dlSlot }()
	case <-ctx.Done():
		return domainbinaries.BinaryInfo{}, ctx.Err()
	}

	spec, ok := domainbinaries.Registry[id]
	if !ok {
		return domainbinaries.BinaryInfo{}, apperr.Validation(fmt.Sprintf("unknown binary %q", id))
	}
	dlSpec, hasDL := spec.CurrentDownloadSpec()
	if !hasDL || dlSpec.URL == "" {
		return domainbinaries.BinaryInfo{}, apperr.New(apperr.CodeBadRequest, fmt.Sprintf("no automated download available for %s on %s/%s", spec.Name, runtime.GOOS, runtime.GOARCH))
	}

	if err := os.MkdirAll(s.toolsDir, 0755); err != nil {
		return domainbinaries.BinaryInfo{}, apperr.Wrap(apperr.CodeUnavailable, "could not prepare tools directory", err)
	}

	report(0, domainbinaries.ProgressConnecting, map[string]string{"name": spec.Name}, fmt.Sprintf("Connecting to download %s...", spec.Name))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, dlSpec.URL, nil)
	if err != nil {
		return domainbinaries.BinaryInfo{}, apperr.Wrap(apperr.CodeUnavailable, "could not create download request", err)
	}
	// Add user-agent header
	req.Header.Set("User-Agent", fmt.Sprintf("%s-Downloader/%s", buildinfo.ProductName, buildinfo.Version))

	resp, err := s.client.Do(req)
	if err != nil {
		return domainbinaries.BinaryInfo{}, apperr.Wrap(apperr.CodeUnavailable, "could not reach the download server; check the network connection", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return domainbinaries.BinaryInfo{}, apperr.Wrap(apperr.CodeUnavailable, fmt.Sprintf("download server returned HTTP %d", resp.StatusCode), nil)
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
		{
			now := time.Now()
			if totalSize > 0 {
				pct := (float64(downloaded) / float64(totalSize)) * 100
				if pct-lastPct >= 0.5 || now.Sub(lastReport) > 200*time.Millisecond || err == io.EOF {
					lastPct = pct
					lastReport = now
					done, total := fmt.Sprintf("%.1f", float64(downloaded)/(1024*1024)), fmt.Sprintf("%.1f", float64(totalSize)/(1024*1024))
					report(pct, domainbinaries.ProgressDownloaded, map[string]string{"done": done, "total": total},
						fmt.Sprintf("Downloaded %s / %s MB", done, total))
				}
			} else {
				// Fix progress stall when Content-Length <= 0 (e.g. chunked transfer) by emitting periodic downloaded MB updates
				if now.Sub(lastReport) > 200*time.Millisecond || err == io.EOF {
					lastReport = now
					done := fmt.Sprintf("%.1f", float64(downloaded)/(1024*1024))
					report(0, domainbinaries.ProgressDownloadedN, map[string]string{"done": done}, fmt.Sprintf("Downloaded %s MB", done))
				}
			}
		}
		return n, err
	}))

	switch dlSpec.ArchiveFormat {
	case "zip":
		tempZip := filepath.Join(s.toolsDir, fmt.Sprintf(".%s-download.zip", id))
		defer os.Remove(tempZip)

		// Download to temp zip while calculating SHA256
		hasher := sha256.New()
		zipFile, err := os.Create(tempZip)
		if err != nil {
			return domainbinaries.BinaryInfo{}, apperr.Wrap(apperr.CodeUnavailable, "could not create temporary download file", err)
		}
		if _, err := io.Copy(io.MultiWriter(zipFile, hasher), progressReader); err != nil {
			zipFile.Close()
			_ = os.Remove(tempZip)
			return domainbinaries.BinaryInfo{}, apperr.Wrap(apperr.CodeUnavailable, "the download was interrupted; try again", err)
		}
		zipFile.Close()

		// Verify SHA256 checksum
		if err := verifyChecksum(spec.Name, dlSpec.SHA256, hasher); err != nil {
			_ = os.Remove(tempZip)
			return domainbinaries.BinaryInfo{}, err
		}

		report(95, domainbinaries.ProgressExtracting, nil, "Extracting binary...")

		if err := extractFileFromZip(tempZip, dlSpec.ExtractFile, targetPath); err != nil {
			return domainbinaries.BinaryInfo{}, apperr.Wrap(apperr.CodeIntegrity, "the downloaded archive is damaged or incomplete", err)
		}

	case "raw":
		report(lastPct, domainbinaries.ProgressWriting, nil, "Writing executable...")
		hasher := sha256.New()
		err = fsutil.WriteAtomic(targetPath, 0755, func(w io.Writer) error {
			if _, err := io.Copy(io.MultiWriter(w, hasher), progressReader); err != nil {
				return err
			}
			return verifyChecksum(spec.Name, dlSpec.SHA256, hasher)
		})
		if err != nil {
			var ae *apperr.Error
			if errors.As(err, &ae) {
				_ = os.Remove(targetPath)
				return domainbinaries.BinaryInfo{}, ae
			}
			return domainbinaries.BinaryInfo{}, apperr.Wrap(apperr.CodeUnavailable, "the download was interrupted; try again", err)
		}

	default:
		return domainbinaries.BinaryInfo{}, apperr.New(apperr.CodeBadRequest, fmt.Sprintf("unsupported archive format %q", dlSpec.ArchiveFormat))
	}

	// Ensure executable permissions
	_ = os.Chmod(targetPath, 0755)

	report(100, domainbinaries.ProgressInstalled, map[string]string{"name": spec.Name}, fmt.Sprintf("%s installed. Probing...", spec.Name))

	return s.Probe(ctx, id)
}

// verifyChecksum compares the downloaded bytes' SHA-256 with the manifest value; an empty
// expected value means the manifest pins no checksum.
func verifyChecksum(name, expected string, h hash.Hash) error {
	if expected == "" {
		return nil
	}
	actual := hex.EncodeToString(h.Sum(nil))
	want := strings.ToLower(strings.TrimSpace(expected))
	if actual != want {
		return apperr.Integrity(fmt.Sprintf("binary %s checksum mismatch: expected %s, got %s", name, want, actual))
	}
	return nil
}
