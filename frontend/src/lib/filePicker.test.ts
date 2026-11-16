import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { pickMediaFiles } from "./filePicker";

describe("filePicker", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
    (globalThis as unknown as { window?: Record<string, unknown> }).window = {};
  });

  afterEach(() => {
    delete (globalThis as unknown as { window?: Record<string, unknown> }).window;
  });

  it("calls bridge.BrowseImages when multiple is true in desktop mode", async () => {
    const mockBrowseImages = vi.fn().mockResolvedValue(["C:\\test1.png", "C:\\test2.png"]);
    (window as unknown as { go: { desktop: { Bridge: unknown } } }).go = {
      desktop: {
        Bridge: {
          BrowseImages: mockBrowseImages,
          BrowseImage: vi.fn(),
        },
      },
    };

    const res = await pickMediaFiles({ multiple: true, title: "Choose Photos" });
    expect(mockBrowseImages).toHaveBeenCalledWith("Choose Photos");
    expect(res.paths).toEqual(["C:\\test1.png", "C:\\test2.png"]);
    expect(res.files).toEqual([]);
  });

  it("calls bridge.BrowseImage when multiple is false in desktop mode", async () => {
    const mockBrowseImage = vi.fn().mockResolvedValue("C:\\single.png");
    (window as unknown as { go: { desktop: { Bridge: unknown } } }).go = {
      desktop: {
        Bridge: {
          BrowseImages: vi.fn(),
          BrowseImage: mockBrowseImage,
        },
      },
    };

    const res = await pickMediaFiles({ multiple: false, title: "Single Photo" });
    expect(mockBrowseImage).toHaveBeenCalledWith("Single Photo");
    expect(res.paths).toEqual(["C:\\single.png"]);
    expect(res.files).toEqual([]);
  });

  it("returns empty paths cleanly if user cancels native dialog without error or fallback", async () => {
    const mockBrowseImages = vi.fn().mockResolvedValue([]);
    (window as unknown as { go: { desktop: { Bridge: unknown } } }).go = {
      desktop: {
        Bridge: {
          BrowseImages: mockBrowseImages,
        },
      },
    };

    const res = await pickMediaFiles({ multiple: true, title: "Pick" });
    expect(mockBrowseImages).toHaveBeenCalledTimes(1);
    expect(res.paths).toEqual([]);
    expect(res.files).toEqual([]);
  });

  it("supports directory kind via bridge.BrowseDirectory", async () => {
    const mockBrowseDir = vi.fn().mockResolvedValue("C:\\output");
    (window as unknown as { go: { desktop: { Bridge: unknown } } }).go = {
      desktop: {
        Bridge: {
          BrowseDirectory: mockBrowseDir,
        },
      },
    };

    const res = await pickMediaFiles({ kind: "directory", title: "Select Folder" });
    expect(mockBrowseDir).toHaveBeenCalledWith("Select Folder");
    expect(res.paths).toEqual(["C:\\output"]);
  });

  it("supports executable kind via bridge.BrowseExecutable", async () => {
    const mockBrowseExe = vi.fn().mockResolvedValue("C:\\ffmpeg.exe");
    (window as unknown as { go: { desktop: { Bridge: unknown } } }).go = {
      desktop: {
        Bridge: {
          BrowseExecutable: mockBrowseExe,
        },
      },
    };

    const res = await pickMediaFiles({ kind: "executable", title: "Select Exe" });
    expect(mockBrowseExe).toHaveBeenCalledWith("Select Exe");
    expect(res.paths).toEqual(["C:\\ffmpeg.exe"]);
  });
});
