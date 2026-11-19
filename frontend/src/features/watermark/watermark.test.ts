import { describe, expect, it, beforeEach, vi } from "vitest";
import React from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { appConfig } from "../../config/appConfig";
import { NodeTypes } from "../../contracts/generated";
import { useWatermarkStore } from "./useWatermarkStore";
import { WatermarkView } from "./WatermarkView";
import WatermarkViewDefault from "./WatermarkView";
import { WatermarkConfigCard } from "./WatermarkConfigCard";
import WatermarkConfigCardDefault from "./WatermarkConfigCard";
import { WatermarkResultsCard } from "./WatermarkResultsCard";
import WatermarkResultsCardDefault from "./WatermarkResultsCard";
import { rtClient } from "../../lib/rtClient";
import * as rtMedia from "../../lib/rtClient/media";
import { ActionError } from "../../lib/rtClient/errors";
import { useConnectionStore } from "../connection/useConnectionStore";
import { Actions, type Spec } from "../../contracts/generated";
import { useNodeSpecsStore } from "../workflow/useNodeSpecs";

// The node spec as the backend sends it (workflow:specs); the UI must take its choices from here.
const watermarkSpec: Spec = {
  type: "tool.watermark_remover",
  provider: "tool",
  label: "Watermark Remover",
  description: "",
  inputs: [],
  outputs: [],
  params: [
    { name: "workers", label: "Workers", kind: "integer", min: 0, max: 64, default: "0" },
    {
      name: "corner",
      label: "Corner",
      kind: "select",
      options: ["bottom-right", "bottom-left", "top-right", "top-left", "any"],
      default: "bottom-right",
    },
  ],
};
useNodeSpecsStore.setState({ specs: [watermarkSpec], loading: false, error: null });

// In Node SSR environment, mock useSyncExternalStore to return client snapshot (getState)
// so dynamic store state mutations are reflected in server-rendered markup.
vi.spyOn(React, "useSyncExternalStore").mockImplementation((_subscribe, getSnapshot) =>
  getSnapshot()
);

describe("Watermark module exports", () => {
  it("exports both named and default exports for all decomposed components", () => {
    expect(WatermarkView).toBeDefined();
    expect(WatermarkViewDefault).toBe(WatermarkView);

    expect(WatermarkConfigCard).toBeDefined();
    expect(WatermarkConfigCardDefault).toBe(WatermarkConfigCard);

    expect(WatermarkResultsCard).toBeDefined();
    expect(WatermarkResultsCardDefault).toBe(WatermarkResultsCard);
  });
});

