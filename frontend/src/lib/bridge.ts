import type { BridgeApi, NativeEventMap, NativeEventName } from "../contracts/generated";

/** Shape of the globals injected by the Wails runtime (absent in a plain browser). */
interface WailsWindow {
  go?: { desktop?: { Bridge?: BridgeApi } };
  runtime?: {
    EventsOn?: (name: string, cb: (...data: unknown[]) => void) => () => void;
    BrowserOpenURL?: (url: string) => void;
  };
}

const wails = (): WailsWindow => window as unknown as WailsWindow;

/** Returns the desktop bridge, or null when running in a browser. */
export function getBridge(): BridgeApi | null {
  return wails().go?.desktop?.Bridge ?? null;
}

export function isDesktop(): boolean {
  return getBridge() !== null;
}

/** Subscribes to a native (Wails) event; a no-op outside the desktop shell. */
export function onNativeEvent<E extends NativeEventName>(
  name: E,
  handler: (data: NativeEventMap[E]) => void
): () => void {
  const on = wails().runtime?.EventsOn;
  if (!on) return () => {};
  return on(name, (data) => handler(data as NativeEventMap[E]));
}

/** Opens an https URL in the system browser. Other schemes are refused. */
export function openExternal(url: string): boolean {
  let parsed: URL;
  try {
    parsed = new URL(url);
  } catch {
    return false;
  }
  if (parsed.protocol !== "https:") return false;
  const open = wails().runtime?.BrowserOpenURL;
  if (open) open(parsed.toString());
  else window.open(parsed.toString(), "_blank", "noopener,noreferrer");
  return true;
}
