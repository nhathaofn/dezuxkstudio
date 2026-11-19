package mediagen

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"app/internal/apperr"
	"app/internal/config"
	"app/internal/fsutil"
	"app/internal/progress"
)

func (s *Service) StagingDir() string {
	s.mediaMu.RLock()
	defer s.mediaMu.RUnlock()
	if strings.TrimSpace(s.stagingDir) != "" {
		return s.stagingDir
	}
	if strings.TrimSpace(s.mediaDir) != "" {
		return filepath.Join(s.mediaDir, "staging")
	}
	return ""
}

func (s *Service) resolveStageTarget(subDir, name string) (string, string, error) {
	baseStagingDir := s.StagingDir()
	if baseStagingDir == "" {
		return "", "", apperr.Validation("Staging directory is not configured")
	}
	baseAbs, err := filepath.Abs(baseStagingDir)
	if err != nil {
		return "", "", apperr.Wrap(apperr.CodeValidation, "Invalid staging directory", err)
	}
	dir := baseAbs
	if cleanSub := strings.TrimSpace(subDir); cleanSub != "" {
		if filepath.IsAbs(cleanSub) || filepath.VolumeName(cleanSub) != "" || strings.HasPrefix(cleanSub, "/") || strings.HasPrefix(cleanSub, `\`) {
			return "", "", apperr.Validation("Staging subdirectory must be relative")
		}
		dir = filepath.Clean(filepath.Join(baseAbs, cleanSub))
		if !fsutil.IsSubpath(baseAbs, dir, true) {
			return "", "", apperr.Validation("Staging subdirectory escapes staging directory")
		}
	}
	fileName := strings.TrimSpace(name)
	if fileName == "" {
		fileName = uniqueFilename("staged", ".bin")
	} else {
		if filepath.IsAbs(fileName) || strings.ContainsAny(fileName, `/\`) || fileName == "." || fileName == ".." || filepath.Base(fileName) != fileName {
			return "", "", apperr.Validation("Staging filename must be a plain basename")
		}
	}
	stagedPath := filepath.Clean(filepath.Join(dir, fileName))
	if !fsutil.IsSubpath(baseAbs, stagedPath, false) {
		return "", "", apperr.Validation("Staged path escapes staging directory")
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", "", apperr.Wrap(apperr.CodeUnavailable, "Failed creating staging directory", err)
	}
	return dir, stagedPath, nil
}

// StageFile writes data to a unique file in the staging directory and acquires an active lease on it.
// The caller must invoke the returned release function when the job completes.
func (s *Service) StageFile(subDir, name string, data []byte) (string, func(), error) {
	if s.cfg.MaxOutputBytes > 0 && int64(len(data)) > s.cfg.MaxOutputBytes {
		return "", nil, apperr.New(apperr.CodeUnavailable, "Staged media payload exceeded size limit").
			WithMsg("errors.mediagen.output_too_large", nil)
	}
	_, stagedPath, err := s.resolveStageTarget(subDir, name)
	if err != nil {
		return "", nil, err
	}
	if err := fsutil.WriteFileAtomic(stagedPath, data, 0o600); err != nil {
		return "", nil, apperr.Wrap(apperr.CodeUnavailable, "Failed writing staged media file", err)
	}
	release := s.LeaseFile(stagedPath)
	return stagedPath, release, nil
}

// StageStream streams bounded content from reader r into a staging file atomically and acquires an active lease on it.
func (s *Service) StageStream(subDir, name string, r io.Reader, maxBytes int64) (string, func(), error) {
	if maxBytes <= 0 {
		maxBytes = s.cfg.MaxOutputBytes
	}
	_, stagedPath, err := s.resolveStageTarget(subDir, name)
	if err != nil {
		return "", nil, err
	}

	err = fsutil.WriteAtomic(stagedPath, 0o600, func(w io.Writer) error {
		n, copyErr := io.Copy(w, io.LimitReader(r, maxBytes+1))
		if copyErr != nil {
			return copyErr
		}
		if n > maxBytes {
			return errStageTooLarge
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, errStageTooLarge) {
			return "", nil, apperr.New(apperr.CodeUnavailable, "Staged media payload exceeded size limit").
				WithMsg("errors.mediagen.output_too_large", nil)
		}
		return "", nil, apperr.Wrap(apperr.CodeUnavailable, "Failed streaming staged data", err)
	}

	release := s.LeaseFile(stagedPath)
	return stagedPath, release, nil
}

// leaseInputs acquires active leases on all provided file paths and returns a combined release function.
func (s *Service) leaseInputs(paths ...string) func() {
	var releases []func()
	for _, p := range paths {
		if strings.TrimSpace(p) != "" {
			releases = append(releases, s.LeaseFile(p))
		}
	}
	return func() {
		for _, rel := range releases {
			if rel != nil {
				rel()
			}
		}
	}
}

// prepareJobContext sets up a bounded request timeout context.
// Single-shot requests (isVideo == false) attach progress.KeepAlive, whereas long async video/lip-sync
// polling flows report real progress ticks via pollUntil so a hung socket is never masked (§6).
func (s *Service) prepareJobContext(ctx context.Context, isVideo bool) (context.Context, func()) {
	callCtx, cancel := s.requestTimeoutCtx(ctx, isVideo)
	if isVideo {
		return callCtx, cancel
	}
	stopKA := progress.KeepAlive(callCtx)
	cleanup := func() {
		stopKA()
		cancel()
	}
	return callCtx, cleanup
}

// readReferenceImages reads and encodes multiple local reference images.

type LeaseRegistry struct {
	mu     sync.RWMutex
	leases map[string]int // normalized absolute path -> active lease refcount
}

// NewLeaseRegistry creates a new active lease registry.
func NewLeaseRegistry() *LeaseRegistry {
	return &LeaseRegistry{
		leases: make(map[string]int),
	}
}

var defaultLeaseRegistry = NewLeaseRegistry()

func normalizeLeasePath(p string) string {
	if strings.TrimSpace(p) == "" {
		return ""
	}
	abs, err := filepath.Abs(p)
	if err == nil {
		p = abs
	}
	p = filepath.Clean(p)
	return strings.ToLower(p)
}

// Acquire registers an active lease on path and returns a release callback.
// Releasing is idempotent.
func (r *LeaseRegistry) Acquire(path string) func() {
	key := normalizeLeasePath(path)
	if key == "" {
		return func() {}
	}
	r.mu.Lock()
	r.leases[key]++
	r.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			r.mu.Lock()
			r.leases[key]--
			if r.leases[key] <= 0 {
				delete(r.leases, key)
			}
			r.mu.Unlock()
		})
	}
}

// Release drops one lease reference on path.
func (r *LeaseRegistry) Release(path string) {
	key := normalizeLeasePath(path)
	if key == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.leases[key]--
	if r.leases[key] <= 0 {
		delete(r.leases, key)
	}
}

// IsLeased reports whether path or any file within/enclosing path has an active lease.
func (r *LeaseRegistry) IsLeased(path string) bool {
	key := normalizeLeasePath(path)
	if key == "" {
		return false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()

	// Direct match
	if r.leases[key] > 0 {
		return true
	}

	// Check if any leased file is inside this directory: key + separator prefix
	prefix := key + string(filepath.Separator)
	for leasedPath := range r.leases {
		if strings.HasPrefix(leasedPath, prefix) {
			return true
		}
	}

	// Check if this path is inside a leased directory: leasedPath + separator prefix
	for leasedPath := range r.leases {
		if strings.HasPrefix(key, leasedPath+string(filepath.Separator)) {
			return true
		}
	}

	return false
}

// ActiveLeases returns all normalized paths currently holding an active lease.
func (r *LeaseRegistry) ActiveLeases() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.leases))
	for k := range r.leases {
		out = append(out, k)
	}
	return out
}

// Count returns the number of unique active lease paths.
func (r *LeaseRegistry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.leases)
}

// Prune scans dir and removes unleased files whose modTime is older than ttl.
// Expired files WITH an active lease are strictly preserved.
func (r *LeaseRegistry) Prune(dir string, ttl time.Duration, now ...time.Time) (int, error) {
	if strings.TrimSpace(dir) == "" {
		return 0, nil
	}
	if _, err := os.Stat(dir); err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	if ttl <= 0 {
		ttl = config.Default(config.ModeDesktop).Media.StagingTTL
	}
	cur := time.Now()
	if len(now) > 0 && !now[0].IsZero() {
		cur = now[0]
	}
	cutoff := cur.Add(-ttl)

	type item struct {
		path    string
		isDir   bool
		modTime time.Time
	}
	var files []item
	var dirs []item

	err := filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if p == dir {
			return nil
		}
		if info.IsDir() {
			dirs = append(dirs, item{path: p, isDir: true, modTime: info.ModTime()})
		} else {
			files = append(files, item{path: p, isDir: false, modTime: info.ModTime()})
		}
		return nil
	})
	if err != nil {
		return 0, err
	}

	pruned := 0
	// 1. Prune expired unleased files
	for _, f := range files {
		if r.IsLeased(f.path) {
			continue // strictly protected
		}
		if f.modTime.Before(cutoff) {
			if err := os.Remove(f.path); err == nil {
				pruned++
			}
		}
	}

	// 2. Prune empty unleased directories (deepest first)
	sort.Slice(dirs, func(i, j int) bool {
		return len(dirs[i].path) > len(dirs[j].path)
	})
	for _, d := range dirs {
		if r.IsLeased(d.path) {
			continue
		}
		entries, err := os.ReadDir(d.path)
		if err == nil && len(entries) == 0 {
			_ = os.Remove(d.path)
		}
	}

	return pruned, nil
}

// Package-level lease functions delegating to defaultLeaseRegistry:

// LeaseFile registers an active lease on path and returns a release callback.
func LeaseFile(path string) func() {
	return defaultLeaseRegistry.Acquire(path)
}

// AcquireLease registers an active lease on path and returns a release callback.
func AcquireLease(path string) func() {
	return defaultLeaseRegistry.Acquire(path)
}

// ReleaseLease drops one lease reference for path.
func ReleaseLease(path string) {
	defaultLeaseRegistry.Release(path)
}

// IsLeased reports whether path is protected by an active lease.
func IsLeased(path string) bool {
	return defaultLeaseRegistry.IsLeased(path)
}

// PruneStaging scans dir and deletes unleased files older than ttl.
// Any file with an active lease is strictly protected.
func PruneStaging(dir string, ttl time.Duration, now ...time.Time) (int, error) {
	return defaultLeaseRegistry.Prune(dir, ttl, now...)
}

// StartStagingGC runs periodic background staging garbage collection until ctx is canceled.
// It executes an initial scan immediately, then ticks at the given interval.
func StartStagingGC(ctx context.Context, dir string, ttl, interval time.Duration) {
	defMedia := config.Default(config.ModeDesktop).Media
	if ttl <= 0 {
		ttl = defMedia.StagingTTL
	}
	if interval <= 0 {
		interval = defMedia.StagingGCInterval
	}
	if _, err := PruneStaging(dir, ttl); err != nil {
		slog.Default().WarnContext(ctx, "mediagen: staging GC prune failed", "error", err)
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := PruneStaging(dir, ttl); err != nil {
				slog.Default().WarnContext(ctx, "mediagen: staging GC prune failed", "error", err)
			}
		}
	}
}

// Service methods delegating to the service's LeaseRegistry:

// LeaseFile registers an active lease on path for this service instance.
func (s *Service) LeaseFile(path string) func() {
	if s.leases == nil {
		return defaultLeaseRegistry.Acquire(path)
	}
	return s.leases.Acquire(path)
}

// AcquireLease registers an active lease on path for this service instance.
func (s *Service) AcquireLease(path string) func() {
	return s.LeaseFile(path)
}

// ReleaseLease drops one lease reference for path.
func (s *Service) ReleaseLease(path string) {
	if s.leases == nil {
		defaultLeaseRegistry.Release(path)
		return
	}
	s.leases.Release(path)
}

// IsLeased reports whether path is protected by an active lease.
func (s *Service) IsLeased(path string) bool {
	if s.leases == nil {
		return defaultLeaseRegistry.IsLeased(path)
	}
	return s.leases.IsLeased(path)
}

// ActiveLeases returns currently held active leases for this service instance.
func (s *Service) ActiveLeases() []string {
	if s.leases == nil {
		return defaultLeaseRegistry.ActiveLeases()
	}
	return s.leases.ActiveLeases()
}

// PruneStaging scans dir (or s.StagingDir() if empty) and deletes unleased expired files.
func (s *Service) PruneStaging(dir string, ttl time.Duration, now ...time.Time) (int, error) {
	if strings.TrimSpace(dir) == "" {
		dir = s.StagingDir()
	}
	if ttl <= 0 {
		ttl = s.stagingTTL
	}
	reg := s.leases
	if reg == nil {
		reg = defaultLeaseRegistry
	}
	return reg.Prune(dir, ttl, now...)
}

// StartStagingGC runs periodic background staging garbage collection until ctx is canceled.
func (s *Service) StartStagingGC(ctx context.Context, dir string, ttl, interval time.Duration) {
	if strings.TrimSpace(dir) == "" {
		dir = s.StagingDir()
	}
	if ttl <= 0 {
		ttl = s.stagingTTL
	}
	if interval <= 0 {
		interval = s.stagingGCInterval
	}
	if _, err := s.PruneStaging(dir, ttl); err != nil {
		s.log.WarnContext(ctx, "mediagen: staging GC prune failed", "error", err)
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := s.PruneStaging(dir, ttl); err != nil {
				s.log.WarnContext(ctx, "mediagen: staging GC prune failed", "error", err)
			}
		}
	}
}

// StartStagingGCAuto runs staging GC with configured defaults on the service.
func (s *Service) StartStagingGCAuto(ctx context.Context) {
	s.StartStagingGC(ctx, "", s.stagingTTL, s.stagingGCInterval)
}
