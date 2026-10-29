import { describe, expect, it } from "vitest";
import { ErrorCodeValues, LanguageValues } from "../../contracts/generated";
import { ClientErrorCodes } from "../rtClient/errors";
import { en } from "./en";
import { isMessageKey, translate } from "./translate";

describe("translate", () => {
  it("interpolates placeholders and keeps unknown ones", () => {
    expect(translate("en", "update.available", { version: "1.2.3" })).toBe(
      "Version 1.2.3 is available."
    );
    expect(translate("vi", "update.available")).toContain("{version}");
  });

  it("has a non-empty translation for every key in every language", () => {
    for (const lang of LanguageValues) {
      for (const key of Object.keys(en)) {
        expect(isMessageKey(key)).toBe(true);
        if (isMessageKey(key)) expect(translate(lang, key).length).toBeGreaterThan(0);
      }
    }
  });

  it("translates every server and client error code", () => {
    for (const code of [...ErrorCodeValues, ...Object.values(ClientErrorCodes)]) {
      expect(isMessageKey(`errors.${code}`)).toBe(true);
    }
  });
});
