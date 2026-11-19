// @vitest-environment jsdom
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";

const { rtRequestMock, getBridgeMock, isDesktopMock, downloadBlobMock } = vi.hoisted(() => ({
  rtRequestMock: vi.fn(),
  getBridgeMock: vi.fn(),
  isDesktopMock: vi.fn(() => false),
  downloadBlobMock: vi.fn(),
}));

vi.mock("../../lib/rtClient/media", async () => {
  const actual = await vi.importActual<typeof import("../../lib/rtClient/media")>(
    "../../lib/rtClient/media"
  );
  return {
    ...actual,
    downloadMediaBlob: downloadBlobMock,
  };
});

vi.mock("../../lib/bridge", async () => {
  const actual = await vi.importActual<typeof import("../../lib/bridge")>("../../lib/bridge");
  return {
    ...actual,
    getBridge: getBridgeMock,
    isDesktop: isDesktopMock,
  };
});

import { AppHeader } from "../../app/AppHeader";
import { StatusBanner } from "../../app/StatusBanner";
import { rtClient } from "../../lib/rtClient";
import { ConnectionCard } from "../connection/ConnectionCard";
import { useConnectionStore } from "../connection/useConnectionStore";
import { DiagnosticsCard } from "../diagnostics/DiagnosticsCard";
import { LiveLogView } from "../logs/LiveLogView";
import { uiLog, useLogStore } from "../logs/useLogStore";
import { usePreferencesStore } from "../preferences/usePreferencesStore";
import { StudioView } from "../studio/StudioView";
import { useStudioStore } from "../studio/useStudioStore";
import { SystemCard } from "../system/SystemCard";
import { useSystemStore } from "../system/useSystemStore";
import { WatermarkResultsCard } from "../watermark/WatermarkResultsCard";
import { useWatermarkStore } from "../watermark/useWatermarkStore";

