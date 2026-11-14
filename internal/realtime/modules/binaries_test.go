package modules_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"app/internal/apperr"
	"app/internal/contracts"
	domainbinaries "app/internal/domain/binaries"
	"app/internal/realtime"
	"app/internal/realtime/modules"
	binariessvc "app/internal/service/binaries"
)

type stubBinariesRepo struct {
	paths map[domainbinaries.BinaryID]string
}

func (r *stubBinariesRepo) GetCustomPath(_ context.Context, id domainbinaries.BinaryID) (string, error) {
	return r.paths[id], nil
}

func (r *stubBinariesRepo) GetAllCustomPaths(_ context.Context) (map[domainbinaries.BinaryID]string, error) {
	cp := make(map[domainbinaries.BinaryID]string, len(r.paths))
	for k, v := range r.paths {
		cp[k] = v
	}
	return cp, nil
}

func (r *stubBinariesRepo) SetCustomPath(_ context.Context, id domainbinaries.BinaryID, customPath string) error {
	if r.paths == nil {
		r.paths = make(map[domainbinaries.BinaryID]string)
	}
	r.paths[id] = customPath
	return nil
}

func TestBinariesModule_SetPathGuard(t *testing.T) {
	t.Run("locked host admin refuses binaries:set_path without mutating repo", func(t *testing.T) {
		repo := &stubBinariesRepo{}
		svc := binariessvc.New(repo, t.TempDir(), slog.New(slog.DiscardHandler))
		reg := realtime.NewRegistry(contracts.ActionNames())
		modules.Binaries{Service: svc, LockHostAdmin: true}.Register(reg)

		hSet, ok := reg.Lookup(contracts.ActionBinariesSetPath)
		if !ok {
			t.Fatalf("%s not registered", contracts.ActionBinariesSetPath)
		}
		_, err := hSet(context.Background(), &realtime.Request{}, []byte(`{"id":"ffmpeg","custom_path":"C:\\custom\\ffmpeg.exe"}`))
		var ae *apperr.Error
		if !errors.As(err, &ae) || ae.Code != apperr.CodeUnavailable {
			t.Fatalf("expected ERR_UNAVAILABLE, got %v", err)
		}
		wantMsg := "host path and desktop folder actions are disabled in server mode"
		if ae.Message != wantMsg {
			t.Fatalf("expected message %q, got %q", wantMsg, ae.Message)
		}
		if got := repo.paths[domainbinaries.IDFFmpeg]; got != "" {
			t.Fatalf("expected repo to remain unchanged, got %q", got)
		}

		// Read-only binaries:probe stays available when LockHostAdmin is true.
		hProbe, ok := reg.Lookup(contracts.ActionBinariesProbe)
		if !ok {
			t.Fatalf("%s not registered", contracts.ActionBinariesProbe)
		}
		res, err := hProbe(context.Background(), &realtime.Request{}, []byte(`{"id":"ffmpeg"}`))
		if err != nil {
			t.Fatalf("binaries:probe failed: %v", err)
		}
		info, ok := res.(contracts.BinaryInfo)
		if !ok || info.ID != domainbinaries.IDFFmpeg {
			t.Fatalf("unexpected binaries:probe result: %#v", res)
		}
	})

	t.Run("unlocked host admin allows binaries:set_path", func(t *testing.T) {
		repo := &stubBinariesRepo{}
		svc := binariessvc.New(repo, t.TempDir(), slog.New(slog.DiscardHandler))
		reg := realtime.NewRegistry(contracts.ActionNames())
		modules.Binaries{Service: svc, LockHostAdmin: false}.Register(reg)

		hSet, ok := reg.Lookup(contracts.ActionBinariesSetPath)
		if !ok {
			t.Fatalf("%s not registered", contracts.ActionBinariesSetPath)
		}
		res, err := hSet(context.Background(), &realtime.Request{}, []byte(`{"id":"ffmpeg","custom_path":"C:\\custom\\ffmpeg.exe"}`))
		if err != nil {
			t.Fatalf("expected binaries:set_path to succeed, got %v", err)
		}
		info, ok := res.(contracts.BinaryInfo)
		if !ok || info.CustomPath != `C:\custom\ffmpeg.exe` {
			t.Fatalf("unexpected binary info: %#v", res)
		}
		if repo.paths[domainbinaries.IDFFmpeg] != `C:\custom\ffmpeg.exe` {
			t.Fatalf("expected custom path persisted in repo, got %q", repo.paths[domainbinaries.IDFFmpeg])
		}
	})
}
