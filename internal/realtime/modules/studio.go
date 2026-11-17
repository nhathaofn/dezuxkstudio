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
	// LockHostAdmin disables studio:open_export_folder in headless server mode unless server.allow_host_admin is enabled.
	LockHostAdmin bool
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
			if err := s.Service.DeleteProject(engine.WithFileGuard(ctx, s.FileGuard), p); err != nil {
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
			return s.Service.SaveScript(engine.WithFileGuard(ctx, s.FileGuard), p)
		})

	realtime.Handle(r, contracts.ActionStudioAssetUpsert,
		func(ctx context.Context, _ *realtime.Request, p contracts.StudioAssetUpsertPayload) (contracts.StudioAssetUpsertResult, error) {
			return s.Service.UpsertAsset(engine.WithFileGuard(ctx, s.FileGuard), p)
		})

	realtime.Handle(r, contracts.ActionStudioShotUpsert,
		func(ctx context.Context, _ *realtime.Request, p contracts.StudioShotUpsertPayload) (contracts.StudioShotUpsertResult, error) {
			return s.Service.UpsertShot(engine.WithFileGuard(ctx, s.FileGuard), p)
		})

	realtime.Handle(r, contracts.ActionStudioShotReorder,
		func(ctx context.Context, _ *realtime.Request, p contracts.StudioShotReorderPayload) (contracts.StudioShotListResult, error) {
			return s.Service.ReorderShots(ctx, p)
		})

	realtime.Handle(r, contracts.ActionStudioEpisodeRunStage,
		func(ctx context.Context, _ *realtime.Request, p contracts.StudioEpisodeRunStagePayload) (contracts.StudioRunStageResult, error) {
			return s.Service.RunStage(engine.WithFileGuard(ctx, s.FileGuard), p)
		})

	realtime.Handle(r, contracts.ActionStudioQueueEnqueue,
		func(ctx context.Context, _ *realtime.Request, p contracts.StudioQueueEnqueuePayload) (contracts.StudioQueueEnqueueResult, error) {
			return s.Service.EnqueueBatch(engine.WithFileGuard(ctx, s.FileGuard), p)
		})

	realtime.Handle(r, contracts.ActionStudioQueueList,
		func(ctx context.Context, _ *realtime.Request, p contracts.StudioQueueListPayload) (contracts.StudioQueueListResult, error) {
			return s.Service.ListJobs(ctx, p)
		})

	realtime.Handle(r, contracts.ActionStudioQueueControl,
		func(ctx context.Context, _ *realtime.Request, p contracts.StudioQueueControlPayload) (contracts.StudioQueueControlResult, error) {
			return s.Service.ControlQueue(ctx, p)
		})

	realtime.Handle(r, contracts.ActionStudioCostSummary,
		func(ctx context.Context, _ *realtime.Request, p contracts.StudioCostSummaryPayload) (contracts.StudioCostSummaryResult, error) {
			return s.Service.GetCostSummary(ctx, p)
		})

	realtime.Handle(r, contracts.ActionStudioPricingUpsert,
		func(ctx context.Context, _ *realtime.Request, p contracts.StudioPricingUpsertPayload) (contracts.StudioPricingEntry, error) {
			return s.Service.UpsertPricing(ctx, p)
		})

	realtime.Handle(r, contracts.ActionStudioVersionsList,
		func(ctx context.Context, _ *realtime.Request, p contracts.StudioVersionsListPayload) (contracts.StudioVersionsListResult, error) {
			return s.Service.ListVersions(ctx, p)
		})

	realtime.Handle(r, contracts.ActionStudioVersionSelect,
		func(ctx context.Context, _ *realtime.Request, p contracts.StudioVersionSelectPayload) (contracts.StudioVersionSelectResult, error) {
			return s.Service.SelectVersion(engine.WithFileGuard(ctx, s.FileGuard), p)
		})

	realtime.Handle(r, contracts.ActionStudioGridSplit,
		func(ctx context.Context, _ *realtime.Request, p contracts.StudioGridSplitPayload) (contracts.StudioGridSplitResult, error) {
			return s.Service.SplitGrid(engine.WithFileGuard(ctx, s.FileGuard), p)
		})

	realtime.Handle(r, contracts.ActionStudioMaskEdit,
		func(ctx context.Context, _ *realtime.Request, p contracts.StudioMaskEditPayload) (contracts.StudioMaskEditResult, error) {
			return s.Service.ApplyMaskEdit(engine.WithFileGuard(ctx, s.FileGuard), p)
		})

	realtime.Handle(r, contracts.ActionStudioExtractLastFrame,
		func(ctx context.Context, _ *realtime.Request, p contracts.StudioExtractLastFramePayload) (contracts.StudioExtractLastFrameResult, error) {
			return s.Service.ExtractLastFrame(engine.WithFileGuard(ctx, s.FileGuard), p)
		})

	realtime.Handle(r, contracts.ActionStudioQCReview,
		func(ctx context.Context, _ *realtime.Request, p contracts.StudioQCReviewPayload) (contracts.StudioQCReport, error) {
			return s.Service.RunQCReview(engine.WithFileGuard(ctx, s.FileGuard), p)
		})

	realtime.Handle(r, contracts.ActionStudioBatchPreflight,
		func(ctx context.Context, _ *realtime.Request, p contracts.StudioBatchPreflightPayload) (contracts.BatchPreflightOutput, error) {
			return s.Service.RunBatchPreflight(engine.WithFileGuard(ctx, s.FileGuard), p)
		})

	realtime.Handle(r, contracts.ActionStudioTimelineGet,
		func(ctx context.Context, _ *realtime.Request, p contracts.StudioIDPayload) (contracts.StudioTimelineResult, error) {
			if err := p.Validate(); err != nil {
				return contracts.StudioTimelineResult{}, err
			}
			return s.Service.GetTimeline(engine.WithFileGuard(ctx, s.FileGuard), p.ID)
		})

	realtime.Handle(r, contracts.ActionStudioTimelineApplyOps,
		func(ctx context.Context, _ *realtime.Request, p contracts.StudioTimelineApplyOpsPayload) (contracts.StudioTimelineResult, error) {
			return s.Service.ApplyTimelineOps(engine.WithFileGuard(ctx, s.FileGuard), p)
		})

	realtime.Handle(r, contracts.ActionStudioTimelineUndo,
		func(ctx context.Context, _ *realtime.Request, p contracts.StudioTimelineRevisionNavPayload) (contracts.StudioTimelineResult, error) {
			return s.Service.UndoTimeline(engine.WithFileGuard(ctx, s.FileGuard), p)
		})

	realtime.Handle(r, contracts.ActionStudioTimelineRedo,
		func(ctx context.Context, _ *realtime.Request, p contracts.StudioTimelineRevisionNavPayload) (contracts.StudioTimelineResult, error) {
			return s.Service.RedoTimeline(engine.WithFileGuard(ctx, s.FileGuard), p)
		})

	realtime.Handle(r, contracts.ActionStudioTimelineAutoAssemble,
		func(ctx context.Context, _ *realtime.Request, p contracts.StudioTimelineAutoAssemblePayload) (contracts.StudioTimelineResult, error) {
			return s.Service.AutoAssembleTimeline(engine.WithFileGuard(ctx, s.FileGuard), p)
		})

	realtime.Handle(r, contracts.ActionStudioMediaPeaksThumbs,
		func(ctx context.Context, _ *realtime.Request, p contracts.StudioMediaPeaksThumbsPayload) (contracts.MediaPeaksThumbsOutput, error) {
			return s.Service.ComputeMediaPeaksThumbs(engine.WithFileGuard(ctx, s.FileGuard), p)
		})

	realtime.Handle(r, contracts.ActionStudioTimelineRender,
		func(ctx context.Context, _ *realtime.Request, p contracts.StudioTimelineRenderPayload) (contracts.StudioTimelineRenderResult, error) {
			return s.Service.RenderTimeline(engine.WithFileGuard(ctx, s.FileGuard), p)
		})

	realtime.Handle(r, contracts.ActionStudioTimelineExportNLE,
		func(ctx context.Context, _ *realtime.Request, p contracts.StudioTimelineExportNLEPayload) (contracts.ExportNLEDraftOutput, error) {
			return s.Service.ExportNLEDraft(engine.WithFileGuard(ctx, s.FileGuard), p)
		})

	realtime.Handle(r, contracts.ActionStudioOpenExportFolder,
		func(ctx context.Context, _ *realtime.Request, p contracts.StudioOpenExportFolderPayload) (contracts.Empty, error) {
			if s.LockHostAdmin {
				return contracts.Empty{}, hostAdminLockedError()
			}
			return contracts.Empty{}, s.Service.OpenExportFolder(engine.WithFileGuard(ctx, s.FileGuard), p)
		})
}
