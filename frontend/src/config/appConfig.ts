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
    agentDrawerWidth: "dezuxk-agent-drawer-width",
  },
  /** Reconnect backoff with full jitter. */
  reconnect: {
    baseDelayMs: 1000,
    maxDelayMs: 10_000,
    factor: 1.5,
  },
  /**
   * Half-open socket detection. Browsers hide WebSocket pings, so after `idleMs` without any frame
   * from the server the client sends a system:ping; if nothing arrives within `timeoutMs` more the
   * socket is treated as dead and the client reconnects (in-flight requests fail as disconnected).
   */
  liveness: {
    checkIntervalMs: 5_000,
    idleMs: 20_000,
    timeoutMs: 10_000,
  },
  /** Used when no ConnectionInfo is available (browser thin-client mode). */
  fallback: {
    actionTimeoutMs: 30_000,
    longActionTimeoutMs: 15 * 60 * 1000,
    pingIntervalMs: 30_000,
  },
  /** Preferences shown before the server's stored preferences arrive (first paint only). */
  initialPreferences: {
    theme: "light",
    language: "en",
    workspace_root: "",
    onboarding_completed: false,
    agent_skip_trusted_approval: false,
  } satisfies Preferences,
  /** Extra time the client waits beyond the server action timeout before giving up. */
  clientTimeoutGraceMs: 2_000,
  /** Short-lived UI feedback durations. */
  ui: {
    /** Upper bound for model previews in provider connectivity results. */
    providerTestModelLimit: 10,
    /** "Copied" confirmation after copying text. */
    copyFeedbackMs: 1_500,
    /** Window in which a second click confirms a delete. */
    confirmDeleteMs: 3_000,
    /** Agent drawer width in px: default and the range the user can drag it to. */
    agentDrawer: { defaultWidth: 420, minWidth: 340, maxWidth: 680 },
    /** Height in px the chat composer grows to before it scrolls. */
    composerMaxHeight: 200,
    /** Quiet time before the current screen is sent to the agent (typing does not spam the backend). */
    screenContextDebounceMs: 300,
    /** Code block syntax highlighting, plain text fallback cap, and copy feedback. */
    codeBlock: {
      maxPlainCharacters: 20_000,
      copyFeedbackMs: 1_500,
    },
    /** Watermark comparison slider and zoom controls. */
    watermarkCompare: {
      zoomMin: 0.5,
      zoomMax: 4.0,
      zoomStep: 0.25,
    },
  },
  /** How often the header status pill pings the server. */
  latencyProbeIntervalMs: 5_000,
  /** Max entries kept in the live log view. */
  logBufferSize: 500,
  /** Client-only shortcuts; saved provider configuration remains server-owned. */
  providerPresets: [
    { name: "OpenRouter", protocol: "openai", baseUrl: "https://openrouter.ai/api/v1" },
    { name: "DeepSeek", protocol: "openai", baseUrl: "https://api.deepseek.com/v1" },
    { name: "Ollama", protocol: "openai", baseUrl: "http://localhost:11434/v1" },
    { name: "LM Studio", protocol: "openai", baseUrl: "http://localhost:1234/v1" },
    { name: "SiliconFlow", protocol: "openai", baseUrl: "https://api.siliconflow.cn/v1" },
    {
      name: "Google AI Studio-compatible",
      protocol: "gemini",
      baseUrl: "https://generativelanguage.googleapis.com",
    },
    { name: "Anthropic-compatible", protocol: "anthropic", baseUrl: "https://api.anthropic.com" },
  ],
  /** Browser-mode runtime config endpoint served by the core server. */
  runtimeConfigPath: "/runtime-config.json",
  /** Path of the WebSocket endpoint when deriving it from window.location. */
  wsPath: "/ws",
  /** Authenticated HTTP media staging upload endpoint (browser and remote client modes). */
  mediaUploadPath: "/api/media/upload",
  /** Authenticated HTTP media download endpoint (browser and remote client modes). */
  mediaDownloadPath: "/api/media/download",
  /** Media tuning knobs (ticket auto-renewal, lifetime, and staging). */
  media: {
    ticketRenewSkewMs: 120_000,
    ticketTtlMs: 900_000,
    stagingTtlMs: 24 * 60 * 60 * 1000,
    stagingGcIntervalMs: 60 * 60 * 1000,
  },
  /** External tool binaries download and retry policies. */
  binaries: {
    downloadResume: true,
    downloadRetryMax: 3,
    downloadRetryDelayMs: 1_000,
  },
  /** Watermark detection and processing engine defaults. */
  watermark: {
    maxWorkers: 4,
    maxImagePixels: 100_000_000,
    decodeConcurrency: 2,
  },
  /** Virtual list and high-performance UI rendering tuning knobs. */
  virtualization: {
    defaultItemHeight: 48,
    overscan: 5,
  },
  /** AI Film Studio client UI geometry, canvas editor, and timeline interaction bounds. */
  studio: {
    timeline: {
      defaultPxPerSec: 64,
      minPxPerSec: 24,
      maxPxPerSec: 180,
      zoomStepPx: 16,
      minDurationMs: 1000,
      minLaneWidthPx: 520,
      lanePaddingPx: 80,
      minClipWidthPx: 26,
      minSubClipWidthPx: 28,
      rulerMediumThresholdPx: 40,
      rulerDenseThresholdPx: 80,
      rulerStepCoarseSec: 4,
      rulerStepMediumSec: 2,
      rulerStepFineSec: 1,
      trimStepMs: 200,
      splitGuardEdgeMs: 250,
      subtitleDefaultDurMs: 3000,
      playSeekToleranceSec: 0.25,
      waveformWidthStepPx: 4,
      waveformHeightPx: 20,
      waveformBarWidthPx: 2,
      waveformBarMinHeightPx: 2,
      waveformBarMaxHeightPx: 18,
      nudgeStepMs: 40,
      scrubStepMs: 20,
      minSpeed: 0.25,
      maxSpeed: 4,
      maxVolume: 2,
    },
    maskEditor: {
      defaultWidthPx: 512,
      defaultHeightPx: 512,
      defaultBrushPx: 28,
      minBrushPx: 6,
      maxBrushPx: 96,
    },
    gridSplitter: {
      defaultRows: 2,
      defaultCols: 2,
      minWeightPct: 10,
      maxWeightPct: 90,
      weightTotalPct: 100,
    },
  },
} as const;
