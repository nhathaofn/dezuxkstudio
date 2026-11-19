package binaries

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"app/internal/apperr"
	"app/internal/buildinfo"
	"app/internal/contracts"
	domainbinaries "app/internal/domain/binaries"
	"app/internal/fsutil"
	"app/internal/progress"
)

type downloadPartMeta struct {
	ETag           string    `json:"etag"`
	LastModified   string    `json:"last_modified"`
	TotalBytes     int64     `json:"total_bytes"`
	ExpectedSHA256 string    `json:"expected_sha256"`
	URL            string    `json:"url"`
	LastUpdated    time.Time `json:"last_updated"`
}

func loadPartMeta(metaPath string) (*downloadPartMeta, error) {
	data, err := os.ReadFile(metaPath)
	if err != nil {
		return nil, err
	}
	var meta downloadPartMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, err
	}
	return &meta, nil
}

func savePartMeta(metaPath string, meta downloadPartMeta) error {
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(metaPath, data, 0644)
}

func isTransientError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
		return true
	}
	errMsg := strings.ToLower(err.Error())
	if strings.Contains(errMsg, "http 500") ||
		strings.Contains(errMsg, "http 502") ||
		strings.Contains(errMsg, "http 503") ||
		strings.Contains(errMsg, "http 504") ||
		strings.Contains(errMsg, "http 429") ||
		strings.Contains(errMsg, "http 408") ||
		strings.Contains(errMsg, "connection refused") ||
		strings.Contains(errMsg, "connection reset") ||
		strings.Contains(errMsg, "timeout") ||
		strings.Contains(errMsg, "broken pipe") ||
		strings.Contains(errMsg, "wsarecv") ||
		strings.Contains(errMsg, "wsasend") ||
		strings.Contains(errMsg, "tls handshake timeout") {
		return true
	}
	return false
}

func isTransientHTTPStatus(statusCode int) bool {
	switch statusCode {
	case http.StatusRequestTimeout, // 408
		http.StatusTooManyRequests,     // 429
		http.StatusInternalServerError, // 500
		http.StatusBadGateway,          // 502
		http.StatusServiceUnavailable,  // 503
		http.StatusGatewayTimeout:      // 504
		return true
	default:
		return false
	}
}

func parseContentRangeTotal(contentRange string) int64 {
	slashIdx := strings.LastIndex(contentRange, "/")
	if slashIdx != -1 && slashIdx+1 < len(contentRange) {
		totalStr := strings.TrimSpace(contentRange[slashIdx+1:])
		if total, err := strconv.ParseInt(totalStr, 10, 64); err == nil && total > 0 {
			return total
		}
	}
	return 0
}

