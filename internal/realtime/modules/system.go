// Package modules adapts services to realtime actions. A module only decodes
// payloads (done by realtime.Handle), calls one service method and maps the result
// to a contract DTO — no business logic lives here.
package modules

import (
	"context"
	"encoding/json"
	"time"

	"app/internal/apperr"
	"app/internal/contracts"
	"app/internal/realtime"
	"app/internal/service/system"
	"app/internal/service/update"
)

func hostAdminLockedError() error {
	return apperr.Unavailable("host path and desktop folder actions are disabled in server mode")
}

// System exposes system:* actions.
type System struct {
	Info   *system.Service
	Update *update.Service
	// LockHostAdmin refuses system:open_folder in server mode unless server.allow_host_admin is enabled.
	LockHostAdmin bool
}

func (System) Name() string { return "system" }

func (m System) Register(r *realtime.Registry) {
	realtime.Handle(r, contracts.ActionSystemPing,
		func(context.Context, *realtime.Request, contracts.Empty) (contracts.PingResult, error) {
			return contracts.PingResult{ServerTime: time.Now().UnixMilli()}, nil
		})

	realtime.Handle(r, contracts.ActionSystemEcho,
		func(_ context.Context, _ *realtime.Request, p json.RawMessage) (json.RawMessage, error) {
			return p, nil
		})

	realtime.Handle(r, contracts.ActionSystemInfo,
		func(context.Context, *realtime.Request, contracts.Empty) (contracts.SystemInfo, error) {
			return m.Info.Info(), nil
		})

	realtime.Handle(r, contracts.ActionSystemOpenFolder,
		func(_ context.Context, _ *realtime.Request, p contracts.OpenFolderPayload) (contracts.Empty, error) {
			if m.LockHostAdmin {
				return contracts.Empty{}, hostAdminLockedError()
			}
			return contracts.Empty{}, m.Info.OpenFolder(p.Target)
		})

	realtime.Handle(r, contracts.ActionSystemCheckUpdate,
		func(ctx context.Context, _ *realtime.Request, _ contracts.Empty) (contracts.UpdateInfo, error) {
			return m.Update.Check(ctx)
		})
}
