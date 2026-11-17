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
	"app/internal/security"
)

// AssetResolver resolves an asset_id to its backing file path and registered root directory.
type AssetResolver interface {
	ResolveAsset(ctx context.Context, assetID string) (filePath string, registeredRoot string, err error)
}

// AssetResolverFunc is an adapter to allow the use of ordinary functions as AssetResolvers.
type AssetResolverFunc func(ctx context.Context, assetID string) (string, string, error)

// ResolveAsset calls f(ctx, assetID).
func (f AssetResolverFunc) ResolveAsset(ctx context.Context, assetID string) (string, string, error) {
	return f(ctx, assetID)
}

type mediaDownloadConfig struct {
	mediaDirFunc   func() string
	ticketSigner   *security.TicketSigner
	assetResolver  AssetResolver
	authToken      string
	allowAnonymous bool
	enforceAuth    bool
}

// MediaDownloadOption configures MediaDownload.
type MediaDownloadOption func(*mediaDownloadConfig)

// WithMediaDirFunc sets the dynamic getter function for the active media directory.
func WithMediaDirFunc(fn func() string) MediaDownloadOption {
	return func(c *mediaDownloadConfig) {
		c.mediaDirFunc = fn
	}
}

// WithTicketSigner sets the TicketSigner used to verify HMAC signed media tickets.
func WithTicketSigner(signer *security.TicketSigner) MediaDownloadOption {
	return func(c *mediaDownloadConfig) {
		c.ticketSigner = signer
	}
}

// WithAssetResolver sets the AssetResolver used to resolve asset_id to file paths.
func WithAssetResolver(resolver AssetResolver) MediaDownloadOption {
	return func(c *mediaDownloadConfig) {
		c.assetResolver = resolver
	}
}

// WithAuth configures master token or anonymous authentication for MediaDownload.
func WithAuth(token string, allowAnonymous bool) MediaDownloadOption {
	return func(c *mediaDownloadConfig) {
		c.authToken = token
		c.allowAnonymous = allowAnonymous
		c.enforceAuth = true
	}
}

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

// MediaDownload handles GET /api/media/download supporting asset_id with signed HMAC tickets
// or valid Bearer tokens, as well as legacy path resolution inside mediaDir.
// It streams files using http.ServeContent to support HTTP 206 Range requests and video seeking.
func MediaDownload(mediaDir string, opts ...MediaDownloadOption) http.Handler {
	cfg := mediaDownloadConfig{}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			writeError(w, http.StatusMethodNotAllowed, apperr.New(apperr.CodeBadRequest, "method not allowed"))
			return
		}

		// Security: Reject ANY ?token= query parameter carrying master tokens.
		// Master tokens must NEVER be passed via URL query strings.
		if r.URL.Query().Has("token") || r.URL.Query().Get("token") != "" {
			writeError(w, http.StatusBadRequest, apperr.Validation("token query parameter is forbidden; master tokens must not be passed via URL query strings"))
			return
		}

		assetID := strings.TrimSpace(r.URL.Query().Get("asset_id"))
		rawPath := strings.TrimSpace(r.URL.Query().Get("path"))

		if assetID == "" && rawPath == "" {
			writeError(w, http.StatusBadRequest, apperr.Validation("missing or invalid 'asset_id' or 'path' query parameter"))
			return
		}
		if (assetID != "" && strings.ContainsRune(assetID, 0)) || (rawPath != "" && strings.ContainsRune(rawPath, 0)) {
			writeError(w, http.StatusBadRequest, apperr.Validation("invalid character in identifier"))
			return
		}

		ticketStr := strings.TrimSpace(r.URL.Query().Get("ticket"))
		purpose := security.PurposeDownload

		if ticketStr != "" {
			if assetID == "" {
				writeError(w, http.StatusBadRequest, apperr.Validation("ticket authentication requires 'asset_id' parameter"))
				return
			}
			if cfg.ticketSigner == nil {
				writeError(w, http.StatusUnauthorized, apperr.New(apperr.CodeUnauthorized, "ticket signer not configured"))
				return
			}

			purposeParam := strings.TrimSpace(r.URL.Query().Get("purpose"))
			var claims *security.TicketClaims
			var err error

			if purposeParam != "" {
				if purposeParam != security.PurposePreview && purposeParam != security.PurposeDownload {
					writeError(w, http.StatusBadRequest, apperr.Validation("invalid purpose; must be 'preview' or 'download'"))
					return
				}
				claims, err = cfg.ticketSigner.VerifyTicket(ticketStr, assetID, purposeParam)
			} else {
				claims, err = cfg.ticketSigner.VerifyTicket(ticketStr, assetID, security.PurposePreview)
				if errors.Is(err, security.ErrPurposeMismatch) {
					claims, err = cfg.ticketSigner.VerifyTicket(ticketStr, assetID, security.PurposeDownload)
				}
			}

			if err != nil {
				if errors.Is(err, security.ErrExpiredTicket) {
					writeError(w, http.StatusUnauthorized, apperr.New(apperr.CodeUnauthorized, "ticket expired"))
					return
				}
				if errors.Is(err, security.ErrInvalidSignature) {
					writeError(w, http.StatusForbidden, apperr.New(apperr.CodeUnauthorized, "invalid ticket signature"))
					return
				}
				writeError(w, http.StatusForbidden, apperr.New(apperr.CodeUnauthorized, err.Error()))
				return
			}
			purpose = claims.Purpose
		} else {
			if cfg.enforceAuth {
				if cfg.authToken == "" && cfg.allowAnonymous {
					// Anonymous access allowed
				} else {
					bearer := security.BearerToken(r)
					if !security.TokenEqual(cfg.authToken, bearer) {
						writeError(w, http.StatusUnauthorized, apperr.New(apperr.CodeUnauthorized, "unauthorized"))
						return
					}
				}
			} else if cfg.authToken != "" {
				bearer := security.BearerToken(r)
				if !security.TokenEqual(cfg.authToken, bearer) {
					writeError(w, http.StatusUnauthorized, apperr.New(apperr.CodeUnauthorized, "unauthorized"))
					return
				}
			}

			if purposeParam := strings.TrimSpace(r.URL.Query().Get("purpose")); purposeParam == security.PurposePreview {
				purpose = security.PurposePreview
			}
		}

		currentMediaDir := mediaDir
		if cfg.mediaDirFunc != nil {
			if dyn := strings.TrimSpace(cfg.mediaDirFunc()); dyn != "" {
				currentMediaDir = dyn
			}
		}

		var targetPath string
		var targetRoot string

		if assetID != "" {
			if cfg.assetResolver != nil {
				p, rRoot, err := cfg.assetResolver.ResolveAsset(r.Context(), assetID)
				if err != nil {
					writeError(w, http.StatusNotFound, apperr.NotFound("requested media asset not found"))
					return
				}
				targetPath = p
				targetRoot = rRoot
			} else {
				targetPath = filepath.Join(currentMediaDir, filepath.FromSlash(assetID))
				targetRoot = currentMediaDir
			}
			if targetRoot == "" {
				targetRoot = currentMediaDir
			}
		} else {
			targetPath = rawPath
			targetRoot = currentMediaDir
		}

		realRoot, _ := resolveExistingDir(targetRoot)
		if realRoot == "" && !filepath.IsAbs(targetPath) {
			writeError(w, http.StatusNotFound, apperr.NotFound("media folder not found"))
			return
		}

		candidate := targetPath
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

		if realRoot == "" || !isStrictSubPath(realRoot, realTarget) {
			authorized := false
			if auth, ok := cfg.assetResolver.(interface{ IsPathAuthorized(string) bool }); ok {
				authorized = auth.IsPathAuthorized(realTarget)
			}
			if !authorized {
				writeError(w, http.StatusForbidden, apperr.New(apperr.CodeUnauthorized, "access denied outside media folder"))
				return
			}
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

		dispositionType := "attachment"
		if purpose == security.PurposePreview {
			dispositionType = "inline"
		}

		h := w.Header()
		h.Set("Content-Type", contentType)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Cache-Control", "no-store")
		h.Set("Accept-Ranges", "bytes")
		h.Set("Content-Disposition", mime.FormatMediaType(dispositionType, map[string]string{
			"filename": filepath.Base(realTarget),
		}))

		http.ServeContent(w, r, filepath.Base(realTarget), info.ModTime(), f)
	})
}

