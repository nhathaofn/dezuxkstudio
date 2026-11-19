// @vitest-environment jsdom
import React from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, fireEvent, render, screen } from "@testing-library/react";

const { requestMock } = vi.hoisted(() => ({
  requestMock: vi.fn(),
}));

vi.mock("../../lib/rtClient", async () => {
  const actual = await vi.importActual<typeof import("../../lib/rtClient")>("../../lib/rtClient");
  return {
    ...actual,
    rtClient: {
      ...actual.rtClient,
      request: requestMock,
    },
  };
});

import type { BinaryInfo } from "../../contracts/generated";
import { BinaryCard } from "./BinaryCard";
import { useBinariesStore } from "./useBinariesStore";

const missingBinary: BinaryInfo = {
  id: "ffmpeg",
  name: "FFmpeg",
  status: "missing",
  path: "",
  version: "",
  custom_path: "",
  installed_by_app: false,
  download_url: "https://example.com/ffmpeg.zip",
  error: "",
};

describe("BinaryCard tab-switch download persistence", () => {
  beforeEach(() => {
    requestMock.mockReset();
    useBinariesStore.setState({
      binaries: [missingBinary],
      activeProgress: {},
      abortControllers: {},
      error: null,
    });
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("keeps store-level download progress and AbortController working across unmount/remount", async () => {
    let capturedSignal: AbortSignal | undefined;
    requestMock.mockImplementation(
      (
        _action: string,
        _payload: unknown,
        opts?: {
          signal?: AbortSignal;
          onProgress?: (p: { percent: number; message: string }) => void;
        }
      ) => {
        capturedSignal = opts?.signal;
        opts?.onProgress?.({ percent: 42, message: "downloading" });
        return new Promise((_resolve, reject) => {
          opts?.signal?.addEventListener("abort", () => {
            reject(new DOMException("Aborted", "AbortError"));
          });
        });
      }
    );

    const { unmount } = render(<BinaryCard binary={missingBinary} />);

    let downloadPromise: Promise<unknown> | undefined;
    await act(async () => {
      downloadPromise = useBinariesStore
        .getState()
        .download("ffmpeg")
        .catch(() => undefined);
      await Promise.resolve();
    });

    // Unmount simulates navigating away from Settings -> Tools tab.
    unmount();
    expect(useBinariesStore.getState().activeProgress["ffmpeg"]?.percent).toBe(42);
    expect(capturedSignal?.aborted).toBe(false);

    // Remount sees ongoing progress and Cancel button still aborts the live AbortController.
    render(<BinaryCard binary={missingBinary} />);
    const cancelBtn = screen.getByRole("button", { name: /cancel|hủy/i });
    await act(async () => {
      fireEvent.click(cancelBtn);
      await downloadPromise;
    });

    expect(capturedSignal?.aborted).toBe(true);
  });
});
