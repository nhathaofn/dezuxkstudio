import { describe, expect, it } from "vitest";
import { newModel } from "./customModels";
import { enabledCounts, factsSummary, filterModels } from "./modelPresentation";

const models = [
  {
    ...newModel({ model_id: "gemini-flash", capability: "llm", endpoint: "gemini-generate" }),
    display_name: "Fast model",
  },
  {
    ...newModel({ model_id: "veo-video", capability: "video", endpoint: "gemini-generate" }),
    enabled: false,
  },
];
const labels = { seconds: "s", audio: "audio", tokens: (count: string) => `${count} tokens` };

describe("compact model presentation", () => {
  it("searches ids and display names with trimmed, case-insensitive queries", () => {
    expect(filterModels(models, " FLASH ")).toEqual([models[0]]);
    expect(filterModels(models, "fast MODEL")).toEqual([models[0]]);
    expect(filterModels(models, "video")).toEqual([models[1]]);
    expect(filterModels(models, "missing")).toEqual([]);
    expect(filterModels(models, "  ")).toEqual(models);
  });
  it("counts enabled rows in the filtered result without modifying the source", () => {
    expect(enabledCounts(models)).toEqual({ enabled: 1, total: 2 });
    expect(enabledCounts(filterModels(models, "video"))).toEqual({ enabled: 0, total: 1 });
    expect(enabledCounts([])).toEqual({ enabled: 0, total: 0 });
    expect(models[0].enabled).toBe(true);
  });
  it("summarizes video facts in duration, resolution, audio order", () => {
    expect(
      factsSummary(
        { durations: [4, 6, 8], resolutions: ["720p", "1080p"], audio: true, max_output_tokens: 0 },
        labels
      )
    ).toBe("4 / 6 / 8 s · 720p, 1080p · audio");
  });
  it("omits unknown facts and preserves precise token limits", () => {
    expect(factsSummary(models[0].facts, labels)).toBe("");
    expect(factsSummary({ ...models[0].facts, max_output_tokens: 64000 }, labels)).toBe(
      "64k tokens"
    );
    expect(factsSummary({ ...models[0].facts, max_output_tokens: 65536 }, labels)).toBe(
      "65536 tokens"
    );
  });
  it("formats summary units with translated labels", () => {
    expect(
      factsSummary(
        { durations: [8], resolutions: [], audio: true, max_output_tokens: 0 },
        { ...labels, seconds: "giây", audio: "âm thanh" }
      )
    ).toBe("8 giây · âm thanh");
  });
});
