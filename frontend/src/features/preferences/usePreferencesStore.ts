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
import { useSystemStore } from "../system/useSystemStore";
import { useAgentStore } from "../agent/useAgentStore";
import { applyTheme } from "./theme";

const pick = <T extends string>(values: readonly T[], v: string | null, fallback: T): T =>
  v !== null && (values as readonly string[]).includes(v) ? (v as T) : fallback;

/** Hints cached locally so the first paint already uses the user's theme/language. */
function initialPrefs(): Preferences {
  const { theme, language, workspace_root, onboarding_completed, agent_skip_trusted_approval } =
    appConfig.initialPreferences;
  return {
    theme: pick<Theme>(ThemeValues, safeStorage.get(appConfig.storageKeys.themeHint), theme),
    language: pick<Language>(
      LanguageValues,
      safeStorage.get(appConfig.storageKeys.languageHint),
      language
    ),
    workspace_root,
    onboarding_completed,
    agent_skip_trusted_approval,
  };
}

function commit(p: Preferences): void {
  if (typeof document !== "undefined") {
    applyTheme(p.theme);
    document.documentElement.lang = p.language;
  }
  safeStorage.set(appConfig.storageKeys.themeHint, p.theme);
  safeStorage.set(appConfig.storageKeys.languageHint, p.language);
}

export type StorePreferences = {
  theme: Theme;
  language: Language;
  workspace_root?: string;
  onboarding_completed?: boolean;
  agent_skip_trusted_approval?: boolean;
};

interface PreferencesState {
  prefs: StorePreferences;
  /** True after the first successful load() response from preferences:get. */
  loaded: boolean;
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

  const persist = async (patch: PreferencesPatch): Promise<Preferences> => {
    try {
      const saved = await rtClient.request(Actions.PREFERENCES_UPDATE, patch);
      commit(saved);
      set({
        prefs: saved,
        dirty: false,
        error: null,
      });
      return saved;
    } catch (err) {
      const actionErr = toActionError(err);
      set({ error: actionErr });
      throw actionErr;
    }
  };

  return {
    prefs,
    loaded: false,
    dirty: false,
    error: null,

    load: async () => {
      if (get().dirty) {
        const {
          theme,
          language,
          workspace_root,
          onboarding_completed,
          agent_skip_trusted_approval,
        } = get().prefs;
        try {
          await persist({
            theme,
            language,
            workspace_root,
            onboarding_completed,
            agent_skip_trusted_approval,
          });
          set({ loaded: true });
        } catch (err) {
          set({ loaded: true, error: toActionError(err) });
        }
        return;
      }
      try {
        const p = await rtClient.request(Actions.PREFERENCES_GET, {});
        commit(p);
        set({
          prefs: p,
          loaded: true,
          error: null,
        });
      } catch (err) {
        set({ error: toActionError(err) });
      }
    },

    update: async (patch) => {
      const prevPrefs = get().prefs;
      const prevRoot = prevPrefs.workspace_root ?? "";
      const isConnected = rtClient.getStatus() === "connected";

      // If connected, defer setting onboarding_completed in the store until persist()
      // succeeds so WorkspaceOnboardingDialog remains mounted while saving=true.
      const optimisticPatch = { ...patch };
      if (isConnected && patch.onboarding_completed && !prevPrefs.onboarding_completed) {
        delete optimisticPatch.onboarding_completed;
      }

      const next: StorePreferences = {
        workspace_root: "",
        onboarding_completed: false,
        agent_skip_trusted_approval: false,
        ...prevPrefs,
        ...optimisticPatch,
      };
      commit(next as Preferences);
      set({
        prefs: next,
        dirty: true,
        error: null,
      });

      if (isConnected) {
        try {
          await persist(patch);
        } catch (err) {
          commit(prevPrefs as Preferences);
          set({
            prefs: prevPrefs,
            dirty: false,
            error: toActionError(err),
          });
          throw err;
        }
      }

      if (patch.workspace_root !== undefined && patch.workspace_root !== prevRoot) {
        void useSystemStore.getState().loadInfo();
        void useAgentStore.getState().fetchAllowedDirs();
      }
    },

    receive: (p) => {
      const prevRoot = get().prefs.workspace_root ?? "";
      commit(p);
      set({
        prefs: p,
        dirty: false,
        error: null,
      });
      if (p.workspace_root !== prevRoot) {
        void useSystemStore.getState().loadInfo();
        void useAgentStore.getState().fetchAllowedDirs();
      }
    },
  };
});
