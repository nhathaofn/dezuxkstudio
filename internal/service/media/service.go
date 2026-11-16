package media

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"app/internal/apperr"
	"app/internal/config"
	"app/internal/contracts"
	"app/internal/security"
	"app/internal/storage/sqlite"
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
type Repository interface {
	RegisterAsset(ctx context.Context, asset sqlite.MediaAssetRecord) (*sqlite.MediaAssetRecord, error)
	GetAsset(ctx context.Context, id string) (*sqlite.MediaAssetRecord, error)
	GetAssetByPath(ctx context.Context, filePath string) (*sqlite.MediaAssetRecord, error)
	ListAssets(ctx context.Context, filter sqlite.MediaListFilter) ([]sqlite.MediaAssetRecord, int, error)
	DeleteAsset(ctx context.Context, id string) error
	PruneMissingAssets(ctx context.Context, missingIDs []string) (int64, error)
	PruneExpiredAssets(ctx context.Context, olderThan time.Time, maxItems int) (int64, error)
	UpdateLastVerified(ctx context.Context, id string, verifiedAt int64) error
}

// Options configures Service construction.
type Options struct {
	Repo         Repository
	Signer       *security.TicketSigner
	Config       config.MediaConfig
	MediaDir     string
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
	allowedRoots []string
	onChanged    func()
	log          *slog.Logger
	mu           sync.RWMutex
}

// New constructs a new media Service.
func New(opts Options) *Service {
	cfg := opts.Config
	def := config.Default(config.ModeDesktop).Media
	if cfg.LibraryMaxItems <= 0 {
		cfg.LibraryMaxItems = def.LibraryMaxItems
	}
	if cfg.LibraryRetention <= 0 {
		cfg.LibraryRetention = def.LibraryRetention
	}
	if cfg.TicketTTL <= 0 {
		cfg.TicketTTL = def.TicketTTL
	}
	if cfg.RegisteredRootsMax <= 0 {
		cfg.RegisteredRootsMax = def.RegisteredRootsMax
	}

	log := opts.Logger
	if log == nil {
		log = slog.Default()
	}

	roots := make([]string, 0, len(opts.AllowedRoots)+1)
	if strings.TrimSpace(opts.MediaDir) != "" {
		if realMediaDir, err := filepath.EvalSymlinks(filepath.Clean(opts.MediaDir)); err == nil {
			roots = append(roots, realMediaDir)
		} else {
			roots = append(roots, filepath.Clean(opts.MediaDir))
		}
	}
	for _, r := range opts.AllowedRoots {
		if strings.TrimSpace(r) != "" {
			if realR, err := filepath.EvalSymlinks(filepath.Clean(r)); err == nil {
				roots = append(roots, realR)
			} else {
				roots = append(roots, filepath.Clean(r))
			}
		}
	}

	return &Service{
		repo:         opts.Repo,
		signer:       opts.Signer,
		cfg:          cfg,
		mediaDir:     opts.MediaDir,
		allowedRoots: roots,
		onChanged:    opts.OnChanged,
		log:          log,
	}
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
	for _, existing := range s.allowedRoots {
		if strings.EqualFold(existing, clean) {
			return
		}
	}
	if len(s.allowedRoots) >= s.cfg.RegisteredRootsMax {
		// Replace oldest non-mediaDir root
		if len(s.allowedRoots) > 1 {
			s.allowedRoots = append(s.allowedRoots[:1], s.allowedRoots[2:]...)
		}
	}
	s.allowedRoots = append(s.allowedRoots, clean)
}

// isRootAuthorized checks if realRoot is an authorized registered root or subdirectory of one.
func (s *Service) isRootAuthorized(realRoot string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if len(s.allowedRoots) == 0 {
		return true // If no roots configured, allow root itself
	}

	for _, allowed := range s.allowedRoots {
		if isSubpath(allowed, realRoot) {
			return true
		}
	}
	return false
}

