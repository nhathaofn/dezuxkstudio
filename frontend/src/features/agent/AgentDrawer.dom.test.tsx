// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render } from "@testing-library/react";

import { AgentDrawer } from "./AgentDrawer";
import { useAgentDrawerStore } from "./useAgentDrawerStore";
import { useConnectionStore } from "../connection/useConnectionStore";

describe("AgentDrawer pointer resize cleanup", () => {
  afterEach(() => {
    vi.restoreAllMocks();
    useAgentDrawerStore.setState({ panelOpen: false, panelWidth: 420 });
  });

  it("cleans up window pointer listeners when unmounted mid-resize drag", () => {
    useConnectionStore.setState({ status: "idle" });
    useAgentDrawerStore.setState({ panelOpen: true, panelWidth: 420, activeTab: "connections" });
    const removeSpy = vi.spyOn(window, "removeEventListener");

    const { container, unmount } = render(<AgentDrawer />);
    const handle = container.querySelector(".agent-drawer__resizer") as HTMLElement;
    expect(handle).toBeTruthy();

    fireEvent.pointerDown(handle, { clientX: 500 });
    fireEvent(window, new MouseEvent("pointermove", { clientX: 460 }));
    expect(useAgentDrawerStore.getState().panelWidth).toBeGreaterThanOrEqual(420);

    const widthBeforeUnmount = useAgentDrawerStore.getState().panelWidth;
    unmount();

    expect(removeSpy).toHaveBeenCalledWith("pointermove", expect.any(Function));
    expect(removeSpy).toHaveBeenCalledWith("pointerup", expect.any(Function));
    expect(removeSpy).toHaveBeenCalledWith("pointercancel", expect.any(Function));

    fireEvent(window, new MouseEvent("pointermove", { clientX: 300 }));
    expect(useAgentDrawerStore.getState().panelWidth).toBe(widthBeforeUnmount);
  });
});
