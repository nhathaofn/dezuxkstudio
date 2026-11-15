import { describe, expect, it } from "vitest";
import { renderToStaticMarkup } from "react-dom/server";
import {
  computeVirtualRange,
  DEFAULT_ITEM_HEIGHT,
  DEFAULT_OVERSCAN,
  VirtualList,
} from "./VirtualList";

describe("computeVirtualRange calculation unit tests", () => {
  it("handles empty items array gracefully", () => {
    const res = computeVirtualRange({
      items: [],
      scrollTop: 0,
      viewportHeight: 400,
      overscan: 3,
    });

    expect(res.totalHeight).toBe(0);
    expect(res.startIndex).toBe(0);
    expect(res.endIndex).toBe(-1);
    expect(res.visibleItems).toEqual([]);
    expect(res.offsets).toEqual([]);
    expect(res.sizes).toEqual([]);
  });

  it("calculates total height and offsets accurately for fixed height items", () => {
    const items = Array.from({ length: 100 }, (_, i) => ({ id: i, label: `Item ${i}` }));
    const itemHeight = 50;

    const res = computeVirtualRange({
      items,
      itemHeight,
      scrollTop: 0,
      viewportHeight: 300,
      overscan: 2,
    });

    expect(res.totalHeight).toBe(100 * 50); // 5000px
    expect(res.offsets.length).toBe(100);
    expect(res.sizes.length).toBe(100);
    expect(res.offsets[0]).toBe(0);
    expect(res.offsets[10]).toBe(500);
    expect(res.sizes[0]).toBe(50);
  });

  it("windows visible items with overscan at initial scroll position", () => {
    const items = Array.from({ length: 100 }, (_, i) => `row-${i}`);
    const itemHeight = 40;
    const viewportHeight = 200; // raw visible: 0..5 (6 items)
    const overscan = 2;

    const res = computeVirtualRange({
      items,
      itemHeight,
      scrollTop: 0,
      viewportHeight,
      overscan,
    });

    // rawStart = 0, rawEnd = 5
    // startIndex = max(0, 0 - 2) = 0
    // endIndex = min(99, 5 + 2) = 7
    expect(res.startIndex).toBe(0);
    expect(res.endIndex).toBe(7);
    expect(res.visibleItems.length).toBe(8);
    expect(res.visibleItems[0].index).toBe(0);
    expect(res.visibleItems[7].index).toBe(7);
    expect(res.visibleItems[0].offset).toBe(0);
    expect(res.visibleItems[7].offset).toBe(280);
  });

  it("windows visible items correctly when scrolled down mid-list", () => {
    const items = Array.from({ length: 200 }, (_, i) => `item-${i}`);
    const itemHeight = 50;
    const viewportHeight = 400; // 8 items in viewport
    const scrollTop = 1000; // at item index 20
    const overscan = 3;

    const res = computeVirtualRange({
      items,
      itemHeight,
      scrollTop,
      viewportHeight,
      overscan,
    });

    // rawStart = 1000 / 50 = 20
    // rawEnd = (1000 + 400) / 50 = 28
    // startIndex = 20 - 3 = 17
    // endIndex = 28 + 3 = 31
    expect(res.startIndex).toBe(17);
    expect(res.endIndex).toBe(31);
    expect(res.visibleItems.length).toBe(15);
    expect(res.visibleItems[0].item).toBe("item-17");
    expect(res.visibleItems[res.visibleItems.length - 1].item).toBe("item-31");
  });

  it("clamps overscan to list boundaries when scrolled near the end", () => {
    const items = Array.from({ length: 50 }, (_, i) => `item-${i}`);
    const itemHeight = 40;
    const viewportHeight = 300;
    const scrollTop = 1900; // near bottom (total 2000px)
    const overscan = 5;

    const res = computeVirtualRange({
      items,
      itemHeight,
      scrollTop,
      viewportHeight,
      overscan,
    });

    expect(res.endIndex).toBe(49);
    expect(res.startIndex).toBeGreaterThanOrEqual(0);
    expect(res.visibleItems[res.visibleItems.length - 1].index).toBe(49);
  });

  it("supports dynamic variable item heights with prefix sums and binary search", () => {
    const items = Array.from({ length: 20 }, (_, i) => ({ id: i, text: `Item ${i}` }));
    // Alternating heights: even = 30px, odd = 70px (average 50px per item)
    const itemHeightFn = (idx: number) => (idx % 2 === 0 ? 30 : 70);

    const res = computeVirtualRange({
      items,
      itemHeight: itemHeightFn,
      scrollTop: 100, // item 0 (30) + item 1 (70) = 100. ScrollTop reaches item 2.
      viewportHeight: 200,
      overscan: 1,
    });

    // Total height for 20 items: 10 * 30 + 10 * 70 = 1000
    expect(res.totalHeight).toBe(1000);
    expect(res.offsets[0]).toBe(0);
    expect(res.offsets[1]).toBe(30);
    expect(res.offsets[2]).toBe(100);
    expect(res.sizes[0]).toBe(30);
    expect(res.sizes[1]).toBe(70);
    expect(res.sizes[2]).toBe(30);
    expect(res.startIndex).toBeLessThanOrEqual(2);
    expect(res.endIndex).toBeGreaterThan(2);
  });

  it("provides SSR / unmeasured fallback when viewportHeight is 0 or negative", () => {
    const items = Array.from({ length: 50 }, (_, i) => `ssr-${i}`);

    const res = computeVirtualRange({
      items,
      itemHeight: 48,
      scrollTop: 0,
      viewportHeight: 0,
      overscan: 5,
    });

    expect(res.totalHeight).toBe(50 * 48);
    expect(res.startIndex).toBe(0);
    expect(res.visibleItems.length).toBeGreaterThan(0);
    expect(res.visibleItems[0].item).toBe("ssr-0");
  });

  it("uses defaultItemHeight and overscan from appConfig when omitted", () => {
    const items = Array.from({ length: 10 }, (_, i) => `default-${i}`);

    const res = computeVirtualRange({
      items,
      scrollTop: 0,
      viewportHeight: 300,
    });

    expect(res.sizes[0]).toBe(DEFAULT_ITEM_HEIGHT);
    expect(res.totalHeight).toBe(10 * DEFAULT_ITEM_HEIGHT);
    // overscan defaults to DEFAULT_OVERSCAN (5)
    expect(res.endIndex).toBe(
      Math.min(9, Math.floor(300 / DEFAULT_ITEM_HEIGHT) + DEFAULT_OVERSCAN)
    );
  });
});

