import { describe, expect, it } from "vitest";
import { toolKeys, toolLabel } from "../../features/agent/toolLabels";
import { ActionError } from "../rtClient";
import { en, type MessageKey } from "./en";
import {
  describeError,
  isMessageKey,
  translate,
  translateServerMessage,
  type Translate,
} from "./index";
import { vi as viCatalog } from "./vi";

const enT: Translate = (key, vars) => translate("en", key, vars);
const viT: Translate = (key, vars) => translate("vi", key, vars);

const goSources = import.meta.glob<string>("../../../../internal/**/*.go", {
  query: "?raw",
  import: "default",
  eager: true,
});

function extractPlaceholders(template: string): string[] {
  const matches = template.match(/\{(\w+)\}/g);
  return matches ? [...new Set(matches.map((m) => m.slice(1, -1)))].sort() : [];
}

describe("server message keys", () => {
  it("translates a keyed server error with its parameters", () => {
    const err = new ActionError(
      "ERR_UNAUTHORIZED",
      "isolated OpenAI Codex profile is not signed in",
      {
        msg_key: "agent.err.notSignedIn",
        msg_params: { name: "OpenAI Codex" },
      }
    );
    expect(describeError(viT, err)).toBe("OpenAI Codex chưa đăng nhập. Hãy bấm Đăng nhập trước.");
  });

  it("falls back to the category and server text for unknown keys", () => {
    const err = new ActionError("ERR_NOT_FOUND", "gone", { msg_key: "agent.err.fromTheFuture" });
    expect(describeError(viT, err)).toContain("gone");
    expect(translateServerMessage(viT, "agent.err.fromTheFuture", undefined)).toBeUndefined();
  });

  it("covers 100% of backend Go WithMsg, MsgKey, and progress.Update keys in both en.ts and vi.ts", () => {
    const entries = Object.entries(goSources).filter(
      ([file]) => !file.endsWith("_test.go") && !file.includes("/internal/config/")
    );
    expect(entries.length).toBeGreaterThan(50);

    const discoveredKeys = new Set<string>();
    const withMsgParamSets = new Map<string, string[][]>();
    const progressParamSets = new Map<string, string[][]>();

    const keyPattern =
      /"((?:errors|studio\.progress|studio\.qc\.issue|studio\.preflight|studio\.batch|mediagen\.progress|watermark\.progress|tools\.progress|workflow\.progress|agent\.err|agent\.errors|agent\.notice)\.[a-zA-Z0-9_.]+)"/g;

    for (const [, src] of entries) {
      for (const m of src.matchAll(keyPattern)) {
        discoveredKeys.add(m[1]);
      }

      for (const m of src.matchAll(/\.WithMsg\(\s*"([^"]+)"\s*,\s*([\s\S]*?)\)/g)) {
        const key = m[1];
        discoveredKeys.add(key);
        const arg = m[2].trim();
        const params =
          arg === "nil" ? [] : [...arg.matchAll(/"([a-zA-Z0-9_]+)"\s*:/g)].map((pm) => pm[1]);
        const list = withMsgParamSets.get(key) ?? [];
        list.push(params);
        withMsgParamSets.set(key, list);
      }

      for (const m of src.matchAll(/FFmpegPipeProgressOptions\s*\([^,]+,[^,]+,\s*"([^"]+)"/g)) {
        const key = m[1];
        discoveredKeys.add(key);
        const list = progressParamSets.get(key) ?? [];
        list.push(["sec"]);
        progressParamSets.set(key, list);
      }

      for (const m of src.matchAll(
        /\bKey\s*:\s*"((?:studio|mediagen|watermark|tools|workflow)\.progress\.[a-z0-9_.]+)"([\s\S]{0,240})/g
      )) {
        const key = m[1];
        discoveredKeys.add(key);
        const block = m[2].split("})")[0] ?? "";
        const pm = block.match(/Params\s*:\s*[^{]*\{([^}]*)\}/);
        if (pm) {
          const params = [...pm[1].matchAll(/"([a-zA-Z0-9_]+)"\s*:/g)].map((x) => x[1]);
          const list = progressParamSets.get(key) ?? [];
          list.push(params);
          progressParamSets.set(key, list);
        }
      }
    }

    expect(discoveredKeys.size).toBeGreaterThan(250);

    const missingKeys: string[] = [];
    const placeholderErrors: string[] = [];

    for (const key of [...discoveredKeys].sort()) {
      if (!isMessageKey(key) || !(key in en) || !(key in viCatalog)) {
        missingKeys.push(key);
        continue;
      }
      const typedKey = key as MessageKey;
      const enPlaceholders = extractPlaceholders(en[typedKey]);
      const viPlaceholders = extractPlaceholders(viCatalog[typedKey]);
      if (enPlaceholders.join(",") !== viPlaceholders.join(",")) {
        placeholderErrors.push(
          `${key}: en=[${enPlaceholders.join(",")}] vs vi=[${viPlaceholders.join(",")}]`
        );
      }
    }

    expect(missingKeys, `Server keys missing from en/vi: ${missingKeys.join(", ")}`).toEqual([]);
    expect(placeholderErrors).toEqual([]);

    // Verify progress keys with parameters interpolate cleanly without leaving raw {tokens}
    for (const [key, sets] of progressParamSets.entries()) {
      const merged: Record<string, string> = {};
      for (const params of sets) {
        for (const p of params) merged[p] = "1";
      }
      if (Object.keys(merged).length > 0) {
        const outEn = translateServerMessage(enT, key, merged);
        const outVi = translateServerMessage(viT, key, merged);
        expect(outEn, `Progress key ${key} should translate in en`).toBeDefined();
        expect(outVi, `Progress key ${key} should translate in vi`).toBeDefined();
        expect(outEn).not.toMatch(/\{\w+\}/);
        expect(outVi).not.toMatch(/\{\w+\}/);
      }
    }

    // Verify WithMsg keys where some call sites pass nil never leave raw {tokens} on nil params
    for (const [key, sets] of withMsgParamSets.entries()) {
      const hasNilCall = sets.some((s) => s.length === 0);
      if (hasNilCall) {
        const outVi = translateServerMessage(viT, key, undefined);
        expect(outVi, `Key ${key} called with nil params must not contain raw {token}`).not.toMatch(
          /\{\w+\}/
        );
      }
    }
  });

  it("maps every MCP tool registered in internal/service/agent to localized toolLabels in en and vi", () => {
    const agentFiles = Object.entries(goSources).filter(
      ([file]) => file.includes("/internal/service/agent/") && !file.endsWith("_test.go")
    );
    expect(agentFiles.length).toBeGreaterThan(3);

    const mcpToolNames = new Set<string>();
    for (const [, src] of agentFiles) {
      for (const m of src.matchAll(/addMCPTool[A-Za-z0-9_]*\s*\(\s*[^,]+,\s*"([^"]+)"/g)) {
        mcpToolNames.add(m[1]);
      }
    }

    expect(mcpToolNames.size).toBeGreaterThanOrEqual(50);

    const unmappedTools: string[] = [];
    for (const name of [...mcpToolNames].sort()) {
      const mappedKey = toolKeys[name];
      if (!mappedKey || !(mappedKey in en) || !(mappedKey in viCatalog)) {
        unmappedTools.push(name);
        continue;
      }
      const labelEn = toolLabel(enT, `mcp__dezuxk__${name}`);
      const labelVi = toolLabel(viT, `mcp__dezuxk__${name}`);
      expect(labelEn.length).toBeGreaterThan(0);
      expect(labelVi.length).toBeGreaterThan(0);
      expect(labelEn).not.toBe(labelVi);
    }

    expect(
      unmappedTools,
      `Unmapped MCP tools in toolLabels.ts: ${unmappedTools.join(", ")}`
    ).toEqual([]);
  });
});

describe("download progress keys", () => {
  it("translates every progress key the Go downloader emits", () => {
    expect(
      translateServerMessage(viT, "tools.progress.downloaded", { done: "1.5", total: "80.0" })
    ).toBe("Đã tải 1.5 / 80.0 MB");
    for (const key of [
      "tools.progress.connecting",
      "tools.progress.downloaded",
      "tools.progress.downloadedN",
      "tools.progress.extracting",
      "tools.progress.writing",
      "tools.progress.installed",
    ]) {
      expect(
        translateServerMessage(viT, key, { name: "FFmpeg", done: "1", total: "2" })
      ).toBeDefined();
    }
  });
});