describe("Watermark UI Components Runtime Rendering & State Bindings", () => {
  beforeEach(() => {
    useWatermarkStore.getState().reset();
    useWatermarkStore.setState({
      inPlace: false,
      outputDir: "",
      workers: null,
      corner: null,
    });
  });

  it("renders WatermarkView in default initial state without crashing", () => {
    const html = renderToStaticMarkup(React.createElement(WatermarkView));
    expect(html).toContain("dashboard-layout");
    expect(html).toContain("dashboard-hero");
    expect(html).toContain("disabled");
    expect(html).toContain("Queue is empty");
    expect(html).toContain("Drop images here or browse");
  });

  it("renders with queued single file and multiple files correctly", () => {
    useWatermarkStore.getState().setInputPath("C:/images/test1.png");
    let html = renderToStaticMarkup(React.createElement(WatermarkView));
    expect(html).toContain("C:/images/test1.png");
    expect(html).toContain("Reset");

    useWatermarkStore
      .getState()
      .setInputPaths(["C:/images/test1.png", "C:/images/test2.jpg", "C:/images/test3.webp"]);
    html = renderToStaticMarkup(React.createElement(WatermarkView));
    expect(html).toContain("C:/images/test1.png");
    expect(html).toContain("C:/images/test2.jpg");
    expect(html).toContain("C:/images/test3.webp");
    expect(html).toContain("Ready");
  });

  it("properly renders output directory controls according to inPlace state", () => {
    let html = renderToStaticMarkup(React.createElement(WatermarkConfigCard));
    expect(html).toContain("Default: workspace watermark folder");

    useWatermarkStore.getState().setInPlace(true);
    html = renderToStaticMarkup(React.createElement(WatermarkConfigCard));
    expect(html).not.toContain("Default: workspace watermark folder");
  });

  it("renders corner and workers select bindings accurately", () => {
    useWatermarkStore.getState().setCorner("top-left");
    useWatermarkStore.getState().setWorkers(8);

    const html = renderToStaticMarkup(React.createElement(WatermarkConfigCard));
    expect(html).toContain('value="top-left"');
    expect(html).toContain('value="8"');
  });

  it("offers every worker count up to the spec's max and preselects spec defaults", () => {
    const html = renderToStaticMarkup(React.createElement(WatermarkConfigCard));
    expect(html).toContain('value="64"');
    expect(html).toContain('value="32"');
    expect(html).toMatch(/<option value="bottom-right" selected/);
    expect(html).toContain("Top-Left");
  });

  it("shows live per-image progress while a batch runs", () => {
    useWatermarkStore.setState({
      inputPaths: ["C:/a.png", "C:/b.png"],
      loading: true,
      progress: {
        percent: 50,
        message: "Processed 1 / 2 images",
        msg_key: "workflow.progress.items",
        msg_params: { done: "1", total: "2" },
      },
    });
    const html = renderToStaticMarkup(React.createElement(WatermarkResultsCard));
    expect(html).toContain("Processed 1 / 2 images");
    expect(html).toContain('role="progressbar"');
    expect(html).toContain("width:50%");
  });

  it("renders loading state with spinner in both header button and results card", () => {
    useWatermarkStore.setState({
      inputPaths: ["C:/img.png"],
      loading: true,
    });

    const html = renderToStaticMarkup(React.createElement(WatermarkView));
    expect(html).toContain("animate-spin");
    expect(html).toContain("Processing...");
  });

  it("renders single-image result with copy path button and KPI stats", () => {
    useWatermarkStore.setState({
      inputPath: "C:/input.png",
      result: {
        images: [],
        image: {
          localUrl: "C:/output/input_clean.png",
          kind: "image/png",
        },
        detected: true,
        total_count: 1,
        detected_count: 1,
        results: [],
      },
    });

    const html = renderToStaticMarkup(React.createElement(WatermarkView));
    expect(html).toContain("C:/output/input_clean.png");
    expect(html).toContain("Watermark Removed");
    expect(html).toContain("Already Clean");
  });

  it("renders batch results with clean, detected, and error items", () => {
    useWatermarkStore.setState({
      inputPaths: ["C:/a.png", "C:/b.png", "C:/c.png"],
      result: {
        images: [],
        image: { localUrl: "C:/a_clean.png", kind: "image/png" },
        detected: true,
        total_count: 3,
        detected_count: 2,
        results: [
          {
            path: "C:/a.png",
            out_path: "C:/a_clean.png",
            detected: true,
            score: 0.95,
          },
          {
            path: "C:/b.png",
            out_path: "C:/b_clean.png",
            detected: false,
            score: 0.05,
          },
          {
            path: "C:/c.png",
            out_path: "C:/c.png",
            detected: false,
            score: 0,
            error: "Corrupted JPEG header",
          },
        ],
      },
    });

    const html = renderToStaticMarkup(React.createElement(WatermarkResultsCard));
    expect(html).toContain("C:/a.png");
    expect(html).toContain("C:/a_clean.png");
    expect(html).toContain("C:/b.png");
    expect(html).toContain("C:/c.png");
    expect(html).toContain("Corrupted JPEG header");
    expect(html).toContain("Removed (95%)");
    expect(html).toContain("Clean");
    expect(html).toContain("Error");
  });

  it("renders global ActionError banner in WatermarkView", () => {
    useWatermarkStore.setState({
      error: new ActionError("ERR_INTERNAL", "Critical GPU worker crash"),
    });

    const html = renderToStaticMarkup(React.createElement(WatermarkView));
    expect(html).toContain("Critical GPU worker crash");
  });
});

