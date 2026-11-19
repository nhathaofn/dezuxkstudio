package media

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"app/internal/apperr"
	"app/internal/config"
	"app/internal/contracts"
	domainmedia "app/internal/domain/media"
	"app/internal/engine"
	"app/internal/fsutil"
	"app/internal/security"
)

// RegisterAssetParams contains input parameters for registering a media asset.
type RegisterAssetParams struct {
	ID             string
	Kind           string
	SourceNode     string
	FilePath       string
	RegisteredRoot string
	MIMEType       string
	SizeBytes      int64
	Width          int
	Height         int
	DurationSec    float64
	MetadataJSON   string
}

// Repository defines the storage contract required by media.Service.
type Repository = domainmedia.Repository

// Options configures Service construction.
type Options struct {
	Repo         Repository
	Signer       *security.TicketSigner
	Config       config.MediaConfig
	MediaDir     string
	StagingDir   string
	AllowedRoots []string
	OnChanged    func()
	Logger       *slog.Logger
}

// Service manages the media assets library, realpath symlink escape guard,
// signed URL issuance, and asset file resolution.
type Service struct {
	repo         Repository
	signer       *security.TicketSigner
	cfg          config.MediaConfig
	mediaDir     string
	stagingDir   string
	mediaRoots   []string
	allowedRoots []string
	onChanged    func()
	log          *slog.Logger
	mu           sync.RWMutex
}

var allowedInputMediaExts = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".webp": true, ".gif": true, ".bmp": true,
	".tif": true, ".tiff": true, ".heic": true, ".heif": true, ".avif": true,
	".mp4": true, ".m4v": true, ".mov": true, ".webm": true, ".mkv": true, ".avi": true,
	".wmv": true, ".flv": true, ".mpg": true, ".mpeg": true, ".3gp": true,
	".mp3": true, ".wav": true, ".m4a": true, ".aac": true, ".flac": true, ".ogg": true, ".opus": true, ".wma": true,
}

// New constructs a new media Service.
func New(opts Options) *Service {
	cfg := config.ResolveMediaConfig(opts.Config)

	log := opts.Logger
	if log == nil {
		log = slog.Default()
	}

	cleanMediaDir := ""
	if strings.TrimSpace(opts.MediaDir) != "" {
		cleanMediaDir = filepath.Clean(opts.MediaDir)
		if realMediaDir, err := filepath.EvalSymlinks(cleanMediaDir); err == nil {
			cleanMediaDir = realMediaDir
		}
	}

	cleanStagingDir := ""
	if strings.TrimSpace(opts.StagingDir) != "" {
		cleanStagingDir = filepath.Clean(opts.StagingDir)
		if realStagingDir, err := filepath.EvalSymlinks(cleanStagingDir); err == nil {
			cleanStagingDir = realStagingDir
		}
	}

	roots := make([]string, 0, len(opts.AllowedRoots)+2)
	addRoot := func(candidate string) {
		if candidate == "" {
			return
		}
		for _, existing := range roots {
			if strings.EqualFold(existing, candidate) {
				return
			}
		}
		roots = append(roots, candidate)
	}

	addRoot(cleanMediaDir)
	addRoot(cleanStagingDir)

	for _, r := range opts.AllowedRoots {
		if strings.TrimSpace(r) != "" {
			cleanR := filepath.Clean(r)
			if realR, err := filepath.EvalSymlinks(cleanR); err == nil {
				cleanR = realR
			}
			addRoot(cleanR)
		}
	}

	var mediaRoots []string
	if cleanMediaDir != "" {
		mediaRoots = append(mediaRoots, cleanMediaDir)
	}

	return &Service{
		repo:         opts.Repo,
		signer:       opts.Signer,
		cfg:          cfg,
		mediaDir:     cleanMediaDir,
		stagingDir:   cleanStagingDir,
		mediaRoots:   mediaRoots,
		allowedRoots: roots,
		onChanged:    opts.OnChanged,
		log:          log,
	}
}

