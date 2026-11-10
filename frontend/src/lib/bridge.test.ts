import { describe, expect, it, vi, beforeEach, afterEach } from "vitest";
import { getBridge, isDesktop, onFileDrop, onNativeEvent, openExternal } from "./bridge";

describe("bridge module", () => {
  beforeEach(() => {
    // Provide a mocked window object on globalThis for Node test environment
    (globalThis as unknown as { window?: Record<string, unknown> }).window = {};
  });

  afterEach(() => {
    delete (globalThis as unknown as { window?: Record<string, unknown> }).window;
  });

  describe("getBridge and isDesktop", () => {
    it("returns null and false when running in browser mode (no bridge)", () => {
      expect(getBridge()).toBeNull();
      expect(isDesktop()).toBe(false);
    });

    it("returns bridge instance and true when Wails desktop bridge is injected", () => {
      const mockBridge = {
        BrowseImage: vi.fn(),
      } as unknown as NonNullable<ReturnType<typeof getBridge>>;

      (window as unknown as { go?: { desktop?: { Bridge?: unknown } } }).go = {
        desktop: { Bridge: mockBridge },
      };

      expect(getBridge()).toBe(mockBridge);
      expect(isDesktop()).toBe(true);
    });
  });

  describe("onNativeEvent", () => {
    it("returns a no-op function when runtime.EventsOn is not present", () => {
      const handler = vi.fn();
      const unsub = onNativeEvent("app:ready", handler);
      expect(typeof unsub).toBe("function");
      unsub();
      expect(handler).not.toHaveBeenCalled();
    });

    it("registers listener and returns unsubscribe function when runtime.EventsOn exists", () => {
      const offFn = vi.fn();
      const onFn = vi.fn().mockReturnValue(offFn);
      (window as unknown as { runtime?: { EventsOn?: unknown } }).runtime = {
        EventsOn: onFn,
      };

      const handler = vi.fn();
      const unsub = onNativeEvent("app:ready", handler);

      expect(onFn).toHaveBeenCalledTimes(1);
      expect(onFn).toHaveBeenCalledWith("app:ready", expect.any(Function));

      // Test event trigger
      const callback = (onFn.mock.calls[0] as unknown[])[1] as (data: unknown) => void;
      callback({});
      expect(handler).toHaveBeenCalledWith({});

      // Unsubscribe
      unsub();
      expect(offFn).toHaveBeenCalledTimes(1);
    });
  });

  describe("onFileDrop", () => {
    it("returns a no-op when runtime.OnFileDrop is missing", () => {
      const cb = vi.fn();
      const unsub = onFileDrop(cb);
      expect(typeof unsub).toBe("function");
      unsub();
      expect(cb).not.toHaveBeenCalled();
    });

    it("registers listener via runtime.OnFileDrop and cleans up via runtime.OnFileDropOff", () => {
      const dropOffMock = vi.fn();
      const dropOnMock = vi.fn();
      (
        window as unknown as { runtime?: { OnFileDrop?: unknown; OnFileDropOff?: unknown } }
      ).runtime = {
        OnFileDrop: dropOnMock,
        OnFileDropOff: dropOffMock,
      };

      const callback = vi.fn();
      const unsub = onFileDrop(callback, false);

      expect(dropOnMock).toHaveBeenCalledWith(callback, false);

      unsub();
      expect(dropOffMock).toHaveBeenCalledTimes(1);
    });
  });

  describe("openExternal", () => {
    it("refuses invalid or non-https URLs", () => {
      expect(openExternal("invalid-url")).toBe(false);
      expect(openExternal("http://example.com")).toBe(false);
      expect(openExternal("javascript:alert(1)")).toBe(false);
      expect(openExternal("file:///C:/secret")).toBe(false);
    });

    it("uses window.open when runtime.BrowserOpenURL is absent", () => {
      const openSpy = vi.fn().mockReturnValue(null);
      (window as unknown as { open?: unknown }).open = openSpy;
      const res = openExternal("https://example.com/docs");
      expect(res).toBe(true);
      expect(openSpy).toHaveBeenCalledWith(
        "https://example.com/docs",
        "_blank",
        "noopener,noreferrer"
      );
    });

    it("delegates to runtime.BrowserOpenURL when available", () => {
      const browserOpenMock = vi.fn();
      (window as unknown as { runtime?: { BrowserOpenURL?: unknown } }).runtime = {
        BrowserOpenURL: browserOpenMock,
      };

      const res = openExternal("https://example.com/docs");
      expect(res).toBe(true);
      expect(browserOpenMock).toHaveBeenCalledWith("https://example.com/docs");
    });
  });
});
