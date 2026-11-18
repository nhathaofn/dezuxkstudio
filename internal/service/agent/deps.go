package agent

import (
	"context"
	"encoding/json"

	"app/internal/contracts"
	domainagent "app/internal/domain/agent"
	domainbinaries "app/internal/domain/binaries"
	domainprefs "app/internal/domain/preferences"
	"app/internal/engine"
)

// WorkflowRunner exposes registered workflow node specs and execution to DezuxkStudio's MCP server.
type WorkflowRunner interface {
	Specs(ctx context.Context) []engine.Spec
	RunNode(ctx context.Context, nodeType string, input json.RawMessage) (any, error)
	RunGraph(ctx context.Context, g engine.Graph) (engine.GraphResult, error)
}

// BinariesManager exposes external binary inspection and installation to DezuxkStudio's MCP server.
type BinariesManager interface {
	List(ctx context.Context) ([]domainbinaries.BinaryInfo, error)
	Probe(ctx context.Context, id domainbinaries.BinaryID) (domainbinaries.BinaryInfo, error)
	Download(ctx context.Context, id domainbinaries.BinaryID) (domainbinaries.BinaryInfo, error)
}

// ManagedCLIs is the app's own, self-updating copy of each agent CLI. It never reads or changes a CLI
// that is installed elsewhere on the machine.
type ManagedCLIs interface {
	Supported(id domainagent.ProviderID) bool
	Current(id domainagent.ProviderID) (domainagent.ManagedCopy, bool)
	// Install makes the newest official release current; changed is false when it already was.
	Install(ctx context.Context, id domainagent.ProviderID) (cur domainagent.ManagedCopy, changed bool, err error)
}

// PreferencesManager exposes app preferences read/update to DezuxkStudio's MCP server.
type PreferencesManager interface {
	Get(ctx context.Context) (domainprefs.Preferences, error)
	Update(ctx context.Context, patch domainprefs.Patch) (domainprefs.Preferences, error)
}

// SystemInspector exposes app runtime/path metadata to DezuxkStudio's MCP server.
type SystemInspector interface {
	Info() contracts.SystemInfo
}

// UpdateChecker checks for newer official DezuxkStudio releases.
type UpdateChecker interface {
	Check(ctx context.Context) (contracts.UpdateInfo, error)
}

// MediaManager exposes registered media library listing, deletion, and input registration to MCP tools.
type MediaManager interface {
	List(ctx context.Context, p contracts.MediaListPayload) (contracts.MediaListResult, error)
	Delete(ctx context.Context, id string, deleteFile bool) error
	RegisterInput(ctx context.Context, p contracts.MediaRegisterInputPayload) (contracts.MediaAssetItem, error)
}

// DiagnosticsExporter exposes diagnostic bundle export to DezuxkStudio's MCP server.
type DiagnosticsExporter interface {
	Export(ctx context.Context) (contracts.DiagnosticsBundle, error)
}

// StudioManager exposes AI film studio projects, episodes, assets, shots, workspace sync, and stage execution.
type StudioManager interface {
	ListProjects(ctx context.Context) (contracts.StudioProjectListResult, error)
	CreateProject(ctx context.Context, p contracts.StudioProjectCreatePayload) (contracts.StudioProjectDetailResult, error)
	GetProject(ctx context.Context, idOrSlug string) (contracts.StudioProjectDetailResult, error)
	CreateEpisode(ctx context.Context, p contracts.StudioEpisodeCreatePayload) (contracts.StudioEpisodeDetailResult, error)
	GetEpisode(ctx context.Context, episodeID string) (contracts.StudioEpisodeDetailResult, error)
	SaveScript(ctx context.Context, p contracts.StudioEpisodeSaveScriptPayload) (contracts.StudioEpisodeDetailResult, error)
	UpsertAsset(ctx context.Context, p contracts.StudioAssetUpsertPayload) (contracts.StudioAssetUpsertResult, error)
	UpsertShot(ctx context.Context, p contracts.StudioShotUpsertPayload) (contracts.StudioShotUpsertResult, error)
	SyncWorkspace(ctx context.Context, projectID string) (*contracts.StudioProjectDetailResult, error)
	RunStage(ctx context.Context, p contracts.StudioEpisodeRunStagePayload) (contracts.StudioRunStageResult, error)
}

