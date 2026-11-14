package handlers

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unicode"

	"app/internal/apperr"
	"app/internal/config"
)

const (
	// MediaStagingSubdir holds HTTP-uploaded inputs and their sibling outputs under MediaDir.
	MediaStagingSubdir = "staging"

	maxMultipartMemory = 8 << 20
	maxFilenameRunes   = 80
)

var allowedImageContentTypes = map[string]string{
	"image/png":  ".png",
	"image/jpeg": ".jpg",
	"image/webp": ".webp",
}

var downloadMimeByExt = map[string]string{
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".webp": "image/webp",
	".mp4":  "video/mp4",
	".m4v":  "video/x-m4v",
	".mov":  "video/quicktime",
	".webm": "video/webm",
	".mkv":  "video/x-matroska",
	".mp3":  "audio/mpeg",
	".wav":  "audio/wav",
	".ogg":  "audio/ogg",
	".flac": "audio/flac",
	".aac":  "audio/aac",
	".m4a":  "audio/mp4",
	".opus": "audio/opus",
}

type mediaUploadResponse struct {
	Path string `json:"path"`
	Name string `json:"name"`
	Size int64  `json:"size"`
}

// MediaUpload handles POST /api/media/upload for remote/browser clients that cannot
// address server files by local path. Files are stored inside <mediaDir>/staging/<id>/<name>
// so AccessPolicy and watermark nodes accept them without opening arbitrary filesystem access.
func MediaUpload(mediaDir string, maxBytes int64) http.Handler {
	if maxBytes <= 0 {
		maxBytes = config.Default(config.ModeDesktop).Server.MaxMediaUploadBytes
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, apperr.New(apperr.CodeBadRequest, "method not allowed"))
			return
		}
		if strings.TrimSpace(mediaDir) == "" {
			writeError(w, http.StatusInternalServerError, apperr.Unavailable("media staging is unavailable"))
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
		if err := r.ParseMultipartForm(maxMultipartMemory); err != nil {
			var maxErr *http.MaxBytesError
			if errors.As(err, &maxErr) {
				writeError(w, http.StatusRequestEntityTooLarge, apperr.Validation("uploaded file exceeds maximum size"))
				return
			}
			writeError(w, http.StatusBadRequest, apperr.New(apperr.CodeBadRequest, "invalid multipart upload"))
			return
		}
		if r.MultipartForm != nil {
			defer func() { _ = r.MultipartForm.RemoveAll() }()
		}

		src, hdr, err := r.FormFile("file")
		if err != nil {
			writeError(w, http.StatusBadRequest, apperr.Validation("missing 'file' form field"))
			return
		}
		defer func() { _ = src.Close() }()

		var sniff [512]byte
		n, err := io.ReadFull(src, sniff[:])
		if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
			var maxErr *http.MaxBytesError
			if errors.As(err, &maxErr) {
				writeError(w, http.StatusRequestEntityTooLarge, apperr.Validation("uploaded file exceeds maximum size"))
				return
			}
			writeError(w, http.StatusBadRequest, apperr.New(apperr.CodeBadRequest, "failed to read uploaded file"))
			return
		}
		if n == 0 {
			writeError(w, http.StatusBadRequest, apperr.Validation("uploaded file is empty"))
			return
		}

		mimeType := http.DetectContentType(sniff[:n])
		canonicalExt, ok := allowedImageContentTypes[mimeType]
		if !ok {
			writeError(w, http.StatusUnsupportedMediaType, apperr.Validation("only PNG, JPEG, and WebP images are supported"))
			return
		}

		safeName := sanitizeMediaFilename(hdr.Filename, mimeType, canonicalExt)
		destDir, destPath, err := allocateStagingPath(mediaDir, safeName)
		if err != nil {
			writeError(w, http.StatusInternalServerError, apperr.Wrap(apperr.CodeInternal, "failed to prepare staging directory", err))
			return
		}

		f, err := os.OpenFile(destPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			_ = os.RemoveAll(destDir)
			writeError(w, http.StatusInternalServerError, apperr.Wrap(apperr.CodeInternal, "failed to store uploaded file", err))
			return
		}

		written, copyErr := io.Copy(f, io.MultiReader(bytes.NewReader(sniff[:n]), src))
		closeErr := f.Close()
		if copyErr != nil || closeErr != nil {
			_ = os.RemoveAll(destDir)
			var maxErr *http.MaxBytesError
			if errors.As(copyErr, &maxErr) {
				writeError(w, http.StatusRequestEntityTooLarge, apperr.Validation("uploaded file exceeds maximum size"))
				return
			}
			writeError(w, http.StatusInternalServerError, apperr.New(apperr.CodeInternal, "failed to save uploaded file"))
			return
		}

		writeJSON(w, http.StatusOK, mediaUploadResponse{
			Path: destPath,
			Name: safeName,
			Size: written,
		})
	})
}

