import { describe, expect, it } from "vitest";
import { ActionError } from "../rtClient";
import { describeError, translate, translateServerMessage, type Translate } from "./index";

const vi: Translate = (key, vars) => translate("vi", key, vars);

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
    expect(describeError(vi, err)).toBe("OpenAI Codex chưa đăng nhập. Hãy bấm Đăng nhập trước.");
  });

  it("falls back to the category and server text for unknown keys", () => {
    const err = new ActionError("ERR_NOT_FOUND", "gone", { msg_key: "agent.err.fromTheFuture" });
    expect(describeError(vi, err)).toContain("gone");
    expect(translateServerMessage(vi, "agent.err.fromTheFuture", undefined)).toBeUndefined();
  });
});

describe("download progress keys", () => {
  it("translates every progress key the Go downloader emits", () => {
    expect(
      translateServerMessage(vi, "tools.progress.downloaded", { done: "1.5", total: "80.0" })
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
        translateServerMessage(vi, key, { name: "FFmpeg", done: "1", total: "2" })
      ).toBeDefined();
    }
  });
});
