import { create } from "zustand";
import {
  Actions,
  type FolderTarget,
  type SystemInfo,
  type UpdateInfo,
} from "../../contracts/generated";
import { rtClient, toActionError, type ActionError } from "../../lib/rtClient";

interface SystemState {
  info: SystemInfo | null;
  infoError: ActionError | null;
  update: UpdateInfo | null;
  updateError: ActionError | null;
  checkingUpdate: boolean;
  loadInfo: () => Promise<void>;
  checkUpdate: () => Promise<void>;
  openFolder: (target: FolderTarget) => Promise<ActionError | null>;
}

export const useSystemStore = create<SystemState>((set, get) => ({
  info: null,
  infoError: null,
  update: null,
  updateError: null,
  checkingUpdate: false,

  loadInfo: async () => {
    try {
      set({ info: await rtClient.request(Actions.SYSTEM_INFO, {}), infoError: null });
    } catch (err) {
      set({ infoError: toActionError(err) });
    }
  },

  checkUpdate: async () => {
    if (get().checkingUpdate) return;
    set({ checkingUpdate: true });
    try {
      set({ update: await rtClient.request(Actions.SYSTEM_CHECK_UPDATE, {}), updateError: null });
    } catch (err) {
      set({ updateError: toActionError(err) });
    } finally {
      set({ checkingUpdate: false });
    }
  },

  openFolder: async (target) => {
    try {
      await rtClient.request(Actions.SYSTEM_OPEN_FOLDER, { target });
      return null;
    } catch (err) {
      return toActionError(err);
    }
  },
}));
