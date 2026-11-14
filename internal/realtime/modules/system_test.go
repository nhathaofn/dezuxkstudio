package modules_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"app/internal/apperr"
	"app/internal/config"
	"app/internal/contracts"
	"app/internal/paths"
	"app/internal/realtime"
	"app/internal/realtime/modules"
	"app/internal/service/system"
)

func TestSystemModule_OpenFolderGuard(t *testing.T) {
	base := t.TempDir()
	p := paths.Paths{
		Config: filepath.Join(base, "config"),
		Data:   filepath.Join(base, "data"),
		Logs:   filepath.Join(base, "logs"),
	}

	t.Run("locked host admin refuses open_folder without invoking opener", func(t *testing.T) {
		called := false
		svc := system.New(config.ModeServer, p, func(string) error {
			called = true
			return nil
		})
		reg := realtime.NewRegistry(contracts.ActionNames())
		modules.System{Info: svc, LockHostAdmin: true}.Register(reg)

		hOpen, ok := reg.Lookup(contracts.ActionSystemOpenFolder)
		if !ok {
			t.Fatalf("%s not registered", contracts.ActionSystemOpenFolder)
		}
		_, err := hOpen(context.Background(), &realtime.Request{}, []byte(`{"target":"logs"}`))
		var ae *apperr.Error
		if !errors.As(err, &ae) || ae.Code != apperr.CodeUnavailable {
			t.Fatalf("expected ERR_UNAVAILABLE, got %v", err)
		}
		wantMsg := "host path and desktop folder actions are disabled in server mode"
		if ae.Message != wantMsg {
			t.Fatalf("expected message %q, got %q", wantMsg, ae.Message)
		}
		if called {
			t.Fatal("open callback must not be invoked when LockHostAdmin is true")
		}

		// Read-only system:info stays available when LockHostAdmin is true.
		hInfo, ok := reg.Lookup(contracts.ActionSystemInfo)
		if !ok {
			t.Fatalf("%s not registered", contracts.ActionSystemInfo)
		}
		res, err := hInfo(context.Background(), &realtime.Request{}, []byte(`{}`))
		if err != nil {
			t.Fatalf("system:info failed: %v", err)
		}
		info, ok := res.(contracts.SystemInfo)
		if !ok || info.Mode != config.ModeServer {
			t.Fatalf("unexpected system:info result: %#v", res)
		}
	})

	t.Run("unlocked host admin forwards open_folder", func(t *testing.T) {
		var opened string
		svc := system.New(config.ModeServer, p, func(dir string) error {
			opened = dir
			return nil
		})
		reg := realtime.NewRegistry(contracts.ActionNames())
		modules.System{Info: svc, LockHostAdmin: false}.Register(reg)

		hOpen, ok := reg.Lookup(contracts.ActionSystemOpenFolder)
		if !ok {
			t.Fatalf("%s not registered", contracts.ActionSystemOpenFolder)
		}
		if _, err := hOpen(context.Background(), &realtime.Request{}, []byte(`{"target":"logs"}`)); err != nil {
			t.Fatalf("expected open_folder to succeed when unlocked, got %v", err)
		}
		if opened != p.Logs {
			t.Fatalf("expected opened dir %q, got %q", p.Logs, opened)
		}
	})
}