describe("Adversarial Stress Testing & Edge Cases", () => {
  beforeEach(() => {
    useWatermarkStore.getState().reset();
  });

  it("safely escapes potential XSS / script injection in file paths and errors", () => {
    const maliciousPath = '<script>alert("xss")</script>.png';
    const maliciousError = "<img src=x onerror=alert(1)>";
    useWatermarkStore.setState({
      inputPaths: [maliciousPath, "other.png"],
      result: {
        images: [],
        image: { localUrl: maliciousPath, kind: "image/png" },
        detected: false,
        total_count: 2,
        detected_count: 0,
        results: [
          {
            path: maliciousPath,
            out_path: maliciousPath,
            detected: false,
            score: 0,
            error: maliciousError,
          },
          {
            path: "other.png",
            out_path: "other.png",
            detected: false,
            score: 0,
          },
        ],
      },
    });

    const html = renderToStaticMarkup(React.createElement(WatermarkView));
    expect(html).not.toContain('<script>alert("xss")</script>');
    expect(html).not.toContain("<img src=x onerror=alert(1)>");
    expect(html).toContain("&lt;script&gt;");
    expect(html).toContain("&lt;img src=x");
  });

  it("survives rendering large file batches without crashing or memory exhaustion", () => {
    const largeQueue = Array.from({ length: 500 }, (_, i) => `D:/photos/batch_input_${i}.jpg`);
    useWatermarkStore.setState({
      inputPaths: largeQueue,
    });

    const start = performance.now();
    const html = renderToStaticMarkup(React.createElement(WatermarkResultsCard));
    const duration = performance.now() - start;

    expect(html).toContain("500 images in queue");
    expect(html).toContain("D:/photos/batch_input_0.jpg");
    expect(html).toContain("D:/photos/batch_input_499.jpg");
    expect(duration).toBeLessThan(1000);
  });

  it("handles extreme KPI numbers gracefully", () => {
    useWatermarkStore.setState({
      result: {
        images: [],
        image: { localUrl: "res.png", kind: "image/png" },
        detected: true,
        total_count: 1_000_000,
        detected_count: 999_999,
        results: [],
      },
    });

    const html = renderToStaticMarkup(React.createElement(WatermarkResultsCard));
    expect(html).toContain("1000000");
    expect(html).toContain("999999");
    expect(html).toContain("1");
  });

  it("handles unicode, emojis, and non-ASCII file paths across operating systems", () => {
    const unicodePath = "C:/Users/NhậtHào/Ảnh/tiếng_việt_có_dấu_🎨_✨.png";
    useWatermarkStore.setState({
      inputPaths: [unicodePath],
      result: {
        images: [],
        image: { localUrl: unicodePath, kind: "image/png" },
        detected: true,
        total_count: 1,
        detected_count: 1,
        results: [],
      },
    });

    const html = renderToStaticMarkup(React.createElement(WatermarkResultsCard));
    expect(html).toContain("tiếng_việt_có_dấu_🎨_✨.png");
  });

  it("handles missing optional fields in WatermarkItemResult without crashing", () => {
    useWatermarkStore.setState({
      result: {
        images: [],
        image: { localUrl: "test.png", kind: "image/png" },
        detected: false,
        total_count: 2,
        detected_count: 0,
        results: [
          {
            path: "in1.png",
            out_path: "",
            detected: false,
            score: 0,
          },
          {
            path: "in2.png",
            out_path: "in2.png",
            detected: true,
            score: 1.25,
          },
        ],
      },
    });

    const html = renderToStaticMarkup(React.createElement(WatermarkResultsCard));
    expect(html).toContain("in1.png");
    expect(html).toContain("in2.png");
    expect(html).toContain("Removed (125%)");
  });

  it("handles results payload when results array is empty or undefined", () => {
    useWatermarkStore.setState({
      result: {
        images: [],
        image: { localUrl: "single.png", kind: "image/png" },
        detected: true,
        total_count: 1,
        detected_count: 1,
        results: [],
      },
    });

    const html = renderToStaticMarkup(React.createElement(WatermarkResultsCard));
    expect(html).toContain("single.png");
  });
});

