import { create } from "zustand";
import {
  Actions,
  NodeTypes,
  type ActionProgress,
  type ProbeMediaOutput,
  type StudioAssetUpsertPayload,
  type StudioBatchProgressEvent,
  type StudioEpisodeDetailResult,
  type StudioEpisodeRunStagePayload,
  type StudioEpisodeStage,
  type StudioEpisodeUpdatedEvent,
  type StudioProject,
  type StudioProjectChangedEvent,
  type StudioProjectCreatePayload,
  type StudioProjectDetailResult,
  type StudioProjectUpdatePayload,
  type StudioRunStageAction,
  type StudioShot,
  type StudioShotUpsertPayload,
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
}));
