import { beforeEach, describe, expect, it, vi } from "vitest";
import React from "react";
import { renderToStaticMarkup } from "react-dom/server";
import type { SystemInfo } from "../../contracts/generated";
import { useSystemStore } from "../system/useSystemStore";
import { usePreferencesStore } from "../preferences/usePreferencesStore";
import { useConnectionStore } from "../connection/useConnectionStore";
import { useAgentCustomizationsStore } from "./useAgentCustomizationsStore";
import { useAgentDrawerStore } from "./useAgentDrawerStore";
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
  useSystemStore.setState({
    info: {
      mode,
      paths: {
        workspace_root: "D:/MyMasterWorkspace",
      },
    } as unknown as SystemInfo,
  });
  return renderToStaticMarkup(React.createElement(AgentDrawer));
}

// Trusted folders can only be changed where the core accepts it: the desktop app on a local core.
describe("AgentDrawer trusted folders", () => {
  beforeEach(() => {
    useConnectionStore.setState({ status: "connected" });
    usePreferencesStore.setState({
      prefs: {
        theme: "light",
        language: "en",
        workspace_root: "D:/MyMasterWorkspace",
        onboarding_completed: true,
        agent_skip_trusted_approval: false,
      },
      loaded: true,
      dirty: false,
      error: null,
    });
    useAgentDrawerStore.setState({
      panelOpen: true,
      activeTab: "connections",
    });
    useAgentStore.setState({
      allowedDirs: ["D:/Ảnh/Dự án tháng 11"],
      error: null,
    });
    useAgentCustomizationsStore.setState({ error: null });
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

  it("displays pinned badge for the master workspace root without remove button", () => {
    const html = render(true, "desktop");
    expect(html).toContain("Master Root");
    expect(html).toContain("D:/MyMasterWorkspace");
    expect(html).toContain("Configure workspace root in Settings");
    // Verify pinned folder element does NOT have remove button
    expect(html).toContain("agent-folder-pinned");
  });

  it("displays the auto-approve switch and description", () => {
    const html = render(true, "desktop");
    expect(html).toContain("Auto-approve file overwrite operations in trusted folders");
    expect(html).toContain("File overwrites in trusted folders skip approval");
    expect(html).toContain('id="agent-skip-trusted-approval-switch"');
    // In desktop mode, switch is enabled (not disabled)
    expect(html).not.toMatch(/id="agent-skip-trusted-approval-switch"[^>]*disabled=""/);
  });

  it("disables the auto-approve switch in server mode", () => {
    const html = render(false, "server");
    expect(html).toMatch(/id="agent-skip-trusted-approval-switch"[^>]*disabled=""/);
  });

  it("reflects agent_skip_trusted_approval checked and unchecked states in rendered switch", () => {
    // Unchecked state (false)
    usePreferencesStore.setState({
      prefs: {
        theme: "light",
        language: "en",
        workspace_root: "D:/MyMasterWorkspace",
        onboarding_completed: true,
        agent_skip_trusted_approval: false,
      },
    });
    let html = render(true, "desktop");
    expect(html).toContain('id="agent-skip-trusted-approval-switch"');
    expect(html).toContain('role="switch"');
    expect(html).toContain('aria-checked="false"');

    // Checked state (true)
    usePreferencesStore.setState({
      prefs: {
        theme: "light",
        language: "en",
        workspace_root: "D:/MyMasterWorkspace",
        onboarding_completed: true,
        agent_skip_trusted_approval: true,
      },
    });
    html = render(true, "desktop");
    expect(html).toContain('aria-checked="true"');
  });
});
