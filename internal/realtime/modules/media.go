package modules

import (
	"context"

	"app/internal/contracts"
	"app/internal/engine"
	"app/internal/realtime"
	mediasvc "app/internal/service/media"
)

// Media exposes media:* realtime actions.
type Media struct {
	Service *mediasvc.Service
	// FileGuard, when set (headless server mode), constrains local file registration and deletion.
	FileGuard engine.FileGuard
}

func (Media) Name() string { return "media" }

func (m Media) Register(r *realtime.Registry) {
	realtime.Handle(r, contracts.ActionMediaList,
		func(ctx context.Context, _ *realtime.Request, p contracts.MediaListPayload) (contracts.MediaListResult, error) {
			return m.Service.List(ctx, p)
		})

	realtime.Handle(r, contracts.ActionMediaDelete,
		func(ctx context.Context, _ *realtime.Request, p contracts.MediaDeletePayload) (contracts.Empty, error) {
			if err := m.Service.Delete(engine.WithFileGuard(ctx, m.FileGuard), p.ID, p.DeleteFile); err != nil {
				return contracts.Empty{}, err
			}
			return contracts.Empty{}, nil
		})

	realtime.Handle(r, contracts.ActionMediaSignURL,
		func(ctx context.Context, _ *realtime.Request, p contracts.MediaSignURLPayload) (contracts.MediaSignedURLResult, error) {
			return m.Service.SignAssetURL(ctx, p)
		})

	realtime.Handle(r, contracts.ActionMediaRegisterInput,
		func(ctx context.Context, _ *realtime.Request, p contracts.MediaRegisterInputPayload) (contracts.MediaAssetItem, error) {
			return m.Service.RegisterInput(engine.WithFileGuard(ctx, m.FileGuard), p)
		})
}
