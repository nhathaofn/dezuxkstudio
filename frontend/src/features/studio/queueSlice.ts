import {
  Actions,
  type StudioQueueEnqueuePayload,
  type StudioRunStageAction,
} from "../../contracts/generated";
import { rtClient, toActionError } from "../../lib/rtClient";
import { normalizeCostSummary, normalizePreflight } from "./studioFormatters";
import type { StudioGet, StudioSet, StudioState } from "./storeTypes";

export function createQueueSlice(
  set: StudioSet,
  get: StudioGet
): Pick<
  StudioState,
  | "openQueueDrawer"
  | "cancelPreflight"
  | "confirmPreflight"
  | "fetchQueue"
  | "enqueueBatch"
  | "controlQueue"
  | "fetchCostSummary"
  | "upsertPricing"
  | "runBatchPreflight"
  | "receiveQueueUpdated"
  | "receiveCostUpdated"
> {
  return {
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
        if (!preflight) {
          return false;
        }
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
      set({ saving: true, error: null });
      try {
        const res = await rtClient.request(Actions.STUDIO_QUEUE_ENQUEUE, {
          ...payload,
          episode_id: epId,
          approved: payload.approved ?? true,
        });
        const incoming = res.jobs ?? [];
        set((s) => ({
          saving: false,
          jobs: [...incoming, ...s.jobs.filter((x) => !incoming.some((n) => n.id === x.id))],
          lastSummary: res.summary ?? "",
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

    fetchCostSummary: async (projectId, episodeId, opts) => {
      try {
        const sum = await rtClient.request(Actions.STUDIO_COST_SUMMARY, {
          project_id: projectId,
          episode_id: episodeId,
        });
        set({ costSummary: normalizeCostSummary(sum) });
      } catch (err) {
        if (opts?.quiet === false) {
          set({ error: toActionError(err) });
        }
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

    runBatchPreflight: async (stage, model, itemCount) => {
      const pid = get().activeProjectId;
      if (!pid) return null;
      try {
        const raw = await rtClient.request(Actions.STUDIO_BATCH_PREFLIGHT, {
          project_id: pid,
          episode_id: get().activeEpisodeId ?? undefined,
          stage,
          model,
          item_count: itemCount,
        });
        return normalizePreflight(raw);
      } catch (err) {
        set({ error: toActionError(err) });
        return null;
      }
    },
  };
}
