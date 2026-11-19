package server

import (
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestServerLifecycleAndValidation(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok")
	})

	if _, err := New(Options{}); err == nil {
		t.Fatal("expected incomplete Options to fail New")
	}

	srv, err := New(Options{
		Host:              "0.0.0.0",
		Port:              0,
		ReadHeaderTimeout: 2 * time.Second,
		ReadTimeout:       5 * time.Second,
		WriteTimeout:      5 * time.Second,
		IdleTimeout:       10 * time.Second,
		Handler:           handler,
		Logger:            logger,
	})
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	if srv.Port() != 0 {
		t.Fatalf("expected Port()==0 before Start, got %d", srv.Port())
	}
	// Stop before Start is a safe no-op.
	if err := srv.Stop(); err != nil {
		t.Fatalf("Stop before Start: %v", err)
	}

	if err := srv.Start(); err != nil {
		t.Fatalf("server.Start: %v", err)
	}
	defer func() { _ = srv.Stop() }()

	if srv.Port() <= 0 {
		t.Fatalf("expected positive bound port, got %d", srv.Port())
	}
	wsURL := srv.WSURL()
	if !strings.HasPrefix(wsURL, "ws://127.0.0.1:") {
		t.Fatalf("expected 0.0.0.0 to map to loopback WSURL, got %q", wsURL)
	}

	httpURL := "http://127.0.0.1:" + wsURL[len("ws://127.0.0.1:"):len(wsURL)-len(PathWS)]
	res, err := http.Get(httpURL)
	if err != nil {
		t.Fatalf("GET %s: %v", httpURL, err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", res.StatusCode)
	}

	// Second listener on the exact same port must fail instead of shifting ports silently.
	srv2, err := New(Options{
		Host:              "0.0.0.0",
		Port:              srv.Port(),
		ReadHeaderTimeout: 2 * time.Second,
		IdleTimeout:       10 * time.Second,
		Handler:           handler,
		Logger:            logger,
	})
	if err != nil {
		t.Fatalf("srv2 New: %v", err)
	}
	if err := srv2.Start(); err == nil {
		_ = srv2.Stop()
		t.Fatal("expected busy port Start() to fail")
	}

	if err := srv.Stop(); err != nil {
		t.Fatalf("srv.Stop: %v", err)
	}
	select {
	case _, ok := <-srv.Errors():
		if ok {
			t.Fatal("expected clean Stop not to emit fatal error")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Errors() channel close after Stop")
	}
}
