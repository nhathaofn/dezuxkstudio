import type { Theme } from "../../contracts/generated";

const DARK_QUERY = "(prefers-color-scheme: dark)";

/** Resolves "system" to the OS colour scheme. */
export function resolveTheme(theme: Theme): "light" | "dark" {
  if (theme !== "system") return theme;
  return window.matchMedia?.(DARK_QUERY).matches ? "dark" : "light";
}

let unwatch: (() => void) | null = null;

/** Applies the theme to <html data-theme> and follows OS changes while on "system". */
export function applyTheme(theme: Theme): void {
  document.documentElement.setAttribute("data-theme", resolveTheme(theme));
  unwatch?.();
  unwatch = null;
  if (theme === "system" && window.matchMedia) {
    const mq = window.matchMedia(DARK_QUERY);
    const onChange = () =>
      document.documentElement.setAttribute("data-theme", resolveTheme("system"));
    mq.addEventListener("change", onChange);
    unwatch = () => mq.removeEventListener("change", onChange);
  }
}
