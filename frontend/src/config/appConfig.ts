import type { Preferences } from "../contracts/generated";

/**
 * Frontend tuning knobs. Values that the backend owns (action timeout, ping interval,
 * mode) arrive at runtime through ConnectionInfo; only client-side behaviour lives here.
 */
export const appConfig = {
  /** localStorage keys (per-viewer conveniences only — never credentials). */
  storageKeys: {
    manualWsUrl: "dezuxk.manual_ws_url",
    themeHint: "dezuxk.theme_hint",
    languageHint: "dezuxk.language_hint",
    devMode: "dezuxk.dev_mode",
  },
  /** Reconnect backoff with full jitter. */
  reconnect: {
    baseDelayMs: 1000,
    maxDelayMs: 10_000,
    factor: 1.5,
  },
  /** Used when no ConnectionInfo is available (browser thin-client mode). */
  fallback: {
    actionTimeoutMs: 30_000,
    pingIntervalMs: 30_000,
  },
  /** Preferences shown before the server's stored preferences arrive (first paint only). */
  initialPreferences: {
    theme: "light",
    language: "en",
  } satisfies Preferences,
  /** Extra time the client waits beyond the server action timeout before giving up. */
  clientTimeoutGraceMs: 2_000,
  /** How often the header latency badge pings the server. */
  latencyProbeIntervalMs: 5_000,
  /** Max entries kept in the live log view. */
  logBufferSize: 500,
  /** Browser-mode runtime config endpoint served by the core server. */
  runtimeConfigPath: "/runtime-config.json",
  /** Path of the WebSocket endpoint when deriving it from window.location. */
  wsPath: "/ws",
} as const;
