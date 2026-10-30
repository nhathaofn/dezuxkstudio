import { create } from "zustand";
import { appConfig } from "../../config/appConfig";

export type LogLevel = "info" | "warn" | "error";

export interface LogEntry {
  id: number;
  timestamp: number;
  level: LogLevel;
  message: string;
}

interface LogState {
  entries: LogEntry[];
  add: (level: LogLevel, message: string) => void;
  clear: () => void;
}

let seq = 0;

/** Bounded in-memory log of UI-visible events (newest last). */
export const useLogStore = create<LogState>((set) => ({
  entries: [],
  add: (level, message) =>
    set((s) => {
      const next = [{ id: ++seq, timestamp: Date.now(), level, message }, ...s.entries];
      return { entries: next.slice(0, appConfig.logBufferSize) };
    }),
  clear: () => set({ entries: [] }),
}));

/** Convenience logger usable outside React components. */
export const uiLog = {
  info: (m: string) => useLogStore.getState().add("info", m),
  warn: (m: string) => useLogStore.getState().add("warn", m),
  error: (m: string) => useLogStore.getState().add("error", m),
};
