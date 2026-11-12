import { describe, expect, it } from "vitest";
import type { SettingSpec } from "../../contracts/generated";
import { filterSettingSpecs, settingsDiff } from "./settings";

describe("provider settings", () => {
  it("sends only changes, including empty strings to reset stored overrides", () => {
    expect(
      settingsDiff(
        { image_rpm: 10, video_rpm: 0, image_max_workers: 4 },
        {
          image_rpm: "",
          video_rpm: "0",
          image_max_workers: " 4 ",
          request_gap_ms: "0",
          tts_max_workers: "",
        }
      )
    ).toEqual({ image_rpm: "", request_gap_ms: "0" });
    expect(settingsDiff({ image_rpm: 10 }, {})).toEqual({});
    expect(settingsDiff({}, { image_rpm: " 20 " })).toEqual({ image_rpm: "20" });
  });

  it("filters settings by capabilities while keeping universal limits", () => {
    // The backend uses an empty capability for universal specs.
    const specs = [
      { key: "request_gap_ms", capability: "", min: 0, max: 600000 },
      { key: "image_rpm", capability: "image", min: 0, max: 10000 },
      { key: "video_rpm", capability: "video", min: 0, max: 10000 },
    ] as SettingSpec[];
    expect(filterSettingSpecs(specs, ["image"]).map((s) => s.key)).toEqual([
      "request_gap_ms",
      "image_rpm",
    ]);
    expect(filterSettingSpecs(specs, []).map((s) => s.key)).toEqual(["request_gap_ms"]);
    expect(filterSettingSpecs(specs, ["image", "video"])).toEqual(specs);
  });
});
