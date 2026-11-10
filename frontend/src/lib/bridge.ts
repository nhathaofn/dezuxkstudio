import type { BridgeApi, NativeEventMap, NativeEventName } from "../contracts/generated";

/** Shape of the globals injected by the Wails runtime (absent in a plain browser). */
interface WailsWindow {
  go?: { desktop?: { Bridge?: BridgeApi } };
  runtime?: {
    EventsOn?: (name: string, cb: (...data: unknown[]) => void) => () => void;
    EventsOff?: (name: string, ...additionalEventNames: string[]) => void;
    BrowserOpenURL?: (url: string) => void;
    OnFileDrop?: (
      callback: (x: number, y: number, paths: string[]) => void,
      useDropTarget?: boolean
    ) => void;
    OnFileDropOff?: () => void;
  };
}

const wails = (): WailsWindow =>
  typeof window !== "undefined" ? (window as unknown as WailsWindow) : {};

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

/**
 * Subscribes to native file drops (Wails OnFileDrop); a no-op outside the desktop shell.
 * Returns an unsubscribe callback to stop listening.
 */
export function onFileDrop(
  callback: (x: number, y: number, paths: string[]) => void,
  useDropTarget = false
): () => void {
  const rt = wails().runtime;
  if (!rt?.OnFileDrop || !rt?.OnFileDropOff) return () => {};
  rt.OnFileDrop(callback, useDropTarget);
  return () => {
    rt.OnFileDropOff?.();
  };
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
  else if (typeof window !== "undefined")
    window.open(parsed.toString(), "_blank", "noopener,noreferrer");
  return true;
}
