package agentcli

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"app/internal/apperr"
	domainagent "app/internal/domain/agent"
	domainbinaries "app/internal/domain/binaries"
	"app/internal/progress"
)

// fetch downloads rel, verifies its checksum and unpacks it into versions/<version>. Nothing outside the
// app's folder is touched, and an incomplete or tampered download never becomes a version folder.
func (m *Manager) fetch(ctx context.Context, id domainagent.ProviderID, name string, spec domainagent.InstallSpec, rel Release) error {
	report := func(pct float64, key string, params map[string]string) {
		progress.Report(ctx, progress.Update{Percent: pct, Key: key, Params: params})
	}
	if err := os.MkdirAll(m.versionsDir(id), 0o755); err != nil {
		return fmt.Errorf("agentcli: create folder: %w", err)
	}
	staging, err := os.MkdirTemp(m.versionsDir(id), ".staging-")
	if err != nil {
		return fmt.Errorf("agentcli: create staging: %w", err)
	}
	defer os.RemoveAll(staging)

	report(0, domainbinaries.ProgressConnecting, map[string]string{"name": name})
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rel.URL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", m.ua)
	resp, err := m.client.Do(req)
	if err != nil {
		return apperr.Wrap(apperr.CodeUnavailable, "could not reach the download server; check the network connection", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return apperr.Wrap(apperr.CodeUnavailable, fmt.Sprintf("download server returned HTTP %d", resp.StatusCode), nil)
	}

	var h hash.Hash
	switch rel.Algo {
	case "sha256":
		h = sha256.New()
	case "sha512":
		h = sha512.New()
	default:
		return fmt.Errorf("agentcli: unsupported checksum %q", rel.Algo)
	}
	payload := filepath.Join(staging, ".download")
	f, err := os.Create(payload)
	if err != nil {
		return fmt.Errorf("agentcli: create download: %w", err)
	}
	total := resp.ContentLength
	var done int64
	var last time.Time
	src := readerFunc(func(p []byte) (int, error) {
		n, err := resp.Body.Read(p)
		done += int64(n)
		if now := time.Now(); now.Sub(last) > 200*time.Millisecond || err == io.EOF {
			last = now
			mb := fmt.Sprintf("%.1f", float64(done)/(1<<20))
			if total > 0 {
				report(float64(done)/float64(total)*90, domainbinaries.ProgressDownloaded,
					map[string]string{"done": mb, "total": fmt.Sprintf("%.1f", float64(total)/(1<<20))})
			} else {
				report(0, domainbinaries.ProgressDownloadedN, map[string]string{"done": mb})
			}
		}
		return n, err
	})
	_, copyErr := io.Copy(io.MultiWriter(f, h), src)
	closeErr := f.Close()
	if copyErr != nil || closeErr != nil {
		return apperr.Wrap(apperr.CodeUnavailable, "the download was interrupted; try again", firstErr(copyErr, closeErr))
	}
	if got := hex.EncodeToString(h.Sum(nil)); !strings.EqualFold(got, strings.TrimSpace(rel.Checksum)) {
		return apperr.Integrity(fmt.Sprintf("%s checksum mismatch: expected %s, got %s", name, rel.Checksum, got))
	}

	report(95, domainbinaries.ProgressExtracting, nil)
	unpacked := filepath.Join(staging, "unpacked")
	exe := filepath.Join(unpacked, filepath.FromSlash(spec.Executable))
	if rel.Archive {
		if err := untarGz(payload, unpacked); err != nil {
			return apperr.Wrap(apperr.CodeIntegrity, "the downloaded archive is damaged or incomplete", err)
		}
	} else {
		if err := os.MkdirAll(filepath.Dir(exe), 0o755); err != nil {
			return err
		}
		if err := os.Rename(payload, exe); err != nil {
			return err
		}
	}
	if st, err := os.Stat(exe); err != nil || st.IsDir() {
		return apperr.Wrap(apperr.CodeIntegrity, "the downloaded package does not contain the program", err)
	}
	_ = os.Chmod(exe, 0o755)

	final := filepath.Join(m.versionsDir(id), rel.Version)
	if err := os.RemoveAll(final); err != nil { // a half-written earlier attempt
		return fmt.Errorf("agentcli: clear old attempt: %w", err)
	}
	if err := os.Rename(unpacked, final); err != nil {
		return fmt.Errorf("agentcli: install %s: %w", rel.Version, err)
	}
	report(100, domainbinaries.ProgressInstalled, map[string]string{"name": name})
	return nil
}

type readerFunc func(p []byte) (int, error)

func (r readerFunc) Read(p []byte) (int, error) { return r(p) }

func firstErr(errs ...error) error {
	for _, e := range errs {
		if e != nil {
			return e
		}
	}
	return nil
}

// untarGz unpacks a .tar.gz into dir, refusing any entry that would land outside it.
func untarGz(archive, dir string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	root, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		target := filepath.Join(root, filepath.FromSlash(hdr.Name))
		if rel, err := filepath.Rel(root, target); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return fmt.Errorf("archive entry %q escapes the folder", hdr.Name)
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
			if err != nil {
				return err
			}
			_, cpErr := io.Copy(out, tr)
			if cerr := out.Close(); cpErr == nil {
				cpErr = cerr
			}
			if cpErr != nil {
				return cpErr
			}
		}
	}
}
