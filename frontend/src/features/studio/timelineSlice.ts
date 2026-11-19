import { Actions, type ErrorCode, type StudioTimelineActor } from "../../contracts/generated";
import { rtClient, toActionError } from "../../lib/rtClient";
import {
  normalizeEpisodeDetail,
  normalizeTimeline,
  normalizeTimelineResult,
} from "./studioFormatters";
import type { StudioGet, StudioSet, StudioState } from "./storeTypes";

let timelineReqSeq = 0;

export function createTimelineSlice(
  set: StudioSet,
  get: StudioGet
): Pick<
  StudioState,
  | "fetchTimeline"
  | "applyTimelineOps"
  | "undoTimeline"
  | "redoTimeline"
  | "autoAssembleTimeline"
  | "computeClipPeaksThumbs"
  | "renderTimeline"
  | "exportNLEDraft"
  | "openExportFolder"
  | "receiveTimelineChanged"
> {
  return {
    fetchTimeline: async (episodeId, opts) => {
      const epId = episodeId ?? get().activeEpisodeId;
      if (!epId) return null;
      const seq = ++timelineReqSeq;
      set((s) => ({
        timelineLoading: true,
        error: opts?.preserveError ? s.error : null,
      }));
      try {
        const res = await rtClient.request(Actions.STUDIO_TIMELINE_GET, { id: epId });
        if (seq !== timelineReqSeq) return null;
        if (!res?.timeline && !res?.episode_id) {
          set({ timelineLoading: false });
          return null;
        }
        const normRes = normalizeTimelineResult(res);
        set({ timelineResult: normRes, timelineLoading: false });
        return normRes;
      } catch (err) {
        if (seq !== timelineReqSeq) return null;
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
          actor: "user" satisfies StudioTimelineActor,
          summary,
          ops,
        });
        set({ saving: false, timelineResult: normalizeTimelineResult(res) });
        return true;
      } catch (err) {
        const ae = toActionError(err);
        set({ saving: false, error: ae });
        if (ae.code === ("ERR_CONFLICT" satisfies ErrorCode)) {
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
        set({ saving: false, timelineResult: normalizeTimelineResult(res) });
        return true;
      } catch (err) {
        const ae = toActionError(err);
        set({ saving: false, error: ae });
        if (ae.code === ("ERR_CONFLICT" satisfies ErrorCode)) {
          void get().fetchTimeline(epId, { preserveError: true });
        }
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
        set({ saving: false, timelineResult: normalizeTimelineResult(res) });
        return true;
      } catch (err) {
        const ae = toActionError(err);
        set({ saving: false, error: ae });
        if (ae.code === ("ERR_CONFLICT" satisfies ErrorCode)) {
          void get().fetchTimeline(epId, { preserveError: true });
        }
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
          timelineResult: normalizeTimelineResult(res),
        });
        return true;
      } catch (err) {
        const ae = toActionError(err);
        set({ timelineLoading: false, actionProgress: null, error: ae });
        if (ae.code === ("ERR_CONFLICT" satisfies ErrorCode)) {
          void get().fetchTimeline(epId, { preserveError: true });
        }
        return false;
      }
    },

    computeClipPeaksThumbs: async (mediaPath, clipId, opts = {}) => {
      const epId = get().activeEpisodeId;
      if (!epId || !mediaPath) return null;
      if (clipId) {
        set((s) => ({
          actionProgress: null,
          peaksLoadingClipIds: { ...s.peaksLoadingClipIds, [clipId]: true },
        }));
      } else {
        set({ actionProgress: null });
      }
      try {
        const out = await rtClient.request(
          Actions.STUDIO_MEDIA_PEAKS_THUMBS,
          {
            episode_id: epId,
            media_path: mediaPath,
            ...(clipId ? { clip_id: clipId } : {}),
            ...(opts.thumb_interval_sec ? { thumb_interval_sec: opts.thumb_interval_sec } : {}),
            ...(opts.peak_bins ? { peak_bins: opts.peak_bins } : {}),
          },
          {
            onProgress: (p) => {
              if (!p.heartbeat) set({ actionProgress: p });
            },
          }
        );
        if (clipId) {
          set((s) => {
            const next = { ...s.peaksLoadingClipIds };
            delete next[clipId];
            return { peaksLoadingClipIds: next, actionProgress: null };
          });
        } else {
          set({ actionProgress: null });
        }
        return out;
      } catch (err) {
        if (clipId) {
          set((s) => {
            const next = { ...s.peaksLoadingClipIds };
            delete next[clipId];
            return { peaksLoadingClipIds: next, actionProgress: null, error: toActionError(err) };
          });
        } else {
          set({ actionProgress: null, error: toActionError(err) });
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
          episodeDetail: res.episode ? normalizeEpisodeDetail(res.episode) : get().episodeDetail,
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
      if (!activeEp || evt.episode_id !== activeEp) return;
      set((s) => {
        const prev = s.timelineResult;
        if (prev && prev.episode_id === evt.episode_id && evt.revision < prev.revision) {
          return s;
        }
        const prevRevs = prev?.episode_id === evt.episode_id ? (prev.revisions ?? []) : [];
        const nextRevs = prevRevs.some((r) => r.revision === evt.revision)
          ? prevRevs
          : [
              {
                id: `rev-${evt.episode_id}-${evt.revision}`,
                revision: evt.revision,
                actor: evt.actor,
                summary: evt.summary,
                created_at: Date.now(),
              },
              ...prevRevs,
            ];
        return {
          timelineResult: {
            project_id: evt.project_id,
            episode_id: evt.episode_id,
            revision: evt.revision,
            can_undo: evt.can_undo,
            can_redo: evt.can_redo,
            duration_ms: evt.duration_ms,
            timeline_path: prev?.episode_id === evt.episode_id ? prev.timeline_path : undefined,
            timeline: normalizeTimeline(evt.timeline),
            revisions: nextRevs,
            updated_at: Date.now(),
          },
        };
      });
    },
  };
}
