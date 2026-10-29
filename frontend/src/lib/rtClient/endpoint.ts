import type { AppMode, BridgeApi } from "../../contracts/generated";
import { appConfig } from "../../config/appConfig";
import { toActionError } from "./errors";
import { RuntimeConfigSchema } from "./protocol";

/** Everything the realtime client needs to connect. */
export interface Endpoint {
  url: string;
  token: string;
  mode: AppMode | "browser";
  actionTimeoutMs: number;
  pingIntervalMs: number;
  checkUpdatesOnStartup: boolean;
  source: "bridge" | "query" | "manual" | "runtime-config" | "location";
}

/** Injected environment, so resolution is testable without a browser. */
export interface EndpointEnv {
  bridge: BridgeApi | null;
  location: Pick<Location, "search" | "protocol" | "host">;
  storage: Pick<Storage, "getItem"> | null;
  fetchJson: (path: string) => Promise<unknown>;
  dev: boolean;
}

const browserEndpoint = (url: string, source: Endpoint["source"]): Endpoint => ({
  url,
  token: "",
  mode: "browser",
  actionTimeoutMs: appConfig.fallback.actionTimeoutMs,
  pingIntervalMs: appConfig.fallback.pingIntervalMs,
  checkUpdatesOnStartup: false,
  source,
});

/**
 * Resolves the endpoint. Inside the desktop shell the bridge is the only source of
 * truth (it carries the session token); a bridge error is fatal and re-thrown.
 * In a plain browser: ?ws= (dev only) → manual override → runtime-config → location.
 */
export async function resolveEndpoint(env: EndpointEnv): Promise<Endpoint | null> {
  if (env.bridge) {
    try {
      const info = await env.bridge.GetConnectionInfo();
      return {
        url: info.ws_url,
        token: info.token,
        mode: info.mode,
        actionTimeoutMs: info.action_timeout_ms,
        pingIntervalMs: info.ping_interval_ms,
        checkUpdatesOnStartup: info.check_updates_on_startup,
        source: "bridge",
      };
    } catch (err) {
      throw toActionError(err);
    }
  }

  if (env.dev) {
    const fromQuery = new URLSearchParams(env.location.search).get("ws");
    if (fromQuery) return browserEndpoint(fromQuery, "query");
  }

  const manual = env.storage?.getItem(appConfig.storageKeys.manualWsUrl);
  if (manual) return browserEndpoint(manual, "manual");

  try {
    const parsed = RuntimeConfigSchema.safeParse(await env.fetchJson(appConfig.runtimeConfigPath));
    if (parsed.success) return browserEndpoint(parsed.data.ws_url, "runtime-config");
  } catch {
    // endpoint not served here; fall through
  }

  const { protocol, host } = env.location;
  if (!env.dev && host && (protocol === "http:" || protocol === "https:")) {
    const wsProto = protocol === "https:" ? "wss:" : "ws:";
    return browserEndpoint(`${wsProto}//${host}${appConfig.wsPath}`, "location");
  }
  return null;
}

/** Validates a user-entered WebSocket URL. */
export function isValidWsUrl(raw: string): boolean {
  try {
    const u = new URL(raw);
    return (u.protocol === "ws:" || u.protocol === "wss:") && u.host !== "" && !u.username;
  } catch {
    return false;
  }
}
