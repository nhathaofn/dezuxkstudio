import type { Translate } from "./i18n";

/** Formats an uptime in seconds as "1h 5m" or "5m 3s" in the user's language. */
export function formatDuration(t: Translate, totalSeconds: number): string {
  const h = Math.floor(totalSeconds / 3600);
  const m = Math.floor((totalSeconds % 3600) / 60);
  return h > 0
    ? t("system.uptime.hm", { h, m })
    : t("system.uptime.ms", { m, s: totalSeconds % 60 });
}
