import type {
  ActionProgress,
  BatchPreflightOutput,
  ExportNLEDraftOutput,
  MediaPeaksThumbsOutput,
  ProbeMediaOutput,
  StudioAssetUpsertPayload,
  StudioBatchProgressEvent,
  StudioCostSummaryResult,
  StudioCostUpdatedEvent,
  StudioEpisodeDetailResult,
  StudioEpisodeRunStagePayload,
  StudioEpisodeStage,
  StudioEpisodeUpdatedEvent,
  StudioExtractLastFramePayload,
  StudioGridSplitPayload,
  StudioJob,
  StudioMaskEditPayload,
  StudioMediaTargetKind,
  StudioMediaVersion,
  StudioPricingUpsertPayload,
  StudioProject,
  StudioProjectChangedEvent,
  StudioProjectCreatePayload,
  StudioProjectDetailResult,
  StudioProjectUpdatePayload,
  StudioQCReport,
  StudioQCReviewPayload,
  StudioQueueControlPayload,
  StudioQueueEnqueuePayload,
  StudioQueueUpdatedEvent,
  StudioRunStageAction,
  StudioShot,
  StudioShotUpsertPayload,
  StudioTimelineAutoAssemblePayload,
  StudioTimelineChangedEvent,
  StudioTimelineExportNLEPayload,
  StudioTimelineOp,
  StudioTimelineRenderPayload,
  StudioTimelineRenderResult,
  StudioTimelineResult,
} from "../../contracts/generated";
import type { ActionError } from "../../lib/rtClient";

export type StudioSet = (partial: Partial<StudioState> | ((s: StudioState) => Partial<StudioState>)) => void;
export type StudioGet = () => StudioState;

export interface StudioState {
  projects: StudioProject[];
  projectsDir: string;
  projectDetail: StudioProjectDetailResult | null;
  episodeDetail: StudioEpisodeDetailResult | null;
  activeProjectId: string | null;
  activeEpisodeId: string | null;
  activeStage: StudioEpisodeStage;
  selectedAssetIds: string[];
  loadingProjects: boolean;
  loadingEpisode: boolean;
  needsInitialConnectSync: boolean;
  saving: boolean;
  runningStage: StudioRunStageAction | null;
  actionProgress: ActionProgress | null;
  batchProgress: StudioBatchProgressEvent | null;
  probeMap: Record<string, ProbeMediaOutput>;
  jobs: StudioJob[];
  costSummary: StudioCostSummaryResult | null;
  versionsByTarget: Record<string, StudioMediaVersion[]>;
  qcByTarget: Record<string, StudioQCReport>;
  queueDrawerOpen: boolean;
  versionDrawerTarget: StudioVersionModalTarget | null;
  gridModalTarget: StudioGridModalTarget | null;
  maskModalTarget: StudioMaskModalTarget | null;
  pendingPreflight: StudioPendingPreflight | null;
  timelineResult: StudioTimelineResult | null;
  timelineLoading: boolean;
  renderingTimeline: boolean;
  exportingNLE: boolean;
  lastRenderResult: StudioTimelineRenderResult | null;
  lastNLEExport: ExportNLEDraftOutput | null;
  peaksLoadingClipIds: Record<string, boolean>;
  error: ActionError | null;
  lastSummary: string;

  setActiveStage: (stage: StudioEpisodeStage) => void;
  toggleSelectedAssetId: (assetId: string) => void;
  clearError: () => void;
  clearTransientState: () => void;
  resyncAfterReconnect: () => Promise<void>;

  fetchProjects: () => Promise<void>;
  selectProject: (projectId: string, opts?: { preserveEpisode?: boolean }) => Promise<void>;
  createProject: (payload: StudioProjectCreatePayload) => Promise<boolean>;
  updateProject: (payload: StudioProjectUpdatePayload) => Promise<boolean>;
  deleteProject: (projectId: string, deleteFiles: boolean) => Promise<boolean>;

  selectEpisode: (episodeId: string, opts?: { preserveStage?: boolean }) => Promise<void>;
  createEpisode: (projectId: string, title?: string) => Promise<boolean>;

  saveScript: (rawScript: string) => Promise<boolean>;
  upsertAsset: (payload: StudioAssetUpsertPayload) => Promise<boolean>;
  uploadAssetImage: (assetId: string, file: File) => Promise<boolean>;
  upsertShot: (payload: StudioShotUpsertPayload) => Promise<boolean>;
  uploadShotKeyframe: (shotId: string, file: File) => Promise<boolean>;
  reorderShots: (shotIDs: string[]) => Promise<void>;

  runStage: (
    stage: StudioRunStageAction,
    opts?: Omit<StudioEpisodeRunStagePayload, "episode_id" | "stage">
  ) => Promise<boolean>;
  probeShot: (shot: StudioShot) => Promise<ProbeMediaOutput | null>;
  syncWorkspace: (projectId?: string) => Promise<boolean>;
  runShotDubMux: (payload: {
    shot: StudioShot;
    videoPath?: string;
    audioPath?: string;
    text?: string;
    modelRef?: string;
    voice?: string;
    overflowMode?: string;
    duckOriginal?: boolean;
    originalVolume?: number;
  }) => Promise<boolean>;
  runShotLipSync: (payload: {
    shot: StudioShot;
    videoPath?: string;
    audioPath?: string;
    modelRef?: string;
    faceIndex?: number;
    prompt?: string;
  }) => Promise<boolean>;