// StudioPhase2Manager exposes durable queue, billing ledger, media versions, and QC controls.
type StudioPhase2Manager interface {
	EnqueueBatch(ctx context.Context, p contracts.StudioQueueEnqueuePayload) (contracts.StudioQueueEnqueueResult, error)
	ListJobs(ctx context.Context, p contracts.StudioQueueListPayload) (contracts.StudioQueueListResult, error)
	ControlQueue(ctx context.Context, p contracts.StudioQueueControlPayload) (contracts.StudioQueueControlResult, error)
	GetCostSummary(ctx context.Context, p contracts.StudioCostSummaryPayload) (contracts.StudioCostSummaryResult, error)
	ListVersions(ctx context.Context, p contracts.StudioVersionsListPayload) (contracts.StudioVersionsListResult, error)
	SelectVersion(ctx context.Context, p contracts.StudioVersionSelectPayload) (contracts.StudioVersionSelectResult, error)
	RunQCReview(ctx context.Context, p contracts.StudioQCReviewPayload) (contracts.StudioQCReport, error)
	RunBatchPreflight(ctx context.Context, p contracts.StudioBatchPreflightPayload) (contracts.BatchPreflightOutput, error)
}

// StudioPhase3Manager exposes multi-track EditTimeline read and revision-checked mutations to MCP tools.
type StudioPhase3Manager interface {
	GetTimeline(ctx context.Context, episodeID string) (contracts.StudioTimelineResult, error)
	ApplyTimelineOps(ctx context.Context, p contracts.StudioTimelineApplyOpsPayload) (contracts.StudioTimelineResult, error)
	UndoTimeline(ctx context.Context, p contracts.StudioTimelineRevisionNavPayload) (contracts.StudioTimelineResult, error)
	RedoTimeline(ctx context.Context, p contracts.StudioTimelineRevisionNavPayload) (contracts.StudioTimelineResult, error)
}

// StudioExtendedManager exposes full UI-parity Studio operations (project updates, shot reorder,
// pricing catalog updates, grid split, mask edit, continuity frame extraction, timeline assemble/peaks/render/NLE export).
type StudioExtendedManager interface {
	ProjectsDir() string
	UpdateProject(ctx context.Context, p contracts.StudioProjectUpdatePayload) (contracts.StudioProjectDetailResult, error)
	DeleteProject(ctx context.Context, p contracts.StudioProjectDeletePayload) error
	ReorderShots(ctx context.Context, p contracts.StudioShotReorderPayload) (contracts.StudioShotListResult, error)
	UpsertPricing(ctx context.Context, p contracts.StudioPricingUpsertPayload) (contracts.StudioPricingEntry, error)
	SplitGrid(ctx context.Context, p contracts.StudioGridSplitPayload) (contracts.StudioGridSplitResult, error)
	ApplyMaskEdit(ctx context.Context, p contracts.StudioMaskEditPayload) (contracts.StudioMaskEditResult, error)
	ExtractLastFrame(ctx context.Context, p contracts.StudioExtractLastFramePayload) (contracts.StudioExtractLastFrameResult, error)
	AutoAssembleTimeline(ctx context.Context, p contracts.StudioTimelineAutoAssemblePayload) (contracts.StudioTimelineResult, error)
	ComputeMediaPeaksThumbs(ctx context.Context, p contracts.StudioMediaPeaksThumbsPayload) (contracts.MediaPeaksThumbsOutput, error)
	RenderTimeline(ctx context.Context, p contracts.StudioTimelineRenderPayload) (contracts.StudioTimelineRenderResult, error)
	ExportNLEDraft(ctx context.Context, p contracts.StudioTimelineExportNLEPayload) (contracts.ExportNLEDraftOutput, error)
}