func (s *Service) appendAllowedRootLocked(clean string) {
	for _, existing := range s.allowedRoots {
		if strings.EqualFold(existing, clean) {
			return
		}
	}
	if len(s.allowedRoots) >= s.cfg.RegisteredRootsMax {
		isMediaRoot := func(r string) bool {
			for _, m := range s.mediaRoots {
				if strings.EqualFold(m, r) {
					return true
				}
			}
			return false
		}

		// Phase 1: Evict oldest root that is not current mediaDir, stagingDir, or any historic mediaRoot
		evictedIdx := -1
		for i, r := range s.allowedRoots {
			if !strings.EqualFold(r, s.mediaDir) && !strings.EqualFold(r, s.stagingDir) && !isMediaRoot(r) {
				evictedIdx = i
				break
			}
		}
		// Phase 2: If all candidates are historic media roots, evict oldest historic media root
		if evictedIdx < 0 {
			for i, r := range s.allowedRoots {
				if !strings.EqualFold(r, s.mediaDir) && !strings.EqualFold(r, s.stagingDir) {
					evictedIdx = i
					break
				}
			}
		}
		if evictedIdx >= 0 {
			s.allowedRoots = append(s.allowedRoots[:evictedIdx], s.allowedRoots[evictedIdx+1:]...)
		}
	}
	s.allowedRoots = append(s.allowedRoots, clean)
}

// AddAllowedRoot registers a new root directory as authorized for media inspection.
func (s *Service) AddAllowedRoot(root string) {
	if strings.TrimSpace(root) == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	clean := filepath.Clean(root)
	realRoot, err := filepath.EvalSymlinks(clean)
	if err == nil {
		clean = realRoot
	}
	s.appendAllowedRootLocked(clean)
}

// SetMediaDir updates the active media root directory and ensures old roots remain authorized.
func (s *Service) SetMediaDir(newMediaDir string) {
	if strings.TrimSpace(newMediaDir) == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	clean := filepath.Clean(newMediaDir)
	realDir, err := filepath.EvalSymlinks(clean)
	if err == nil {
		clean = realDir
	}
	s.mediaDir = clean

	found := false
	for _, m := range s.mediaRoots {
		if strings.EqualFold(m, clean) {
			found = true
			break
		}
	}
	if !found {
		s.mediaRoots = append(s.mediaRoots, clean)
	}

	s.appendAllowedRootLocked(clean)
}

// MediaDir returns the currently configured media root directory.
func (s *Service) MediaDir() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.mediaDir
}

// StagingDir returns the currently configured staging directory.
func (s *Service) StagingDir() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.stagingDir
}

func (s *Service) getMediaDir() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.mediaDir
}

// isRootAuthorized checks if realRoot is an authorized registered root or subdirectory of one.
// Fails closed when no allowedRoots are configured.
func (s *Service) isRootAuthorized(realRoot string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if len(s.allowedRoots) == 0 {
		return false
	}

	for _, allowed := range s.allowedRoots {
		if fsutil.IsSubpath(allowed, realRoot, false) {
			return true
		}
	}
	return false
}

// matchingAuthorizedRoot returns the most specific authorized root containing realTarget, or "" if none.
func (s *Service) matchingAuthorizedRoot(realTarget string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	best := ""
	for _, allowed := range s.allowedRoots {
		if fsutil.IsSubpath(allowed, realTarget, false) && len(allowed) > len(best) {
			best = allowed
		}
	}
	return best
}

// IsPathAuthorized reports whether target file or directory is inside any authorized root.
// Fails closed when no allowedRoots are configured.
func (s *Service) IsPathAuthorized(target string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if len(s.allowedRoots) == 0 {
		return false
	}

	cleanTarget := filepath.Clean(target)
	realTarget, err := filepath.EvalSymlinks(cleanTarget)
	if err == nil {
		cleanTarget = realTarget
	}

	for _, allowed := range s.allowedRoots {
		if fsutil.IsSubpath(allowed, cleanTarget, false) {
			return true
		}
	}
	return false
}

