import { create } from "zustand";

export interface LogEntry {
  id: string;
  timestamp: number;
  level: "info" | "warn" | "error";
  message: string;
}

const MAX_LOG_BUFFER = 500;

interface LogStreamState {
  logs: LogEntry[];
  addLog: (level: LogEntry["level"], message: string) => void;
  clearLogs: () => void;
}

export const useLogStreamStore = create<LogStreamState>((set) => ({
  logs: [],

  addLog: (level, message) => {
    const entry: LogEntry = {
      id: "log_" + Math.random().toString(36).substring(2, 9) + "_" + Date.now().toString(36),
      timestamp: Date.now(),
      level,
      message,
    };

    set((state) => {
      const next = [...state.logs, entry];
      if (next.length > MAX_LOG_BUFFER) {
        return { logs: next.slice(next.length - MAX_LOG_BUFFER) };
      }
      return { logs: next };
    });
  },

  clearLogs: () => set({ logs: [] }),
}));
