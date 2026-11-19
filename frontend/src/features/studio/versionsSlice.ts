import {
  Actions,
  type StudioMediaTargetKind,
  type StudioQCReport,
} from "../../contracts/generated";
import { rtClient, toActionError } from "../../lib/rtClient";
import {
  normalizeEpisodeDetail,
  normalizeProjectDetail,
  normalizeQCReport,
} from "./studioFormatters";
import { targetVersionKey, type StudioGet, type StudioSet, type StudioState } from "./storeTypes";

export function createVersionsSlice(
  set: StudioSet,
  get: StudioGet
): Pick<
  StudioState,
  | "openVersionDrawer"
  | "openGridModal"
  | "openMaskModal"
  | "fetchVersions"
  | "selectVersion"
  | "splitGrid"
  | "applyMaskEdit"
  | "extractLastFrame"
  | "runQCReview"
> {
  return {
    openVersionDrawer: (target) => {
      set({ versionDrawerTarget: target });
      if (target) {
        void get().fetchVersions(target.targetKind, target.targetId);
      }
    },

    openGridModal: (target) => set({ gridModalTarget: target }),
    openMaskModal: (target) => set({ maskModalTarget: target }),

    fetchVersions: async (targetKind, targetId) => {
      try {
        const res = await rtClient.request(Actions.STUDIO_VERSIONS_LIST, {
          target_kind: targetKind,
          target_id: targetId,
        });
        const key = targetVersionKey(targetKind, targetId);
        const legacyQC = (res as unknown as { qc_reports?: StudioQCReport[] }).qc_reports?.[0];
        const latestQC = res.latest_qc ?? legacyQC;
        set((s) => ({
          versionsByTarget: { ...s.versionsByTarget, [key]: res.versions ?? [] },
          qcByTarget: latestQC
            ? { ...s.qcByTarget, [key]: normalizeQCReport(latestQC) }
            : s.qcByTarget,
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
          episodeDetail: res.episode ? normalizeEpisodeDetail(res.episode) : s.episodeDetail,
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
          if (projDetail?.project) {
            set({ projectDetail: normalizeProjectDetail(projDetail) });
          }
        }
        return true;
      } catch (err) {
        set({ saving: false, error: toActionError(err) });
        return false;
      }
    },

    splitGrid: async (payload) => {
      set({ saving: true, actionProgress: null, error: null });
      try {
        const res = await rtClient.request(Actions.STUDIO_GRID_SPLIT, payload, {
          onProgress: (p) => {
            if (!p.heartbeat) set({ actionProgress: p });
          },
        });
        const incoming = res.versions ?? [];
        if (incoming.length > 0) {
          const key = targetVersionKey(
            payload.target_kind || ("grid_panel" satisfies StudioMediaTargetKind),
            payload.target_id || payload.project_id
          );
          set((s) => ({
            versionsByTarget: {
              ...s.versionsByTarget,
              [key]: [...incoming, ...(s.versionsByTarget[key] ?? [])],
            },
          }));
        }
        set({ saving: false, actionProgress: null });
        return true;
      } catch (err) {
        set({ saving: false, actionProgress: null, error: toActionError(err) });
        return false;
      }
    },

    applyMaskEdit: async (payload) => {
      set({ saving: true, actionProgress: null, error: null });
      try {
        const res = await rtClient.request(Actions.STUDIO_MASK_EDIT, payload, {
          onProgress: (p) => {
            if (!p.heartbeat) set({ actionProgress: p });
          },
        });
        set((s) => ({
          saving: false,
          actionProgress: null,
          episodeDetail: res.episode ? normalizeEpisodeDetail(res.episode) : s.episodeDetail,
          maskModalTarget: null,
        }));
        if (res.version) {
          await get().fetchVersions(res.version.target_kind, res.version.target_id);
        }
        return true;
      } catch (err) {
        set({ saving: false, actionProgress: null, error: toActionError(err) });
        return false;
      }
    },

    extractLastFrame: async (payload) => {
      const epId = get().activeEpisodeId;
      if (!epId) return false;
      set({ saving: true, actionProgress: null, error: null });
      try {
        const res = await rtClient.request(
          Actions.STUDIO_EXTRACT_LAST_FRAME,
          {
            ...payload,
            episode_id: epId,
          },
          {
            onProgress: (p) => {
              if (!p.heartbeat) set({ actionProgress: p });
            },
          }
        );
        set((s) => ({
          saving: false,
          actionProgress: null,
          episodeDetail: res.episode ? normalizeEpisodeDetail(res.episode) : s.episodeDetail,
          lastSummary: res.frame_path,
        }));
        return true;
      } catch (err) {
        set({ saving: false, actionProgress: null, error: toActionError(err) });
        return false;
      }
    },

    runQCReview: async (payload) => {
      set({ saving: true, actionProgress: null, error: null });
      try {
        const raw = await rtClient.request(Actions.STUDIO_QC_REVIEW, payload, {
          onProgress: (p) => {
            if (!p.heartbeat) set({ actionProgress: p });
          },
        });
        const rep = normalizeQCReport(raw);
        const key = targetVersionKey(rep.target_kind, rep.target_id);
        set((s) => ({
          saving: false,
          actionProgress: null,
          qcByTarget: { ...s.qcByTarget, [key]: rep },
        }));
        return rep;
      } catch (err) {
        set({ saving: false, actionProgress: null, error: toActionError(err) });
        return null;
      }
    },
  };
}