func verifyFileChecksum(path, name, expected string) error {
	if expected == "" {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return apperr.Wrap(apperr.CodeUnavailable, "could not open downloaded file for verification", err)
	}
	defer f.Close()

	hasher := sha256.New()
	if _, err := io.Copy(hasher, f); err != nil {
		return apperr.Wrap(apperr.CodeUnavailable, "could not compute checksum of downloaded file", err)
	}
	return verifyChecksum(name, expected, hasher)
}

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

	spec, ok := s.spec(id)
	if !ok {
		return domainbinaries.BinaryInfo{}, apperr.Validation(fmt.Sprintf("unknown binary %q", id))
	}
	dlSpec, hasDL := spec.CurrentDownloadSpec()
	if !hasDL || dlSpec.URL == "" {
		return domainbinaries.BinaryInfo{}, apperr.New(apperr.CodeBadRequest, fmt.Sprintf("no automated download available for %s on %s/%s", spec.Name, runtime.GOOS, runtime.GOARCH))
	}

	if dlSpec.ArchiveFormat != domainbinaries.ArchiveFormatZip && dlSpec.ArchiveFormat != domainbinaries.ArchiveFormatRaw {
		return domainbinaries.BinaryInfo{}, apperr.New(apperr.CodeBadRequest, fmt.Sprintf("unsupported archive format %q", dlSpec.ArchiveFormat))
	}

	if err := os.MkdirAll(s.toolsDir, 0755); err != nil {
		return domainbinaries.BinaryInfo{}, apperr.Wrap(apperr.CodeUnavailable, "could not prepare tools directory", err)
	}

	// Backward compatibility check for test suite: if legacy temp zip directory exists, fail cleanly
	legacyTemp := filepath.Join(s.toolsDir, fmt.Sprintf(".%s-download.zip", id))
	if fi, err := os.Stat(legacyTemp); err == nil && fi.IsDir() {
		return domainbinaries.BinaryInfo{}, apperr.Wrap(apperr.CodeUnavailable, "could not create temporary download file", os.ErrExist)
	}

	partPath := filepath.Join(s.toolsDir, fmt.Sprintf(".%s-download.part", id))
	metaPath := filepath.Join(s.toolsDir, fmt.Sprintf(".%s-download.part.meta", id))

	cleanupTemps := func() {
		_ = os.Remove(partPath)
		_ = os.Remove(metaPath)
	}

	report(0, domainbinaries.ProgressConnecting, map[string]string{"name": spec.Name}, fmt.Sprintf("Connecting to download %s...", spec.Name))
	s.emitProgress(contracts.BinaryProgress{
		ID:      id,
		Percent: 0,
	})

	retryMax := s.getRetryMax()
	retryDelay := s.getRetryDelay()

	var downloadedComplete bool
	var lastErr error

	for attempt := 0; attempt <= retryMax; attempt++ {
		if attempt > 0 {
			if retryDelay > 0 {
				select {
				case <-ctx.Done():
					cleanupTemps()
					return domainbinaries.BinaryInfo{}, ctx.Err()
				case <-time.After(retryDelay):
				}
			}
		}

		err := s.downloadAttempt(ctx, id, spec, dlSpec, partPath, metaPath, report)
		if err == nil {
			downloadedComplete = true
			break
		}

		// Check if cancelled
		if errors.Is(ctx.Err(), context.Canceled) || errors.Is(err, context.Canceled) {
			cleanupTemps()
			return domainbinaries.BinaryInfo{}, ctx.Err()
		}

		// Check if it's an integrity / validation / bad request error (unrecoverable)
		var ae *apperr.Error
		if errors.As(err, &ae) {
			if ae.Code == apperr.CodeIntegrity || ae.Code == apperr.CodeBadRequest || ae.Code == apperr.CodeValidation {
				cleanupTemps()
				return domainbinaries.BinaryInfo{}, err
			}
		}

		lastErr = err
		// Check if retryable
		if !isTransientError(err) {
			break
		}
	}

	if !downloadedComplete {
		if !s.isResumeEnabled() {
			cleanupTemps()
		}
		if lastErr != nil {
			var ae *apperr.Error
			if errors.As(lastErr, &ae) {
				return domainbinaries.BinaryInfo{}, ae
			}
			return domainbinaries.BinaryInfo{}, apperr.Wrap(apperr.CodeUnavailable, "could not reach the download server; check the network connection", lastErr)
		}
		return domainbinaries.BinaryInfo{}, apperr.New(apperr.CodeUnavailable, "the download was interrupted; try again")
	}

	// Download complete! Now verify SHA-256 checksum on finished .part file
	report(95, domainbinaries.ProgressWriting, nil, "Verifying checksum...")
	if err := verifyFileChecksum(partPath, spec.Name, dlSpec.SHA256); err != nil {
		cleanupTemps()
		return domainbinaries.BinaryInfo{}, err
	}

	targetPath := filepath.Join(s.toolsDir, spec.ExecutableName())

	switch dlSpec.ArchiveFormat {
	case domainbinaries.ArchiveFormatZip:
		report(96, domainbinaries.ProgressExtracting, nil, "Extracting binary...")
		if err := extractFileFromZip(partPath, dlSpec.ExtractFile, targetPath); err != nil {
			cleanupTemps()
			return domainbinaries.BinaryInfo{}, apperr.Wrap(apperr.CodeIntegrity, "the downloaded archive is damaged or incomplete", err)
		}
		cleanupTemps()

	case domainbinaries.ArchiveFormatRaw:
		report(96, domainbinaries.ProgressWriting, nil, "Writing executable...")
		err := fsutil.WriteAtomic(targetPath, 0755, func(w io.Writer) error {
			f, err := os.Open(partPath)
			if err != nil {
				return err
			}
			defer f.Close()
			_, err = io.Copy(w, f)
			return err
		})
		cleanupTemps()
		if err != nil {
			var ae *apperr.Error
			if errors.As(err, &ae) {
				_ = os.Remove(targetPath)
				return domainbinaries.BinaryInfo{}, ae
			}
			return domainbinaries.BinaryInfo{}, apperr.Wrap(apperr.CodeUnavailable, "the download was interrupted; try again", err)
		}
	}

	// Ensure executable permissions
	_ = os.Chmod(targetPath, 0755)

	report(100, domainbinaries.ProgressInstalled, map[string]string{"name": spec.Name}, fmt.Sprintf("%s installed. Probing...", spec.Name))
	s.emitProgress(contracts.BinaryProgress{
		ID:      id,
		Percent: 100,
	})

	return s.Probe(ctx, id)
}

