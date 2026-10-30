// Package binaries provides use cases for resolving, probing, downloading, and managing external CLI tools.
package binaries

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"app/internal/apperr"
	domainbinaries "app/internal/domain/binaries"
	"app/internal/fsutil"
	"app/internal/runner"
)

// Service coordinates binary inspection, path overrides, and automated downloads.
type Service struct {
	repo     domainbinaries.Repository
	toolsDir string
	logger   *slog.Logger
	client   *http.Client
	mu       sync.Mutex
}

// New creates a new binaries Service.
func New(repo domainbinaries.Repository, toolsDir string, logger *slog.Logger) *Service {
	return &Service{
		repo:     repo,
		toolsDir: toolsDir,
		logger:   logger,
		client:   &http.Client{Timeout: 10 * time.Minute},
	}
}

// List inspects all registered binaries and returns their status.
func (s *Service) List(ctx context.Context) ([]domainbinaries.BinaryInfo, error) {
	allIDs := domainbinaries.AllIDs()
	infos := make([]domainbinaries.BinaryInfo, 0, len(allIDs))
	for _, id := range allIDs {
		info, err := s.Probe(ctx, id)
		if err != nil {
			s.logger.WarnContext(ctx, "failed to probe binary", "id", id, "err", err)
			spec := domainbinaries.Registry[id]
			_, canDownload := spec.CurrentDownloadSpec()
			infos = append(infos, domainbinaries.BinaryInfo{
				ID:          id,
				Name:        spec.Name,
				Description: spec.Description,
				Executable:  spec.ExecutableName(),
				Status:      domainbinaries.StatusInvalid,
				CanDownload: canDownload,
			})
			continue
		}
		infos = append(infos, info)
	}
	return infos, nil
}

// Get returns the status of a single binary.
func (s *Service) Get(ctx context.Context, id domainbinaries.BinaryID) (domainbinaries.BinaryInfo, error) {
	return s.Probe(ctx, id)
}

// SetCustomPath sets or clears the custom path for a binary and returns the updated status.
func (s *Service) SetCustomPath(ctx context.Context, id domainbinaries.BinaryID, customPath string) (domainbinaries.BinaryInfo, error) {
	if err := domainbinaries.ValidateID(id); err != nil {
		return domainbinaries.BinaryInfo{}, err
	}
	customPath = strings.TrimSpace(customPath)
	if err := s.repo.SetCustomPath(ctx, id, customPath); err != nil {
		return domainbinaries.BinaryInfo{}, err
	}
	return s.Probe(ctx, id)
}

// Resolve identifies the executable location and source for a binary.
func (s *Service) Resolve(ctx context.Context, id domainbinaries.BinaryID) (resolvedPath string, source domainbinaries.Source, customPath string, err error) {
	spec, ok := domainbinaries.Registry[id]
	if !ok {
		return "", domainbinaries.SourceNone, "", apperr.Validation(fmt.Sprintf("unknown binary %q", id))
	}

	// 1. Custom path configured by user
	customPath, err = s.repo.GetCustomPath(ctx, id)
	if err != nil {
		return "", domainbinaries.SourceNone, "", err
	}
	if customPath != "" {
		if fileExists(customPath) {
			return customPath, domainbinaries.SourceCustom, customPath, nil
		}
		// If specified custom path does not exist, note it but continue fallbacks
	}

	// 2. Application tools directory (%LOCALAPPDATA%/.../data/tools)
	if s.toolsDir != "" {
		toolsPath := filepath.Join(s.toolsDir, spec.ExecutableName())
		if fileExists(toolsPath) {
			return toolsPath, domainbinaries.SourceTools, customPath, nil
		}
	}

	// 3. Portable tools directory next to the executable (./tools)
	portableTools := filepath.Join(".", "tools", spec.ExecutableName())
	if fileExists(portableTools) {
		absPortable, err := filepath.Abs(portableTools)
		if err == nil {
			return absPortable, domainbinaries.SourceTools, customPath, nil
		}
		return portableTools, domainbinaries.SourceTools, customPath, nil
	}

	// 4. System PATH
	if sysPath, err := exec.LookPath(spec.ExecutableBase); err == nil {
		return sysPath, domainbinaries.SourceSystem, customPath, nil
	}
	if sysPath, err := exec.LookPath(spec.ExecutableName()); err == nil {
		return sysPath, domainbinaries.SourceSystem, customPath, nil
	}

	return "", domainbinaries.SourceNone, customPath, nil
}

