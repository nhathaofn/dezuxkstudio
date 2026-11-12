import { describe, expect, it } from "vitest";
import { isGroupOpen } from "./modelGroups";
import { showsTestDetail } from "./health";

describe("model groups", () => {
  it("are closed by default", () => {
    expect(isGroupOpen({}, "llm", false)).toBe(false);
  });
  it("open only the group the user opened", () => {
    expect(isGroupOpen({ image: true }, "image", false)).toBe(true);
    expect(isGroupOpen({ image: true }, "video", false)).toBe(false);
  });
  it("all open while searching, so a match is never hidden", () => {
    expect(isGroupOpen({}, "tts", true)).toBe(true);
  });
});

describe("test result detail", () => {
  it("hides the English detail of outcomes the app raised itself", () => {
    expect(showsTestDetail("secret_missing")).toBe(false);
    expect(showsTestDetail("invalid_credentials")).toBe(false);
  });
  it("keeps the remote error text for network and auth failures", () => {
    for (const o of [
      "auth_failed",
      "http_error",
      "network",
      "timeout",
      "rate_limited",
      "not_found",
    ])
      expect(showsTestDetail(o)).toBe(true);
  });
});