// RunStagingCleanup deletes expired directories under stagingDir periodically until ctx is cancelled.
// Non-positive ttl or interval values fall back to config.Default(config.ModeDesktop).Server.
func RunStagingCleanup(ctx context.Context, dir string, ttl, interval time.Duration, log *slog.Logger) {
	RunStagingCleanupWithGuard(ctx, dir, ttl, interval, log, nil)
}

// RunStagingCleanupWithGuard deletes expired unguarded entries under stagingDir periodically until ctx is cancelled.
// Any entry for which shouldSkip returns true is preserved (e.g. dedicated mediagen subdir or actively leased paths).
func RunStagingCleanupWithGuard(ctx context.Context, dir string, ttl, interval time.Duration, log *slog.Logger, shouldSkip func(path string) bool) {
	if ttl <= 0 || interval <= 0 {
		def := config.Default(config.ModeDesktop).Server
		if ttl <= 0 {
			ttl = def.MediaStagingTTL
		}
		if interval <= 0 {
			interval = def.MediaStagingCleanupInterval
		}
	}
	stagingDir := dir
	if filepath.Base(filepath.Clean(dir)) != MediaStagingSubdir {
		stagingDir = filepath.Join(dir, MediaStagingSubdir)
	}
	clean := func() {
		if err := CleanupStagingDirWhere(stagingDir, ttl, time.Now(), shouldSkip); err != nil && log != nil {
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
	return CleanupStagingDirWhere(stagingDir, maxAge, now, nil)
}

// CleanupStagingDirWhere removes staging entries older than maxAge unless shouldSkip returns true for their path.
func CleanupStagingDirWhere(stagingDir string, maxAge time.Duration, now time.Time, shouldSkip func(path string) bool) error {
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
		if shouldSkip != nil && shouldSkip(p) {
			continue
		}
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

func allocateStagingPath(dir, safeName string) (string, string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", "", err
	}
	realDir, err := resolveExistingDir(dir)
	if err != nil {
		return "", "", err
	}
	stagingRoot := realDir
	if filepath.Base(realDir) != MediaStagingSubdir {
		stagingRoot = filepath.Join(realDir, MediaStagingSubdir)
		if err := os.MkdirAll(stagingRoot, 0o700); err != nil {
			return "", "", err
		}
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