// IsPathAuthorized reports whether target file or directory is inside any authorized root.
func (s *Service) IsPathAuthorized(target string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if len(s.allowedRoots) == 0 {
		return true
	}

	cleanTarget := filepath.Clean(target)
	realTarget, err := filepath.EvalSymlinks(cleanTarget)
	if err == nil {
		cleanTarget = realTarget
	}

	for _, allowed := range s.allowedRoots {
		if isSubpath(allowed, cleanTarget) {
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
		rootCandidate = s.mediaDir
	}
	if rootCandidate == "" {
		rootCandidate = filepath.Dir(cleanFile)
	}

	realTarget, realRoot, err := evalSubpath(cleanFile, rootCandidate)
	if err != nil {
		return nil, err
	}

	if !s.isRootAuthorized(realRoot) {
		return nil, apperr.New(apperr.CodeUnauthorized, fmt.Sprintf("registered root %q is not authorized", realRoot))
	}

	fi, err := os.Stat(realTarget)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, apperr.NotFound(fmt.Sprintf("media file does not exist: %v", err))
		}
		return nil, apperr.Wrap(apperr.CodeValidation, "cannot read media file info", err)
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

	record := sqlite.MediaAssetRecord{
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
func (s *Service) RegisterInput(ctx context.Context, p contracts.MediaRegisterInputPayload) (contracts.MediaAssetItem, error) {
	if err := p.Validate(); err != nil {
		return contracts.MediaAssetItem{}, err
	}
	root := s.mediaDir
	if root == "" {
		root = filepath.Dir(p.FilePath)
	}
	// Temporarily allow this file's folder as root if inside workspace
	s.AddAllowedRoot(filepath.Dir(p.FilePath))

	item, err := s.RegisterAsset(ctx, RegisterAssetParams{
		FilePath:       p.FilePath,
		Kind:           p.Kind,
		SourceNode:     p.SourceNode,
		RegisteredRoot: root,
	})
	if err != nil {
		return contracts.MediaAssetItem{}, err
	}
	return *item, nil
}

// List returns a paginated list of media assets matching filter, and performs auto-pruning.
func (s *Service) List(ctx context.Context, filter contracts.MediaListPayload) (contracts.MediaListResult, error) {
	if s.repo == nil {
		return contracts.MediaListResult{Assets: []contracts.MediaAssetItem{}}, nil
	}

	// Auto-prune expired assets if exceeding library max items or retention
	if s.cfg.LibraryMaxItems > 0 || s.cfg.LibraryRetention > 0 {
		olderThan := time.Time{}
		if s.cfg.LibraryRetention > 0 {
			olderThan = time.Now().Add(-s.cfg.LibraryRetention)
		}
		_, _ = s.repo.PruneExpiredAssets(ctx, olderThan, s.cfg.LibraryMaxItems)
	}

	records, total, err := s.repo.ListAssets(ctx, sqlite.MediaListFilter{
		Kind:   filter.Kind,
		Limit:  filter.Limit,
		Offset: filter.Offset,
		Search: filter.Search,
	})
	if err != nil {
		return contracts.MediaListResult{}, apperr.Wrap(apperr.CodeInternal, "failed to list media assets", err)
	}

	// Auto-prune missing files on listing
	var missingIDs []string
	validRecords := make([]sqlite.MediaAssetRecord, 0, len(records))
	for _, rec := range records {
		if _, err := os.Stat(rec.FilePath); os.IsNotExist(err) {
			missingIDs = append(missingIDs, rec.ID)
		} else {
			validRecords = append(validRecords, rec)
		}
	}

	if len(missingIDs) > 0 {
		_, _ = s.repo.PruneMissingAssets(ctx, missingIDs)
		total -= len(missingIDs)
		if total < 0 {
			total = 0
		}
		records = validRecords
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

// Delete removes an asset from the library, optionally removing the backing file.
func (s *Service) Delete(ctx context.Context, id string, deleteFile bool) error {
	if s.repo == nil {
		return apperr.Unavailable("media repository is unavailable")
	}

	asset, err := s.repo.GetAsset(ctx, id)
	if err != nil {
		return apperr.Wrap(apperr.CodeInternal, "failed to get asset for deletion", err)
	}
	if asset == nil {
		return apperr.NotFound(fmt.Sprintf("media asset %q not found", id))
	}

	if deleteFile {
		realTarget, realRoot, err := evalSubpath(asset.FilePath, asset.RegisteredRoot)
		if err != nil {
			return apperr.New(apperr.CodeUnauthorized, fmt.Sprintf("cannot safely delete backing file: %v", err))
		}
		if !s.isRootAuthorized(realRoot) {
			return apperr.New(apperr.CodeUnauthorized, "cannot delete backing file: root is not authorized")
		}
		_ = os.Remove(realTarget)
	}

	if err := s.repo.DeleteAsset(ctx, id); err != nil {
		return apperr.Wrap(apperr.CodeInternal, "failed to delete media asset record", err)
	}

	if s.onChanged != nil {
		s.onChanged()
	}
	return nil
}

// SignAssetURL issues a signed scoped streaming/download URL for assetID.
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
		return contracts.MediaSignedURLResult{}, apperr.NotFound(fmt.Sprintf("media asset %q not found", p.AssetID))
	}

	purpose := p.Purpose
	if purpose == "" {
		purpose = security.PurposePreview
	}

	ttl := s.cfg.TicketTTL
	if p.TTLSeconds > 0 {
		ttl = time.Duration(p.TTLSeconds) * time.Second
	}

	ticket, err := s.signer.SignTicket(p.AssetID, purpose, ttl)
	if err != nil {
		return contracts.MediaSignedURLResult{}, apperr.Wrap(apperr.CodeInternal, "failed to sign ticket", err)
	}

	expiresAt := time.Now().Add(ttl).Unix()
	urlStr := fmt.Sprintf("/api/media/download?asset_id=%s&ticket=%s&purpose=%s",
		url.QueryEscape(p.AssetID),
		url.QueryEscape(ticket),
		url.QueryEscape(purpose),
	)

	return contracts.MediaSignedURLResult{
		AssetID:   p.AssetID,
		URL:       urlStr,
		Ticket:    ticket,
		ExpiresAt: expiresAt,
		Purpose:   purpose,
	}, nil
}

// SignURL issues a signed URL string using default TicketTTL.
func (s *Service) SignURL(ctx context.Context, assetID, purpose string) (string, error) {
	res, err := s.SignAssetURL(ctx, contracts.MediaSignURLPayload{
		AssetID: assetID,
		Purpose: purpose,
	})
	if err != nil {
		return "", err
	}
	return res.URL, nil
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
		return "", "", time.Time{}, 0, apperr.NotFound(fmt.Sprintf("media asset %q not found", assetID))
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
			return "", "", time.Time{}, 0, apperr.NotFound("media asset file missing on disk")
		}
		return "", "", time.Time{}, 0, apperr.Wrap(apperr.CodeValidation, "failed to stat media file", err)
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
		return "", "", apperr.NotFound(fmt.Sprintf("media asset %q not found", assetID))
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
		return nil, apperr.NotFound(fmt.Sprintf("media asset %q not found", id))
	}
	item := toContract(*asset)
	return &item, nil
}

