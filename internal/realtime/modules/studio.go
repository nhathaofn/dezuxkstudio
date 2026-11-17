package modules

import (
	"context"

	"app/internal/contracts"
	"app/internal/engine"
	"app/internal/realtime"
	studiosvc "app/internal/service/studio"
)

// Studio exposes studio:* realtime actions.
type Studio struct {
	Service *studiosvc.Service
	// FileGuard, when set (headless server mode), constrains node file access during RunStage.
	FileGuard engine.FileGuard
}

func (Studio) Name() string { return "studio" }

func (s Studio) Register(r *realtime.Registry) {
	realtime.Handle(r, contracts.ActionStudioProjectList,
		func(ctx context.Context, _ *realtime.Request, _ contracts.Empty) (contracts.StudioProjectListResult, error) {
			return s.Service.ListProjects(ctx)
		})

	realtime.Handle(r, contracts.ActionStudioProjectCreate,
		func(ctx context.Context, _ *realtime.Request, p contracts.StudioProjectCreatePayload) (contracts.StudioProjectDetailResult, error) {
			return s.Service.CreateProject(ctx, p)
		})

	realtime.Handle(r, contracts.ActionStudioProjectGet,
		func(ctx context.Context, _ *realtime.Request, p contracts.StudioIDPayload) (contracts.StudioProjectDetailResult, error) {
			if err := p.Validate(); err != nil {
				return contracts.StudioProjectDetailResult{}, err
			}
			return s.Service.GetProject(ctx, p.ID)
		})

	realtime.Handle(r, contracts.ActionStudioProjectUpdate,
		func(ctx context.Context, _ *realtime.Request, p contracts.StudioProjectUpdatePayload) (contracts.StudioProjectDetailResult, error) {
			return s.Service.UpdateProject(ctx, p)
		})

	realtime.Handle(r, contracts.ActionStudioProjectDelete,
		func(ctx context.Context, _ *realtime.Request, p contracts.StudioProjectDeletePayload) (contracts.Empty, error) {
			if err := s.Service.DeleteProject(ctx, p); err != nil {
				return contracts.Empty{}, err
			}
			return contracts.Empty{}, nil
		})

	realtime.Handle(r, contracts.ActionStudioEpisodeCreate,
		func(ctx context.Context, _ *realtime.Request, p contracts.StudioEpisodeCreatePayload) (contracts.StudioEpisodeDetailResult, error) {
			return s.Service.CreateEpisode(ctx, p)
		})

	realtime.Handle(r, contracts.ActionStudioEpisodeGet,
		func(ctx context.Context, _ *realtime.Request, p contracts.StudioIDPayload) (contracts.StudioEpisodeDetailResult, error) {
			if err := p.Validate(); err != nil {
				return contracts.StudioEpisodeDetailResult{}, err
			}
			return s.Service.GetEpisode(ctx, p.ID)
		})

	realtime.Handle(r, contracts.ActionStudioEpisodeSaveScript,
		func(ctx context.Context, _ *realtime.Request, p contracts.StudioEpisodeSaveScriptPayload) (contracts.StudioEpisodeDetailResult, error) {
			return s.Service.SaveScript(ctx, p)
		})

	realtime.Handle(r, contracts.ActionStudioAssetUpsert,
		func(ctx context.Context, _ *realtime.Request, p contracts.StudioAssetUpsertPayload) (contracts.StudioAssetUpsertResult, error) {
			return s.Service.UpsertAsset(ctx, p)
		})

	realtime.Handle(r, contracts.ActionStudioShotUpsert,
		func(ctx context.Context, _ *realtime.Request, p contracts.StudioShotUpsertPayload) (contracts.StudioShotUpsertResult, error) {
			return s.Service.UpsertShot(ctx, p)
		})

	realtime.Handle(r, contracts.ActionStudioShotReorder,
		func(ctx context.Context, _ *realtime.Request, p contracts.StudioShotReorderPayload) (contracts.StudioShotListResult, error) {
			return s.Service.ReorderShots(ctx, p)
		})

	realtime.Handle(r, contracts.ActionStudioEpisodeRunStage,
		func(ctx context.Context, _ *realtime.Request, p contracts.StudioEpisodeRunStagePayload) (contracts.StudioRunStageResult, error) {
			return s.Service.RunStage(engine.WithFileGuard(ctx, s.FileGuard), p)
		})
}
