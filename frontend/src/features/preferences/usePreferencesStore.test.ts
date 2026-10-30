import { describe, expect, it, beforeEach } from "vitest";
import { appConfig } from "../../config/appConfig";
import { safeStorage } from "../../lib/storage";
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

// Assign to globalThis
(globalThis as unknown as { window: { localStorage: typeof localStorageMock } }).window = {
  localStorage: localStorageMock,
};

describe("usePreferencesStore devMode", () => {
  beforeEach(() => {
    storage.clear();
    safeStorage.remove(appConfig.storageKeys.devMode);
    usePreferencesStore.setState({ devMode: false });
  });

  it("defaults devMode to false when storage is unset", () => {
    expect(usePreferencesStore.getState().devMode).toBe(false);
  });

  it("toggles devMode and persists to safeStorage", () => {
    const { toggleDevMode } = usePreferencesStore.getState();
    toggleDevMode();

    expect(usePreferencesStore.getState().devMode).toBe(true);
    expect(safeStorage.get(appConfig.storageKeys.devMode)).toBe("true");

    toggleDevMode();
    expect(usePreferencesStore.getState().devMode).toBe(false);
    expect(safeStorage.get(appConfig.storageKeys.devMode)).toBe("false");
  });

  it("sets devMode explicitly with setDevMode", () => {
    const { setDevMode } = usePreferencesStore.getState();
    setDevMode(true);

    expect(usePreferencesStore.getState().devMode).toBe(true);
    expect(safeStorage.get(appConfig.storageKeys.devMode)).toBe("true");

    setDevMode(false);
    expect(usePreferencesStore.getState().devMode).toBe(false);
    expect(safeStorage.get(appConfig.storageKeys.devMode)).toBe("false");
  });
});