// MediaDownload handles GET /api/media/download?path=... for regular files strictly
// inside mediaDir (symlink-resolved). Arbitrary host paths outside mediaDir are refused.
func MediaDownload(mediaDir string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, apperr.New(apperr.CodeBadRequest, "method not allowed"))
			return
		}
		rawPath := strings.TrimSpace(r.URL.Query().Get("path"))
		if rawPath == "" || strings.ContainsRune(rawPath, 0) {
			writeError(w, http.StatusBadRequest, apperr.Validation("missing or invalid 'path' query parameter"))
			return
		}

		realRoot, err := resolveExistingDir(mediaDir)
		if err != nil {
			writeError(w, http.StatusNotFound, apperr.NotFound("media folder not found"))
			return
		}

		candidate := rawPath
		if !filepath.IsAbs(candidate) {
			candidate = filepath.Join(realRoot, filepath.FromSlash(candidate))
		}
		candidate = filepath.Clean(candidate)

		realTarget, err := filepath.EvalSymlinks(candidate)
		if err != nil {
			if os.IsNotExist(err) {
				writeError(w, http.StatusNotFound, apperr.NotFound("requested media file not found"))
				return
			}
			writeError(w, http.StatusBadRequest, apperr.New(apperr.CodeBadRequest, "invalid media path"))
			return
		}

		if !isStrictSubPath(realRoot, realTarget) {
			writeError(w, http.StatusForbidden, apperr.New(apperr.CodeUnauthorized, "access denied outside media folder"))
			return
		}

		info, err := os.Stat(realTarget)
		if err != nil {
			if os.IsNotExist(err) {
				writeError(w, http.StatusNotFound, apperr.NotFound("requested media file not found"))
				return
			}
			writeError(w, http.StatusInternalServerError, apperr.Wrap(apperr.CodeInternal, "failed to read media metadata", err))
			return
		}
		if !info.Mode().IsRegular() {
			writeError(w, http.StatusForbidden, apperr.New(apperr.CodeBadRequest, "requested path is not a file"))
			return
		}

		f, err := os.Open(realTarget)
		if err != nil {
			writeError(w, http.StatusInternalServerError, apperr.Wrap(apperr.CodeInternal, "failed to open media file", err))
			return
		}
		defer func() { _ = f.Close() }()

		ext := strings.ToLower(filepath.Ext(realTarget))
		contentType := downloadMimeByExt[ext]
		if contentType == "" {
			contentType = "application/octet-stream"
		}

		h := w.Header()
		h.Set("Content-Type", contentType)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Cache-Control", "no-store")
		h.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{
			"filename": filepath.Base(realTarget),
		}))

		http.ServeContent(w, r, filepath.Base(realTarget), info.ModTime(), f)
	})
}

// RunStagingCleanup deletes expired directories under <mediaDir>/staging periodically until ctx is cancelled.
// Non-positive ttl or interval values fall back to config.Default(config.ModeDesktop).Server.
func RunStagingCleanup(ctx context.Context, mediaDir string, ttl, interval time.Duration, log *slog.Logger) {
	if ttl <= 0 || interval <= 0 {
		def := config.Default(config.ModeDesktop).Server
		if ttl <= 0 {
			ttl = def.MediaStagingTTL
		}
		if interval <= 0 {
			interval = def.MediaStagingCleanupInterval
		}
	}
	stagingDir := filepath.Join(mediaDir, MediaStagingSubdir)
	clean := func() {
		if err := CleanupStagingDir(stagingDir, ttl, time.Now()); err != nil && log != nil {
			log.Warn("media: staging cleanup failed", "error", err)
		}
	}
	clean()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			clean()
		}
	}
}

