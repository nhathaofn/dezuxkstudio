import { beforeEach, describe, expect, it, vi } from "vitest";
import React from "react";
import { renderToStaticMarkup } from "react-dom/server";
import type { SystemInfo } from "../../contracts/generated";
import { useSystemStore } from "../system/useSystemStore";
import { useAgentStore } from "./useAgentStore";

const bridge = vi.hoisted(() => ({ desktop: false }));
vi.mock("../../lib/bridge", async (orig) => ({
  ...(await orig<typeof import("../../lib/bridge")>()),
  isDesktop: () => bridge.desktop,
}));

vi.spyOn(React, "useSyncExternalStore").mockImplementation((_s, getSnapshot) => getSnapshot());

const { AgentDrawer } = await import("./AgentDrawer");

function render(desktop: boolean, mode: SystemInfo["mode"]): string {
  bridge.desktop = desktop;
  useSystemStore.setState({ info: { mode } as SystemInfo });
  return renderToStaticMarkup(React.createElement(AgentDrawer));
}

// Trusted folders can only be changed where the core accepts it: the desktop app on a local core.
describe("AgentDrawer trusted folders", () => {
  beforeEach(() => {
    useAgentStore.setState({
      panelOpen: true,
      activeTab: "connections",
      allowedDirs: ["D:/Ảnh/Dự án tháng 11"],
      error: null,
    });
  });

  it("lists folders read-only in a browser", () => {
    const html = render(false, "server");
    expect(html).toContain("D:/Ảnh/Dự án tháng 11");
    expect(html).not.toContain('aria-label="Remove"');
    expect(html).not.toContain("Add Folder…");
  });

  it("lists folders read-only in the desktop app connected to a headless server", () => {
    const html = render(true, "server");
    expect(html).not.toContain('aria-label="Remove"');
    expect(html).not.toContain("Add Folder…");
  });

  it("lets the desktop app on its own core add and remove folders", () => {
    const html = render(true, "desktop");
    expect(html).toContain('aria-label="Remove"');
    expect(html).toContain("Add Folder…");
  });
});
