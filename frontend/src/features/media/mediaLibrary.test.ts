import { describe, expect, it, vi, beforeEach } from "vitest";
import React from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { useMediaLibraryStore } from "./useMediaLibraryStore";
import { MediaLibraryDrawer } from "./MediaLibraryDrawer";
import * as mediaClient from "../../lib/rtClient/media";
import type { MediaAssetItem } from "../../contracts/generated";

const sampleAssets: MediaAssetItem[] = [
  {
    id: "asset-1",
    kind: "image",
    source_node: "tool.image_generate",
    file_path: "D:/media/generated/img1.png",
    registered_root: "D:/media",
    mime_type: "image/png",
    size_bytes: 1024 * 500, // 500 KB
    width: 1024,
    height: 1024,
    created_at: 1700000000,
    last_verified_at: 1700000000,
  },
  {
    id: "asset-2",
    kind: "video",
    source_node: "tool.video_generate",
    file_path: "D:/media/generated/clip1.mp4",
    registered_root: "D:/media",
    mime_type: "video/mp4",
    size_bytes: 1024 * 1024 * 5, // 5 MB
    duration_sec: 12.5,
    created_at: 1700000100,
    last_verified_at: 1700000100,
  },
];

describe("MediaLibrary Store and Drawer", () => {
  let listSpy: ReturnType<typeof vi.spyOn>;
  let deleteSpy: ReturnType<typeof vi.spyOn>;

  beforeEach(() => {
    vi.restoreAllMocks();
    vi.spyOn(React, "useSyncExternalStore").mockImplementation((_s, getSnapshot) => getSnapshot());
    useMediaLibraryStore.setState({
      drawerOpen: false,
      assets: [],
      total: 0,
      loading: false,
      deletingId: null,
      error: null,
      kindFilter: "",
      searchQuery: "",
    });
    listSpy = vi.spyOn(mediaClient, "listMediaAssets");
    deleteSpy = vi.spyOn(mediaClient, "deleteMediaAsset");
  });

  it("manages drawer open/close/toggle state", () => {
    const store = useMediaLibraryStore.getState();
    expect(useMediaLibraryStore.getState().drawerOpen).toBe(false);

    store.openDrawer();
    expect(useMediaLibraryStore.getState().drawerOpen).toBe(true);

    store.closeDrawer();
    expect(useMediaLibraryStore.getState().drawerOpen).toBe(false);

    store.toggleDrawer();
    expect(useMediaLibraryStore.getState().drawerOpen).toBe(true);
  });

  it("fetchAssets queries media:list with kind and search filters", async () => {
    listSpy.mockResolvedValueOnce({
      assets: sampleAssets,
      total: 2,
    });

    useMediaLibraryStore.setState({ kindFilter: "image", searchQuery: "img1" });
    await useMediaLibraryStore.getState().fetchAssets();

    expect(listSpy).toHaveBeenCalledWith({
      kind: "image",
      search: "img1",
    });

    expect(useMediaLibraryStore.getState().assets).toEqual(sampleAssets);
    expect(useMediaLibraryStore.getState().total).toBe(2);
    expect(useMediaLibraryStore.getState().loading).toBe(false);
  });

  it("deleteAsset calls media:delete and removes asset from state", async () => {
    deleteSpy.mockResolvedValueOnce(undefined);
    useMediaLibraryStore.setState({
      assets: sampleAssets,
      total: 2,
    });

    await useMediaLibraryStore.getState().deleteAsset("asset-1");

    expect(deleteSpy).toHaveBeenCalledWith({
      id: "asset-1",
      delete_file: false,
    });

    const currentAssets = useMediaLibraryStore.getState().assets;
    expect(currentAssets.length).toBe(1);
    expect(currentAssets[0].id).toBe("asset-2");
    expect(useMediaLibraryStore.getState().total).toBe(1);
  });

  it("renders MediaLibraryDrawer closed (null) by default", () => {
    useMediaLibraryStore.setState({ drawerOpen: false });
    const html = renderToStaticMarkup(React.createElement(MediaLibraryDrawer));
    expect(html).toBe("");
  });

  it("renders MediaLibraryDrawer open with items and action buttons", () => {
    useMediaLibraryStore.setState({
      drawerOpen: true,
      assets: sampleAssets,
      total: 2,
    });

    const html = renderToStaticMarkup(React.createElement(MediaLibraryDrawer));
    expect(html).toContain("Media Library");
    expect(html).toContain("img1.png");
    expect(html).toContain("clip1.mp4");
    expect(html).toContain("Send to Watermark");
    expect(html).toContain("Ask Agent about this file");
  });
});
