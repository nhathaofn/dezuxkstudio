import { describe, expect, it } from "vitest";
import type { CustomEndpoint, CustomModelInput } from "../../contracts/generated";
import { reconcileModels } from "./customModels";

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
    const models: CustomModelInput[] = [
      { model_id: "a", endpoint: "openai-chat", enabled: true, is_default: true },
      { model_id: "b", endpoint: "openai-chat", enabled: true, is_default: true },
      { model_id: "c", endpoint: "openai-images", enabled: false, is_default: true },
      { model_id: "d", endpoint: "openai-images", enabled: true, is_default: true },
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
    const models: CustomModelInput[] = [
      { model_id: "a", endpoint: "openai-chat", enabled: true, is_default: true },
      { model_id: "b", endpoint: "openai-images", enabled: true, is_default: true },
    ];
    expect(reconcileModels(models, endpoints, "gemini")).toEqual([
      { ...models[0], endpoint: "gemini-generate", is_default: true },
      { ...models[1], endpoint: "gemini-generate", is_default: false },
    ]);
  });
});
