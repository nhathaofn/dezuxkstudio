import { describe, expect, it } from "vitest";
import { en, type MessageKey } from "./en";
import { vi } from "./vi";
import { translate, isMessageKey } from "./translate";
import { describeError } from "./index";
import { ActionError } from "../rtClient/errors";
import type { Language } from "../../contracts/generated";
import { usePreferencesStore } from "../../features/preferences/usePreferencesStore";

const VI_DIACRITICS_REGEX =
  /[àáảãạăằắẳẵặâầấẩẫậèéẻẽẹêềếểễệìíỉĩịòóỏõọôồốổỗộơờớởỡợùúủũụưừứửữựỳýỷỹỵđ]/i;

describe("Dictionary Lookups & Interpolation Robustness", () => {
  describe("1.1 Uncataloged Key Behavior", () => {
    it("reports undefined when an uncataloged key is requested without vars", () => {
      // TypeScript prevents this at compile time, but at runtime dynamic keys may be passed.
      const uncataloged = "non.existent.key" as unknown as MessageKey;
      const result = translate("en", uncataloged);
      // Verify behavior: returns undefined because catalogs[lang]?.[key] ?? en[key] is undefined.
      expect(result).toBeUndefined();
    });

    it("evaluates behavior when an uncataloged key is requested with vars", () => {
      const uncataloged = "non.existent.key" as unknown as MessageKey;
      // When template is undefined, template.replace(...) throws a TypeError.
      expect(() => {
        translate("en", uncataloged, { count: 5 });
      }).toThrow(TypeError);
    });

    it("demonstrates prototype chain leakage in isMessageKey via 'in' operator", () => {
      // Due to `key in en`, Object.prototype methods return true!
      expect(isMessageKey("toString")).toBe(true);
      expect(isMessageKey("valueOf")).toBe(true);
      expect(isMessageKey("constructor")).toBe(true);
      // And when called, translate returns the prototype function rather than a string!
      const leaked = translate("en", "toString" as unknown as MessageKey);
      expect(typeof leaked).toBe("function");
      expect(leaked).toBe(Object.prototype.toString);
    });

    it("verifies isMessageKey returns false for normal non-existent keys", () => {
      expect(isMessageKey("app.loading")).toBe(true);
      expect(isMessageKey("non.existent.key")).toBe(false);
      expect(isMessageKey("")).toBe(false);
    });

    it("verifies describeError safely handles uncataloged error codes without throwing", () => {
      const t = (k: MessageKey, v?: Record<string, string | number>) => translate("en", k, v);
      const unknownErr = new ActionError(
        "TOTALLY_UNKNOWN_CODE" as unknown as import("../../contracts/generated").ErrorCode,
        "Detail failure"
      );
      const result = describeError(t, unknownErr);
      // Must fall back to ERR_INTERNAL category
      expect(result).toBe(`${en["errors.ERR_INTERNAL"]} (Detail failure)`);
    });

    it("safely falls back to English when an uncataloged language code is requested", () => {
      const result = translate("es" as Language, "watermark.workers.count", { count: 8 });
      expect(result).toBe("8 Workers");
    });
  });

  describe("1.2 Interpolation Parameter Stress-Testing", () => {
    it("preserves raw placeholder tokens when parameters are omitted", () => {
      expect(translate("en", "watermark.workers.count")).toBe("{count} Workers");
      expect(translate("vi", "watermark.workers.count")).toBe("{count} Luồng");
      expect(translate("en", "watermark.result.detectedScore")).toBe("Removed ({score}%)");
      expect(translate("vi", "watermark.result.detectedScore")).toBe("Đã xóa ({score}%)");
      expect(translate("en", "tools.browse.title")).toBe("Select executable for {name}");
      expect(translate("vi", "tools.browse.title")).toBe("Chọn tệp thực thi cho {name}");
      expect(translate("en", "system.uptime.hm")).toBe("{h}h {m}m");
      expect(translate("vi", "system.uptime.hm")).toBe("{h} giờ {m} phút");
      expect(translate("en", "system.uptime.ms")).toBe("{m}m {s}s");
      expect(translate("vi", "system.uptime.ms")).toBe("{m} phút {s} giây");
    });

    it("correctly interpolates zero (0) as a valid falsy value without dropping it", () => {
      expect(translate("en", "watermark.workers.count", { count: 0 })).toBe("0 Workers");
      expect(translate("vi", "watermark.workers.count", { count: 0 })).toBe("0 Luồng");
      expect(translate("en", "watermark.result.detectedScore", { score: 0 })).toBe("Removed (0%)");
      expect(translate("vi", "watermark.result.detectedScore", { score: 0 })).toBe("Đã xóa (0%)");
      expect(translate("en", "system.uptime.hm", { h: 0, m: 0 })).toBe("0h 0m");
      expect(translate("vi", "system.uptime.hm", { h: 0, m: 0 })).toBe("0 giờ 0 phút");
    });

    it("correctly handles empty string as interpolation parameter", () => {
      expect(translate("en", "tools.browse.title", { name: "" })).toBe("Select executable for ");
      expect(translate("vi", "tools.browse.title", { name: "" })).toBe("Chọn tệp thực thi cho ");
    });

    it("safely handles negative numbers, floats, and extreme numbers", () => {
      expect(translate("en", "watermark.workers.count", { count: -1 })).toBe("-1 Workers");
      expect(translate("en", "watermark.result.detectedScore", { score: 99.85 })).toBe(
        "Removed (99.85%)"
      );
      expect(translate("en", "watermark.badge.filesCount", { count: 1000000 })).toBe(
        "1000000 files"
      );
    });

    it("safely ignores superfluous parameters", () => {
      const normal = translate("en", "watermark.workers.count", { count: 4 });
      const extra = translate("en", "watermark.workers.count", {
        count: 4,
        extraParam: "ignored",
        nested: "payload",
      });
      expect(extra).toBe(normal);
      expect(extra).toBe("4 Workers");
    });

    it("safely handles special regex replacement patterns in variable values without pattern expansion", () => {
      // In JS String.prototype.replace(regex, string), $& is whole match, $1 is group 1, etc.
      // Because translate.ts uses a replacer function (m, name) => ..., $ tokens are NOT expanded!
      const trickyValues = ["$&", "$1", "$`", "$'", "$$$$", "{nested}"];
      for (const val of trickyValues) {
        const res = translate("en", "tools.browse.title", { name: val });
        expect(res).toBe(`Select executable for ${val}`);
      }
    });

    it("safely handles XSS and script tags as literal strings without alteration", () => {
      const xss = '<script>alert("xss")</script><img src=x onerror=alert(1)>';
      const res = translate("en", "tools.browse.title", { name: xss });
      expect(res).toBe(`Select executable for ${xss}`);
    });
  });

  describe("1.3 Prototype Pollution & Template Injection Hardening", () => {
    it("does not allow Prototype Pollution via variable parameters", () => {
      const payload = JSON.parse('{"__proto__": {"polluted": true}}');
      translate("en", "tools.browse.title", payload);
      expect((Object.prototype as Record<string, unknown>).polluted).toBeUndefined();
      expect(({} as Record<string, unknown>).polluted).toBeUndefined();
    });

    it("does not perform recursive / second-order template injection", () => {
      // If variable value contains a placeholder name that exists in vars,
      // it must NOT recursively evaluate.
      const res = translate("en", "tools.browse.title", {
        name: "{count}",
        count: 999,
      });
      // The single-pass replace evaluates {name} -> "{count}". It does NOT evaluate to "999".
      expect(res).toBe("Select executable for {count}");
    });

    it("confirms no catalog template contains JavaScript Object prototype property names", () => {
      // Since translate.ts uses `name in vars`, if a template used `{toString}`,
      // it would match Object.prototype.toString.
      const prototypeProperties = [
        "toString",
        "valueOf",
        "constructor",
        "__proto__",
        "hasOwnProperty",
        "isPrototypeOf",
        "propertyIsEnumerable",
        "toLocaleString",
      ];

      const checkCatalog = (cat: Record<string, string>, catName: string) => {
        for (const [k, tmpl] of Object.entries(cat)) {
          const matches = tmpl.match(/\{(\w+)\}/g) || [];
          for (const m of matches) {
            const varName = m.replace(/[{}]/g, "");
            expect(
              prototypeProperties.includes(varName),
              `${catName} key "${k}" uses sensitive prototype property name: "${varName}"`
            ).toBe(false);
          }
        }
      };

      checkCatalog(en, "en");
      checkCatalog(vi, "vi");
    });
  });
});

