import { create } from "zustand";
import { appConfig } from "../../config/appConfig";
import { Actions, type AgentUIContextPayload } from "../../contracts/generated";
import { rtClient } from "../../lib/rtClient";
import { safeStorage } from "../../lib/storage";

export type AgentDrawerTab = "chat" | "connections" | "customizations";
/** What the chat tab shows: the current chat or the list of earlier chats. */
export type AgentChatView = "chat" | "history";

export const WIDTH_STORAGE_KEY = appConfig.storageKeys.agentDrawerWidth;
const {
  defaultWidth: DEFAULT_WIDTH,
  minWidth: MIN_WIDTH,
  maxWidth: MAX_WIDTH,
} = appConfig.ui.agentDrawer;

export function clampDrawerWidth(width: number): number {
  if (!Number.isFinite(width)) return DEFAULT_WIDTH;
  return Math.min(MAX_WIDTH, Math.max(MIN_WIDTH, Math.round(width)));
}

function readSavedWidth(): number {
  if (typeof window === "undefined") return DEFAULT_WIDTH;
  const raw = safeStorage.get(WIDTH_STORAGE_KEY);
  if (!raw) return DEFAULT_WIDTH;
  return clampDrawerWidth(Number(raw));
}

interface AgentDrawerState {
  panelOpen: boolean;
  panelWidth: number;
  activeTab: AgentDrawerTab;
  chatView: AgentChatView;

  openPanel: () => void;
  closePanel: () => void;
  togglePanel: () => void;
  setPanelWidth: (width: number, persist?: boolean) => void;
  setActiveTab: (tab: AgentDrawerTab) => void;
  setChatView: (view: AgentChatView) => void;
  setUIContext: (ctx: AgentUIContextPayload) => Promise<void>;
}

export const useAgentDrawerStore = create<AgentDrawerState>((set) => ({
  panelOpen: true,
  panelWidth: readSavedWidth(),
  activeTab: "chat",
  chatView: "chat",

  openPanel: () => set({ panelOpen: true }),
  closePanel: () => set({ panelOpen: false }),
  togglePanel: () => set((s) => ({ panelOpen: !s.panelOpen })),

  setPanelWidth: (width, persist = false) => {
    const next = clampDrawerWidth(width);
    if (persist) {
      safeStorage.set(WIDTH_STORAGE_KEY, String(next));
    }
    set({ panelWidth: next });
  },

  setActiveTab: (activeTab) => set({ activeTab }),
  setChatView: (chatView) => set({ chatView }),

  setUIContext: async (ctx) => {
    try {
      await rtClient.request(Actions.AGENT_SET_UI_CONTEXT, ctx);
    } catch {
      // Ignore UI context sync failures when disconnected
    }
  },
}));