// RegisterAsset validates, secures with realpath guard, and persists a media asset record.
func (s *Service) RegisterAsset(ctx context.Context, req RegisterAssetParams) (*contracts.MediaAssetItem, error) {
	if s.repo == nil {
		return nil, apperr.Unavailable("media repository is unavailable")
	}

	cleanFile := strings.TrimSpace(req.FilePath)
	if cleanFile == "" {
		return nil, apperr.Validation("file_path is required")
	}

	rootCandidate := strings.TrimSpace(req.RegisteredRoot)
	if rootCandidate == "" {
		rootCandidate = s.getMediaDir()
	}
	if rootCandidate == "" {
		rootCandidate = filepath.Dir(cleanFile)
	}

	realTarget, realRoot, err := evalSubpath(cleanFile, rootCandidate)
	if err != nil {
		return nil, err
	}

	if !s.isRootAuthorized(realRoot) {
		return nil, apperr.New(apperr.CodeUnauthorized, "registered root is not authorized")
	}

	fi, err := os.Stat(realTarget)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, apperr.Wrap(apperr.CodeNotFound, "media file does not exist", err)
		}
		return nil, apperr.Wrap(apperr.CodeValidation, "cannot read media file metadata", err)
	}
	if !fi.Mode().IsRegular() {
		return nil, apperr.Validation("media file is not a regular file")
	}

	size := req.SizeBytes
	if size <= 0 {
		size = fi.Size()
	}

	mimeType := strings.TrimSpace(req.MIMEType)
	if mimeType == "" || mimeType == "application/octet-stream" {
		mimeType = detectMimeType(realTarget)
	}

	kind := strings.TrimSpace(req.Kind)
	if kind == "" {
		kind = inferKindFromMime(mimeType)
	}

	sourceNode := strings.TrimSpace(req.SourceNode)
	if sourceNode == "" {
		sourceNode = "user_input"
	}

	record := domainmedia.AssetRecord{
		ID:             req.ID,
		Kind:           kind,
		SourceNode:     sourceNode,
		FilePath:       realTarget,
		RegisteredRoot: realRoot,
		MIMEType:       mimeType,
		SizeBytes:      size,
		Width:          req.Width,
		Height:         req.Height,
		DurationSec:    req.DurationSec,
		MetadataJSON:   req.MetadataJSON,
		CreatedAt:      time.Now().Unix(),
		LastVerifiedAt: time.Now().Unix(),
	}

	saved, err := s.repo.RegisterAsset(ctx, record)
	if err != nil {
		return nil, apperr.Wrap(apperr.CodeInternal, "failed to register media asset", err)
	}

	if s.onChanged != nil {
		s.onChanged()
	}

	res := toContract(*saved)
	return &res, nil
}

// RegisterInput registers an existing local media file as an asset (media:register_input action).
// In guarded server mode (engine.HasFileGuard), FileGuard is enforced first and new roots are never added.
// In local desktop mode, user-selected regular media files outside mediaDir register their parent directory safely.
func (s *Service) RegisterInput(ctx context.Context, p contracts.MediaRegisterInputPayload) (contracts.MediaAssetItem, error) {
	if err := p.Validate(); err != nil {
		return contracts.MediaAssetItem{}, err
	}
	rawPath := strings.TrimSpace(p.FilePath)
	if strings.ContainsRune(rawPath, 0) {
		return contracts.MediaAssetItem{}, apperr.Validation("invalid media file path")
	}
	if err := engine.CheckFile(ctx, rawPath, false); err != nil {
		return contracts.MediaAssetItem{}, apperr.From(err)
	}

	candidate := rawPath
	if !filepath.IsAbs(candidate) {
		baseRoot := s.getMediaDir()
		if baseRoot == "" {
			return contracts.MediaAssetItem{}, apperr.Validation("relative media file path requires configured media directory")
		}
		candidate = filepath.Join(baseRoot, filepath.FromSlash(candidate))
	}
	candidate = filepath.Clean(candidate)

	realFile, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		if os.IsNotExist(err) {
			return contracts.MediaAssetItem{}, apperr.Wrap(apperr.CodeNotFound, "media file does not exist", err)
		}
		return contracts.MediaAssetItem{}, apperr.Wrap(apperr.CodeValidation, "invalid media file path", err)
	}
	if err := engine.CheckFile(ctx, realFile, false); err != nil {
		return contracts.MediaAssetItem{}, apperr.From(err)
	}

	fi, err := os.Stat(realFile)
	if err != nil {
		if os.IsNotExist(err) {
			return contracts.MediaAssetItem{}, apperr.Wrap(apperr.CodeNotFound, "media file does not exist", err)
		}
		return contracts.MediaAssetItem{}, apperr.Wrap(apperr.CodeValidation, "cannot read media file metadata", err)
	}
	if !fi.Mode().IsRegular() {
		return contracts.MediaAssetItem{}, apperr.Validation("media file is not a regular file")
	}

	ext := strings.ToLower(filepath.Ext(realFile))
	if !allowedInputMediaExts[ext] {
		return contracts.MediaAssetItem{}, apperr.Validation("unsupported media file extension")
	}

	root := s.matchingAuthorizedRoot(realFile)
	if root == "" {
		if engine.HasFileGuard(ctx) {
			return contracts.MediaAssetItem{}, apperr.New(apperr.CodeUnauthorized, "media file is outside authorized roots")
		}
		realDir := filepath.Dir(realFile)
		s.AddAllowedRoot(realDir)
		root = realDir
	}

	item, err := s.RegisterAsset(ctx, RegisterAssetParams{
		FilePath:       realFile,
		Kind:           p.Kind,
		SourceNode:     p.SourceNode,
		RegisteredRoot: root,
	})
	if err != nil {
		return contracts.MediaAssetItem{}, err
	}
	return *item, nil
}

