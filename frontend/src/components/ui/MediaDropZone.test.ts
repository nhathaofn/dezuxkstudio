import { describe, expect, it } from "vitest";
import React from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { MediaDropZone } from "./MediaDropZone";

describe("MediaDropZone", () => {
  it("renders default dropzone elements in static markup", () => {
    const html = renderToStaticMarkup(
      React.createElement(MediaDropZone, {
        label: "Pick an image",
        title: "Drop your files here",
        description: "PNG, JPG up to 20MB",
      })
    );

    expect(html).toContain("watermark-dropzone");
    expect(html).toContain("Drop your files here");
    expect(html).toContain("PNG, JPG up to 20MB");
    expect(html).toContain('aria-label="Pick an image"');
  });

  it("renders selectedCount and custom selectedSummary", () => {
    const html = renderToStaticMarkup(
      React.createElement(MediaDropZone, {
        selectedCount: 4,
        selectedSummary: "4 images queued for cleaning",
      })
    );

    expect(html).toContain("4 images queued for cleaning");
  });

  it("renders disabled state with appropriate class and aria-disabled", () => {
    const html = renderToStaticMarkup(
      React.createElement(MediaDropZone, {
        disabled: true,
        title: "Disabled DropZone",
      })
    );

    expect(html).toContain("is-disabled");
    expect(html).toContain('aria-disabled="true"');
  });

  it("renders custom children when provided", () => {
    const html = renderToStaticMarkup(
      React.createElement(
        MediaDropZone,
        null,
        React.createElement("span", { id: "custom-inner" }, "Custom drop hint content")
      )
    );

    expect(html).toContain("custom-inner");
    expect(html).toContain("Custom drop hint content");
  });
});
