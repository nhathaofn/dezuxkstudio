import { create } from "zustand";
import { rtClient } from "../services/rtClient";
import type { ConnectionStatus } from "../schemas/realtime.schema";
import { useLogStreamStore } from "./useLogStreamStore";

interface RealtimeState {
  status: ConnectionStatus;
  latency: number | null;
  currentUrl: string;
  isInitialized: boolean;

  // Actions
  init: () => Promise<void>;
  resetToAuto: () => Promise<void>;
  updateUrl: (newUrl: string) => void;
  runPing: () => Promise<void>;
  sendAction: <T = unknown>(action: string, payload?: unknown) => Promise<T>;
  onEvent: (event: string, handler: (data: unknown, refId?: string) => void) => () => void;
}

export const useRealtimeStore = create<RealtimeState>((set, get) => ({
  status: "disconnected",
  latency: null,
  currentUrl: "",
  isInitialized: false,

  init: async () => {
    if (get().isInitialized) return;

    // Listen for status changes
    rtClient.onStatusChange((newStatus) => {
      set({ status: newStatus });
      if (newStatus === "connected") {
        get().runPing();
      } else {
        set({ latency: null });
      }
    });

    const resolvedUrl = await rtClient.init();
    set({ currentUrl: resolvedUrl, isInitialized: true });

    // Automatically stream real-time events from Go server into UI logs
    rtClient.on("progress", (data: unknown) => {
      const payload = data as Record<string, unknown> | null;
      const msg = payload?.message ? String(payload.message) : JSON.stringify(data);
      useLogStreamStore.getState().addLog("info", `[Progress Stream] ${msg}`);
    });
  },

  resetToAuto: async () => {
    const resolvedUrl = await rtClient.init();
    set({ currentUrl: resolvedUrl });
  },

  updateUrl: (newUrl: string) => {
    set({ currentUrl: newUrl });
    rtClient.setTargetUrl(newUrl);
  },

  runPing: async () => {
    try {
      const ms = await rtClient.ping();
      set({ latency: ms });
    } catch {
      set({ latency: null });
    }
  },

  sendAction: <T = unknown>(action: string, payload?: unknown) => {
    return rtClient.sendAction<T>(action, payload);
  },

  onEvent: (event: string, handler: (data: unknown, refId?: string) => void) => {
    return rtClient.on(event, handler);
  },
}));
