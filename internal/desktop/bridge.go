// Package desktop hosts the Wails shell: window options, the frontend bridge, the
// dynamic Content-Security-Policy and the core lifecycle.
package desktop

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"time"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"app/internal/apperr"
	"app/internal/buildinfo"
	"app/internal/config"
	"app/internal/contracts"
	"app/internal/secrets"
)

// Bridge is bound to the frontend by Wails. Every exported method becomes callable
// from JavaScript, so it must only expose the methods declared in contracts.Bridge().
type Bridge struct {
	cfg     *config.Config
	secrets secrets.Store

	ready     chan struct{}
	readyOnce sync.Once
	mu        sync.RWMutex
	info      contracts.ConnectionInfo
	err       error
	wailsCtx  context.Context
}

func newBridge(cfg *config.Config, store secrets.Store) *Bridge {
	return &Bridge{cfg: cfg, secrets: store, ready: make(chan struct{})}
}

// GetConnectionInfo waits until the core is ready and returns how to reach it.
func (b *Bridge) GetConnectionInfo() (contracts.ConnectionInfo, error) {
	timer := time.NewTimer(b.cfg.Desktop.ReadyTimeout)
	defer timer.Stop()
	select {
	case <-b.ready:
	case <-timer.C:
		return contracts.ConnectionInfo{}, apperr.Unavailable("the application core did not start in time")
	}

	b.mu.RLock()
	info, err := b.info, b.err
	b.mu.RUnlock()
	if err != nil {
		return contracts.ConnectionInfo{}, err
	}
	if b.cfg.Mode == config.ModeClient && b.cfg.Client.AuthToken == "" {
		token, err := b.secrets.Get(secrets.KeyRemoteAuthToken)
		if err != nil && !errors.Is(err, secrets.ErrNotFound) {
			return contracts.ConnectionInfo{}, apperr.Wrap(apperr.CodeUnavailable, "could not read the credential store", err)
		}
		info.Token = token
	}
	return info, nil
}

// GetBuildInfo returns build metadata.
func (b *Bridge) GetBuildInfo() contracts.BuildInfo {
	return contracts.NewBuildInfo(buildinfo.Get())
}

// SetRemoteToken stores the client-mode token in the OS credential store.
func (b *Bridge) SetRemoteToken(token string) error {
	if b.cfg.Mode != config.ModeClient {
		return apperr.New(apperr.CodeBadRequest, "remote token is only used in client mode")
	}
	if err := b.secrets.Set(secrets.KeyRemoteAuthToken, token); err != nil {
		return apperr.Wrap(apperr.CodeUnavailable, "could not write the credential store", err)
	}
	return nil
}

// BrowseExecutable opens a native file picker to select an executable file.
func (b *Bridge) BrowseExecutable(title string) (string, error) {
	b.mu.RLock()
	ctx := b.wailsCtx
	b.mu.RUnlock()
	if ctx == nil {
		return "", apperr.Unavailable("desktop window is not ready")
	}
	filters := []wailsruntime.FileFilter{
		{DisplayName: "Executables (*.exe)", Pattern: "*.exe"},
		{DisplayName: "All Files (*.*)", Pattern: "*.*"},
	}
	if runtime.GOOS != "windows" {
		filters = []wailsruntime.FileFilter{
			{DisplayName: "All Files (*)", Pattern: "*"},
		}
	}
	if title == "" {
		title = "Select Executable"
	}
	selected, err := wailsruntime.OpenFileDialog(ctx, wailsruntime.OpenDialogOptions{
		Title:   title,
		Filters: filters,
	})
	if err != nil {
		return "", apperr.Wrap(apperr.CodeInternal, "failed to open file dialog", err)
	}
	return selected, nil
}

