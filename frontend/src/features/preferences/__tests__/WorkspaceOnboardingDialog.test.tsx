import React from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { renderToStaticMarkup } from "react-dom/server";
import type { SystemInfo } from "../../../contracts/generated";
import { ActionError, rtClient } from "../../../lib/rtClient";
import { useSystemStore } from "../../system/useSystemStore";
import { usePreferencesStore } from "../usePreferencesStore";
import { WorkspaceOnboardingDialog } from "../WorkspaceOnboardingDialog";

const bridgeMock = vi.hoisted(() => ({
  desktop: true,
  browseResult: "D:/CustomStudio",
}));

vi.mock("../../../lib/bridge", async (orig) => ({
  ...(await orig<typeof import("../../../lib/bridge")>()),
  isDesktop: () => bridgeMock.desktop,
  getBridge: () => ({
    BrowseDirectory: vi.fn().mockImplementation(async () => bridgeMock.browseResult),
  }),
}));

vi.spyOn(React, "useSyncExternalStore").mockImplementation((_s, getSnapshot) => getSnapshot());

describe("WorkspaceOnboardingDialog", () => {
  const sampleSuggested = "C:\\Users\\Tester\\Documents\\DezuxkStudio";

  beforeEach(() => {
    vi.restoreAllMocks();
    vi.spyOn(React, "useSyncExternalStore").mockImplementation((_s, getSnapshot) => getSnapshot());
    usePreferencesStore.setState({
      prefs: {
        theme: "light",
        language: "en",
        workspace_root: "",
        onboarding_completed: false,
        agent_skip_trusted_approval: false,
      },
      loaded: true,
      dirty: false,
      error: null,
    });

    useSystemStore.setState({
      info: {
        mode: "desktop",
        paths: {
          suggested_workspace_root: sampleSuggested,
          workspace_root: "",
          data: "C:\\Users\\Tester\\AppData\\Local\\DezuxkStudio\\data",
          config: "C:\\Users\\Tester\\AppData\\Roaming\\DezuxkStudio",
          logs: "C:\\Users\\Tester\\AppData\\Local\\DezuxkStudio\\logs",
          diagnostics: "C:\\Users\\Tester\\AppData\\Local\\DezuxkStudio\\diagnostics",
          media_dir: "C:\\Users\\Tester\\AppData\\Local\\DezuxkStudio\\data\\studio\\media",
          watermark_dir:
            "C:\\Users\\Tester\\AppData\\Local\\DezuxkStudio\\data\\studio\\media\\watermark",
          agent_workspace:
            "C:\\Users\\Tester\\AppData\\Local\\DezuxkStudio\\data\\studio\\workspace",
        },
      } as unknown as SystemInfo,
      infoError: null,
    });
  });

  it("renders modal with explanation and pre-filled suggested workspace root", () => {
    const html = renderToStaticMarkup(<WorkspaceOnboardingDialog />);

    expect(html).toContain("Master Workspace Setup");
    expect(html).toContain("%LOCALAPPDATA%");
    expect(html).toContain(sampleSuggested);
    expect(html).toContain("Browse…");
  });

  it("renders tree preview with all required subdirectories", () => {
    const html = renderToStaticMarkup(<WorkspaceOnboardingDialog />);

    expect(html).toContain("onboarding-tree-preview");
    expect(html).toContain("media/images");
    expect(html).toContain("media/videos");
    expect(html).toContain("media/tts");
    expect(html).toContain("media/watermark");
    expect(html).toContain("workspace");
  });

  it("renders primary and secondary action buttons without close dismiss button", () => {
    const html = renderToStaticMarkup(<WorkspaceOnboardingDialog />);

    expect(html).toContain("Use System Default");
    expect(html).toContain("Save &amp; Start");
    expect(html).not.toContain('aria-label="Close"');
  });

  it("renders validation or server error when present", () => {
    usePreferencesStore.setState({
      error: new ActionError("ERR_VALIDATION", "Selected path is a root volume"),
    });

    const html = renderToStaticMarkup(<WorkspaceOnboardingDialog />);
    expect(html).toContain("Selected path is a root volume");
  });

  it("renders localized Vietnamese text when language is vi", () => {
    usePreferencesStore.setState({
      prefs: {
        theme: "light",
        language: "vi",
        workspace_root: "",
        onboarding_completed: false,
        agent_skip_trusted_approval: false,
      },
    });

    const html = renderToStaticMarkup(<WorkspaceOnboardingDialog />);
    expect(html).toContain("Thiết lập Thư mục Tổng");
    expect(html).toContain("Dùng mặc định hệ thống");
    expect(html).toContain("Lưu &amp; Bắt đầu");
    expect(html).toContain("Chọn thư mục…");
  });

  it("keeps onboarding_completed false and renders error when save fails with server rejection", async () => {
    vi.spyOn(rtClient, "getStatus").mockReturnValue("connected");
    vi.spyOn(rtClient, "request").mockRejectedValueOnce(
      new ActionError("ERR_VALIDATION", "Invalid workspace path: root volume")
    );

    await expect(
      usePreferencesStore.getState().update({
        workspace_root: "C:\\",
        onboarding_completed: true,
      })
    ).rejects.toThrow("Invalid workspace path: root volume");

    const state = usePreferencesStore.getState();
    expect(state.prefs.onboarding_completed).toBe(false);
    expect(state.prefs.workspace_root).toBe("");
    expect(state.error?.message).toBe("Invalid workspace path: root volume");

    const html = renderToStaticMarkup(<WorkspaceOnboardingDialog />);
    expect(html).toContain("Invalid workspace path: root volume");
    expect(html).toContain("Master Workspace Setup");
  });

  it("updates store preferences and marks completed on successful save", async () => {
    vi.spyOn(rtClient, "getStatus").mockReturnValue("connected");
    vi.spyOn(rtClient, "request").mockResolvedValueOnce({
      theme: "light",
      language: "en",
      workspace_root: "D:/MyCustomRoot",
      onboarding_completed: true,
      agent_skip_trusted_approval: false,
    });

    await usePreferencesStore.getState().update({
      workspace_root: "D:/MyCustomRoot",
      onboarding_completed: true,
    });

    const state = usePreferencesStore.getState();
    expect(state.prefs.workspace_root).toBe("D:/MyCustomRoot");
    expect(state.prefs.onboarding_completed).toBe(true);
    expect(state.error).toBeNull();
  });
});
