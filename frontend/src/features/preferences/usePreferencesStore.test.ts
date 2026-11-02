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

describe("usePreferencesStore gateway settings", () => {
  it("initializes with default gatewayUrl and hasGatewayApiKey", () => {
    const state = usePreferencesStore.getState();
    expect(state.gatewayUrl).toBe("http://127.0.0.1:8080");
    expect(state.hasGatewayApiKey).toBe(false);
    expect(state.prefs.gatewayUrl).toBe("http://127.0.0.1:8080");
    expect(state.prefs.hasGatewayApiKey).toBe(false);
  });

  it("updates gatewayUrl and gatewayApiKey via setters", async () => {
    const { setGatewayUrl, setGatewayApiKey } = usePreferencesStore.getState();
    await setGatewayUrl("http://custom-gw:9000");
    expect(usePreferencesStore.getState().gatewayUrl).toBe("http://custom-gw:9000");
    expect(usePreferencesStore.getState().prefs.gatewayUrl).toBe("http://custom-gw:9000");

    await setGatewayApiKey("sk-secret-key-123");
    expect(usePreferencesStore.getState().hasGatewayApiKey).toBe(true);
    expect(usePreferencesStore.getState().prefs.hasGatewayApiKey).toBe(true);
  });
});