describe("Language Switching & Catalog Integrity", () => {
  it("enforces key parity between en.ts and vi.ts", () => {
    const enKeys = Object.keys(en);
    const viKeys = Object.keys(vi);

    expect(enKeys.length).toBeGreaterThan(0);
    expect(enKeys.length).toBe(viKeys.length);

    const sortedEn = [...enKeys].sort();
    const sortedVi = [...viKeys].sort();
    expect(sortedEn).toEqual(sortedVi);
  });

  it("verifies zero Vietnamese diacritics in en.ts except language.vi", () => {
    for (const [key, text] of Object.entries(en)) {
      if (key === "language.vi") {
        expect(text).toBe("Tiếng Việt");
        continue;
      }
      const hasDiacritics = VI_DIACRITICS_REGEX.test(text);
      expect(hasDiacritics, `en.ts key "${key}" contains Vietnamese diacritics: "${text}"`).toBe(
        false
      );
    }
  });

  it("verifies Vietnamese translations in vi.ts contain authentic diacritics where appropriate", () => {
    // Spot check a set of keys that MUST contain Vietnamese diacritics in vi.ts
    const vietnameseSampleKeys: MessageKey[] = [
      "header.nav.ariaLabel",
      "settings.nav.ariaLabel",
      "system.uptime.hm",
      "system.uptime.ms",
      "tools.browse.title",
      "watermark.workers.count",
      "watermark.badge.multiThreaded",
      "watermark.badge.filesCount",
      "watermark.status.ready",
      "watermark.copyPath",
      "watermark.result.detectedScore",
      "watermark.result.cleanBadge",
      "watermark.result.error",
    ];

    for (const key of vietnameseSampleKeys) {
      const viText = vi[key];
      expect(
        VI_DIACRITICS_REGEX.test(viText),
        `vi.ts key "${key}" should contain Vietnamese diacritics: "${viText}"`
      ).toBe(true);
    }
  });

  it("verifies reactive language switching updates translate outputs immediately", async () => {
    // Initial state
    const store = usePreferencesStore.getState();
    expect(["en", "vi"]).toContain(store.prefs.language);

    // Switch to en
    await usePreferencesStore.getState().update({ language: "en" });
    let currentLang = usePreferencesStore.getState().prefs.language;
    expect(currentLang).toBe("en");
    expect(translate(currentLang, "watermark.workers.count", { count: 4 })).toBe("4 Workers");
    expect(translate(currentLang, "header.nav.ariaLabel")).toBe("Main navigation");

    // Switch to vi
    await usePreferencesStore.getState().update({ language: "vi" });
    currentLang = usePreferencesStore.getState().prefs.language;
    expect(currentLang).toBe("vi");
    expect(translate(currentLang, "watermark.workers.count", { count: 4 })).toBe("4 Luồng");
    expect(translate(currentLang, "header.nav.ariaLabel")).toBe("Điều hướng chính");

    // Switch back to en
    await usePreferencesStore.getState().update({ language: "en" });
    currentLang = usePreferencesStore.getState().prefs.language;
    expect(currentLang).toBe("en");
    expect(translate(currentLang, "watermark.workers.count", { count: 4 })).toBe("4 Workers");
  });
});