describe("VirtualList React component rendering", () => {
  it("renders container, inner list, and visible items in static markup", () => {
    const items = [
      { id: "a", name: "Alpha File" },
      { id: "b", name: "Beta File" },
      { id: "c", name: "Gamma File" },
    ];

    const html = renderToStaticMarkup(
      <VirtualList
        items={items}
        height={300}
        itemHeight={50}
        className="test-virtual-container"
        innerClassName="test-virtual-inner"
        getItemKey={(item) => item.id}
        renderItem={(item) => <div className="row-item">{item.name}</div>}
      />
    );

    expect(html).toContain("virtual-list");
    expect(html).toContain("test-virtual-container");
    expect(html).toContain("virtual-list__inner");
    expect(html).toContain("test-virtual-inner");
    expect(html).toContain("Alpha File");
    expect(html).toContain("Beta File");
    expect(html).toContain("Gamma File");
    expect(html).toContain('role="feed"');
    expect(html).toContain('tabindex="0"');
    expect(html).toContain("height:150px"); // 3 * 50px
  });

  it("renders emptyPlaceholder when items list is empty", () => {
    const html = renderToStaticMarkup(
      <VirtualList
        items={[]}
        emptyPlaceholder={<div id="no-data">No entries found</div>}
        renderItem={() => null}
      />
    );

    expect(html).toContain("no-data");
    expect(html).toContain("No entries found");
  });

  it("respects custom ariaLabel and role attributes", () => {
    const html = renderToStaticMarkup(
      <VirtualList
        items={["One", "Two"]}
        role="list"
        ariaLabel="Queued Media Files"
        renderItem={(item) => <span>{item}</span>}
      />
    );

    expect(html).toContain('role="list"');
    expect(html).toContain('aria-label="Queued Media Files"');
    expect(html).toContain("One");
    expect(html).toContain("Two");
  });

  it("renders variable height items with correct transform and height styles", () => {
    const items = ["Short Item", "Tall Detailed Item"];

    const html = renderToStaticMarkup(
      <VirtualList
        items={items}
        height={400}
        itemHeight={(idx) => (idx === 0 ? 40 : 80)}
        renderItem={(item) => <div>{item}</div>}
      />
    );

    expect(html).toContain("height:120px"); // 40 + 80
    expect(html).toContain("Short Item");
    expect(html).toContain("Tall Detailed Item");
  });

  it("supports estimateSize as an alias for itemHeight", () => {
    const items = ["Line 1", "Line 2"];

    const html = renderToStaticMarkup(
      <VirtualList items={items} estimateSize={30} renderItem={(item) => <div>{item}</div>} />
    );

    expect(html).toContain("height:60px");
    expect(html).toContain("Line 1");
    expect(html).toContain("Line 2");
  });
});

