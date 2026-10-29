import { create } from "zustand";

export type ThemeMode = "light" | "dark";

const STORAGE_KEY = "dezuxk_theme";
const DEFAULT_THEME: ThemeMode = "light";

interface ThemeState {
  theme: ThemeMode;
  setTheme: (theme: ThemeMode) => void;
  toggleTheme: () => void;
  initTheme: () => void;
}

function applyThemeToDOM(theme: ThemeMode) {
  document.documentElement.setAttribute("data-theme", theme);
}

export const useThemeStore = create<ThemeState>((set, get) => ({
  theme: DEFAULT_THEME,

  initTheme: () => {
    const saved = localStorage.getItem(STORAGE_KEY) as ThemeMode | null;
    const initial = saved === "dark" || saved === "light" ? saved : DEFAULT_THEME;
    applyThemeToDOM(initial);
    set({ theme: initial });
  },

  setTheme: (theme: ThemeMode) => {
    localStorage.setItem(STORAGE_KEY, theme);
    applyThemeToDOM(theme);
    set({ theme });
  },

  toggleTheme: () => {
    const next = get().theme === "light" ? "dark" : "light";
    get().setTheme(next);
  },
}));