// Probe executes the binary with probe arguments to detect version and verify execution.
func (s *Service) Probe(ctx context.Context, id domainbinaries.BinaryID) (domainbinaries.BinaryInfo, error) {
	spec, ok := domainbinaries.Registry[id]
	if !ok {
		return domainbinaries.BinaryInfo{}, apperr.Validation(fmt.Sprintf("unknown binary %q", id))
	}

	_, canDownload := spec.CurrentDownloadSpec()
	resolvedPath, source, customPath, err := s.Resolve(ctx, id)
	if err != nil {
		return domainbinaries.BinaryInfo{}, err
	}

	info := domainbinaries.BinaryInfo{
		ID:           id,
		Name:         spec.Name,
		Description:  spec.Description,
		Executable:   spec.ExecutableName(),
		ResolvedPath: resolvedPath,
		CustomPath:   customPath,
		Source:       source,
		CanDownload:  canDownload,
	}

	if resolvedPath == "" {
		info.Status = domainbinaries.StatusMissing
		return info, nil
	}

	// Run probe command with 5s timeout
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	res, err := runner.RunWithOptions(probeCtx, resolvedPath, spec.ProbeArgs, runner.RunOptions{})
	if err != nil {
		s.logger.WarnContext(ctx, "binary probe execution failed", "id", id, "path", resolvedPath, "err", err)
		info.Status = domainbinaries.StatusInvalid
		return info, nil
	}

	output := res.Stdout + "\n" + res.Stderr
	version := spec.ExtractVersion(output)
	if version != "" {
		info.Version = version
		info.Status = domainbinaries.StatusAvailable
	} else if res.ExitCode == 0 {
		info.Status = domainbinaries.StatusAvailable
	} else {
		info.Status = domainbinaries.StatusInvalid
	}

	return info, nil
}

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
	req.Header.Set("User-Agent", "DezuxkStudio-Downloader/1.0")

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
		if onProgress != nil && totalSize > 0 {
			pct := (float64(downloaded) / float64(totalSize)) * 100
			now := time.Now()
			if pct-lastPct >= 0.5 || now.Sub(lastReport) > 200*time.Millisecond || err == io.EOF {
				lastPct = pct
				lastReport = now
				msg := fmt.Sprintf("Downloaded %.1f / %.1f MB", float64(downloaded)/(1024*1024), float64(totalSize)/(1024*1024))
				onProgress(pct, msg)
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

		// Download to temp zip
		zipFile, err := os.Create(tempZip)
		if err != nil {
			return domainbinaries.BinaryInfo{}, fmt.Errorf("binaries: create temp zip: %w", err)
		}
		if _, err := io.Copy(zipFile, progressReader); err != nil {
			zipFile.Close()
			return domainbinaries.BinaryInfo{}, fmt.Errorf("binaries: save temp zip: %w", err)
		}
		zipFile.Close()

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
		err = fsutil.WriteAtomic(targetPath, 0755, func(w io.Writer) error {
			_, err := io.Copy(w, progressReader)
			return err
		})
		if err != nil {
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

// extractFileFromZip extracts a specific entry from a zip archive into targetPath atomically.
func extractFileFromZip(zipPath, targetEntry, targetPath string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer r.Close()

	var matchedFile *zip.File
	for _, f := range r.File {
		cleanName := strings.ReplaceAll(f.Name, "\\", "/")
		targetClean := strings.ReplaceAll(targetEntry, "\\", "/")
		if cleanName == targetClean || strings.HasSuffix(cleanName, "/"+filepath.Base(targetClean)) || filepath.Base(cleanName) == filepath.Base(targetClean) {
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

func fileExists(p string) bool {
	info, err := os.Stat(p)
	if err != nil {
		return false
	}
	return !info.IsDir()
}

type readerFunc func(p []byte) (int, error)

func (rf readerFunc) Read(p []byte) (int, error) { return rf(p) }
