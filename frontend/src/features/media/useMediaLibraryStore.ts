import { create } from "zustand";
import type { MediaAssetItem } from "../../contracts/generated";
import { deleteMediaAsset, listMediaAssets } from "../../lib/rtClient/media";
import { toActionError, type ActionError } from "../../lib/rtClient/errors";

interface MediaLibraryState {
  drawerOpen: boolean;
  assets: MediaAssetItem[];
  total: number;
  loading: boolean;
  deletingId: string | null;
  error: ActionError | Error | null;
  kindFilter: string;
  searchQuery: string;

  openDrawer: () => void;
  closeDrawer: () => void;
  toggleDrawer: () => void;
  setKindFilter: (kind: string) => void;
  setSearchQuery: (query: string) => void;
  receiveAssetsChanged: () => void;
  fetchAssets: () => Promise<void>;
  deleteAsset: (id: string, deleteFile?: boolean) => Promise<void>;
}

export const useMediaLibraryStore = create<MediaLibraryState>((set, get) => ({
  drawerOpen: false,
  assets: [],
  total: 0,
  loading: false,
  deletingId: null,
  error: null,
  kindFilter: "",
  searchQuery: "",

  openDrawer: () => {
    set({ drawerOpen: true });
    void get().fetchAssets();
  },

  closeDrawer: () => set({ drawerOpen: false }),

  toggleDrawer: () => {
    const next = !get().drawerOpen;
    set({ drawerOpen: next });
    if (next) {
      void get().fetchAssets();
    }
  },

  setKindFilter: (kindFilter) => {
    set({ kindFilter });
    void get().fetchAssets();
  },

  setSearchQuery: (searchQuery) => {
    set({ searchQuery });
    void get().fetchAssets();
  },

  receiveAssetsChanged: () => {
    if (get().drawerOpen || get().assets.length > 0) {
      void get().fetchAssets();
    }
  },

  fetchAssets: async () => {
    set({ loading: true, error: null });
    try {
      const { kindFilter, searchQuery } = get();
      const res = await listMediaAssets({
        kind: kindFilter || undefined,
        search: searchQuery.trim() || undefined,
      });
      set({ assets: res.assets || [], total: res.total || 0, loading: false });
    } catch (err) {
      set({ error: toActionError(err), loading: false });
    }
  },

  deleteAsset: async (id: string, deleteFile = false) => {
    set({ deletingId: id });
    try {
      await deleteMediaAsset({ id, delete_file: deleteFile });
      // Optimistically remove from local assets
      set((s) => ({
        assets: s.assets.filter((a) => a.id !== id),
        total: Math.max(0, s.total - 1),
        deletingId: null,
      }));
    } catch (err) {
      set({ error: toActionError(err), deletingId: null });
      throw err;
    }
  },
}));
