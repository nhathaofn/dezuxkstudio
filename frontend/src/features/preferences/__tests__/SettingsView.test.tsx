import React from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { Language, SystemInfo } from "../../../contracts/generated";
import { ActionError } from "../../../lib/rtClient";

const bridgeMock = vi.hoisted(() => ({
  desktop: true,
  browseDirectory: vi.fn<() => Promise<string>>(),
}));

vi.mock("../../../lib/bridge", () => ({
  isDesktop: () => bridgeMock.desktop,
  getBridge: () =>
    bridgeMock.desktop
      ? {
          BrowseDirectory: bridgeMock.browseDirectory,
        }
      : null,
}));

vi.mock("react-router-dom", () => ({
  useSearchParams: () => [new URLSearchParams("tab=general"), vi.fn()],
}));

// Make Zustand hooks read live state snapshots during SSR renderToStaticMarkup
// instead of falling back to the un-mutated initial store snapshot.
vi.spyOn(React, "useSyncExternalStore").mockImplementation((_subscribe, getSnapshot) =>
  getSnapshot()
);

const { useConnectionStore } = await import("../../connection/useConnectionStore");
const { useSystemStore } = await import("../../system/useSystemStore");
const { usePreferencesStore } = await import("../usePreferencesStore");
const { SettingsView } = await import("../SettingsView");

function makeSystemInfo(mode: "desktop" | "server", workspaceRoot: string): SystemInfo {
  return {
    mode,
    build: {
      app_id: "com.dezuxk.studio",
      product_name: "Dezuxk Studio",
      version: "0.1.0",
      commit: "abc1234",
      build_date: "2026-01-01T00:00:00Z",
      go_version: "go1.24",
      os: "windows",
      arch: "amd64",
      dev: false,
    },
    runtime: {
      num_cpu: 8,
      num_goroutine: 12,
      alloc_mb: 24.5,
      sys_mb: 64,
      gc_cycles: 3,
      uptime_seconds: 120,
    },
    paths: {
      config: "C:/Data/config",
      data: "C:/Data",
      logs: "C:/Data/logs",
      diagnostics: "C:/Data/diagnostics",
      workspace_root: workspaceRoot,
      suggested_workspace_root: "D:/DefaultStudioRoot",
      media_dir: workspaceRoot ? `${workspaceRoot}/media` : "",
      watermark_dir: workspaceRoot ? `${workspaceRoot}/media/watermark` : "",
      agent_workspace: workspaceRoot ? `${workspaceRoot}/workspace` : "",
    },
  };
}

function setStoreState(opts?: {
  desktopBridge?: boolean;
  mode?: "desktop" | "server";
  language?: Language;
  prefWorkspaceRoot?: string;
  activeWorkspaceRoot?: string;
  sysInfoNull?: boolean;
  prefError?: ActionError | null;
}) {
  const {
    desktopBridge = true,
    mode = "desktop",
    language = "en",
    prefWorkspaceRoot = "",
    activeWorkspaceRoot = "D:/DefaultStudioRoot",
    sysInfoNull = false,
    prefError = null,
  } = opts ?? {};

  bridgeMock.desktop = desktopBridge;

  usePreferencesStore.setState({
    prefs: {
      theme: "system",
      language,
      workspace_root: prefWorkspaceRoot,
      onboarding_completed: true,
      agent_skip_trusted_approval: false,
    },
    loaded: true,
    dirty: false,
    error: prefError,
  });

  useSystemStore.setState({
    info: sysInfoNull ? null : makeSystemInfo(mode, activeWorkspaceRoot),
    infoError: null,
    update: null,
    updateError: null,
    checkingUpdate: false,
  });

  useConnectionStore.setState({
    status: "connected",
    endpoint: {
      url: "ws://127.0.0.1:8080/ws",
      token: "",
      source: "bridge",
      mode,
      actionTimeoutMs: 10000,
      longActionTimeoutMs: 60000,
      pingIntervalMs: 15000,
      checkUpdatesOnStartup: false,
    },
    resolved: true,
    latencyMs: 5,
    fatal: null,
  });
}

