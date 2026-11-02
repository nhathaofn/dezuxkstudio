import { describe, expect, it } from "vitest";
import {
  ErrorCodeValues,
  LanguageValues,
  type ErrorCode,
  type Language,
} from "../../contracts/generated";
import { ActionError, ClientErrorCodes } from "../rtClient/errors";
import { en, type MessageKey } from "./en";
import { vi } from "./vi";
import { isMessageKey, translate } from "./translate";
import { describeError } from "./index";

const VI_DIACRITICS_REGEX =
  /[àáảãạăằắẳẵặâầấẩẫậèéẻẽẹêềếểễệìíỉĩịòóỏõọôồốổỗộơờớởỡợùúủũụưừứửữựỳýỷỹỵđ]/i;

describe("Localization Audit (R1)", () => {
  describe("Tier 1 — Feature Coverage", () => {
    it("enforces 100% key parity between en.ts and vi.ts dictionaries", () => {
      const enKeys = Object.keys(en).sort();
      const viKeys = Object.keys(vi).sort();

      const missingInVi = enKeys.filter((k) => !(k in vi));
      const extraInVi = viKeys.filter((k) => !(k in en));

      expect(
        missingInVi,
        `Keys present in en.ts but missing in vi.ts: ${missingInVi.join(", ")}`
      ).toEqual([]);
      expect(
        extraInVi,
        `Keys present in vi.ts but missing in en.ts: ${extraInVi.join(", ")}`
      ).toEqual([]);
      expect(enKeys.length).toBe(viKeys.length);
    });

    it("verifies every translation string is non-empty and not just whitespace", () => {
      for (const [key, value] of Object.entries(en)) {
        expect(typeof value, `en key ${key} must be a string`).toBe("string");
        expect(value.trim().length, `en key ${key} has empty or whitespace value`).toBeGreaterThan(
          0
        );
      }
      for (const [key, value] of Object.entries(vi)) {
        expect(typeof value, `vi key ${key} must be a string`).toBe("string");
        expect(value.trim().length, `vi key ${key} has empty or whitespace value`).toBeGreaterThan(
          0
        );
      }
    });

    it("ensures every key in every supported language resolves via translate()", () => {
      for (const lang of LanguageValues) {
        for (const key of Object.keys(en) as MessageKey[]) {
          const result = translate(lang, key);
          expect(
            result.length,
            `${lang} translation for ${key} should not be empty`
          ).toBeGreaterThan(0);
        }
      }
    });

    it("validates placeholder variable symmetry between en and vi", () => {
      const extractPlaceholders = (tmpl?: string): string[] => {
        if (!tmpl) return [];
        const matches = tmpl.match(/\{(\w+)\}/g);
        return matches ? matches.map((m) => m.replace(/[{}]/g, "")).sort() : [];
      };

      for (const key of Object.keys(en) as MessageKey[]) {
        const enPlaceholders = extractPlaceholders(en[key]);
        const viPlaceholders = extractPlaceholders(vi[key]);
        expect(
          viPlaceholders,
          `Placeholder mismatch in key "${key}": en=[${enPlaceholders}] vs vi=[${viPlaceholders}]`
        ).toEqual(enPlaceholders);
      }
    });

    it("confirms English catalogue contains zero Vietnamese diacritics (excluding endonyms)", () => {
      const exemptKeys = new Set(["language.vi"]);
      for (const [key, text] of Object.entries(en)) {
        if (exemptKeys.has(key)) continue;
        expect(
          VI_DIACRITICS_REGEX.test(text),
          `English string for key "${key}" contains Vietnamese diacritics: "${text}"`
        ).toBe(false);
      }
    });
  });

  describe("Tier 2 — Boundary & Corner Cases", () => {
    it("preserves unresolved placeholder tokens when variables are omitted", () => {
      const res = translate("en", "update.available");
      expect(res).toContain("{version}");
    });

    it("handles zero, empty string, and numeric values in interpolations properly", () => {
      const resZero = translate("en", "logs.subtitle" as MessageKey, { max: 0 });
      expect(resZero).toContain("0");

      const resEmpty = translate("en", "update.available", { version: "" });
      expect(resEmpty).not.toContain("{version}");
      expect(resEmpty).toBe("Version  is available.");
    });

    it("safely ignores superfluous variables without altering message output", () => {
      const normal = translate("en", "update.available", { version: "1.0.0" });
      const withSuperfluous = translate("en", "update.available", {
        version: "1.0.0",
        extraFoo: "bar",
        unrelated: 12345,
      });
      expect(withSuperfluous).toBe(normal);
    });

    it("handles special characters and format-breaking strings safely", () => {
      const malicious = '<script>alert("xss")</script> & {nested} %s \\n';
      const output = translate("en", "update.available", { version: malicious });
      expect(output).toBe(`Version ${malicious} is available.`);
    });

    it("falls back to English when an uncataloged language code is requested", () => {
      const fallbackResult = translate("fr" as Language, "app.loading");
      expect(fallbackResult).toBe(en["app.loading"]);
    });
  });

  describe("Tier 3 — Cross-Feature Combinations (Error System Integration)", () => {
    it("maps all backend ErrorCodeValues to localized error strings", () => {
      for (const code of ErrorCodeValues) {
        const errorKey = `errors.${code}`;
        expect(isMessageKey(errorKey), `errors.${code} must be a registered MessageKey`).toBe(true);

        const enError = translate("en", errorKey as MessageKey);
        const viError = translate("vi", errorKey as MessageKey);
        expect(enError.length).toBeGreaterThan(0);
        expect(viError.length).toBeGreaterThan(0);
        expect(VI_DIACRITICS_REGEX.test(enError)).toBe(false);
      }
    });

    it("maps all frontend ClientErrorCodes to localized error strings", () => {
      for (const code of Object.values(ClientErrorCodes)) {
        const errorKey = `errors.${code}`;
        expect(isMessageKey(errorKey), `errors.${code} must be a registered MessageKey`).toBe(true);
        expect(translate("en", errorKey as MessageKey).length).toBeGreaterThan(0);
        expect(translate("vi", errorKey as MessageKey).length).toBeGreaterThan(0);
      }
    });

    it("formats describeError() correctly with category and detailed message", () => {
      const tEn = (k: MessageKey, vars?: Record<string, string | number>) =>
        translate("en", k, vars);
      const tVi = (k: MessageKey, vars?: Record<string, string | number>) =>
        translate("vi", k, vars);

      // 1. Error with unique message
      const errWithMsg = new ActionError("ERR_VALIDATION", "Invalid graph cycle detected");
      const expectedEnVal = translate("en", "errors.ERR_VALIDATION");
      const expectedViVal = translate("vi", "errors.ERR_VALIDATION");
      expect(describeError(tEn, errWithMsg)).toBe(
        `${expectedEnVal} (Invalid graph cycle detected)`
      );
      expect(describeError(tVi, errWithMsg)).toBe(
        `${expectedViVal} (Invalid graph cycle detected)`
      );

      // 2. Error where message equals category (no redundant parens)
      const errSameMsg = new ActionError("ERR_VALIDATION", expectedEnVal);
      expect(describeError(tEn, errSameMsg)).toBe(expectedEnVal);

      // 3. Error with unknown error code falls back to ERR_INTERNAL
      const errUnknown = new ActionError("ERR_NON_EXISTENT_CODE" as ErrorCode, "Something blew up");
      const fallbackEn = translate("en", "errors.ERR_INTERNAL");
      const fallbackVi = translate("vi", "errors.ERR_INTERNAL");
      expect(describeError(tEn, errUnknown)).toBe(`${fallbackEn} (Something blew up)`);
      expect(describeError(tVi, errUnknown)).toBe(`${fallbackVi} (Something blew up)`);
    });
  });

  describe("Tier 4 — Real-World Application Scenarios", () => {
    it("simulates full UI language switching across major application modules", () => {
      const domainKeys: MessageKey[] = [
        "nav.overview",
        "nav.watermark",
        "nav.settings",
        "settings.title",
        "settings.theme.title",
        "overview.quickActions",
        "tools.status.available",
        "tools.status.missing",
      ];

      for (const key of domainKeys) {
        const enVal = translate("en", key);
        const viVal = translate("vi", key);

        expect(enVal.length, `Domain key ${key} should have valid EN translation`).toBeGreaterThan(
          0
        );
        expect(viVal.length, `Domain key ${key} should have valid VI translation`).toBeGreaterThan(
          0
        );
        expect(enVal, `Key ${key} must have distinct translations between EN and VI`).not.toBe(
          viVal
        );
        expect(
          VI_DIACRITICS_REGEX.test(enVal),
          `EN translation for ${key} must not have VI diacritics`
        ).toBe(false);
      }
    });
  });
});
