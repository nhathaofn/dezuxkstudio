import { describe, expect, it } from "vitest";
import type { CustomEndpoint, ProviderModel } from "../../contracts/generated";
import {
  mergeDiscovered,
  newModel,
  parseDurations,
  parseResolutions,
  reconcileModels,
} from "./customModels";

const endpoints: CustomEndpoint[] = [
  {
    key: "openai-chat",
    capability: "llm",
    method: "POST",
    path: "/chat/completions",
    formats: ["openai"],
  },
  {
    key: "openai-images",
    capability: "image",
    method: "POST",
    path: "/images",
    formats: ["openai"],
  },
  {
    key: "gemini-generate",
    capability: "llm",
    method: "POST",
    path: "/generateContent",
    formats: ["gemini"],
  },
];

describe("custom model reconciliation", () => {
  it("allows one enabled default per capability and clears disabled defaults", () => {
    const models: ProviderModel[] = [
      {
        ...newModel({ model_id: "a", capability: "llm", endpoint: "openai-chat" }),
        is_default: true,
      },
      {
        ...newModel({ model_id: "b", capability: "llm", endpoint: "openai-chat" }),
        is_default: true,
      },
      {
        ...newModel({ model_id: "c", capability: "image", endpoint: "openai-images" }),
        enabled: false,
        is_default: true,
      },
      {
        ...newModel({ model_id: "d", capability: "image", endpoint: "openai-images" }),
        is_default: true,
      },
    ];
    expect(reconcileModels(models, endpoints, "openai").map((model) => model.is_default)).toEqual([
      true,
      false,
      false,
      true,
    ]);
    expect(models[1].is_default).toBe(true);
  });

  it("limits endpoints to the selected protocol when switching protocols", () => {
    const models: ProviderModel[] = [
      {
        ...newModel({ model_id: "a", capability: "llm", endpoint: "openai-chat" }),
        is_default: true,
      },
      {
        ...newModel({ model_id: "b", capability: "image", endpoint: "openai-images" }),
        is_default: true,
      },
    ];
    expect(reconcileModels(models, endpoints, "gemini")).toEqual([
      { ...models[0], endpoint: "gemini-generate", is_default: true },
      { ...models[1], endpoint: "gemini-generate", capability: "llm", is_default: false },
    ]);
  });

  it("merges by id and capability without overwriting user edits or duplicating discovery rows", () => {
    const existing: ProviderModel[] = [
      {
        ...newModel({ model_id: "a", capability: "llm", endpoint: "openai-chat" }),
        display_name: "My name",
        enabled: false,
        facts: { durations: [], resolutions: [], audio: false, max_output_tokens: 900 },
      },
      {
        ...newModel({ model_id: "b", capability: "llm", endpoint: "openai-chat" }),
        is_default: true,
      },
    ];
    const discovered = [
      { model_id: "a", capability: "llm" as const, endpoint: "gemini-generate" as const },
      { model_id: "a", capability: "image" as const, endpoint: "openai-images" as const },
      { model_id: "c", capability: "llm" as const, endpoint: "openai-chat" as const },
    ];
    const merged = mergeDiscovered(existing, [...discovered, ...discovered]);
    expect(merged).toHaveLength(4);
    expect(merged.slice(0, 2)).toEqual(existing);
    expect(merged[2]).toMatchObject({
      enabled: true,
      is_default: true,
      user_added: false,
      capability: "image",
    });
    expect(merged[3].is_default).toBe(false);
    expect(existing).toHaveLength(2);
  });

  it("assigns the first enabled model of each capability when discovery has no existing defaults", () => {
    const merged = mergeDiscovered(
      [],
      [
        { model_id: "a", capability: "llm", endpoint: "openai-chat" },
        { model_id: "b", capability: "llm", endpoint: "openai-chat" },
        { model_id: "c", capability: "image", endpoint: "openai-images" },
      ]
    );
    expect(merged.map((m) => m.is_default)).toEqual([true, false, true]);
  });

  it("parses facts lists and validates each entry", () => {
    expect(parseDurations("4, 8,10")).toEqual([4, 8, 10]);
    expect(parseDurations(" ")).toEqual([]);
    for (const value of ["0", "-4", "1.5", "4,", "4,,8", "four", "Infinity"]) {
      expect(() => parseDurations(value)).toThrow();
    }
    expect(parseResolutions("720p, 1080p")).toEqual(["720p", "1080p"]);
    expect(parseResolutions("")).toEqual([]);
    expect(() => parseResolutions("720p, ")).toThrow();
  });
});