function renderSettings(): string {
  return renderToStaticMarkup(React.createElement(SettingsView));
}

describe("SettingsView Master Workspace Root section", () => {
  beforeEach(() => {
    bridgeMock.desktop = true;
    bridgeMock.browseDirectory.mockReset();
    setStoreState();
  });

  it("renders System Default badge and hides Use Default button when workspace_root is unset", () => {
    setStoreState({
      prefWorkspaceRoot: "",
      activeWorkspaceRoot: "D:/DefaultStudioRoot",
    });

    const html = renderSettings();
    expect(html).toContain("Master Workspace Root");
    expect(html).toContain("D:/DefaultStudioRoot");
    expect(html).toContain("System Default");
    expect(html).toContain("Open Folder");
    expect(html).toContain("Change Folder…");
    expect(html).not.toContain("Use Default");
    expect(html).not.toContain("Custom");
    expect(html).not.toMatch(/<button[^>]*disabled=""[^>]*>.*?(Open Folder|Change Folder…)/);
  });

  it("renders Custom badge and Use Default button when custom workspace_root is configured", () => {
    setStoreState({
      prefWorkspaceRoot: "E:/CustomStudio",
      activeWorkspaceRoot: "E:/CustomStudio",
    });

    const html = renderSettings();
    expect(html).toContain("E:/CustomStudio");
    expect(html).toContain("Custom");
    expect(html).toContain("agent-badge-online");
    expect(html).toContain("Open Folder");
    expect(html).toContain("Change Folder…");
    expect(html).toContain("Use Default");
    expect(html).not.toMatch(
      /<button[^>]*disabled=""[^>]*>.*?(Open Folder|Change Folder…|Use Default)/
    );
  });

  it("disables workspace root action buttons and shows desktopOnly tooltip in server mode", () => {
    setStoreState({
      desktopBridge: false,
      mode: "server",
      prefWorkspaceRoot: "E:/CustomStudio",
      activeWorkspaceRoot: "E:/CustomStudio",
    });

    const html = renderSettings();
    expect(html).toContain('title="This action is only available in desktop mode"');
    const disabledWorkspaceButtons = html.match(
      /<button[^>]*disabled=""[^>]*>.*?(Open Folder|Change Folder…|Use Default)<\/button>/g
    );
    expect(disabledWorkspaceButtons).toHaveLength(3);
  });

  it("disables workspace root action buttons when bridge is absent even if sysInfo mode is desktop", () => {
    setStoreState({
      desktopBridge: false,
      mode: "desktop",
      prefWorkspaceRoot: "",
      activeWorkspaceRoot: "D:/DefaultStudioRoot",
    });

    const html = renderSettings();
    expect(html).toContain('title="This action is only available in desktop mode"');
    const disabledWorkspaceButtons = html.match(
      /<button[^>]*disabled=""[^>]*>.*?(Open Folder|Change Folder…)<\/button>/g
    );
    expect(disabledWorkspaceButtons).toHaveLength(2);
  });

  it("renders validation/preferences error alert inside workspace error container", () => {
    setStoreState({
      prefError: new ActionError("ERR_VALIDATION", "Invalid workspace root path"),
    });

    const html = renderSettings();
    expect(html).toContain("settings-workspace-error");
    expect(html).toContain('role="alert"');
    expect(html).toContain("Invalid workspace root path");
  });

  it("renders Not configured fallback when active workspace root is unavailable", () => {
    setStoreState({
      sysInfoNull: true,
    });

    const html = renderSettings();
    expect(html).toContain("Not configured");
    expect(html).toContain("System Default");
  });

  it("renders Vietnamese labels cleanly when language is set to vi", () => {
    setStoreState({
      language: "vi",
      prefWorkspaceRoot: "E:/CustomStudio",
      activeWorkspaceRoot: "E:/CustomStudio",
    });

    const html = renderSettings();
    expect(html).toContain("Thư mục Tổng (Workspace Root)");
    expect(html).toContain("Tùy chỉnh");
    expect(html).toContain("Mở thư mục");
    expect(html).toContain("Đổi thư mục…");
    expect(html).toContain("Dùng mặc định");
  });
});
