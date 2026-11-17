import { create } from "zustand";
import {
  Actions,
  NodeTypes,
  type ActionProgress,
  type BatchPreflightOutput,
  type ExportNLEDraftOutput,
  type MediaPeaksThumbsOutput,
  type ProbeMediaOutput,
  type StudioAssetUpsertPayload,
  type StudioBatchProgressEvent,
  type StudioCostSummaryResult,
  type StudioCostUpdatedEvent,
  type StudioEpisodeDetailResult,
  type StudioEpisodeRunStagePayload,
  type StudioEpisodeStage,
  type StudioEpisodeUpdatedEvent,
  type StudioExtractLastFramePayload,
  type StudioGridSplitPayload,
  type StudioJob,
  type StudioMaskEditPayload,
  type StudioMediaVersion,
  type StudioPricingUpsertPayload,
  type StudioProject,
  type StudioProjectChangedEvent,
  type StudioProjectCreatePayload,
  type StudioProjectDetailResult,
  type StudioProjectUpdatePayload,
  type StudioQCReport,
  type StudioQCReviewPayload,
  type StudioQueueControlPayload,
  type StudioQueueEnqueuePayload,
  type StudioQueueUpdatedEvent,
  type StudioRunStageAction,
  type StudioShot,
  type StudioShotUpsertPayload,
  type StudioTimelineAutoAssemblePayload,
  type StudioTimelineChangedEvent,
  type StudioTimelineExportNLEPayload,
  type StudioTimelineOp,
  type StudioTimelineRenderPayload,
  type StudioTimelineRenderResult,
  type StudioTimelineResult,
} from "../../contracts/generated";
import { rtClient, toActionError, uploadMediaFile, type ActionError } from "../../lib/rtClient";

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

  fetchProjects: () => Promise<void>;
  selectProject: (projectId: string) => Promise<void>;
  createProject: (payload: StudioProjectCreatePayload) => Promise<boolean>;
  updateProject: (payload: StudioProjectUpdatePayload) => Promise<boolean>;
  deleteProject: (projectId: string, deleteFiles: boolean) => Promise<boolean>;

  selectEpisode: (episodeId: string) => Promise<void>;
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
  fetchCostSummary: (projectId: string, episodeId?: string) => Promise<void>;
  upsertPricing: (payload: StudioPricingUpsertPayload) => Promise<boolean>;
  fetchVersions: (targetKind: string, targetId: string) => Promise<void>;
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
  targetKind: string;
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
  targetKind?: string;
  targetId?: string;
  imagePath: string;
  assetId?: string;
}

