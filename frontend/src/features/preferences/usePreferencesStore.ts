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
  /** Pending local field changes made offline that have not been pushed to the core yet. */
  dirtyPatch: PreferencesPatch | null;
  error: ActionError | null;
  /** Syncs with the core after (re)connecting: always pulls latest server prefs, then applies dirtyPatch if any. */
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
        dirtyPatch: null,
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
    dirtyPatch: null,
    error: null,

    load: async () => {
      try {
        const serverPrefs = await rtClient.request(Actions.PREFERENCES_GET, {});
        const pending = get().dirtyPatch;
        if (pending && Object.keys(pending).length > 0) {
          const mergedPatch: PreferencesPatch = {};
          for (const key of Object.keys(pending) as (keyof PreferencesPatch)[]) {
            const val = pending[key];
            if (val !== undefined && serverPrefs[key] !== val) {
              (mergedPatch as Record<string, unknown>)[key] = val;
            }
          }
          if (Object.keys(mergedPatch).length > 0) {
            await persist(mergedPatch);
            set({ loaded: true });
            return;
          }
        }
        commit(serverPrefs);
        set({
          prefs: serverPrefs,
          loaded: true,
          dirty: false,
          dirtyPatch: null,
          error: null,
        });
      } catch (err) {
        set({ error: toActionError(err) });
      }
    },

    update: async (patch) => {
      const prevPrefs = get().prefs;
      const prevRoot = prevPrefs.workspace_root ?? "";
      const prevDirty = get().dirty;
      const prevDirtyPatch = get().dirtyPatch;
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
      const nextPatch = isConnected ? prevDirtyPatch : { ...(prevDirtyPatch ?? {}), ...patch };
      set({
        prefs: next,
        dirty: !isConnected || prevDirty,
        dirtyPatch: nextPatch,
        error: null,
      });

      if (isConnected) {
        try {
          await persist(patch);
        } catch (err) {
          commit(prevPrefs as Preferences);
          set({
            prefs: prevPrefs,
            dirty: prevDirty,
            dirtyPatch: prevDirtyPatch,
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
        dirtyPatch: null,
        error: null,
      });
      if (p.workspace_root !== prevRoot) {
        void useSystemStore.getState().loadInfo();
        void useAgentStore.getState().fetchAllowedDirs();
      }
    },
  };
});
