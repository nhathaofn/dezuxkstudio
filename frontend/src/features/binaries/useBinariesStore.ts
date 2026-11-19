import { create } from "zustand";
import {
  Actions,
  type ActionProgress,
  type BinaryID,
  type BinaryInfo,
  type BinaryProgress,
} from "../../contracts/generated";
import { rtClient, toActionError, type ActionError } from "../../lib/rtClient";

interface BinariesState {
  binaries: BinaryInfo[];
  loading: boolean;
  error: ActionError | null;
  activeProgress: Record<string, ActionProgress | null>;
  abortControllers: Record<string, AbortController>;
  fetchBinaries: () => Promise<void>;
  probe: (id: BinaryID) => Promise<void>;
  download: (
    id: BinaryID,
    signal?: AbortSignal,
    onProgress?: (p: ActionProgress) => void
  ) => Promise<void>;
  cancelDownload: (id: BinaryID) => void;
  setCustomPath: (id: BinaryID, customPath: string) => Promise<void>;
  setProgress: (id: BinaryID, progress: ActionProgress | null) => void;
  receiveBinaryProgress: (bp: BinaryProgress) => void;
}

export const useBinariesStore = create<BinariesState>((set, get) => ({
  binaries: [],
  loading: false,
  error: null,
  activeProgress: {},
  abortControllers: {},

  fetchBinaries: async () => {
    set({ loading: true, error: null });
    try {
      const res = await rtClient.request(Actions.BINARIES_LIST, {});
      set({ binaries: res.binaries, loading: false });
    } catch (err) {
      set({ error: toActionError(err), loading: false });
    }
  },

  probe: async (id: BinaryID) => {
    try {
      const updated = await rtClient.request(Actions.BINARIES_PROBE, { id });
      set((s) => ({
        binaries: s.binaries.map((b) => (b.id === id ? updated : b)),
      }));
    } catch (err) {
      set({ error: toActionError(err) });
    }
  },

  setProgress: (id: BinaryID, progress: ActionProgress | null) => {
    set((s) => ({
      activeProgress: { ...s.activeProgress, [id]: progress },
    }));
  },

  receiveBinaryProgress: (bp: BinaryProgress) => {
    if (!bp?.id) return;
    get().setProgress(bp.id, {
      percent: bp.percent,
      message: "",
      resumed_from_bytes: bp.resumed_from_bytes,
      is_resume: bp.is_resume,
      binary_progress: bp,
    });
  },

  cancelDownload: (id: BinaryID) => {
    const controller = get().abortControllers[id];
    if (controller) {
      controller.abort();
    }
    set((s) => {
      const nextControllers = { ...s.abortControllers };
      delete nextControllers[id];
      return {
        abortControllers: nextControllers,
        activeProgress: { ...s.activeProgress, [id]: null },
      };
    });
  },

  download: async (
    id: BinaryID,
    externalSignal?: AbortSignal,
    onProgress?: (p: ActionProgress) => void
  ) => {
    get().abortControllers[id]?.abort();
    const controller = new AbortController();
    const onExternalAbort = () => controller.abort();
    if (externalSignal) {
      if (externalSignal.aborted) controller.abort();
      else externalSignal.addEventListener("abort", onExternalAbort, { once: true });
    }

    set((s) => ({
      abortControllers: { ...s.abortControllers, [id]: controller },
      error: null,
    }));

    try {
      const handleProgress = (p: ActionProgress) => {
        set((s) => ({
          activeProgress: { ...s.activeProgress, [id]: p },
        }));
        onProgress?.(p);
      };
      const updated = await rtClient.request(
        Actions.BINARIES_DOWNLOAD,
        { id },
        { signal: controller.signal, onProgress: handleProgress }
      );
      set((s) => ({
        binaries: s.binaries.map((b) => (b.id === id ? updated : b)),
      }));
    } catch (err) {
      set({ error: toActionError(err) });
      throw err;
    } finally {
      externalSignal?.removeEventListener("abort", onExternalAbort);
      set((s) => {
        const nextControllers = { ...s.abortControllers };
        if (nextControllers[id] === controller) {
          delete nextControllers[id];
        }
        return {
          abortControllers: nextControllers,
          activeProgress: { ...s.activeProgress, [id]: null },
        };
      });
    }
  },

  setCustomPath: async (id: BinaryID, customPath: string) => {
    try {
      const updated = await rtClient.request(Actions.BINARIES_SET_PATH, {
        id,
        custom_path: customPath,
      });
      set((s) => ({
        binaries: s.binaries.map((b) => (b.id === id ? updated : b)),
      }));
    } catch (err) {
      set({ error: toActionError(err) });
      throw err;
    }
  },
}));
