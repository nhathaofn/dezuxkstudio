import { describe, expect, it, beforeEach, vi } from "vitest";
import React from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { useWatermarkStore } from "./watermark/useWatermarkStore";
import { WatermarkView } from "./watermark/WatermarkView";
import WatermarkViewDefault from "./watermark/WatermarkView";
import { WatermarkConfigCard } from "./watermark/WatermarkConfigCard";
import WatermarkConfigCardDefault from "./watermark/WatermarkConfigCard";
import { WatermarkResultsCard } from "./watermark/WatermarkResultsCard";
import WatermarkResultsCardDefault from "./watermark/WatermarkResultsCard";
import { rtClient } from "../lib/rtClient";
import { ActionError } from "../lib/rtClient/errors";
import { Actions } from "../contracts/generated";

// In Node SSR environment, mock useSyncExternalStore to return client snapshot (getState)
// so dynamic store state mutations are reflected in server-rendered markup.
vi.spyOn(React, "useSyncExternalStore").mockImplementation((_subscribe, getSnapshot) =>
  getSnapshot()
);

describe("Watermark Modularization Acceptance Criteria Audit", () => {
  it("strictly contains only the authorized 4 files with no hidden hooks or extra sub-components", () => {
    // import.meta.glob queries the exact files in the watermark feature module
    const modules = import.meta.glob("./watermark/*");
    const filenames = Object.keys(modules).map((k) => k.replace("./watermark/", ""));
    const expectedFiles = [
      "WatermarkConfigCard.tsx",
      "WatermarkResultsCard.tsx",
      "WatermarkView.tsx",
      "useWatermarkStore.ts",
    ];

    expect(filenames.sort()).toEqual(expectedFiles.sort());
  });

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
      workers: 0,
      corner: "bottom-right",
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
    expect(html).toContain("Same directory as source files");

    useWatermarkStore.getState().setInPlace(true);
    html = renderToStaticMarkup(React.createElement(WatermarkConfigCard));
    expect(html).not.toContain("Same directory as source files");
  });

  it("renders corner and workers select bindings accurately", () => {
    useWatermarkStore.getState().setCorner("top-left");
    useWatermarkStore.getState().setWorkers(8);

    const html = renderToStaticMarkup(React.createElement(WatermarkConfigCard));
    expect(html).toContain('value="top-left"');
    expect(html).toContain('value="8"');
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
        image: {
          localUrl: "C:/output/input_clean.png",
          kind: "image/png",
        },
        detected: true,
        total_count: 1,
        detected_count: 1,
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
        image: { localUrl: "res.png", kind: "image/png" },
        detected: true,
        total_count: 1_000_000,
        detected_count: 999_999,
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
        image: { localUrl: unicodePath, kind: "image/png" },
        detected: true,
        total_count: 1,
        detected_count: 1,
      },
    });

    const html = renderToStaticMarkup(React.createElement(WatermarkResultsCard));
    expect(html).toContain("tiếng_việt_có_dấu_🎨_✨.png");
  });

  it("handles missing optional fields in WatermarkItemResult without crashing", () => {
    useWatermarkStore.setState({
      result: {
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
        image: { localUrl: "single.png", kind: "image/png" },
        detected: true,
        total_count: 1,
        detected_count: 1,
        results: undefined,
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

    expect(requestSpy).toHaveBeenCalledWith(Actions.WORKFLOW_RUN_NODE, {
      type: "tool.watermark_remover",
      input: {
        image: "C:/target.png",
        images: ["C:/target.png"],
        inplace: true,
        output_dir: "",
        workers: 4,
        corner: "top-right",
      },
    });

    expect(useWatermarkStore.getState().loading).toBe(false);
    expect(useWatermarkStore.getState().result).toEqual(mockOutput);
    expect(useWatermarkStore.getState().error).toBeNull();
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
      result: { image: { localUrl: "bar.png", kind: "image/png" }, detected: true },
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
});