// CleanupStagingDir removes staging entries whose newest file mtime is older than maxAge relative to now.
func CleanupStagingDir(stagingDir string, maxAge time.Duration, now time.Time) error {
	if maxAge <= 0 || strings.TrimSpace(stagingDir) == "" {
		return nil
	}
	entries, err := os.ReadDir(stagingDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	cutoff := now.Add(-maxAge)
	for _, entry := range entries {
		p := filepath.Join(stagingDir, entry.Name())
		latest, err := latestModTime(p)
		if err != nil {
			continue
		}
		if latest.Before(cutoff) {
			_ = os.RemoveAll(p)
		}
	}
	return nil
}

func latestModTime(root string) (time.Time, error) {
	info, err := os.Stat(root)
	if err != nil {
		return time.Time{}, err
	}
	latest := info.ModTime()
	if !info.IsDir() {
		return latest, nil
	}
	_ = filepath.WalkDir(root, func(_ string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return nil
		}
		if fi.ModTime().After(latest) {
			latest = fi.ModTime()
		}
		return nil
	})
	return latest, nil
}

func allocateStagingPath(mediaDir, safeName string) (string, string, error) {
	if err := os.MkdirAll(mediaDir, 0o700); err != nil {
		return "", "", err
	}
	realMedia, err := resolveExistingDir(mediaDir)
	if err != nil {
		return "", "", err
	}
	stagingRoot := filepath.Join(realMedia, MediaStagingSubdir)
	if err := os.MkdirAll(stagingRoot, 0o700); err != nil {
		return "", "", err
	}

	var rnd [16]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		return "", "", err
	}
	id := hex.EncodeToString(rnd[:])
	destDir := filepath.Join(stagingRoot, id)
	if err := os.MkdirAll(destDir, 0o700); err != nil {
		return "", "", err
	}
	destPath := filepath.Clean(filepath.Join(destDir, safeName))
	if !isStrictSubPath(stagingRoot, destPath) {
		_ = os.RemoveAll(destDir)
		return "", "", errors.New("staging path escapes staging directory")
	}
	return destDir, destPath, nil
}

func sanitizeMediaFilename(raw, mimeType, fallbackExt string) string {
	normalized := strings.ReplaceAll(strings.TrimSpace(raw), "\\", "/")
	if idx := strings.LastIndexByte(normalized, '/'); idx >= 0 {
		normalized = normalized[idx+1:]
	}
	ext := strings.ToLower(filepath.Ext(normalized))
	stem := strings.TrimSuffix(normalized, filepath.Ext(normalized))

	var b strings.Builder
	count := 0
	for _, r := range stem {
		if count >= maxFilenameRunes {
			break
		}
		if unicode.IsControl(r) || strings.ContainsRune(`<>:"/\|?*`, r) {
			b.WriteByte('_')
			count++
			continue
		}
		b.WriteRune(r)
		count++
	}
	cleanStem := strings.Trim(b.String(), ". ")
	if cleanStem == "" || cleanStem == "." || cleanStem == ".." {
		cleanStem = "image"
	}

	// Keep .jpeg if user supplied it for image/jpeg; otherwise force the sniffed extension
	// so mismatched or missing extensions cannot bypass extension checks downstream.
	outExt := fallbackExt
	if mimeType == "image/jpeg" && ext == ".jpeg" {
		outExt = ".jpeg"
	}
	return cleanStem + outExt
}

func resolveExistingDir(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(filepath.Clean(abs))
}

func isStrictSubPath(root, target string) bool {
	if root == "" || target == "" {
		return false
	}
	cleanRoot := filepath.Clean(root)
	cleanTarget := filepath.Clean(target)
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		cleanRoot = strings.ToLower(cleanRoot)
		cleanTarget = strings.ToLower(cleanTarget)
	}
	if cleanRoot == cleanTarget {
		return false
	}
	rel, err := filepath.Rel(cleanRoot, cleanTarget)
	if err != nil {
		return false
	}
	return rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