describe("JSX Components Interpolation & Key Validation", () => {
  it("verifies all newly added keys in M3 exist and interpolate cleanly", () => {
    const m3Keys: Record<string, Record<string, string | number> | undefined> = {
      "header.nav.ariaLabel": undefined,
      "settings.nav.ariaLabel": undefined,
      "system.uptime.hm": { h: 2, m: 35 },
      "system.uptime.ms": { m: 15, s: 42 },
      "tools.browse.title": { name: "ffmpeg.exe" },
      "watermark.workers.count": { count: 8 },
      "watermark.badge.model": undefined,
      "watermark.badge.multiThreaded": undefined,
      "watermark.badge.filesCount": { count: 12 },
      "watermark.status.ready": undefined,
      "watermark.copyPath": undefined,
      "watermark.result.detectedScore": { score: 98 },
      "watermark.result.cleanBadge": undefined,
      "watermark.result.error": undefined,
    };

    for (const [key, vars] of Object.entries(m3Keys)) {
      expect(isMessageKey(key), `Key "${key}" must be a valid MessageKey`).toBe(true);

      const enOut = translate("en", key as MessageKey, vars);
      const viOut = translate("vi", key as MessageKey, vars);

      // Verify no unresolved placeholder tokens remain
      expect(enOut).not.toMatch(/\{(\w+)\}/);
      expect(viOut).not.toMatch(/\{(\w+)\}/);

      // Verify non-empty
      expect(enOut.length).toBeGreaterThan(0);
      expect(viOut.length).toBeGreaterThan(0);
    }
  });

  it("verifies uptime formatters produce expected localized output", () => {
    // Test hours and minutes
    const enUptimeHM = translate("en", "system.uptime.hm", { h: 3, m: 24 });
    const viUptimeHM = translate("vi", "system.uptime.hm", { h: 3, m: 24 });
    expect(enUptimeHM).toBe("3h 24m");
    expect(viUptimeHM).toBe("3 giờ 24 phút");

    // Test minutes and seconds
    const enUptimeMS = translate("en", "system.uptime.ms", { m: 4, s: 12 });
    const viUptimeMS = translate("vi", "system.uptime.ms", { m: 4, s: 12 });
    expect(enUptimeMS).toBe("4m 12s");
    expect(viUptimeMS).toBe("4 phút 12 giây");
  });
});
