package binaries

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"runtime"
	"testing"
	"time"

	"app/internal/apperr"
	domainbinaries "app/internal/domain/binaries"
)

func pointFFmpegAt(t *testing.T, url string) {
	t.Helper()
	orig := domainbinaries.Registry[domainbinaries.IDFFmpeg]
	spec := orig
	spec.Downloads = map[string]domainbinaries.DownloadSpec{
		fmt.Sprintf("%s_%s", runtime.GOOS, runtime.GOARCH): {URL: url, ArchiveFormat: domainbinaries.ArchiveFormatZip, ExtractFile: "bin/ffmpeg.exe"},
	}
	domainbinaries.Registry[domainbinaries.IDFFmpeg] = spec
	t.Cleanup(func() { domainbinaries.Registry[domainbinaries.IDFFmpeg] = orig })
}

// Network failures used to surface as a generic ERR_INTERNAL ("internal error").
func TestDownloadNetworkErrorsAreUserFacing(t *testing.T) {
	svc := New(newMemoryRepo(), t.TempDir(), slog.New(slog.DiscardHandler))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	pointFFmpegAt(t, srv.URL+"/ffmpeg.zip")
	_, err := svc.Download(context.Background(), domainbinaries.IDFFmpeg, nil)
	if !apperr.IsCode(err, apperr.CodeUnavailable) {
		t.Fatalf("bad status: want ERR_UNAVAILABLE, got %v", err)
	}
	srv.Close()

	// The server is gone now: connection refused.
	_, err = svc.Download(context.Background(), domainbinaries.IDFFmpeg, nil)
	if !apperr.IsCode(err, apperr.CodeUnavailable) {
		t.Fatalf("unreachable: want ERR_UNAVAILABLE, got %v", err)
	}
}

// A second download waiting for the first must give up when its request is cancelled,
// instead of blocking on a mutex until the first finishes.
func TestQueuedDownloadHonoursCancellation(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1000")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(release)
	pointFFmpegAt(t, srv.URL+"/ffmpeg.zip")
	svc := New(newMemoryRepo(), t.TempDir(), slog.New(slog.DiscardHandler))

	firstCtx, cancelFirst := context.WithCancel(context.Background())
	defer cancelFirst()
	go func() { _, _ = svc.Download(firstCtx, domainbinaries.IDFFmpeg, nil) }()
	time.Sleep(200 * time.Millisecond) // first download now holds the slot

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := svc.Download(ctx, domainbinaries.IDFFmpeg, nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want context deadline, got %v", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("queued download ignored cancellation for %v", d)
	}
}