export interface StudioMaskModalTarget {
  projectId: string;
  episodeId?: string;
  targetKind: string;
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

function isHeavyStudioStage(stage: string): boolean {
  return (
    stage === "generate_assets" ||
    stage === "generate_keyframes" ||
    stage === "generate_videos" ||
    stage === "generate_tts" ||
    stage === "lipsync"
  );
}

export const useStudioStore = create<StudioState>((set, get) => ({
  projects: [],
  projectsDir: "",
  projectDetail: null,
  episodeDetail: null,
  activeProjectId: null,
  activeEpisodeId: null,
  activeStage: "script",
  selectedAssetIds: [],
  loadingProjects: false,
  loadingEpisode: false,
  saving: false,
  runningStage: null,
  actionProgress: null,
  batchProgress: null,
  probeMap: {},
  jobs: [],
  costSummary: null,
  versionsByTarget: {},
  qcByTarget: {},
  queueDrawerOpen: false,
  versionDrawerTarget: null,
  gridModalTarget: null,
  maskModalTarget: null,
  pendingPreflight: null,
  timelineResult: null,
  timelineLoading: false,
  renderingTimeline: false,
  exportingNLE: false,
  lastRenderResult: null,
  lastNLEExport: null,
  peaksLoadingClipIds: {},
  error: null,
  lastSummary: "",

  setActiveStage: (stage) => set({ activeStage: stage }),

  toggleSelectedAssetId: (assetId) =>
    set((s) => ({
      selectedAssetIds: s.selectedAssetIds.includes(assetId)
        ? s.selectedAssetIds.filter((id) => id !== assetId)
        : [...s.selectedAssetIds, assetId],
    })),

  clearError: () => set({ error: null }),

  fetchProjects: async () => {
    set({ loadingProjects: true, error: null });
    try {
      const res = await rtClient.request(Actions.STUDIO_PROJECT_LIST, {});
      const list = res.projects ?? [];
      set({ projects: list, projectsDir: res.projects_dir, loadingProjects: false });

      const curId = get().activeProjectId;
      if (curId && list.some((p) => p.id === curId)) {
        if (!get().projectDetail || get().projectDetail?.project.id !== curId) {
          await get().selectProject(curId);
        }
      } else if (list.length > 0) {
        await get().selectProject(list[0].id);
      } else {
        set({
          activeProjectId: null,
          activeEpisodeId: null,
          projectDetail: null,
          episodeDetail: null,
        });
      }
    } catch (err) {
      set({ error: toActionError(err), loadingProjects: false });
    }
  },

  selectProject: async (projectId) => {
    set({ loadingEpisode: true, error: null, activeProjectId: projectId });
    try {
      const detail = await rtClient.request(Actions.STUDIO_PROJECT_GET, { id: projectId });
      const activeEp = detail.active_episode ?? null;
      set({
        projectDetail: detail,
        episodeDetail: activeEp,
        activeProjectId: detail.project.id,
        activeEpisodeId: activeEp?.episode.id ?? detail.episodes[0]?.id ?? null,
        activeStage: activeEp?.episode.stage ?? get().activeStage,
        loadingEpisode: false,
      });
      void get().fetchQueue(detail.project.id, activeEp?.episode.id);
      void get().fetchCostSummary(detail.project.id, activeEp?.episode.id);
    } catch (err) {
      set({ error: toActionError(err), loadingEpisode: false });
    }
  },

  createProject: async (payload) => {
    set({ saving: true, error: null });
    try {
      const detail = await rtClient.request(Actions.STUDIO_PROJECT_CREATE, payload);
      const activeEp = detail.active_episode ?? null;
      set((s) => ({
        projects: [detail.project, ...s.projects.filter((p) => p.id !== detail.project.id)],
        projectDetail: detail,
        episodeDetail: activeEp,
        activeProjectId: detail.project.id,
        activeEpisodeId: activeEp?.episode.id ?? null,
        activeStage: activeEp?.episode.stage ?? "script",
        saving: false,
        lastSummary: `Created project ${detail.project.title}`,
      }));
      return true;
    } catch (err) {
      set({ error: toActionError(err), saving: false });
      return false;
    }
  },

  updateProject: async (payload) => {
    set({ saving: true, error: null });
    try {
      const detail = await rtClient.request(Actions.STUDIO_PROJECT_UPDATE, payload);
      const curEpId = get().activeEpisodeId;
      let nextEp = detail.active_episode ?? get().episodeDetail;
      if (curEpId && nextEp?.episode.id !== curEpId) {
        nextEp = await rtClient.request(Actions.STUDIO_EPISODE_GET, { id: curEpId });
      }
      set((s) => ({
        projects: s.projects.map((p) => (p.id === detail.project.id ? detail.project : p)),
        projectDetail: detail,
        episodeDetail: nextEp,
        saving: false,
      }));
      return true;
    } catch (err) {
      set({ error: toActionError(err), saving: false });
      return false;
    }
  },

  deleteProject: async (projectId, deleteFiles) => {
    set({ saving: true, error: null });
    try {
      await rtClient.request(Actions.STUDIO_PROJECT_DELETE, {
        id: projectId,
        delete_files: deleteFiles,
      });
      set((s) => ({
        projects: s.projects.filter((p) => p.id !== projectId),
        activeProjectId: s.activeProjectId === projectId ? null : s.activeProjectId,
        saving: false,
      }));
      await get().fetchProjects();
      return true;
    } catch (err) {
      set({ error: toActionError(err), saving: false });
      return false;
    }
  },

  selectEpisode: async (episodeId) => {
    set({ loadingEpisode: true, error: null, activeEpisodeId: episodeId });
    try {
      const detail = await rtClient.request(Actions.STUDIO_EPISODE_GET, { id: episodeId });
      set({
        episodeDetail: detail,
        activeEpisodeId: detail.episode.id,
        loadingEpisode: false,
      });
      void get().fetchQueue(detail.project.id, detail.episode.id);
      void get().fetchCostSummary(detail.project.id, detail.episode.id);
    } catch (err) {
      set({ error: toActionError(err), loadingEpisode: false });
    }
  },

  createEpisode: async (projectId, title) => {
    set({ saving: true, error: null });
    try {
      const epDetail = await rtClient.request(Actions.STUDIO_EPISODE_CREATE, {
        project_id: projectId,
        title,
      });
      const projDetail = await rtClient.request(Actions.STUDIO_PROJECT_GET, { id: projectId });
      set({
        projectDetail: projDetail,
        episodeDetail: epDetail,
        activeEpisodeId: epDetail.episode.id,
        activeStage: epDetail.episode.stage,
        saving: false,
      });
      return true;
    } catch (err) {
      set({ error: toActionError(err), saving: false });
      return false;
    }
  },

  saveScript: async (rawScript) => {
    const epId = get().activeEpisodeId;
    if (!epId) return false;
    set({ saving: true, error: null });
    try {
      const detail = await rtClient.request(Actions.STUDIO_EPISODE_SAVE_SCRIPT, {
        episode_id: epId,
        raw_script: rawScript,
      });
      set({ episodeDetail: detail, saving: false, lastSummary: "Saved raw script" });
      return true;
    } catch (err) {
      set({ error: toActionError(err), saving: false });
      return false;
    }
  },

  upsertAsset: async (payload) => {
    set({ saving: true, error: null });
    try {
      const res = await rtClient.request(Actions.STUDIO_ASSET_UPSERT, payload);
      set((s) => ({
        episodeDetail: s.episodeDetail
          ? { ...s.episodeDetail, assets: res.assets ?? [] }
          : s.episodeDetail,
        projectDetail: s.projectDetail
          ? { ...s.projectDetail, assets: res.assets ?? [] }
          : s.projectDetail,
        saving: false,
      }));
      return true;
    } catch (err) {
      set({ error: toActionError(err), saving: false });
      return false;
    }
  },

  uploadAssetImage: async (assetId, file) => {
    const ep = get().episodeDetail;
    const asset = ep?.assets.find((a) => a.id === assetId);
    if (!ep || !asset) return false;
    set({ saving: true, error: null });
    try {
      const uploaded = await uploadMediaFile(file);
      const reg = await rtClient.request(Actions.MEDIA_REGISTER_INPUT, {
        file_path: uploaded.path,
        kind: "image",
        source_node: "studio.asset_upload",
      });
      await get().upsertAsset({
        id: asset.id,
        project_id: asset.project_id,
        kind: asset.kind,
        name: asset.name,
        code_tag: asset.code_tag,
        description: asset.description,
        prompt: asset.prompt,
        primary_asset_id: reg.id,
        primary_path: reg.file_path,
      });
      return true;
    } catch (err) {
      set({ error: toActionError(err), saving: false });
      return false;
    }
  },

  upsertShot: async (payload) => {
    set({ saving: true, error: null });
    try {
      const res = await rtClient.request(Actions.STUDIO_SHOT_UPSERT, payload);
      set((s) => ({
        episodeDetail: s.episodeDetail
          ? { ...s.episodeDetail, shots: res.shots ?? [] }
          : s.episodeDetail,
        saving: false,
      }));
      return true;
    } catch (err) {
      set({ error: toActionError(err), saving: false });
      return false;
    }
  },

  uploadShotKeyframe: async (shotId, file) => {
    const ep = get().episodeDetail;
    const shot = ep?.shots.find((sh) => sh.id === shotId);
    if (!ep || !shot) return false;
    set({ saving: true, error: null });
    try {
      const uploaded = await uploadMediaFile(file);
      const reg = await rtClient.request(Actions.MEDIA_REGISTER_INPUT, {
        file_path: uploaded.path,
        kind: "image",
        source_node: "studio.keyframe_upload",
      });
      await get().upsertShot({
        id: shot.id,
        episode_id: shot.episode_id,
        keyframe_asset_id: reg.id,
        keyframe_path: reg.file_path,
        status:
          shot.status === "pending" || shot.status === "failed" ? "keyframe_ready" : shot.status,
      });
      return true;
    } catch (err) {
      set({ error: toActionError(err), saving: false });
      return false;
    }
  },

  reorderShots: async (shotIDs) => {
    const epId = get().activeEpisodeId;
    if (!epId) return;
    set({ saving: true, error: null });
    try {
      const res = await rtClient.request(Actions.STUDIO_SHOT_REORDER, {
        episode_id: epId,
        shot_ids: shotIDs,
      });
      set((s) => ({
        episodeDetail: s.episodeDetail
          ? { ...s.episodeDetail, shots: res.shots ?? [] }
          : s.episodeDetail,
        saving: false,
      }));
    } catch (err) {
      set({ error: toActionError(err), saving: false });
    }
  },

  runStage: async (stage, opts = {}) => {
    const epId = get().activeEpisodeId;
    if (!epId) return false;
    const proj = get().projectDetail?.project;
    if (proj && !proj.auto_approve_batches && !opts.approved && isHeavyStudioStage(stage)) {
      const preflight = await get().runBatchPreflight(stage, opts.model, undefined);
      if (preflight) {
        set({ pendingPreflight: { mode: "stage", stage, opts: { ...opts }, preflight } });
        return false;
      }
    }
    set({
      runningStage: stage,
      actionProgress: null,
      batchProgress: null,
      error: null,
    });
    try {
      const res = await rtClient.request(
        Actions.STUDIO_EPISODE_RUN_STAGE,
        {
          episode_id: epId,
          stage,
          ...opts,
        },
        {
          onProgress: (p) => {
            if (!p.heartbeat) set({ actionProgress: p });
          },
        }
      );
      set({
        episodeDetail: res.episode,
        runningStage: null,
        actionProgress: null,
        lastSummary: res.summary || stage,
      });
      return true;
    } catch (err) {
      set({
        error: toActionError(err),
        runningStage: null,
        actionProgress: null,
      });
      return false;
    }
  },

  probeShot: async (shot) => {
    if (!shot.video_path) return null;
    try {
      const res = await rtClient.request(Actions.WORKFLOW_RUN_NODE, {
        type: NodeTypes.PROBE_MEDIA,
        input: {
          media: { kind: "video", localUrl: shot.video_path },
        },
      });
      const out = res.output as ProbeMediaOutput;
      set((s) => ({
        probeMap: { ...s.probeMap, [shot.id]: out },
      }));
      return out;
    } catch (err) {
      set({ error: toActionError(err) });
      return null;
    }
  },

  receiveProjectChanged: (evt) => {
    void get().fetchProjects();
    if (evt.project_id && evt.project_id === get().activeProjectId && evt.action !== "deleted") {
      void get().selectProject(evt.project_id);
    }
  },

  receiveEpisodeUpdated: (evt) => {
    if (evt.episode_id && evt.episode_id === get().activeEpisodeId && !get().runningStage) {
      void get().selectEpisode(evt.episode_id);
    }
  },

  receiveBatchProgress: (evt) => {
    if (evt.episode_id === get().activeEpisodeId) {
      set({ batchProgress: evt.status === "running" ? evt : null });
    }
  },

  receiveQueueUpdated: (evt) => {
    if (evt.project_id === get().activeProjectId && evt.job) {
      set((s) => {
        const idx = s.jobs.findIndex((j) => j.id === evt.job!.id);
        const jobs =
          idx >= 0
            ? s.jobs.map((j) => (j.id === evt.job!.id ? evt.job! : j))
            : [evt.job!, ...s.jobs];
        return { jobs };
      });
    } else if (evt.project_id === get().activeProjectId) {
      void get().fetchQueue(evt.project_id, evt.episode_id);
    }
  },

  receiveCostUpdated: (evt) => {
    if (evt.project_id === get().activeProjectId) {
      void get().fetchCostSummary(evt.project_id, evt.episode_id);
    }
  },

  openQueueDrawer: (open) => {
    set({ queueDrawerOpen: open });
    const pid = get().activeProjectId;
    if (open && pid) {
      void get().fetchQueue(pid, get().activeEpisodeId ?? undefined);
      void get().fetchCostSummary(pid, get().activeEpisodeId ?? undefined);
    }
  },

  openVersionDrawer: (target) => {
    set({ versionDrawerTarget: target });
    if (target) {
      void get().fetchVersions(target.targetKind, target.targetId);
    }
  },

  openGridModal: (target) => set({ gridModalTarget: target }),
  openMaskModal: (target) => set({ maskModalTarget: target }),
  cancelPreflight: () => set({ pendingPreflight: null }),

  confirmPreflight: async () => {
    const pending = get().pendingPreflight;
    if (!pending) return false;
    set({ pendingPreflight: null });
    if (pending.mode === "queue") {
      return get().enqueueBatch({
        ...(pending.opts as Omit<StudioQueueEnqueuePayload, "episode_id">),
        stage: pending.stage,
        approved: true,
      });
    }
    return get().runStage(pending.stage as StudioRunStageAction, {
      ...pending.opts,
      approved: true,
    });
  },

  fetchQueue: async (projectId, episodeId) => {
    try {
      const res = await rtClient.request(Actions.STUDIO_QUEUE_LIST, {
        project_id: projectId,
        episode_id: episodeId,
      });
      set({ jobs: res.jobs ?? [] });
    } catch (err) {
      set({ error: toActionError(err) });
    }
  },

  enqueueBatch: async (payload) => {
    const epId = get().activeEpisodeId;
    const proj = get().projectDetail?.project;
    if (!epId || !proj) return false;
    if (!proj.auto_approve_batches && !payload.approved) {
      const preflight = await get().runBatchPreflight(payload.stage, payload.model, undefined);
      if (preflight) {
        set({
          pendingPreflight: {
            mode: "queue",
            stage: payload.stage,
            opts: { ...payload },
            preflight,
          },
        });
        return false;
      }
    }
    set({ saving: true, error: null });
    try {
      const res = await rtClient.request(Actions.STUDIO_QUEUE_ENQUEUE, {
        ...payload,
        episode_id: epId,
        approved: payload.approved ?? true,
      });
      set((s) => ({
        saving: false,
        jobs: [...res.jobs, ...s.jobs.filter((x) => !res.jobs.some((n) => n.id === x.id))],
        lastSummary: res.summary,
      }));
      return true;
    } catch (err) {
      set({ saving: false, error: toActionError(err) });
      return false;
    }
  },

  controlQueue: async (payload) => {
    set({ saving: true, error: null });
    try {
      await rtClient.request(Actions.STUDIO_QUEUE_CONTROL, payload);
      const pid = payload.project_id ?? get().activeProjectId;
      if (pid) await get().fetchQueue(pid, get().activeEpisodeId ?? undefined);
      set({ saving: false });
    } catch (err) {
      set({ saving: false, error: toActionError(err) });
    }
  },

  fetchCostSummary: async (projectId, episodeId) => {
    try {
      const sum = await rtClient.request(Actions.STUDIO_COST_SUMMARY, {
        project_id: projectId,
        episode_id: episodeId,
      });
      set({ costSummary: sum });
    } catch {
      // Quiet background refresh error
    }
  },

  upsertPricing: async (payload) => {
    set({ saving: true, error: null });
    try {
      await rtClient.request(Actions.STUDIO_PRICING_UPSERT, payload);
      const pid = get().activeProjectId;
      if (pid) await get().fetchCostSummary(pid, get().activeEpisodeId ?? undefined);
      set({ saving: false });
      return true;
    } catch (err) {
      set({ saving: false, error: toActionError(err) });
      return false;
    }
  },

  fetchVersions: async (targetKind, targetId) => {
    try {
      const res = await rtClient.request(Actions.STUDIO_VERSIONS_LIST, {
        target_kind: targetKind,
        target_id: targetId,
      });
      const key = targetVersionKey(targetKind, targetId);
      set((s) => ({
        versionsByTarget: { ...s.versionsByTarget, [key]: res.versions ?? [] },
        qcByTarget: res.latest_qc ? { ...s.qcByTarget, [key]: res.latest_qc } : s.qcByTarget,
      }));
    } catch (err) {
      set({ error: toActionError(err) });
    }
  },

  selectVersion: async (versionId) => {
    set({ saving: true, error: null });
    try {
      const res = await rtClient.request(Actions.STUDIO_VERSION_SELECT, {
        version_id: versionId,
      });
      const key = targetVersionKey(res.version.target_kind, res.version.target_id);
      set((s) => ({
        saving: false,
        episodeDetail: res.episode ?? s.episodeDetail,
        versionsByTarget: {
          ...s.versionsByTarget,
          [key]: (s.versionsByTarget[key] ?? []).map((v) => ({
            ...v,
            is_selected: v.id === res.version.id,
          })),
        },
      }));
      const pid = get().activeProjectId;
      if (pid) {
        const projDetail = await rtClient.request(Actions.STUDIO_PROJECT_GET, { id: pid });
        set({ projectDetail: projDetail });
      }
      return true;
    } catch (err) {
      set({ saving: false, error: toActionError(err) });
      return false;
    }
  },

  splitGrid: async (payload) => {
    set({ saving: true, error: null });
    try {
      const res = await rtClient.request(Actions.STUDIO_GRID_SPLIT, payload);
      if (payload.target_kind && payload.target_id && res.versions) {
        const key = targetVersionKey(payload.target_kind, payload.target_id);
        set((s) => ({
          versionsByTarget: {
            ...s.versionsByTarget,
            [key]: [...res.versions!, ...(s.versionsByTarget[key] ?? [])],
          },
        }));
      }
      set({ saving: false });
      return true;
    } catch (err) {
      set({ saving: false, error: toActionError(err) });
      return false;
    }
  },

  applyMaskEdit: async (payload) => {
    set({ saving: true, error: null });
    try {
      const res = await rtClient.request(Actions.STUDIO_MASK_EDIT, payload);
      set((s) => ({
        saving: false,
        episodeDetail: res.episode ?? s.episodeDetail,
        maskModalTarget: null,
      }));
      if (res.version) {
        await get().fetchVersions(res.version.target_kind, res.version.target_id);
      }
      return true;
    } catch (err) {
      set({ saving: false, error: toActionError(err) });
      return false;
    }
  },

  extractLastFrame: async (payload) => {
    const epId = get().activeEpisodeId;
    if (!epId) return false;
    set({ saving: true, error: null });
    try {
      const res = await rtClient.request(Actions.STUDIO_EXTRACT_LAST_FRAME, {
        ...payload,
        episode_id: epId,
      });
      set((s) => ({
        saving: false,
        episodeDetail: res.episode ?? s.episodeDetail,
        lastSummary: res.frame_path,
      }));
      return true;
    } catch (err) {
      set({ saving: false, error: toActionError(err) });
      return false;
    }
  },

  runQCReview: async (payload) => {
    set({ saving: true, error: null });
    try {
      const rep = await rtClient.request(Actions.STUDIO_QC_REVIEW, payload);
      const key = targetVersionKey(rep.target_kind, rep.target_id);
      set((s) => ({
        saving: false,
        qcByTarget: { ...s.qcByTarget, [key]: rep },
      }));
      return rep;
    } catch (err) {
      set({ saving: false, error: toActionError(err) });
      return null;
    }
  },

  runBatchPreflight: async (stage, model, itemCount) => {
    const pid = get().activeProjectId;
    if (!pid) return null;
    try {
      return await rtClient.request(Actions.STUDIO_BATCH_PREFLIGHT, {
        project_id: pid,
        episode_id: get().activeEpisodeId ?? undefined,
        stage,
        model,
        item_count: itemCount,
      });
    } catch (err) {
      set({ error: toActionError(err) });
      return null;
    }
  },

  fetchTimeline: async (episodeId, opts) => {
    const epId = episodeId ?? get().activeEpisodeId;
    if (!epId) return null;
    set((s) => ({
      timelineLoading: true,
      error: opts?.preserveError ? s.error : null,
    }));
    try {
      const res = await rtClient.request(Actions.STUDIO_TIMELINE_GET, { id: epId });
      set({ timelineResult: res, timelineLoading: false });
      return res;
    } catch (err) {
      set({ timelineLoading: false, error: toActionError(err) });
      return null;
    }
  },

  applyTimelineOps: async (ops, summary) => {
    const epId = get().activeEpisodeId;
    if (!epId || ops.length === 0) return false;
    const expectedRev =
      get().timelineResult?.episode_id === epId ? get().timelineResult!.revision : 0;
    set({ saving: true, error: null });
    try {
      const res = await rtClient.request(Actions.STUDIO_TIMELINE_APPLY_OPS, {
        episode_id: epId,
        expected_revision: expectedRev,
        actor: "user",
        summary,
        ops,
      });
      set({ saving: false, timelineResult: res });
      return true;
    } catch (err) {
      const ae = toActionError(err);
      set({ saving: false, error: ae });
      if (ae.code === "ERR_CONFLICT") {
        void get().fetchTimeline(epId, { preserveError: true });
      }
      return false;
    }
  },

  undoTimeline: async () => {
    const cur = get().timelineResult;
    const epId = cur?.episode_id ?? get().activeEpisodeId;
    if (!epId) return false;
    set({ saving: true, error: null });
    try {
      const res = await rtClient.request(Actions.STUDIO_TIMELINE_UNDO, {
        episode_id: epId,
        expected_revision: cur?.revision ?? 0,
      });
      set({ saving: false, timelineResult: res });
      return true;
    } catch (err) {
      set({ saving: false, error: toActionError(err) });
      return false;
    }
  },

  redoTimeline: async () => {
    const cur = get().timelineResult;
    const epId = cur?.episode_id ?? get().activeEpisodeId;
    if (!epId) return false;
    set({ saving: true, error: null });
    try {
      const res = await rtClient.request(Actions.STUDIO_TIMELINE_REDO, {
        episode_id: epId,
        expected_revision: cur?.revision ?? 0,
      });
      set({ saving: false, timelineResult: res });
      return true;
    } catch (err) {
      set({ saving: false, error: toActionError(err) });
      return false;
    }
  },

  autoAssembleTimeline: async (payload = {}) => {
    const epId = get().activeEpisodeId;
    if (!epId) return false;
    const expectedRev =
      get().timelineResult?.episode_id === epId ? get().timelineResult!.revision : undefined;
    set({ timelineLoading: true, actionProgress: null, error: null });
    try {
      const res = await rtClient.request(
        Actions.STUDIO_TIMELINE_AUTO_ASSEMBLE,
        {
          episode_id: epId,
          ...(expectedRev !== undefined ? { expected_revision: expectedRev } : {}),
          ...payload,
        },
        {
          onProgress: (p) => {
            if (!p.heartbeat) set({ actionProgress: p });
          },
        }
      );
      set({
        timelineLoading: false,
        actionProgress: null,
        timelineResult: res,
      });
      return true;
    } catch (err) {
      set({ timelineLoading: false, actionProgress: null, error: toActionError(err) });
      return false;
    }
  },

  computeClipPeaksThumbs: async (mediaPath, clipId, opts = {}) => {
    const epId = get().activeEpisodeId;
    if (!epId || !mediaPath) return null;
    if (clipId) {
      set((s) => ({ peaksLoadingClipIds: { ...s.peaksLoadingClipIds, [clipId]: true } }));
    }
    try {
      const out = await rtClient.request(Actions.STUDIO_MEDIA_PEAKS_THUMBS, {
        episode_id: epId,
        media_path: mediaPath,
        ...(clipId ? { clip_id: clipId } : {}),
        ...(opts.thumb_interval_sec ? { thumb_interval_sec: opts.thumb_interval_sec } : {}),
        ...(opts.peak_bins ? { peak_bins: opts.peak_bins } : {}),
      });
      if (clipId) {
        set((s) => {
          const next = { ...s.peaksLoadingClipIds };
          delete next[clipId];
          return { peaksLoadingClipIds: next };
        });
      }
      return out;
    } catch (err) {
      if (clipId) {
        set((s) => {
          const next = { ...s.peaksLoadingClipIds };
          delete next[clipId];
          return { peaksLoadingClipIds: next, error: toActionError(err) };
        });
      } else {
        set({ error: toActionError(err) });
      }
      return null;
    }
  },

  renderTimeline: async (payload = {}) => {
    const epId = get().activeEpisodeId;
    if (!epId) return null;
    set({ renderingTimeline: true, actionProgress: null, error: null });
    try {
      const res = await rtClient.request(
        Actions.STUDIO_TIMELINE_RENDER,
        { episode_id: epId, ...payload },
        {
          onProgress: (p) => {
            if (!p.heartbeat) set({ actionProgress: p });
          },
        }
      );
      set({
        renderingTimeline: false,
        actionProgress: null,
        lastRenderResult: res,
        episodeDetail: res.episode ?? get().episodeDetail,
        lastSummary: res.output_path,
      });
      return res;
    } catch (err) {
      set({ renderingTimeline: false, actionProgress: null, error: toActionError(err) });
      return null;
    }
  },

  exportNLEDraft: async (payload) => {
    const epId = get().activeEpisodeId;
    if (!epId) return null;
    set({ exportingNLE: true, actionProgress: null, error: null });
    try {
      const out = await rtClient.request(
        Actions.STUDIO_TIMELINE_EXPORT_NLE,
        { episode_id: epId, ...payload },
        {
          onProgress: (p) => {
            if (!p.heartbeat) set({ actionProgress: p });
          },
        }
      );
      set({ exportingNLE: false, actionProgress: null, lastNLEExport: out });
      return out;
    } catch (err) {
      set({ exportingNLE: false, actionProgress: null, error: toActionError(err) });
      return null;
    }
  },

  openExportFolder: async (filePath) => {
    const epId = get().activeEpisodeId;
    if (!epId && !filePath) return false;
    try {
      await rtClient.request(Actions.STUDIO_OPEN_EXPORT_FOLDER, {
        episode_id: epId ?? "",
        ...(filePath ? { file_path: filePath } : {}),
      });
      return true;
    } catch (err) {
      set({ error: toActionError(err) });
      return false;
    }
  },

  receiveTimelineChanged: (evt) => {
    const activeEp = get().activeEpisodeId;
    if (activeEp && evt.episode_id !== activeEp) return;
    set((s) => {
      const prev = s.timelineResult;
      return {
        timelineResult: {
          project_id: evt.project_id,
          episode_id: evt.episode_id,
          revision: evt.revision,
          can_undo: evt.can_undo,
          can_redo: evt.can_redo,
          duration_ms: evt.duration_ms,
          timeline_path: prev?.episode_id === evt.episode_id ? prev.timeline_path : undefined,
          timeline: evt.timeline,
          revisions: prev?.episode_id === evt.episode_id ? prev.revisions : undefined,
          updated_at: Date.now(),
        },
      };
    });
  },
}));
