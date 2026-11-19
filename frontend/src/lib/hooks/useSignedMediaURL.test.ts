import { describe, expect, it, vi, beforeEach, afterEach } from "vitest";
import React from "react";
import { renderToStaticMarkup } from "react-dom/server";
import type { MediaSignedURLResult } from "../../contracts/generated";
import { SignedMediaSession, useSignedMediaURL, type SignedMediaState } from "./useSignedMediaURL";
import * as mediaModule from "../rtClient/media";
import { appConfig } from "../../config/appConfig";

const flush = async () => {
  await Promise.resolve();
  await Promise.resolve();
};

describe("SignedMediaSession & useSignedMediaURL logic", () => {
  let signMediaURLSpy: ReturnType<typeof vi.spyOn>;

  beforeEach(() => {
    vi.useFakeTimers();
    signMediaURLSpy = vi.spyOn(mediaModule, "signMediaURL");
  });

  afterEach(() => {
    vi.restoreAllMocks();
    vi.useRealTimers();
  });

  it("does not call signMediaURL when assetId is null or empty", async () => {
    const session = new SignedMediaSession();
    const states: SignedMediaState[] = [];
    session.subscribe((s) => states.push({ ...s }));

    session.configure(null);
    expect(signMediaURLSpy).not.toHaveBeenCalled();
    expect(session.getState().url).toBeNull();
    expect(session.getState().ticket).toBeNull();
    expect(session.getState().loading).toBe(false);

    session.configure("   ");
    expect(signMediaURLSpy).not.toHaveBeenCalled();
    expect(session.getState().url).toBeNull();

    session.dispose();
  });

  it("fetches signed URL with assetId and purpose", async () => {
    const session = new SignedMediaSession();
    const fakeExpiresSec = Math.floor(Date.now() / 1000) + 900;

    signMediaURLSpy.mockResolvedValueOnce({
      asset_id: "asset-1",
      url: "http://127.0.0.1:8080/api/media/download?asset_id=asset-1&ticket=hmac-1",
      ticket: "hmac-1",
      expires_at: fakeExpiresSec,
      purpose: "download",
    });

    session.configure("asset-1", { purpose: "download" });
    expect(session.getState().loading).toBe(true);

    await flush();

    expect(signMediaURLSpy).toHaveBeenCalledTimes(1);
    expect(signMediaURLSpy).toHaveBeenCalledWith({
      asset_id: "asset-1",
      purpose: "download",
      ttl_seconds: undefined,
    });

    expect(session.getState().url).toBe(
      "http://127.0.0.1:8080/api/media/download?asset_id=asset-1&ticket=hmac-1"
    );
    expect(session.getState().ticket).toBe("hmac-1");
    expect(session.getState().expiresAt).toBe(fakeExpiresSec * 1000);
    expect(session.getState().loading).toBe(false);
    expect(session.getState().error).toBeNull();

    session.dispose();
  });

  it("supports millisecond timestamps directly if expires_at >= 1e11", async () => {
    const session = new SignedMediaSession();
    const expMs = Date.now() + 600_000;

    signMediaURLSpy.mockResolvedValueOnce({
      asset_id: "asset-ms",
      url: "http://example.com/asset-ms",
      ticket: "tok-ms",
      expires_at: expMs,
      purpose: "preview",
    });

    session.configure("asset-ms");
    await flush();

    expect(session.getState().expiresAt).toBe(expMs);
    session.dispose();
  });

  it("automatically renews ticket before expiration based on ticketRenewSkewMs", async () => {
    const now = 1_700_000_000_000;
    vi.setSystemTime(now);

    const ttlSec = 900; // 15 min = 900s
    const expSec = Math.floor(now / 1000) + ttlSec;
    const renewSkewMs = appConfig.media.ticketRenewSkewMs; // 120,000ms

    signMediaURLSpy.mockResolvedValueOnce({
      asset_id: "asset-auto",
      url: "http://127.0.0.1:8080/api/media/download?asset_id=asset-auto&ticket=tok-1",
      ticket: "tok-1",
      expires_at: expSec,
      purpose: "preview",
    });

    const session = new SignedMediaSession();
    session.configure("asset-auto");

    await flush();
    expect(session.getState().ticket).toBe("tok-1");
    expect(signMediaURLSpy).toHaveBeenCalledTimes(1);

    // Expected renewal timer delay: 900*1000 - 120_000 = 780_000 ms
    const expectedDelayMs = ttlSec * 1000 - renewSkewMs;

    signMediaURLSpy.mockResolvedValueOnce({
      asset_id: "asset-auto",
      url: "http://127.0.0.1:8080/api/media/download?asset_id=asset-auto&ticket=tok-renewed",
      ticket: "tok-renewed",
      expires_at: expSec + ttlSec,
      purpose: "preview",
    });

    // Advance right before renewal time: no call yet
    vi.advanceTimersByTime(expectedDelayMs - 1000);
    expect(signMediaURLSpy).toHaveBeenCalledTimes(1);

    // Advance past renewal time: triggers automatic renew
    vi.advanceTimersByTime(2000);
    await flush();

    expect(signMediaURLSpy).toHaveBeenCalledTimes(2);
    expect(session.getState().ticket).toBe("tok-renewed");

    session.dispose();
  });

  it("recovers media playback at currentTime on media error", async () => {
    const expSec = Math.floor(Date.now() / 1000) + 600;

    signMediaURLSpy.mockResolvedValueOnce({
      asset_id: "asset-rec",
      url: "http://127.0.0.1:8080/api/media/download?asset_id=asset-rec&ticket=tok-old",
      ticket: "tok-old",
      expires_at: expSec,
      purpose: "preview",
    });

    const session = new SignedMediaSession();
    session.configure("asset-rec");
    await flush();

    // Mock video/audio element with playback in progress at 45.2 seconds
    const playFn = vi.fn().mockReturnValue(Promise.resolve());
    const mockMedia = {
      currentTime: 45.2,
      paused: false,
      ended: false,
      readyState: 2, // metadata ready
      play: playFn,
    } as unknown as HTMLMediaElement;

    session.setMediaElement(mockMedia);

    // Prepare renewed response
    signMediaURLSpy.mockResolvedValueOnce({
      asset_id: "asset-rec",
      url: "http://127.0.0.1:8080/api/media/download?asset_id=asset-rec&ticket=tok-recovered",
      ticket: "tok-recovered",
      expires_at: expSec + 600,
      purpose: "preview",
    });

    // Trigger media error (e.g. ticket expired while streaming)
    session.handleMediaError(mockMedia);
    await flush();

    expect(signMediaURLSpy).toHaveBeenCalledTimes(2);
    expect(session.getState().ticket).toBe("tok-recovered");
    expect(mockMedia.currentTime).toBe(45.2);
    expect(playFn).toHaveBeenCalled();

    session.dispose();
  });

  it("handles errors cleanly and supports manual renew", async () => {
    const session = new SignedMediaSession();
    signMediaURLSpy.mockRejectedValueOnce(new Error("Connection lost"));

    session.configure("asset-fail");
    await flush();

    expect(session.getState().error?.message).toContain("Connection lost");
    expect(session.getState().url).toBeNull();
    expect(session.getState().loading).toBe(false);

    // Manual renew after recovery
    signMediaURLSpy.mockResolvedValueOnce({
      asset_id: "asset-fail",
      url: "http://127.0.0.1:8080/api/media/download?asset_id=asset-fail&ticket=tok-recovered",
      ticket: "tok-recovered",
      expires_at: Math.floor(Date.now() / 1000) + 300,
      purpose: "preview",
    });

    await session.renew();
    await flush();

    expect(session.getState().error).toBeNull();
    expect(session.getState().ticket).toBe("tok-recovered");

    session.dispose();
  });

  it("cleans up renewal timers and prevents callback after disposal", async () => {
    let resolveSign!: (v: MediaSignedURLResult) => void;
    signMediaURLSpy.mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          resolveSign = resolve;
        })
    );

    const session = new SignedMediaSession();
    session.configure("asset-dispose");
    expect(session.getState().loading).toBe(true);

    session.dispose();

    resolveSign({
      asset_id: "asset-dispose",
      url: "http://example.com/asset",
      ticket: "tok",
      expires_at: 1000,
      purpose: "preview",
    });
    await flush();

    // After dispose, state is not updated
    expect(session.getState().url).toBeNull();
  });

  it("renders with useSignedMediaURL hook in a component without crashing", () => {
    function DummyMediaViewer({ assetId }: { assetId: string }) {
      const { url, loading } = useSignedMediaURL(assetId);
      return React.createElement("div", null, loading ? "loading..." : url || "none");
    }

    const html = renderToStaticMarkup(
      React.createElement(DummyMediaViewer, { assetId: "test-id" })
    );
    expect(html).toContain("div");
  });

  it("caps consecutive handleMediaError retries and cleans up pending loadedmetadata listeners on dispose", async () => {
    const expSec = Math.floor(Date.now() / 1000) + 600;
    let callIdx = 0;
    signMediaURLSpy.mockImplementation(async () => {
      callIdx += 1;
      return {
        asset_id: "asset-corrupt",
        url: `http://127.0.0.1:8080/api/media/download?asset_id=asset-corrupt&ticket=tok-${callIdx}`,
        ticket: `tok-${callIdx}`,
        expires_at: expSec + callIdx,
        purpose: "preview",
      };
    });

    const addListener = vi.fn();
    const removeListener = vi.fn();
    const mockMedia = {
      currentTime: 12.5,
      paused: false,
      ended: false,
      readyState: 0, // metadata not loaded yet -> attaches loadedmetadata listener
      addEventListener: addListener,
      removeEventListener: removeListener,
    } as unknown as HTMLMediaElement;

    const session = new SignedMediaSession();
    session.configure("asset-corrupt");
    await flush();
    expect(signMediaURLSpy).toHaveBeenCalledTimes(1);

    // Repeated media errors must stop at maxMediaErrorRetries instead of looping forever
    for (let i = 0; i < 6; i++) {
      session.handleMediaError(mockMedia);
      await flush();
    }
    expect(signMediaURLSpy).toHaveBeenCalledTimes(1 + appConfig.media.maxMediaErrorRetries);
    expect(addListener).toHaveBeenCalledWith("loadedmetadata", expect.any(Function));

    // Disposing removes any pending loadedmetadata listener
    session.dispose();
    expect(removeListener).toHaveBeenCalledWith("loadedmetadata", expect.any(Function));
  });

  it("ignores stale in-flight request when assetId changes and supports StrictMode revive after dispose", async () => {
    let resolveA!: (val: MediaSignedURLResult) => void;
    let resolveB!: (val: MediaSignedURLResult) => void;
    const expSec = Math.floor(Date.now() / 1000) + 600;

    signMediaURLSpy
      .mockImplementationOnce(
        () =>
          new Promise<MediaSignedURLResult>((r) => {
            resolveA = r;
          })
      )
      .mockImplementationOnce(
        () =>
          new Promise<MediaSignedURLResult>((r) => {
            resolveB = r;
          })
      );

    const session = new SignedMediaSession();
    session.configure("asset-A");
    expect(signMediaURLSpy).toHaveBeenCalledTimes(1);

    // Switching assetId before asset-A resolves must start a new request instead of reusing asset-A's promise
    session.configure("asset-B");
    expect(signMediaURLSpy).toHaveBeenCalledTimes(2);

    resolveA({
      asset_id: "asset-A",
      url: "http://example.com/A",
      ticket: "tok-A",
      expires_at: expSec,
      purpose: "preview",
    });
    await flush();
    // Stale A result must not overwrite current state for asset-B
    expect(session.getState().url).toBeNull();
    expect(session.getState().loading).toBe(true);

    resolveB({
      asset_id: "asset-B",
      url: "http://example.com/B",
      ticket: "tok-B",
      expires_at: expSec,
      purpose: "preview",
    });
    await flush();
    expect(session.getState().url).toBe("http://example.com/B");
    expect(session.getState().loading).toBe(false);

    // StrictMode unmount -> remount: dispose() then revive() + configure()
    signMediaURLSpy.mockResolvedValueOnce({
      asset_id: "asset-C",
      url: "http://example.com/C",
      ticket: "tok-C",
      expires_at: expSec,
      purpose: "preview",
    });
    session.dispose();
    session.revive();
    session.configure("asset-C");
    await flush();
    expect(session.getState().url).toBe("http://example.com/C");
    session.dispose();
  });
});