// List returns a paginated list of media assets matching filter.
// Expiration and missing-file pruning run asynchronously in RunGC / PruneNow rather than on read queries.
func (s *Service) List(ctx context.Context, filter contracts.MediaListPayload) (contracts.MediaListResult, error) {
	if s.repo == nil {
		return contracts.MediaListResult{Assets: []contracts.MediaAssetItem{}}, nil
	}

	records, total, err := s.repo.ListAssets(ctx, domainmedia.ListFilter{
		Kind:   filter.Kind,
		Limit:  filter.Limit,
		Offset: filter.Offset,
		Search: filter.Search,
	})
	if err != nil {
		return contracts.MediaListResult{}, apperr.Wrap(apperr.CodeInternal, "failed to list media assets", err)
	}

	assets := make([]contracts.MediaAssetItem, len(records))
	for i, r := range records {
		assets[i] = toContract(r)
	}

	return contracts.MediaListResult{
		Assets: assets,
		Total:  total,
	}, nil
}

// RunGC periodically prunes expired and missing media asset records until ctx is cancelled.
func (s *Service) RunGC(ctx context.Context) {
	s.PruneNow(ctx)
	ticker := time.NewTicker(15 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.PruneNow(ctx)
		}
	}
}

// PruneNow removes expired library entries and missing on-disk backing files in bounded batches.
func (s *Service) PruneNow(ctx context.Context) {
	if s.repo == nil || ctx.Err() != nil {
		return
	}
	if s.cfg.LibraryMaxItems > 0 || s.cfg.LibraryRetention > 0 {
		olderThan := time.Time{}
		if s.cfg.LibraryRetention > 0 {
			olderThan = time.Now().Add(-s.cfg.LibraryRetention)
		}
		_, _ = s.repo.PruneExpiredAssets(ctx, olderThan, s.cfg.LibraryMaxItems)
	}
	const batchSize = 200
	var missingIDs []string
	for offset := 0; ; offset += batchSize {
		if ctx.Err() != nil {
			return
		}
		records, total, err := s.repo.ListAssets(ctx, domainmedia.ListFilter{Limit: batchSize, Offset: offset})
		if err != nil || len(records) == 0 {
			break
		}
		for _, rec := range records {
			if _, statErr := os.Stat(rec.FilePath); os.IsNotExist(statErr) {
				missingIDs = append(missingIDs, rec.ID)
			}
		}
		if offset+len(records) >= total {
			break
		}
	}
	if len(missingIDs) > 0 {
		if n, err := s.repo.PruneMissingAssets(ctx, missingIDs); err == nil && n > 0 && s.onChanged != nil {
			s.onChanged()
		}
	}
}

// Delete removes an asset from the library, optionally removing the backing file.
// Validates paths and guards first, deletes the database record, then removes the backing file.
func (s *Service) Delete(ctx context.Context, id string, deleteFile bool) error {
	if s.repo == nil {
		return apperr.Unavailable("media repository is unavailable")
	}

	asset, err := s.repo.GetAsset(ctx, id)
	if err != nil {
		return apperr.Wrap(apperr.CodeInternal, "failed to get asset for deletion", err)
	}
	if asset == nil {
		return apperr.NotFound("media asset not found").WithDetails(map[string]any{"id": id})
	}

	var realTarget string
	if deleteFile {
		target, realRoot, err := evalSubpath(asset.FilePath, asset.RegisteredRoot)
		if err != nil {
			return apperr.Wrap(apperr.CodeUnauthorized, "cannot safely delete backing file", err)
		}
		if !s.isRootAuthorized(realRoot) {
			return apperr.New(apperr.CodeUnauthorized, "cannot delete backing file: root is not authorized")
		}
		if err := engine.CheckFile(ctx, target, true); err != nil {
			return apperr.From(err)
		}
		realTarget = target
	}

	if err := s.repo.DeleteAsset(ctx, id); err != nil {
		return apperr.Wrap(apperr.CodeInternal, "failed to delete media asset record", err)
	}
	if deleteFile && realTarget != "" {
		if err := os.Remove(realTarget); err != nil && !os.IsNotExist(err) {
			return apperr.Wrap(apperr.CodeUnavailable, "failed to remove media file from disk", err)
		}
	}

	if s.onChanged != nil {
		s.onChanged()
	}
	return nil
}

