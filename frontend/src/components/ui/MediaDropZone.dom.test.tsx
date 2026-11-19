// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import { act, render } from "@testing-library/react";

const unsubscribeSpy = vi.fn();
let capturedDropHandler: ((x: number, y: number, paths: string[]) => void) | null = null;
const onFileDropSpy = vi.fn((cb: (x: number, y: number, paths: string[]) => void) => {
  capturedDropHandler = cb;
  return unsubscribeSpy;
});

vi.mock("../../lib/bridge", () => ({
  isDesktop: () => true,
  onFileDrop: (cb: (x: number, y: number, paths: string[]) => void) => onFileDropSpy(cb),
}));

import { activeFileDropSubscriberCount } from "../../lib/fileDropDispatcher";
import { MediaDropZone } from "./MediaDropZone";

describe("MediaDropZone multi-zone dispatcher DOM behavior", () => {
  afterEach(() => {
    capturedDropHandler = null;
    onFileDropSpy.mockClear();
    unsubscribeSpy.mockClear();
    vi.restoreAllMocks();
  });

  it("shares one native OnFileDrop listener across 3 zones, hit-tests coordinates, and cleans up", () => {
    const onZone1 = vi.fn();
    const onZone2 = vi.fn();
    const onZone3 = vi.fn();

    const { container, rerender, unmount } = render(
      <div>
        <MediaDropZone label="zone-1" onPathsSelected={onZone1} />
        <MediaDropZone label="zone-2" onPathsSelected={onZone2} />
        <MediaDropZone label="zone-3" onPathsSelected={onZone3} />
      </div>
    );

    expect(onFileDropSpy).toHaveBeenCalledTimes(1);
    expect(activeFileDropSubscriberCount()).toBe(3);

    const zones = container.querySelectorAll(".watermark-dropzone");
    expect(zones.length).toBe(3);

    vi.spyOn(zones[0], "getBoundingClientRect").mockReturnValue({
      left: 0,
      top: 0,
      right: 100,
      bottom: 100,
      width: 100,
      height: 100,
      x: 0,
      y: 0,
      toJSON: () => ({}),
    });
    vi.spyOn(zones[1], "getBoundingClientRect").mockReturnValue({
      left: 120,
      top: 0,
      right: 220,
      bottom: 100,
      width: 100,
      height: 100,
      x: 120,
      y: 0,
      toJSON: () => ({}),
    });
    vi.spyOn(zones[2], "getBoundingClientRect").mockReturnValue({
      left: 240,
      top: 0,
      right: 340,
      bottom: 100,
      width: 100,
      height: 100,
      x: 240,
      y: 0,
      toJSON: () => ({}),
    });

    act(() => {
      capturedDropHandler?.(150, 50, ["D:/clips/shot.mp4"]);
    });

    expect(onZone1).not.toHaveBeenCalled();
    expect(onZone2).toHaveBeenCalledWith(["D:/clips/shot.mp4"]);
    expect(onZone3).not.toHaveBeenCalled();

    // Unmounting one zone keeps the shared native listener alive.
    rerender(
      <div>
        <MediaDropZone label="zone-1" onPathsSelected={onZone1} />
        <MediaDropZone label="zone-3" onPathsSelected={onZone3} />
      </div>
    );
    expect(activeFileDropSubscriberCount()).toBe(2);
    expect(unsubscribeSpy).not.toHaveBeenCalled();

    // Unmounting the remaining zones unsubscribes native listener once.
    unmount();
    expect(activeFileDropSubscriberCount()).toBe(0);
    expect(unsubscribeSpy).toHaveBeenCalledTimes(1);
  });
});