describe("Core views & P0-P2 regression DOM coverage", () => {
  beforeEach(() => {
    rtRequestMock.mockReset();
    vi.spyOn(rtClient, "request").mockImplementation(rtRequestMock as typeof rtClient.request);
    getBridgeMock.mockReset();
    downloadBlobMock.mockReset();
    isDesktopMock.mockReturnValue(false);
    useLogStore.getState().clear();
    usePreferencesStore.setState({
      loaded: true,
      dirtyPatch: {},
      error: null,
      prefs: {
        theme: "light",
        language: "en",
        workspace_root: "/ws/original",
        onboarding_completed: true,
        agent_skip_trusted_approval: false,
      },
    });
  });

  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
  });

  it("renders AppHeader, StatusBanner, ConnectionCard (including SetRemoteToken), DiagnosticsCard, LiveLogView & SystemCard", async () => {
    isDesktopMock.mockReturnValue(true);
    const setRemoteTokenBridge = vi.fn().mockResolvedValue(undefined);
    getBridgeMock.mockReturnValue({
      SetRemoteToken: setRemoteTokenBridge,
      GetBuildInfo: vi.fn().mockResolvedValue({
        product_name: "DezuxkStudio",
        version: "1.2.3",
        commit: "abc1234",
        build_date: "2026-11-19",
        os: "windows",
        arch: "amd64",
        go_version: "go1.27",
      }),
      GetEndpoint: vi
        .fn()
        .mockResolvedValue({ url: "ws://127.0.0.1:9999/ws", token: "", mode: "client" }),
    });

    useConnectionStore.setState({
      status: "reconnecting",
      resolved: true,
      endpoint: {
        url: "ws://127.0.0.1:9999/ws",
        source: "bridge",
        mode: "client",
        token: "",
        actionTimeoutMs: 10000,
        longActionTimeoutMs: 60000,
        pingIntervalMs: 15000,
        checkUpdatesOnStartup: false,
      },
    });
    useSystemStore.setState({ info: null, buildInfo: null, infoError: null });

    uiLog.info("boot complete");

    const { rerender } = render(
      <MemoryRouter>
        <AppHeader />
        <StatusBanner />
        <SystemCard />
        <LiveLogView />
      </MemoryRouter>
    );

    await act(async () => {
      await Promise.resolve();
    });

    expect(screen.getByText(/boot complete/)).toBeTruthy();
    expect(screen.getByRole("status")).toBeTruthy();
    expect(screen.getByText("1.2.3")).toBeTruthy();

    fireEvent.click(screen.getByRole("button", { name: /clear/i }));
    expect(useLogStore.getState().entries).toHaveLength(0);

    // Connected state: DiagnosticsCard export + cancel, ConnectionCard remote token UI.
    useConnectionStore.setState({ status: "connected" });
    rtRequestMock.mockImplementation(() => new Promise(() => {}));
    rerender(
      <MemoryRouter>
        <ConnectionCard />
        <DiagnosticsCard />
      </MemoryRouter>
    );

    fireEvent.click(screen.getByRole("button", { name: /export/i }));
    expect(screen.getByRole("progressbar")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: /cancel/i }));

    // Open remote token editor in client mode and save token.
    const tokenBtn = screen.getByRole("button", { name: /token|configure/i });
    fireEvent.click(tokenBtn);
    const tokenInput = screen.getByPlaceholderText(/token/i);
    fireEvent.change(tokenInput, { target: { value: "secret-remote-tok" } });
    fireEvent.click(screen.getByRole("button", { name: /save/i }));
    await act(async () => {
      await Promise.resolve();
    });
    expect(setRemoteTokenBridge).toHaveBeenCalledWith("secret-remote-tok");
  });

  it("merges offline dirtyPatch onto incoming PREFERENCES_GET without clobbering workspace_root", async () => {
    useConnectionStore.setState({ status: "idle" });
    await usePreferencesStore.getState().update({ theme: "dark" });
    expect(usePreferencesStore.getState().dirtyPatch).toEqual({ theme: "dark" });

    const serverPrefs = {
      schema_version: 1,
      theme: "light",
      language: "vi",
      workspace_root: "/ws/server-latest",
      onboarding_completed: true,
      agent_skip_trusted_approval: true,
    };
    rtRequestMock
      .mockResolvedValueOnce(serverPrefs)
      .mockResolvedValueOnce({ ...serverPrefs, theme: "dark" });
    await usePreferencesStore.getState().load();

    const prefs = usePreferencesStore.getState().prefs;
    expect(prefs.theme).toBe("dark");
    expect(prefs.workspace_root).toBe("/ws/server-latest");
  });

  it("renders WatermarkResultsCard queue removal without off-by-one, cleanCount, and preview retry", async () => {
    const urlProto = URL as unknown as {
      createObjectURL?: (obj: Blob) => string;
      revokeObjectURL?: (url: string) => void;
    };
    const origCreateObjectURL = urlProto.createObjectURL;
    urlProto.createObjectURL = vi.fn(() => "blob:preview");
    downloadBlobMock.mockRejectedValue(new Error("403 forbidden"));

    useWatermarkStore.setState({
      mediaKind: "image",
      inputPath: "",
      inputPaths: ["/a.png", "/b.png", "/c.png"],
      queuedFiles: [],
      queuedObjectKeys: [],
      loading: false,
      progress: null,
      inPlace: false,
      result: null,
      videoResult: null,
      comparisonItem: null,
    });

    const { rerender } = render(<WatermarkResultsCard />);

    // Remove the first queued file (/a.png) and ensure /b.png and /c.png remain.
    const removeButtons = screen.getAllByRole("button", { name: /remove|xóa/i });
    fireEvent.click(removeButtons[0]);
    expect(useWatermarkStore.getState().inputPaths).toEqual(["/b.png", "/c.png"]);

    // Switch to results state with comparison viewer open to verify cleanCount and preview retry.
    act(() => {
      useWatermarkStore.setState({
        comparisonItem: {
          beforeUrl: "/y.png",
          afterUrl: "/y_clean.png",
          title: "y.png",
        },
        result: {
          image: { localUrl: "/y_clean.png", kind: "image" },
          images: [{ localUrl: "/y_clean.png", kind: "image" }],
          detected: true,
          total_count: 2,
          detected_count: 1,
          results: [
            { path: "/x.png", out_path: "", detected: false, score: 0 },
            { path: "/y.png", out_path: "/y_clean.png", detected: true, score: 0.9 },
          ],
        },
      });
    });
    rerender(<WatermarkResultsCard />);

    await act(async () => {
      await Promise.resolve();
      await Promise.resolve();
    });

    // Clean KPI is rendered as 1.
    expect(screen.getAllByText("1").length).toBeGreaterThanOrEqual(1);

    // Preview failed -> Retry button triggers another downloadMediaBlob call.
    const retryButtons = screen.getAllByRole("button", { name: /retry|thử lại/i });
    expect(retryButtons.length).toBeGreaterThanOrEqual(1);
    const callsBeforeRetry = downloadBlobMock.mock.calls.length;
    fireEvent.click(retryButtons[0]);
    await act(async () => {
      await Promise.resolve();
    });
    expect(downloadBlobMock.mock.calls.length).toBeGreaterThan(callsBeforeRetry);
    urlProto.createObjectURL = origCreateObjectURL;
  });

  it("renders StudioView ErrorText visibly on store validation errors", async () => {
    rtRequestMock.mockResolvedValue({ projects: [] });
    useStudioStore.setState({
      projects: [],
      error: null,
    });
    render(<StudioView />);
    await act(async () => {
      await Promise.resolve();
    });
    act(() => {
      useStudioStore.setState({
        error: { name: "ActionError", code: "ERR_VALIDATION", message: "title required" },
      });
    });
    const alert = screen.getByRole("alert");
    expect(alert.textContent).toContain("title required");
  });
});