describe("useWatermarkStore Actions & Edge Case Robustness", () => {
  beforeEach(() => {
    useWatermarkStore.getState().reset();
  });

  it("setInputPath synchronizes both inputPath and inputPaths", () => {
    const store = useWatermarkStore.getState();
    store.setInputPath("C:/sample.png");

    expect(useWatermarkStore.getState().inputPath).toBe("C:/sample.png");
    expect(useWatermarkStore.getState().inputPaths).toEqual(["C:/sample.png"]);

    store.setInputPath("");
    expect(useWatermarkStore.getState().inputPath).toBe("");
    expect(useWatermarkStore.getState().inputPaths).toEqual([]);
  });

  it("setInputPaths synchronizes inputPath to first entry or empty", () => {
    const store = useWatermarkStore.getState();
    store.setInputPaths(["C:/first.png", "C:/second.png"]);

    expect(useWatermarkStore.getState().inputPath).toBe("C:/first.png");
    expect(useWatermarkStore.getState().inputPaths).toEqual(["C:/first.png", "C:/second.png"]);

    store.setInputPaths([]);
    expect(useWatermarkStore.getState().inputPath).toBe("");
    expect(useWatermarkStore.getState().inputPaths).toEqual([]);
  });

  it("removeWatermark does nothing when queue is empty", async () => {
    const requestSpy = vi.spyOn(rtClient, "request");
    await useWatermarkStore.getState().removeWatermark();
    expect(requestSpy).not.toHaveBeenCalled();
    requestSpy.mockRestore();
  });

  it("removeWatermark sends correct payload and handles successful response", async () => {
    const mockOutput = {
      image: { localUrl: "out.png", kind: "image/png" },
      detected: true,
      total_count: 1,
      detected_count: 1,
    };
    const requestSpy = vi.spyOn(rtClient, "request").mockResolvedValueOnce({
      output: mockOutput,
    } as never);

    useWatermarkStore.getState().setInputPath("C:/target.png");
    useWatermarkStore.getState().setInPlace(true);
    useWatermarkStore.getState().setWorkers(4);
    useWatermarkStore.getState().setCorner("top-right");

    await useWatermarkStore.getState().removeWatermark();

    expect(requestSpy).toHaveBeenCalledWith(
      Actions.WORKFLOW_RUN_NODE,
      {
        type: "tool.watermark_remover",
        input: {
          images: [{ localUrl: "C:/target.png", kind: "image" }],
          inplace: true,
          output_dir: "",
          copy_clean: true,
          preserve_tree: false,
          workers: 4,
          corner: "top-right",
        },
      },
      expect.objectContaining({ onProgress: expect.any(Function) })
    );

    expect(useWatermarkStore.getState().loading).toBe(false);
    expect(useWatermarkStore.getState().result).toEqual(mockOutput);
    expect(useWatermarkStore.getState().error).toBeNull();
    requestSpy.mockRestore();
  });

  it("removeWatermark leaves unset settings to the node's defaults", async () => {
    const requestSpy = vi
      .spyOn(rtClient, "request")
      .mockResolvedValueOnce({ output: { images: [] } } as never);
    useWatermarkStore.setState({ workers: null, corner: null, inPlace: false, outputDir: "" });
    useWatermarkStore.getState().setInputPath("C:/x.png");
    await useWatermarkStore.getState().removeWatermark();
    const payload = requestSpy.mock.calls[0][1] as { input: Record<string, unknown> };
    expect(payload.input).not.toHaveProperty("workers");
    expect(payload.input).not.toHaveProperty("corner");
    requestSpy.mockRestore();
  });

  it("removeWatermark catches client failure and populates error state", async () => {
    const requestSpy = vi
      .spyOn(rtClient, "request")
      .mockRejectedValueOnce(new Error("Timeout waiting for node execution"));

    useWatermarkStore.getState().setInputPath("C:/failing.png");
    await useWatermarkStore.getState().removeWatermark();

    expect(useWatermarkStore.getState().loading).toBe(false);
    expect(useWatermarkStore.getState().result).toBeNull();
    expect(useWatermarkStore.getState().error).toBeDefined();
    expect(useWatermarkStore.getState().error?.message).toContain(
      "Timeout waiting for node execution"
    );
    requestSpy.mockRestore();
  });

  it("reset restores store to clean initial state", () => {
    useWatermarkStore.setState({
      inputPath: "C:/foo.png",
      inputPaths: ["C:/foo.png"],
      result: {
        image: { localUrl: "bar.png", kind: "image/png" },
        images: [],
        detected: true,
        total_count: 1,
        detected_count: 1,
        results: [],
      },
      error: new ActionError("ERR_INTERNAL", "some error"),
      loading: true,
    });

    useWatermarkStore.getState().reset();

    const state = useWatermarkStore.getState();
    expect(state.inputPath).toBe("");
    expect(state.inputPaths).toEqual([]);
    expect(state.result).toBeNull();
    expect(state.error).toBeNull();
    expect(state.loading).toBe(false);
  });

  it("handles absolute file paths in useWatermarkStore correctly without truncation", () => {
    const windowsAbsPaths = [
      "D:\\Photos\\Vacation 2026\\IMG_001.png",
      "D:\\Photos\\Vacation 2026\\IMG_002.jpg",
    ];

    useWatermarkStore.getState().setInputPaths(windowsAbsPaths);

    const state = useWatermarkStore.getState();
    expect(state.inputPaths).toEqual(windowsAbsPaths);
    expect(state.inputPath).toBe("D:\\Photos\\Vacation 2026\\IMG_001.png");
  });

  it("uploads queued browser Files to media staging before running workflow node", async () => {
    const epSpy = vi.spyOn(rtClient, "getEndpoint").mockReturnValue({
      url: "ws://127.0.0.1:8080/ws",
      token: "tok",
      mode: "browser",
      actionTimeoutMs: 30_000,
      longActionTimeoutMs: 600_000,
      pingIntervalMs: 30_000,
      checkUpdatesOnStartup: false,
      source: "runtime-config",
    });
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(
        new Response(
          JSON.stringify({ path: "/srv/media/staging/job1/photo.png", name: "photo.png", size: 4 }),
          { status: 200, headers: { "Content-Type": "application/json" } }
        )
      );
    vi.stubGlobal("fetch", fetchMock);
    const requestSpy = vi.spyOn(rtClient, "request").mockResolvedValueOnce({
      output: {
        image: { localUrl: "/srv/media/staging/job1/photo_nowm.png", kind: "image/png" },
        images: [],
        detected: true,
        total_count: 1,
        detected_count: 1,
      },
    } as never);

    const file = new File([new Uint8Array([1, 2, 3, 4])], "photo.png", { type: "image/png" });
    useWatermarkStore.getState().setOutputDir("C:/ignored/client/dir");
    useWatermarkStore.getState().setQueuedFiles([file]);
    expect(useWatermarkStore.getState().inputPaths).toEqual(["photo.png"]);

    await useWatermarkStore.getState().removeWatermark();

    expect(fetchMock).toHaveBeenCalledWith(
      "http://127.0.0.1:8080/api/media/upload",
      expect.objectContaining({ method: "POST", headers: { Authorization: "Bearer tok" } })
    );
    expect(requestSpy).toHaveBeenCalledWith(
      Actions.WORKFLOW_RUN_NODE,
      {
        type: "tool.watermark_remover",
        input: {
          images: [{ localUrl: "/srv/media/staging/job1/photo.png", kind: "image" }],
          inplace: false,
          output_dir: "",
          copy_clean: true,
          preserve_tree: false,
        },
      },
      expect.any(Object)
    );
    expect(useWatermarkStore.getState().remoteNameMap).toEqual({
      "/srv/media/staging/job1/photo.png": "photo.png",
    });

    requestSpy.mockRestore();
    epSpy.mockRestore();
    vi.unstubAllGlobals();
  });

  it("hides outputDir and renders Download actions in browser and client modes", () => {
    const prevEndpoint = useConnectionStore.getState().endpoint;
    useConnectionStore.setState({
      endpoint: {
        url: "wss://core.example.com/ws",
        token: "tok",
        mode: "browser",
        actionTimeoutMs: 30_000,
        longActionTimeoutMs: 600_000,
        pingIntervalMs: 30_000,
        checkUpdatesOnStartup: false,
        source: "runtime-config",
      },
    });
    useWatermarkStore.setState({
      remoteNameMap: { "/srv/media/staging/id/input.png": "input.png" },
      result: {
        images: [],
        image: { localUrl: "/srv/media/staging/id/input_nowm.png", kind: "image/png" },
        detected: true,
        total_count: 2,
        detected_count: 1,
        results: [
          {
            path: "/srv/media/staging/id/input.png",
            out_path: "/srv/media/staging/id/input_nowm.png",
            detected: true,
            score: 0.9,
          },
          {
            path: "/srv/media/staging/id/clean.png",
            out_path: "/srv/media/staging/id/clean.png",
            detected: false,
            score: 0.1,
          },
        ],
      },
    });

    try {
      const cfgHtml = renderToStaticMarkup(React.createElement(WatermarkConfigCard));
      expect(cfgHtml).not.toContain("Default: workspace watermark folder");

      const resHtml = renderToStaticMarkup(React.createElement(WatermarkResultsCard));
      expect(resHtml).toContain('aria-label="Download"');
      expect(resHtml).not.toContain('aria-label="Copy path"');
      expect(resHtml).toContain("→ input_nowm.png<");
      expect(resHtml).toContain(">input.png<");
      expect(resHtml).not.toContain(">/srv/media/staging/id/input.png<");
    } finally {
      useConnectionStore.setState({ endpoint: prevEndpoint });
    }
  });
});

