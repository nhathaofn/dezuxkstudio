// @vitest-environment jsdom
import React, { StrictMode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, render, screen } from "@testing-library/react";

const { signMediaURLMock } = vi.hoisted(() => ({
  signMediaURLMock: vi.fn(),
}));

vi.mock("../rtClient/media", async () => {
  const actual = await vi.importActual<typeof import("../rtClient/media")>("../rtClient/media");
  return {
    ...actual,
    signMediaURL: signMediaURLMock,
  };
});

import { useSignedMediaURL } from "./useSignedMediaURL";

const MediaProbe: React.FC<{ assetId: string | null }> = ({ assetId }) => {
  const { url, loading, error } = useSignedMediaURL(assetId, "preview");
  return (
    <div data-testid="probe">
      {loading ? "loading" : error ? `err:${error.message}` : (url ?? "empty")}
    </div>
  );
};

describe("useSignedMediaURL DOM & StrictMode", () => {
  beforeEach(() => {
    signMediaURLMock.mockReset();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("revives after StrictMode simulated unmount/remount and switches assetId cleanly", async () => {
    let resolveFirst!: (val: {
      asset_id: string;
      url: string;
      ticket: string;
      expires_at: number;
    }) => void;
    signMediaURLMock
      .mockImplementationOnce(
        () =>
          new Promise((resolve) => {
            resolveFirst = resolve;
          })
      )
      .mockResolvedValue({
        asset_id: "asset-strict",
        url: "http://127.0.0.1/media?ticket=t1",
        ticket: "t1",
        expires_at: Math.floor(Date.now() / 1000) + 120,
      });

    const { rerender } = render(
      <StrictMode>
        <MediaProbe assetId="asset-strict" />
      </StrictMode>
    );

    await act(async () => {
      resolveFirst?.({
        asset_id: "asset-strict",
        url: "http://127.0.0.1/media?ticket=t0",
        ticket: "t0",
        expires_at: Math.floor(Date.now() / 1000) + 120,
      });
      await Promise.resolve();
    });

    expect(screen.getByTestId("probe").textContent).toContain("http://127.0.0.1/media?ticket=");

    // Switching assetId mid-flight must ignore stale responses and resolve the new assetId.
    let resolveStale!: (val: {
      asset_id: string;
      url: string;
      ticket: string;
      expires_at: number;
    }) => void;
    signMediaURLMock
      .mockImplementationOnce(
        () =>
          new Promise((resolve) => {
            resolveStale = resolve;
          })
      )
      .mockResolvedValueOnce({
        asset_id: "asset-next",
        url: "http://127.0.0.1/media?ticket=next",
        ticket: "next",
        expires_at: Math.floor(Date.now() / 1000) + 120,
      });

    rerender(
      <StrictMode>
        <MediaProbe assetId="asset-mid" />
      </StrictMode>
    );
    rerender(
      <StrictMode>
        <MediaProbe assetId="asset-next" />
      </StrictMode>
    );

    await act(async () => {
      resolveStale?.({
        asset_id: "asset-mid",
        url: "http://127.0.0.1/media?ticket=stale",
        ticket: "stale",
        expires_at: Math.floor(Date.now() / 1000) + 120,
      });
      await Promise.resolve();
    });

    expect(screen.getByTestId("probe").textContent).toBe("http://127.0.0.1/media?ticket=next");
  });
});
