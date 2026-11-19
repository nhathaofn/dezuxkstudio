import { create } from "zustand";
import { appConfig } from "../../config/appConfig";
import { getBridge } from "../../lib/bridge";
import {
  resolveEndpoint,
  rtClient,
  toActionError,
  type ActionError,
  type ConnectionStatus,
  type Endpoint,
} from "../../lib/rtClient";
import { safeSessionStorage, safeStorage } from "../../lib/storage";

interface ConnectionState {
  status: ConnectionStatus;
  endpoint: Endpoint | null;
  /** True once endpoint resolution has finished (endpoint may still be null). */
  resolved: boolean;
  latencyMs: number | null;
  /** Set when the core cannot start; the UI shows the fatal screen. */
  fatal: ActionError | null;
  connect: () => Promise<void>;
  /** Browser mode only: pin a manual endpoint. */
  setManualUrl: (url: string) => Promise<void>;
  /** Browser mode only: forget the manual endpoint. */
  resetToAuto: () => Promise<void>;
  /** Saves an authentication token for remote server connection (--mode client or browser mode) and reconnects. */
  setRemoteToken: (token: string) => Promise<void>;
  setLatency: (ms: number | null) => void;
  setFatal: (err: ActionError) => void;
}

async function fetchJson(path: string): Promise<unknown> {
  const res = await fetch(path, { cache: "no-store" });
  if (!res.ok) throw new Error(`HTTP ${res.status}`);
  return res.json();
}

export const useConnectionStore = create<ConnectionState>((set, get) => ({
  status: rtClient.getStatus(),
  endpoint: null,
  resolved: false,
  latencyMs: null,
  fatal: null,

  connect: async () => {
    try {
      const endpoint = await resolveEndpoint({
        bridge: getBridge(),
        location: window.location,
        storage: { getItem: (k) => safeStorage.get(k) },
        sessionToken: safeSessionStorage.get(appConfig.storageKeys.remoteToken),
        fetchJson,
        dev: import.meta.env.DEV,
      });
      set({ endpoint, resolved: true });
      if (endpoint) rtClient.connect(endpoint);
      else rtClient.disconnect();
    } catch (err) {
      get().setFatal(toActionError(err));
    }
  },

  setManualUrl: async (url) => {
    safeStorage.set(appConfig.storageKeys.manualWsUrl, url);
    await get().connect();
  },

  resetToAuto: async () => {
    safeStorage.remove(appConfig.storageKeys.manualWsUrl);
    await get().connect();
  },

  setRemoteToken: async (token) => {
    const clean = token.trim();
    const bridge = getBridge();
    if (bridge) {
      await bridge.SetRemoteToken(clean);
    } else if (clean) {
      safeSessionStorage.set(appConfig.storageKeys.remoteToken, clean);
    } else {
      safeSessionStorage.remove(appConfig.storageKeys.remoteToken);
    }
    await get().connect();
  },

  setLatency: (ms) => set({ latencyMs: ms }),

  setFatal: (err) => {
    rtClient.disconnect();
    set({ fatal: err });
  },
}));