describe("Phase 1 Wave B Watermark Policy & Compare UX Enhancement", () => {
  beforeEach(() => {
    useWatermarkStore.getState().reset();
    useWatermarkStore.setState({
      inPlace: false,
      outputDir: "",
      workers: null,
      corner: null,
      copyClean: true,
      preserveTree: false,
      comparisonItem: null,
      comparisonZoom: 1.0,
      comparisonPan: { x: 0, y: 0 },
      comparisonSliderPos: 50,
    });
  });

  it("toggles CopyClean and PreserveTree in store and includes them in removeWatermark payload", async () => {
    const store = useWatermarkStore.getState();
    expect(store.copyClean).toBe(true);
    expect(store.preserveTree).toBe(false);

    store.setCopyClean(false);
    store.setPreserveTree(true);

    expect(useWatermarkStore.getState().copyClean).toBe(false);
    expect(useWatermarkStore.getState().preserveTree).toBe(true);

    const requestSpy = vi.spyOn(rtClient, "request").mockResolvedValueOnce({
      output: { images: [], total_count: 1, detected_count: 0 },
    } as never);

    useWatermarkStore.getState().setInputPath("C:/test.png");
    await useWatermarkStore.getState().removeWatermark();

    expect(requestSpy).toHaveBeenCalledWith(
      Actions.WORKFLOW_RUN_NODE,
      expect.objectContaining({
        input: expect.objectContaining({
          copy_clean: false,
          preserve_tree: true,
        }),
      }),
      expect.any(Object)
    );
    requestSpy.mockRestore();
  });

  it("renders status badges for processed, copied, skipped, and failed items", () => {
    useWatermarkStore.setState({
      inputPaths: ["C:/1.png", "C:/2.png", "C:/3.png", "C:/4.png"],
      result: {
        images: [],
        image: { localUrl: "C:/1_nowm.png", kind: "image/png" },
        detected: true,
        total_count: 4,
        detected_count: 1,
        results: [
          {
            path: "C:/1.png",
            out_path: "C:/1_nowm.png",
            detected: true,
            score: 0.92,
            status: "processed",
          },
          {
            path: "C:/2.png",
            out_path: "C:/out/2.png",
            detected: false,
            score: 0.0,
            status: "copied",
          },
          {
            path: "C:/3.png",
            out_path: "C:/out/3.png",
            detected: false,
            score: 0.0,
            status: "skipped",
            reason: "output already exists with identical parameters",
          },
          {
            path: "C:/4.png",
            out_path: "",
            detected: false,
            score: 0.0,
            status: "failed",
            error: "corrupted image data",
          },
        ],
      },
    });

    const html = renderToStaticMarkup(React.createElement(WatermarkResultsCard));
    expect(html).toContain("watermark-badge-connected");
    expect(html).toContain("Removed (92%)");
    expect(html).toContain("watermark-badge-copied");
    expect(html).toContain("Copied");
    expect(html).toContain("watermark-badge-skipped");
    expect(html).toContain("Skipped");
    expect(html).toContain("output already exists with identical parameters");
    expect(html).toContain("watermark-badge-error");
    expect(html).toContain("Error");
  });

  it("manages comparison state and respects appConfig.ui.watermarkCompare limits", () => {
    const store = useWatermarkStore.getState();
    const { zoomMax, zoomStep } = appConfig.ui.watermarkCompare;

    store.setComparisonItem({
      beforeUrl: "C:/input.png",
      afterUrl: "C:/output_nowm.png",
      title: "Comparison Sample",
    });

    expect(useWatermarkStore.getState().comparisonItem).toEqual({
      beforeUrl: "C:/input.png",
      afterUrl: "C:/output_nowm.png",
      title: "Comparison Sample",
    });
    expect(useWatermarkStore.getState().comparisonZoom).toBe(1.0);
    expect(useWatermarkStore.getState().comparisonSliderPos).toBe(50);

    // Zoom in
    store.setComparisonZoom((z) => Math.min(zoomMax, +(z + zoomStep).toFixed(2)));
    expect(useWatermarkStore.getState().comparisonZoom).toBe(+(1.0 + zoomStep).toFixed(2));

    // Pan
    store.setComparisonPan({ x: 25, y: -10 });
    expect(useWatermarkStore.getState().comparisonPan).toEqual({ x: 25, y: -10 });

    // Slider pos
    store.setComparisonSliderPos(75);
    expect(useWatermarkStore.getState().comparisonSliderPos).toBe(75);

    // Reset comparison
    store.resetComparison();
    expect(useWatermarkStore.getState().comparisonItem).toBeNull();
    expect(useWatermarkStore.getState().comparisonZoom).toBe(1.0);
    expect(useWatermarkStore.getState().comparisonPan).toEqual({ x: 0, y: 0 });
    expect(useWatermarkStore.getState().comparisonSliderPos).toBe(50);
  });

  it("renders ComparisonViewer when comparisonItem is active in results card", () => {
    useWatermarkStore.setState({
      inputPath: "C:/photo.png",
      comparisonItem: {
        beforeUrl: "C:/photo.png",
        afterUrl: "C:/photo_nowm.png",
        title: "photo.png",
      },
      result: {
        images: [],
        image: { localUrl: "C:/photo_nowm.png", kind: "image/png" },
        detected: true,
        total_count: 1,
        detected_count: 1,
        results: [],
      },
    });

    const html = renderToStaticMarkup(React.createElement(WatermarkResultsCard));
    expect(html).toContain("watermark-comparison-viewer");
    expect(html).toContain('aria-label="Before/After split slider"');
    expect(html).toContain('aria-label="Zoom in"');
    expect(html).toContain('aria-label="Zoom out"');
    expect(html).toContain('aria-label="Close comparison"');
  });

  it("dismisses error when dismiss button is triggered or dismissError action called", () => {
    useWatermarkStore.setState({
      error: new ActionError("ERR_VALIDATION", "Test dismissable error"),
    });

    let html = renderToStaticMarkup(React.createElement(WatermarkView));
    expect(html).toContain("Test dismissable error");
    expect(html).toContain('aria-label="Dismiss error"');

    useWatermarkStore.getState().dismissError();
    expect(useWatermarkStore.getState().error).toBeNull();

    html = renderToStaticMarkup(React.createElement(WatermarkView));
    expect(html).not.toContain("Test dismissable error");
  });

  it("tracks duplicate queued file names distinctly and revokes removed index without collision", () => {
    const createSpy = vi.spyOn(URL, "createObjectURL").mockImplementation(() => `blob:mock-${Math.random()}`);
    const revokeSpy = vi.spyOn(URL, "revokeObjectURL").mockImplementation(() => {});
    try {
      const f1 = new File(["one"], "dup.png", { type: "image/png" });
      const f2 = new File(["two"], "dup.png", { type: "image/png" });
      useWatermarkStore.getState().setQueuedFiles([f1, f2]);
      const keys = useWatermarkStore.getState().queuedObjectKeys;
      expect(keys).toHaveLength(2);
      expect(keys[0]).not.toBe(keys[1]);

      useWatermarkStore.getState().removeQueuedFile(0);
      expect(useWatermarkStore.getState().queuedFiles).toEqual([f2]);
      expect(revokeSpy).toHaveBeenCalledTimes(1);
    } finally {
      createSpy.mockRestore();
      revokeSpy.mockRestore();
    }
  });

  it("subtracts failed items from cleanCount KPI in WatermarkResultsCard", () => {
    useWatermarkStore.setState({
      result: {
        detected: true,
        total_count: 5,
        detected_count: 2,
        results: [
          { path: "a.png", out_path: "a_out.png", status: "processed", detected: true, score: 0.9 },
          { path: "b.png", out_path: "b_out.png", status: "processed", detected: true, score: 0.9 },
          { path: "c.png", out_path: "c_out.png", status: "clean", detected: false, score: 0.1 },
          { path: "d.png", out_path: "", status: "error", detected: false, error: "corrupt file" },
          { path: "e.png", out_path: "", status: "error", detected: false, error: "unreadable" },
        ],
      } as unknown as import("./useWatermarkStore").WatermarkNodeResult,
    });
    const html = renderToStaticMarkup(React.createElement(WatermarkResultsCard));
    // total=5, detected=2, failed=2 => cleanCount=1 (not 3)
    expect(html).toContain('>1<');
  });

  it("supports video watermark removal mode, delogo/crop params, and invokes tool.video_watermark_remove", async () => {
    const reqSpy = vi.spyOn(rtClient, "request").mockResolvedValue({
      output: {
        video: { localUrl: "D:/out/clean.mp4", kind: "video" },
        path: "D:/out/clean.mp4",
        asset_id: "vid-wm-1",
        duration_sec: 12,
      },
    } as never);
    useWatermarkStore.getState().setMediaKind("video");
    useWatermarkStore.getState().setVideoInputPath("D:/in/clip.mp4");
    useWatermarkStore.getState().setVideoMode("delogo");
    useWatermarkStore.getState().setVideoX(10);
    useWatermarkStore.getState().setVideoY(20);
    useWatermarkStore.getState().setVideoWidth(120);
    useWatermarkStore.getState().setVideoHeight(60);

    await useWatermarkStore.getState().removeVideoWatermark();
    expect(reqSpy).toHaveBeenCalledWith(
      Actions.WORKFLOW_RUN_NODE,
      expect.objectContaining({
        type: NodeTypes.VIDEO_WATERMARK_REMOVE,
        input: expect.objectContaining({
          video_path: { localUrl: "D:/in/clip.mp4", kind: "video" },
          mode: "delogo",
          x: 10,
          y: 20,
          width: 120,
          height: 60,
        }),
      }),
      expect.any(Object)
    );
    expect(useWatermarkStore.getState().videoResult?.path).toBe("D:/out/clean.mp4");

    const html = renderToStaticMarkup(React.createElement(WatermarkView));
    expect(html).toContain("D:/out/clean.mp4");
  });
});

// Keep reference used in typecheck when media module is inspected by tests.
void rtMedia;
