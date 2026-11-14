import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { Actions } from "../../contracts/generated";
import { rtClient } from "../../lib/rtClient";
import { safeStorage } from "../../lib/storage";
import { WIDTH_STORAGE_KEY, clampDrawerWidth, useAgentDrawerStore } from "./useAgentDrawerStore";

describe("useAgentDrawerStore", () => {
  beforeEach(() => {
    useAgentDrawerStore.setState({
      panelOpen: false,
      panelWidth: 420,
      activeTab: "chat",
      chatView: "chat",
    });
    vi.restoreAllMocks();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("shows the agent panel when the app starts", () => {
    expect(useAgentDrawerStore.getInitialState().panelOpen).toBe(true);
  });

  it("toggles drawer open state and clamps drawer width", () => {
    const store = useAgentDrawerStore.getState();
    expect(store.panelOpen).toBe(false);
    store.togglePanel();
    expect(useAgentDrawerStore.getState().panelOpen).toBe(true);
    store.closePanel();
    expect(useAgentDrawerStore.getState().panelOpen).toBe(false);
    store.openPanel();
    expect(useAgentDrawerStore.getState().panelOpen).toBe(true);

    expect(clampDrawerWidth(100)).toBe(340);
    expect(clampDrawerWidth(9999)).toBe(680);
    expect(clampDrawerWidth(Number.NaN)).toBe(420);
  });

  it("sets clamped panel width and persists only when requested", () => {
    const setSpy = vi.spyOn(safeStorage, "set").mockImplementation(() => {});
    useAgentDrawerStore.getState().setPanelWidth(100);
    expect(useAgentDrawerStore.getState().panelWidth).toBe(340);
    expect(setSpy).not.toHaveBeenCalled();

    useAgentDrawerStore.getState().setPanelWidth(512.4, true);
    expect(useAgentDrawerStore.getState().panelWidth).toBe(512);
    expect(setSpy).toHaveBeenCalledWith(WIDTH_STORAGE_KEY, "512");
  });

  it("switches activeTab and sets chatView purely without RPC side effects", () => {
    const reqSpy = vi.spyOn(rtClient, "request");
    useAgentDrawerStore.getState().setActiveTab("connections");
    expect(useAgentDrawerStore.getState().activeTab).toBe("connections");

    useAgentDrawerStore.getState().setChatView("history");
    expect(useAgentDrawerStore.getState().chatView).toBe("history");
    expect(reqSpy).not.toHaveBeenCalled();
  });

  it("syncs UI context and ignores request failures", async () => {
    const reqSpy = vi.spyOn(rtClient, "request").mockResolvedValueOnce({});
    await useAgentDrawerStore.getState().setUIContext({ route: "/", note: "", screen: "Home" });
    expect(reqSpy).toHaveBeenCalledWith(Actions.AGENT_SET_UI_CONTEXT, {
      route: "/",
      note: "",
      screen: "Home",
    });

    reqSpy.mockRejectedValueOnce(new Error("disconnected"));
    await expect(
      useAgentDrawerStore.getState().setUIContext({ route: "/", note: "" })
    ).resolves.toBeUndefined();
  });
});