describe("useWatermarkStore ObjectURL tracking and memory leak cleanup", () => {
  it("tracks and safely revokes ObjectURLs during lifecycle operations", async () => {
    const { useWatermarkStore } = await import("../../features/watermark/useWatermarkStore");

    const revokedUrls: string[] = [];
    const originalCreate = URL.createObjectURL;
    const originalRevoke = URL.revokeObjectURL;

    let counter = 0;
    URL.createObjectURL = (() =>
      `blob:http://localhost/mock-uuid-${++counter}`) as unknown as typeof URL.createObjectURL;
    URL.revokeObjectURL = ((u: string) => {
      revokedUrls.push(u);
    }) as unknown as typeof URL.revokeObjectURL;

    try {
      const store = useWatermarkStore.getState();
      store.reset();
      revokedUrls.length = 0;

      // 1. Explicitly create tracked ObjectURLs
      const url1 = store.createTrackedObjectURL("img1", new Blob(["test1"]));
      const url2 = store.createTrackedObjectURL("img2", new Blob(["test2"]));

      expect(url1).toContain("blob:http://localhost/mock-uuid-");
      expect(url2).toContain("blob:http://localhost/mock-uuid-");
      expect(useWatermarkStore.getState().objectUrls["img1"]).toBe(url1);
      expect(useWatermarkStore.getState().objectUrls["img2"]).toBe(url2);

      // 2. Replacing existing key revokes old URL
      const url1b = store.createTrackedObjectURL("img1", new Blob(["test1-replacement"]));
      expect(revokedUrls).toContain(url1);
      expect(useWatermarkStore.getState().objectUrls["img1"]).toBe(url1b);

      // 3. Revoke single URL by key
      store.revokeTrackedObjectURL("img2");
      expect(revokedUrls).toContain(url2);
      expect(useWatermarkStore.getState().objectUrls["img2"]).toBeUndefined();

      // 4. Revoke all remaining URLs
      store.revokeAllObjectURLs();
      expect(revokedUrls).toContain(url1b);
      expect(Object.keys(useWatermarkStore.getState().objectUrls).length).toBe(0);

      // 5. Queued files lifecycle
      const dummyFile1 = new File(["data"], "file1.png", { type: "image/png" });
      const dummyFile2 = new File(["data"], "file2.jpg", { type: "image/jpeg" });
      store.setQueuedFiles([dummyFile1, dummyFile2]);

      const queuedUrls = useWatermarkStore.getState().objectUrls;
      const file1Url = queuedUrls["file1.png"];
      const file2Url = queuedUrls["file2.jpg"];
      expect(file1Url).toBeDefined();
      expect(file2Url).toBeDefined();

      // Removing one queued file explicitly revokes its ObjectURL
      store.removeQueuedFile("file1.png");
      expect(revokedUrls).toContain(file1Url);
      expect(useWatermarkStore.getState().queuedFiles.length).toBe(1);
      expect(useWatermarkStore.getState().objectUrls["file1.png"]).toBeUndefined();

      // Setting inputPath clears queued files and revokes remaining URLs
      store.setInputPath("D:/custom.png");
      expect(revokedUrls).toContain(file2Url);
      expect(Object.keys(useWatermarkStore.getState().objectUrls).length).toBe(0);

      // 6. Reset comparison cleans up blob URLs
      store.setComparisonItem({
        beforeUrl: "blob:http://localhost/before",
        afterUrl: "blob:http://localhost/after",
        title: "Test Compare",
      });
      store.resetComparison();
      expect(revokedUrls).toContain("blob:http://localhost/before");
      expect(revokedUrls).toContain("blob:http://localhost/after");
    } finally {
      URL.createObjectURL = originalCreate;
      URL.revokeObjectURL = originalRevoke;
    }
  });
});
