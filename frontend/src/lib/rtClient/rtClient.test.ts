import { afterEach, describe, expect, it, vi } from "vitest";
import { ErrorCodeValues } from "../../contracts/generated";
import { backoffDelay } from "./backoff";
import { resolveEndpoint, isValidWsUrl, type EndpointEnv } from "./endpoint";
import { ActionError, toActionError } from "./errors";
import { PendingRequests } from "./pending";
import { parseServerFrame } from "./protocol";

const reconnect = { baseDelayMs: 1000, maxDelayMs: 10_000, factor: 2 };

describe("backoffDelay", () => {
  it("grows exponentially and is capped", () => {
    const max = () => 0.999999;
    expect(backoffDelay(0, reconnect, max)).toBe(999);
    expect(backoffDelay(2, reconnect, max)).toBe(3999);
    expect(backoffDelay(20, reconnect, max)).toBe(9999);
  });
  it("uses full jitter down to zero", () => {
    expect(backoffDelay(5, reconnect, () => 0)).toBe(0);
  });
});

describe("PendingRequests", () => {
  afterEach(() => vi.useRealTimers());

  it("resolves the matching response and ignores unknown ids", () => {
    const p = new PendingRequests();
    const resolve = vi.fn();
    p.add("a", 1000, { resolve, reject: vi.fn() }, vi.fn());
    expect(p.complete({ type: "response", id: "zzz", success: true })).toBe(false);
    expect(p.complete({ type: "response", id: "a", success: true, data: { x: 1 } })).toBe(true);
    expect(resolve).toHaveBeenCalledWith({ x: 1 });
    expect(p.size).toBe(0);
  });

  it("maps error frames to ActionError with a known code", () => {
    const p = new PendingRequests();
    const reject = vi.fn();
    p.add("a", 1000, { resolve: vi.fn(), reject }, vi.fn());
    p.complete({
      type: "response",
      id: "a",
      success: false,
      error: { code: "ERR_NOT_FOUND", message: "x" },
    });
    expect(reject.mock.calls[0][0]).toBeInstanceOf(ActionError);
    expect(reject.mock.calls[0][0].code).toBe("ERR_NOT_FOUND");
  });

  it("times out, notifies the server and settles once", () => {
    vi.useFakeTimers();
    const p = new PendingRequests();
    const reject = vi.fn();
    const onTimeout = vi.fn();
    p.add("a", 50, { resolve: vi.fn(), reject }, onTimeout);
    vi.advanceTimersByTime(60);
    expect(onTimeout).toHaveBeenCalledOnce();
    expect(reject.mock.calls[0][0].code).toBe("ERR_TIMEOUT");
    expect(p.complete({ type: "response", id: "a", success: true })).toBe(false);
  });

  it("routes progress and rejects everything on disconnect", () => {
    const p = new PendingRequests();
    const onProgress = vi.fn();
    const reject = vi.fn();
    p.add("a", 1000, { resolve: vi.fn(), reject, onProgress }, vi.fn());
    p.progress("a", { percent: 50, message: "half" });
    expect(onProgress).toHaveBeenCalledWith({ percent: 50, message: "half" });
    p.rejectAll(new ActionError("ERR_DISCONNECTED", "gone"));
    expect(reject).toHaveBeenCalledOnce();
    expect(p.size).toBe(0);
  });
});

describe("parseServerFrame", () => {
  it("accepts responses and events", () => {
    expect(parseServerFrame('{"type":"response","id":"1","success":true}')?.type).toBe("response");
    expect(parseServerFrame('{"type":"event","event":"x","data":null,"timestamp":1}')?.type).toBe(
      "event"
    );
  });
  it("rejects malformed frames", () => {
    expect(parseServerFrame("not json")).toBeNull();
    expect(parseServerFrame('{"type":"other"}')).toBeNull();
    expect(parseServerFrame('{"type":"event","event":""}')).toBeNull();
  });
});

describe("toActionError", () => {
  it("normalises bridge error bodies, JSON strings and unknown codes", () => {
    expect(toActionError({ code: "ERR_TIMEOUT", message: "m" }).code).toBe("ERR_TIMEOUT");
    expect(toActionError('{"code":"ERR_VALIDATION","message":"m"}').code).toBe("ERR_VALIDATION");
    expect(toActionError({ code: "WHATEVER", message: "m" }).code).toBe("ERR_INTERNAL");
    expect(toActionError(new Error("boom")).message).toBe("boom");
  });
  it("covers every generated error code", () => {
    for (const code of ErrorCodeValues)
      expect(toActionError({ code, message: "" }).code).toBe(code);
  });
});

const env = (over: Partial<EndpointEnv> = {}): EndpointEnv => ({
  bridge: null,
  location: { search: "", protocol: "http:", host: "example.test" },
  storage: { getItem: () => null },
  fetchJson: () => Promise.reject(new Error("404")),
  dev: false,
  ...over,
});

describe("resolveEndpoint", () => {
  it("prefers the desktop bridge and carries the token", async () => {
    const bridge = {
      GetConnectionInfo: () =>
        Promise.resolve({
          mode: "desktop" as const,
          ws_url: "ws://127.0.0.1:1/ws",
          token: "t",
          action_timeout_ms: 10,
          ping_interval_ms: 20,
          check_updates_on_startup: true,
        }),
      GetBuildInfo: () => Promise.reject(new Error("unused")),
      SetRemoteToken: () => Promise.resolve(),
    };
    const ep = await resolveEndpoint(env({ bridge }));
    expect(ep).toMatchObject({
      url: "ws://127.0.0.1:1/ws",
      token: "t",
      source: "bridge",
      actionTimeoutMs: 10,
    });
  });

  it("re-throws bridge failures as ActionError (fatal screen)", async () => {
    const bridge = {
      GetConnectionInfo: () => Promise.reject({ code: "ERR_UNAVAILABLE", message: "db locked" }),
      GetBuildInfo: () => Promise.reject(new Error("unused")),
      SetRemoteToken: () => Promise.resolve(),
    };
    await expect(resolveEndpoint(env({ bridge }))).rejects.toMatchObject({
      code: "ERR_UNAVAILABLE",
    });
  });

  it("honours ?ws= only in dev builds", async () => {
    const location = { search: "?ws=ws://dev/ws", protocol: "http:", host: "example.test" };
    expect((await resolveEndpoint(env({ location, dev: true })))?.source).toBe("query");
    expect((await resolveEndpoint(env({ location })))?.source).toBe("location");
  });

  it("uses manual override, then runtime config, then location", async () => {
    expect((await resolveEndpoint(env({ storage: { getItem: () => "ws://m/ws" } })))?.url).toBe(
      "ws://m/ws"
    );
    const fetchJson = () => Promise.resolve({ ws_url: "ws://rc/ws" });
    expect((await resolveEndpoint(env({ fetchJson })))?.source).toBe("runtime-config");
    const https = { search: "", protocol: "https:", host: "h.test" };
    expect((await resolveEndpoint(env({ location: https })))?.url).toBe("wss://h.test/ws");
  });
});

describe("isValidWsUrl", () => {
  it("accepts ws/wss without credentials only", () => {
    expect(isValidWsUrl("wss://host:1/ws")).toBe(true);
    expect(isValidWsUrl("http://host/ws")).toBe(false);
    expect(isValidWsUrl("ws://user:pw@host/ws")).toBe(false);
    expect(isValidWsUrl("nonsense")).toBe(false);
  });
});