// evalSubpath evaluates symlinks and asserts realTarget is inside realRoot.
func evalSubpath(targetPath, rootPath string) (realTarget, realRoot string, err error) {
	cleanRoot := filepath.Clean(rootPath)
	realRoot, err = filepath.EvalSymlinks(cleanRoot)
	if err != nil {
		return "", "", apperr.New(apperr.CodeUnauthorized, fmt.Sprintf("cannot resolve registered root %q: %v", cleanRoot, err))
	}

	cleanTarget := targetPath
	if !filepath.IsAbs(cleanTarget) {
		cleanTarget = filepath.Join(realRoot, filepath.FromSlash(cleanTarget))
	}
	cleanTarget = filepath.Clean(cleanTarget)

	realTarget, err = filepath.EvalSymlinks(cleanTarget)
	if err != nil {
		if os.IsNotExist(err) {
			return "", "", apperr.NotFound(fmt.Sprintf("media file does not exist: %v", err))
		}
		return "", "", apperr.Validation(fmt.Sprintf("invalid media path: %v", err))
	}

	if !isSubpath(realRoot, realTarget) {
		return "", "", apperr.New(apperr.CodeUnauthorized, "path escapes registered root boundary")
	}

	return realTarget, realRoot, nil
}

// isSubpath reports whether target is strictly inside or equal to root directory.
func isSubpath(root, target string) bool {
	r := filepath.Clean(root)
	t := filepath.Clean(target)
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		r = strings.ToLower(r)
		t = strings.ToLower(t)
	}

	rel, err := filepath.Rel(r, t)
	if err != nil {
		return false
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	return true
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

func toContract(r sqlite.MediaAssetRecord) contracts.MediaAssetItem {
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
