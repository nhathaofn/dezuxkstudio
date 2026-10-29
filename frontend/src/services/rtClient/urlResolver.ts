import { RuntimeConfigSchema } from "../../schemas/realtime.schema";

const STORAGE_KEY = "dezuxk_ws_url";

export async function resolveDynamicServerUrl(): Promise<string> {
  // 1. Explicit query parameter ?ws=... (highest priority, useful for automated testing)
  const urlParams = new URLSearchParams(window.location.search);
  const paramWs = urlParams.get("ws");
  if (paramWs) return paramWs;

  // 2. Wails Native Runtime Binding (Primary truth for Desktop App - always live and dynamic)
  for (let attempt = 0; attempt < 30; attempt++) {
    const win = window as unknown as {
      go?: { main?: { App?: { GetActiveWS?: () => Promise<string> } } };
    };
    if (win.go?.main?.App?.GetActiveWS) {
      try {
        const activeWS = await win.go.main.App.GetActiveWS();
        if (activeWS && activeWS.trim() !== "") {
          return activeWS;
        }
      } catch {
        // Wails bridge still initializing
      }
    }
    await new Promise((resolve) => setTimeout(resolve, 80));
  }

  // 3. Saved manual override in localStorage (Only used for browser thin-client mode outside Wails)
  const saved = localStorage.getItem(STORAGE_KEY);
  if (saved && !saved.includes(":5173/")) {
    return saved;
  }

  // 4. Dynamic runtime config via HTTP AssetServer (/runtime-config.json)
  try {
    const res = await fetch("/runtime-config.json", { cache: "no-store" });
    if (res.ok) {
      const json = await res.json();
      const parsed = RuntimeConfigSchema.safeParse(json);
      if (parsed.success && parsed.data.ws_url) {
        return parsed.data.ws_url;
      }
    }
  } catch {
    // Ignore fetch error, proceed to fallback
  }

  // 5. Browser environment: derive dynamically from current window.location (if not dev port 5173)
  if (
    window.location.port !== "5173" &&
    (window.location.protocol === "http:" || window.location.protocol === "https:")
  ) {
    const proto = window.location.protocol === "https:" ? "wss:" : "ws:";
    return `${proto}//${window.location.host}/ws`;
  }

  return "";
}

export function saveTargetUrl(url: string): void {
  localStorage.setItem(STORAGE_KEY, url);
}

export function clearSavedTargetUrl(): void {
  localStorage.removeItem(STORAGE_KEY);
}
