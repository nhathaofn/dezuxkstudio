import { create } from "zustand";
import {
  Actions,
  type ActionProgress,
  type BinaryID,
  type BinaryInfo,
} from "../../contracts/generated";
import { rtClient, toActionError, type ActionError } from "../../lib/rtClient";

interface BinariesState {
  binaries: BinaryInfo[];
  loading: boolean;
  error: ActionError | null;
  fetchBinaries: () => Promise<void>;
  probe: (id: BinaryID) => Promise<void>;
  download: (
    id: BinaryID,
    signal: AbortSignal,
    onProgress: (p: ActionProgress) => void
  ) => Promise<void>;
  setCustomPath: (id: BinaryID, customPath: string) => Promise<void>;
}

export const useBinariesStore = create<BinariesState>((set) => ({
  binaries: [],
  loading: false,
  error: null,

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

  download: async (id: BinaryID, signal: AbortSignal, onProgress: (p: ActionProgress) => void) => {
    try {
      const updated = await rtClient.request(
        Actions.BINARIES_DOWNLOAD,
        { id },
        { signal, onProgress }
      );
      set((s) => ({
        binaries: s.binaries.map((b) => (b.id === id ? updated : b)),
      }));
    } catch (err) {
      set({ error: toActionError(err) });
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