// SignAssetURL issues a signed scoped streaming/download URL for assetID, capping TTL at configured TicketTTL.
func (s *Service) SignAssetURL(ctx context.Context, p contracts.MediaSignURLPayload) (contracts.MediaSignedURLResult, error) {
	if err := p.Validate(); err != nil {
		return contracts.MediaSignedURLResult{}, err
	}
	if s.signer == nil {
		return contracts.MediaSignedURLResult{}, apperr.Unavailable("ticket signer is not configured")
	}
	if s.repo == nil {
		return contracts.MediaSignedURLResult{}, apperr.Unavailable("media repository is unavailable")
	}

	asset, err := s.repo.GetAsset(ctx, p.AssetID)
	if err != nil {
		return contracts.MediaSignedURLResult{}, apperr.Wrap(apperr.CodeInternal, "failed to get asset", err)
	}
	if asset == nil {
		return contracts.MediaSignedURLResult{}, apperr.NotFound("media asset not found").WithDetails(map[string]any{"id": p.AssetID})
	}

	purpose := p.Purpose
	if purpose == "" {
		purpose = security.PurposePreview
	}

	ttl := s.cfg.TicketTTL
	if p.TTLSeconds > 0 {
		requested := time.Duration(p.TTLSeconds) * time.Second
		if requested < ttl {
			ttl = requested
		}
	}

	ticket, err := s.signer.SignTicket(p.AssetID, purpose, ttl)
	if err != nil {
		return contracts.MediaSignedURLResult{}, apperr.Wrap(apperr.CodeInternal, "failed to sign ticket", err)
	}

	expiresAt := time.Now().Add(ttl).Unix()
	urlStr := "/api/media/download?asset_id=" + url.QueryEscape(p.AssetID) +
		"&ticket=" + url.QueryEscape(ticket) +
		"&purpose=" + url.QueryEscape(purpose)

	return contracts.MediaSignedURLResult{
		AssetID:   p.AssetID,
		URL:       urlStr,
		Ticket:    ticket,
		ExpiresAt: expiresAt,
		Purpose:   purpose,
	}, nil
}

// ResolveAssetFile resolves the real on-disk file path, verifies that it strictly resides
// within its registered root without symlink escape, and returns metadata for HTTP streaming.
func (s *Service) ResolveAssetFile(ctx context.Context, assetID string) (filePath string, mimeType string, modTime time.Time, size int64, err error) {
	if s.repo == nil {
		return "", "", time.Time{}, 0, apperr.Unavailable("media repository is unavailable")
	}

	asset, err := s.repo.GetAsset(ctx, assetID)
	if err != nil {
		return "", "", time.Time{}, 0, apperr.Wrap(apperr.CodeInternal, "failed to get asset", err)
	}
	if asset == nil {
		return "", "", time.Time{}, 0, apperr.NotFound("media asset not found").WithDetails(map[string]any{"id": assetID})
	}

	realTarget, realRoot, err := evalSubpath(asset.FilePath, asset.RegisteredRoot)
	if err != nil {
		return "", "", time.Time{}, 0, err
	}

	if !s.isRootAuthorized(realRoot) {
		return "", "", time.Time{}, 0, apperr.New(apperr.CodeUnauthorized, "asset registered root is not authorized")
	}

	fi, err := os.Stat(realTarget)
	if err != nil {
		if os.IsNotExist(err) {
			return "", "", time.Time{}, 0, apperr.Wrap(apperr.CodeNotFound, "media asset file missing on disk", err)
		}
		return "", "", time.Time{}, 0, apperr.Wrap(apperr.CodeValidation, "failed to read media file metadata", err)
	}
	if !fi.Mode().IsRegular() {
		return "", "", time.Time{}, 0, apperr.Validation("resolved path is not a regular file")
	}

	_ = s.repo.UpdateLastVerified(ctx, asset.ID, time.Now().Unix())

	return realTarget, asset.MIMEType, fi.ModTime(), fi.Size(), nil
}

// ResolveAsset returns the on-disk file path and registered root for an asset ID.
// It implements handlers.AssetResolver for MediaDownload.
func (s *Service) ResolveAsset(ctx context.Context, assetID string) (filePath string, registeredRoot string, err error) {
	if s.repo == nil {
		return "", "", apperr.Unavailable("media repository is unavailable")
	}
	asset, err := s.repo.GetAsset(ctx, assetID)
	if err != nil {
		return "", "", apperr.Wrap(apperr.CodeInternal, "failed to get asset", err)
	}
	if asset == nil {
		return "", "", apperr.NotFound("media asset not found").WithDetails(map[string]any{"id": assetID})
	}
	return asset.FilePath, asset.RegisteredRoot, nil
}

