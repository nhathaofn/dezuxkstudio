import { Actions } from "../../contracts/generated";
import { rtClient, toActionError } from "../../lib/rtClient";
import { normalizeEpisodeDetail, normalizeProjectDetail } from "./studioFormatters";
import type { StudioGet, StudioSet, StudioState } from "./storeTypes";

let projectReqSeq = 0;
let episodeReqSeq = 0;

export function createProjectSlice(
  set: StudioSet,
  get: StudioGet
): Pick<
  StudioState,
  | "setActiveStage"
  | "toggleSelectedAssetId"
  | "clearError"
  | "clearTransientState"
  | "resyncAfterReconnect"
  | "fetchProjects"
  | "selectProject"
  | "createProject"
  | "updateProject"
  | "deleteProject"
  | "selectEpisode"
  | "createEpisode"
  | "syncWorkspace"
> {
  return {
    setActiveStage: (stage) => set({ activeStage: stage }),

    toggleSelectedAssetId: (assetId) =>
      set((s) => ({
        selectedAssetIds: s.selectedAssetIds.includes(assetId)
          ? s.selectedAssetIds.filter((id) => id !== assetId)
          : [...s.selectedAssetIds, assetId],
      })),

    clearError: () => set({ error: null }),

    clearTransientState: () =>
      set({
        saving: false,
        runningStage: null,
        timelineLoading: false,
        renderingTimeline: false,
        exportingNLE: false,
        actionProgress: null,
        batchProgress: null,
        peaksLoadingClipIds: {},
      }),

    resyncAfterReconnect: async () => {
      const pid = get().activeProjectId;
      const epId = get().activeEpisodeId;
      const prevStage = get().activeStage;
      const hadTimeline = get().timelineResult?.episode_id === epId || prevStage === "export";
      const verTarget = get().versionDrawerTarget;

      await get().fetchProjects();

      const pidStillExists = pid ? get().projects.some((p) => p.id === pid) : false;
      if (!pidStillExists) {
        return;
      }

      if (epId && pid) {
        let epStillExists = true;
        try {
          const rawProj = await rtClient.request(Actions.STUDIO_PROJECT_GET, { id: pid });
          if (rawProj?.project) {
            const normProj = normalizeProjectDetail(rawProj);
            set({ projectDetail: normProj });
            epStillExists = normProj.episodes.some((e) => e.id === epId);
          }
        } catch {
          // Proceed with selectEpisode fallback if project detail refresh fails
        }

        if (epStillExists) {
          await get().selectEpisode(epId, { preserveStage: true });
          if (hadTimeline) {
            await get().fetchTimeline(epId, { preserveError: true });
          }
        } else {
          await get().selectProject(pid);
        }
      } else if (pid) {
        await get().selectProject(pid, { preserveEpisode: true });
      }

      if (verTarget && verTarget.projectId === get().activeProjectId) {
        await get().fetchVersions(verTarget.targetKind, verTarget.targetId);
      }
    },

    fetchProjects: async () => {
      set({ loadingProjects: true, error: null, needsInitialConnectSync: false });
      try {
        const res = await rtClient.request(Actions.STUDIO_PROJECT_LIST, {});
        const list = res.projects ?? [];
        set({ projects: list, projectsDir: res.projects_dir ?? "", loadingProjects: false });

        const curId = get().activeProjectId;
        if (curId && list.some((p) => p.id === curId)) {
          if (!get().projectDetail || get().projectDetail?.project.id !== curId) {
            await get().selectProject(curId, { preserveEpisode: true });
          }
        } else if (list.length > 0) {
          await get().selectProject(list[0].id);
        } else {
          set({
            activeProjectId: null,
            activeEpisodeId: null,
            projectDetail: null,
            episodeDetail: null,
            timelineResult: null,
            lastRenderResult: null,
            lastNLEExport: null,
            probeMap: {},
            selectedAssetIds: [],
          });
        }
      } catch (err) {
        const ae = toActionError(err);
        set({
          error: ae,
          loadingProjects: false,
          needsInitialConnectSync: ae.code === "ERR_NOT_CONNECTED",
        });
      }
    },

    selectProject: async (projectId, opts) => {
      const seq = ++projectReqSeq;
      const prevProjectId = get().activeProjectId;
      const prevEpisodeId = get().activeEpisodeId;
      const prevStage = get().activeStage;
      const switchingProject = prevProjectId !== projectId;

      set((s) => ({
        loadingEpisode: true,
        error: null,
        activeProjectId: projectId,
        ...(switchingProject
          ? {
              selectedAssetIds: [],
              probeMap: {},
              timelineResult: null,
              lastRenderResult: null,
              lastNLEExport: null,
              peaksLoadingClipIds: {},
            }
          : {
              selectedAssetIds: s.selectedAssetIds,
            }),
      }));

      try {
        const raw = await rtClient.request(Actions.STUDIO_PROJECT_GET, { id: projectId });
        if (seq !== projectReqSeq || !raw?.project) {
          if (seq === projectReqSeq) set({ loadingEpisode: false });
          return;
        }
        const detail = normalizeProjectDetail(raw);
        const keepPrevEp =
          Boolean(opts?.preserveEpisode && prevEpisodeId) &&
          detail.episodes.some((e) => e.id === prevEpisodeId);

        let nextEpDetail = detail.active_episode ?? null;
        if (keepPrevEp && prevEpisodeId && nextEpDetail?.episode.id !== prevEpisodeId) {
          const fetchedEp = await rtClient.request(Actions.STUDIO_EPISODE_GET, {
            id: prevEpisodeId,
          });
          if (seq !== projectReqSeq) return;
          if (fetchedEp?.episode) {
            nextEpDetail = normalizeEpisodeDetail(fetchedEp);
          }
        }

        const nextEpId = keepPrevEp
          ? prevEpisodeId
          : (nextEpDetail?.episode.id ?? detail.episodes[0]?.id ?? null);

        set({
          projectDetail: detail,
          episodeDetail: nextEpDetail,
          activeProjectId: detail.project.id,
          activeEpisodeId: nextEpId,
          activeStage: keepPrevEp ? prevStage : (nextEpDetail?.episode.stage ?? prevStage),
          loadingEpisode: false,
        });
        void get().fetchQueue(detail.project.id, nextEpId ?? undefined);
        void get().fetchCostSummary(detail.project.id, nextEpId ?? undefined);
      } catch (err) {
        if (seq !== projectReqSeq) return;
        set({ error: toActionError(err), loadingEpisode: false });
      }
    },

    createProject: async (payload) => {
      set({ saving: true, error: null });
      try {
        const raw = await rtClient.request(Actions.STUDIO_PROJECT_CREATE, payload);
        const detail = normalizeProjectDetail(raw);
        const activeEp = detail.active_episode ?? null;
        set((s) => ({
          projects: [detail.project, ...s.projects.filter((p) => p.id !== detail.project.id)],
          projectDetail: detail,
          episodeDetail: activeEp,
          activeProjectId: detail.project.id,
          activeEpisodeId: activeEp?.episode.id ?? null,
          activeStage: activeEp?.episode.stage ?? "script",
          selectedAssetIds: [],
          probeMap: {},
          timelineResult: null,
          lastRenderResult: null,
          lastNLEExport: null,
          saving: false,
          lastSummary: detail.project.title,
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
        const raw = await rtClient.request(Actions.STUDIO_PROJECT_UPDATE, payload);
        const detail = normalizeProjectDetail(raw);
        const curEpId = get().activeEpisodeId;
        let nextEp = detail.active_episode ?? get().episodeDetail;
        if (curEpId && nextEp?.episode.id !== curEpId) {
          nextEp = normalizeEpisodeDetail(
            await rtClient.request(Actions.STUDIO_EPISODE_GET, { id: curEpId })
          );
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

    selectEpisode: async (episodeId, opts) => {
      const seq = ++episodeReqSeq;
      const prevEpId = get().activeEpisodeId;
      const switchingEpisode = prevEpId !== episodeId;

      set({
        loadingEpisode: true,
        error: null,
        activeEpisodeId: episodeId,
        ...(switchingEpisode
          ? {
              probeMap: {},
              timelineResult: null,
              lastRenderResult: null,
              lastNLEExport: null,
              peaksLoadingClipIds: {},
            }
          : {}),
      });

      try {
        const raw = await rtClient.request(Actions.STUDIO_EPISODE_GET, { id: episodeId });
        if (seq !== episodeReqSeq) return;
        const detail = normalizeEpisodeDetail(raw);
        set((s) => ({
          episodeDetail: detail,
          activeEpisodeId: detail.episode.id,
          activeStage: opts?.preserveStage ? s.activeStage : detail.episode.stage,
          loadingEpisode: false,
        }));
        void get().fetchQueue(detail.project.id, detail.episode.id);
        void get().fetchCostSummary(detail.project.id, detail.episode.id);
      } catch (err) {
        if (seq !== episodeReqSeq) return;
        set({ error: toActionError(err), loadingEpisode: false });
      }
    },

    createEpisode: async (projectId, title) => {
      set({ saving: true, error: null });
      try {
        const epDetail = normalizeEpisodeDetail(
          await rtClient.request(Actions.STUDIO_EPISODE_CREATE, {
            project_id: projectId,
            title,
          })
        );
        const projDetail = normalizeProjectDetail(
          await rtClient.request(Actions.STUDIO_PROJECT_GET, { id: projectId })
        );
        set({
          projectDetail: projDetail,
          episodeDetail: epDetail,
          activeEpisodeId: epDetail.episode.id,
          activeStage: epDetail.episode.stage,
          probeMap: {},
          timelineResult: null,
          lastRenderResult: null,
          lastNLEExport: null,
          peaksLoadingClipIds: {},
          saving: false,
        });
        return true;
      } catch (err) {
        set({ error: toActionError(err), saving: false });
        return false;
      }
    },

    syncWorkspace: async (projectId) => {
      const pid = projectId ?? get().activeProjectId ?? undefined;
      set({ saving: true, error: null });
      try {
        const res = await rtClient.request(
          Actions.STUDIO_SYNC_WORKSPACE,
          pid ? { project_id: pid } : {}
        );
        const normProj = res.project ? normalizeProjectDetail(res.project) : undefined;
        set((s) => ({
          saving: false,
          projects: res.projects ?? s.projects,
          projectDetail: normProj ?? s.projectDetail,
          episodeDetail: normProj?.active_episode ?? s.episodeDetail,
          lastSummary: res.projects_dir,
        }));
        const activeEp = get().activeEpisodeId;
        if (activeEp) {
          await get().selectEpisode(activeEp);
        }
        return true;
      } catch (err) {
        set({ saving: false, error: toActionError(err) });
        return false;
      }
    },
  };
}