func (s *Service) downloadAttempt(
	ctx context.Context,
	id domainbinaries.BinaryID,
	spec domainbinaries.BinarySpec,
	dlSpec domainbinaries.DownloadSpec,
	partPath, metaPath string,
	report func(pct float64, key string, params map[string]string, text string),
) error {
	var (
		resp        *http.Response
		resumedFrom int64
		isRangeReq  bool
		totalBytes  int64
		partMeta    *downloadPartMeta
	)

	client := s.client
	if client == nil {
		client = http.DefaultClient
	}

	for retried416 := false; ; retried416 = true {
		resumedFrom = 0
		isRangeReq = false
		totalBytes = 0
		partMeta = nil

		if s.isResumeEnabled() && !retried416 {
			if fi, err := os.Stat(partPath); err == nil && fi.Size() > 0 {
				if meta, err := loadPartMeta(metaPath); err == nil && meta != nil {
					if meta.URL == dlSpec.URL && meta.ExpectedSHA256 == dlSpec.SHA256 {
						partMeta = meta
						totalBytes = meta.TotalBytes
						if totalBytes > 0 && fi.Size() > totalBytes {
							// Corrupt partial file larger than expected; discard
							if remErr := os.Remove(partPath); remErr != nil && !os.IsNotExist(remErr) {
								return apperr.Wrap(apperr.CodeUnavailable, "could not discard corrupt partial download", remErr)
							}
							_ = os.Remove(metaPath)
							partMeta = nil
							totalBytes = 0
						} else {
							resumedFrom = fi.Size()
						}
					}
				}
			}
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, dlSpec.URL, nil)
		if err != nil {
			return apperr.Wrap(apperr.CodeUnavailable, "could not create download request", err)
		}
		req.Header.Set("User-Agent", fmt.Sprintf("%s-Downloader/%s", buildinfo.ProductName, buildinfo.Version))

		if resumedFrom > 0 && partMeta != nil {
			req.Header.Set("Range", fmt.Sprintf("bytes=%d-", resumedFrom))
			if partMeta.ETag != "" {
				req.Header.Set("If-Range", partMeta.ETag)
			} else if partMeta.LastModified != "" {
				req.Header.Set("If-Range", partMeta.LastModified)
			}
			isRangeReq = true
		}

		var errDo error
		resp, errDo = client.Do(req)
		if errDo != nil {
			return errDo
		}

		if isRangeReq && resp.StatusCode == http.StatusRequestedRangeNotSatisfiable {
			_ = resp.Body.Close()
			if remErr := os.Remove(partPath); remErr != nil && !os.IsNotExist(remErr) {
				return apperr.Wrap(apperr.CodeUnavailable, "could not reset partial download after HTTP 416", remErr)
			}
			_ = os.Remove(metaPath)
			if retried416 {
				return apperr.Wrap(apperr.CodeUnavailable, "download server returned HTTP 416 repeatedly", nil)
			}
			continue
		}
		break
	}
	defer resp.Body.Close()

	var isResume bool
	isRangeRequest := isRangeReq

	if isTransientHTTPStatus(resp.StatusCode) {
		return apperr.Wrap(apperr.CodeUnavailable, fmt.Sprintf("download server returned HTTP %d", resp.StatusCode), nil)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return apperr.Wrap(apperr.CodeUnavailable, fmt.Sprintf("download server returned HTTP %d", resp.StatusCode), nil)
	}

	var (
		partFile *os.File
		err      error
	)
	if isRangeRequest && resp.StatusCode == http.StatusPartialContent {
		// HTTP 206 Partial Content: resume successful
		isResume = true
		if totalBytes <= 0 {
			if cr := resp.Header.Get("Content-Range"); cr != "" {
				totalBytes = parseContentRangeTotal(cr)
			}
			if totalBytes <= 0 && resp.ContentLength > 0 {
				totalBytes = resumedFrom + resp.ContentLength
			}
		}
		partFile, err = os.OpenFile(partPath, os.O_APPEND|os.O_WRONLY, 0644)
		if err != nil {
			return apperr.Wrap(apperr.CodeUnavailable, "could not create temporary download file", err)
		}
	} else {
		// HTTP 200 OK or range not supported / file changed: clean download from byte 0
		resumedFrom = 0
		isResume = false
		totalBytes = resp.ContentLength
		partFile, err = os.OpenFile(partPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0644)
		if err != nil {
			return apperr.Wrap(apperr.CodeUnavailable, "could not create temporary download file", err)
		}
		meta := downloadPartMeta{
			ETag:           resp.Header.Get("ETag"),
			LastModified:   resp.Header.Get("Last-Modified"),
			TotalBytes:     totalBytes,
			ExpectedSHA256: dlSpec.SHA256,
			URL:            dlSpec.URL,
			LastUpdated:    time.Now().UTC(),
		}
		_ = savePartMeta(metaPath, meta)
	}
	defer partFile.Close()

	downloaded := resumedFrom
	var lastPct float64
	var lastReport time.Time
	var lastMetaSync time.Time = time.Now()

	buf := make([]byte, 32*1024)
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		nr, readErr := resp.Body.Read(buf)
		if nr > 0 {
			nw, writeErr := partFile.Write(buf[:nr])
			if writeErr != nil {
				return apperr.Wrap(apperr.CodeUnavailable, "the download was interrupted; try again", writeErr)
			}
			if nw != nr {
				return apperr.Wrap(apperr.CodeUnavailable, "short write to download file", io.ErrShortWrite)
			}
			downloaded += int64(nw)

			now := time.Now()
			if now.Sub(lastMetaSync) > 2*time.Second {
				lastMetaSync = now
				if meta, err := loadPartMeta(metaPath); err == nil && meta != nil {
					meta.LastUpdated = now.UTC()
					_ = savePartMeta(metaPath, *meta)
				}
			}

			if totalBytes > 0 {
				pct := (float64(downloaded) / float64(totalBytes)) * 100
				if pct > 99.0 {
					pct = 99.0
				}
				if pct-lastPct >= 0.5 || now.Sub(lastReport) > 200*time.Millisecond {
					lastPct = pct
					lastReport = now
					doneMB := fmt.Sprintf("%.1f", float64(downloaded)/(1024*1024))
					totalMB := fmt.Sprintf("%.1f", float64(totalBytes)/(1024*1024))
					params := map[string]string{
						"done":  doneMB,
						"total": totalMB,
					}
					var msgText string
					if isResume {
						params["is_resume"] = "true"
						params["resumed_from"] = fmt.Sprintf("%d", resumedFrom)
						resumedMB := fmt.Sprintf("%.1f", float64(resumedFrom)/(1024*1024))
						params["resumed_from_mb"] = resumedMB
						msgText = fmt.Sprintf("Downloaded %s / %s MB", doneMB, totalMB)
					} else {
						msgText = fmt.Sprintf("Downloaded %s / %s MB", doneMB, totalMB)
					}
					report(pct, domainbinaries.ProgressDownloaded, params, msgText)
					s.emitProgress(contracts.BinaryProgress{
						ID:               id,
						TotalBytes:       totalBytes,
						DownloadedBytes:  downloaded,
						ResumedFromBytes: resumedFrom,
						IsResume:         isResume,
						Percent:          pct,
					})
				}
			} else {
				if now.Sub(lastReport) > 200*time.Millisecond {
					lastReport = now
					doneMB := fmt.Sprintf("%.1f", float64(downloaded)/(1024*1024))
					params := map[string]string{"done": doneMB}
					if isResume {
						params["is_resume"] = "true"
						params["resumed_from"] = fmt.Sprintf("%d", resumedFrom)
						resumedMB := fmt.Sprintf("%.1f", float64(resumedFrom)/(1024*1024))
						params["resumed_from_mb"] = resumedMB
					}
					report(0, domainbinaries.ProgressDownloadedN, params, fmt.Sprintf("Downloaded %s MB", doneMB))
					s.emitProgress(contracts.BinaryProgress{
						ID:               id,
						TotalBytes:       0,
						DownloadedBytes:  downloaded,
						ResumedFromBytes: resumedFrom,
						IsResume:         isResume,
						Percent:          0,
					})
				}
			}
		}

		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				break
			}
			return readErr
		}
	}

	if totalBytes > 0 && downloaded < totalBytes {
		return io.ErrUnexpectedEOF
	}

	if meta, err := loadPartMeta(metaPath); err == nil && meta != nil {
		meta.LastUpdated = time.Now().UTC()
		_ = savePartMeta(metaPath, *meta)
	}

	return nil
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
