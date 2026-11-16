import { create } from "zustand";
import {
  Actions,
  Events,
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
  fetchBinaries: () => Promise<void>;
  probe: (id: BinaryID) => Promise<void>;
  download: (
    id: BinaryID,
    signal: AbortSignal,
    onProgress: (p: ActionProgress) => void
  ) => Promise<void>;
  setCustomPath: (id: BinaryID, customPath: string) => Promise<void>;
  setProgress: (id: BinaryID, progress: ActionProgress | null) => void;
}

export const useBinariesStore = create<BinariesState>((set) => ({
  binaries: [],
  loading: false,
  error: null,
  activeProgress: {},

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

  download: async (id: BinaryID, signal: AbortSignal, onProgress: (p: ActionProgress) => void) => {
    try {
      const handleProgress = (p: ActionProgress) => {
        set((s) => ({
          activeProgress: { ...s.activeProgress, [id]: p },
        }));
        onProgress(p);
      };
      const updated = await rtClient.request(
        Actions.BINARIES_DOWNLOAD,
        { id },
        { signal, onProgress: handleProgress }
      );
      set((s) => ({
        binaries: s.binaries.map((b) => (b.id === id ? updated : b)),
        activeProgress: { ...s.activeProgress, [id]: null },
      }));
    } catch (err) {
      set((s) => ({
        error: toActionError(err),
        activeProgress: { ...s.activeProgress, [id]: null },
      }));
      throw err;
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

// Listen to realtime EventBinariesProgress broadcasts if available
if (typeof window !== "undefined" && rtClient && typeof rtClient.on === "function") {
  rtClient.on(Events.BINARIES_PROGRESS, (bp: BinaryProgress) => {
    if (bp?.id) {
      useBinariesStore.getState().setProgress(bp.id, {
        percent: bp.percent,
        message: "",
        resumed_from_bytes: bp.resumed_from_bytes,
        is_resume: bp.is_resume,
        binary_progress: bp,
      });
    }
  });
}