// GetAsset returns a single media asset item by ID.
func (s *Service) GetAsset(ctx context.Context, id string) (*contracts.MediaAssetItem, error) {
	if s.repo == nil {
		return nil, apperr.Unavailable("media repository is unavailable")
	}
	asset, err := s.repo.GetAsset(ctx, id)
	if err != nil {
		return nil, apperr.Wrap(apperr.CodeInternal, "failed to get asset", err)
	}
	if asset == nil {
		return nil, apperr.NotFound("media asset not found").WithDetails(map[string]any{"id": id})
	}
	item := toContract(*asset)
	return &item, nil
}

// evalSubpath evaluates symlinks and asserts realTarget is inside realRoot without leaking host paths in user messages.
func evalSubpath(targetPath, rootPath string) (realTarget, realRoot string, err error) {
	cleanRoot := filepath.Clean(rootPath)
	realRoot, err = filepath.EvalSymlinks(cleanRoot)
	if err != nil {
		return "", "", apperr.Wrap(apperr.CodeUnauthorized, "cannot resolve registered root", err)
	}

	cleanTarget := targetPath
	if !filepath.IsAbs(cleanTarget) {
		cleanTarget = filepath.Join(realRoot, filepath.FromSlash(cleanTarget))
	}
	cleanTarget = filepath.Clean(cleanTarget)

	realTarget, err = filepath.EvalSymlinks(cleanTarget)
	if err != nil {
		if os.IsNotExist(err) {
			return "", "", apperr.Wrap(apperr.CodeNotFound, "media file does not exist", err)
		}
		return "", "", apperr.Wrap(apperr.CodeValidation, "invalid media path", err)
	}

	resolvedTarget, resolvedRoot, resErr := fsutil.ResolveSubpath(realRoot, realTarget, false)
	if resErr != nil {
		return "", "", apperr.New(apperr.CodeUnauthorized, "path escapes registered root boundary")
	}

	return resolvedTarget, resolvedRoot, nil
}

func detectMimeType(filePath string) string {
	f, err := os.Open(filePath)
	if err != nil {
		return "application/octet-stream"
	}
	defer f.Close()

	buf := make([]byte, 512)
	n, err := f.Read(buf)
	if err != nil && err != io.EOF {
		return "application/octet-stream"
	}
	ct := strings.Split(http.DetectContentType(buf[:n]), ";")[0]
	if ct != "application/octet-stream" {
		return ct
	}

	ext := strings.ToLower(filepath.Ext(filePath))
	switch ext {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".webp":
		return "image/webp"
	case ".mp4":
		return "video/mp4"
	case ".webm":
		return "video/webm"
	case ".mov":
		return "video/quicktime"
	case ".wav":
		return "audio/wav"
	case ".mp3":
		return "audio/mpeg"
	case ".flac":
		return "audio/flac"
	case ".ogg":
		return "audio/ogg"
	case ".zip":
		return "application/zip"
	case ".fcpxml":
		return "application/xml"
	case ".otio", ".json":
		return "application/json"
	case ".srt", ".ass":
		return "text/plain"
	}
	return "application/octet-stream"
}

func inferKindFromMime(mime string) string {
	lower := strings.ToLower(mime)
	if strings.HasPrefix(lower, "image/") {
		return "image"
	}
	if strings.HasPrefix(lower, "video/") {
		return "video"
	}
	if strings.HasPrefix(lower, "audio/") {
		return "audio"
	}
	return "image"
}

func toContract(r domainmedia.AssetRecord) contracts.MediaAssetItem {
	return contracts.MediaAssetItem{
		ID:             r.ID,
		Kind:           r.Kind,
		SourceNode:     r.SourceNode,
		FilePath:       r.FilePath,
		RegisteredRoot: r.RegisteredRoot,
		MIMEType:       r.MIMEType,
		SizeBytes:      r.SizeBytes,
		Width:          r.Width,
		Height:         r.Height,
		DurationSec:    r.DurationSec,
		MetadataJSON:   r.MetadataJSON,
		CreatedAt:      r.CreatedAt,
		LastVerifiedAt: r.LastVerifiedAt,
	}
}