// BrowseImage opens a native file picker to select an image file.
func (b *Bridge) BrowseImage(title string) (string, error) {
	b.mu.RLock()
	ctx := b.wailsCtx
	b.mu.RUnlock()
	if ctx == nil {
		return "", apperr.Unavailable("desktop window is not ready")
	}
	filters := []wailsruntime.FileFilter{
		{DisplayName: "Images (*.png;*.jpg;*.jpeg;*.webp)", Pattern: "*.png;*.jpg;*.jpeg;*.webp"},
		{DisplayName: "All Files (*.*)", Pattern: "*.*"},
	}
	if runtime.GOOS != "windows" {
		filters = []wailsruntime.FileFilter{
			{DisplayName: "All Files (*)", Pattern: "*"},
		}
	}
	if title == "" {
		title = "Select Image"
	}
	selected, err := wailsruntime.OpenFileDialog(ctx, wailsruntime.OpenDialogOptions{
		Title:   title,
		Filters: filters,
	})
	if err != nil {
		return "", apperr.Wrap(apperr.CodeInternal, "failed to open file dialog", err)
	}
	return selected, nil
}

// BrowseImages opens a native file picker to select multiple image files.
func (b *Bridge) BrowseImages(title string) ([]string, error) {
	b.mu.RLock()
	ctx := b.wailsCtx
	b.mu.RUnlock()
	if ctx == nil {
		return nil, apperr.Unavailable("desktop window is not ready")
	}
	filters := []wailsruntime.FileFilter{
		{DisplayName: "Images (*.png;*.jpg;*.jpeg;*.webp)", Pattern: "*.png;*.jpg;*.jpeg;*.webp"},
		{DisplayName: "All Files (*.*)", Pattern: "*.*"},
	}
	if runtime.GOOS != "windows" {
		filters = []wailsruntime.FileFilter{
			{DisplayName: "All Files (*)", Pattern: "*"},
		}
	}
	if title == "" {
		title = "Select Images"
	}
	selected, err := wailsruntime.OpenMultipleFilesDialog(ctx, wailsruntime.OpenDialogOptions{
		Title:   title,
		Filters: filters,
	})
	if err != nil {
		return nil, apperr.Wrap(apperr.CodeInternal, "failed to open multiple files dialog", err)
	}
	return selected, nil
}

// BrowseDirectory opens a native directory picker to select a folder.
func (b *Bridge) BrowseDirectory(title string) (string, error) {
	b.mu.RLock()
	ctx := b.wailsCtx
	b.mu.RUnlock()
	if ctx == nil {
		return "", apperr.Unavailable("desktop window is not ready")
	}
	if title == "" {
		title = "Select Folder"
	}
	selected, err := wailsruntime.OpenDirectoryDialog(ctx, wailsruntime.OpenDialogOptions{
		Title: title,
	})
	if err != nil {
		return "", apperr.Wrap(apperr.CodeInternal, "failed to open directory dialog", err)
	}
	return selected, nil
}

func (b *Bridge) setWailsContext(ctx context.Context) {
	b.mu.Lock()
	b.wailsCtx = ctx
	b.mu.Unlock()
}

func (b *Bridge) succeed(info contracts.ConnectionInfo) {
	b.mu.Lock()
	b.info = info
	b.mu.Unlock()
	b.readyOnce.Do(func() { close(b.ready) })
}

func (b *Bridge) fail(err error) {
	b.mu.Lock()
	if b.err == nil {
		b.err = err
	}
	b.mu.Unlock()
	b.readyOnce.Do(func() { close(b.ready) })
}

func connectionInfo(cfg *config.Config, wsURL, token string) contracts.ConnectionInfo {
	return contracts.ConnectionInfo{
		Mode:                  cfg.Mode,
		WSURL:                 wsURL,
		Token:                 token,
		ActionTimeoutMs:       cfg.Realtime.ActionTimeout.Milliseconds(),
		PingIntervalMs:        cfg.Realtime.PingInterval.Milliseconds(),
		CheckUpdatesOnStartup: cfg.Update.CheckOnStartup && cfg.Update.FeedURL != "",
	}
}