  receiveProjectChanged: (evt: StudioProjectChangedEvent) => void;
  receiveEpisodeUpdated: (evt: StudioEpisodeUpdatedEvent) => void;
  receiveBatchProgress: (evt: StudioBatchProgressEvent) => void;
  receiveQueueUpdated: (evt: StudioQueueUpdatedEvent) => void;
  receiveCostUpdated: (evt: StudioCostUpdatedEvent) => void;

  openQueueDrawer: (open: boolean) => void;
  openVersionDrawer: (target: StudioVersionModalTarget | null) => void;
  openGridModal: (target: StudioGridModalTarget | null) => void;
  openMaskModal: (target: StudioMaskModalTarget | null) => void;
  cancelPreflight: () => void;
  confirmPreflight: () => Promise<boolean>;

  fetchQueue: (projectId: string, episodeId?: string) => Promise<void>;
  enqueueBatch: (payload: Omit<StudioQueueEnqueuePayload, "episode_id">) => Promise<boolean>;
  controlQueue: (payload: StudioQueueControlPayload) => Promise<void>;
  fetchCostSummary: (
    projectId: string,
    episodeId?: string,
    opts?: { quiet?: boolean }
  ) => Promise<void>;
  upsertPricing: (payload: StudioPricingUpsertPayload) => Promise<boolean>;
  fetchVersions: (targetKind: StudioMediaTargetKind, targetId: string) => Promise<void>;
  selectVersion: (versionId: string) => Promise<boolean>;
  splitGrid: (payload: StudioGridSplitPayload) => Promise<boolean>;
  applyMaskEdit: (payload: StudioMaskEditPayload) => Promise<boolean>;
  extractLastFrame: (
    payload: Omit<StudioExtractLastFramePayload, "episode_id">
  ) => Promise<boolean>;
  runQCReview: (payload: StudioQCReviewPayload) => Promise<StudioQCReport | null>;
  runBatchPreflight: (
    stage: string,
    model?: string,
    itemCount?: number
  ) => Promise<BatchPreflightOutput | null>;

  fetchTimeline: (
    episodeId?: string,
    opts?: { preserveError?: boolean }
  ) => Promise<StudioTimelineResult | null>;
  applyTimelineOps: (ops: StudioTimelineOp[], summary?: string) => Promise<boolean>;
  undoTimeline: () => Promise<boolean>;
  redoTimeline: () => Promise<boolean>;
  autoAssembleTimeline: (
    payload?: Omit<StudioTimelineAutoAssemblePayload, "episode_id">
  ) => Promise<boolean>;
  computeClipPeaksThumbs: (
    mediaPath: string,
    clipId?: string,
    opts?: { thumb_interval_sec?: number; peak_bins?: number }
  ) => Promise<MediaPeaksThumbsOutput | null>;
  renderTimeline: (
    payload?: Omit<StudioTimelineRenderPayload, "episode_id">
  ) => Promise<StudioTimelineRenderResult | null>;
  exportNLEDraft: (
    payload: Omit<StudioTimelineExportNLEPayload, "episode_id">
  ) => Promise<ExportNLEDraftOutput | null>;
  openExportFolder: (filePath?: string) => Promise<boolean>;
  receiveTimelineChanged: (evt: StudioTimelineChangedEvent) => void;
}

export interface StudioVersionModalTarget {
  projectId: string;
  episodeId?: string;
  targetKind: StudioMediaTargetKind;
  targetId: string;
  label: string;
  mediaPath: string;
  mediaAssetId?: string;
  promptExpected?: string;
  referencePaths?: string[];
}

export interface StudioGridModalTarget {
  projectId: string;
  episodeId?: string;
  targetKind?: StudioMediaTargetKind;
  targetId?: string;
  imagePath: string;
  assetId?: string;
}

export interface StudioMaskModalTarget {
  projectId: string;
  episodeId?: string;
  targetKind: StudioMediaTargetKind;
  targetId: string;
  imagePath: string;
  assetId?: string;
  defaultPrompt?: string;
}

export interface StudioPendingPreflight {
  mode: "stage" | "queue";
  stage: string;
  opts: Record<string, unknown>;
  preflight: BatchPreflightOutput;
}

export function targetVersionKey(targetKind: string, targetId: string): string {
  return `${targetKind}:${targetId}`;
}

const HEAVY_STAGES = new Set<string>([
  "generate_assets",
  "generate_keyframes",
  "generate_videos",
  "generate_tts",
  "lipsync",
] satisfies (StudioRunStageAction | "lipsync")[]);

export function isHeavyStudioStage(stage: string): boolean {
  return HEAVY_STAGES.has(stage);
}
