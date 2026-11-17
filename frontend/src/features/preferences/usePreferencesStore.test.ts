import { describe, expect, it, beforeEach, vi } from "vitest";
import { Actions, type Preferences } from "../../contracts/generated";
import { appConfig } from "../../config/appConfig";
import { safeStorage } from "../../lib/storage";
import { rtClient, ActionError } from "../../lib/rtClient";
import { useSystemStore } from "../system/useSystemStore";
import { useAgentStore } from "../agent/useAgentStore";
import { usePreferencesStore } from "./usePreferencesStore";

// Mock localStorage in Node test environment
const storage = new Map<string, string>();
const localStorageMock = {
  getItem: (k: string) => storage.get(k) ?? null,
  setItem: (k: string, v: string) => {
    storage.set(k, String(v));
  },
  removeItem: (k: string) => {
    storage.delete(k);
  },
  clear: () => {
    storage.clear();
  },
};

(globalThis as unknown as { window: { localStorage: typeof localStorageMock } }).window = {
  localStorage: localStorageMock,
};

describe("usePreferencesStore", () => {
  beforeEach(() => {
    storage.clear();
    vi.restoreAllMocks();
    usePreferencesStore.setState({
      prefs: {
        theme: "light",
        language: "en",
        workspace_root: "",
        onboarding_completed: false,
        agent_skip_trusted_approval: false,
      },
      loaded: false,
      dirty: false,
      error: null,
    });
  });

  it("initializes with default schema v2 properties", () => {
    const state = usePreferencesStore.getState();
    expect(state.prefs.theme).toBe("light");
    expect(state.prefs.language).toBe("en");
    expect(state.prefs.workspace_root).toBe("");
    expect(state.prefs.onboarding_completed).toBe(false);
    expect(state.prefs.agent_skip_trusted_approval).toBe(false);
    expect(state.loaded).toBe(false);
  });

  it("updates theme and language locally via update()", async () => {
    await usePreferencesStore.getState().update({ theme: "dark", language: "vi" });
    const state = usePreferencesStore.getState();
    expect(state.prefs.theme).toBe("dark");
    expect(state.prefs.language).toBe("vi");
    expect(safeStorage.get(appConfig.storageKeys.themeHint)).toBe("dark");
    expect(safeStorage.get(appConfig.storageKeys.languageHint)).toBe("vi");
  });

  it("sets loaded to true upon successful load()", async () => {
    const serverPrefs: Preferences = {
      theme: "dark",
      language: "vi",
      workspace_root: "D:/MyStudio",
      onboarding_completed: true,
      agent_skip_trusted_approval: true,
    };
    vi.spyOn(rtClient, "request").mockResolvedValueOnce(serverPrefs);

    expect(usePreferencesStore.getState().loaded).toBe(false);
    await usePreferencesStore.getState().load();

    const state = usePreferencesStore.getState();
    expect(state.loaded).toBe(true);
    expect(state.prefs.workspace_root).toBe("D:/MyStudio");
    expect(state.prefs.onboarding_completed).toBe(true);
    expect(state.prefs.agent_skip_trusted_approval).toBe(true);
    expect(rtClient.request).toHaveBeenCalledWith(Actions.PREFERENCES_GET, {});
  });

  it("triggers loadInfo() and fetchAllowedDirs() when workspace_root changes via update()", async () => {
    const loadInfoSpy = vi.spyOn(useSystemStore.getState(), "loadInfo").mockResolvedValue();
    const fetchDirsSpy = vi.spyOn(useAgentStore.getState(), "fetchAllowedDirs").mockResolvedValue();

    await usePreferencesStore.getState().update({ workspace_root: "D:/NewWorkspace" });

    expect(usePreferencesStore.getState().prefs.workspace_root).toBe("D:/NewWorkspace");
    expect(loadInfoSpy).toHaveBeenCalledTimes(1);
    expect(fetchDirsSpy).toHaveBeenCalledTimes(1);
  });

  it("does not trigger loadInfo() or fetchAllowedDirs() when workspace_root is unchanged", async () => {
    const loadInfoSpy = vi.spyOn(useSystemStore.getState(), "loadInfo").mockResolvedValue();
    const fetchDirsSpy = vi.spyOn(useAgentStore.getState(), "fetchAllowedDirs").mockResolvedValue();

    await usePreferencesStore.getState().update({ theme: "dark" });

    expect(loadInfoSpy).not.toHaveBeenCalled();
    expect(fetchDirsSpy).not.toHaveBeenCalled();
  });

  it("triggers loadInfo() and fetchAllowedDirs() when workspace_root changes via receive()", () => {
    const loadInfoSpy = vi.spyOn(useSystemStore.getState(), "loadInfo").mockResolvedValue();
    const fetchDirsSpy = vi.spyOn(useAgentStore.getState(), "fetchAllowedDirs").mockResolvedValue();

    const pushedPrefs: Preferences = {
      theme: "light",
      language: "en",
      workspace_root: "D:/ReceivedWorkspace",
      onboarding_completed: true,
      agent_skip_trusted_approval: false,
    };

    usePreferencesStore.getState().receive(pushedPrefs);

    expect(usePreferencesStore.getState().prefs.workspace_root).toBe("D:/ReceivedWorkspace");
    expect(loadInfoSpy).toHaveBeenCalledTimes(1);
    expect(fetchDirsSpy).toHaveBeenCalledTimes(1);
  });

  it("updates agent_skip_trusted_approval via update()", async () => {
    await usePreferencesStore.getState().update({ agent_skip_trusted_approval: true });
    expect(usePreferencesStore.getState().prefs.agent_skip_trusted_approval).toBe(true);
  });

  it("rolls back state, rethrows error, and does not trigger loadInfo/fetchAllowedDirs when persist() fails", async () => {
    vi.spyOn(rtClient, "getStatus").mockReturnValue("connected");
    vi.spyOn(rtClient, "request").mockRejectedValueOnce(
      new ActionError("ERR_VALIDATION", "Root volume not allowed")
    );
    const loadInfoSpy = vi.spyOn(useSystemStore.getState(), "loadInfo").mockResolvedValue();
    const fetchDirsSpy = vi.spyOn(useAgentStore.getState(), "fetchAllowedDirs").mockResolvedValue();

    await expect(
      usePreferencesStore.getState().update({
        workspace_root: "C:\\",
        onboarding_completed: true,
      })
    ).rejects.toThrow("Root volume not allowed");

    const state = usePreferencesStore.getState();
    expect(state.prefs.workspace_root).toBe("");
    expect(state.prefs.onboarding_completed).toBe(false);
    expect(state.dirty).toBe(false);
    expect(state.error?.code).toBe("ERR_VALIDATION");
    expect(state.error?.message).toBe("Root volume not allowed");
    expect(loadInfoSpy).not.toHaveBeenCalled();
    expect(fetchDirsSpy).not.toHaveBeenCalled();
  });

  it("persists update and marks onboarding_completed true when connected and persist succeeds", async () => {
    vi.spyOn(rtClient, "getStatus").mockReturnValue("connected");
    const updatedPrefs: Preferences = {
      theme: "light",
      language: "en",
      workspace_root: "D:/ValidWorkspace",
      onboarding_completed: true,
      agent_skip_trusted_approval: false,
    };
    vi.spyOn(rtClient, "request").mockResolvedValueOnce(updatedPrefs);
    const loadInfoSpy = vi.spyOn(useSystemStore.getState(), "loadInfo").mockResolvedValue();
    const fetchDirsSpy = vi.spyOn(useAgentStore.getState(), "fetchAllowedDirs").mockResolvedValue();

    await usePreferencesStore.getState().update({
      workspace_root: "D:/ValidWorkspace",
      onboarding_completed: true,
    });

    const state = usePreferencesStore.getState();
    expect(state.prefs.workspace_root).toBe("D:/ValidWorkspace");
    expect(state.prefs.onboarding_completed).toBe(true);
    expect(state.dirty).toBe(false);
    expect(state.error).toBeNull();
    expect(loadInfoSpy).toHaveBeenCalledTimes(1);
    expect(fetchDirsSpy).toHaveBeenCalledTimes(1);
  });
});
