import { create } from "zustand";
import {
  Actions,
  LanguageValues,
  ThemeValues,
  type Language,
  type Preferences,
  type PreferencesPatch,
  type Theme,
} from "../../contracts/generated";
import { appConfig } from "../../config/appConfig";
import { rtClient, toActionError, type ActionError } from "../../lib/rtClient";
import { safeStorage } from "../../lib/storage";
import { applyTheme } from "./theme";

const pick = <T extends string>(values: readonly T[], v: string | null, fallback: T): T =>
  v !== null && (values as readonly string[]).includes(v) ? (v as T) : fallback;

/** Hints cached locally so the first paint already uses the user's theme/language. */
function initialPrefs(): Preferences {
  const { theme, language } = appConfig.initialPreferences;
  return {
    theme: pick<Theme>(ThemeValues, safeStorage.get(appConfig.storageKeys.themeHint), theme),
    language: pick<Language>(
      LanguageValues,
      safeStorage.get(appConfig.storageKeys.languageHint),
      language
    ),
  };
}

function commit(p: Preferences): void {
  applyTheme(p.theme);
  document.documentElement.lang = p.language;
  safeStorage.set(appConfig.storageKeys.themeHint, p.theme);
  safeStorage.set(appConfig.storageKeys.languageHint, p.language);
}

interface PreferencesState {
  prefs: Preferences;
  /** True while a local change has not been saved to the core yet. */
  dirty: boolean;
  error: ActionError | null;
  /** Syncs with the core after (re)connecting: pushes unsaved local changes, else pulls. */
  load: () => Promise<void>;
  /** Applies the change immediately in the UI, then persists it when connected. */
  update: (patch: PreferencesPatch) => Promise<void>;
  /** Applies a server-pushed change (preferences:updated). */
  receive: (p: Preferences) => void;
}

export const usePreferencesStore = create<PreferencesState>((set, get) => {
  const prefs = initialPrefs();
  commit(prefs);

  const persist = async (patch: PreferencesPatch) => {
    try {
      const saved = await rtClient.request(Actions.PREFERENCES_UPDATE, patch);
      commit(saved);
      set({ prefs: saved, dirty: false, error: null });
    } catch (err) {
      // Keep the local choice; it is retried on the next successful connection.
      set({ error: toActionError(err) });
    }
  };

  return {
    prefs,
    dirty: false,
    error: null,

    load: async () => {
      if (get().dirty) {
        const { theme, language } = get().prefs;
        await persist({ theme, language });
        return;
      }
      try {
        const p = await rtClient.request(Actions.PREFERENCES_GET, {});
        commit(p);
        set({ prefs: p, error: null });
      } catch (err) {
        set({ error: toActionError(err) });
      }
    },

    update: async (patch) => {
      const next: Preferences = { ...get().prefs, ...patch };
      commit(next);
      set({ prefs: next, dirty: true, error: null });
      if (rtClient.getStatus() === "connected") await persist(patch);
    },

    receive: (p) => {
      commit(p);
      set({ prefs: p });
    },
  };
});
